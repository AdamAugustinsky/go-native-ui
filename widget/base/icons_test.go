package base

import (
	"testing"

	"gonativeui/icon"
)

// An icon added after the first frame gets the next ID, draws from the next
// frame on, and the icons before it keep their IDs and sizes, also across a
// change of scale.
func TestAddIconAfterTheFirstFrame(t *testing.T) {
	g := newRig(t, 200, 100, 1, func(k *Kit) {})
	at := g.k.Icons()
	n := at.Len()
	w0, h0 := at.Size(icon.ChevronLeft)
	first := g.k.AddIcon(icon.Spec{Symbol: "star", Fallback: '★', Pt: 20})
	second := g.k.AddIcon(icon.Spec{Symbol: "heart", Fallback: '♥', Pt: 20})
	if first != icon.NumStandard || second != first+1 || at.Len() != n+2 {
		t.Fatalf("IDs %d, %d and %d icons, want %d, %d and %d", first, second, at.Len(), icon.NumStandard, icon.NumStandard+1, n+2)
	}
	g.frames(2)
	for _, id := range []icon.ID{first, second} {
		if w, h := at.Size(id); w == 0 || h == 0 || at.Source(id) == icon.SourceNone {
			t.Errorf("icon %d draws nothing after it was added (size %vx%v)", id, w, h)
		}
	}
	if w, h := at.Size(icon.ChevronLeft); w != w0 || h != h0 {
		t.Errorf("an icon from before changed from %vx%v to %vx%v", w0, h0, w, h)
	}
	g.scale = 2
	g.frames(2)
	if w, _ := at.Size(first); w == 0 {
		t.Error("the added icon is gone after a change of scale")
	}
}
