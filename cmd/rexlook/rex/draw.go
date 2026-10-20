package rex

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// draw builds the whole frame in tree order: background, panes, titlebar (with
// the traffic lights, if the scene draws them). Every box is a floating child
// of its container with a whole-pixel position and size.
func (r *Rex) draw(c *ui.Context) {
	r.th.Chrome.Background(c, r.th)
	r.drawPanes(c)
	r.drawTitlebar(c)
}

// ---- titlebar ----

// inactiveOpacity is how much of the titlebar's content shows while the
// window is not key.
const inactiveOpacity = 0.55

// Where the tab capsule lies, in reference pixels: it starts capsuleL from
// the window's left edge and may reach capsuleR from its right edge.
const (
	capsuleL = 402
	capsuleR = 380
)

func (r *Rex) drawTitlebar(c *ui.Context) {
	k := r.K
	W := float32(r.lastW)

	// The titlebar band moves the window: a drag area under the tabs and
	// buttons, which keep their presses (ui.Context.DragRegions). The traffic
	// lights are part of it when the scene draws them.
	buttons := theme.ButtonsNone
	if r.Traffic {
		buttons = theme.ButtonsTraffic
	}
	r.th.Chrome.Titlebar(c, r.th, theme.Titlebar{Width: W, Buttons: buttons})

	// The window's key state (animated) dims the band while another window
	// has the keyboard, as AppKit's titlebar content does: quieter titles and
	// icons, a flatter pill (the tabs animate their own the same way).
	keyTgt := float32(0)
	if c.Input().Focused {
		keyTgt = 1
	}
	kw := c.AnimateValue(keyKeyW, keyTgt, 12)
	r.keyW = kw

	// The tabs: rexui's Tabs, Rex's capsule, with the stacked cards of each
	// tab drawn in its lead.
	r.fillTabList()
	t := r.bar.Begin(r.k, keyTabs, r.tabList[:r.nTabs], r.active, rexui.TabsOpts{X0: capsuleL * k, X1: W - capsuleR*k, Y: 12 * k})
	for j := 0; j < t.N(); j++ {
		cell := t.BeginTab(j)
		r.drawCards(c, &cell)
		t.EndTab(&cell)
	}
	t.End()
	r.capsule, r.pill = r.bar.CapsuleRect(), r.bar.PillRect()
	for i := 0; i < r.nTabs; i++ {
		r.tabRects[i] = r.bar.TabRect(i)
	}

	// Host block: device glyph, host name over machine name.
	kg := keyGen{Base: ui.KeyN(keyBar, 3)}
	c.PushOpacity(theme.Lerp(inactiveOpacity, 1, kw))
	devCol, hostCol, machCol := r.col(0x5F5E63, 0xA3A7B8), r.col(0x5F5D64, 0xB8BBCB), r.col(0x87848B, 0x8D91A3)
	if r.backdrop {
		devCol = r.label(0.62)
	}
	r.IconAt(c, &kg, icDevice, 209*k, 43*k, devCol)
	if r.backdrop {
		// Over the material the text dims by its colour, not by opacity,
		// down to floors (labelAlpha).
		c.PopOpacity()
		hostCol, machCol = r.bandLabel(labelTitle), r.bandLabel(labelSecondary)
	}
	r.Put(c, &kg, r.th.Font.UISemi, "rex-snake", 245*k, 42*k, hostCol, 0, 0)
	r.Put(c, &kg, r.th.Font.Small, "Mac Studio", 245*k, 66*k, machCol, 0, 0)
	if !r.backdrop {
		c.PopOpacity()
	}

	// Right edge: ⌘ and +, the window's own when it has native buttons.
	if r.btns != nil || r.shellShot {
		if r.btns != nil {
			r.btns.SetButtonEnabled(cmdPlus, r.nTabs < maxTabs)
		}
		return
	}
	hit := 28 * k
	r.IconButton(c, keyCmd, icCommand, W-115.5*k, 45*k, hit, r.tone(0x5F5D62, 0xA3A7B8), r.tone(0x1E2227, 0xECEDF3), true)
	r.IconButton(c, keyPlus, icPlus, W-46.5*k, 45*k, hit, r.tone(0x5F5D62, 0xA3A7B8), r.tone(0x1E2227, 0xECEDF3), r.nTabs < maxTabs)
}

