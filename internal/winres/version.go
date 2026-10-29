package winres

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Version is a VERSIONINFO resource: the fixed file and product versions
// (four 16-bit parts each) and the en-US string table, in order.
type Version struct {
	File, Product [4]uint16
	// Strings are the StringFileInfo entries (CompanyName, FileDescription,
	// FileVersion, ProductName, ProductVersion, OriginalFilename, …).
	Strings []KV
}

// KV is one string of a version resource.
type KV struct{ Key, Value string }

// String returns the value of key, or "".
func (v Version) String(key string) string {
	for _, kv := range v.Strings {
		if kv.Key == key {
			return kv.Value
		}
	}
	return ""
}

// ParseQuad reads "1.2.3" or "1.2.3.4" into four parts (missing ones are 0).
func ParseQuad(s string) ([4]uint16, error) {
	var q [4]uint16
	parts := strings.Split(s, ".")
	if s == "" || len(parts) > 4 {
		return q, fmt.Errorf("winres: version %q must be one to four dotted numbers", s)
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return q, fmt.Errorf("winres: version %q: each part must be 0 to 65535", s)
		}
		q[i] = uint16(n)
	}
	return q, nil
}

// QuadString is "1.2.3.4".
func QuadString(q [4]uint16) string {
	return fmt.Sprintf("%d.%d.%d.%d", q[0], q[1], q[2], q[3])
}

// Encode is the VS_VERSIONINFO resource for v.
func (v Version) Encode() []byte {
	le := binary.LittleEndian
	fixed := make([]byte, 52)
	le.PutUint32(fixed[0:], 0xFEEF04BD) // dwSignature
	le.PutUint32(fixed[4:], 0x00010000) // dwStrucVersion
	q := func(off int, x [4]uint16) {
		le.PutUint32(fixed[off:], uint32(x[0])<<16|uint32(x[1]))
		le.PutUint32(fixed[off+4:], uint32(x[2])<<16|uint32(x[3]))
	}
	q(8, v.File)
	q(16, v.Product)
	le.PutUint32(fixed[24:], 0x3F)    // dwFileFlagsMask
	le.PutUint32(fixed[32:], 0x40004) // dwFileOS: VOS_NT_WINDOWS32
	le.PutUint32(fixed[36:], 1)       // dwFileType: VFT_APP

	var strs [][]byte
	for _, kv := range v.Strings {
		val := utf16z(kv.Value)
		strs = append(strs, node(kv.Key, val, len(val)/2, 1))
	}
	table := node("040904B0", nil, 0, 1, strs...)
	sfi := node("StringFileInfo", nil, 0, 1, table)
	trans := []byte{0x09, 0x04, 0xB0, 0x04} // en-US (0x0409), Unicode (1200)
	vfi := node("VarFileInfo", nil, 0, 1, node("Translation", trans, len(trans), 0))
	return node("VS_VERSION_INFO", fixed, len(fixed), 0, sfi, vfi)
}

// node is one version-resource block: wLength, wValueLength, wType, the key,
// padding to 4 bytes, the value, then the children, each padded to 4 bytes.
func node(key string, value []byte, valueLen int, typ uint16, children ...[]byte) []byte {
	b := make([]byte, 6)
	b = append(b, utf16z(key)...)
	b = pad4(b)
	b = append(b, value...)
	for _, c := range children {
		b = pad4(b)
		b = append(b, c...)
	}
	binary.LittleEndian.PutUint16(b[0:], uint16(len(b)))
	binary.LittleEndian.PutUint16(b[2:], uint16(valueLen))
	binary.LittleEndian.PutUint16(b[4:], typ)
	return b
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

var errVersion = errors.New("winres: a malformed version resource")

// ParseVersion decodes a VS_VERSIONINFO resource.
func ParseVersion(b []byte) (Version, error) {
	var v Version
	key, val, kids, err := block(b)
	if err != nil {
		return v, err
	}
	if key != "VS_VERSION_INFO" || len(val) < 52 || binary.LittleEndian.Uint32(val) != 0xFEEF04BD {
		return v, errVersion
	}
	le := binary.LittleEndian
	unq := func(off int) [4]uint16 {
		ms, ls := le.Uint32(val[off:]), le.Uint32(val[off+4:])
		return [4]uint16{uint16(ms >> 16), uint16(ms), uint16(ls >> 16), uint16(ls)}
	}
	v.File, v.Product = unq(8), unq(16)
	for _, c := range kids {
		k, _, tables, err := block(c)
		if err != nil {
			return v, err
		}
		if k != "StringFileInfo" {
			continue
		}
		for _, t := range tables {
			_, _, strs, err := block(t)
			if err != nil {
				return v, err
			}
			for _, s := range strs {
				k, val, _, err := block(s)
				if err != nil {
					return v, err
				}
				v.Strings = append(v.Strings, KV{k, utf16String(val)})
			}
			break // the first table
		}
	}
	return v, nil
}

// block splits one version-resource block into its key, value bytes and
// children. A text value's length is in characters; it is read up to the
// first child or the end.
func block(b []byte) (key string, value []byte, children [][]byte, err error) {
	le := binary.LittleEndian
	if len(b) < 6 {
		return "", nil, nil, errVersion
	}
	n := int(le.Uint16(b))
	if n < 6 || n > len(b) {
		return "", nil, nil, errVersion
	}
	b = b[:n]
	vlen, typ := int(le.Uint16(b[2:])), le.Uint16(b[4:])
	i := 6
	var u []uint16
	for ; i+1 < len(b); i += 2 {
		c := le.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	i += 2
	key = string(utf16.Decode(u))
	i = (i + 3) &^ 3
	if typ == 1 {
		vlen *= 2
	}
	if i+vlen > len(b) {
		if typ != 1 {
			return "", nil, nil, errVersion
		}
		vlen = max(0, len(b)-i) // some writers count bytes: take what is there
	}
	if i <= len(b) {
		value = b[i : i+vlen]
	}
	i += vlen
	for {
		i = (i + 3) &^ 3
		if i+6 > len(b) {
			break
		}
		cl := int(le.Uint16(b[i:]))
		if cl < 6 || i+cl > len(b) {
			return "", nil, nil, errVersion
		}
		children = append(children, b[i:i+cl])
		i += cl
	}
	return key, value, children, nil
}

func utf16String(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}
