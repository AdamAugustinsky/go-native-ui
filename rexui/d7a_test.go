package rexui_test

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"gonativeui/gpu"

	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// A popover opens on the preferred side of its anchor, flips when the window
// has no room there, and its arrow points at the anchor's centre.
func TestPopoverPlacement(t *testing.T) {
	for _, tc := range []struct {
		name string
		edge rexui.PopoverEdge
		y    float32 // the anchor's top, in pixels
		want rexui.PopoverEdge
	}{
		{"below", rexui.PopoverBelow, 100, rexui.PopoverBelow},
		{"above", rexui.PopoverAbove, 300, rexui.PopoverAbove},
		{"below flips", rexui.PopoverBelow, 700, rexui.PopoverAbove},
		{"above flips", rexui.PopoverAbove, 20, rexui.PopoverBelow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sig rexui.Popover
			anchor := [4]float32{300, tc.y, 400, tc.y + 48}
			g := newRig(t, theme.Light, nil, func(g *rig) {
				p := rexui.BeginPopover(g.k, "pop", anchor, rexui.PopoverOpts{Edge: tc.edge, Width: 200, Behavior: rexui.PopoverApplicationDefined})
				rexui.Label(g.k, "Content", rexui.LabelOpts{})
				rexui.EndPopover(g.k, &p)
				sig = p
			})
			g.h = 800
			g.frames(4)
			if sig.Edge != tc.want {
				t.Fatalf("edge %d, want %d", sig.Edge, tc.want)
			}
			r := g.c.Rect(sig.ID)
			if tc.want == rexui.PopoverBelow && r[1] <= anchor[3] || tc.want == rexui.PopoverAbove && r[3] >= anchor[1] {
				t.Fatalf("body %v on the wrong side of the anchor %v", r, anchor)
			}
		})
	}
}

// popoverRef is rexui/testdata/popover-27.2.json: a real NSPopover on macOS
// 27.2, measured live (tools/appkitref/popover.sh): an NSPopover is a window
// of its own that the window server composites, so AppKitRef's offscreen
// renders cannot show it.
type popoverRef struct {
	Modes map[string]struct {
		Radius, ArrowH, Gap float64
		Fill, Edge          string
	}
}

// A drawn popover has the real one's body radius, fill and edge, and its arrow
// the real one's height and distance from the anchor, within the recipes'
// tolerances, in light and dark.
func TestPopoverMatchesAppKit(t *testing.T) {
	b, err := os.ReadFile("testdata/popover-27.2.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref popoverRef
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatal(err)
	}
	for m, mode := range [...]string{"light", "dark"} {
		want := ref.Modes[mode]
		var body ui.ID
		anchor := [4]float32{300, 100, 400, 148}
		g := newRig(t, theme.Mode(m), nil, func(g *rig) {
			p := rexui.BeginPopover(g.k, "pop", anchor, rexui.PopoverOpts{Width: 260, Behavior: rexui.PopoverApplicationDefined})
			rexui.Label(g.k, "Popover content", rexui.LabelOpts{})
			rexui.EndPopover(g.k, &p)
			body = p.ID
		})
		g.h = 1200
		g.settle()
		br := g.c.Rect(body)
		bg := g.k.Theme().Colors[theme.Window] | 0xFF
		var shapes []gpu.Quad
		var bodyQ *gpu.Quad
		tip := math.Inf(1)
		for i := range g.list.Quads {
			q := &g.list.Quads[i]
			if q.Mode != gpu.ModeShape || q.Soft > 0 || (q.Color[0]&0xFF == 0 && q.Border == 0) {
				continue
			}
			shapes = append(shapes, *q)
			if math.Abs(float64(q.Rect[0]-br[0])) < 1 && math.Abs(float64(q.Rect[1]-br[1])) < 1 && math.Abs(float64(q.Rect[2]-br[2])) < 1 {
				bodyQ = q
			}
			if float64(q.Rect[3]) <= float64(br[1])+1 && float64(q.Rect[1]) > float64(anchor[3]) && q.Rect[2]-q.Rect[0] < (br[2]-br[0])/2 {
				tip = math.Min(tip, float64(q.Rect[1]))
			}
		}
		if bodyQ == nil || math.IsInf(tip, 1) {
			t.Fatalf("%s: no body (%v) or arrow (%v) among the shapes", mode, bodyQ != nil, tip)
		}
		if d := math.Abs(float64(bodyQ.Radii[0])/2 - want.Radius); d > tolLength {
			t.Errorf("%s radius: ours %v, AppKit %v", mode, bodyQ.Radii[0]/2, want.Radius)
		}
		cx, cy := float64(br[0]+br[2])/2, float64(br[1]+br[3])/2
		if d := deltaE(hex(composite(shapes, bg, cx, cy)), want.Fill); d > tolFill {
			t.Errorf("%s fill: ours %s, AppKit %s (ΔE %.2f)", mode, hex(composite(shapes, bg, cx, cy)), want.Fill, d)
		}
		if d := deltaE(hex(composite(shapes, bg, float64(br[0])+0.5, cy)), want.Edge); d > tolFill {
			t.Errorf("%s edge: ours %s, AppKit %s (ΔE %.2f)", mode, hex(composite(shapes, bg, float64(br[0])+0.5, cy)), want.Edge, d)
		}
		if h := (float64(br[1]) - tip) / 2; math.Abs(h-want.ArrowH) > tolLength {
			t.Errorf("%s arrow height: ours %v, AppKit %v", mode, h, want.ArrowH)
		}
		if gap := (tip - float64(anchor[3])) / 2; math.Abs(gap-want.Gap) > tolLength {
			t.Errorf("%s gap to the anchor: ours %v, AppKit %v", mode, gap, want.Gap)
		}
	}
}

