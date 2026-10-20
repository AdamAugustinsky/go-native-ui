package catalog

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The D7a samples: the combo box, token field and colour well that AppKit has
// (AppKitRef's combo, token and color-well, and the states added beside them),
// and the components it has not (popover, toast, banner, badge, avatar,
// skeleton, empty state, the chat pieces and the completion list), which have
// no reference to measure against and so are shown on a page of their own
// (d7aPage) for the eye, the layout audit and the allocation test.

// d7aState is what the D7a samples edit.
type d7aState struct {
	made   bool
	combo  [3]rexui.ComboState
	cbuf   [3]ui.TextBuffer
	tf     [3]rexui.TokenFieldState
	tokens [3][]string
	well   [3]rexui.ColorWellState
	color  uint32
	comp   rexui.CompletionState
	styles [2]ui.RichStyles
}

var (
	comboChoices = []string{"Combo", "Other", "Third choice"}
	d7aItems     = []rexui.CompletionItem{
		{Title: "Ada Lovelace", Detail: "@ada"},
		{Title: "Alan Turing", Detail: "@alan"},
		{Title: "Grace Hopper", Detail: "@grace"},
		{Title: "Katherine Johnson", Detail: "@kjohnson"},
	}
	bubbleBody = []byte("Lunch at noon? I booked the place by the river, and Paolo says he will bring the notes.")
)

func (d *d7aState) make() {
	if d.made {
		return
	}
	d.made = true
	for i := range d.cbuf {
		d.cbuf[i].Reserve(64)
		d.cbuf[i].SetString("Combo")
	}
	for i := range d.tf {
		d.tf[i].Input.Reserve(64)
		d.tokens[i] = make([]string, 0, 8)
		d.tokens[i] = append(d.tokens[i], "Alpha", "Beta")
	}
	for i := range d.well {
		d.well[i].Hex.Reserve(16)
	}
	d.comp.Reset(len(d7aItems), true)
}

// d7a is the state of the D7a samples, after (*State).make.
func (s *State) d7aState() *d7aState {
	s.d7a.make()
	return &s.d7a
}

func combo(i int, look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		d := s.d7aState()
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		s.k.NextWidth(s.k.Fixed(160))
		id := rexui.Combobox(s.k, "Combo", &d.combo[i], &d.cbuf[i], comboChoices, rexui.ComboOpts{Placeholder: "Placeholder"}).ID
		s.k.EndDisabled()
		return id
	}
}

func token(i int, look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		d := s.d7aState()
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		s.k.NextWidth(s.k.Fixed(200))
		id := rexui.TokenField(s.k, "Tokens", &d.tf[i], &d.tokens[i], rexui.TokenFieldOpts{Placeholder: "Placeholder"}).ID
		s.k.EndDisabled()
		return id
	}
}

func colorWell(i int, look base.Look, disabled bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		d := s.d7aState()
		d.color = s.k.Theme().Colors[theme.Teal] | 0xFF
		s.k.BeginDisabled(disabled)
		s.k.Force(look)
		id := rexui.ColorWell(s.k, "Colour", &d.well[i], &d.color, rexui.ColorWellOpts{}).ID
		s.k.EndDisabled()
		return id
	}
}

// sized is a fixed-size box of w × h points for a sample that is not one
// control: it draws nothing and holds what fn builds, so that the sample has
// AppKit's frame, and the root the measurements read.
func sized(w, h float32, fn func(s *State)) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		k, c := s.k, s.k.Context()
		c.NextSize(ui.Px(k.Pt(w)), ui.Px(k.Pt(h)))
		c.NextAuditExempt(ui.AuditOverflow.Mask()) // overlays and floating parts of a sample may leave its frame
		frame := c.Box(0, "frame")
		c.Push(frame)
		fn(s)
		c.Pop()
		return frame
	}
}

func row(s *State, fn func(s *State)) {
	k := s.k
	k.NextGap(12)
	k.Row("row")
	fn(s)
	k.EndRow()
}

func badges(s *State) {
	k := s.k
	row(s, func(s *State) {
		rexui.BadgeCountN(k, 3, rexui.BadgeOpts{})
		rexui.BadgeCountN(k, 42, rexui.BadgeOpts{})
		rexui.BadgeCountN(k, 120, rexui.BadgeOpts{})
		rexui.Badge(k, "", rexui.BadgeOpts{Variant: rexui.BadgeStatus, Name: "Online"})
		rexui.Badge(k, "Beta", rexui.BadgeOpts{Variant: rexui.BadgeTag})
		k.BeginDisabled(true)
		rexui.Badge(k, "Off", rexui.BadgeOpts{Variant: rexui.BadgeTag})
		k.EndDisabled()
	})
}

