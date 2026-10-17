package app

import (
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

// The ui cursors are handed to the platform by value: the two enums must agree.
func TestCursorEnumsAgree(t *testing.T) {
	for _, c := range []struct {
		u ui.Cursor
		p platform.Cursor
	}{
		{ui.CursorArrow, platform.CursorArrow},
		{ui.CursorPointer, platform.CursorPointer},
		{ui.CursorText, platform.CursorText},
		{ui.CursorResizeEW, platform.CursorResizeEW},
		{ui.CursorResizeNS, platform.CursorResizeNS},
	} {
		if platformCursor(c.u) != c.p {
			t.Errorf("ui cursor %d is platform cursor %d, want %d", c.u, platformCursor(c.u), c.p)
		}
	}
}
