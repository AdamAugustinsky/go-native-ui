package rexui

import (
	"unicode/utf8"

	"gonativeui/img"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// AvatarPresence is the presence dot at an avatar's bottom right corner.
type AvatarPresence uint8

const (
	// PresenceNone draws no dot.
	PresenceNone AvatarPresence = iota
	// PresenceOnline is a green dot.
	PresenceOnline
	// PresenceAway is an orange dot.
	PresenceAway
	// PresenceBusy is a red dot.
	PresenceBusy

	numPresence
)

// AvatarOpts are an avatar's options, passed by value.
type AvatarOpts struct {
	// Size is the side in points (0: AvatarLook.Size, 36).
	Size float32
	// Image is the picture (an img.Handle from the kit's image cache, see
	// base.Kit.SetImages), shown as a circle once it is ready; until then, and
	// if it fails, the initials show. The zero handle is none.
	Image img.Handle
	// Color is the initials' backdrop; the zero Tint takes one of
	// AvatarLook.Palette by a hash of the name, so a person keeps their colour.
	Color Tint
	// Presence is the dot at the bottom right corner.
	Presence AvatarPresence
	// Backdrop is the colour the dot's ring and a stack's overlaps are cut
	// out in (the surface the avatar lies on); the zero Tint is the window
	// background.
	Backdrop Tint
}

// AvatarRecipe is the look of avatars. Contacts' avatars are the model; the
// values are designed (nothing of an avatar is a system control).
type AvatarRecipe struct {
	// Size is the default side in points.
	Size float32
	// Palette is the backdrops initials take (white text on them).
	Palette []theme.Role
	// Presence are the dots' colours; Dot is the dot's diameter as a share
	// of the avatar's side (at least MinDot points) and Ring the width of
	// the backdrop-coloured ring round it, in points.
	Presence    [numPresence]Tint
	Dot, MinDot float32
	Ring        float32
	// Overlap is how much of each avatar a stack covers with the next one,
	// as a share of the side; StackRing is the width of the cut round each.
	Overlap, StackRing float32
}

// AvatarLook is the avatars' recipe.
var AvatarLook = AvatarRecipe{
	Size:     36,
	Palette:  []theme.Role{theme.Blue, theme.Indigo, theme.Purple, theme.Pink, theme.Orange, theme.Teal, theme.Green, theme.Brown},
	Presence: [numPresence]Tint{PresenceOnline: Of(theme.Green), PresenceAway: Of(theme.Orange), PresenceBusy: Of(theme.Red)},
	Dot:      0.3, MinDot: 8, Ring: 2,
	Overlap: 0.3, StackRing: 2,
}

// initialsOf appends the initials of name to f: the first letter of its first
// and last words, upper-cased where that is ASCII ("Ada Lovelace" is "AL",
// "Ada" is "A").
func initialsOf(f *ui.Fmt, name string) {
	first, last := -1, -1
	inWord := false
	for i, r := range name {
		sp := r == ' ' || r == '\t' || r == '\n'
		if !sp && !inWord {
			if first < 0 {
				first = i
			}
			last = i
		}
		inWord = !sp
	}
	put := func(i int) {
		r, n := utf8.DecodeRuneInString(name[i:])
		if r >= 'a' && r <= 'z' {
			f.Byte(byte(r - 'a' + 'A'))
		} else {
			f.Str(name[i : i+n])
		}
	}
	if first >= 0 {
		put(first)
		if last != first {
			put(last)
		}
	}
}

// hashName is FNV-1a over name.
func hashName(name string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(name); i++ {
		h = (h ^ uint32(name[i])) * 16777619
	}
	return h
}

// Avatar builds an avatar of the person or thing called name: their picture
// as a circle, or their initials on a colour until it is ready (and if it
// never is), with an optional presence dot. It is a square of Size points
// (the kit's NextSize replaces it, and the circle follows the smaller side).
// The name is the accessible name.
//
//	rexui.Avatar(k, "Ada Lovelace", rexui.AvatarOpts{Image: v.photo[row], Presence: rexui.PresenceOnline})
func Avatar(k *base.Kit, name string, o AvatarOpts) ui.ID {
	c := k.Context()
	rc := &AvatarLook
	sc := k.Scale()
	size := rc.Size
	if o.Size > 0 {
		size = o.Size
	}
	side := pt(sc, size)
	key := ui.HashString(c.ParentKey(), "avatar") // avatars of one parent are told apart by their order
	k.ApplySize(ui.Px(side), ui.Px(side))
	c.NextRole(ui.RoleImage)
	c.NextSemName(name)
	if o.Presence != PresenceNone && o.Presence < numPresence {
		c.NextSemValueBytes(base.StrBytes([...]string{"", "Online", "Away", "Busy"}[o.Presence]))
	}
	c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask()) // the ring and the cut lie over their neighbour on purpose // the presence dot sits on the corner
	id := c.BoxKeyed(disabledFlag(k), key, nil)
	c.Push(id)
	if o.Image != 0 && k.ImageState(o.Image) == img.Ready {
		k.NextSize(ui.Px(side), ui.Px(side))
		k.Image(0, o.Image, ui.FitFill, side/2, "")
	} else {
		avatarInitials(k, key, name, side, size, &o)
	}
	if o.Presence != PresenceNone && o.Presence < numPresence {
		avatarDot(k, key, side, &o)
	}
	c.Pop()
	return id
}

