//go:build linux || textengine

package text

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"runtime"
	"testing"
)

// A PNG over the size limit must be rejected before the decoder allocates
// its pixels. 4097×128 is one column over the 4096 limit; decoding it would
// allocate ~2 MB, so a check made only after decoding fails the allocation
// bound below.
func TestPNGOversizeRejectedBeforeDecode(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 4097, 128))); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()

	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	_, _, _, err := pngPremultiplied(data)
	runtime.ReadMemStats(&m1)
	if err == nil {
		t.Fatal("oversized PNG accepted")
	}
	if d := m1.TotalAlloc - m0.TotalAlloc; d > 1<<20 {
		t.Fatalf("rejecting it allocated %d bytes (decoded before checking the size?)", d)
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
