package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ComboState is a combo box's state, owned by the app: whether its list is
// open and the list's highlight.
type ComboState struct {
	// Open: the list of choices shows.
	Open bool
	// List is the list's state (CompletionState).
	List CompletionState

	// what the field's commands need and leave (see comboCmd)
	items    []string
	text     *ui.TextBuffer
	accepted bool // the field's commands chose row pick
	pick     int
}

// ComboOpts are a combo box's options, passed by value.
type ComboOpts struct {
	// Size is the control size.
	Size theme.Size
	// Placeholder shows while the text is empty.
	Placeholder string
}

// ComboRecipe is the look of combo boxes (NSComboBox on macOS 27.2, measured
// on the Mac mini): a text field's body (FieldLook) with a rounded button at
// its end that holds a down chevron.
type ComboRecipe struct {
	// Width is the width of a combo box the kit's NextWidth does not size, in
	// points (AppKitRef's frame: 160).
	Width float32
	// ButtonW and ButtonH are the button's size, ButtonRight its distance from
	// the field's end and Radius its corner radius, in points (23.5 × 17.5,
	// 4 and 4.5 measured; centred vertically).
	ButtonW, ButtonH, ButtonRight, Radius float32
	// Button is the button's fill (#E9E9E9 over white and #2C2C2C over the dark
	// field measured: the secondary fill), ButtonOpen while the list shows,
	// and Chevron the chevron's colour.
	Button, ButtonOpen, Chevron Tint
}

// ComboLook is the combo boxes' recipe.
var ComboLook = ComboRecipe{
	Width: 160, ButtonW: 23.5, ButtonH: 17.5, ButtonRight: 4, Radius: 4,
	Button: Of(theme.Fill2), ButtonOpen: Of(theme.Fill1), Chevron: Of(theme.Label),
}

// ComboSignal is what a combo box reports.
type ComboSignal struct {
	base.FieldSignal
	// Open: the list of choices is showing.
	Open bool
	// Chosen is the choice picked this frame (a click, or Enter on the
	// highlighted row), -1 for none; the text was set to it.
	Chosen int
}

// Combobox builds a combo box: a text field with a list of choices that the
// button, or Down with the focus, opens. The field is an ordinary one (typing
// anything is allowed); the list highlights the first choice that begins with
// what has been typed, and a click, or Enter on a highlighted row, puts that
// choice in the field and closes the list. Up and Down move through the list
// (the field keeps the focus and the text does not move meanwhile); Escape and
// a press elsewhere close it; Tab takes the highlighted row and Shift+Tab does
// not, and both close the list and let the focus move on. The keys are the
// field's commands (ui.FieldCommander), so the arrows work as the system's edit
// actions too, as macOS sends them. The list is a CompletionList below the
// field that takes no focus.
//
// The state is the app's: st holds whether the list is open, and buf the text.
//
//	cb := rexui.Combobox(k, "Font", &v.fontState, &v.font, fonts, rexui.ComboOpts{})
//	if cb.Chosen >= 0 { v.applyFont() }
func Combobox(k *base.Kit, label string, st *ComboState, buf *ui.TextBuffer, items []string, o ComboOpts) ComboSignal {
	c, r := k.Context(), k.Theme()
	rc, fl := &ComboLook, &FieldLook
	sc := k.Scale()
	s := &r.Sizes[o.Size%theme.NumSizes]
	fkey := base.FieldKey(c.ParentKey(), label)
	n := len(items)
	out := ComboSignal{Chosen: -1}

	// the keys the list takes are offered to the field's commander (comboCmd)
	st.items, st.text, st.accepted = items, buf, false

	if _, ok := k.PendingWidth(); !ok {
		k.NextWidth(k.Fixed(rc.Width))
	}
	ti := k.BeginTextInput(label, theme.Snap(s.FieldHeight), base.HoverOff)
	h := ti.H
	font := s.Font.Font
	radius := s.FieldRadius
	padL := pt(sc, fl.InsetX)
	btnW, btnH := pt(sc, rc.ButtonW), pt(sc, rc.ButtonH)
	padR := pt(sc, rc.ButtonRight) + btnW + pt(sc, 2)
	tc := fl.Text.RGBA(r)
	if ti.Disabled {
		tc = fl.TextDisabled.RGBA(r)
	}
	c.NextBG(fl.Fill.RGBA(r))
	c.NextSize(ui.Pct(1), ui.Px(h))
	c.NextRadius(radius)
	c.NextPadding(padL, 0, padR, 0)
	c.NextFont(font)
	c.NextTextColor(tc)
	c.NextSemState(comboState(st))
	if n > 0 && !ti.Disabled {
		c.NextFieldCommander((*comboCmd)(st), "")
	}
	res := k.TextInput(&ti, buf, ui.RoleComboBox, false)
	out.FieldSignal = res

	w := k.FieldWidth(&ti)
	if w > 0 {
		bw := pt(sc, fl.BorderW)
		theme.At(c, -bw, -bw, w+2*bw, h+2*bw)
		c.NextBorder(fl.Border.RGBA(r), bw)
		c.NextRadius(radius + bw)
		c.BoxKeyed(ui.DrawBorder|base.Floating, ui.KeyN(ti.WrapKey, 5), nil)
	}
	if buf.Len() == 0 && o.Placeholder != "" && !k.Composing(res) {
		pc := fl.Placeholder.RGBA(r)
		if ti.Disabled {
			pc = fl.TextDisabled.RGBA(r)
		}
		theme.At(c, padL, 0, 0, h)
		c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(h))
		c.NextFont(font)
		c.NextTextColor(pc)
		c.BoxKeyed(ui.DrawText|base.Floating, ui.KeyN(ti.WrapKey, 2), base.StrBytes(o.Placeholder))
	}

	// the button
	if w > 0 {
		bkey := ui.KeyN(ti.WrapKey, 6)
		fill, fg := rc.Button.RGBA(r), rc.Chevron.RGBA(r)
		flags := ui.Clickable | ui.ActiveAnimation | base.Floating
		if st.Open || c.ActiveTOf(bkey) > 0.5 {
			fill = rc.ButtonOpen.RGBA(r)
		}
		if ti.Disabled {
			flags = base.Floating
			fg = r.Colors[theme.TextDisabled]
		}
		theme.At(c, theme.Snap(w-pt(sc, rc.ButtonRight)-btnW), theme.Snap((h-btnH)/2), btnW, btnH)
		c.NextBG(fill)
		c.NextRadius(pt(sc, rc.Radius))
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		c.NextRole(ui.RoleButton)
		c.NextSemName(str.ShowOptions)
		b := c.BoxKeyed(flags|ui.DrawBackground, bkey, nil)
		c.Push(b)
		k.Icon(icon.PullDown, fg)
		c.Pop()
		if !ti.Disabled && c.Signal(b).Clicked {
			if st.Open {
				st.Open = false
			} else {
				comboOpen(st, buf, items)
				c.SetFocus(fkey)
			}
		}
	}
	k.FocusRing(res.ID, ti.Visual.TextEntry(), radius) // (a text-entry control rings however it got the focus)
	k.EndTextInput(&ti)

	// typing moves the highlight to the first choice that begins with the text
	if res.Changed && st.Open {
		st.List.Sel = comboMatch(items, buf.Bytes())
	}
	if st.Open && n > 0 && !ti.Disabled {
		anchor := c.RectOf(ti.WrapKey)
		cs := completion(k, str.Options, &st.List, nil, items, anchor, CompletionOpts{Width: (anchor[2] - anchor[0]) / sc}, ui.DismissSemitransient)
		if cs.Chosen >= 0 {
			out.Chosen = cs.Chosen
		}
		if cs.Dismissed {
			st.Open = false
		}
	}
	if st.accepted {
		out.Chosen = st.pick
	}
	if out.Chosen >= 0 && out.Chosen < n {
		buf.SetString(items[out.Chosen])
		out.Changed = true
		st.Open = false
		c.RequestFrame()
	} else {
		out.Chosen = -1
	}
	out.Open = st.Open
	return out
}

