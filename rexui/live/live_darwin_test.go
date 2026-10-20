//go:build darwin

package live

import (
	"math"
	"testing"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/rexui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"
)

// lab is a window with a combo box, a token field, a colour well and a button
// after them, and what they reported.
type lab struct {
	k       *base.Kit
	combo   rexui.ComboState
	cbuf    ui.TextBuffer
	items   []string
	chosen  int
	tokens  rexui.TokenFieldState
	toks    []string
	well    rexui.ColorWellState
	color   uint32
	changed int
}

func (p *lab) Init(a *app.App) error {
	p.k = base.NewKit(a.UI, a.Text, rex.Theme())
	p.items = []string{"Alpha", "Beta", "Gamma"}
	p.color = 0x336699FF
	p.chosen = -1
	return nil
}

func (p *lab) Build(c *ui.Context) {
	k := p.k
	k.Begin(c)
	k.NextPad(20)
	k.NextGap(12)
	k.Column("col")
	if cs := rexui.Combobox(k, "Greek", &p.combo, &p.cbuf, p.items, rexui.ComboOpts{}); cs.Chosen >= 0 {
		p.chosen = cs.Chosen
	}
	rexui.TokenField(k, "To", &p.tokens, &p.toks, rexui.TokenFieldOpts{})
	if rexui.ColorWell(k, "Accent", &p.well, &p.color, rexui.ColorWellOpts{Panel: true}).Changed {
		p.changed++
	}
	rexui.Button(k, "After", rexui.ButtonOpts{})
	k.EndColumn()
	k.End()
}

func start(t *testing.T) (*widgettest.LiveApp, *lab) {
	t.Helper()
	p := &lab{}
	return widgettest.StartLive(t, app.Config{Title: "rexui lab", Width: 520, Height: 420}, p), p
}

// view reads the lab's state on the main thread.
func view(l *widgettest.LiveApp, p *lab) (open bool, sel int, text string, chosen int, toks []string, input string, hasSel bool, focus ui.Key) {
	l.Do(func(a *app.App) {
		open, sel, text, chosen = p.combo.Open, p.combo.List.Sel, p.cbuf.String(), p.chosen
		toks = append([]string(nil), p.toks...)
		input, hasSel = p.tokens.Input.String(), p.tokens.HasSel
		focus = a.UI.FocusKey()
	})
	return
}

// The combo box from the real keyboard: Down is the system's moveDown: (an
// edit command, no key event), Return and Tab are keys.
func TestLiveComboboxWithRealKeys(t *testing.T) {
	l, p := start(t)
	l.Frames(4)
	l.Click(ui.RoleComboBox, "Greek")
	l.Press(ui.KeyDown, 0)
	open, sel, _, _, _, _, _, field := view(l, p)
	if !open || sel != 0 {
		t.Fatalf("Down: open %v, highlight %d", open, sel)
	}
	l.Press(ui.KeyDown, 0)
	l.Press(ui.KeyDown, 0)
	l.Press(ui.KeyUp, 0)
	if _, sel, _, _, _, _, _, _ = view(l, p); sel != 1 {
		t.Fatalf("Down, Down, Up: highlight %d, want 1", sel)
	}
	l.Press(ui.KeyEnter, 0)
	if open, _, text, chosen, _, _, _, _ := view(l, p); open || text != "Beta" || chosen != 1 {
		t.Fatalf("Return: open %v, text %q, chosen %d", open, text, chosen)
	}
	// Tab with the list open takes the row, closes it and moves on
	l.Press(ui.KeyBackspace, 0) // (the whole text is not selected: one letter goes)
	l.Press(ui.KeyDown, 0)
	l.Press(ui.KeyTab, 0)
	open, _, text, _, _, _, _, now := view(l, p)
	if open || now == field {
		t.Fatalf("Tab: open %v, focus left the combo box: %v (text %q)", open, now != field, text)
	}
}

// The token field from the real keyboard: a comma in typed text, Backspace at
// the start (the system's deleteBackward:) and the arrows.
func TestLiveTokenFieldWithRealKeys(t *testing.T) {
	l, p := start(t)
	l.Frames(4)
	l.Click(ui.RoleTextField, "To")
	l.Type("ada,alan,gr")
	_, _, _, _, toks, input, _, _ := view(l, p)
	if len(toks) != 2 || toks[0] != "ada" || toks[1] != "alan" || input != "gr" {
		t.Fatalf("typed ada,alan,gr: tokens %q, input %q", toks, input)
	}
	l.Press(ui.KeyBackspace, 0)
	l.Press(ui.KeyBackspace, 0) // "g" and "r" are gone
	l.Press(ui.KeyBackspace, 0) // at the start: the last token is selected
	if _, _, _, _, toks, _, hasSel, _ := view(l, p); !hasSel || len(toks) != 2 {
		t.Fatalf("Backspace at the start: selected %v, tokens %q", hasSel, toks)
	}
	l.Press(ui.KeyBackspace, 0) // and removed
	if _, _, _, _, toks, _, _, _ := view(l, p); len(toks) != 1 || toks[0] != "ada" {
		t.Fatalf("Backspace on the selected token left %q", toks)
	}
}

