//go:build darwin

package rexui_test

import (
	"math"
	"strings"
	"syscall"
	"testing"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// Toolbar symbols are drawn at AppKit's large scale and items laid out by
// the symbols' exact alignment widths there (not rounded to pixels); menus
// by the medium-scale image width. AppKit's values, macOS 27.2: sidebar.left
// at 13 pt large is 23.41 wide (alignment) and its image 24 × 18; at medium
// 18 wide and 14 tall.
func TestToolbarSymbolsAtLargeScale(t *testing.T) {
	onMacOS27(t)
	if sp := rexui.ToolbarSymbol("sidebar.left", '▤'); sp.Pt != 13 || sp.Scale != icon.ScaleLarge {
		t.Fatalf("ToolbarSymbol: %+v, want 13 pt at the large scale", sp)
	}
	g := openRig(t, theme.Light, nil, func(*rig) {})
	k := g.k
	large := k.AddIcon(rexui.ToolbarSymbol("sidebar.left", '▤'))
	medium := k.AddIcon(icon.Spec{Symbol: "sidebar.left", Fallback: '▤', Pt: 13})
	g.settle()
	if aw := k.IconAlignWidth(large) / g.scale; math.Abs(float64(aw)-23.41) > 0.02 {
		t.Errorf("alignment width %v pt, want AppKit's 23.41 (large, exact)", aw)
	}
	if mw := k.IconMediumWidth(large) / g.scale; mw != 18 {
		t.Errorf("medium image width %v pt, want 18", mw)
	}
	// the raster is AppKit's large image (18 pt tall, ink 1 px inside)
	_, hl := k.IconSize(large)
	_, hm := k.IconSize(medium)
	if d := hl/g.scale - 18; d < 0 || d > 1.5 {
		t.Errorf("the large glyph is %v pt tall, want AppKit's 18 (and the raster's margin)", hl/g.scale)
	}
	if hl <= hm*1.2 {
		t.Errorf("large %v px tall, medium %v: want about 9/7", hl, hm)
	}
}

// With the system's symbols the finder toolbar's items are AppKit's to the
// half point (the Mac mini's Finder scene, captures-macmini 26B5101f): a
// group's first part 4 wider and its last 5 (Share 36 at the unified
// minimum, Tags 39.5), the menus fit to their medium images.
func TestToolbarItemWidthsMatchAppKit(t *testing.T) {
	onMacOS27(t)
	g, _ := newFinderToolbar(t, theme.ToolbarUnified)
	for _, c := range []struct {
		role ui.Role
		name string
		x, w float32
	}{
		{ui.RoleButton, "Group", 646, 47.5}, {ui.RoleGroup, "share-tag", 702, 83.5},
		{ui.RoleButton, "Share", 702, 36}, {ui.RoleButton, "Tags", 746, 39.5},
		{ui.RoleButton, "Action", 794, 44.5},
	} {
		r := g.frameOf(c.role, c.name)
		if !nearPt(r[0], c.x) || !nearPt(r[2]-r[0], c.w) {
			t.Errorf("%s: x %v, %v wide; AppKit's %v, %v", c.name, r[0], r[2]-r[0], c.x, c.w)
		}
	}
}

// onMacOS27 skips a test of AppKit's symbol metrics off macOS 27, where the
// symbols' widths differ (CI's runners).
func onMacOS27(t *testing.T) {
	t.Helper()
	if v, _ := syscall.Sysctl("kern.osproductversion"); !strings.HasPrefix(v, "27.") {
		t.Skipf("macOS %s: the symbol widths are measured on 27", v)
	}
}