// comboCmd is a ComboState as the field commander of its text field: the keys
// the list takes, as the keys or the system's edit actions (macOS delivers the
// arrows as edit actions), in the order they arrive. Down opens the list or
// moves down it; Up moves up; Enter takes the highlighted row; Escape closes
// it; Tab takes the highlighted row, if any, and Shift+Tab does not, and
// either closes the list and leaves the focus to move on.
type comboCmd ComboState

// FieldCommand implements ui.FieldCommander.
func (s *comboCmd) FieldCommand(cmd ui.FieldCommand, _ rune) bool {
	st := (*ComboState)(s)
	n := len(st.items)
	if n == 0 {
		return false
	}
	switch cmd {
	case ui.FieldMoveDown:
		if !st.Open {
			comboOpen(st, st.text, st.items)
		} else {
			st.List.Command(CompletionDown, n)
		}
		return true
	case ui.FieldMoveUp:
		if st.Open {
			st.List.Command(CompletionUp, n)
			return true
		}
	case ui.FieldInsertNewline:
		if st.Open && st.List.Sel >= 0 && st.List.Sel < n {
			st.accepted, st.pick = true, st.List.Sel
			return true
		}
	case ui.FieldCancel:
		if st.Open {
			st.Open = false
			return true
		}
	case ui.FieldInsertTab, ui.FieldInsertBacktab:
		if st.Open {
			if cmd == ui.FieldInsertTab && st.List.Sel >= 0 && st.List.Sel < n {
				st.accepted, st.pick = true, st.List.Sel
			}
			st.Open = false
		}
	}
	return false
}

// comboOpen opens the list with the highlight on the choice the text names, or
// the first that begins with it, else none.
func comboOpen(st *ComboState, buf *ui.TextBuffer, items []string) {
	st.Open = true
	st.List.Reset(len(items), false)
	st.List.Sel = comboMatch(items, buf.Bytes())
	if st.List.Sel < 0 && buf.Len() == 0 && len(items) > 0 {
		st.List.Sel = 0
	}
}

// comboState is the combo box's accessibility state: expanded while the list
// shows.
func comboState(st *ComboState) ui.SemState {
	if st.Open {
		return ui.SemExpanded | ui.SemEditable
	}
	return ui.SemEditable
}

// comboMatch is the index of the first of items that begins with text, ignoring
// the case of ASCII letters; -1 if none does or the text is empty.
func comboMatch(items []string, text []byte) int {
	if len(text) == 0 {
		return -1
	}
	for i, it := range items {
		if len(it) < len(text) {
			continue
		}
		ok := true
		for j := range text {
			a, b := it[j], text[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
