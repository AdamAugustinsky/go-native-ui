package demo

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// newTab adds a tab after the chosen one and chooses it.
func (d *Demo) newTab() {
	if len(d.tabs) >= rexui.MaxTabs {
		return
	}
	t := rexui.Tab{ID: d.nextTab, Title: "Untitled", Glyph: rexui.Sym(d.ic.doc)}
	d.nextTab++
	at := min(d.tabSel+1, len(d.tabs))
	d.tabs = append(d.tabs, rexui.Tab{})
	copy(d.tabs[at+1:], d.tabs[at:])
	d.tabs[at] = t
	d.tabSel = at
}

// toolbarTabs builds the toolbar's tabs in the box (the current parent).
func (d *Demo) toolbarTabs(box ui.ID) {
	k := d.k
	c, r := k.Context(), k.Theme()
	br := c.Rect(box)
	rc := &rexui.TabsRecipes[rexui.TabsNeutral]
	hh := (rc.Height + 2*rc.Inset[1]) * r.Scale
	ev := rexui.Tabs(k, "Window Tabs", &d.bar, d.tabs, &d.tabSel, rexui.TabsOpts{Variant: rexui.TabsNeutral, X0: 0, X1: br[2] - br[0], Y: theme.Snap((br[3] - br[1] - hh) / 2)})
	d.apply(&d.tabs, &d.tabSel, ev)
}

// tabsContent is a pane with Rex tabs in content, in the Rex look.
func (d *Demo) tabsContent(w, h float32) {
	k := d.k
	c, r := k.Context(), k.Theme()
	s := r.Scale
	pad := theme.Snap(24 * s)
	ev := rexui.Tabs(k, "Sections", &d.inBar, d.inTabs, &d.inSel, rexui.TabsOpts{X0: pad, X1: w - pad, Y: theme.Snap(16 * s), Focusable: true, NoDrag: true})
	d.apply(&d.inTabs, &d.inSel, ev)
	st := &r.Styles[theme.StyleTitle2]
	theme.At(c, pad, theme.Snap(80*s), w-2*pad, st.Line)
	c.NextFont(st.Font)
	c.NextTextColor(r.Colors[theme.Label])
	title := ""
	if d.tabSel >= 0 && d.tabSel < len(d.tabs) {
		title = d.tabs[d.tabSel].Title
	}
	c.BoxKeyed(ui.DrawText|base.Floating, ui.HashString(0, "tab title"), base.StrBytes(title))
	if d.inSel >= 0 && d.inSel < len(d.inTabs) {
		sub := &r.Styles[theme.StyleBody]
		theme.At(c, pad, theme.Snap(110*s), w-2*pad, sub.Line)
		c.NextFont(sub.Font)
		c.NextTextColor(r.Colors[theme.Label2])
		c.BoxKeyed(ui.DrawText|base.Floating, ui.HashString(0, "section"), base.StrBytes(d.inTabs[d.inSel].Title))
	}
}

// apply applies a tab bar's close and move to its tabs.
func (d *Demo) apply(tabs *[]rexui.Tab, sel *int, ev rexui.TabsEvents) {
	t := *tabs
	if ev.MoveFrom >= 0 && ev.MoveTo >= 0 && ev.MoveFrom < len(t) && ev.MoveTo < len(t) {
		m := t[ev.MoveFrom]
		if ev.MoveFrom < ev.MoveTo {
			copy(t[ev.MoveFrom:ev.MoveTo], t[ev.MoveFrom+1:ev.MoveTo+1])
		} else {
			copy(t[ev.MoveTo+1:ev.MoveFrom+1], t[ev.MoveTo:ev.MoveFrom])
		}
		t[ev.MoveTo] = m
	}
	if ev.Close >= 0 && ev.Close < len(t) && len(t) > 1 {
		copy(t[ev.Close:], t[ev.Close+1:])
		t = t[:len(t)-1]
		if *sel >= len(t) || *sel > ev.Close {
			*sel = max(*sel-1, 0)
		}
	}
	*tabs = t
}