// avatarInitials draws the initials disc.
func avatarInitials(k *base.Kit, key ui.Key, name string, side, size float32, o *AvatarOpts) {
	c, r := k.Context(), k.Theme()
	rc := &AvatarLook
	col := o.Color
	if col == (Tint{}) {
		col = Of(rc.Palette[hashName(name)%uint32(len(rc.Palette))])
	}
	fill := col.RGBA(r)
	fg := r.Colors[theme.OnAccent]
	if k.Disabled() {
		fill, fg = r.Colors[theme.Fill1], r.Colors[theme.TextDisabled]
	}
	st := avatarStyle(r, size)
	theme.At(c, 0, 0, side, side)
	c.NextBG(fill)
	c.NextRadius(side / 2)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	disc := c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 1), nil)
	f := c.Fmt()
	initialsOf(f, name)
	b := f.Bytes()
	if len(b) == 0 {
		return
	}
	c.Push(disc)
	c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(st.Line))
	c.NextFont(st.Font)
	c.NextTextColor(fg)
	c.BoxKeyed(ui.DrawText, ui.KeyN(key, 2), b)
	c.Pop()
}

// avatarStyle is the text style of the initials at a side of size points.
func avatarStyle(r *theme.Resolved, size float32) *theme.StyleFont {
	switch {
	case size <= 20:
		return &r.Styles[theme.StyleCaption2]
	case size <= 28:
		return &r.Styles[theme.StyleSubheadline]
	case size <= 44:
		return &r.Styles[theme.StyleHeadline]
	case size <= 72:
		return &r.Styles[theme.StyleTitle3]
	}
	return &r.Styles[theme.StyleTitle2]
}

// avatarDot draws the presence dot with its ring.
func avatarDot(k *base.Kit, key ui.Key, side float32, o *AvatarOpts) {
	c, r := k.Context(), k.Theme()
	rc := &AvatarLook
	sc := k.Scale()
	d := theme.Snap(max(side*rc.Dot, pt(sc, rc.MinDot)))
	ring := pt(sc, rc.Ring)
	out := d + 2*ring
	back := o.Backdrop.RGBA(r)
	if o.Backdrop == (Tint{}) {
		back = r.Colors[theme.Window]
	}
	dot := rc.Presence[o.Presence].RGBA(r)
	if k.Disabled() {
		dot = r.Colors[theme.Label3]
	}
	x, y := side-out+ring, side-out+ring // the ring's edge is a little past the corner
	theme.At(c, x, y, out, out)
	c.NextBG(back)
	c.NextRadius(out / 2)
	c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask()) // the ring and the cut lie over their neighbour on purpose
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 3), nil)
	theme.At(c, x+ring, y+ring, d, d)
	c.NextBG(dot)
	c.NextRadius(d / 2)
	c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask()) // the ring and the cut lie over their neighbour on purpose
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 4), nil)
}

// AvatarStack builds a row of avatars that overlap, for the members of a
// group: the first max of names (at most 8; 0: 4) as initials discs, then, if
// there are more, a "+N" disc. label is the accessible name of the group
// ("3 members").
//
//	rexui.AvatarStack(k, "Members", names, 4, rexui.AvatarOpts{Size: 28})
func AvatarStack(k *base.Kit, label string, names []string, max int, o AvatarOpts) ui.ID {
	c, r := k.Context(), k.Theme()
	rc := &AvatarLook
	sc := k.Scale()
	size := rc.Size
	if o.Size > 0 {
		size = o.Size
	}
	if max <= 0 || max > 8 {
		max = 4
	}
	shown := min(len(names), max)
	extra := len(names) - shown
	n := shown
	if extra > 0 {
		n++
	}
	side := pt(sc, size)
	step := theme.Snap(side * (1 - rc.Overlap))
	ring := pt(sc, rc.StackRing)
	display, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	w := side
	if n > 0 {
		w = step*float32(n-1) + side
	}
	k.ApplySize(ui.Px(w), ui.Px(side))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(display)
	c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask()) // the ring and the cut lie over their neighbour on purpose
	id := c.BoxKeyed(0, key, nil)
	c.Push(id)
	back := o.Backdrop.RGBA(r)
	if o.Backdrop == (Tint{}) {
		back = r.Colors[theme.Window]
	}
	st := avatarStyle(r, size)
	for i := 0; i < n; i++ {
		x := float32(i) * step
		// the cut: a disc of the backdrop, a ring wider than the avatar
		theme.At(c, x-ring, -ring, side+2*ring, side+2*ring)
		c.NextBG(back)
		c.NextRadius(side/2 + ring)
		c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask()) // the ring and the cut lie over their neighbour on purpose
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, uint64(10*i)), nil)
		fill, fg := Of(rc.Palette[0]), r.Colors[theme.OnAccent]
		var name string
		if i < shown {
			name = names[i]
			fill = Of(rc.Palette[hashName(name)%uint32(len(rc.Palette))])
		} else {
			fill, fg = Of(theme.Fill1), r.Colors[theme.Label2]
		}
		if k.Disabled() {
			fill, fg = Of(theme.Fill1), r.Colors[theme.TextDisabled]
		}
		theme.At(c, x, 0, side, side)
		c.NextBG(fill.RGBA(r))
		if fill.Role == theme.Fill1 { // a translucent fill over the cut, not over what is under it
			c.NextBG(theme.Over(fill.RGBA(r), back))
		}
		c.NextRadius(side / 2)
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		c.NextAuditExempt(ui.AuditCovered.Mask()) // each lies over the one before, on purpose
		disc := c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, uint64(10*i+1)), nil)
		f := c.Fmt()
		if i < shown {
			initialsOf(f, name)
		} else {
			f.Byte('+').Int(extra)
		}
		b := f.Bytes()
		if len(b) == 0 {
			continue
		}
		c.Push(disc)
		c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(st.Line))
		c.NextFont(st.Font)
		c.NextTextColor(fg)
		c.BoxKeyed(ui.DrawText, ui.KeyN(key, uint64(10*i+2)), b)
		c.Pop()
	}
	c.Pop()
	return id
}
