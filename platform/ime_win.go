package platform

// The pure part of the Windows input-method handling, free of Windows API
// calls so the tests run everywhere.

const (
	gcsCompStr   = 0x0008 // GCS_COMPSTR
	gcsCursorPos = 0x0080 // GCS_CURSORPOS
	gcsResultStr = 0x0800 // GCS_RESULTSTR
)

// compositionChanged reports whether a WM_IME_COMPOSITION's flags say the
// composition string or its cursor changed: either one means the preedit
// has to be published again (the cursor can move alone).
func compositionChanged(flags uintptr) bool { return flags&(gcsCompStr|gcsCursorPos) != 0 }

// compositionCaret is the preedit caret, as a byte offset into comp, for an
// update with flags whose GCS_CURSORPOS value (a UTF-16 index) is cursor16;
// without GCS_CURSORPOS the caret is at the end of the composition.
func compositionCaret(flags uintptr, comp []byte, cursor16 uintptr) int {
	if flags&gcsCursorPos == 0 {
		return len(comp)
	}
	return u16ToByte(comp, cursor16)
}
