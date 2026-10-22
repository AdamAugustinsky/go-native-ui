//go:build darwin

package live

import (
	"testing"

	"gonativeui/app"
	"gonativeui/text"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

type richLinkProbe struct {
	a      *app.App
	styles ui.RichStyles
	count  int
	last   uint32
	key    ui.Key
}

func (p *richLinkProbe) Init(a *app.App) error { p.a = a; return nil }

func (p *richLinkProbe) Build(c *ui.Context) {
	// Force this fixture's traversal policy, not the OS setting. The separate
	// TestRealTabFollowsKeyboardNavigation checks the actual system preference.
	c.SetTabReachesControls(true)
	p.styles.Set(0, ui.RichStyle{Font: p.a.Text.Font(text.FontDesc{SizePt: 18, Scale: c.Scale()}), Color: 0x222222FF})
	c.NextPad(12 * c.Scale())
	sig := c.RichText(123, []byte("First action and Second action"), []ui.Span{
		{Len: 12, Flags: ui.SpanLink, Tag: 1}, {Off: 17, Len: 13, Flags: ui.SpanMention, Tag: 2},
	}, &p.styles, ui.RichOpts{MaxW: 320 * c.Scale()})
	p.key = c.KeyOf(sig.ID)
	if sig.Clicked != 0 {
		p.count++
		p.last = sig.Clicked
	}
}

func TestRealRichLinkFocusAndActivation(t *testing.T) {
	p := &richLinkProbe{}
	l := widgettest.StartLive(t, app.Config{Title: "local rich-link probe", Width: 400, Height: 240}, p)
	l.Frames(4)
	for _, step := range []struct {
		mods ui.Mods
		want string
	}{
		{0, "First action"}, {0, "Second action"}, {ui.ModShift, "First action"},
	} {
		l.Press(ui.KeyTab, step.mods)
		l.Frames(2)
		if got := focusedControl(l); got != step.want {
			t.Fatalf("real Tab(%v) focused %q, want %q", step.mods, got, step.want)
		}
	}
	for i, code := range []ui.KeyCode{ui.KeyEnter, ui.KeySpace} {
		l.Press(code, 0)
		l.Frames(3)
		var count int
		var tag uint32
		l.Do(func(*app.App) { count, tag = p.count, p.last })
		if count != i+1 || tag != 1 {
			t.Fatalf("real activation count %d tag %d, want %d/1", count, tag, i+1)
		}
	}
	l.Do(func(a *app.App) { a.UI.SetFocus(ui.RichLinkKey(p.key, 1)); a.UI.SetFocusVisible(true) })
	l.Frames(2)
	if got := focusedControl(l); got != "Second action" {
		t.Fatalf("explicit span focus: %q", got)
	}
	l.Press(ui.KeyEnter, 0)
	l.Frames(2)
	l.Click(ui.RoleLink, "First action")
	l.Frames(3)
	var count int
	var tag uint32
	l.Do(func(*app.App) { count, tag = p.count, p.last })
	if count != 4 || tag != 1 {
		t.Fatalf("explicit and pointer activation: count %d tag %d, want 4/1", count, tag)
	}
}
