package rex

import (
	"gonativeui/theme"
	rextheme "gonativeui/theme/rex"
	"gonativeui/ui"
)

const (
	glideRate   = 30   // 1/s: how fast panes move to their places after a split or close
	instantRate = 4000 // a rate that reaches the target within the frame
	dimRate     = 20   // 1/s: focus dimming
	maxRate     = 20   // 1/s: maximise and restore
	fadeRate    = 18   // 1/s: content fade-in on a tab change, a new pane
)

var (
	keyBorn = ui.HashString(keyAnim, "born")
)

type paneTitle struct {
	title string
	icon  iconID
}

var paneTitles = [...]paneTitle{
	kindAgent: {"Stress-test Snake demo | rex-snake", icAgent},
	kindGit:   {"Git Changes ~/Sites/rex-snake", icGit},
	kindSnake: {"Node ~/Sites/rex-snake", icNode},
	kindBun:   {"Bun ~/sites/announcement-draft", icNode},
	kindShell: {"fish ~/Sites/rex-snake", icTabTerm},
}

// work is the area the panes live in, in pixels.
func (r *Rex) work() [4]float32 {
	s := r.Scale
	W, H := float32(r.lastW), float32(r.lastH)
	return [4]float32{theme.Snap(7.5 * s), theme.Snap(45.5 * s), W - theme.Snap(7.5*s), H - theme.Snap(7.5*s)}
}

func (r *Rex) gutters() gutters {
	s := r.Scale
	return gutters{x: theme.Snap(7 * s), y: theme.Snap(7.5 * s), minW: 150 * s, minH: 100 * s}
}

func (r *Rex) drawPanes(c *ui.Context) {
	t := &r.tabs[r.active]
	work := r.work()
	g := r.gutters()
	t.layout(work, &g)

	initial := float32(1)
	if r.switchN > 0 {
		initial = 0
	}
	fade := c.AnimateValueFrom(ui.KeyN(keyFade, uint64(r.switchN)), initial, 1, fadeRate)
	anyMax := float32(0)
	if t.max != 0 {
		anyMax = 1
	}
	anyMax = c.AnimateValue(ui.KeyN(keyAnyT, uint64(t.uid)), anyMax, maxRate)

	// Displayed rectangle, focus and opacity of each pane.
	var (
		ord     [maxPanes]int8
		n       int
		ft, opa [maxPanes]float32
	)
	gk := float32(instantRate)
	if r.glide {
		gk = glideRate
	}
	r.moving = false
	for slot := range t.panes {
		p := &t.panes[slot]
		if !p.used {
			continue
		}
		isMax := p.uid == t.max
		if !isMax && anyMax >= 1 {
			continue // hidden behind the maximised pane
		}
		tr := t.rects[slot]
		var d [4]float32
		for i := 0; i < 4; i++ {
			key := ui.KeyN(ui.KeyN(keyGlide, uint64(p.uid)), uint64(i))
			var v float32
			if p.born {
				v = c.AnimateValueFrom(key, p.from[i], tr[i], gk)
			} else {
				v = c.AnimateValue(key, tr[i], gk)
			}
			if v != tr[i] {
				r.moving = true
			}
			d[i] = v
		}
		mt := float32(0)
		if isMax {
			mt = 1
		}
		mt = c.AnimateValue(ui.KeyN(keyMaxT, uint64(p.uid)), mt, maxRate)
		for i := 0; i < 4; i++ {
			d[i] = theme.Snap(theme.Lerp(d[i], work[i], mt))
		}
		r.disp[slot] = d

		foc := float32(0)
		if t.focus == p.uid {
			foc = 1
		}
		ft[slot] = c.AnimateValue(ui.KeyN(keyFocus, uint64(p.uid)), foc, dimRate)
		bornInit := float32(1)
		if p.born {
			bornInit = 0
		}
		o := c.AnimateValueFrom(ui.KeyN(keyBorn, uint64(p.uid)), bornInit, 1, fadeRate)
		if !isMax {
			o *= 1 - anyMax
		}
		opa[slot] = o
		p.born = false

		ord[n] = int8(slot)
		n++
	}
	if !r.moving {
		r.glide = false
	}
	// The maximised pane goes last.
	for i := 0; i < n; i++ {
		if t.panes[ord[i]].uid == t.max {
			ord[i], ord[n-1] = ord[n-1], ord[i]
			break
		}
	}

	c.PushOpacity(fade)
	// All shadows first, so no pane's shadow lands on a neighbour's fill.
	for _, slot := range ord[:n] {
		r.th.Chrome.SurfaceShadow(c, r.th, r.surface(t, slot, ft[slot], opa[slot]))
	}
	for _, slot := range ord[:n] {
		r.drawPane(c, t, slot, r.disp[slot], ft[slot], opa[slot])
	}
	if t.max == 0 && anyMax < 0.001 {
		r.drawDividers(c, t)
	}
	c.PopOpacity()
}