func avatars(s *State) {
	k := s.k
	row(s, func(s *State) {
		rexui.Avatar(k, "Ada Lovelace", rexui.AvatarOpts{})
		rexui.Avatar(k, "Alan Turing", rexui.AvatarOpts{Presence: rexui.PresenceOnline})
		rexui.Avatar(k, "Grace", rexui.AvatarOpts{Size: 28, Presence: rexui.PresenceBusy})
		rexui.Avatar(k, "k", rexui.AvatarOpts{Size: 20})
		rexui.AvatarStack(k, "Members", []string{"Ada Lovelace", "Alan Turing", "Grace Hopper", "Katherine Johnson", "Margaret Hamilton"}, 3, rexui.AvatarOpts{Size: 28})
	})
}

func skeletons(s *State) {
	k := s.k
	row(s, func(s *State) {
		rexui.Skeleton(k, "avatar", rexui.SkeletonOpts{Shape: rexui.SkeletonAvatar, Still: true, Phase: 0.5})
		k.NextWidth(k.Fixed(220))
		k.NextGap(8)
		k.Column("lines")
		k.NextWidth(k.Fixed(220))
		rexui.Skeleton(k, "l1", rexui.SkeletonOpts{Still: true, Phase: 0.5})
		k.NextWidth(k.Fixed(160))
		rexui.Skeleton(k, "l2", rexui.SkeletonOpts{Still: true, Phase: 0.5})
		k.EndColumn()
		k.NextWidth(k.Fixed(120))
		rexui.Skeleton(k, "block", rexui.SkeletonOpts{Shape: rexui.SkeletonBlock, Still: true, Phase: 0.5})
	})
}

func bannerSample(lv rexui.BannerLevel, title, msg, action string, closeBtn bool) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.k.NextWidth(s.k.Fixed(420))
		return rexui.Banner(s.k, title, rexui.BannerOpts{Level: lv, Message: msg, Action: action, Dismissible: closeBtn}).ID
	}
}

func toastSample(s *State) {
	rexui.Toast(s.k, "Message sent", rexui.ToastOpts{BannerOpts: rexui.BannerOpts{Level: rexui.BannerSuccess, Message: "Delivered to 3 people."}, Duration: -1})
}

