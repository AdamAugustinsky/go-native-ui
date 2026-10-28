package rexui

import "gonativeui/widget/base"

// NewTextAreaLook is the state of a hosted text area whose editor has no
// view (an offscreen window cannot host one): it builds the hosted area's
// box, outline and placeholder, for comparing its look offscreen.
func NewTextAreaLook(s TextAreaSetup) *TextAreaState {
	st, _ := NewTextArea(nil, s)
	st.ed = &base.NativeEditor{}
	return st
}
