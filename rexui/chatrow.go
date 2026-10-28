package rexui

import (
	"gonativeui/img"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ChatRowOpts are a chat list row's options, passed by value.
type ChatRowOpts struct {
	// Compact is the one-line row: a smaller avatar and no preview.
	Compact bool
	// Selected draws the sidebar's selection: the accent block with white text
	// while the window is key, the grey of an inactive window's otherwise.
	Selected bool
	// Unread is the number of unread messages: the title turns bold and a
	// count badge shows at the end of the row (none for 0).
	Unread int
	// Muted draws the badge grey, as a conversation whose notifications are off.
	Muted bool
	// Typing replaces the preview with "typing…" in the accent colour.
	Typing bool
	// Image is the avatar's picture, Presence its dot (Avatar's).
	Image    img.Handle
	Presence AvatarPresence
}

// ChatRowRecipe is the look of chat list rows (a conversation in Messages'
// or Mail's sidebar: designed after §3.4, row 56 pt: avatar 36, title in
// Headline, preview in Subheadline over two lines, time in Caption 1).
type ChatRowRecipe struct {
	// H and AvatarSize are the row's height and its avatar's side, in points,
	// regular and compact; Pad the space round the content and Gap the one
	// between its parts.
	H, AvatarSize [2]float32
	Pad, Gap      float32
	// Radius is the selection block's (the sidebar's, 8) and Inset how far it
	// is inset from the row's edges.
	Radius, Inset float32
	// Selected and SelectedInactive are the selection's fills: the sidebar's
	// accent, and the grey of a window that is not key.
	Selected, SelectedInactive Tint
	// Title, TitleSelected, Preview, PreviewSelected, Time and Typing are the
	// text colours.
	Title, TitleSelected, Preview, PreviewSelected, Time, Typing Tint
}

// ChatRowLook is the chat rows' recipe.
var ChatRowLook = ChatRowRecipe{
	H: [2]float32{56, 44}, AvatarSize: [2]float32{36, 28},
	Pad: 10, Gap: 10, Radius: 8, Inset: 6,
	Selected: Of(theme.SidebarSel), SelectedInactive: Of(theme.SidebarSelInactive),
	Title: Of(theme.Label), TitleSelected: Of(theme.OnAccent), Preview: Of(theme.Label2), PreviewSelected: Of(theme.OnAccent),
	Time: Of(theme.Label2), Typing: Of(theme.Accent),
}

// ChatListRow builds a row of a list of conversations: the avatar, the
// title, the preview (two lines at most), the time of the last message and an
// unread badge. It fills the parent's width and is ChatRowLook.H high. A press
// or Enter activates it (Clicked); keyboard focus shows the ring, the pointer's
// does not. Its accessible name is the title and its value the preview. In a
// list the app owns the selection and passes it as o.Selected.
//
//	if rexui.ChatListRow(k, c.Fmt().Str("chat").Int(i).String(), chat.name, chat.last, chat.time, rexui.ChatRowOpts{Selected: i == v.sel}).Clicked {
//		v.sel = i
//	}
func ChatListRow(k *base.Kit, label, title, preview, when string, o ChatRowOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	rc := &ChatRowLook
	sc := k.Scale()
	ci := 0
	if o.Compact {
		ci = 1
	}
	p := k.PressState(label, base.HoverOff)
	h := pt(sc, rc.H[ci])
	radius := pt(sc, rc.Radius)
	inset := pt(sc, rc.Inset)
	pad, gap := pt(sc, rc.Pad), pt(sc, rc.Gap)

	var fill uint32
	titleC, previewC, timeC := rc.Title.RGBA(r), rc.Preview.RGBA(r), rc.Time.RGBA(r)
	active := !p.Inactive
	switch {
	case p.Disabled:
		titleC, previewC, timeC = r.Colors[theme.TextDisabled], r.Colors[theme.TextDisabled], r.Colors[theme.TextDisabled]
	case o.Selected && active:
		fill = rc.Selected.RGBA(r)
		titleC, previewC, timeC = rc.TitleSelected.RGBA(r), rc.PreviewSelected.RGBA(r), rc.PreviewSelected.RGBA(r)
	case o.Selected:
		fill = rc.SelectedInactive.RGBA(r)
	}

	var state ui.SemState
	if o.Selected {
		state = ui.SemSelected
	}
	c.NextSemState(state)
	c.NextSemValueBytes(base.StrBytes(preview))
	c.NextAxis(ui.AxisX)
	c.NextPadding(inset, 0, inset, 0)
	p.Name = title // the row is named by its conversation, not by the label that tells rows apart
	k.BeginPress(&p, ui.RoleListItem, ui.Pct(1), ui.Px(h), 0, nil)
	id := p.ID
	// the selection block lies inset from the row's edges
	c.NextBG(fill)
	c.NextRadius(radius)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(gap)
	c.NextPadding(pad, 0, pad, 0)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	block := c.BoxKeyed(ui.DrawBackground, ui.KeyN(p.Key, 19), nil)
	c.Push(block)
	Avatar(k, title, AvatarOpts{Size: rc.AvatarSize[ci], Image: o.Image, Presence: o.Presence, Backdrop: Of(theme.Window)})

	// the text column: title and time on one line, the preview below
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextGap(pt(sc, 1))
	col := c.BoxKeyed(0, ui.KeyN(p.Key, 20), nil)
	c.Push(col)
	ts := &r.Styles[theme.StyleHeadline]
	if o.Unread == 0 {
		ts = &r.Styles[theme.StyleBodySemibold]
	}
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(gap)
	c.NextSize(ui.Pct(1), ui.Px(ts.Line))
	line := c.BoxKeyed(0, ui.KeyN(p.Key, 21), nil)
	c.Push(line)
	c.NextSize(ui.Pct(1), ui.Px(ts.Line))
	c.NextFont(ts.Font)
	c.NextTextColor(titleC)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(p.Key, 22), base.StrBytes(title))
	if when != "" {
		tm := &r.Styles[theme.StyleCaption1]
		c.NextSize(ui.TextSize(0), ui.Px(tm.Line))
		c.NextFont(tm.Font)
		c.NextTextColor(timeC)
		c.BoxKeyed(ui.DrawText, ui.KeyN(p.Key, 23), base.StrBytes(when))
	}
	c.Pop()
	if !o.Compact {
		ps := &r.Styles[theme.StyleSubheadline]
		txt, pc := preview, previewC
		if o.Typing {
			txt, pc = "typing…", rc.Typing.RGBA(r)
			if o.Selected && active {
				pc = previewC
			}
		}
		c.NextFont(ps.Font)
		c.NextTextColor(pc)
		c.NextSize(ui.Pct(1), ui.Px(2*ps.Line))
		c.RichText(ui.KeyN(p.Key, 24), base.StrBytes(txt), nil, nil, ui.RichOpts{MaxLines: 2, Ellipsis: true})
	}
	c.Pop()

	if o.Unread > 0 {
		bo := BadgeOpts{}
		if o.Muted {
			bo.Color = Of(theme.Gray)
		}
		if o.Selected && active {
			bo.Color, bo.TextColor = Of(theme.OnAccent), Of(theme.Accent) // (white on the accent row: the text is the accent)
		}
		BadgeCountN(k, o.Unread, bo)
	}
	c.Pop() // the block
	k.FocusRing(id, p.Visual, radius)
	return k.EndPress(&p)
}