// A transient popover closes on Escape, on a press outside it and when the
// window stops being key; a semitransient one on Escape and a press outside
// but not when the window stops being key; an application-defined one only
// when the app says (all as measured on NSPopover).
func TestPopoverDismissal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		behavior rexui.PopoverBehavior
		act      func(g *rig)
		dismiss  bool
	}{
		{"escape", rexui.PopoverTransient, func(g *rig) { g.key(ui.KeyEscape, 0) }, true},
		{"press outside", rexui.PopoverTransient, func(g *rig) { g.click(20, 20) }, true},
		{"inactive window", rexui.PopoverTransient, func(g *rig) { g.in.Focused = false; g.frame() }, true},
		{"semitransient escape", rexui.PopoverSemitransient, func(g *rig) { g.key(ui.KeyEscape, 0) }, true},
		{"semitransient press outside", rexui.PopoverSemitransient, func(g *rig) { g.click(20, 20) }, true},
		{"semitransient inactive", rexui.PopoverSemitransient, func(g *rig) { g.in.Focused = false; g.frame() }, false},
		{"app-defined escape", rexui.PopoverApplicationDefined, func(g *rig) { g.key(ui.KeyEscape, 0) }, false},
		{"app-defined press outside", rexui.PopoverApplicationDefined, func(g *rig) { g.click(20, 20) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dismissed := false
			g := newRig(t, theme.Light, nil, func(g *rig) {
				k := g.k
				k.Row("anchor")
				rexui.Button(k, "Open", rexui.ButtonOpts{})
				k.EndRow()
				p := rexui.BeginPopover(k, "Details", [4]float32{300, 100, 400, 148}, rexui.PopoverOpts{Behavior: tc.behavior})
				rexui.Button(k, "Inside", rexui.ButtonOpts{})
				rexui.EndPopover(k, &p)
				dismissed = dismissed || p.Dismissed
			})
			g.h = 800
			g.frames(3)
			if dismissed {
				t.Fatal("dismissed on its own")
			}
			n := g.node(ui.RoleDialog, "Details")
			if n.Bounds[2] <= n.Bounds[0] {
				t.Fatalf("the popover's node has no bounds: %v", n.Bounds)
			}
			g.node(ui.RoleButton, "Inside")
			tc.act(g)
			g.frames(2)
			if dismissed != tc.dismiss {
				t.Fatalf("dismissed = %v, want %v", dismissed, tc.dismiss)
			}
		})
	}
}

// The combo box opens on Down, highlights the choice the typed text begins,
// chooses on Enter and on a click, closes on Escape; its role, value and
// expanded state are in the semantic tree.
func TestComboboxKeyboardAndClicks(t *testing.T) {
	var st rexui.ComboState
	var buf ui.TextBuffer
	choices := []string{"Helvetica", "Hercules", "Menlo", "Monaco"}
	var sig rexui.ComboSignal
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		sig = rexui.Combobox(g.k, "Font", &st, &buf, choices, rexui.ComboOpts{})
	}))
	g.h = 800
	n := g.node(ui.RoleComboBox, "Font")
	g.click(centre(n))
	g.settle()
	g.key(ui.KeyDown, 0)
	g.frame()
	if !st.Open || !sig.Open {
		t.Fatal("Down did not open the list")
	}
	if got := g.node(ui.RoleComboBox, "Font"); got.State&ui.SemExpanded == 0 {
		t.Fatal("the open combo box is not expanded in the semantic tree")
	}
	g.typeText("mo")
	if st.List.Sel != 3 {
		t.Fatalf("typing %q highlighted row %d, want Monaco's 3", "mo", st.List.Sel)
	}
	g.in.PushKey(ui.KeyEvent{Code: 'A', Mods: ui.PrimaryMod(), Down: true})
	g.frame()
	g.typeText("men")
	if st.List.Sel != 2 {
		t.Fatalf("highlight %d, want Menlo's 2 for the text %q", st.List.Sel, buf.String())
	}
	g.key(ui.KeyEnter, 0)
	if buf.String() != "Menlo" || st.Open || sig.Chosen != 2 && !sig.Changed {
		t.Fatalf("Enter: text %q open %v chosen %d", buf.String(), st.Open, sig.Chosen)
	}
	// the button opens it, a click on a row chooses it
	g.click(centre(n))
	g.settle()
	if st.Open {
		g.key(ui.KeyEscape, 0)
		if st.Open {
			t.Fatal("Escape did not close the list")
		}
	}
	st.Open = false
	g.key(ui.KeyDown, 0)
	g.settle()
	row := g.node(ui.RoleListItem, "Monaco")
	g.click(centre(row))
	if buf.String() != "Monaco" || st.Open {
		t.Fatalf("a click on Monaco left %q, open %v", buf.String(), st.Open)
	}
}

