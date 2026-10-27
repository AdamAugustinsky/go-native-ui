package term_test

import (
	"errors"
	"testing"
	"unsafe"

	"gonativeui/term"
)

func TestCellIsEightBytes(t *testing.T) {
	if s := unsafe.Sizeof(term.Cell{}); s != 8 {
		t.Fatalf("Cell is %d bytes, D74 says 8", s)
	}
}

func TestColors(t *testing.T) {
	if c := term.Indexed(196); c.Kind() != term.ColorIndexed || c.Index() != 196 {
		t.Errorf("Indexed: %#x", c)
	}
	if r, g, b := term.RGB(1, 2, 3).RGB(); r != 1 || g != 2 || b != 3 || term.RGB(1, 2, 3).Kind() != term.ColorRGB {
		t.Error("RGB")
	}
	if term.DefaultColor.Kind() != term.ColorDefault {
		t.Error("default")
	}
}

// TestContract is the wave-0 stub: a sized terminal whose Write is
// unsupported until C1.
func TestContract(t *testing.T) {
	tm := term.New(term.Options{})
	if c, r := tm.Size(); c != term.DefaultCols || r != term.DefaultRows {
		t.Fatalf("Size = %d×%d", c, r)
	}
	if _, err := tm.Write([]byte("\x1b[1mhi")); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Write = %v", err)
	}
	tm.Resize(120, 40)
	if c, r := tm.Size(); c != 120 || r != 40 {
		t.Fatalf("Resize: %d×%d", c, r)
	}
}
