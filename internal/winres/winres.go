// Package winres writes and reads Windows resources in plain Go (D77): the
// COFF object (.syso) that go build links into a Windows executable, and a
// reader of the resources, machine type, subsystem and signature directory
// of a built PE file. It also encodes and decodes VERSIONINFO. It runs on
// every OS, so gnui package builds Windows executables from a Mac and its
// tests read them back anywhere; app/selfupdate reads an update's machine
// type and version with it.
//
// It covers what gnui writes, not every resource layout: numeric types,
// names and languages (string names are read and skipped).
package winres

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"unicode/utf16"
)

// Resource types (winuser.h).
const (
	TypeIcon      uint16 = 3  // RT_ICON
	TypeGroupIcon uint16 = 14 // RT_GROUP_ICON
	TypeVersion   uint16 = 16 // RT_VERSION
	TypeManifest  uint16 = 24 // RT_MANIFEST
)

// LangEnUS is the language gnui writes its resources in (en-US).
const LangEnUS uint16 = 0x0409

// Machine types (winnt.h IMAGE_FILE_MACHINE_*).
const (
	MachineAMD64 uint16 = 0x8664
	MachineARM64 uint16 = 0xAA64
	Machine386   uint16 = 0x014C
)

// MachineFor is the machine type of a GOARCH, or 0.
func MachineFor(goarch string) uint16 {
	switch goarch {
	case "amd64":
		return MachineAMD64
	case "arm64":
		return MachineARM64
	case "386":
		return Machine386
	}
	return 0
}

// Resource is one resource: its type, numeric name and language, and bytes.
type Resource struct {
	Type, ID, Lang uint16
	Data           []byte
}

// Find returns the first resource of type typ (and ID id, when id is not 0).
func Find(rs []Resource, typ, id uint16) (Resource, bool) {
	for _, r := range rs {
		if r.Type == typ && (id == 0 || r.ID == id) {
			return r, true
		}
	}
	return Resource{}, false
}

// COFF relocation types for a 32-bit image-relative address.
const (
	relAMD64Addr32NB = 3 // IMAGE_REL_AMD64_ADDR32NB
	relARM64Addr32NB = 2 // IMAGE_REL_ARM64_ADDR32NB
	rel386Dir32NB    = 7 // IMAGE_REL_I386_DIR32NB
)

// COFF returns a COFF object file with one .rsrc section holding rs, for
// the machine type (MachineAMD64, MachineARM64): the .syso file go build
// links into the executable when it sits in the main package's directory.
func COFF(machine uint16, rs []Resource) ([]byte, error) {
	var relType uint16
	switch machine {
	case MachineAMD64:
		relType = relAMD64Addr32NB
	case MachineARM64:
		relType = relARM64Addr32NB
	case Machine386:
		relType = rel386Dir32NB
	default:
		return nil, fmt.Errorf("winres: machine type %#x", machine)
	}
	sec, relocs, err := section(rs)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	const fileHeader, sectionHeader = 20, 40
	dataOff := fileHeader + sectionHeader
	relOff := dataOff + len(sec)
	symOff := relOff + 10*len(relocs)
	if len(relocs) > 0xFFFF {
		return nil, errors.New("winres: too many resources")
	}
	out := make([]byte, symOff+18+4)
	le.PutUint16(out[0:], machine)
	le.PutUint16(out[2:], 1) // NumberOfSections
	le.PutUint32(out[8:], uint32(symOff))
	le.PutUint32(out[12:], 1) // NumberOfSymbols
	sh := out[fileHeader:]
	copy(sh[0:8], ".rsrc")
	le.PutUint32(sh[16:], uint32(len(sec))) // SizeOfRawData
	le.PutUint32(sh[20:], uint32(dataOff))  // PointerToRawData
	le.PutUint32(sh[24:], uint32(relOff))   // PointerToRelocations
	le.PutUint16(sh[32:], uint16(len(relocs)))
	le.PutUint32(sh[36:], 0x40000040) // IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ
	copy(out[dataOff:], sec)
	for i, r := range relocs {
		p := out[relOff+10*i:]
		le.PutUint32(p[0:], r) // the field's offset in the section
		le.PutUint32(p[4:], 0) // symbol 0: the section
		le.PutUint16(p[8:], relType)
	}
	sym := out[symOff:]
	copy(sym[0:8], ".rsrc")
	le.PutUint16(sym[12:], 1) // SectionNumber
	sym[16] = 3               // IMAGE_SYM_CLASS_STATIC
	le.PutUint32(out[symOff+18:], 4)
	return out, nil
}

