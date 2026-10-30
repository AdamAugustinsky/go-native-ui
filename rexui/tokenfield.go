package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// TokenFieldState is a token field's state, owned by the app (the tokens
// themselves are the app's []string).
type TokenFieldState struct {
	// Input is the text being typed after the tokens. Call Input.Reserve(64)
	// once to make typing in it allocation-free.
	Input ui.TextBuffer
	// Sel is the selected token, when HasSel.
	Sel    int
	HasSel bool

	tokens  *[]string // the app's list, for the field's commands (tokenCmd)
	changed bool      // the commands added or removed a token this frame
}

// TokenFieldOpts are a token field's options, passed by value.
type TokenFieldOpts struct {
	// Size is the control size (the field's height and radius).
	Size theme.Size
	// Placeholder shows while there are no tokens and no text.
	Placeholder string
}

// TokenRecipe is the look of token fields (NSTokenField on macOS 27.2,
// measured on the Mac mini): a text field's body (FieldLook) holding tokens,
// rounded rectangles of the system blue's tint 15 pt high with the text 16.5 pt
// in from each end, 6.5 pt apart, the first 10 pt in from the field's edge.
type TokenRecipe struct {
	// Width is the width of a token field that the kit's NextWidth does not
	// size, in points (AppKitRef's frame: 200).
	Width float32
	// TokenH, PadX and Radius are a token's height, the space between its
	// ends and its text, and its corner radius; Gap is the space between
	// tokens, LineGap the one between lines, Inset the first token's distance
	// from the field's start, and MinInput the narrowest the typing area gets
	// before it wraps to a line of its own, all in points.
	TokenH, PadX, Radius, Gap, LineGap, Inset, MinInput float32
	// PadY is the field's padding above and below its lines, in points; a
	// line is the text's height (16), so a field of one line is 24 high.
	PadY float32
	// Fill is a token's fill (#ABD8FF over white, #0F3F63 over the dark
	// field measured: the blue at 33 %, whatever the accent), Text its label's colour; Selected
	// and SelectedInactive are a selected token's fills (the list selection's,
	// emphasised in a key window), OnSelected its text.
	Fill, Text, Selected, SelectedInactive, OnSelected Tint
}

// TokenLook is the token fields' recipe.
var TokenLook = TokenRecipe{
	Width:  200,
	TokenH: 15, PadX: 16.5, Radius: 1.75, Gap: 6.5, LineGap: 1, Inset: 10, MinInput: 24, PadY: 4,
	Fill: OfAlpha(theme.Blue, 0.33, 0.33), Text: Of(theme.Label),
	Selected: Of(theme.Selection), SelectedInactive: Of(theme.SelectionUnemph), OnSelected: Of(theme.OnSelection),
}

// TokenFieldSignal is what a token field reports.
type TokenFieldSignal struct {
	base.FieldSignal
	// Tokens: the app's list changed in this frame (a token was added or
	// removed): the new list is *tokens.
	Tokens bool
}

