//go:build darwin

package rexui_test

import (
	"image"
	"testing"

	"gonativeui/app"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

var _ = widgettest.DefineUpdateFlag()

// textAreaScene is a settings form's two text areas, as the hosted look
// builds them with no view: one with its placeholder, one disabled.
type textAreaScene struct {
	k        *widget.Kit
	mode     theme.Mode
	notes    *rexui.TextAreaState
	disabled *rexui.TextAreaState
	areas    [2]ui.ID
}

func (s *textAreaScene) Init(a *app.App) error {
	s.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	s.k.SetMode(s.mode)
	s.notes = rexui.NewTextAreaLook(rexui.TextAreaSetup{Placeholder: "Instructions for the bot", MinLines: 3, MaxLines: 8})
	s.disabled = rexui.NewTextAreaLook(rexui.TextAreaSetup{Placeholder: "Notes", MinLines: 2, MaxLines: 2})
	return nil
}

func (s *textAreaScene) Build(c *ui.Context) {
	k := s.k
	k.Begin(c)
	k.BeginWindow()
	k.NextGap(12)
	k.Column("form")
	rexui.Label(k.Kit, "Instructions", rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	s.areas[0] = rexui.TextArea(k.Kit, "Instructions", s.notes, rexui.TextAreaOpts{}).ID
	rexui.Label(k.Kit, "Notes", rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	s.areas[1] = rexui.TextArea(k.Kit, "Notes", s.disabled, rexui.TextAreaOpts{Disabled: true}).ID
	k.EndColumn()
	k.EndWindow()
	k.End()
}

// The text area's look, offscreen, in light and dark: its fill, outline,
// corner and placeholder over Rex's field, the height of its lines, and a
// clean layout audit (C4).
func TestTextAreaLook(t *testing.T) {
	for _, m := range []struct {
		name string
		mode theme.Mode
	}{{"light", theme.Light}, {"dark", theme.Dark}} {
		t.Run(m.name, func(t *testing.T) {
			s := &textAreaScene{mode: m.mode}
			r := widgettest.Open(t, app.Config{Title: "text area"}, 640, 420, 2, s)
			r.Frames(3)
			r.AuditLayout("text area, "+m.name, widgettest.Allow{Check: ui.AuditOverflow, Where: "{floating background}", Other: "Window",
				Reason: "the opaque Rex field's cells reach past the window's edges (theme/rex chrome.go), not the text area"})
			a := r.Context().Rect(s.areas[0])
			if h := (a[3] - a[1]) / 2; h != 3*16+2*rexui.TextAreaLook.InsetY {
				t.Errorf("a 3-line area is %v pt high", h)
			}
			r.AssertImageRect("textarea-"+m.name, image.Rect(0, 0, 640, 420), widgettest.Tolerance{})
		})
	}
}