// section lays out the resource tree (type, then name, then language) and
// the data, and returns the offsets of the data entries' address fields,
// which need a relocation.
func section(rs []Resource) ([]byte, []uint32, error) {
	if len(rs) == 0 {
		return nil, nil, errors.New("winres: no resources")
	}
	rs = append([]Resource(nil), rs...)
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Lang < b.Lang
	})
	for i := 1; i < len(rs); i++ {
		if rs[i].Type == rs[i-1].Type && rs[i].ID == rs[i-1].ID && rs[i].Lang == rs[i-1].Lang {
			return nil, nil, fmt.Errorf("winres: duplicate resource %d/%d/%d", rs[i].Type, rs[i].ID, rs[i].Lang)
		}
	}
	// Group: types, then names per type, then languages per name.
	type name struct {
		id    uint16
		langs []int // indices into rs
	}
	type typ struct {
		id    uint16
		names []name
	}
	var types []typ
	for i, r := range rs {
		if len(types) == 0 || types[len(types)-1].id != r.Type {
			types = append(types, typ{id: r.Type})
		}
		t := &types[len(types)-1]
		if len(t.names) == 0 || t.names[len(t.names)-1].id != r.ID {
			t.names = append(t.names, name{id: r.ID})
		}
		n := &t.names[len(t.names)-1]
		n.langs = append(n.langs, i)
	}
	dirSize := func(n int) int { return 16 + 8*n }
	// Offsets: the root, the type directories, the name directories, then the
	// data entries and the data.
	off := dirSize(len(types))
	typeOff := make([]int, len(types))
	for i, t := range types {
		typeOff[i] = off
		off += dirSize(len(t.names))
	}
	nameOff := make([][]int, len(types))
	for i, t := range types {
		nameOff[i] = make([]int, len(t.names))
		for j, n := range t.names {
			nameOff[i][j] = off
			off += dirSize(len(n.langs))
		}
	}
	entryOff := off
	off += 16 * len(rs)
	dataOff := make([]int, len(rs))
	for i, r := range rs {
		off = (off + 7) &^ 7
		dataOff[i] = off
		off += len(r.Data)
	}
	off = (off + 7) &^ 7
	b := make([]byte, off)
	le := binary.LittleEndian
	dir := func(at, n int) {
		le.PutUint16(b[at+14:], uint16(n)) // NumberOfIdEntries
	}
	entry := func(dirAt, k int, id uint16, target uint32) {
		p := b[dirAt+16+8*k:]
		le.PutUint32(p[0:], uint32(id))
		le.PutUint32(p[4:], target)
	}
	const subdir = 0x80000000
	dir(0, len(types))
	var relocs []uint32
	for i, t := range types {
		entry(0, i, t.id, subdir|uint32(typeOff[i]))
		dir(typeOff[i], len(t.names))
		for j, n := range t.names {
			entry(typeOff[i], j, n.id, subdir|uint32(nameOff[i][j]))
			dir(nameOff[i][j], len(n.langs))
			for k, ri := range n.langs {
				e := entryOff + 16*ri
				entry(nameOff[i][j], k, rs[ri].Lang, uint32(e))
				le.PutUint32(b[e:], uint32(dataOff[ri]))
				le.PutUint32(b[e+4:], uint32(len(rs[ri].Data)))
				relocs = append(relocs, uint32(e))
				copy(b[dataOff[ri]:], rs[ri].Data)
			}
		}
	}
	return b, relocs, nil
}

// File is what Read finds in a PE file.
type File struct {
	Machine   uint16 // IMAGE_FILE_MACHINE_*
	Subsystem uint16 // 2 GUI (windowsgui), 3 console
	// Signed reports a non-empty certificate table (an Authenticode
	// signature is embedded; whether it is valid is the OS's question).
	Signed    bool
	Resources []Resource
}

// Subsystem values (winnt.h).
const (
	SubsystemGUI     uint16 = 2
	SubsystemConsole uint16 = 3
)

var errFormat = errors.New("winres: not a PE file")

