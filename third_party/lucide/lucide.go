// Package lucide holds the Lucide icon set (https://lucide.dev, ISC; see
// LICENSE and VERSION beside this file) as compiled outlines, for package
// icon to rasterise. tools/lucide writes icons.bin from an upstream release;
// nothing here is edited by hand.
//
// # Format
//
// icons.bin is a DEFLATE stream (compress/flate). Inflated, it is
//
//	"LUC1"
//	u16 n                       the number of names, aliases included
//	n × { u8 len, name, u32 off, u16 size }   sorted by name
//	data                        the icons, each at off, size bytes long
//
// with every integer little-endian, and off counted from the start of data.
// An alias shares its icon's off and size. An icon is a list of elements,
// each
//
//	u8 flags                    FlagFill: filled as well as stroked
//	ops…                        u8 op, then its points
//	u8 OpEnd
//
// whose points are in 1/64 of Lucide's 24-unit grid (26.6 fixed point): OpMove
// and OpLine have one, OpQuad two, OpCubic three, OpClose none. A point is x
// and y, each the difference from the point before it in the element (from 0
// for the first) as a zigzag varint (encoding/binary.AppendVarint). Every
// element is stroked with Lucide's round caps and joins.
package lucide

import (
	"bytes"
	"compress/flate"
	_ "embed"
	"encoding/binary"
	"errors"
	"io"
	"sort"
	"sync"
)

//go:embed icons.bin
var blob []byte

// License is the set's licence (ISC, and MIT for the icons from Feather), for
// an app's About box or credits: the ISC licence asks that it go with every
// copy, and a program that draws the icons carries them.
//
//go:embed LICENSE
var License string

// The ops of an element.
const (
	OpMove  = 0
	OpLine  = 1
	OpQuad  = 2
	OpCubic = 3
	OpClose = 4
	OpEnd   = 0xFF
)

// FlagFill marks an element that is filled as well as stroked (Lucide's
// fill="currentColor", a few dots).
const FlagFill = 1

// Grid is the side of Lucide's drawing grid, in units; a point is in 1/64 of
// a unit.
const Grid = 24

// Icon is one icon's elements, in the format above.
type Icon []byte

type entry struct {
	name      string
	off, size uint32
}

var (
	once    sync.Once
	data    []byte
	entries []entry
	loadErr error
)

func load() {
	raw, err := io.ReadAll(flate.NewReader(bytes.NewReader(blob)))
	if err != nil {
		loadErr = err
		return
	}
	entries, data, loadErr = parse(raw)
}

func parse(raw []byte) ([]entry, []byte, error) {
	bad := errors.New("lucide: icons.bin is malformed")
	if len(raw) < 6 || string(raw[:4]) != "LUC1" {
		return nil, nil, bad
	}
	n := int(binary.LittleEndian.Uint16(raw[4:]))
	p := 6
	es := make([]entry, 0, n)
	for range n {
		if p >= len(raw) {
			return nil, nil, bad
		}
		l := int(raw[p])
		if p+1+l+6 > len(raw) {
			return nil, nil, bad
		}
		e := entry{name: string(raw[p+1 : p+1+l])}
		p += 1 + l
		e.off, e.size = binary.LittleEndian.Uint32(raw[p:]), uint32(binary.LittleEndian.Uint16(raw[p+4:]))
		p += 6
		es = append(es, e)
	}
	d := raw[p:]
	for _, e := range es {
		if int(e.off)+int(e.size) > len(d) {
			return nil, nil, bad
		}
	}
	return es, d, nil
}

// Lookup returns the icon named name ("search", "chevron-down", or one of
// Lucide's aliases such as "alert-triangle"). The set is inflated on the
// first call, about 0.3 MB, and kept.
func Lookup(name string) (Icon, bool) {
	once.Do(load)
	i := sort.Search(len(entries), func(i int) bool { return entries[i].name >= name })
	if i == len(entries) || entries[i].name != name {
		return nil, false
	}
	e := entries[i]
	return Icon(data[e.off : e.off+e.size : e.off+e.size]), true
}

// Names returns the names of every icon, aliases included, sorted.
func Names() []string {
	once.Do(load)
	ns := make([]string, len(entries))
	for i, e := range entries {
		ns[i] = e.name
	}
	return ns
}

// Err is the error inflating the set met, nil if it loaded (or has not been
// loaded yet).
func Err() error { return loadErr }