// A token field commits what is typed on Enter and on a comma, selects the
// last token on Backspace at an empty field, removes the selected one on
// Backspace again, and moves between tokens with Left and Right.
func TestTokenFieldKeyboard(t *testing.T) {
	var st rexui.TokenFieldState
	tokens := []string{}
	var sig rexui.TokenFieldSignal
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		sig = rexui.TokenField(g.k, "To", &st, &tokens, rexui.TokenFieldOpts{})
	}))
	g.h = 800
	g.key(ui.KeyTab, 0)
	g.typeText("ada,")
	if len(tokens) != 1 || tokens[0] != "ada" || !sig.Tokens && len(tokens) != 1 {
		t.Fatalf("a comma left tokens %q", tokens)
	}
	g.typeText("alan")
	g.key(ui.KeyEnter, 0)
	if len(tokens) != 2 || tokens[1] != "alan" {
		t.Fatalf("Enter left tokens %q", tokens)
	}
	if st.Input.Len() != 0 {
		t.Fatalf("the input kept %q", st.Input.String())
	}
	g.key(ui.KeyBackspace, 0)
	if !st.HasSel || st.Sel != 1 || len(tokens) != 2 {
		t.Fatalf("Backspace at an empty field: sel %v/%d, tokens %q", st.HasSel, st.Sel, tokens)
	}
	g.frame() // (the tokens are drawn before the field takes the key: the frame after shows it)
	if n := g.node(ui.RoleListItem, "alan"); n.State&ui.SemSelected == 0 {
		t.Fatal("the selected token is not selected in the semantic tree")
	}
	g.key(ui.KeyLeft, 0)
	if st.Sel != 0 {
		t.Fatalf("Left moved to %d", st.Sel)
	}
	g.key(ui.KeyBackspace, 0)
	if len(tokens) != 1 || tokens[0] != "alan" {
		t.Fatalf("Backspace on a selected token left %q", tokens)
	}
	// a click selects a token
	g.frames(2)
	g.click(centre(g.node(ui.RoleListItem, "alan")))
	if !st.HasSel || st.Sel != 0 {
		t.Fatal("a click did not select the token")
	}
	g.typeText("x")
	if st.HasSel {
		t.Fatal("typing kept the token selected")
	}
}

// The press that closes a popover and what it does to the anchor, as
// NSPopover does it: a transient popover's press on the anchor closes it and
// does not reach the button (it would reopen it); a semitransient or
// application-defined one's reaches the button and leaves it open.
func TestPopoverAnchorPress(t *testing.T) {
	for _, tc := range []struct {
		name      string
		behavior  rexui.PopoverBehavior
		dismissed bool
		clicked   bool
	}{
		{"transient", rexui.PopoverTransient, true, false},
		{"semitransient", rexui.PopoverSemitransient, false, true},
		{"app-defined", rexui.PopoverApplicationDefined, false, true},
	} {
		var dismissed, clicked bool
		g := newRig(t, theme.Light, nil, func(g *rig) {
			k := g.k
			if rexui.Button(k, "Open", rexui.ButtonOpts{}).Clicked {
				clicked = true
			}
			anchor := g.c.RectOf(ui.HashString(g.c.ParentKey(), "Open"))
			if anchor == ([4]float32{}) {
				return
			}
			p := rexui.BeginPopover(k, "Details", anchor, rexui.PopoverOpts{Behavior: tc.behavior})
			rexui.Label(k, "Body", rexui.LabelOpts{})
			rexui.EndPopover(k, &p)
			dismissed = dismissed || p.Dismissed
		})
		g.h = 800
		g.frames(3)
		x, y := centre(g.node(ui.RoleButton, "Open"))
		g.click(x, y)
		if dismissed != tc.dismissed || clicked != tc.clicked {
			t.Errorf("%s: a press on the anchor: dismissed %v (want %v), the button clicked %v (want %v)",
				tc.name, dismissed, tc.dismissed, clicked, tc.clicked)
		}
	}
}