// panel reads the shared NSColorPanel on the main thread: whether it is on
// screen and its colour in sRGB.
func panel(l *widgettest.LiveApp) (visible bool, rgb [4]float64) {
	l.Do(func(*app.App) {
		cp := objc.Send(objc.Cls("NSColorPanel"), objc.Sel("sharedColorPanel"))
		visible = objc.SendBool(cp, objc.Sel("isVisible"))
		col := objc.Send(cp, objc.Sel("color"))
		col = objc.Send1(col, objc.Sel("colorUsingColorSpace:"), uintptr(objc.Send(objc.Cls("NSColorSpace"), objc.Sel("sRGBColorSpace"))))
		for i, s := range []string{"redComponent", "greenComponent", "blueComponent", "alphaComponent"} {
			rgb[i] = objc.SendF64(col, objc.Sel(s))
		}
	})
	return
}

// userMoves does what the user does in the panel: sets its colour and sends
// the action (the panel is continuous).
func userMoves(l *widgettest.LiveApp, r, g, b float64) {
	l.Do(func(*app.App) {
		cp := objc.Send(objc.Cls("NSColorPanel"), objc.Sel("sharedColorPanel"))
		cls, sel := objc.Cls("NSColor"), objc.Sel("colorWithSRGBRed:green:blue:alpha:") // (both use the frame: before it is assembled)
		col := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).
			ArgF64(r).ArgF64(g).ArgF64(b).ArgF64(1).Call(objc.MsgSend).R0())
		objc.Send1(cp, objc.Sel("setColor:"), uintptr(col))
		action, target := objc.Send(cp, objc.Sel("action")), objc.Send(cp, objc.Sel("target"))
		if target == 0 || action == 0 {
			return
		}
		nsApp := objc.Send(objc.Cls("NSApplication"), objc.Sel("sharedApplication"))
		objc.Send3(nsApp, objc.Sel("sendAction:to:from:"), uintptr(action), uintptr(target), uintptr(cp))
	})
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

// The colour well and the real NSColorPanel: pressing the well opens the panel
// with the well's colour; a colour the user sets in the panel is the well's; a
// colour the app sets is the panel's; closing the panel ends the well.
func TestLiveColorWellOpensTheRealColorPanel(t *testing.T) {
	l, p := start(t)
	closePanel := func() {
		l.Do(func(*app.App) {
			cp := objc.Send(objc.Cls("NSColorPanel"), objc.Sel("sharedColorPanel"))
			objc.Send(cp, objc.Sel("close"))
		})
	}
	t.Cleanup(closePanel) // (before the window goes: registered after its own)
	l.Frames(4)
	if vis, _ := panel(l); vis {
		t.Skip("the colour panel was already on screen")
	}
	l.Click(ui.RoleButton, "Accent")
	l.Frames(3)
	vis, rgb := panel(l)
	if !vis {
		t.Fatal("pressing the well did not open the colour panel")
	}
	if !near(rgb[0], 0x33/255.0) || !near(rgb[1], 0x66/255.0) || !near(rgb[2], 0x99/255.0) {
		t.Fatalf("the panel shows %v, the well's colour is #336699", rgb)
	}
	// the user chooses a colour in the panel
	userMoves(l, 1, 0.5, 0)
	l.Frames(3)
	var color uint32
	var changed int
	l.Do(func(a *app.App) { color, changed = p.color, p.changed })
	if color>>8 != 0xFF8000 && color>>8 != 0xFF7F00 || changed == 0 {
		t.Fatalf("the panel's colour reached the well as %08X (changed %d), want #FF8000", color, changed)
	}
	// the app changes the well's colour: the panel follows
	l.Do(func(a *app.App) { p.color = 0x00AA55FF })
	l.Frames(3)
	if _, rgb = panel(l); !near(rgb[0], 0) || !near(rgb[1], 0xAA/255.0) || !near(rgb[2], 0x55/255.0) {
		t.Fatalf("after the app set #00AA55 the panel shows %v", rgb)
	}
	// the user closes the panel: the well is no longer active and does not reopen it
	closePanel()
	l.Frames(4)
	var open bool
	l.Do(func(a *app.App) { open = p.well.Open })
	if vis, _ = panel(l); open || vis {
		t.Fatalf("after the panel closed: the well is open %v, the panel visible %v", open, vis)
	}
}
