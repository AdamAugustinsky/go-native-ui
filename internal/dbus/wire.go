package dbus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

// The D-Bus wire format, little-endian only. Values are described by a
// signature (y b n q i u x t d s o g a v, structs "(...)" and dict entries
// "{kv}") and travel as Go values:
//
//	y byte   b bool    n int16   q uint16  i int32   u uint32
//	x int64  t uint64  d float64 s string   o ObjectPath   g Signature
//	v Variant   a... []any (as []string and ay []byte also accepted to send)
//	(...) Struct   {kv} DictEntry (an array of them is the dictionary)
//
// Decoding returns exactly these types, except that ay is a []byte. Every
// length read from the wire is checked against the bytes that are left, and
// nesting is limited, so a hostile message cannot make the decoder allocate
// more than a small multiple of the message size.

// ObjectPath is a D-Bus object path ("/org/freedesktop/portal/desktop").
type ObjectPath string

// Signature is a D-Bus type signature.
type Signature string

// Variant is a value with its own single complete type signature.
type Variant struct {
	Sig string
	Val any
}

// Struct is a D-Bus struct.
type Struct []any

// DictEntry is one {key value} entry; an array of them is a dictionary.
type DictEntry struct{ Key, Val any }

// Limits.
const (
	// MaxMessage is the largest message accepted from the bus (the
	// specification allows 128 MiB; the portal replies we read are tiny).
	MaxMessage = 1 << 20
	maxFields  = 1 << 16 // header field array, as in the specification
	maxDepth   = 32      // nesting of arrays, structs and variants
	maxSigLen  = 255
)

// V returns v as a Variant with the signature its Go type implies (basic
// types only); use a Variant literal for containers.
func V(v any) Variant {
	switch v.(type) {
	case byte:
		return Variant{"y", v}
	case bool:
		return Variant{"b", v}
	case int16:
		return Variant{"n", v}
	case uint16:
		return Variant{"q", v}
	case int32:
		return Variant{"i", v}
	case uint32:
		return Variant{"u", v}
	case int64:
		return Variant{"x", v}
	case uint64:
		return Variant{"t", v}
	case float64:
		return Variant{"d", v}
	case string:
		return Variant{"s", v}
	case ObjectPath:
		return Variant{"o", v}
	case Signature:
		return Variant{"g", v}
	case Variant:
		return Variant{"v", v}
	}
	panic(fmt.Sprintf("dbus: no signature for %T", v))
}

var errSig = errors.New("dbus: invalid signature")

// typeLen returns the length of the first complete type in s.
func typeLen(s string) (int, error) { return typeLenDepth(s, 0, 0) }

func typeLenDepth(s string, arr, st int) (int, error) {
	if len(s) == 0 || arr > maxDepth || st > maxDepth {
		return 0, errSig
	}
	switch s[0] {
	case 'y', 'b', 'n', 'q', 'i', 'u', 'x', 't', 'd', 's', 'o', 'g', 'v':
		return 1, nil
	case 'a':
		if len(s) > 1 && s[1] == '{' { // a dict entry is only valid as an array element
			n, err := dictEntryLen(s[1:], arr+1, st)
			return n + 1, err
		}
		n, err := typeLenDepth(s[1:], arr+1, st)
		return n + 1, err
	case '(':
		i := 1
		for {
			if i >= len(s) {
				return 0, errSig
			}
			if s[i] == ')' {
				if i == 1 {
					return 0, errSig // empty struct
				}
				return i + 1, nil
			}
			n, err := typeLenDepth(s[i:], arr, st+1)
			if err != nil {
				return 0, err
			}
			i += n
		}
	}
	return 0, errSig
}

// dictEntryLen is the length of "{kv}" at the start of s.
func dictEntryLen(s string, arr, st int) (int, error) {
	if len(s) < 4 || s[0] != '{' || !isBasic(s[1]) {
		return 0, errSig
	}
	n, err := typeLenDepth(s[2:], arr, st+1)
	if err != nil || len(s) < 2+n+1 || s[2+n] != '}' {
		return 0, errSig
	}
	return 2 + n + 1, nil
}

func isBasic(c byte) bool {
	switch c {
	case 'y', 'b', 'n', 'q', 'i', 'u', 'x', 't', 'd', 's', 'o', 'g':
		return true
	}
	return false
}

