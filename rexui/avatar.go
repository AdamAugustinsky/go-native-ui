package rexui

import (
	"unicode"
	"unicode/utf8"

	"gonativeui/img"
	"gonativeui/text"
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
	// Label, when not empty, is the accessible name in place of the name
	// Avatar was given ("Ada Lovelace, online, 3 unread"); the name still
	// makes the initials and the colour.
	Label string
	// Size is the side in points (0: AvatarLook.Size, 36).
	Size float32
	// Image is the picture (an img.Handle from the kit's image cache, see
	// base.Kit.SetImages), shown as a circle once it is ready; until then, and
	// if it fails, the initials show. The zero handle is none.
	Image img.Handle
	// Color is the initials' backdrop: a role, or a colour of the app's own
	// (OfRGBA); the zero Tint takes one of AvatarLook.Palette by a hash of the
	// name, so a person keeps their colour.
	Color Tint
	// Images are AvatarStack's pictures, by the index of the names: one that
	// is set and ready shows in place of that name's initials.
	Images []img.Handle
	// Pressable makes the avatar a button: it takes the keyboard focus (with
	// the focus ring), clicks, Enter and Space activate it, and its role is
	// RoleButton. Read the press with k.SignalOf(id).Clicked on the ID Avatar
	// returns. An avatar of AvatarStack is never pressable.
	Pressable bool
	// InitialsScale is the initials' point size as a share of the avatar's
	// diameter (0.38 is what chat apps draw); 0 takes the size of the text
	// style the avatar's size picks. InitialsWeight is their weight, CSS-like
	// (theme.WeightSemibold is 600); 0 takes that text style's weight. Set
	// either and the initials use a font of their own; the other then falls
	// back to that style's (theme.MacText), and both zero leave the look
	// unchanged. AvatarStack's initials follow the same two.
	InitialsScale  float32
	InitialsWeight int
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
	// Palette is the backdrops initials take (white text on them), each
	// deepened as far as white needs to read on it at 4.5:1.
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

// initialsOf appends the initials of name (Initials) to f.
func initialsOf(f *ui.Fmt, name string) {
	var buf [64]byte
	for _, c := range Initials(buf[:0], name) {
		f.Byte(c)
	}
}

// BlankInitial is what an avatar shows for a name that is empty or only white
// space: a hash sign, as chat apps do (Initials returns it). A person symbol
// would need an icon drawn in every avatar; the sign is text, so it takes the
// avatar's colour, weight and size like any initials.
const BlankInitial = "#"

// Initials appends to dst the initials an avatar shows for name: the first
// letter or digit of its first and last words, title-cased ("Ada Lovelace" is
// "AL", "Ada" is "A"). A word is what lies between white space or a hyphen or
// dash, so "Ada Lovelace-King" is "AK" and "Ada-Lovelace" is "AL"; its
// initial is its first letter or digit wherever it is in it ("Name (You)" is
// "NY", "@ada" is "A"), in any script, with what makes one grapheme with it
// (combining marks: "e\u0301" stays whole); a word with none (an emoji) has
// no initial. A name with no letter or digit at all gives its first grapheme
// ("🎉 🎂" is "🎉"), and a name that is empty or only white space gives
// BlankInitial ("#"). It allocates nothing beyond dst's growth.
func Initials(dst []byte, name string) []byte {
	b := base.StrBytes(name)
	first, last := -1, -1
	found := false // the word has its initial
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		switch {
		case unicode.IsSpace(r) || unicode.Is(unicode.Pd, r):
			found = false
		case !found && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if first < 0 {
				first = i
			}
			last, found = i, true
		}
		i += n
	}
	put := func(i int) {
		end := text.NextGrapheme(b, i)
		r, n := utf8.DecodeRune(b[i:])
		if u := unicode.ToTitle(r); u != r && r != utf8.RuneError {
			dst = utf8.AppendRune(dst, u)
			i += n
		}
		dst = append(dst, b[i:end]...)
	}
	if first < 0 { // no letter or digit: the first grapheme that is not white space
		for i := 0; i < len(b); {
			r, n := utf8.DecodeRune(b[i:])
			if !unicode.IsSpace(r) {
				put(i)
				return dst
			}
			i += n
		}
		return append(dst, BlankInitial...)
	}
	put(first)
	if last != first {
		put(last)
	}
	return dst
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
// The name is the accessible name unless AvatarOpts.Label is set. With Pressable it is a button, and the
// press is k.SignalOf(id).Clicked.
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
	role, flags := ui.RoleImage, disabledFlag(k)
	var vis base.Visual
	if o.Pressable {
		role, flags = ui.RoleButton, k.InteractFlags(base.HoverOff)
		vis = k.VisualOf(key, base.HoverOff)
	}
	c.NextRole(role)
	if o.Label != "" {
		c.NextSemName(o.Label)
	} else {
		c.NextSemName(name)
	}
	if o.Presence != PresenceNone && o.Presence < numPresence {
		c.NextSemValueBytes(base.StrBytes([...]string{"", str.Online, str.Away, str.Busy}[o.Presence]))
	}
	c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask()) // the ring and the cut lie over their neighbour on purpose // the presence dot sits on the corner
	id := c.BoxKeyed(flags, key, nil)
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
	if o.Pressable {
		k.FocusRing(id, vis, side/2)
	}
	c.Pop()
	return id
}

