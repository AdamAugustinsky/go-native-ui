//go:build darwin && !gnui_publicapi

package platform

import "gonativeui/internal/objc"

// The shell's private AppKit API: the sidebar's and the inspector's glass
// variants and the window's corner radius. A build with the gnui_publicapi
// tag (Mac App Store apps) leaves this file out, so that its binary does not
// contain these selectors, and uses material_public_darwin.go instead; at run
// time Window.publicAPIOnly turns the same calls off.

// privateAPI is whether this build has the shell's private AppKit calls.
const privateAPI = true

// setSplitGlass gives glass view v the private configuration AppKit gives
// the sidebar's (a == AreaSidebar) or the inspector's glass, when the view
// takes both settings.
func setSplitGlass(v objc.ID, a MaterialArea) {
	sv := splitGlass()
	g := sv[0]
	if a == AreaInspector {
		g = sv[1]
	}
	if g.ok && objc.SendBool1(v, selRespondsTo, uintptr(selSetVariant)) && objc.SendBool1(v, selRespondsTo, uintptr(selSetAdaptive)) {
		objc.Send1(v, selSetVariant, uintptr(g.variant))
		objc.Send1(v, selSetAdaptive, uintptr(g.adaptive))
	}
}

// windowCornerRadius is the corner radius of window win (private
// _cornerRadius; Ghostty reads it the same way), false where the window does
// not answer it.
func windowCornerRadius(win objc.ID) (float64, bool) {
	if !objc.SendBool1(win, selRespondsTo, uintptr(selWinCornerRadius)) {
		return 0, false
	}
	return objc.SendF64(win, selWinCornerRadius), true
}

// glassConfig is a private NSGlassEffectView configuration.
type glassConfig struct {
	variant, adaptive int64
	ok                bool
}

var (
	selWinCornerRadius = objc.Sel("_cornerRadius") // NSWindow

	selSetVariant  = objc.Sel("set_variant:")
	selSetAdaptive = objc.Sel("set_adaptiveAppearance:")
	selVariant     = objc.Sel("_variant")
	selAdaptive    = objc.Sel("_adaptiveAppearance")

	splitGlassCfg  [2]glassConfig // sidebar, inspector
	splitGlassDone bool
)

// knownSplitGlass are the sidebar and inspector glass configurations of
// NSSplitViewController by macOS major version, as AppKit sets them (read
// from its views; TestSplitGlassMatchesAppKit checks the running system's).
// The private variant numbers moved between macOS 26 and 27, so a version
// that is not listed is asked at run time (splitGlass).
var knownSplitGlass = map[int][2]glassConfig{
	27: {{18, 1, true}, {19, 1, true}},
}

// splitGlass is the sidebar's and the inspector's glass configuration on
// this system: from knownSplitGlass, or else read once from the glass views
// of a throwaway NSSplitViewController with a sidebar and an inspector (about
// 17 ms, once per process, only on systems not listed).
func splitGlass() [2]glassConfig {
	if !splitGlassDone {
		splitGlassDone = true
		if c, ok := knownSplitGlass[osMajor()]; ok {
			splitGlassCfg = c
		} else {
			splitGlassCfg = readSplitGlass()
		}
	}
	return splitGlassCfg
}

// readSplitGlass builds an NSSplitViewController with a sidebar and an
// inspector, lays it out without a window and reads its glass views'
// configuration.
func readSplitGlass() (out [2]glassConfig) {
	svcCls, itemCls, vcCls := objc.Cls("NSSplitViewController"), objc.Cls("NSSplitViewItem"), objc.Cls("NSViewController")
	if svcCls == 0 || clsGlass() == 0 {
		return out
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	newObj := func(c objc.ID) objc.ID { return objc.Send(objc.Send(c, selAlloc), selInitPlain) }
	svc := newObj(svcCls)
	for _, sel := range [2]objc.SEL{objc.Sel("sidebarWithViewController:"), objc.Sel("inspectorWithViewController:")} {
		vc := newObj(vcCls)
		v := newObj(nsViewClass())
		objc.Send1(vc, objc.Sel("setView:"), uintptr(v))
		objc.Send(v, selReleaseObj)
		objc.Send1(svc, objc.Sel("addSplitViewItem:"), uintptr(objc.Send1(itemCls, sel, uintptr(vc))))
		objc.Send(vc, selReleaseObj)
	}
	v := objc.Send(svc, objc.Sel("view"))
	objc.Send(v, objc.Sel("layoutSubtreeIfNeeded"))
	n := 0
	var walk func(v objc.ID)
	walk = func(v objc.ID) {
		if n < 2 && objc.SendBool1(v, selIsKindOfClass, uintptr(clsGlass())) &&
			objc.SendBool1(v, selRespondsTo, uintptr(selVariant)) && objc.SendBool1(v, selRespondsTo, uintptr(selAdaptive)) {
			out[n] = glassConfig{int64(objc.Send(v, selVariant)), int64(objc.Send(v, selAdaptive)), true}
			n++
		}
		subs := objc.Send(v, selSubviews)
		for i := 0; i < int(objc.Send(subs, objc.Sel("count"))); i++ {
			walk(objc.Send1(subs, objc.Sel("objectAtIndex:"), uintptr(i)))
		}
	}
	walk(v)
	objc.Send(svc, selReleaseObj)
	return out
}

// osMajor is the running macOS's major version (0 if unknown).
func osMajor() int {
	pi := objc.Send(objc.Cls("NSProcessInfo"), objc.Sel("processInfo"))
	s := string(objc.AppendUTF8(nil, objc.Send(pi, objc.Sel("operatingSystemVersionString")))) // "Version 27.2 (Build …)"
	n, seen := 0, false
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= '0' && c <= '9' {
			n, seen = n*10+int(c-'0'), true
		} else if seen {
			break
		}
	}
	return n
}