// fillTabList describes the tabs to the bar: each tab's title and path, and
// the room its stacked cards take before the title. The last tab has no
// close button (the scene keeps one tab).
func (r *Rex) fillTabList() {
	for i := 0; i < r.nTabs; i++ {
		t := &r.tabs[i]
		r.tabList[i] = rexui.Tab{ID: uint64(t.uid), Title: t.name, Detail: t.path, Lead: t.style.textX / 2, NoClose: r.nTabs <= 1}
	}
}

// drawCards builds a tab's stacked cards: coloured cards peek out behind the
// front card, which holds the tab's symbol.
func (r *Rex) drawCards(c *ui.Context, cell *rexui.TabCell) {
	k := r.K
	t := &r.tabs[cell.Index]
	st := &t.style
	tx, ty := cell.Rect[0], cell.Rect[1]
	kg := keyGen{Base: ui.KeyN(r.tabKey(t.uid), 7)}
	nb := 0
	for nb < len(st.backs) && st.backs[nb] != 0 {
		nb++
	}
	x0 := cell.X + st.iconX*k
	for j := nb; j >= 1; j-- {
		fj := float32(j)
		theme.At(c, theme.Snap(x0+st.step*fj*k)-tx, theme.Snap((27+3*fj)*k)-ty, theme.Snap(x0+(st.step*fj+45)*k)-theme.Snap(x0+st.step*fj*k), theme.Snap((65-3*fj)*k)-theme.Snap((27+3*fj)*k))
		c.NextBG(theme.Col(st.backs[j-1]))
		c.NextRadius(8 * k)
		c.BoxKeyed(ui.DrawBackground|fl, kg.Next(), nil)
	}
	fx0, fy0, fx1, fy1 := theme.Snap(x0), theme.Snap(27*k), theme.Snap(x0+45*k), theme.Snap(65*k)
	flags, disp := r.k.Icons().Prepare(c, st.icon, theme.Col(st.symbol))
	theme.At(c, fx0-tx, fy0-ty, fx1-fx0, fy1-fy0)
	c.NextBG(st.front)
	c.NextRadius(8 * k)
	c.NextBorder(st.border, max(1, k))
	c.NextShadow(ui.Shadow{OffY: 1.5 * k, Sigma: 2 * k, Color: 0x00000030})
	c.BoxKeyed(flags|ui.DrawBackground|ui.DrawBorder|ui.DrawShadow|fl, kg.Next(), disp)
}

// label is the text colour of the titlebar over a backdrop: black (light) or
// white (dark) at alpha a, as the system's label colours are.
func (r *Rex) label(a float32) uint32 {
	return theme.ColA(r.tone(0x000000, 0xFFFFFF), a)
}

// The titlebar's text over a backdrop must keep AppKit's contrast floors
// (measured on 27.2; TestBandTextContrastOverGlass):
//
//   - in the key window, the title block and the chosen tab at least
//     secondaryLabelColor's on the window background: 3.95:1 light
//     (black 0.498 over white), 5.84:1 dark (white 0.549 over #1E1E1E);
//   - the other tabs, and everything while the window is not key, at least
//     AppKit's own inactive window title: 1.98:1 light (#B8B8B8 on white),
//     2.25:1 dark (#5C5C5C on #262626; D0 captures, finder and chat).
//
// The background is the glass under the theme's scrim (and, for the tabs,
// the capsule's tint), whose range was measured from screen captures over a
// light and a dark window. Each role has a label alpha per look in the key
// window and one when it is not; the key state animates between them.
type labelRole uint8

const (
	labelTitle     labelRole = iota // the host name (the chosen tab: rexui.TabsRecipe.LabelAlpha)
	labelSecondary                  // the machine name (a tab's path: the same)
)

var labelAlphas = [2]struct {
	key [2]float32
	off float32
}{
	labelTitle:     {key: [2]float32{0.85, 0.92}, off: 0.52},
	labelSecondary: {key: [2]float32{0.64, 0.84}, off: 0.44},
}

// labelAlpha is the alpha of role's text now: the key window's for the look
// (mixed light to dark), toward the not-key one as the window loses key.
func (r *Rex) labelAlpha(role labelRole) float32 {
	a := labelAlphas[role]
	return theme.Lerp(a.off, theme.Lerp(a.key[0], a.key[1], r.th.T), r.keyW)
}

// bandLabel is role's text colour now.
func (r *Rex) bandLabel(role labelRole) uint32 { return r.label(r.labelAlpha(role)) }
