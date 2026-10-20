package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// FieldVariant selects a text field's look.
type FieldVariant uint8

const (
	// FieldDefault is a bezeled field: textBackgroundColor at 98 % with a
	// one-point outline just outside its frame.
	FieldDefault FieldVariant = iota
	// FieldMono is FieldDefault in the monospaced text style, for paths and
	// commands (also what the kit's NextMono asks for).
	FieldMono
	// FieldBorderless has no fill and no outline: inline editing (a rename in
	// a list).
	FieldBorderless

	// NumFieldVariants is the number of variants.
	NumFieldVariants
)

// FieldOpts are a text field's options, passed by value.
type FieldOpts struct {
	// Variant is the look.
	Variant FieldVariant
	// Size is the control size (theme.SizeSpec's FieldHeight and FieldRadius).
	Size theme.Size
}

// FieldRecipe is the look of text and search fields (NSTextField and
// NSSearchField on macOS 27.2).
type FieldRecipe struct {
	// Fill is the body (InputFill: textBackgroundColor at 98 %).
	Fill Tint
	// Border is the outline, BorderW wide in points, drawn just outside the
	// frame as AppKit does (theme.FieldOutline: black at 15 % in light,
	// white at 9 % in dark, #D9D9D9 over white and #323232 over #1E1E1E
	// measured).
	Border  Tint
	BorderW float32
	// Text, Placeholder and TextDisabled colour the text. An editable
	// field's text is controlTextColor (theme.ControlText, which equals
	// labelColor on 27.2: #212121 over white and #E1E1E1 over the dark field
	// measured live), not textColor.
	Text, Placeholder, TextDisabled Tint
	// InsetX is the distance from the frame's edges to the text, in points.
	InsetX float32
}

// FieldLook is the text fields' recipe.
var FieldLook = FieldRecipe{
	Fill:   Of(theme.InputFill),
	Border: Of(theme.FieldOutline), BorderW: 1,
	Text: Of(theme.ControlText), Placeholder: Of(theme.Placeholder), TextDisabled: Of(theme.TextDisabled),
	InsetX: 6,
}

// TextField builds a single-line text field editing buf, with the placeholder
// shown while it is empty and no input method is composing. It is
// ui.Context.TextField (caret, selection, mouse, copy, cut, paste, undo and
// input methods) in macOS's bezel; its focus ring shows whenever it has the
// focus, as AppKit's does. It fills the parent's width unless the kit's
// NextWidth says otherwise.
//
//	if rexui.TextField(k, "Name", &v.name, "Untitled", rexui.FieldOpts{}).Submitted {
//		v.rename()
//	}
func TextField(k *base.Kit, label string, buf *ui.TextBuffer, placeholder string, o FieldOpts) base.FieldSignal {
	return field(k, label, buf, placeholder, o, false)
}

// field builds a text field, or with search a search field (SearchLook).
func field(k *base.Kit, label string, buf *ui.TextBuffer, placeholder string, o FieldOpts, search bool) base.FieldSignal {
	c, r := k.Context(), k.Theme()
	rc, sr := &FieldLook, &SearchLook
	s := &r.Sizes[o.Size%theme.NumSizes]
	ti := k.BeginTextInput(label, theme.Snap(s.FieldHeight), base.HoverOff)
	h := ti.H
	font := s.Font.Font
	if ti.Mono || o.Variant == FieldMono {
		font = r.Styles[theme.StyleMono].Font
	}
	radius := s.FieldRadius
	padL := pt(k.Scale(), rc.InsetX)
	padR := padL
	var clearW float32
	if search {
		// AppKit's outline is 26 pt tall outside with corners of about
		// 12.4 pt, between a capsule of the frame (12) and one of the
		// outline (13): half an outline's width less than the outline's
		radius = h/2 - pt(k.Scale(), rc.BorderW)/2
		padL = pt(k.Scale(), sr.TextX)
		cw, _ := k.IconSize(icon.ClearField)
		clearW = theme.Snap(k.Pt(sr.ClearRight) + cw/2)
		if buf.Len() > 0 {
			padR = clearW + r.Space.S4
		}
	}
	tc := rc.Text.RGBA(r)
	if ti.Disabled {
		tc = rc.TextDisabled.RGBA(r)
	}
	if o.Variant != FieldBorderless {
		c.NextBG(rc.Fill.RGBA(r))
	} else {
		c.NextBG(0)
	}
	c.NextSize(ui.Pct(1), ui.Px(h))
	c.NextRadius(radius)
	c.NextPadding(padL, 0, padR, 0)
	c.NextFont(font)
	c.NextTextColor(tc)
	role := ui.RoleTextField
	if search {
		role = ui.RoleSearchField
	}
	res := k.TextInput(&ti, buf, role, search)

	if o.Variant != FieldBorderless {
		if w := k.FieldWidth(&ti); w > 0 {
			bw := pt(k.Scale(), rc.BorderW)
			theme.At(c, -bw, -bw, w+2*bw, h+2*bw)
			c.NextBorder(rc.Border.RGBA(r), bw)
			c.NextRadius(radius + bw)
			c.BoxKeyed(ui.DrawBorder|base.Floating, ui.KeyN(ti.WrapKey, 5), nil)
		}
	}
	if search {
		searchDecor(k, &ti, buf, h, clearW, &res)
	}
	if buf.Len() == 0 && placeholder != "" && !k.Composing(res) {
		pc := rc.Placeholder.RGBA(r)
		if ti.Disabled {
			pc = rc.TextDisabled.RGBA(r)
		}
		theme.At(c, padL, 0, 0, h)
		c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(h))
		c.NextFont(font)
		c.NextTextColor(pc)
		c.BoxKeyed(ui.DrawText|base.Floating, ui.KeyN(ti.WrapKey, 2), base.StrBytes(placeholder))
	}
	ring := ti.Visual.TextEntry() // a text-entry control rings however it got the focus
	ringR := radius
	if search { // AppKit rings a search field's frame as a capsule
		ringR = h / 2
	}
	k.FocusRing(res.ID, ring, ringR)
	k.EndTextInput(&ti)
	return res
}
