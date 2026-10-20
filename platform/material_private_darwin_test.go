//go:build darwin && !gnui_publicapi

package platform

import (
	"testing"

	"gonativeui/internal/objc"
)

// glassVariant is glass view v's private variant, false where it has none.
func glassVariant(v objc.ID) (int64, bool) {
	if !objc.SendBool1(v, selRespondsTo, uintptr(selVariant)) {
		return 0, false
	}
	return int64(objc.Send(v, selVariant)), true
}

// splitVariant is the private variant of the sidebar's (0) or the
// inspector's (1) glass on this system, false where it is not known.
func splitVariant(i int) (int64, bool) {
	g := splitGlass()[i]
	return g.variant, g.ok
}

// The sidebar's and the inspector's glass configurations listed for this
// macOS are the ones AppKit's NSSplitViewController gives its own glass.
func TestSplitGlassMatchesAppKit(t *testing.T) {
	var known, read [2]glassConfig
	var listed bool
	var major int
	onMain(func() {
		w, err := Open(Options{Title: "split glass", Width: 300, Height: 200, Hidden: true}) // AppKit set up as for an app
		if err != nil {
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		major = osMajor()
		known, listed = knownSplitGlass[major]
		read = readSplitGlass()
	})
	if !read[0].ok || !read[1].ok {
		t.Skipf("macOS %d: AppKit's split view has no configurable glass", major)
	}
	if !listed {
		t.Skipf("macOS %d is not listed: AppKit's %+v is read at run time", major, read)
	}
	if known != read {
		t.Errorf("macOS %d: knownSplitGlass %+v, AppKit uses %+v", major, known, read)
	}
}