// The colour well opens its picker on a click and on Space, a swatch sets the
// colour, the hex field applies #RRGGBB on Enter; the semantic value is the
// colour's hex.
func TestColorWellPicker(t *testing.T) {
	var st rexui.ColorWellState
	st.Hex.Reserve(16)
	col := uint32(0x336699FF)
	var sig rexui.ColorWellSignal
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		sig = rexui.ColorWell(g.k, "Accent", &st, &col, rexui.ColorWellOpts{Drawn: true})
	}))
	g.h = 800
	n := g.node(ui.RoleButton, "Accent")
	if v := string(g.sem.Text(n.Value)); v != "#336699" {
		t.Fatalf("the well's value is %q", v)
	}
	g.key(ui.KeyTab, 0)
	g.key(ui.KeySpace, 0)
	if !st.Open || !sig.Open {
		t.Fatal("Space did not open the picker")
	}
	g.settle()
	g.click(centre(g.node(ui.RoleRadio, "Orange")))
	if col == 0x336699FF || !sig.Changed && col>>8 == 0x336699 {
		t.Fatalf("a swatch left the colour %08X", col)
	}
	g.click(centre(g.node(ui.RoleTextField, "Hex")))
	g.in.PushKey(ui.KeyEvent{Code: 'A', Mods: ui.PrimaryMod(), Down: true})
	g.frame()
	g.typeText("#102030")
	g.key(ui.KeyEnter, 0)
	if col != 0x102030FF {
		t.Fatalf("the hex field left the colour %08X", col)
	}
	g.click(10, 10) // a press outside closes the picker
	if st.Open {
		t.Fatal("a press outside left the picker open")
	}
}

// By default the well drives the system colour panel through ui: a click asks
// for it with the well's colour, what the user sets there is the well's colour,
// a colour the app sets is asked again, closing the panel ends it, and a
// second well that claims the panel ends the first.
func TestColorWellPanel(t *testing.T) {
	if !platform.NativeColorPanel {
		t.Skip("this system has no colour panel: the well uses its picker")
	}
	var st, st2 rexui.ColorWellState
	col, col2 := uint32(0x336699FF), uint32(0x112233FF)
	var sig rexui.ColorWellSignal
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		sig = rexui.ColorWell(g.k, "Accent", &st, &col, rexui.ColorWellOpts{})
		rexui.ColorWell(g.k, "Other", &st2, &col2, rexui.ColorWellOpts{})
	}))
	g.click(centre(g.node(ui.RoleButton, "Accent")))
	owner, rgba, ok := g.c.ColorPanelRequest()
	if !ok || rgba != 0x336699FF || !st.Open || !sig.Open {
		t.Fatalf("a click: request %v %08X %v, open %v", owner, rgba, ok, st.Open)
	}
	if st.Hex.Len() != 0 {
		t.Error("the drawn picker's state was touched")
	}
	g.in.SetColorPick(0xAA5500FF)
	g.frame()
	if col != 0xAA5500FF || !sig.Changed {
		t.Fatalf("the user's colour: %08X changed %v", col, sig.Changed)
	}
	if _, rgba, _ = g.c.ColorPanelRequest(); rgba != 0xAA5500FF {
		t.Errorf("the panel is asked for %08X after the user chose", rgba)
	}
	col = 0x010203FF // the app changes it
	g.frame()
	if _, rgba, _ = g.c.ColorPanelRequest(); rgba != 0x010203FF {
		t.Errorf("the panel is asked for %08X after the app changed the colour", rgba)
	}
	// the second well takes the panel: the first closes
	g.click(centre(g.node(ui.RoleButton, "Other")))
	g.frames(2)
	if st.Open || !st2.Open {
		t.Fatalf("after another well claimed it: first %v, second %v", st.Open, st2.Open)
	}
	if o2, _, _ := g.c.ColorPanelRequest(); o2 == owner {
		t.Error("the panel still belongs to the first well")
	}
	g.in.SetColorClosed()
	g.frame()
	g.frame()
	if st2.Open {
		t.Error("closing the panel left the well open")
	}
	if _, _, ok := g.c.ColorPanelRequest(); ok {
		t.Error("nothing should ask for the panel now")
	}
}

// Where the platform cannot show the colour panel (the app says so), the well
// opens its own picker in its place.
func TestColorWellFallsBackToThePicker(t *testing.T) {
	if !platform.NativeColorPanel {
		t.Skip("this system has no colour panel: the well always uses its picker")
	}
	var st rexui.ColorWellState
	col := uint32(0x336699FF)
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		rexui.ColorWell(g.k, "Accent", &st, &col, rexui.ColorWellOpts{})
	}))
	g.h = 800
	g.click(centre(g.node(ui.RoleButton, "Accent")))
	if _, _, ok := g.c.ColorPanelRequest(); !ok {
		t.Fatal("the well did not ask for the panel")
	}
	g.in.SetColorFailed() // what the app does when ShowColorPanel returns ErrUnsupported
	g.frames(3)
	g.node(ui.RoleRadio, "Orange") // the picker's swatches are there
	if _, _, ok := g.c.ColorPanelRequest(); ok {
		t.Error("the well still asks for the panel")
	}
}