// TokenField builds a token field: a text field that turns what is typed into
// tokens. Enter or a comma ends a token (pasted text is split at its commas); Backspace (or Left, at the start of an
// empty field) selects the last token and Backspace or Delete removes the
// selected one; Left and Right move between tokens, Escape and typing leave
// them. A click selects a token. The field wraps its tokens onto more lines
// as NSTokenField does. *tokens is the app's list, which the field appends to
// and removes from (the one allocation of a new token is its string).
//
// Editing a token in place (a double click) is not offered; remove it and type
// it again.
//
//	tf := rexui.TokenField(k, "Recipients", &v.tf, &v.to, rexui.TokenFieldOpts{})
//	if tf.Tokens { v.validate() }
func TokenField(k *base.Kit, label string, st *TokenFieldState, tokens *[]string, o TokenFieldOpts) TokenFieldSignal {
	c, r := k.Context(), k.Theme()
	rc, fl := &TokenLook, &FieldLook
	sc := k.Scale()
	s := &r.Sizes[o.Size%theme.NumSizes]
	display, ident := ui.SplitLabel(label)
	wkey := ui.HashString(c.ParentKey(), ident)
	flowKey := ui.HashString(wkey, "tokens")
	fkey := ui.HashString(flowKey, "input") // the key of c.TextField("input") in the flow
	buf := &st.Input
	out := TokenFieldSignal{}
	toks := *tokens

	// the keys the tokens take are the field's commands (tokenCmd), offered
	// as the field is built; typing leaves a selected token
	focused := !k.Disabled() && c.FocusKey() == fkey
	if st.HasSel && st.Sel >= len(toks) {
		st.HasSel = false
	}
	if focused && st.HasSel && c.Input().NText > 0 {
		st.HasSel = false
	}
	st.tokens, st.changed = tokens, false

	// the field's body: wide as the parent unless sized; lines of tokens
	pw := pt(sc, rc.Width)
	if pe, ok := k.PendingWidth(); ok && pe.Kind != ui.SizeAuto {
		k.NextWidth(pe)
	} else {
		k.NextWidth(ui.Px(pw))
	}
	padY := pt(sc, rc.PadY)
	inset, padR := pt(sc, rc.Inset), pt(sc, fl.InsetX)
	radius := s.FieldRadius
	vis := k.VisualOf(fkey, base.HoverOff)
	k.ApplySize(ui.Pct(1), ui.Children(1))
	c.NextBG(fl.Fill.RGBA(r))
	c.NextRadius(radius)
	c.NextPadding(inset, padY, padR, padY)
	c.NextAxis(ui.AxisY)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(display)
	flags := ui.DrawBackground | disabledFlag(k)
	wrap := c.BoxKeyed(flags, wkey, nil)
	wr := c.Rect(wrap) // as the previous frame laid it out
	ww := wr[2] - wr[0]
	c.Push(wrap)

	c.NextHeight(ui.Children(1))
	c.BeginFlow("tokens", ui.FlowOpts{GapX: pt(sc, rc.Gap), GapY: pt(sc, rc.LineGap), Cross: ui.CrossCenter})
	active := !vis.Inactive
	font := s.Font.Font
	var lastRight float32
	for i, t := range toks {
		tk := ui.KeyN(wkey, uint64(100+i))
		selected := st.HasSel && st.Sel == i
		fill, fg := rc.Fill.RGBA(r), rc.Text.RGBA(r)
		if selected {
			fill, fg = rc.SelectedInactive.RGBA(r), rc.Text.RGBA(r)
			if active {
				fill, fg = rc.Selected.RGBA(r), rc.OnSelected.RGBA(r)
			}
		}
		if k.Disabled() {
			fill, fg = r.Colors[theme.Fill2], r.Colors[theme.TextDisabled]
		}
		tw := theme.Snap(k.TextWidth(font, t)) + 2*pt(sc, rc.PadX)
		h := pt(sc, rc.TokenH)
		c.NextBG(fill)
		c.NextRadius(pt(sc, rc.Radius))
		c.NextSize(ui.Px(tw), ui.Px(h))
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		c.NextRole(ui.RoleListItem)
		c.NextSemName(t)
		c.NextSemPos(i, len(toks))
		var state ui.SemState
		if selected {
			state = ui.SemSelected
		}
		c.NextSemState(state)
		tf := ui.DrawBackground
		if !k.Disabled() {
			tf |= ui.Clickable
		}
		id := c.BoxKeyed(tf, tk, nil)
		c.Push(id)
		c.NextSize(ui.TextSize(0), ui.Px(h))
		c.NextFont(font)
		c.NextTextColor(fg)
		c.BoxKeyed(ui.DrawText, ui.KeyN(tk, 1), base.StrBytes(t))
		c.Pop()
		if !k.Disabled() && c.Signal(id).Clicked {
			st.Sel, st.HasSel = i, true
			c.SetFocus(fkey)
			c.SetFocusVisible(false)
		}
		lastRight = c.Rect(id)[2]
	}
	// the typing area takes what its line has left, else a line to itself
	contentR := wr[2] - padR
	room := contentR - lastRight - pt(sc, rc.Gap)
	inputW := contentR - (wr[0] + inset)
	if len(toks) > 0 && room >= pt(sc, rc.MinInput) {
		inputW = room
	}
	if ww <= 0 || inputW < pt(sc, rc.MinInput) {
		inputW = pt(sc, rc.MinInput)
	}
	lineH := s.Font.Line
	tc := fl.Text.RGBA(r)
	if vis.Disabled {
		tc = fl.TextDisabled.RGBA(r)
	}
	if k.Disabled() {
		c.NextFlags(ui.Disabled)
	}
	c.NextBG(0)
	c.NextSize(ui.Px(inputW), ui.Px(lineH))
	c.NextPadding(0, 0, 0, 0)
	c.NextFont(font)
	c.NextTextColor(tc)
	c.NextRole(ui.RoleTextField)
	c.NextSemName(display)
	c.NextFieldNoHover()
	if !k.Disabled() {
		c.NextFieldCommander((*tokenCmd)(st), ",")
	}
	fs := c.TextField("input", buf)
	out.Tokens = st.changed
	out.FieldSignal = base.FieldSignal{
		Signal:    base.Signal{ID: wrap, Hovering: fs.Hovering, Focused: fs.Focused, Changed: fs.Changed, Clicked: fs.Clicked},
		Submitted: fs.Submitted, Cancelled: fs.Cancelled, Pressed: fs.Pressed, Key: fkey,
	}
	if buf.Len() == 0 && len(toks) == 0 && o.Placeholder != "" && !k.Composing(out.FieldSignal) {
		pc := fl.Placeholder.RGBA(r)
		if vis.Disabled {
			pc = fl.TextDisabled.RGBA(r)
		}
		theme.At(c, 0, 0, 0, lineH)
		c.NextSize(ui.TextSize(0), ui.Px(lineH))
		c.NextFont(font)
		c.NextTextColor(pc)
		c.BoxKeyed(ui.DrawText|base.Floating, ui.KeyN(wkey, 2), base.StrBytes(o.Placeholder))
	}
	c.EndFlow()
	// the outline just outside the frame, a floating child of the field's body
	if ww > 0 {
		bw := pt(sc, fl.BorderW)
		theme.At(c, -bw, -bw, ww+2*bw, wr[3]-wr[1]+2*bw)
		c.NextBorder(fl.Border.RGBA(r), bw)
		c.NextRadius(radius + bw)
		c.NextAuditExempt(ui.AuditOverflow.Mask())
		c.BoxKeyed(ui.DrawBorder|base.Floating, ui.KeyN(wkey, 5), nil)
	}
	c.Pop()

	if out.Tokens {
		c.RequestFrame() // the tokens drawn above are the ones before the command
	}
	if out.Tokens {
		st.HasSel = false
	}

	k.FocusRing(wrap, vis.TextEntry(), radius) // (a text-entry control rings however it got the focus)
	return out
}

