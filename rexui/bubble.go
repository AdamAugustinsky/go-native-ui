package rexui

import (
	"math"

	"gonativeui/gpu"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// BubbleVariant selects a message bubble's look.
type BubbleVariant uint8

const (
	// BubbleIncoming is a message from someone else: the primary text on the
	// system fill, at the start of the column. It is the zero value.
	BubbleIncoming BubbleVariant = iota
	// BubbleOutgoing is the user's own message: white text on the accent, at
	// the end of the column.
	BubbleOutgoing
	// BubbleSystem is an event of the conversation ("Paolo joined"): Caption 1
	// in the secondary label colour, centred, with no bubble.
	BubbleSystem

	// NumBubbleVariants is the number of variants.
	NumBubbleVariants
)

// BubbleStatus is where an outgoing message is.
type BubbleStatus uint8

const (
	// BubbleSent is a message that was delivered (the zero value).
	BubbleSent BubbleStatus = iota
	// BubbleSending is a message on its way: drawn in a paler accent.
	BubbleSending
	// BubbleFailed is a message that was not delivered: a red badge with an
	// exclamation mark before the bubble.
	BubbleFailed
)

// BubbleOpts are a bubble's options, passed by value.
type BubbleOpts struct {
	// Variant is the look; Status where an outgoing message is.
	Variant BubbleVariant
	Status  BubbleStatus
	// Tail marks the last bubble of a group from one sender: its corner on the
	// sender's side is squared, as Messages' tail is. The others of a group are
	// all round.
	Tail bool
	// Selected draws a ring round the bubble (the message is selected as a whole,
	// for a reply or a menu), Highlighted tints it toward the find colour (a
	// search hit).
	Selected, Highlighted bool
	// ColumnW is the width of the column the bubble lies in, in points, which
	// limits it to BubbleLook.MaxShare of that; 0 takes the width of the parent
	// as the previous frame laid it out (a bubble built first in a new parent
	// is as wide as its text wants for one frame).
	ColumnW float32
	// Name is the accessible name of the message ("Paolo, 10:42"); empty
	// takes "Message" or "Sent message".
	Name string
	// Fill, when its alpha is not 0, replaces the variant's fill (0xRRGGBBAA,
	// opaque as theirs are: an app's own palette); with FillBottom's alpha
	// not 0 too, the bubble is a vertical gradient from Fill at its top to
	// FillBottom at its bottom. Sending and Highlighted mix into it as into
	// the variant's.
	Fill, FillBottom uint32
	// FindMix, above 0, is how far Highlighted moves the fill toward the find
	// colour in place of the look's BubbleRecipe.FindMix (a highlight that
	// fades out).
	FindMix float32
	// Look is the recipe, nil for BubbleLook.
	Look *BubbleRecipe
	// Curl, when set, gives a Tail bubble Messages' curled tail at its
	// squared corner (BubbleTailPath), filled through this mask (keep one
	// per window and side: gpu.PathMask keeps its texture while the shape
	// stays the same). The row leaves room for the curl on the sender's side,
	// so pass it for every bubble of a column, and they line up.
	Curl *gpu.PathMask
}

// look is o's recipe.
func (o *BubbleOpts) look() *BubbleRecipe {
	if o.Look != nil {
		return o.Look
	}
	return &BubbleLook
}

// BubbleRecipe is the look of message bubbles: Messages' after §3.4 of the
// design (designed: AppKit has no bubble).
type BubbleRecipe struct {
	// PadX and PadY are the padding, Radius the corner radius, TailRadius the
	// squared corner's, MaxShare the widest a bubble gets as a share of its
	// column, and RowGap the space round a row, all in points.
	PadX, PadY, Radius, TailRadius, MaxShare, RowGap float32
	// Incoming is the fill of an incoming bubble: the system fill over the
	// surface under it, so that it is an opaque colour. Outgoing is the accent;
	// Sending the paler fill of a message on its way, mixed from the accent
	// toward the surface by SendingMix; Find the colour a highlighted bubble
	// moves toward by FindMix.
	Incoming, Outgoing, Find Tint
	SendingMix, FindMix      float32
	// Text colours: Incoming, Outgoing and System.
	TextIn, TextOut, TextSystem Tint
	// Ring is a selected bubble's outline, RingW its width in points.
	Ring  Tint
	RingW float32
	// Code is the fill of a code block, CodePad its padding and CodeRadius its
	// radius, in points.
	Code                Tint
	CodePad, CodeRadius float32
}

// BubbleLook is the bubbles' recipe.
var BubbleLook = BubbleRecipe{
	PadX: 12, PadY: 7, Radius: 17, TailRadius: 5, MaxShare: 0.7, RowGap: 2,
	Incoming: Of(theme.Fill1), Outgoing: Of(theme.Accent), Find: Of(theme.FindHighlight),
	SendingMix: 0.45, FindMix: 0.4,
	TextIn: Of(theme.Label), TextOut: Of(theme.OnAccent), TextSystem: Of(theme.Label2),
	Ring: Of(theme.Selection), RingW: 2,
	Code: Of(theme.Fill3), CodePad: 8, CodeRadius: 8,
}

// The styles of a bubble's RichStyles (BubbleRichStyles): style 0 is the body.
const (
	// BubbleStyleLink is a link, BubbleStyleMention a mention (a pill),
	// BubbleStyleCode inline code and BubbleStyleBold bold text.
	BubbleStyleLink = 1 + iota
	BubbleStyleMention
	BubbleStyleCode
	BubbleStyleBold
)

// BubbleRichStyles fills st with the styles of a bubble of variant v, as the
// spans of its body refer to them (BubbleStyleLink and the others): links in
// the link colour (white and underlined on the accent), mentions as a bold
// pill, inline code in the monospace font on the tertiary fill. Style 0, the
// body, takes the bubble's own font and colour.
func BubbleRichStyles(k *base.Kit, v BubbleVariant, st *ui.RichStyles) {
	r := k.Theme()
	body := r.Styles[theme.StyleBody].Font
	bold := k.TextSystem().Font(text.FontDesc{SizePt: 13, Weight: theme.WeightBold, Scale: k.Scale()})
	mono := r.Styles[theme.StyleMono].Font
	link, mention, mbg, code := r.Colors[theme.Link], r.Colors[theme.Accent], theme.WithAlpha(r.Colors[theme.Accent], 0.15), r.Colors[theme.Label]
	if v == BubbleOutgoing {
		link, mention, mbg, code = r.Colors[theme.OnAccent], r.Colors[theme.OnAccent], theme.WithAlpha(r.Colors[theme.OnAccent], 0.25), r.Colors[theme.OnAccent]
	}
	st.Set(0, ui.RichStyle{Font: body})
	st.Set(BubbleStyleLink, ui.RichStyle{Font: body, Color: link, Deco: ui.DecoUnderline})
	st.Set(BubbleStyleMention, ui.RichStyle{Font: bold, Color: mention, BG: mbg, Radius: 4 * k.Scale()})
	st.Set(BubbleStyleCode, ui.RichStyle{Font: mono, Color: code, BG: r.Colors[theme.Fill3], Radius: 3 * k.Scale()})
	st.Set(BubbleStyleBold, ui.RichStyle{Font: bold})
}

// Bubble is a bubble being built: BeginBubble starts it and EndBubble ends it.
type Bubble struct {
	// ID is the bubble's box (the filled shape), 0 for a system message,
	// which has none; Row is the box that holds it and the badge.
	ID, Row ui.ID
	opts    BubbleOpts
	key     ui.Key
	maxW    float32
	plain   bool // no bubble box: a system message, or jumbo emoji
}

// BubbleSignal is what a bubble reports.
type BubbleSignal struct {
	// ID is the bubble (or, for a system message, its text).
	ID ui.ID
}

// bubbleMaxW is the widest a bubble's content may be, in pixels: the share of
// the column, less the padding.
func bubbleMaxW(k *base.Kit, o *BubbleOpts) float32 {
	c := k.Context()
	rc := o.look()
	sc := k.Scale()
	col := pt(sc, o.ColumnW)
	if o.ColumnW <= 0 {
		pr := c.Rect(c.Parent())
		col = pr[2] - pr[0]
	}
	if col <= 0 {
		return 0
	}
	return max(theme.Snap(col*rc.MaxShare)-2*pt(sc, rc.PadX), pt(sc, 24))
}

// BeginBubble starts the row of a message and its bubble in the current parent
// and makes the bubble's box the parent of what is built next, until
// EndBubble: text, an image, a code block, a file; whatever the message holds.
// The row fills the parent's width and places the bubble at the end of it for
// an outgoing message, at the start for an incoming one and in the middle for a
// system message; the bubble takes the width its content needs, up to
// BubbleLook.MaxShare of the column. key tells the bubble from its neighbours
// (an item's id: ui.KeyN(c.ParentKey(), id)).
//
//	b := rexui.BeginBubble(k, key, rexui.BubbleOpts{Variant: rexui.BubbleOutgoing, Tail: true})
//	… the message's content …
//	rexui.EndBubble(k, &b)
func BeginBubble(k *base.Kit, key ui.Key, o BubbleOpts) Bubble {
	return beginBubble(k, key, o, false)
}

// beginBubble is BeginBubble, with plain leaving out the bubble's box (the row
// alone, for the jumbo emoji of a message that has no bubble).
func beginBubble(k *base.Kit, key ui.Key, o BubbleOpts, plain bool) Bubble {
	c, r := k.Context(), k.Theme()
	rc := o.look()
	sc := k.Scale()
	v := o.Variant % NumBubbleVariants
	b := Bubble{opts: o, key: key, plain: plain || v == BubbleSystem}
	b.opts.Variant = v
	b.maxW = bubbleMaxW(k, &o)

	just := ui.JustifyStart
	switch v {
	case BubbleOutgoing:
		just = ui.JustifyEnd
	case BubbleSystem:
		just = ui.JustifyCenter
	}
	k.ApplySize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossEnd) // the failed badge and the bubble's bottom line up
	c.NextJustify(just)
	c.NextGap(pt(sc, 6))
	var padL, padR float32
	if o.Curl != nil && v != BubbleSystem {
		if v == BubbleOutgoing {
			padR = pt(sc, curlReach)
		} else {
			padL = pt(sc, curlReach)
		}
	}
	c.NextPadding(padL, pt(sc, rc.RowGap/2), padR, pt(sc, rc.RowGap/2))
	b.Row = c.BoxKeyed(0, ui.KeyN(key, 0), nil)
	c.Push(b.Row)
	if v == BubbleOutgoing && o.Status == BubbleFailed {
		Badge(k, "!", BadgeOpts{Variant: BadgeCount, Name: str.NotDelivered})
	}
	if b.plain {
		return b
	}

	fill, bottom := bubbleFill(r, &o)
	radius := pt(sc, rc.Radius)
	tail := pt(sc, rc.TailRadius)
	tl, tr, br, bl := radius, radius, radius, radius
	if o.Tail {
		if v == BubbleOutgoing {
			br = tail
		} else {
			bl = tail
		}
	}
	if o.Selected {
		c.NextBorder(rc.Ring.RGBA(r), pt(sc, rc.RingW))
	}
	c.NextBG4(fill, fill, bottom, bottom)
	c.NextRadii(tl, tr, br, bl)
	c.NextPadding(pt(sc, rc.PadX), pt(sc, rc.PadY), pt(sc, rc.PadX), pt(sc, rc.PadY))
	c.NextAxis(ui.AxisY)
	c.NextRole(ui.RoleGroup)
	name := o.Name
	if name == "" {
		name = [...]string{str.Message, str.SentMessage, ""}[v]
	}
	c.NextSemName(name)
	switch o.Status {
	case BubbleSending:
		c.NextSemValueBytes(base.StrBytes(str.Sending))
	case BubbleFailed:
		c.NextSemValueBytes(base.StrBytes(str.NotDelivered))
	}
	if o.Selected {
		c.NextSemState(ui.SemSelected)
	}
	flags := ui.DrawBackground | disabledFlag(k)
	if o.Selected {
		flags |= ui.DrawBorder
	}
	b.ID = c.BoxKeyed(flags, ui.KeyN(key, 1), nil)
	c.Push(b.ID)
	if o.Tail && o.Curl != nil {
		bubbleCurl(k, &b, bottom)
	}
	return b
}