// validSignature reports whether s is a sequence of complete types (at most
// 255 bytes).
func validSignature(s string) bool {
	if len(s) > maxSigLen {
		return false
	}
	for len(s) > 0 {
		n, err := typeLen(s)
		if err != nil {
			return false
		}
		s = s[n:]
	}
	return true
}

func alignOf(c byte) int {
	switch c {
	case 'y', 'g', 'v':
		return 1
	case 'n', 'q':
		return 2
	case 'b', 'i', 'u', 's', 'o', 'a':
		return 4
	}
	return 8 // x t d ( {
}

// ---- encoding -----------------------------------------------------------------

type encoder struct{ b []byte }

func (e *encoder) align(n int) {
	for len(e.b)%n != 0 {
		e.b = append(e.b, 0)
	}
}

func (e *encoder) u32(v uint32) {
	e.align(4)
	e.b = binary.LittleEndian.AppendUint32(e.b, v)
}

func (e *encoder) str(s string) error {
	if !utf8.ValidString(s) {
		return errors.New("dbus: string is not valid UTF-8")
	}
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return errors.New("dbus: string contains NUL")
		}
	}
	e.u32(uint32(len(s)))
	e.b = append(e.b, s...)
	e.b = append(e.b, 0)
	return nil
}

func (e *encoder) sig(s string) error {
	if !validSignature(s) {
		return errSig
	}
	e.b = append(e.b, byte(len(s)))
	e.b = append(e.b, s...)
	e.b = append(e.b, 0)
	return nil
}

// values encodes args according to sig (a sequence of complete types).
func (e *encoder) values(sig string, args []any) error {
	for len(sig) > 0 {
		if len(args) == 0 {
			return errors.New("dbus: fewer arguments than the signature has types")
		}
		n, err := typeLen(sig)
		if err != nil {
			return err
		}
		if err := e.one(sig[:n], args[0], 0); err != nil {
			return err
		}
		sig, args = sig[n:], args[1:]
	}
	if len(args) != 0 {
		return errors.New("dbus: more arguments than the signature has types")
	}
	return nil
}

func badType(sig string, v any) error {
	return fmt.Errorf("dbus: %T does not fit signature %q", v, sig)
}