func TestCompletionStateCommands(t *testing.T) {
	var s rexui.CompletionState
	s.Reset(3, true)
	for i, c := range []struct {
		cmd  rexui.CompletionCmd
		want rexui.CompletionResult
		sel  int
	}{
		{rexui.CompletionDown, rexui.ResultMoved, 1},
		{rexui.CompletionDown, rexui.ResultMoved, 2},
		{rexui.CompletionDown, rexui.ResultMoved, 0}, // wraps
		{rexui.CompletionUp, rexui.ResultMoved, 2},
		{rexui.CompletionTab, rexui.ResultAccept, 2},
		{rexui.CompletionEnter, rexui.ResultAccept, 2},
		{rexui.CompletionEscape, rexui.ResultCancel, 2},
		{rexui.CompletionNone, rexui.ResultNone, 2},
	} {
		if got := s.Command(c.cmd, 3); got != c.want || s.Sel != c.sel {
			t.Fatalf("step %d: result %d sel %d, want %d and %d", i, got, s.Sel, c.want, c.sel)
		}
	}
	s.Reset(0, true)
	if s.Command(rexui.CompletionEnter, 0) != rexui.ResultNone || s.Command(rexui.CompletionDown, 0) != rexui.ResultNone {
		t.Fatal("an empty list reacted")
	}
}

// The completion list takes no focus (the field keeps it from the frame it
// opens in, and through a click on a row), reports a click on a row and a press
// outside it, and names its rows.
func TestCompletionListClickAndFocus(t *testing.T) {
	items := []rexui.CompletionItem{{Title: "Ada", Detail: "@ada"}, {Title: "Alan", Detail: "@alan"}}
	var st rexui.CompletionState
	st.Reset(len(items), true)
	var buf ui.TextBuffer
	chosen, dismissed := -1, false
	var owner ui.Key
	open := true
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.NextWidth(g.k.Fixed(200))
		owner = rexui.TextField(g.k, "Message", &buf, "", rexui.FieldOpts{}).Key
		if open {
			sig := rexui.CompletionList(g.k, "Mentions", &st, items, [4]float32{40, 40, 41, 60}, rexui.CompletionOpts{})
			if sig.Chosen >= 0 {
				chosen = sig.Chosen
			}
			if sig.Dismissed {
				dismissed = true
				open = false
			}
		}
	}))
	g.h = 800
	g.key(ui.KeyTab, 0) // the field has the focus before the list opens
	g.settle()
	if got := g.c.FocusKey(); got != owner {
		t.Fatalf("the focus is %v, the owner is %v", got, owner)
	}
	g.in.PushKey(ui.KeyEvent{Code: 'x', Down: true}) // (the list is on screen; the field is still the one with the focus)
	g.frame()
	n := g.node(ui.RoleListItem, "Alan")
	if n.State&ui.SemSelected != 0 || string(g.sem.Text(n.Value)) != "@alan" {
		t.Fatalf("row %v", n)
	}
	g.click(centre(n))
	if chosen != 1 {
		t.Fatalf("a click on the second row chose %d", chosen)
	}
	if got := g.c.FocusKey(); got != owner {
		t.Fatalf("a click on a row moved the focus from the field to %v", got)
	}
	g.click(600, 600)
	if !dismissed {
		t.Fatal("a press outside did not dismiss the list")
	}
}

