//go:build darwin

package platform

import "testing"

// Every cursor shape is an NSCursor of its own, the diagonals and the hands
// included (the arrow stands in only where AppKit has no diagonal at all).
func TestEveryCursorShapeHasAnNSCursor(t *testing.T) {
	var objs [numCursors]uintptr
	var err error
	onMain(func() {
		if err = ensureApp(); err != nil {
			return
		}
		loadCursors()
		for c := range objs {
			objs[c] = uintptr(cursorObjs[c])
		}
	})
	if err != nil {
		t.Skip("no AppKit:", err)
	}
	for c := Cursor(0); c < numCursors; c++ {
		if objs[c] == 0 {
			t.Errorf("cursor %d has no NSCursor", c)
		}
		for d := Cursor(0); d < c; d++ {
			if objs[c] == objs[d] {
				t.Errorf("cursors %d and %d are the same NSCursor", d, c)
			}
		}
	}
}