// one encodes v as the single complete type sig.
func (e *encoder) one(sig string, v any, depth int) error {
	if depth > maxDepth {
		return errors.New("dbus: nesting too deep")
	}
	switch sig[0] {
	case 'y':
		x, ok := v.(byte)
		if !ok {
			return badType(sig, v)
		}
		e.b = append(e.b, x)
	case 'b':
		x, ok := v.(bool)
		if !ok {
			return badType(sig, v)
		}
		if x {
			e.u32(1)
		} else {
			e.u32(0)
		}
	case 'n':
		x, ok := v.(int16)
		if !ok {
			return badType(sig, v)
		}
		e.align(2)
		e.b = binary.LittleEndian.AppendUint16(e.b, uint16(x))
	case 'q':
		x, ok := v.(uint16)
		if !ok {
			return badType(sig, v)
		}
		e.align(2)
		e.b = binary.LittleEndian.AppendUint16(e.b, x)
	case 'i':
		x, ok := v.(int32)
		if !ok {
			return badType(sig, v)
		}
		e.u32(uint32(x))
	case 'u':
		x, ok := v.(uint32)
		if !ok {
			return badType(sig, v)
		}
		e.u32(x)
	case 'x':
		x, ok := v.(int64)
		if !ok {
			return badType(sig, v)
		}
		e.align(8)
		e.b = binary.LittleEndian.AppendUint64(e.b, uint64(x))
	case 't':
		x, ok := v.(uint64)
		if !ok {
			return badType(sig, v)
		}
		e.align(8)
		e.b = binary.LittleEndian.AppendUint64(e.b, x)
	case 'd':
		x, ok := v.(float64)
		if !ok {
			return badType(sig, v)
		}
		e.align(8)
		e.b = binary.LittleEndian.AppendUint64(e.b, math.Float64bits(x))
	case 's':
		x, ok := v.(string)
		if !ok {
			return badType(sig, v)
		}
		return e.str(x)
	case 'o':
		var x string
		switch p := v.(type) {
		case ObjectPath:
			x = string(p)
		case string:
			x = p
		default:
			return badType(sig, v)
		}
		if !validPath(x) {
			return fmt.Errorf("dbus: invalid object path %q", x)
		}
		return e.str(x)
	case 'g':
		var x string
		switch p := v.(type) {
		case Signature:
			x = string(p)
		case string:
			x = p
		default:
			return badType(sig, v)
		}
		return e.sig(x)
	case 'v':
		x, ok := v.(Variant)
		if !ok {
			return badType(sig, v)
		}
		n, err := typeLen(x.Sig)
		if err != nil || n != len(x.Sig) {
			return errSig
		}
		if err := e.sig(x.Sig); err != nil {
			return err
		}
		return e.one(x.Sig, x.Val, depth+1)
	case 'a':
		elem := sig[1:]
		e.u32(0) // the length, patched below
		lenAt := len(e.b) - 4
		e.align(alignOf(elem[0]))
		start := len(e.b)
		switch xs := v.(type) {
		case []any:
			for _, x := range xs {
				if err := e.one(elem, x, depth+1); err != nil {
					return err
				}
			}
		case []string:
			if elem != "s" && elem != "o" && elem != "g" {
				return badType(sig, v)
			}
			for _, x := range xs {
				if err := e.one(elem, x, depth+1); err != nil {
					return err
				}
			}
		case []byte:
			if elem != "y" {
				return badType(sig, v)
			}
			e.b = append(e.b, xs...)
		default:
			return badType(sig, v)
		}
		n := len(e.b) - start
		if n > MaxMessage {
			return errors.New("dbus: array too large")
		}
		binary.LittleEndian.PutUint32(e.b[lenAt:], uint32(n))
	case '(':
		e.align(8)
		var xs []any
		switch s := v.(type) {
		case Struct:
			xs = s
		case []any:
			xs = s
		default:
			return badType(sig, v)
		}
		return e.members(sig[1:len(sig)-1], xs, depth+1)
	case '{':
		x, ok := v.(DictEntry)
		if !ok {
			return badType(sig, v)
		}
		e.align(8)
		return e.members(sig[1:len(sig)-1], []any{x.Key, x.Val}, depth+1)
	}
	return nil
}

// members encodes the members of a struct or dict entry.
func (e *encoder) members(sig string, xs []any, depth int) error {
	if len(sig) == 0 {
		return errSig
	}
	for len(sig) > 0 {
		if len(xs) == 0 {
			return errors.New("dbus: struct has fewer values than members")
		}
		n, err := typeLen(sig)
		if err != nil {
			return err
		}
		if err := e.one(sig[:n], xs[0], depth); err != nil {
			return err
		}
		sig, xs = sig[n:], xs[1:]
	}
	if len(xs) != 0 {
		return errors.New("dbus: struct has more values than members")
	}
	return nil
}

// validPath reports whether s (a string or []byte) is a valid object path.
func validPath[T ~string | ~[]byte](s T) bool {
	if len(s) == 1 && s[0] == '/' {
		return true
	}
	if len(s) < 2 || s[0] != '/' || s[len(s)-1] == '/' {
		return false
	}
	prev := byte('/')
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '/':
			if prev == '/' {
				return false
			}
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
		prev = c
	}
	return true
}

// ---- decoding -----------------------------------------------------------------

var errShort = errors.New("dbus: message truncated")

type decoder struct {
	b   []byte
	off int
}

func (d *decoder) align(n int) error {
	for d.off%n != 0 {
		if d.off >= len(d.b) || d.b[d.off] != 0 {
			return errors.New("dbus: bad padding")
		}
		d.off++
	}
	return nil
}

func (d *decoder) take(n int) ([]byte, error) {
	if n < 0 || n > len(d.b)-d.off {
		return nil, errShort
	}
	p := d.b[d.off : d.off+n]
	d.off += n
	return p, nil
}

func (d *decoder) u32() (uint32, error) {
	if err := d.align(4); err != nil {
		return 0, err
	}
	p, err := d.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(p), nil
}