// tokenCmd is a TokenFieldState as the field commander of its input: the keys
// of the tokens, as the keys or the system's edit actions, in the order they
// arrive. Left and Right move between the selected tokens (Left at the start of
// the text selects the last); Backspace at the start selects the last token
// and, with one selected, removes it wherever the caret is, as does Delete (the
// text is not touched while a token is selected); Escape leaves
// a selected token; Enter makes what is typed a token; a comma makes the text
// before it a token, wherever it is typed and however many come in one batch.
type tokenCmd TokenFieldState

// FieldCommand implements ui.FieldCommander.
func (s *tokenCmd) FieldCommand(cmd ui.FieldCommand, r rune) bool {
	st := (*TokenFieldState)(s)
	toks := *st.tokens
	buf := &st.Input
	switch cmd {
	case ui.FieldInsertSeparator:
		if tokenCommit(st.tokens, buf, buf.Caret()) {
			st.changed = true
		}
		buf.Remove(0, buf.Caret())
		st.HasSel = false
		return true
	case ui.FieldInsertNewline:
		if buf.Len() == 0 {
			return false // the field's own Enter: Submitted
		}
		if tokenCommit(st.tokens, buf, buf.Len()) {
			st.changed = true
		}
		buf.Reset()
		st.HasSel = false
		return true
	case ui.FieldMoveLeft:
		switch {
		case st.HasSel:
			st.Sel = max(st.Sel-1, 0)
			return true
		case len(toks) > 0 && buf.Caret() == 0 && !buf.HasSelection():
			st.Sel, st.HasSel = len(toks)-1, true
			return true
		}
	case ui.FieldMoveRight:
		if st.HasSel {
			if st.Sel+1 < len(toks) {
				st.Sel++
			} else {
				st.HasSel = false
			}
			return true
		}
	case ui.FieldDeleteBackward, ui.FieldDeleteForward:
		switch {
		case st.HasSel:
			i := st.Sel
			*st.tokens = append(toks[:i], toks[i+1:]...)
			st.changed = true
			st.HasSel = len(*st.tokens) > 0
			st.Sel = min(st.Sel, len(*st.tokens)-1)
			return true
		case cmd == ui.FieldDeleteBackward && len(toks) > 0 && buf.Caret() == 0 && !buf.HasSelection():
			st.Sel, st.HasSel = len(toks)-1, true
			return true
		}
	case ui.FieldCancel:
		if st.HasSel {
			st.HasSel = false
			return true
		}
	}
	return false
}

// tokenCommit appends the text of buf[:end], trimmed of white space, to
// *tokens as a new token; it reports whether there was one.
func tokenCommit(tokens *[]string, buf *ui.TextBuffer, end int) bool {
	b := buf.Bytes()[:end]
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	if len(b) == 0 {
		return false
	}
	*tokens = append(*tokens, string(b))
	return true
}
