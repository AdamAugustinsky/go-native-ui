package app

import (
	"testing"

	"gonativeui/platform"
	"gonativeui/text"
	"gonativeui/ui"
)

// pairApp is a Builder with two stacked text fields, "a" and "b".
type pairApp struct {
	a      *App
	font   text.FontKey
	ba, bb ui.TextBuffer
	ra, rb ui.TextFieldSignal
	hideA  bool
}

func (p *pairApp) Init(a *App) error { p.a = a; return nil }

func (p *pairApp) Build(c *ui.Context) {
	if p.font == 0 {
		p.font = p.a.Text.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: c.Scale()})
	}
	c.PushFont(p.font)
	if !p.hideA {
		c.NextSize(ui.Px(300), ui.Px(40))
		c.NextPad(8)
		p.ra = c.TextField("a", &p.ba)
	}
	c.NextSize(ui.Px(300), ui.Px(40))
	c.NextPad(8)
	p.rb = c.TextField("b", &p.bb)
	c.PopFont()
}

func newPairOffscreen(t *testing.T) (*pairApp, *Offscreen) {
	t.Helper()
	p := &pairApp{}
	o, err := NewOffscreen(Config{Width: 400, Height: 200}, 800, 400, 2, p)
	if err != nil {
		skipOrFail(t, "offscreen renderer", err)
	}
	t.Cleanup(o.Close)
	o.Frame()
	o.Frame()
	o.UI.SetFocus(o.UI.KeyOf(p.ra.ID))
	o.Frame()
	o.Frame()
	if !p.ra.Focused {
		t.Fatal("first field not focused")
	}
	return p, o
}

// centre is where a click lands in the field (window pixels).
func (o *Offscreen) centre(id ui.ID) (x, y float32) {
	r := o.UI.Rect(id)
	return (r[0] + r[2]) / 2, (r[1] + r[3]) / 2
}

// The commit that ends a composition belongs to the field that composed,
// whatever happens to the focus in the same frame.
func TestCommitGoesToTheComposingField(t *testing.T) {
	commit := func(o *Offscreen) {
		e, p := preedit("", 0, 0)
		o.tr.event(e, p)
		o.tr.event(ev(platform.EvText, nil), []byte("仮名"))
	}
	compose := func(o *Offscreen) {
		e, p := preedit("かな", 6, 0)
		o.tr.event(e, p)
		o.Frame()
		if o.tr.in.NPreedit == 0 {
			t.Fatal("no composition")
		}
	}
	check := func(t *testing.T, p *pairApp, wantA, wantB string) {
		t.Helper()
		if p.ba.String() != wantA || p.bb.String() != wantB {
			t.Errorf("a=%q b=%q, want a=%q b=%q", p.ba.String(), p.bb.String(), wantA, wantB)
		}
	}

	t.Run("focus moves, then commit", func(t *testing.T) {
		p, o := newPairOffscreen(t)
		compose(o)
		o.UI.SetFocus(o.UI.KeyOf(p.rb.ID))
		commit(o)
		o.Frame()
		check(t, p, "仮名", "")
		// Typing afterwards goes to the focused field.
		o.tr.event(ev(platform.EvText, nil), []byte("x"))
		o.Frame()
		check(t, p, "仮名", "x")
	})
	t.Run("click on the other field, then commit", func(t *testing.T) {
		p, o := newPairOffscreen(t)
		compose(o)
		x, y := o.centre(p.rb.ID)
		o.tr.event(press(x, y, 1, platform.ButtonLeft), nil)
		commit(o)
		o.Frame()
		check(t, p, "仮名", "")
		if !p.rb.Focused {
			t.Error("the click did not focus the second field")
		}
	})
	t.Run("commit, then click on the other field", func(t *testing.T) {
		p, o := newPairOffscreen(t)
		compose(o)
		commit(o)
		x, y := o.centre(p.rb.ID)
		o.tr.event(press(x, y, 1, platform.ButtonLeft), nil)
		o.Frame()
		check(t, p, "仮名", "")
	})
	t.Run("the composing field is gone", func(t *testing.T) {
		p, o := newPairOffscreen(t)
		compose(o)
		p.hideA = true
		o.UI.SetFocus(o.UI.KeyOf(p.rb.ID))
		commit(o)
		o.Frame()
		check(t, p, "", "") // dropped: it must not land in the other field
		o.tr.event(ev(platform.EvText, nil), []byte("x"))
		o.Frame()
		check(t, p, "", "x")
	})
	t.Run("text that is not a commit still goes to the focused field", func(t *testing.T) {
		p, o := newPairOffscreen(t)
		o.UI.SetFocus(o.UI.KeyOf(p.rb.ID))
		o.tr.event(ev(platform.EvText, nil), []byte("hi"))
		o.Frame()
		check(t, p, "", "hi")
	})
	t.Run("a long commit across frames", func(t *testing.T) {
		p, o := newPairOffscreen(t)
		compose(o)
		o.UI.SetFocus(o.UI.KeyOf(p.rb.ID))
		e, pp := preedit("", 0, 0)
		o.tr.event(e, pp)
		long := ""
		for i := 0; i < 30; i++ {
			long += "仮名" // 180 bytes: three frames of 64
		}
		o.tr.event(ev(platform.EvText, nil), []byte(long))
		for i := 0; i < 4; i++ {
			o.Frame()
		}
		check(t, p, long, "")
	})
}
