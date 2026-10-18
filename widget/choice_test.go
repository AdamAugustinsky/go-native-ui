package widget

import (
	"testing"

	"gonativeui/ui"
)

// controls is a column of the choice widgets, for the tests.
type controls struct {
	clicks   int
	disabled bool
	check    bool
	tri      CheckState
	on       bool
	radio    int
	val      float32
	lastBtn  Signal
}

func (v *controls) build(k *Kit) {
	k.Column("col")
	k.BeginDisabled(v.disabled)
	if k.Button("Press").Clicked {
		v.clicks++
	}
	k.Checkbox("Check", &v.check)
	k.CheckboxTri("Tri", &v.tri)
	k.Switch("Switch", &v.on)
	k.RadioGroup("Group", &v.radio, []string{"One", "Two", "Three"})
	k.Slider("Slide", &v.val, 0, 10, 1)
	k.EndDisabled()
	k.EndColumn()
}

func newControls(t *testing.T) (*rig, *controls) {
	v := &controls{radio: 0, val: 5}
	return newRig(t, 400, 500, v.build), v
}

func TestClickAndKeyboardActivation(t *testing.T) {
	g, v := newControls(t)
	g.clickOn(ui.RoleButton, "Press")
	if v.clicks != 1 {
		t.Fatalf("a click gave %d presses, want 1", v.clicks)
	}
	// Tab reaches the button (it has the focus after the click: move away first)
	g.c.SetFocus(0)
	g.key(ui.KeyTab, 0)
	if !g.c.FocusVisible() {
		t.Fatal("focus reached with Tab is not visible")
	}
	g.key(ui.KeyEnter, 0)
	g.key(ui.KeySpace, 0)
	if v.clicks != 3 {
		t.Fatalf("Enter and Space gave %d presses in total, want 3", v.clicks)
	}
}

func TestCheckboxToggles(t *testing.T) {
	g, v := newControls(t)
	g.clickOn(ui.RoleCheckbox, "Check")
	if !v.check {
		t.Fatal("click did not check")
	}
	if n := g.must(ui.RoleCheckbox, "Check"); n.State&ui.SemChecked == 0 {
		t.Fatal("checked box exports no checked state")
	}
	g.key(ui.KeySpace, 0) // still focused after the click
	if v.check {
		t.Fatal("Space did not uncheck")
	}
	// tri-state: mixed becomes checked
	v.tri = Mixed
	g.frames(2)
	if n := g.must(ui.RoleCheckbox, "Tri"); n.State&ui.SemMixed == 0 {
		t.Fatal("mixed box exports no mixed state")
	}
	g.clickOn(ui.RoleCheckbox, "Tri")
	if v.tri != Checked {
		t.Fatalf("click on a mixed box gave %d, want checked", v.tri)
	}
}

func TestSwitchAndRadio(t *testing.T) {
	g, v := newControls(t)
	g.clickOn(ui.RoleSwitch, "Switch")
	if !v.on {
		t.Fatal("click did not switch on")
	}
	g.clickOn(ui.RoleRadio, "Three")
	if v.radio != 2 {
		t.Fatalf("clicked the third radio, choice is %d", v.radio)
	}
	g.key(ui.KeyDown, 0) // wraps to the first
	if v.radio != 0 {
		t.Fatalf("Down from the last radio chose %d, want 0 (wrap)", v.radio)
	}
	g.key(ui.KeyUp, 0)
	if v.radio != 2 {
		t.Fatalf("Up from the first radio chose %d, want 2 (wrap)", v.radio)
	}
	if f := g.c.FocusKey(); f != g.keyOf(ui.RoleRadio, "Three") {
		t.Fatal("the focus did not follow the choice")
	}
}

func (g *rig) keyOf(role ui.Role, name string) ui.Key { return g.must(role, name).Key }

func TestSliderKeysPointerAndRange(t *testing.T) {
	g, v := newControls(t)
	n := g.must(ui.RoleSlider, "Slide")
	if n.Range != [4]float64{0, 10, 5, 1} {
		t.Fatalf("slider range = %v, want [0 10 5 1]", n.Range)
	}
	// click at the left end: value near the minimum
	g.click(n.Bounds[0]+1, (n.Bounds[1]+n.Bounds[3])/2)
	if v.val > 1 {
		t.Fatalf("click at the left end set %v", v.val)
	}
	g.key(ui.KeyRight, 0)
	g.key(ui.KeyRight, 0)
	before := v.val
	g.key(ui.KeyPageUp, 0)
	if v.val != before+10 && v.val != 10 {
		t.Fatalf("Page Up moved %v to %v", before, v.val)
	}
	g.key(ui.KeyHome, 0)
	if v.val != 0 {
		t.Fatalf("Home gave %v", v.val)
	}
	g.key(ui.KeyEnd, 0)
	if v.val != 10 {
		t.Fatalf("End gave %v", v.val)
	}
	if n := g.must(ui.RoleSlider, "Slide"); n.Range[2] != 10 {
		t.Fatalf("exported value %v after End", n.Range[2])
	}
	// an assistive technology sets the value
	g.in.PushAction(ui.SemRequest{Key: n.Key, Action: ui.ActSetValue, Value: 3})
	g.frame()
	if v.val != 3 {
		t.Fatalf("set-value action gave %v", v.val)
	}
}

func TestDisabledIsDeaf(t *testing.T) {
	g, v := newControls(t)
	v.disabled = true
	g.frames(3)
	x, y := centre(g.must(ui.RoleButton, "Press"))
	g.click(x, y)
	g.clickOn(ui.RoleCheckbox, "Check")
	if v.clicks != 0 || v.check {
		t.Fatal("a disabled widget reacted to the pointer")
	}
	if n := g.must(ui.RoleButton, "Press"); n.State&ui.SemDisabled == 0 || n.Actions != 0 {
		t.Fatalf("disabled button exports state %b actions %b", n.State, n.Actions)
	}
	// an activation queued while it was enabled does not act once it is disabled
	v.disabled = false
	g.frames(2)
	g.c.SetFocus(g.keyOf(ui.RoleButton, "Press"))
	v.disabled = true
	g.in.PushKey(ui.KeyEvent{Code: ui.KeyEnter, Down: true})
	g.in.PushAction(ui.SemRequest{Key: g.keyOf(ui.RoleCheckbox, "Check"), Action: ui.ActPress})
	g.frame()
	if v.clicks != 0 || v.check {
		t.Fatal("an activation queued before the widget was disabled still acted")
	}
	// Tab skips disabled widgets
	g.c.SetFocus(0)
	g.key(ui.KeyTab, 0)
	if g.c.FocusKey() != 0 {
		t.Fatal("Tab focused a disabled widget")
	}
}
