//go:build linux || textengine

package text

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
)

// This file lets go-text load a font without the tables we handle ourselves
// or do not support, so that font.NewFont never reads them (D9: a colour
// emoji font must not be read in whole: Noto Color Emoji's CBDT is ~10 MB,
// Apple Color Emoji's sbix 190 MB of Go heap in the phase-0 probe).
//
// go-text's opentype.Loader reads the table directory from the start of its
// Resource and then reads each table with ReadAt at the offsets listed
// there. sfntView presents a file whose directory is rewritten to omit the
// hidden tables, and passes every other read through to the real file, so
// table offsets stay valid. It also selects one font of a collection: the
// view's directory is always at offset 0.

// sfntTag is a table tag as a big-endian integer.
func sfntTag(s string) uint32 { return binary.BigEndian.Uint32([]byte(s)) }

var (
	tagCBLC = sfntTag("CBLC")
	tagCBDT = sfntTag("CBDT")
	tagTTCF = sfntTag("ttcf")
)

// hiddenTables are not loaded by go-text: colour bitmap tables (read lazily
// by cbdt.go instead), and colour/vector-colour tables that this package
// does not render (sbix, SVG, COLR/CPAL: see the package comment).
var hiddenTables = map[uint32]bool{
	sfntTag("CBLC"): true, sfntTag("CBDT"): true,
	sfntTag("EBLC"): true, sfntTag("EBDT"): true,
	sfntTag("bloc"): true, sfntTag("bdat"): true,
	sfntTag("sbix"): true, sfntTag("SVG "): true,
	sfntTag("COLR"): true, sfntTag("CPAL"): true,
}

// sfntTable is one directory record.
type sfntTable struct {
	tag, checksum, offset, length uint32
}

// sfntDir is the table directory of one font of a file.
type sfntDir struct {
	version uint32
	tables  []sfntTable
}

func (d *sfntDir) find(tag uint32) (sfntTable, bool) {
	for _, t := range d.tables {
		if t.tag == tag {
			return t, true
		}
	}
	return sfntTable{}, false
}

// readSfntDirectory reads the table directory of font number index of r (a
// single font file has index 0; collections have several).
func readSfntDirectory(r io.ReaderAt, index int) (*sfntDir, error) {
	var hdr [12]byte
	if _, err := r.ReadAt(hdr[:], 0); err != nil {
		return nil, fmt.Errorf("reading font header: %w", err)
	}
	off := int64(0)
	if binary.BigEndian.Uint32(hdr[0:]) == tagTTCF {
		n := int(binary.BigEndian.Uint32(hdr[8:]))
		if index < 0 || index >= n {
			return nil, fmt.Errorf("font index %d out of range (collection has %d)", index, n)
		}
		var o [4]byte
		if _, err := r.ReadAt(o[:], 12+4*int64(index)); err != nil {
			return nil, fmt.Errorf("reading collection offsets: %w", err)
		}
		off = int64(binary.BigEndian.Uint32(o[:]))
		if _, err := r.ReadAt(hdr[:], off); err != nil {
			return nil, fmt.Errorf("reading font header: %w", err)
		}
	} else if index != 0 {
		return nil, fmt.Errorf("font index %d in a single-font file", index)
	}
	d := &sfntDir{version: binary.BigEndian.Uint32(hdr[0:])}
	switch d.version {
	case 0x00010000, sfntTag("OTTO"), sfntTag("true"), sfntTag("typ1"):
	default:
		return nil, errors.New("not an OpenType/TrueType font")
	}
	n := int(binary.BigEndian.Uint16(hdr[4:]))
	rec := make([]byte, 16*n)
	if _, err := r.ReadAt(rec, off+12); err != nil {
		return nil, fmt.Errorf("reading table directory: %w", err)
	}
	d.tables = make([]sfntTable, n)
	for i := range d.tables {
		b := rec[16*i:]
		d.tables[i] = sfntTable{
			tag: binary.BigEndian.Uint32(b[0:]), checksum: binary.BigEndian.Uint32(b[4:]),
			offset: binary.BigEndian.Uint32(b[8:]), length: binary.BigEndian.Uint32(b[12:]),
		}
	}
	return d, nil
}

// sfntView is an io.ReaderAt/io.ReadSeeker over a font file with a rewritten
// table directory (see above).
type sfntView struct {
	f    io.ReaderAt
	dir  []byte
	size int64
	pos  int64
}

// newSfntView returns a view of the font described by d with the tables
// selected by hide removed from the directory. size is the real file size.
func newSfntView(f io.ReaderAt, size int64, d *sfntDir, hide func(tag uint32) bool) *sfntView {
	kept := make([]sfntTable, 0, len(d.tables))
	for _, t := range d.tables {
		if !hide(t.tag) {
			kept = append(kept, t)
		}
	}
	n := len(kept)
	buf := make([]byte, 12+16*n)
	binary.BigEndian.PutUint32(buf[0:], d.version)
	binary.BigEndian.PutUint16(buf[4:], uint16(n))
	if n > 0 {
		es := bits.Len(uint(n)) - 1
		sr := 16 << es
		binary.BigEndian.PutUint16(buf[6:], uint16(sr))
		binary.BigEndian.PutUint16(buf[8:], uint16(es))
		binary.BigEndian.PutUint16(buf[10:], uint16(16*n-sr))
	}
	for i, t := range kept {
		b := buf[12+16*i:]
		binary.BigEndian.PutUint32(b[0:], t.tag)
		binary.BigEndian.PutUint32(b[4:], t.checksum)
		binary.BigEndian.PutUint32(b[8:], t.offset)
		binary.BigEndian.PutUint32(b[12:], t.length)
	}
	return &sfntView{f: f, dir: buf, size: size}
}

func (v *sfntView) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	n := 0
	if d := int64(len(v.dir)); off < d {
		n = copy(p, v.dir[off:])
		if n == len(p) {
			return n, nil
		}
		off = d
	}
	m, err := v.f.ReadAt(p[n:], off)
	return n + m, err
}

func (v *sfntView) Read(p []byte) (int, error) {
	if v.pos >= v.size {
		return 0, io.EOF
	}
	n, err := v.ReadAt(p, v.pos)
	v.pos += int64(n)
	if n > 0 && err == io.EOF {
		err = nil
	}
	return n, err
}

func (v *sfntView) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += v.pos
	case io.SeekEnd:
		offset += v.size
	default:
		return 0, errors.New("invalid whence")
	}
	if offset < 0 {
		return 0, errors.New("negative position")
	}
	v.pos = offset
	return offset, nil
}

func fileSize(f *os.File) (int64, error) {
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}
