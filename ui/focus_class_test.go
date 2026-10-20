package ui

import "testing"

// formRig is a mixed form, one 20 px row each at y = 0, 20, ...: a text field
// ("name"), a button ("ok"), a checkbox ("agree"), a list ("items"), a slider
// ("volume"), a search field ("search") and a button ("go").
type formRig struct {
	*harness
	keys map[string]Key
	buf  [2]TextBuffer
}

var formRows = []struct {
	name  string
	flags Flags
}{
	{"name", FocusText},
	{"ok", 0},
	{"agree", 0},
	{"items", FocusList},
	{"volume", 0},
	{"search", FocusText},
	{"go", 0},
}

func newFormRig(t *testing.T) *formRig {
	r := &formRig{harness: newHarness(t, 200, 200), keys: map[string]Key{}}
	r.frame(r.build)
	r.frame(r.build)
	return r
}

func (r *formRig) build(c *Context) {
	for _, row := range formRows {
		c.NextSize(Px(100), Px(20))
		id := c.Box(Clickable|Focusable|FocusAnimation|row.flags, row.name)
		r.keys[row.name] = c.KeyOf(id)
	}
}

func (r *formRig) tab(mods Mods) {
	r.in.PushKey(KeyEvent{Code: KeyTab, Mods: mods, Down: true})
	r.frame(r.build)
}

func (r *formRig) focused() string {
	for n, k := range r.keys {
		if r.c.FocusKey() == k {
			return n
		}
	}
	return ""
}