// avatarInitials draws the initials disc.
func avatarInitials(k *base.Kit, key ui.Key, name string, side, size float32, o *AvatarOpts) {
	c, r := k.Context(), k.Theme()
	rc := &AvatarLook
	fg := uint32(0xFFFFFFFF)
	fill := o.Color.RGBA(r) // the app's colour, as it gave it
	if o.Color == (Tint{}) {
		fill = legibleUnder(Of(rc.Palette[hashName(name)%uint32(len(rc.Palette))]).RGBA(r), fg)
	}
	if k.Disabled() {
		fill, fg = r.Colors[theme.Fill1], r.Colors[theme.TextDisabled]
	}
	st := avatarStyle(k, size, o)
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
	// The text box is the whole disc with the text centred in it, on both
	// axes by the text's own rule. A box of the text's width and the font's line
	// height, centred, leaves the text up to a pixel off: a snapped box a half
	// pixel low.
	// Initials wider than the disc keep the text's own width, overflowing
	// both sides evenly.
	if tw := ui.CeilText(k.TextWidthBytes(st.Font, b)); tw < side {
		c.NextSize(ui.Px(side), ui.Px(side))
		c.NextAlign(ui.AlignCenter)
	} else {
		c.NextSize(ui.TextSize(0), ui.Px(st.Line))
	}
	c.NextFont(st.Font)
	c.NextTextColor(fg)
	c.BoxKeyed(ui.DrawText, ui.KeyN(key, 2), b)
	c.Pop()
}

// legibleUnder is fill, deepened toward black just enough that fg (white
// initials) reads on it at 4.5:1: a green or an orange disc keeps its hue and
// loses the brightness white text cannot stand on.
func legibleUnder(fill, fg uint32) uint32 {
	for i := 0; i < 16 && theme.Contrast(fg, fill) < 4.5; i++ {
		fill = theme.MixRGBA(fill, fill&0xFF, 0.08) // toward black, keeping its alpha
	}
	return fill
}

// largeInitials is the initials' point size as a share of the side of an
// avatar larger than 72 points (a call's), where Title2 would leave them
// small in the disc.
const largeInitials = 0.32

// avatarStyleIndex is the text style of the initials at a side of size points.
func avatarStyleIndex(size float32) theme.TextStyle {
	switch {
	case size <= 20:
		return theme.StyleCaption2
	case size <= 28:
		return theme.StyleSubheadline
	case size <= 44:
		return theme.StyleHeadline
	case size <= 72:
		return theme.StyleTitle3
	}
	return theme.StyleTitle2
}

// avatarStyle is the font and line of the initials at a side of size points:
// the text style the size picks, or, when o sets InitialsScale or
// InitialsWeight, a font of their own (the style's own value for the one not
// set) in a line box of that font.
func avatarStyle(k *base.Kit, size float32, o *AvatarOpts) theme.StyleFont {
	idx := avatarStyleIndex(size)
	st := k.Theme().Styles[idx]
	if o.InitialsScale <= 0 && o.InitialsWeight <= 0 && size <= 72 {
		return st
	}
	spec := theme.MacText[idx]
	pts, weight := spec.Size, spec.Weight
	switch {
	case o.InitialsScale > 0:
		pts = o.InitialsScale * size
	case size > 72: // past the largest style the initials grow with the disc
		pts = max(pts, largeInitials*size)
	}
	if o.InitialsWeight > 0 {
		weight = o.InitialsWeight
	}
	ts := k.TextSystem()
	f := ts.Font(text.FontDesc{SizePt: pts, Weight: weight, Scale: k.Scale()})
	lb := theme.LineBox(ts.Metrics(f))
	return theme.StyleFont{Font: f, Line: lb.H, Baseline: lb.Off}
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
// group: the first max of names (at most 8; 0: 4) as their pictures
// (AvatarOpts.Images, by the same index) or initials discs, then, if there
// are more, a "+N" disc. label is the accessible name of the group ("3
// members").
//
//	rexui.AvatarStack(k, "Members", names, 4, rexui.AvatarOpts{Size: 28, Images: photos})
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
	st := avatarStyle(k, size, &o)
	for i := 0; i < n; i++ {
		x := float32(i) * step
		// the cut: a disc of the backdrop, a ring wider than the avatar
		theme.At(c, x-ring, -ring, side+2*ring, side+2*ring)
		c.NextBG(back)
		c.NextRadius(side/2 + ring)
		c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask()) // the ring and the cut lie over their neighbour on purpose
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, uint64(10*i)), nil)
		if i < shown && i < len(o.Images) && o.Images[i] != 0 && k.ImageState(o.Images[i]) == img.Ready {
			theme.At(c, x, 0, side, side)
			c.NextFlags(base.Floating)
			c.NextAuditExempt(ui.AuditCovered.Mask()) // each lies over the one before, on purpose
			k.Image(uint64(i), o.Images[i], ui.FitFill, side/2, "")
			continue
		}
		fill, fg := Of(rc.Palette[0]), uint32(0xFFFFFFFF)
		var name string
		if i < shown {
			name = names[i]
			fill = OfRGBA(legibleUnder(Of(rc.Palette[hashName(name)%uint32(len(rc.Palette))]).RGBA(r), fg))
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
		if i < n-1 { // the initials centre in the part the next avatar leaves showing
			c.NextPadding(0, 0, side-step+ring, 0)
		}
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
		c.NextSize(ui.TextSize(0), ui.Px(st.Line))
		c.NextFont(st.Font)
		c.NextTextColor(fg)
		c.BoxKeyed(ui.DrawText, ui.KeyN(key, uint64(10*i+2)), b)
		c.Pop()
	}
	c.Pop()
	return id
}