// Skeletons and typing indicators ask for frames only while they are on screen
// and the window is showing, and under Reduce Motion they are still; a window
// with none of them, or with them out of view, draws no frames while idle.
func TestLoopingComponentsAnimateOnlyWhileVisible(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(g *rig)
	}{
		{"skeleton", func(g *rig) { rexui.Skeleton(g.k, "line", rexui.SkeletonOpts{}) }},
		{"skeleton avatar", func(g *rig) { rexui.Skeleton(g.k, "av", rexui.SkeletonOpts{Shape: rexui.SkeletonAvatar}) }},
		{"typing", func(g *rig) { rexui.TypingIndicator(g.k, "Paolo is typing", rexui.TypingOpts{}) }},
		{"typing inline", func(g *rig) { rexui.TypingIndicator(g.k, "Paolo is typing", rexui.TypingOpts{Inline: true}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newRig(t, theme.Light, nil, column(tc.build))
			g.k.ReduceMotion = false
			g.frames(3)
			if !g.c.Animating() && g.c.WakeAfter() == 0 {
				t.Fatal("a visible loop asks for no frames")
			}
			g.in.Hidden = true
			g.frames(3)
			if g.c.Animating() || g.c.WakeAfter() != 0 {
				t.Fatal("a hidden window is asked for frames")
			}
			g.in.Hidden = false
			g.frames(2)
			g.k.ReduceMotion = true
			g.frames(3)
			if g.c.Animating() || g.c.WakeAfter() != 0 {
				t.Fatal("Reduce Motion left the loop running")
			}
			// scrolled out of view: the box is not in the window
			g.k.ReduceMotion = false
			g.w, g.h = 1000, 100
			g.build = func(g *rig) {
				g.k.NextPad(20)
				g.k.Column("col")
				g.k.NextHeight(g.k.Fixed(400))
				g.k.Column("tall")
				g.k.EndColumn()
				tc.build(g)
				g.k.EndColumn()
			}
			g.frames(6)
			if g.c.Animating() || g.c.WakeAfter() != 0 {
				t.Fatal("a loop below the window's bottom asks for frames")
			}
			// inside the window but below a short scroll viewport: clipped out
			g.w, g.h = 1000, 400
			g.build = func(g *rig) {
				c := g.c
				c.NextSize(ui.Px(300), ui.Px(100))
				c.NextAxis(ui.AxisY)
				sa := c.Box(ui.ScrollY|ui.Clip|ui.Hoverable, "viewport")
				c.Push(sa)
				c.NextSize(ui.Px(200), ui.Px(300))
				c.Box(0, "tall")
				tc.build(g)
				c.Pop()
			}
			g.frames(6)
			if g.c.Animating() || g.c.WakeAfter() != 0 {
				t.Fatal("a loop clipped out of a scroll viewport asks for frames")
			}
		})
	}
}

// A toast goes by itself after its time, waits while the pointer is over it,
// asks for a frame only for the end of its time, and a close button dismisses
// it.
func TestToastDismissal(t *testing.T) {
	dismissed := false
	g := newRig(t, theme.Light, nil, func(g *rig) {
		g.k.NextSize(g.k.Fixed(500), g.k.Fixed(300))
		g.k.Column("host")
		if !dismissed {
			dismissed = rexui.Toast(g.k, "Saved", rexui.ToastOpts{Duration: 1}).Dismissed
		}
		g.k.EndColumn()
	})
	g.h = 700
	g.settle()
	g.node(ui.RoleGroup, "Info: Saved")
	if g.c.Animating() {
		t.Fatal("a waiting toast draws every frame")
	}
	if g.c.WakeAfter() <= 0 {
		t.Fatal("a toast with a time left schedules no wake for its end")
	}
	// the pointer over it holds its time
	n := g.node(ui.RoleGroup, "Info: Saved")
	g.in.MouseX, g.in.MouseY = centre(n)
	for range 120 { // two seconds at 60 Hz
		g.frame()
	}
	if dismissed {
		t.Fatal("the toast went while the pointer was over it")
	}
	g.in.MouseX, g.in.MouseY = -100, -100
	for i := 0; i < 120 && !dismissed; i++ {
		g.frame()
	}
	if !dismissed {
		t.Fatal("the toast did not go after its time")
	}
}

func TestBannerAndEmptyStateActions(t *testing.T) {
	var action, dismissed, empty bool
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.NextWidth(g.k.Fixed(420))
		b := rexui.Banner(g.k, "Upload failed", rexui.BannerOpts{Level: rexui.BannerError, Message: "Too large.", Action: "Retry", Dismissible: true})
		action, dismissed = action || b.Action, dismissed || b.Dismissed
		g.k.NextSize(g.k.Fixed(300), g.k.Fixed(200))
		empty = empty || rexui.EmptyState(g.k, "No messages", rexui.EmptyStateOpts{Message: "Start one.", Action: "New Message"}).Action
	}))
	g.h = 800
	g.node(ui.RoleGroup, "Error: Upload failed")
	g.node(ui.RoleGroup, "No messages")
	g.click(centre(g.node(ui.RoleButton, "Retry")))
	g.click(centre(g.node(ui.RoleButton, "Dismiss")))
	g.click(centre(g.node(ui.RoleButton, "New Message")))
	if !action || !dismissed || !empty {
		t.Fatalf("action %v, dismissed %v, empty state action %v", action, dismissed, empty)
	}
}