// cycle presses Tab (or Shift+Tab) n times from nothing focused and returns
// where the focus was after each.
func (r *formRig) cycle(mods Mods, n int) []string {
	r.c.SetFocus(0)
	r.frame(r.build)
	var got []string
	for i := 0; i < n; i++ {
		r.tab(mods)
		got = append(got, r.focused())
	}
	return got
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestTabSkipsControlsWhenTheSettingIsOff: with Tab reaching the text-entry
// controls and lists only (macOS with Keyboard navigation off), Tab and
// Shift+Tab pass the buttons, the checkbox and the slider; with it on they
// visit every box, as ever.
func TestTabSkipsControlsWhenTheSettingIsOff(t *testing.T) {
	r := newFormRig(t)
	if !r.c.TabReachesControls() {
		t.Fatal("the default is every control")
	}
	all := []string{"name", "ok", "agree", "items", "volume", "search", "go", "name"}
	if got := r.cycle(0, 8); !sameStrings(got, all) {
		t.Errorf("setting on, Tab: %v, want %v", got, all)
	}
	allBack := []string{"go", "search", "volume", "items", "agree", "ok", "name", "go"}
	if got := r.cycle(ModShift, 8); !sameStrings(got, allBack) {
		t.Errorf("setting on, Shift+Tab: %v, want %v", got, allBack)
	}

	r.c.SetTabReachesControls(false)
	if r.c.TabReachesControls() {
		t.Fatal("SetTabReachesControls(false)")
	}
	text := []string{"name", "items", "search", "name", "items"}
	if got := r.cycle(0, 5); !sameStrings(got, text) {
		t.Errorf("setting off, Tab: %v, want %v", got, text)
	}
	textBack := []string{"search", "items", "name", "search", "items"}
	if got := r.cycle(ModShift, 5); !sameStrings(got, textBack) {
		t.Errorf("setting off, Shift+Tab: %v, want %v", got, textBack)
	}
	if !r.c.FocusVisible() {
		t.Error("focus moved by Tab is not visible")
	}

	r.c.SetTabReachesControls(true) // the user turned it on
	if got := r.cycle(0, 3); !sameStrings(got, all[:3]) {
		t.Errorf("setting on again, Tab: %v, want %v", got, all[:3])
	}
}

// TestControlThatHasTheFocusKeepsIt: a control focused by a click, a
// SetFocus or an assistive technology keeps the focus while the setting is
// off; Tab leaves it for the next text-entry control or list, and Shift+Tab
// for the previous one.
func TestControlThatHasTheFocusKeepsIt(t *testing.T) {
	r := newFormRig(t)
	r.c.SetTabReachesControls(false)
	focus := func(name string) {
		r.c.SetFocus(r.keys[name])
		r.frame(r.build)
		r.frame(r.build)
		if r.focused() != name {
			t.Fatalf("a control lost the focus it was given: %q", r.focused())
		}
	}
	for _, c := range []struct{ from, fwd, back string }{
		{"ok", "items", "name"},
		{"agree", "items", "name"},
		{"volume", "search", "items"},
		{"go", "name", "search"}, // wraps
	} {
		focus(c.from)
		r.tab(0)
		if r.focused() != c.fwd {
			t.Errorf("Tab from %s: %q, want %q", c.from, r.focused(), c.fwd)
		}
		focus(c.from)
		r.tab(ModShift)
		if r.focused() != c.back {
			t.Errorf("Shift+Tab from %s: %q, want %q", c.from, r.focused(), c.back)
		}
	}
	// The setting turning off under a focused control does not take its focus.
	r.c.SetTabReachesControls(true)
	focus("ok")
	r.c.SetTabReachesControls(false)
	r.frame(r.build)
	if r.focused() != "ok" {
		t.Errorf("the setting changed: focus %q", r.focused())
	}
}

// TestClickAndAccessibilityFocusIgnoreTheClass: a press on a control focuses
// it (without the ring: FocusVisible stays false), and so does an assistive
// technology's focus request, whatever the setting.
func TestClickAndAccessibilityFocusIgnoreTheClass(t *testing.T) {
	r := newFormRig(t)
	r.c.SetTabReachesControls(false)
	r.move(50, 50) // over "agree"
	r.frame(r.build)
	r.in.SetButton(ButtonLeft, true, 1)
	r.frame(r.build)
	r.in.SetButton(ButtonLeft, false, 0)
	r.frame(r.build)
	if r.focused() != "agree" || r.c.FocusVisible() {
		t.Fatalf("after a click on a checkbox: focus %q visible=%v", r.focused(), r.c.FocusVisible())
	}
	r.in.PushAction(SemRequest{Key: r.keys["go"], Action: ActFocus})
	r.frame(r.build)
	r.frame(r.build)
	if r.focused() != "go" {
		t.Errorf("accessibility focus on a button: %q", r.focused())
	}
}

// TestTabWithOnlyControlsGoesNowhere: no text-entry control or list, and the
// setting off: Tab has nowhere to go and the focus stays.
func TestTabWithOnlyControlsGoesNowhere(t *testing.T) {
	r := newFormRig(t)
	build := func(c *Context) {
		for _, n := range []string{"x", "y"} {
			c.NextSize(Px(100), Px(20))
			r.keys[n] = c.KeyOf(c.Box(Clickable|Focusable, n))
		}
	}
	r.frame(build)
	r.frame(build)
	r.c.SetTabReachesControls(false)
	r.c.SetFocus(r.keys["x"])
	r.frame(build)
	r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
	r.frame(build)
	if r.c.FocusKey() != r.keys["x"] {
		t.Errorf("Tab moved the focus to %v", r.c.FocusKey())
	}
	r.c.SetFocus(0)
	r.frame(build)
	r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
	r.frame(build)
	if r.c.FocusKey() != 0 {
		t.Error("Tab focused a control with the setting off")
	}
}

// TestTextFieldAndHostAreTextEntry: the text field and a focusable host that
// says what it is are tab stops with the setting off; one that does not say
// is a control.
func TestTextFieldAndHostAreTextEntry(t *testing.T) {
	r := newRig(t, false)
	var field, textHost, listHost, plainHost, button Key
	build := func(c *Context) {
		c.NextSize(Px(200), Px(24))
		r.res = c.TextField("f", &r.buf)
		field = c.KeyOf(r.res.ID)
		c.NextSize(Px(50), Px(20))
		button = c.KeyOf(c.Box(Focusable|Clickable, "button"))
		c.NextSize(Px(50), Px(20))
		textHost = c.KeyOf(c.Host(1, HashString(0, "text"), HostFocusable|HostTextEntry))
		c.NextSize(Px(50), Px(20))
		plainHost = c.KeyOf(c.Host(2, HashString(0, "plain"), HostFocusable))
		c.NextSize(Px(50), Px(20))
		listHost = c.KeyOf(c.Host(3, HashString(0, "list"), HostFocusable|HostList))
	}
	r.harness.frame(build)
	r.harness.frame(build)
	r.c.SetTabReachesControls(false)
	r.c.SetFocus(0)
	r.harness.frame(build)
	var got []Key
	for i := 0; i < 4; i++ {
		r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
		r.harness.frame(build)
		got = append(got, r.c.FocusKey())
	}
	want := []Key{field, textHost, listHost, field}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Tab order %v, want %v (button %v, plain host %v)", got, want, button, plainHost)
		}
	}
}

// TestTabAllocatesNothing: moving the focus with Tab allocates nothing, with
// the setting on or off.
func TestTabAllocatesNothing(t *testing.T) {
	for _, all := range []bool{true, false} {
		r := newFormRig(t)
		r.c.SetTabReachesControls(all)
		r.tab(0)
		n := testing.AllocsPerRun(50, func() {
			r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
			r.frame(r.build)
			r.in.PushKey(KeyEvent{Code: KeyTab, Mods: ModShift, Down: true})
			r.frame(r.build)
		})
		if n != 0 {
			t.Errorf("setting reaching controls = %v: %.1f allocations per Tab pair", all, n)
		}
	}
}
