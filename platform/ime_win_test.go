package platform

import "testing"

func TestCompositionUpdates(t *testing.T) {
	for _, c := range []struct {
		name  string
		flags uintptr
		want  bool
	}{
		{"string", gcsCompStr, true},
		{"string and cursor", gcsCompStr | gcsCursorPos, true},
		{"cursor only", gcsCursorPos, true},
		{"result only", gcsResultStr, false},
		{"nothing", 0, false},
	} {
		if got := compositionChanged(c.flags); got != c.want {
			t.Errorf("%s: compositionChanged = %v, want %v", c.name, got, c.want)
		}
	}
	comp := []byte("日本語a𠮷") // 3 BMP chars, a, then a surrogate pair in UTF-16
	for _, c := range []struct {
		flags uintptr
		cur   uintptr
		want  int
	}{
		{gcsCompStr, 0, len(comp)},         // no cursor information: the end
		{gcsCursorPos, 0, 0},               // cursor only, at the start
		{gcsCursorPos, 2, 6},               // after 日本
		{gcsCursorPos | gcsCompStr, 4, 10}, // after the a
		{gcsCursorPos, 6, len(comp)},       // after the pair (UTF-16 index 6)
		{gcsCursorPos, 99, len(comp)},      // past the end
	} {
		if got := compositionCaret(c.flags, comp, c.cur); got != c.want {
			t.Errorf("caret(flags %#x, cursor %d) = %d, want %d", c.flags, c.cur, got, c.want)
		}
	}
}

// UTF-16 units to bytes and back, the conversion both input-method paths rely
// on: surrogate pairs count two units, an offset inside a pair rounds up to
// after it, offsets past the end are clamped.
func TestUTF16ByteConversions(t *testing.T) {
	b := []byte("a𠮷é日") // units: a=1, 𠮷=2, é=1, 日=1 → 5; bytes 1+4+2+3
	for units, want := range map[uintptr]int{0: 0, 1: 1, 2: 5, 3: 5, 4: 7, 5: 10, 6: 10, 1 << 40: 10} {
		if got := u16ToByte(b, units); got != want {
			t.Errorf("u16ToByte(%d) = %d, want %d", units, got, want)
		}
	}
	if n := utf16Len(b); n != 5 {
		t.Errorf("utf16Len = %d, want 5", n)
	}
	if n := utf16Len(nil); n != 0 || u16ToByte(nil, 3) != 0 {
		t.Error("empty text")
	}
}