// The curled tail (BubbleTailPath) in points: its box, of which curlOverlap
// lies over the bubble's squared corner (of radius curlCorner) and curlReach
// beyond its side.
const (
	curlW, curlH = 14, 16
	curlOverlap  = 6
	curlReach    = curlW - curlOverlap
	curlCorner   = 5
)

// curlPath is the scratch the tails are built in (main thread).
var curlPath gpu.Path

// BubbleTailPath builds into p Messages' curled tail of a bubble at scale
// (pixels per point), for the bubble's bottom right corner (left: its bottom
// left), and returns the size of its box in pixels: the box's bottom is the
// bubble's, and its side lies 6 pt (in whole pixels) inside the bubble's
// side. The tail leaves the side 16 pt above the bottom, curls out to a tip
// 8 pt beyond it at the bottom and comes back under the bubble to fill what
// the bubble's squared corner (BubbleOpts.Tail: BubbleLook's 5 pt radius)
// leaves out, overlapping the bubble by a pixel at most, so a bubble with a
// gradient shows no seam. Fill it in the bubble's bottom colour
// (ui.Context.NextPath).
func BubbleTailPath(p *gpu.Path, scale float32, left bool) (w, h float32) {
	ov := theme.Snap(curlOverlap * scale)
	h = theme.Snap(curlH * scale)
	sx, sy := ov/curlOverlap, h/curlH // the side and the bottom on whole pixels
	w = float32(math.Ceil(float64(curlW * sx)))
	x := func(v float32) float32 {
		if left {
			return w - v*sx
		}
		return v * sx
	}
	// the bubble's squared corner: its arc, centred (cx, cy) in pixels, of
	// radius r; the tail covers what the arc leaves out and a pixel inside it
	// (no seam where the two edges meet)
	r := theme.Snap(curlCorner * scale)
	cx, cy, ri := ov-r, h-r, max(r-1, 0)
	const k = 0.5523 // a quarter circle's cubic
	p.Reset()
	p.MoveTo(x(curlOverlap), 0)
	p.CubeTo(x(curlOverlap), 9*sy, x(8), 14*sy, x(curlW), h) // out to the tip
	p.CubeTo(x(10), h, x(6), 14.4*sy, x(cx/sx), h)           // back under the bubble
	p.LineTo(x(cx/sx), cy+ri)
	p.CubeTo(x((cx+k*ri)/sx), cy+ri, x((cx+ri)/sx), cy+k*ri, x((cx+ri)/sx), cy) // round the corner
	p.Close()
	p.MoveTo(w, h) // (the mask is the box: w×h)
	return w, h
}

