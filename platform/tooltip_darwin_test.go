//go:build darwin

package platform

import (
	"testing"

	"gonativeui/internal/objc"
)

// askTooltip sends the owner the message AppKit sends when it shows a
// tooltip, and returns the text it answers (ok false for no tooltip).
func askTooltip(w *Window, key uint64) (string, bool) {
	var p objc.ID
	objc.F.Reset().Arg(uintptr(w.aux.tipOwner)).Arg(uintptr(selViewStringForTip)).Arg(uintptr(w.view)).Arg(1).
		ArgPoint(5, 5).Arg(uintptr(key)).Call(objc.MsgSend)
	p = objc.ID(objc.F.R0())
	if p == 0 {
		return "", false
	}
	return string(objc.AppendUTF8(nil, p)), true
}

func TestNativeTooltips(t *testing.T) {
	type result struct {
		err            error
		applies        []int
		texts          []string
		oks            []bool
		allocs         float64
		closedApplies  int
		ownerGone      bool
		asked          []uint64
		afterNoText    bool
		resizedApplies int
	}
	var r result
	onMain(func() {
		w, err := Open(Options{Title: "tooltip test", Width: 400, Height: 300, Hidden: true})
		if err != nil {
			r.err = err
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		texts := map[uint64]string{1: "Save", 2: "caf\u00e9 \U0001F600 \u65e5\u672c\u8a9e"}
		w.SetTooltipText(func(key uint64, dst []byte) []byte {
			r.asked = append(r.asked, key)
			return append(dst, texts[key]...)
		})
		regs := []TooltipRegion{{[4]float32{20, 20, 220, 120}, 1}, {[4]float32{400, 40, 600, 100}, 2}}
		step := func() { r.applies = append(r.applies, w.aux.tipApplies) }
		w.SetTooltipRegions(nil) // nothing registered, nothing to apply
		step()
		w.SetTooltipRegions(regs)
		step()
		w.SetTooltipRegions(regs) // unchanged: not registered again
		w.SetTooltipRegions(append([]TooltipRegion(nil), regs...))
		step()
		moved := append([]TooltipRegion(nil), regs...)
		moved[0].Rect[2] = 240
		w.SetTooltipRegions(moved)
		step()

		// What AppKit would be told when it shows the tooltips.
		for _, k := range []uint64{1, 2, 3} {
			s, ok := askTooltip(w, k)
			r.texts, r.oks = append(r.texts, s), append(r.oks, ok)
		}
		// Steady frames allocate nothing.
		w.SetTooltipRegions(moved)
		r.allocs = testing.AllocsPerRun(200, func() { w.SetTooltipRegions(moved) })
		// A change in the view's height (the rects count from the bottom)
		// registers them again.
		before := w.aux.tipApplies
		w.hPt += 20
		w.SetTooltipRegions(moved)
		r.resizedApplies = w.aux.tipApplies - before
		w.SetTooltipRegions(nil)
		w.SetTooltipText(nil)
		_, r.afterNoText = askTooltip(w, 1)
		w.Close()
		r.closedApplies = w.aux.tipApplies
		w.SetTooltipRegions(regs) // a closed window ignores them
		r.ownerGone = w.aux.tipOwner == 0 && w.aux.tipApplies == r.closedApplies
	})
	if r.err != nil {
		t.Skipf("cannot open a window here: %v", r.err)
	}
	if want := []int{0, 1, 1, 2}; !equalInts(r.applies, want) {
		t.Errorf("registrations after each call: %v, want %v (changes only)", r.applies, want)
	}
	if want := []string{"Save", "caf\u00e9 \U0001F600 \u65e5\u672c\u8a9e", ""}; r.texts[0] != want[0] || r.texts[1] != want[1] || r.texts[2] != want[2] {
		t.Errorf("texts %q, want %q", r.texts, want)
	}
	if want := []bool{true, true, false}; r.oks[0] != want[0] || r.oks[1] != want[1] || r.oks[2] != want[2] {
		t.Errorf("answers %v, want %v (no text: no tooltip)", r.oks, want)
	}
	if len(r.asked) < 3 || r.asked[0] != 1 || r.asked[1] != 2 || r.asked[2] != 3 {
		t.Errorf("the text function was asked for %v", r.asked)
	}
	if r.allocs != 0 {
		t.Errorf("%v allocations per steady SetTooltipRegions", r.allocs)
	}
	if r.resizedApplies != 1 {
		t.Errorf("a height change registered the rects %d times, want 1", r.resizedApplies)
	}
	if r.afterNoText {
		t.Error("a tooltip was answered without a text function")
	}
	if !r.ownerGone {
		t.Error("the closed window kept its tooltip owner or accepted regions")
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