func (d *decoder) str() (string, error) {
	n, err := d.u32()
	if err != nil {
		return "", err
	}
	if int64(n) > int64(len(d.b)-d.off) {
		return "", errShort
	}
	p, _ := d.take(int(n))
	nul, err := d.take(1)
	if err != nil {
		return "", err
	}
	if nul[0] != 0 {
		return "", errors.New("dbus: string is not NUL-terminated")
	}
	s := string(p)
	if !utf8.ValidString(s) {
		return "", errors.New("dbus: string is not valid UTF-8")
	}
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return "", errors.New("dbus: string contains NUL")
		}
	}
	return s, nil
}

func (d *decoder) sig() (string, error) {
	p, err := d.take(1)
	if err != nil {
		return "", err
	}
	q, err := d.take(int(p[0]))
	if err != nil {
		return "", err
	}
	nul, err := d.take(1)
	if err != nil {
		return "", err
	}
	if nul[0] != 0 {
		return "", errors.New("dbus: signature is not NUL-terminated")
	}
	s := string(q)
	if !validSignature(s) {
		return "", errSig
	}
	return s, nil
}

// values decodes one value per complete type of sig.
func (d *decoder) values(sig string) ([]any, error) {
	var out []any
	for len(sig) > 0 {
		n, err := typeLen(sig)
		if err != nil {
			return nil, err
		}
		v, err := d.one(sig[:n], 0)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		sig = sig[n:]
	}
	return out, nil
}

func (d *decoder) one(sig string, depth int) (any, error) {
	if depth > maxDepth {
		return nil, errors.New("dbus: nesting too deep")
	}
	c := sig[0]
	if err := d.align(alignOf(c)); err != nil {
		return nil, err
	}
	switch c {
	case 'y':
		p, err := d.take(1)
		if err != nil {
			return nil, err
		}
		return p[0], nil
	case 'b':
		x, err := d.u32()
		if err != nil {
			return nil, err
		}
		if x > 1 {
			return nil, errors.New("dbus: invalid boolean")
		}
		return x == 1, nil
	case 'n', 'q':
		p, err := d.take(2)
		if err != nil {
			return nil, err
		}
		x := binary.LittleEndian.Uint16(p)
		if c == 'n' {
			return int16(x), nil
		}
		return x, nil
	case 'i':
		x, err := d.u32()
		return int32(x), err
	case 'u':
		return d.u32()
	case 'x', 't', 'd':
		p, err := d.take(8)
		if err != nil {
			return nil, err
		}
		x := binary.LittleEndian.Uint64(p)
		switch c {
		case 'x':
			return int64(x), nil
		case 't':
			return x, nil
		}
		return math.Float64frombits(x), nil
	case 's':
		return d.str()
	case 'o':
		s, err := d.str()
		if err == nil && !validPath(s) {
			err = fmt.Errorf("dbus: invalid object path %q", s)
		}
		return ObjectPath(s), err
	case 'g':
		s, err := d.sig()
		return Signature(s), err
	case 'v':
		s, err := d.sig()
		if err != nil {
			return nil, err
		}
		if n, err := typeLen(s); err != nil || n != len(s) {
			return nil, errSig
		}
		v, err := d.one(s, depth+1)
		return Variant{s, v}, err
	case 'a':
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		elem := sig[1:]
		if err := d.align(alignOf(elem[0])); err != nil {
			return nil, err
		}
		if int64(n) > int64(len(d.b)-d.off) || n > MaxMessage {
			return nil, errShort
		}
		end := d.off + int(n)
		if elem == "y" {
			p, _ := d.take(int(n))
			return append([]byte{}, p...), nil
		}
		out := []any{}
		for d.off < end {
			v, err := d.one(elem, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			if d.off > end {
				return nil, errors.New("dbus: array element overruns the array")
			}
		}
		return out, nil
	case '(':
		var out Struct
		inner := sig[1 : len(sig)-1]
		for len(inner) > 0 {
			n, _ := typeLen(inner)
			v, err := d.one(inner[:n], depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			inner = inner[n:]
		}
		return out, nil
	case '{':
		kn, _ := typeLen(sig[1:])
		k, err := d.one(sig[1:1+kn], depth+1)
		if err != nil {
			return nil, err
		}
		v, err := d.one(sig[1+kn:len(sig)-1], depth+1)
		return DictEntry{k, v}, err
	}
	return nil, errSig
}