// The static pieces name themselves for assistive technologies: a count badge
// by its number, a status dot by the name it was given, an avatar by the
// person's name and presence, a date divider by its date, a typing indicator by
// who is typing.
func TestStaticPiecesAreNamed(t *testing.T) {
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		k := g.k
		rexui.BadgeCountN(k, 120, rexui.BadgeOpts{})
		rexui.Badge(k, "", rexui.BadgeOpts{Variant: rexui.BadgeStatus, Name: "Online"})
		rexui.Avatar(k, "Ada Lovelace", rexui.AvatarOpts{Presence: rexui.PresenceBusy})
		rexui.DateDivider(k, "Today", rexui.DateDividerOpts{})
		rexui.TypingIndicator(k, "Paolo is typing", rexui.TypingOpts{})
		rexui.Skeleton(k, "msgs", rexui.SkeletonOpts{Name: "Loading messages"})
		rexui.Skeleton(k, "decor", rexui.SkeletonOpts{})
	}))
	g.node(ui.RoleLabel, "99+")
	g.node(ui.RoleLabel, "Online")
	av := g.node(ui.RoleImage, "Ada Lovelace")
	if string(g.sem.Text(av.Value)) != "Busy" {
		t.Fatalf("the avatar's value is %q", g.sem.Text(av.Value))
	}
	g.node(ui.RoleLabel, "Today")
	g.node(ui.RoleLabel, "Paolo is typing")
	g.node(ui.RoleProgressBar, "Loading messages")
	for _, n := range g.sem.Nodes {
		if n.Role == ui.RoleProgressBar && string(g.sem.Text(n.Name)) == "" {
			t.Fatal("a decorative skeleton is in the semantic tree")
		}
	}
}

func countRings(g *rig) int {
	ring := g.k.Theme().Colors[theme.FocusRing] >> 8
	n := 0
	for _, q := range g.list.Quads {
		if q.Border > 0 && q.BorderColor>>8 == ring && q.BorderColor&0xFF != 0 {
			n++
		}
	}
	return n
}

// The focus ring by class of control, as AppKit does it: a text-entry control
// (text field, search field, combo box, token field) rings whenever it has the
// focus, a click included; any other control (colour well, chat row, button)
// only for keyboard focus; and none rings in a window that is not key.
func TestFocusRingByClassOfControl(t *testing.T) {
	var well rexui.ColorWellState
	col := uint32(0x336699FF)
	var cb rexui.ComboState
	var cbuf, tbuf, sbuf ui.TextBuffer
	var tf rexui.TokenFieldState
	var toks []string
	for _, tc := range []struct {
		name      string
		role      ui.Role
		label     string
		textEntry bool
		build     func(g *rig)
	}{
		{"text field", ui.RoleTextField, "Name", true, func(g *rig) { rexui.TextField(g.k, "Name", &tbuf, "", rexui.FieldOpts{}) }},
		{"search field", ui.RoleSearchField, "Find", true, func(g *rig) { rexui.SearchField(g.k, "Find", &sbuf, "", rexui.FieldOpts{}) }},
		{"combo box", ui.RoleComboBox, "Font", true, func(g *rig) { rexui.Combobox(g.k, "Font", &cb, &cbuf, []string{"a"}, rexui.ComboOpts{}) }},
		{"token field", ui.RoleTextField, "Tokens", true, func(g *rig) { rexui.TokenField(g.k, "Tokens", &tf, &toks, rexui.TokenFieldOpts{}) }},
		{"color well", ui.RoleButton, "Accent", false, func(g *rig) {
			rexui.ColorWell(g.k, "Accent", &well, &col, rexui.ColorWellOpts{Drawn: true})
		}},
		{"chat row", ui.RoleListItem, "Ada", false, func(g *rig) {
			g.k.NextWidth(g.k.Fixed(260))
			rexui.ChatListRow(g.k, "r1", "Ada", "Hello", "10:42", rexui.ChatRowOpts{})
		}},
		{"button", ui.RoleButton, "Go", false, func(g *rig) { rexui.Button(g.k, "Go", rexui.ButtonOpts{}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newRig(t, theme.Light, nil, column(tc.build))
			g.h = 800
			g.settle()
			if n := countRings(g); n != 0 {
				t.Fatalf("%d rings without focus", n)
			}
			// the keyboard: every class rings
			g.key(ui.KeyTab, 0)
			g.settle()
			if countRings(g) == 0 {
				t.Fatal("keyboard focus shows no ring")
			}
			// the window stops being key: no ring
			g.in.Focused = false
			g.settle()
			if n := countRings(g); n != 0 {
				t.Fatalf("%d rings in an inactive window", n)
			}
			g.in.Focused = true
			// a click: a text-entry control rings, the others do not
			g2 := newRig(t, theme.Light, nil, column(tc.build))
			g2.h = 800
			g2.click(centre(g2.node(tc.role, tc.label)))
			g2.settle()
			if got := countRings(g2) > 0; got != tc.textEntry {
				t.Fatalf("after a click the ring shows %v, want %v", got, tc.textEntry)
			}
			g2.in.Focused = false
			g2.settle()
			if n := countRings(g2); n != 0 {
				t.Fatalf("%d rings in an inactive window after a click", n)
			}
		})
	}
}

// A chat row activates on a click and on Enter, shows its selection as the
// sidebar's accent in a key window and as grey in one that is not, and reports
// the unread count and the selection in the semantic tree.
func TestChatRow(t *testing.T) {
	selected := false
	n := 0
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.NextWidth(g.k.Fixed(260))
		if rexui.ChatListRow(g.k, "r1", "Ada", "Lunch?", "10:42", rexui.ChatRowOpts{Selected: selected, Unread: 3}).Clicked {
			selected = true
			n++
		}
	}))
	g.click(centre(g.node(ui.RoleListItem, "Ada")))
	if n != 1 {
		t.Fatalf("a click activated the row %d times", n)
	}
	g.key(ui.KeyTab, 0)
	g.key(ui.KeyEnter, 0)
	g.frames(2)
	if n < 2 {
		t.Fatal("Enter did not activate the row")
	}
	if r := g.node(ui.RoleListItem, "Ada"); r.State&ui.SemSelected == 0 {
		t.Fatal("the selected row is not selected in the semantic tree")
	}
	g.node(ui.RoleLabel, "3")
}

