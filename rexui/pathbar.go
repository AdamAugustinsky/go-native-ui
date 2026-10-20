package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// PathItem is one component of a path bar: a folder, a volume, the current
// item.
type PathItem struct {
	// Title is the component's name.
	Title string
	// Glyph is its symbol (none: the zero value).
	Glyph Glyph
}

// PathBarOpts are a path bar's options, passed by value.
type PathBarOpts struct {
	// Size is the control size (regular: 13 pt text, 16 pt symbols).
	Size theme.Size
}

// PathSignal is what a path bar reports for a frame.
type PathSignal struct {
	base.Signal
	// Index is the component that was clicked (or pressed by an assistive
	// technology), -1 for none: the app navigates there.
	Index int
}

// PathBarRecipe is the look of path bars (NSPathControl, standard style):
// each component's symbol and name, a chevron between two components; when
// they do not fit, the components between the first and the last lose their
// names, from the first on, then the last name fades. Lengths are in
// points.
type PathBarRecipe struct {
	// Text, TextPressed colour a component's name; Chevron the separator.
	Text, TextPressed, Chevron Tint
	// Gap is between a symbol and its name; SepPad on each side of a
	// chevron; Pad at the bar's ends.
	Gap, SepPad, Pad float32
}

// PathBarLook is the path bars' recipe.
var PathBarLook = PathBarRecipe{
	Text: Of(theme.Label), TextPressed: Of(theme.Label2), Chevron: Of(theme.Label3),
	Gap: 4, SepPad: 3, Pad: 4,
}

// maxPath is the most components a path bar lays out; the first ones go
// beyond it.
const maxPath = 32

// PathBar builds a path bar of items, the first the root and the last the
// current location, as wide as its parent unless the kit's NextWidth says
// otherwise. A click on a component reports its index.
//
//	if p := rexui.PathBar(k, "Path", v.path, rexui.PathBarOpts{}); p.Index >= 0 {
//		v.goTo(p.Index)
//	}
func PathBar(k *base.Kit, label string, items []PathItem, o PathBarOpts) PathSignal {
	c, r := k.Context(), k.Theme()
	rc := &PathBarLook
	s := &r.Sizes[o.Size%theme.NumSizes]
	h := theme.Snap(s.Height)
	font := s.Font.Font
	_, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	res := PathSignal{Index: -1}
	if len(items) > maxPath {
		items = items[len(items)-maxPath:]
	}
	n := len(items)

	k.ApplySize(ui.Pct(1), ui.Px(h))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(label)
	root := c.BoxKeyed(ui.Clip, key, nil)
	c.Push(root)
	rr := c.Rect(root)
	avail := rr[2] - rr[0] - 2*k.Pt(rc.Pad)

	// the widths: each component's symbol and name, the chevrons between
	var iconW, textW [maxPath]float32
	gap, sep := k.Pt(rc.Gap), k.Pt(rc.SepPad)
	cw, ch := k.IconSize(icon.ChevronRight)
	sepW := cw + 2*sep
	total := float32(max(n-1, 0)) * sepW
	for i := 0; i < n; i++ {
		if id, ok := items[i].Glyph.ID(); ok {
			iconW[i], _ = k.IconSize(id)
		}
		textW[i] = k.TextWidth(font, items[i].Title) + base.LabelSlack
		total += iconW[i] + textW[i]
		if iconW[i] > 0 && textW[i] > 0 {
			total += gap
		}
	}
	// too wide: the middle components keep only their symbols
	var named [maxPath]bool
	for i := range named[:n] {
		named[i] = true
	}
	for i := 1; i < n-1 && total > avail && avail > 0; i++ {
		if iconW[i] > 0 {
			named[i] = false
			total -= textW[i] + gap
		}
	}

	x := k.Pt(rc.Pad)
	for i := 0; i < n; i++ {
		it := &items[i]
		w := iconW[i]
		if named[i] {
			if w > 0 && textW[i] > 0 {
				w += gap
			}
			w += textW[i]
		}
		if i == n-1 && avail > 0 {
			w = max(min(w, rr[2]-rr[0]-x-k.Pt(rc.Pad)), iconW[i])
		}
		ikey := ui.KeyN(key, uint64(i)+1)
		down := c.ActiveTOf(ikey)
		col := mix(rc.Text.RGBA(r), rc.TextPressed.RGBA(r), down)
		theme.At(c, theme.Snap(x), 0, theme.Snap(w), h)
		c.NextRole(ui.RoleButton)
		c.NextSemName(it.Title)
		flags := ui.Clickable | ui.ActiveAnimation | base.Floating
		if k.Disabled() {
			flags = ui.Disabled | base.Floating
		}
		b := c.BoxKeyed(flags, ikey, nil)
		c.Push(b)
		ix := float32(0)
		if id, ok := it.Glyph.ID(); ok {
			if f, disp := k.Icons().Prepare(c, id, col); f != 0 {
				_, ih := k.IconSize(id)
				theme.At(c, 0, theme.Snap((h-ih)/2), iconW[i], ih)
				c.BoxKeyed(f|base.Floating, ui.KeyN(ikey, 1), disp)
				ix = iconW[i] + gap
			}
		}
		if named[i] && w > ix {
			theme.At(c, theme.Snap(ix), 0, theme.Snap(w-ix), h)
			c.NextFont(font)
			c.NextTextColor(col)
			c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|base.Floating, ui.KeyN(ikey, 2), base.StrBytes(it.Title))
		}
		c.Pop()
		if sg := c.Signal(b); (sg.Clicked || sg.Action&ui.ActPress != 0) && !k.Disabled() {
			res.Index = i
		}
		x += w
		if i < n-1 {
			if f, disp := k.Icons().Prepare(c, icon.ChevronRight, rc.Chevron.RGBA(r)); f != 0 {
				theme.At(c, theme.Snap(x+sep), theme.Snap((h-ch)/2), cw, ch)
				c.BoxKeyed(f|base.Floating, ui.KeyN(key, 1000+uint64(i)), disp)
			}
			x += sepW
		}
	}
	c.Pop()
	res.Signal = k.SignalOf(root)
	return res
}