// Visitor receives an icon's elements from Icon.Decode.
type Visitor interface {
	// Begin starts an element; flags has FlagFill for one filled as well.
	Begin(flags byte)
	// Op is one op of the element and its points: x, y pairs in 1/64 unit of
	// the grid, absolute, in the first entries of pts (one point for OpMove
	// and OpLine, two for OpQuad, three for OpCubic, none for OpClose). The
	// points come by value so that decoding allocates nothing.
	Op(op byte, pts [6]int32)
	// End ends the element.
	End()
}

// Decode gives the elements of ic to v, and reports whether ic was well
// formed (on a malformed icon it stops where the fault is).
func (ic Icon) Decode(v Visitor) bool {
	var pts [6]int32
	p := 0
	for p < len(ic) {
		v.Begin(ic[p])
		p++
		var x, y int32
		for {
			if p >= len(ic) {
				return false
			}
			op := ic[p]
			p++
			if op == OpEnd {
				break
			}
			n := opPoints(op)
			if n < 0 {
				return false
			}
			for i := range n {
				dx, k := binary.Varint(ic[p:])
				if k <= 0 {
					return false
				}
				p += k
				dy, k := binary.Varint(ic[p:])
				if k <= 0 {
					return false
				}
				p += k
				x, y = x+int32(dx), y+int32(dy)
				pts[2*i], pts[2*i+1] = x, y
			}
			v.Op(op, pts)
		}
		v.End()
	}
	return true
}

// opPoints is the number of points op takes, -1 for an unknown op.
func opPoints(op byte) int {
	switch op {
	case OpMove, OpLine:
		return 1
	case OpQuad:
		return 2
	case OpCubic:
		return 3
	case OpClose:
		return 0
	}
	return -1
}

// Encoder builds icons.bin (tools/lucide).
type Encoder struct {
	names  map[string]int // name → index into icons
	icons  [][]byte
	cur    []byte
	lx, ly int32 // the last point of the element begun
}

// Begin starts a new element; FlagFill in flags fills it as well.
func (e *Encoder) Begin(flags byte) {
	e.cur = append(e.cur, flags)
	e.lx, e.ly = 0, 0
}

// Op adds an op and its points (x, y pairs, absolute, 1/64 unit) to the
// element begun.
func (e *Encoder) Op(op byte, pts ...int32) {
	e.cur = append(e.cur, op)
	for i := 0; i+1 < len(pts); i += 2 {
		e.cur = binary.AppendVarint(e.cur, int64(pts[i]-e.lx))
		e.cur = binary.AppendVarint(e.cur, int64(pts[i+1]-e.ly))
		e.lx, e.ly = pts[i], pts[i+1]
	}
}

// End ends the element begun.
func (e *Encoder) End() { e.cur = append(e.cur, OpEnd) }

// Icon names the elements added since the last Icon.
func (e *Encoder) Icon(name string) {
	if e.names == nil {
		e.names = map[string]int{}
	}
	e.names[name] = len(e.icons)
	e.icons = append(e.icons, e.cur)
	e.cur = nil
}

// Alias makes name another name of the icon named to, which must exist.
func (e *Encoder) Alias(name, to string) bool {
	i, ok := e.names[to]
	if ok {
		e.names[name] = i
	}
	return ok
}

// Bytes returns the inflated set (the format above, before compression).
func (e *Encoder) Bytes() ([]byte, error) {
	names := make([]string, 0, len(e.names))
	for n := range e.names {
		if len(n) > 255 {
			return nil, errors.New("lucide: name too long: " + n)
		}
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) > 0xFFFF {
		return nil, errors.New("lucide: too many icons")
	}
	offs := make([]uint32, len(e.icons))
	var d []byte
	for i, ic := range e.icons {
		if len(ic) > 0xFFFF {
			return nil, errors.New("lucide: icon too large")
		}
		offs[i] = uint32(len(d))
		d = append(d, ic...)
	}
	out := []byte("LUC1")
	out = binary.LittleEndian.AppendUint16(out, uint16(len(names)))
	for _, n := range names {
		i := e.names[n]
		out = append(out, byte(len(n)))
		out = append(out, n...)
		out = binary.LittleEndian.AppendUint32(out, offs[i])
		out = binary.LittleEndian.AppendUint16(out, uint16(len(e.icons[i])))
	}
	return append(out, d...), nil
}

// Compress deflates raw at the best compression, as icons.bin is stored.
func Compress(raw []byte) ([]byte, error) {
	var b bytes.Buffer
	w, err := flate.NewWriter(&b, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