// Read reads the headers and resources of the PE file r of size bytes.
func Read(r io.ReaderAt, size int64) (*File, error) {
	le := binary.LittleEndian
	at := func(off int64, n int) ([]byte, error) {
		if off < 0 || n < 0 || off+int64(n) > size {
			return nil, errFormat
		}
		b := make([]byte, n)
		if _, err := r.ReadAt(b, off); err != nil {
			return nil, err
		}
		return b, nil
	}
	dos, err := at(0, 64)
	if err != nil || dos[0] != 'M' || dos[1] != 'Z' {
		return nil, errFormat
	}
	peOff := int64(le.Uint32(dos[0x3C:]))
	hdr, err := at(peOff, 24)
	if err != nil || string(hdr[0:4]) != "PE\x00\x00" {
		return nil, errFormat
	}
	f := &File{Machine: le.Uint16(hdr[4:])}
	nsec := int(le.Uint16(hdr[6:]))
	optSize := int(le.Uint16(hdr[20:]))
	opt, err := at(peOff+24, optSize)
	if err != nil || optSize < 70 {
		return nil, errFormat
	}
	var ddOff, nddOff int
	switch le.Uint16(opt[0:]) {
	case 0x20B: // PE32+
		ddOff, nddOff = 112, 108
	case 0x10B: // PE32
		ddOff, nddOff = 96, 92
	default:
		return nil, errFormat
	}
	f.Subsystem = le.Uint16(opt[68:])
	if optSize < ddOff {
		return nil, errFormat
	}
	ndd := int(le.Uint32(opt[nddOff:]))
	dd := func(i int) (uint32, uint32) {
		if i >= ndd || ddOff+8*i+8 > optSize {
			return 0, 0
		}
		return le.Uint32(opt[ddOff+8*i:]), le.Uint32(opt[ddOff+8*i+4:])
	}
	if off, n := dd(4); off != 0 && n != 0 { // IMAGE_DIRECTORY_ENTRY_SECURITY: a file offset
		f.Signed = true
	}
	secs, err := at(peOff+24+int64(optSize), 40*nsec)
	if err != nil {
		return nil, errFormat
	}
	rva, rsize := dd(2) // IMAGE_DIRECTORY_ENTRY_RESOURCE
	if rva == 0 || rsize == 0 {
		return f, nil
	}
	// The section that holds the resource directory.
	for i := 0; i < nsec; i++ {
		s := secs[40*i:]
		va, vsize := le.Uint32(s[12:]), le.Uint32(s[8:])
		raw, rawOff := le.Uint32(s[16:]), le.Uint32(s[20:])
		if rva < va || rva >= va+max(vsize, raw) {
			continue
		}
		data, err := at(int64(rawOff), int(raw))
		if err != nil {
			return nil, err
		}
		rs, err := walk(data, rva-va, va)
		if err != nil {
			return nil, err
		}
		f.Resources = rs
		return f, nil
	}
	return nil, fmt.Errorf("%w: the resource directory is in no section", errFormat)
}

// walk reads the resource tree at offset root of the section data sec, whose
// virtual address is va.
func walk(sec []byte, root, va uint32) ([]Resource, error) {
	le := binary.LittleEndian
	bad := fmt.Errorf("%w: a malformed resource directory", errFormat)
	type item struct {
		id  uint32
		off uint32
	}
	read := func(off uint32) ([]item, error) {
		if int64(off)+16 > int64(len(sec)) {
			return nil, bad
		}
		n := int(le.Uint16(sec[off+12:])) + int(le.Uint16(sec[off+14:]))
		if int64(off)+16+8*int64(n) > int64(len(sec)) {
			return nil, bad
		}
		out := make([]item, n)
		for i := range out {
			p := sec[int(off)+16+8*i:]
			out[i] = item{le.Uint32(p), le.Uint32(p[4:])}
		}
		return out, nil
	}
	var out []Resource
	types, err := read(root)
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		if t.off&0x80000000 == 0 || t.id&0x80000000 != 0 {
			continue // a named type: not one of ours
		}
		names, err := read(root + t.off&0x7FFFFFFF)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if n.off&0x80000000 == 0 || n.id&0x80000000 != 0 {
				continue
			}
			langs, err := read(root + n.off&0x7FFFFFFF)
			if err != nil {
				return nil, err
			}
			for _, l := range langs {
				if l.off&0x80000000 != 0 {
					return nil, bad
				}
				e := root + l.off
				if int64(e)+16 > int64(len(sec)) {
					return nil, bad
				}
				drva, dsize := le.Uint32(sec[e:]), le.Uint32(sec[e+4:])
				if drva < va || int64(drva-va)+int64(dsize) > int64(len(sec)) {
					return nil, bad
				}
				out = append(out, Resource{Type: uint16(t.id), ID: uint16(n.id), Lang: uint16(l.id),
					Data: sec[drva-va : drva-va+dsize : drva-va+dsize]})
			}
		}
	}
	return out, nil
}

// utf16z is s as UTF-16LE bytes with a terminating NUL.
func utf16z(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u)+2)
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}