// surface describes the pane in slot, drawn at r.disp[slot], to the chrome.
func (r *Rex) surface(t *tab, slot int8, ft, opa float32) theme.Surface {
	d := r.disp[slot]
	return theme.Surface{Key: paneKey(t.panes[slot].uid), Slot: int(slot), Rect: d, Focus: ft, Opacity: opa, Top: d[1] <= r.work()[1]+0.5}
}

// drawPane builds one pane: its fill (white when focused, a translucent
// composite when not, blending as the focus animation runs), the header, the
// terminal text and the hairline edge.
func (r *Rex) drawPane(c *ui.Context, t *tab, slot int8, d [4]float32, ft, opa float32) {
	k := r.K
	p := &t.panes[slot]
	w, h := d[2]-d[0], d[3]-d[1]
	key := paneKey(p.uid)

	s := r.surface(t, slot, ft, opa)
	kg := r.th.Chrome.BeginSurface(c, r.th, s)

	// Header: process icon and title; the buttons only on the focused pane.
	hc := 36.5 * k
	titleCol := r.col(theme.MixRGB(0x606064, 0x202228, ft), theme.MixRGB(0xA3A7B6, 0xE6E8EF, ft))
	pt := paneTitles[p.kind]
	r.IconAt(c, &kg, pt.icon, 39*k, hc, titleCol)
	avail := w - 62*k - theme.Lerp(20*k, 206*k, ft)
	if avail > 4 {
		adv := r.Width(r.th.Styles[theme.StylePaneTitle].Font, pt.title)
		if adv > avail {
			r.Put(c, &kg, r.th.Styles[theme.StylePaneTitle].Font, pt.title, 62*k, 47*k, titleCol, avail, ui.FadeOverflowX)
		} else {
			r.Put(c, &kg, r.th.Styles[theme.StylePaneTitle].Font, pt.title, 62*k, 47*k, titleCol, adv+2, 0)
		}
	}
	if ft > 0.02 {
		c.PushOpacity(ft)
		icons := [numBtns]iconID{icSplit2, icSplit1, icExpand, icXmark}
		dx := [numBtns]float32{181.5, 133.5, 83, 37.5}
		for b := 0; b < numBtns; b++ {
			enabled := ft > 0.5
			switch b {
			case btnSplitX, btnSplitY:
				enabled = enabled && r.canSplit()
			case btnClose:
				enabled = enabled && r.canClose()
			}
			r.HeaderButton(c, holderKey(p.uid, b), icons[b], headerButtons[b], w-dx[b]*k, hc+k, 28*k, enabled)
		}
		c.PopOpacity()
	}

	// Terminal content.
	g := grid{r: r, c: c, kg: keyGen{Base: ui.KeyN(key, 700)}, x0: 20 * k, y0: 101 * k, pal: rextheme.TerminalAt(r.th.T, ft), ft: ft, w: w, h: h}
	switch p.kind {
	case kindAgent:
		g.agent()
	case kindGit:
		g.git()
	case kindSnake:
		g.snake()
	case kindBun:
		g.bun()
	default:
		g.shell()
	}

	r.th.Chrome.EndSurface(c, r.th, s, kg)
}

// drawDividers builds the grab areas of the gutters between panes (theme.Chrome.Divider).
func (r *Rex) drawDividers(c *ui.Context, t *tab) {
	for i := range t.nodes {
		n := &t.nodes[i]
		if !n.used || n.leaf {
			continue
		}
		r.th.Chrome.Divider(c, r.th, divKey(n.uid), n.axis, n.rect, n.gut)
	}
}