// DateDividerOpts are a date divider's options, passed by value.
type DateDividerOpts struct {
	// Pill draws the date in a capsule on the secondary fill with no rules, the
	// header a list pins at its top edge while it scrolls ("sticky": the app
	// pins it, as it places any floating box).
	Pill bool
}

// DateDivider builds the divider between the days of a conversation: the date in
// Caption 1 semibold in the secondary label colour, centred, with a hairline
// on each side. It fills the parent's width.
//
//	rexui.DateDivider(k, "Today", rexui.DateDividerOpts{})
func DateDivider(k *base.Kit, label string, o DateDividerOpts) ui.ID {
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	display, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	st := &r.Styles[theme.StyleCaption1]
	h := st.Line + 2*pt(sc, 6)
	k.ApplySize(ui.Pct(1), ui.Px(h))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextGap(pt(sc, 10))
	c.NextRole(ui.RoleLabel)
	c.NextSemName(display)
	root := c.BoxKeyed(0, key, nil)
	c.Push(root)
	defer c.Pop()
	rule := func(n uint64) {
		c.NextSize(ui.Pct(1), ui.Px(r.Lines.Hairline))
		c.NextBG(r.Colors[theme.Separator])
		c.BoxKeyed(ui.DrawBackground, ui.KeyN(key, n), nil)
	}
	if o.Pill {
		c.NextBG(theme.Over(r.Colors[theme.Fill2], r.Colors[theme.Window]|0xFF))
		c.NextRadius((st.Line + 2*pt(sc, 3)) / 2)
		c.NextPadding(pt(sc, 10), pt(sc, 3), pt(sc, 10), pt(sc, 3))
		c.NextSize(ui.Children(1), ui.Children(1))
		pill := c.BoxKeyed(ui.DrawBackground, ui.KeyN(key, 3), nil)
		c.Push(pill)
		dateText(k, key, display, st)
		c.Pop()
		return root
	}
	rule(1)
	dateText(k, key, display, st)
	rule(2)
	return root
}

// dateText is the date's text of a divider.
func dateText(k *base.Kit, key ui.Key, s string, st *theme.StyleFont) {
	c, r := k.Context(), k.Theme()
	c.NextSize(ui.TextSize(0), ui.Px(st.Line))
	c.NextFont(k.TextSystem().Font(text.FontDesc{SizePt: 10, Weight: theme.WeightSemibold, Scale: k.Scale()}))
	c.NextTextColor(r.Colors[theme.Label2])
	c.BoxKeyed(ui.DrawText, ui.KeyN(key, 4), base.StrBytes(s))
}
