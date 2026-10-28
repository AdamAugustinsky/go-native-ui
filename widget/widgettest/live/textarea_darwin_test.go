//go:build darwin

package live

import (
	"strings"
	"testing"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/rexui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

// taApp is a settings form with one rexui.TextArea, hosted (C4).
type taApp struct {
	k     *widget.Kit
	st    *rexui.TextAreaState
	setup rexui.TextAreaSetup
	sig   rexui.TextAreaSignal
	h     float32 // the area's box height in the last frame, points
}

func (p *taApp) Init(a *app.App) error {
	p.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	st, err := rexui.NewTextArea(a.Win, p.setup)
	p.st = st
	return err
}

func (p *taApp) Build(c *ui.Context) {
	k := p.k
	k.Begin(c)
	k.BeginWindow()
	k.Label("Bot instructions")
	p.sig = rexui.TextArea(k.Kit, "Bot instructions", p.st, rexui.TextAreaOpts{})
	k.EndWindow()
	k.End()
	if r := c.Rect(p.sig.ID); r[3] > r[1] {
		p.h = (r[3] - r[1]) / c.Scale()
	}
}

// The text area through the real editor: the NSTextView's placeholder shows
// while it is empty; typing and Enter add lines and never submit; the box
// grows from MinLines to MaxLines with the text, then stays; read-only keeps
// the text; the layout audit is clean.
func TestTextAreaHosted(t *testing.T) {
	p := &taApp{setup: rexui.TextAreaSetup{Placeholder: "Be brief", MinLines: 2, MaxLines: 4}}
	l := widgettest.StartLive(t, app.Config{Title: "text area", Width: 420, Height: 320}, p)
	l.Frames(3)
	t.Cleanup(func() { l.Do(func(*app.App) { p.st.Release() }) })
	if !p.st.Hosted() {
		t.Fatal("NewTextArea in a real window made the drawn fallback")
	}
	ed := p.st.Editor()
	tv := objc.ID(ed.View())
	placeholderShown := func() bool {
		var shown bool
		l.Do(func(*app.App) {
			for _, v := range nsArray(objc.Send(tv, objc.Sel("subviews"))) {
				if strings.Contains(className(v), "Placeholder") {
					shown = !objc.SendBool(v, objc.Sel("isHidden"))
				}
			}
		})
		return shown
	}
	if !placeholderShown() {
		t.Error("the empty area shows no placeholder")
	}
	var lineH float32
	l.Do(func(*app.App) { lineH = (ed.Height() - 2*rexui.TextAreaLook.InsetY) / 2 })
	if lineH < 12 || lineH > 20 {
		t.Fatalf("a line is %v pt (height %v for MinLines 2)", lineH, ed.Height())
	}
	inset := 2 * rexui.TextAreaLook.InsetY
	l.Until("the box is two lines high", 60, func() bool { return p.h == 2*lineH+inset })

	l.Do(func(*app.App) { ed.Focus() })
	l.Until("the editor has the keyboard", 120, func() bool { return ed.Focused() && p.sig.Focused })
	l.Type("one")
	l.Press(ui.KeyEnter, 0)
	l.Type("two")
	l.Press(ui.KeyEnter, 0)
	l.Type("three")
	if !l.Within("three lines typed", 120, func() bool { return strings.EqualFold(string(p.st.Text()), "one\ntwo\nthree") }) { // the system may capitalise sentences
		t.Fatalf("typed %q", p.st.Text())
	}
	if placeholderShown() {
		t.Error("the placeholder shows over text")
	}
	l.Until("the box grew to three lines", 60, func() bool { return p.h == 3*lineH+inset })
	l.Press(ui.KeyEnter, ui.ModSuper) // ⌘Enter: no send policy either
	for _, s := range []string{"four", "five", "six"} {
		l.Press(ui.KeyEnter, 0)
		l.Type(s)
	}
	l.Until("six lines", 120, func() bool { return strings.Count(string(p.st.Text()), "\n") >= 5 })
	l.Frames(3)
	if p.h != 4*lineH+inset {
		t.Errorf("six lines with MaxLines 4: the box is %v pt, want %v", p.h, 4*lineH+inset)
	}

	l.Do(func(*app.App) { p.st.SetReadOnly(true) })
	before := string(p.st.Text())
	l.Type("x")
	l.Press(ui.KeyEnter, 0)
	l.Frames(3)
	if got := string(p.st.Text()); got != before {
		t.Errorf("a read-only area took input: %q", got)
	}
	l.Do(func(*app.App) {
		if objc.SendBool(tv, objc.Sel("isEditable")) {
			t.Error("read-only: the text view is editable")
		}
		widgettest.AuditLayout(t, "text area, live", l.App().UI, widgettest.Allow{Check: ui.AuditOverflow, Where: "{floating background}", Other: "Window",
			Reason: "the opaque Rex field's cells reach past the window's edges (theme/rex chrome.go)"})
	})
	l.Do(func(*app.App) { p.st.SetReadOnly(false); ed.Focus() })
	l.Type("!")
	if !l.Within("editable again", 120, func() bool { return strings.HasSuffix(string(p.st.Text()), "!") }) {
		t.Errorf("editable again: %q, focused %v", p.st.Text(), ed.Focused())
	}
}

// nsArray is the objects of an NSArray.
func nsArray(a objc.ID) []objc.ID {
	n := int(objc.Send(a, objc.Sel("count")))
	out := make([]objc.ID, n)
	for i := range out {
		out[i] = objc.Send1(a, objc.Sel("objectAtIndex:"), uintptr(i))
	}
	return out
}

// className is the class name of an object.
func className(o objc.ID) string {
	return string(objc.AppendUTF8(nil, objc.Send(objc.Send(o, objc.Sel("class")), objc.Sel("description"))))
}
