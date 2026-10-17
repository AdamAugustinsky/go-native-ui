//go:build linux || textengine

package text

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"runtime"
	"testing"
)

// A tiny PNG whose IHDR claims 100000×100000 pixels must be rejected before
// the decoder allocates the image.
func TestPNGHugeIHDRRejectedCheaply(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 100000)
	binary.BigEndian.PutUint32(ihdr[4:], 100000)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	chunk := func(typ string, data []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(data)))
		b.Write(n[:])
		b.WriteString(typ)
		b.Write(data)
		binary.BigEndian.PutUint32(n[:], crc32.ChecksumIEEE(append([]byte(typ), data...)))
		b.Write(n[:])
	}
	chunk("IHDR", ihdr)
	chunk("IEND", nil)

	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	_, _, _, err := pngPremultiplied(b.Bytes())
	runtime.ReadMemStats(&m1)
	if err == nil {
		t.Fatal("oversized PNG accepted")
	}
	if d := m1.TotalAlloc - m0.TotalAlloc; d > 1<<20 {
		t.Fatalf("rejecting it allocated %d bytes", d)
	}
}

// A table directory entry that runs past the end of the file is rejected, so
// loaders never allocate its forged length.
func TestSfntTablePastEOFRejected(t *testing.T) {
	f := make([]byte, 12+16+4)
	binary.BigEndian.PutUint32(f[0:], 0x00010000)
	binary.BigEndian.PutUint16(f[4:], 1)
	rec := f[12:]
	copy(rec[0:4], "CBLC")
	binary.BigEndian.PutUint32(rec[8:], 28)
	binary.BigEndian.PutUint32(rec[12:], 0xFFFFFFF0)
	if _, err := readSfntDirectory(bytes.NewReader(f), 0); err == nil {
		t.Fatal("table past EOF accepted")
	}
}
