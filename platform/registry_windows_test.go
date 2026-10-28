package platform

import "testing"

// TestRegistryBookkeeping checks the wave-0 window registry types (W1):
// slots are reused, the open order is kept across a removal, lookups go by
// HWND, the wait set holds the waitables of the windows that asked for a
// frame (not those that did not) then the app's handles, and
// MaxWindows plus the app's handles fit MsgWaitForMultipleObjectsEx's 63.
func TestRegistryBookkeeping(t *testing.T) {
	if maxWaitHandles > 63 {
		t.Fatalf("%d wait handles: MsgWaitForMultipleObjectsEx takes 63 besides the queue", maxWaitHandles)
	}
	var r winRegistry
	ws := []*Window{{hwnd: 10, waitable: 100, frameArmed: true}, {hwnd: 20}, {hwnd: 30, waitable: 300, frameArmed: true}, {hwnd: 50, waitable: 500}}
	for i, w := range ws {
		if s, ok := r.add(w); !ok || s != i {
			t.Fatalf("add %d: slot %d %v", i, s, ok)
		}
	}
	r.remove(ws[0])
	if r.n != 3 || r.open[0] != ws[1] || r.open[1] != ws[2] || r.slots[0] != nil {
		t.Fatalf("after remove: n %d open %v", r.n, r.open[:r.n])
	}
	if s, _ := r.add(&Window{hwnd: 40}); s != 0 {
		t.Fatalf("a freed slot is not reused: %d", s)
	}
	if r.byHWND(30) != ws[2] || r.byHWND(10) != nil {
		t.Fatal("byHWND")
	}
	var s waitSet
	r.fill(&s, [numAppHandles]uintptr{waitWake: 7})
	if s.n != 2 || s.h[0] != 300 || s.win[0] != ws[2] || s.h[1] != 7 || s.win[1] != nil {
		t.Fatalf("wait set: %v %v", s.h[:s.n], s.win[:s.n])
	}
	full := winRegistry{}
	for i := 0; i < MaxWindows; i++ {
		full.add(&Window{})
	}
	if _, ok := full.add(&Window{}); ok {
		t.Fatal("more than MaxWindows")
	}
}