func popoverSample(edge rexui.PopoverEdge) func(s *State) {
	return func(s *State) {
		k, c := s.k, s.k.Context()
		k.NextPadding(0, 0, 0, 0)
		var anchor ui.ID
		k.Row("anchor")
		k.Spacer()
		anchor = rexui.Button(k, "Anchor", rexui.ButtonOpts{}).ID
		k.Spacer()
		k.EndRow()
		pop := rexui.BeginPopover(k, "pop", c.Rect(anchor), rexui.PopoverOpts{Edge: edge, Width: 220, Behavior: rexui.PopoverApplicationDefined})
		rexui.Label(k, "Popover", rexui.LabelOpts{Style: rexui.TextHeadline})
		rexui.Label(k, "A bubble anchored to a control.", rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
		rexui.EndPopover(k, &pop)
	}
}

func completionSample(s *State) {
	k, c := s.k, s.k.Context()
	d := s.d7aState()
	f := rexui.Label(k, "@a", rexui.LabelOpts{})
	r := c.Rect(f)
	caret := [4]float32{r[2], r[1], r[2] + 1, r[3]}
	rexui.CompletionList(k, "Mentions", &d.comp, d7aItems, caret, rexui.CompletionOpts{})
}

func chatSample(s *State) {
	k := s.k
	d := s.d7aState()
	k.NextGap(2)
	k.Column("chat")
	rexui.DateDivider(k, "Today", rexui.DateDividerOpts{})
	for i, v := range [...]rexui.BubbleOpts{
		{Variant: rexui.BubbleIncoming},
		{Variant: rexui.BubbleIncoming, Tail: true},
		{Variant: rexui.BubbleOutgoing, Tail: true},
		{Variant: rexui.BubbleOutgoing, Status: rexui.BubbleSending},
		{Variant: rexui.BubbleOutgoing, Status: rexui.BubbleFailed, Tail: true},
		{Variant: rexui.BubbleSystem},
	} {
		v.ColumnW = 360
		rexui.BubbleRichStyles(k, v.Variant, &d.styles[0])
		body := bubbleBody
		if v.Variant == rexui.BubbleSystem {
			body = []byte("Paolo joined the group")
		} else if i == 1 {
			body = []byte("Sounds good.")
		}
		rexui.MessageBubble(k, ui.KeyN(k.Context().ParentKey(), uint64(i)), body, nil, rexui.MessageOpts{BubbleOpts: v, Styles: &d.styles[0]})
	}
	rexui.TypingIndicator(k, "Paolo is typing", rexui.TypingOpts{Still: true, Phase: 0.2})
	k.EndColumn()
}

func chatRows(s *State) {
	k := s.k
	k.NextGap(0)
	k.Column("rows")
	rexui.ChatListRow(k, "r1", "Ada Lovelace", "I booked the place by the river, and Paolo says he will bring the notes along.", "10:42", rexui.ChatRowOpts{Unread: 3, Presence: rexui.PresenceOnline})
	rexui.ChatListRow(k, "r2", "Design team", "Alan: shipped the new icons", "Yesterday", rexui.ChatRowOpts{Selected: true})
	rexui.ChatListRow(k, "r3", "Grace Hopper", "typing", "09:15", rexui.ChatRowOpts{Typing: true, Muted: true, Unread: 120})
	rexui.ChatListRow(k, "r4", "Compact row", "", "Mon", rexui.ChatRowOpts{Compact: true})
	k.EndColumn()
}

var d7aSamples = []Sample{
	{"combo", 160, 24, combo(0, 0, false), at(4, 12, shapeW)},
	{"combo-focused", 160, 24, combo(1, focused, false), Spec{Probe: [2]float32{4, 12}, What: shapeW | MeasureRing}},
	{"combo-disabled", 160, 24, combo(2, 0, true), at(4, 12, shapeW)},
	{"token", 200, 24, token(0, 0, false), at(4, 12, shapeW)},
	{"token-focused", 200, 24, token(1, focused, false), Spec{Probe: [2]float32{4, 12}, What: shapeW | MeasureRing}},
	{"token-disabled", 200, 24, token(2, 0, true), at(4, 12, shapeW)},
	{"token-chip", 200, 24, token(0, 0, false), Spec{Probe: [2]float32{14, 6}, What: shapeW | MeasureLabel, Inside: true, Label: [4]float32{26, 5, 62, 20}}},
	{"combo-button", 160, 24, combo(0, 0, false), Spec{Probe: [2]float32{136, 7}, What: shapeW, Inside: true, Tol: 4}},
	{"color-well", 48, 24, colorWell(0, 0, false), at(1.5, 12, shapeW)},
	{"color-well-swatch", 48, 24, colorWell(0, 0, false), Spec{Probe: [2]float32{24, 12}, What: MeasureW | MeasureH | MeasureRadius, Inside: true, Tol: 40}},
	{"color-well-disabled", 48, 24, colorWell(1, 0, true), at(1.5, 12, shapeW)},

	{"badges", 300, 24, sized(300, 24, badges), skip("designed: AppKit draws no badge offscreen")},
	{"avatars", 330, 40, sized(330, 40, avatars), skip("designed: no AppKit control")},
	{"skeletons", 420, 84, sized(420, 84, skeletons), skip("designed: no AppKit control")},
	{"empty-state", 300, 150, sized(300, 150, func(s *State) {
		rexui.EmptyState(s.k, "No messages", rexui.EmptyStateOpts{Message: "Start a conversation to see it here.", Action: "New Message"})
	}), skip("designed: no AppKit control")},
	{"banner-info", 420, 44, bannerSample(rexui.BannerInfo, "Software update available", "", "", false), skip("designed: no AppKit control")},
	{"banner-success", 420, 44, bannerSample(rexui.BannerSuccess, "Saved", "", "", true), skip("designed: no AppKit control")},
	{"banner-warning", 420, 62, bannerSample(rexui.BannerWarning, "Low disk space", "Free up room to keep syncing.", "Manage", true), skip("designed: no AppKit control")},
	{"banner-error", 420, 62, bannerSample(rexui.BannerError, "Upload failed", "The file is too large.", "Retry", true), skip("designed: no AppKit control")},
	{"toast", 440, 110, sized(440, 110, toastSample), skip("designed: no AppKit control")},
	{"popover-below", 300, 190, sized(300, 190, popoverSample(rexui.PopoverBelow)), skip("an NSPopover is a window the window server composites: measured live, popover.sh and TestPopoverMatchesAppKit")},
	{"popover-above", 300, 190, sized(300, 190, popoverSample(rexui.PopoverAbove)), skip("an NSPopover is a window the window server composites: measured live, popover.sh and TestPopoverMatchesAppKit")},
	{"chat", 400, 380, sized(400, 380, chatSample), skip("designed after Messages: no AppKit control")},
	{"chat-rows", 300, 220, sized(300, 220, chatRows), skip("designed after Messages: no AppKit control")},
	{"completion-list", 260, 140, sized(260, 140, completionSample), skip("designed: no AppKit control")},
}

// d7aPage is the grid page of the D7a samples that have no AppKit
// counterpart (Pages).
var d7aPage = []string{
	"badges", "avatars", "skeletons", "empty-state",
	"banner-info", "banner-success", "banner-warning", "banner-error", "toast",
	"popover-below", "popover-above", "chat", "chat-rows", "completion-list",
}

func init() {
	Samples = append(Samples, d7aSamples...)
}