// bubbleCurl builds the curled tail of bubble b, in fill, at its bottom
// corner on the sender's side: a floating child placed from the bubble's
// size in the previous layout (a new bubble shows it from its second frame,
// which ui settles by itself).
func bubbleCurl(k *base.Kit, b *Bubble, fill uint32) {
	c := k.Context()
	sc := k.Scale()
	r := c.Rect(b.ID)
	w, h := r[2]-r[0], r[3]-r[1]
	if w <= 0 || h <= 0 {
		return
	}
	left := b.opts.Variant != BubbleOutgoing
	BubbleTailPath(&curlPath, sc, left)
	tw, th := c.NextPath(b.opts.Curl, &curlPath)
	if tw == 0 {
		return
	}
	x := w - pt(sc, curlOverlap)
	if left {
		x = pt(sc, curlOverlap) - tw
	}
	theme.At(c, x, h-th, tw, th)
	c.NextTextColor(fill)
	c.NextAuditExempt(ui.AuditOverflow.Mask() | ui.AuditCovered.Mask()) // it lies outside the bubble, over its corner, on purpose
	c.BoxKeyed(ui.DrawImage|base.Floating, ui.KeyN(b.key, 7), nil)
}

// bubbleFill is the bubble's fill at its top and its bottom: opaque, from
// the variant or the app's Fill, the status and the highlight.
func bubbleFill(r *theme.Resolved, o *BubbleOpts) (top, bottom uint32) {
	rc := o.look()
	surface := r.Colors[theme.Pane] | 0xFF
	switch {
	case o.Fill&0xFF != 0:
		top, bottom = o.Fill, o.Fill
		if o.FillBottom&0xFF != 0 {
			bottom = o.FillBottom
		}
	case o.Variant == BubbleOutgoing:
		top = rc.Outgoing.RGBA(r)
		bottom = top
	default:
		top = theme.Over(rc.Incoming.RGBA(r), surface)
		bottom = top
	}
	if o.Variant == BubbleOutgoing && o.Status == BubbleSending {
		top, bottom = theme.MixRGBA(top, surface, rc.SendingMix), theme.MixRGBA(bottom, surface, rc.SendingMix)
	}
	if o.Highlighted {
		mix := rc.FindMix
		if o.FindMix > 0 {
			mix = o.FindMix
		}
		find := rc.Find.RGBA(r)
		top, bottom = theme.MixRGBA(top, find, mix), theme.MixRGBA(bottom, find, mix)
	}
	return top, bottom
}

