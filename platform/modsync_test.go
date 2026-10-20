package platform

import "testing"

// TestFocusModifiers: hold a modifier, lose focus, release it elsewhere, come
// back: the app is told the modifier is released when focus goes, not left
// believing it is held; one pressed elsewhere is reported on return.
func TestFocusModifiers(t *testing.T) {
	var q eventQueue
	last := ModSuper // reported held
	focusModifiers(&q, &last, 0, 1)
	focusModifiers(&q, &last, 0, 2) // nothing changed: nothing sent
	if len(q.ev) != 1 || q.ev[0].Kind != EvModifiers || q.ev[0].Mods != 0 || last != 0 {
		t.Fatalf("losing focus while ⌘ was held: %+v, last %v", q.ev, last)
	}
	q.ev = q.ev[:0]
	focusModifiers(&q, &last, 0, 3) // back, released elsewhere
	if len(q.ev) != 0 {
		t.Errorf("returned with nothing held: %+v", q.ev)
	}
	focusModifiers(&q, &last, ModShift|ModAlt, 4) // back, pressed elsewhere
	if len(q.ev) != 1 || q.ev[0].Mods != ModShift|ModAlt || last != ModShift|ModAlt {
		t.Errorf("returned with ⇧⌥ held: %+v, last %v", q.ev, last)
	}
}