// On every side the arrow is as high as the real one's (11 pt from the body's
// edge to its tip) with its tip 2 pt from the anchor, centred on the anchor.
func TestPopoverArrowOnEverySide(t *testing.T) {
	anchor := [4]float32{600, 500, 700, 548} // in the middle of the window, room on every side
	for _, edge := range []rexui.PopoverEdge{rexui.PopoverBelow, rexui.PopoverAbove, rexui.PopoverRight, rexui.PopoverLeft} {
		var body ui.ID
		g := newRig(t, theme.Light, nil, func(g *rig) {
			p := rexui.BeginPopover(g.k, "pop", anchor, rexui.PopoverOpts{Edge: edge, Width: 240, Behavior: rexui.PopoverApplicationDefined})
			g.k.NextSize(g.k.Fixed(200), g.k.Fixed(100))
			g.k.Column("content")
			g.k.EndColumn()
			rexui.EndPopover(g.k, &p)
			body = p.ID
		})
		g.w, g.h = 1400, 1100
		g.settle()
		br := g.c.Rect(body)
		// the arrow's extent along its axis, and across it
		tipLo, tipHi := math.Inf(1), math.Inf(-1)
		across0, across1 := math.Inf(1), math.Inf(-1)
		for _, q := range g.list.Quads {
			if q.Mode != gpu.ModeShape || q.Soft > 0 || q.Color[0]&0xFF == 0 {
				continue
			}
			w, h := float64(q.Rect[2]-q.Rect[0]), float64(q.Rect[3]-q.Rect[1])
			switch edge {
			case rexui.PopoverBelow, rexui.PopoverAbove:
				if h == 1 && w < 60 && q.Rect[1] > br[1]-30 && q.Rect[3] < br[3]+30 && (q.Rect[1] < br[1]+2 || q.Rect[3] > br[3]-2) {
					tipLo, tipHi = math.Min(tipLo, float64(q.Rect[1])), math.Max(tipHi, float64(q.Rect[3]))
					across0, across1 = math.Min(across0, float64(q.Rect[0])), math.Max(across1, float64(q.Rect[2]))
				}
			default:
				if w == 1 && h < 60 && q.Rect[0] > br[0]-30 && q.Rect[2] < br[2]+30 && (q.Rect[0] < br[0]+2 || q.Rect[2] > br[2]-2) {
					tipLo, tipHi = math.Min(tipLo, float64(q.Rect[0])), math.Max(tipHi, float64(q.Rect[2]))
					across0, across1 = math.Min(across0, float64(q.Rect[1])), math.Max(across1, float64(q.Rect[3]))
				}
			}
		}
		if math.IsInf(tipLo, 1) {
			t.Fatalf("edge %d: no arrow among the shapes", edge)
		}
		mid := (across0 + across1) / 2
		var gap, height, centre float64
		switch edge {
		case rexui.PopoverBelow:
			gap, height, centre = tipLo-float64(anchor[3]), float64(br[1])-tipLo, float64(anchor[0]+anchor[2])/2
		case rexui.PopoverAbove:
			gap, height, centre = float64(anchor[1])-tipHi, tipHi-float64(br[3]), float64(anchor[0]+anchor[2])/2
		case rexui.PopoverRight:
			gap, height, centre = tipLo-float64(anchor[2]), float64(br[0])-tipLo, float64(anchor[1]+anchor[3])/2
		default:
			gap, height, centre = float64(anchor[0])-tipHi, tipHi-float64(br[2]), float64(anchor[1]+anchor[3])/2
		}
		if math.Abs(gap/2-2) > 0.5 || math.Abs(height/2-11) > 0.5 || math.Abs(mid-centre) > 1.5 {
			t.Errorf("edge %d: gap %.1f pt, arrow height %.1f pt, centre off by %.1f px (want 2, 11, 0)", edge, gap/2, height/2, mid-centre)
		}
	}
}