// EndBubble ends the bubble BeginBubble started.
func EndBubble(k *base.Kit, b *Bubble) BubbleSignal {
	c := k.Context()
	if !b.plain {
		c.Pop() // the bubble
	}
	c.Pop() // the row
	return BubbleSignal{ID: b.ID}
}

// MessageOpts are a message bubble's options.
type MessageOpts struct {
	BubbleOpts
	// Styles is the table of the spans' styles (BubbleRichStyles fills one);
	// nil is the body style alone.
	Styles *ui.RichStyles
	// Item and Part place the text in the selection scope the message is built
	// in (ui.Context.BeginSelection), as ui.RichOpts do: the app's id of the
	// message and, for a message of several texts, which one this is.
	Item uint64
	Part uint16
	// Code shows body as a block of code: the monospace font on the tertiary
	// fill, no wrapping of its own beyond the bubble's width.
	Code bool
}

// MessageSignal is what a message bubble reports.
type MessageSignal struct {
	BubbleSignal
	// Text is the rich text's signal: the link or mention hovered and
	// clicked, and whether an emoji-only message was drawn large.
	Text ui.RichSignal
}

// MessageBubble builds a message: a bubble holding body as rich text (C2:
// spans with link and mention hit regions, emoji-only messages drawn large and
// without a bubble), selectable together with the other messages built in the
// same selection scope (C4). Built in a virtual list inside the scope, a drag
// selects across bubbles, double and triple clicks pick words and paragraphs,
// and copying gives the plain text of the messages between the ends.
//
//	sig := rexui.MessageBubble(k, ui.KeyN(c.ParentKey(), id), body, spans, rexui.MessageOpts{
//		BubbleOpts: rexui.BubbleOpts{Variant: rexui.BubbleOutgoing, Tail: last},
//		Styles: &styles, Item: id,
//	})
//	if sig.Text.Clicked != 0 { v.open(sig.Text.Clicked) }
func MessageBubble(k *base.Kit, key ui.Key, body []byte, spans []ui.Span, o MessageOpts) MessageSignal {
	c, r := k.Context(), k.Theme()
	rc := o.look()
	sc := k.Scale()
	v := o.Variant % NumBubbleVariants
	jumbo := v != BubbleSystem && !o.Code && text.EmojiOnly(body) > 0 && text.EmojiOnly(body) <= text.MaxJumboEmoji
	bo := o.BubbleOpts
	var b Bubble
	var sig MessageSignal
	if jumbo { // emoji alone are drawn large, with no bubble round them
		b = beginBubble(k, key, bo, true)
	} else {
		b = BeginBubble(k, key, bo)
	}
	st := &r.Styles[theme.StyleBody]
	col := rc.TextIn.RGBA(r)
	switch v {
	case BubbleOutgoing:
		col = rc.TextOut.RGBA(r)
	case BubbleSystem:
		st = &r.Styles[theme.StyleCaption1]
		col = rc.TextSystem.RGBA(r)
	}
	if k.Disabled() {
		col = r.Colors[theme.TextDisabled]
	}
	ro := ui.RichOpts{MaxW: b.maxW, Item: o.Item, Part: o.Part}
	if v == BubbleSystem {
		ro.MaxW = 0
	}
	if jumbo {
		ro.EmojiLarge = true
		ro.JumboFont = k.TextSystem().Font(text.FontDesc{SizePt: 13 * text.JumboScale, Weight: theme.WeightRegular, Scale: sc})
	}
	if o.Code {
		c.NextBG(rc.Code.RGBA(r))
		c.NextRadius(pt(sc, rc.CodeRadius))
		c.NextPad(pt(sc, rc.CodePad))
		inner := c.BoxKeyed(ui.DrawBackground, ui.KeyN(key, 2), nil)
		c.Push(inner)
		st = &r.Styles[theme.StyleMono]
		ro.MaxW = max(b.maxW-2*pt(sc, rc.CodePad), 0)
	}
	c.NextFont(st.Font)
	c.NextTextColor(col)
	if v == BubbleSystem {
		c.NextAlign(ui.AlignCenter)
	}
	sig.Text = c.RichText(ui.KeyN(key, 3), body, spans, o.Styles, ro)
	if o.Code {
		c.Pop()
	}
	sig.BubbleSignal = EndBubble(k, &b)
	if v == BubbleSystem || jumbo {
		sig.ID = sig.Text.ID
	}
	return sig
}
