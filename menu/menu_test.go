package menu

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
	"gonativeui/ui"
)

const (
	cmdNew ID = iota + 1
	cmdSave
	cmdQuit
	cmdCopy
	cmdPaste
	cmdUndo
	cmdOff
)

func testCommands() []Command {
	return []Command{
		{ID: cmdNew, Title: "New", Shortcut: Shortcut{Key: 'N', Primary: true}},
		{ID: cmdSave, Title: "Save As…", Shortcut: Shortcut{Key: 'S', Mods: ui.ModShift, Primary: true}},
		{ID: cmdQuit, Title: "Quit", Scope: ScopeApp, Role: RoleQuit, Shortcut: Shortcut{Key: 'Q', Primary: true}},
		{ID: cmdCopy, Title: "Copy", Role: RoleCopy, Shortcut: Shortcut{Key: 'C', Primary: true}},
		{ID: cmdPaste, Title: "Paste", Role: RolePaste, Shortcut: Shortcut{Key: 'V', Primary: true}},
		{ID: cmdUndo, Role: RoleUndo, Shortcut: Shortcut{Key: 'Z', Primary: true}},
		{ID: cmdOff, Title: "Disabled thing", Shortcut: Shortcut{Key: 'D', Primary: true}},
	}
}

func TestNewModelValidates(t *testing.T) {
	good := testCommands()
	menus := []Menu{
		{Title: "File", Items: []Item{{Kind: ItemCommand, Command: cmdNew}, {Kind: ItemSeparator}, {Kind: ItemSubmenu, Submenu: 1}}},
		{Title: "Recent", Items: []Item{{Kind: ItemCommand, Command: cmdSave}}},
		{Title: "Context", Items: []Item{{Kind: ItemCommand, Command: cmdCopy}}},
	}
	m, err := NewModel(good, menus, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := m.Command(cmdSave); !ok || c.Title != "Save As…" {
		t.Fatalf("Command: %+v %v", c, ok)
	}
	if _, ok := m.Command(99); ok {
		t.Error("unknown command found")
	}
	// The model owns copies: changing the arguments afterwards changes nothing.
	good[0].Title = "changed"
	menus[0].Items[0].Command = cmdQuit
	if c, _ := m.Command(cmdNew); c.Title != "New" || m.Menus()[0].Items[0].Command != cmdNew {
		t.Error("NewModel kept the caller's slices")
	}

	bad := []struct {
		name string
		cmds func() []Command
		mens []Menu
		bar  []int
		want string
	}{
		{"zero id", func() []Command { return []Command{{Title: "x"}} }, nil, nil, "id 0"},
		{"repeated id", func() []Command { return []Command{{ID: 1, Title: "a"}, {ID: 1, Title: "b"}} }, nil, nil, "twice"},
		{"no title", func() []Command { return []Command{{ID: 1}} }, nil, nil, "neither a title nor a role"},
		{"bad scope", func() []Command { return []Command{{ID: 1, Title: "a", Scope: 9}} }, nil, nil, "scope"},
		{"bad role", func() []Command { return []Command{{ID: 1, Title: "a", Role: 99}} }, nil, nil, "role"},
		{"role twice", func() []Command {
			return []Command{{ID: 1, Title: "a", Role: RoleCopy}, {ID: 2, Title: "b", Role: RoleCopy}}
		}, nil, nil, "same role"},
		{"shortcut without key", func() []Command {
			return []Command{{ID: 1, Title: "a", Shortcut: Shortcut{Primary: true}}}
		}, nil, nil, "no key"},
		{"shared shortcut", func() []Command {
			return []Command{{ID: 1, Title: "a", Shortcut: Shortcut{Key: 'A', Primary: true}},
				{ID: 2, Title: "b", Shortcut: Shortcut{Key: 'A', Mods: ui.PrimaryMod()}}}
		}, nil, nil, "same shortcut"},
		{"unknown command", testCommands, []Menu{{Items: []Item{{Command: 77}}}}, nil, "unknown command"},
		{"unknown submenu", testCommands, []Menu{{Items: []Item{{Kind: ItemSubmenu, Submenu: 3}}}}, nil, "unknown submenu"},
		{"cycle", testCommands, []Menu{
			{Items: []Item{{Kind: ItemSubmenu, Submenu: 1}}},
			{Items: []Item{{Kind: ItemSubmenu, Submenu: 0}}},
		}, nil, "ancestor"},
		{"self", testCommands, []Menu{{Items: []Item{{Kind: ItemSubmenu, Submenu: 0}}}}, nil, "ancestor"},
		{"submenu twice", testCommands, []Menu{
			{Items: []Item{{Kind: ItemSubmenu, Submenu: 2}}},
			{Items: []Item{{Kind: ItemSubmenu, Submenu: 2}}},
			{},
		}, nil, "twice"},
		{"bar out of range", testCommands, []Menu{{}}, []int{1}, "bar"},
		{"bar twice", testCommands, []Menu{{}}, []int{0, 0}, "twice"},
		{"bar and submenu", testCommands, []Menu{{Items: []Item{{Kind: ItemSubmenu, Submenu: 1}}}, {}}, []int{0, 1}, "submenu"},
	}
	for _, b := range bad {
		_, err := NewModel(b.cmds(), b.mens, b.bar)
		if err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%s: error %v, want one mentioning %q", b.name, err, b.want)
		}
	}
}

func TestEnabledAndChecked(t *testing.T) {
	m, err := NewModel(testCommands(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Enabled(cmdNew) || m.Checked(cmdNew) {
		t.Fatal("defaults: enabled, unchecked")
	}
	m.SetEnabled(cmdNew, false)
	m.SetChecked(cmdNew, true)
	if m.Enabled(cmdNew) || !m.Checked(cmdNew) || !m.Enabled(cmdSave) || m.Checked(cmdSave) {
		t.Fatal("state of one command leaked to another")
	}
	m.SetEnabled(cmdNew, true)
	if !m.Enabled(cmdNew) {
		t.Fatal("re-enable")
	}
	m.SetEnabled(99, false) // unknown ids are ignored
	if m.Enabled(99) || m.Checked(99) {
		t.Fatal("unknown id has state")
	}
	// More than 64 commands use more than one word of state.
	var many []Command
	for i := 1; i <= 130; i++ {
		many = append(many, Command{ID: ID(i), Title: "c"})
	}
	m, _ = NewModel(many, nil, nil)
	m.SetEnabled(70, false)
	m.SetChecked(129, true)
	if m.Enabled(70) || !m.Enabled(69) || !m.Enabled(71) || !m.Checked(129) || m.Checked(65) {
		t.Fatal("state across words")
	}
}

func TestShortcutLabels(t *testing.T) {
	cases := []struct {
		s          Shortcut
		mac, other string
	}{
		{Shortcut{Key: 'N', Primary: true}, "⌘N", "Ctrl+N"},
		{Shortcut{Key: 'N', Mods: ui.ModShift, Primary: true}, "⇧⌘N", "Ctrl+Shift+N"},
		{Shortcut{Key: 'S', Mods: ui.ModCtrl | ui.ModAlt | ui.ModShift}, "⌃⌥⇧S", "Ctrl+Alt+Shift+S"},
		{Shortcut{Key: 'S', Mods: ui.ModSuper}, "⌘S", "Super+S"},
		{Shortcut{Key: 'Q', Mods: ui.ModCtrl, Primary: true}, "⌃⌘Q", "Ctrl+Q"},
		{Shortcut{Key: ui.KeyEnter, Primary: true}, "⌘↩", "Ctrl+Enter"},
		{Shortcut{Key: ui.KeyBackspace, Primary: true}, "⌘⌫", "Ctrl+Backspace"},
		{Shortcut{Key: ui.KeyLeftBracket, Mods: ui.ModShift, Primary: true}, "⇧⌘[", "Ctrl+Shift+["},
		{Shortcut{Key: '1'}, "1", "1"},
		{Shortcut{}, "", ""},
	}
	for _, c := range cases {
		if got := string(appendLabel(nil, c.s, true)); got != c.mac {
			t.Errorf("mac %+v: %q, want %q", c.s, got, c.mac)
		}
		if got := string(appendLabel(nil, c.s, false)); got != c.other {
			t.Errorf("other %+v: %q, want %q", c.s, got, c.other)
		}
	}
	// AppendLabel appends and follows the platform.
	got := string(Shortcut{Key: 'N', Primary: true}.AppendLabel([]byte("New\t")))
	want := "New\tCtrl+N"
	if ui.PrimaryMod() == ui.ModSuper {
		want = "New\t⌘N"
	}
	if got != want {
		t.Errorf("AppendLabel: %q, want %q", got, want)
	}
}

// ---- matching, against a real ui.Context ----

type stubShaper struct{}

func (stubShaper) Metrics(text.FontKey) text.Metrics {
	return text.Metrics{Ascent: 10, Descent: 3, LineHeight: 14}
}
func (stubShaper) Shape(text.FontKey, []byte) text.RunRef { return 0 }
func (stubShaper) Run(text.RunRef) text.RunView           { return text.RunView{} }
func (stubShaper) RunGeneration() uint32                  { return 0 }
func (stubShaper) Glyph(text.FaceID, uint16, uint8) text.GlyphEntry {
	return text.GlyphEntry{}
}
func (stubShaper) AtlasGeneration() uint32       { return 0 }
func (stubShaper) Pages() int                    { return 0 }
func (stubShaper) PageInfo(int) (int, int, bool) { return 0, 0, false }
func (stubShaper) Dirty(int) (x, y, w, h int, pixels []byte, stride int, ok bool) {
	return
}
func (stubShaper) ClearDirty(int) {}

type rig struct {
	t    *testing.T
	c    *ui.Context
	in   ui.Input
	list gpu.DrawList
	m    *Model
	now  int64
}

func newRig(t *testing.T) *rig {
	m, err := NewModel(testCommands(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, c: ui.New(stubShaper{}), m: m}
	r.in.Inside, r.in.Focused = true, true
	r.in.MouseX, r.in.MouseY = -10, -10
	return r
}

func (r *rig) frame(build func(c *ui.Context)) {
	r.now += 16e6
	r.in.Time = r.now
	r.c.Begin(&r.in, 400, 300, 1, 1.0/60)
	if build != nil {
		build(r.c)
	}
	r.c.End(&r.list)
	r.in.ClearFrame()
}

func (r *rig) chord(code ui.KeyCode, mods ui.Mods) {
	r.in.PushKey(ui.KeyEvent{Code: code, Mods: mods, Down: true})
}

// fired collects the commands of a frame in order.
func (r *rig) fired(build func(c *ui.Context)) []ID {
	var got []ID
	r.frame(func(c *ui.Context) {
		r.m.Match(c)
		if build != nil {
			build(c)
		}
		for {
			id, ok := r.m.Next()
			if !ok {
				break
			}
			got = append(got, id)
		}
	})
	return got
}

func idsString(ids []ID) string {
	var b strings.Builder
	for _, id := range ids {
		b.WriteByte('0' + byte(id))
	}
	return b.String()
}

func TestMatchShortcuts(t *testing.T) {
	r := newRig(t)
	pm := ui.PrimaryMod()
	r.chord('N', pm)
	if got := idsString(r.fired(nil)); got != string([]byte{'0' + byte(cmdNew)}) {
		t.Fatalf("⌘N fired %q", got)
	}
	// Exact modifiers: ⇧⌘S is Save As, ⌘S is nothing, ⌥⌘N is nothing.
	r.chord('S', pm)
	r.chord('N', pm|ui.ModAlt)
	r.chord('S', pm|ui.ModShift)
	if got := r.fired(nil); len(got) != 1 || got[0] != cmdSave {
		t.Fatalf("fired %v, want only Save As", got)
	}
	// Releases and repeats of unrelated keys do nothing; a held chord fires per event.
	r.in.PushKey(ui.KeyEvent{Code: 'N', Mods: pm}) // a release
	r.chord('N', pm)
	r.chord('N', pm)
	if got := r.fired(nil); len(got) != 2 {
		t.Fatalf("two presses fired %v", got)
	}
	// Nothing is left over for the next frame.
	if got := r.fired(nil); len(got) != 0 {
		t.Fatalf("stale commands: %v", got)
	}
}

func TestMatchClaimsAndRespectsClaims(t *testing.T) {
	r := newRig(t)
	pm := ui.PrimaryMod()
	r.chord('N', pm)
	r.chord('D', pm) // a disabled command
	r.chord('X', pm) // nobody's
	r.m.SetEnabled(cmdOff, false)
	var claimed [3]bool
	r.frame(func(c *ui.Context) {
		r.m.Match(c)
		for i := range claimed {
			_, claimed[i] = c.KeyAt(i)
		}
	})
	if !claimed[0] || claimed[1] || claimed[2] {
		t.Fatalf("claimed %v: only the matched key (⌘N) is claimed; a disabled command's and unknown keys stay free", claimed)
	}
	// A key somebody claimed first does not match.
	r.chord('N', pm)
	var got []ID
	r.frame(func(c *ui.Context) {
		c.ClaimKey(0)
		r.m.Match(c)
		for id, ok := r.m.Next(); ok; id, ok = r.m.Next() {
			got = append(got, id)
		}
	})
	if len(got) != 0 {
		t.Fatalf("a claimed key fired %v", got)
	}
	// Re-enabling makes it work again.
	r.m.SetEnabled(cmdOff, true)
	r.chord('D', pm)
	if got := r.fired(nil); len(got) != 1 || got[0] != cmdOff {
		t.Fatalf("re-enabled: %v", got)
	}
}

func TestFiredAndNextDeliverOnceInOrder(t *testing.T) {
	r := newRig(t)
	pm := ui.PrimaryMod()
	r.in.PushCommand(uint16(cmdSave)) // from a native menu
	r.in.PushCommand(uint16(cmdNew))
	r.in.PushCommand(777) // not a command of the model
	r.chord('N', pm)      // and by shortcut
	r.frame(func(c *ui.Context) {
		r.m.Match(c)
		r.m.Match(c) // asking again adds nothing
		if r.m.Fired(cmdQuit) {
			t.Error("Fired for a command that did not fire")
		}
		if !r.m.Fired(cmdNew) { // takes the first New
			t.Fatal("New not fired")
		}
		var rest []ID
		for id, ok := r.m.Next(); ok; id, ok = r.m.Next() {
			rest = append(rest, id)
		}
		// System commands come first, in order; New was taken, its second delivery
		// (the shortcut) is still there.
		if len(rest) != 2 || rest[0] != cmdSave || rest[1] != cmdNew {
			t.Errorf("Next gave %v, want Save then New", rest)
		}
		if r.m.Fired(cmdNew) || r.m.Fired(cmdSave) {
			t.Error("a command delivered twice")
		}
	})
}

func TestEditRolesActOnTheFocusedField(t *testing.T) {
	r := newRig(t)
	var buf ui.TextBuffer
	var copied []byte
	r.c.ClipboardSet = func(b []byte) { copied = append(copied[:0], b...) }
	var key ui.Key
	field := func(c *ui.Context) {
		c.NextSize(ui.Px(200), ui.Px(24))
		key = c.KeyOf(c.TextField("f", &buf).ID)
	}
	buf.SetString("hello")
	buf.SelectAll()
	r.frame(field)
	// Without a focused field the copy command fires like any other (a list copies its selection).
	r.in.PushCommand(uint16(cmdCopy))
	if got := r.fired(field); len(got) != 1 || got[0] != cmdCopy || copied != nil {
		t.Fatalf("no field focused: fired %v, copied %q", got, copied)
	}
	r.c.SetFocus(key)
	r.frame(field)
	// With the field focused, a native Edit → Copy acts on the field, once, and does not fire.
	r.in.PushCommand(uint16(cmdCopy))
	if got := r.fired(field); len(got) != 0 || string(copied) != "hello" {
		t.Fatalf("field focused: fired %v, copied %q", got, copied)
	}
	copied = nil
	// The shortcut does the same, whether the field is built before or after Match.
	for _, before := range []bool{false, true} {
		copied = nil
		r.chord('C', ui.PrimaryMod())
		var got []ID
		r.frame(func(c *ui.Context) {
			if before {
				field(c)
			}
			r.m.Match(c)
			if !before {
				field(c)
			}
			for id, ok := r.m.Next(); ok; id, ok = r.m.Next() {
				got = append(got, id)
			}
		})
		if len(got) != 0 || string(copied) != "hello" {
			t.Fatalf("⌘C with the field built before Match=%v: fired %v, copied %q", before, got, copied)
		}
	}
	// An edit command delivered after the field was built waits for the next frame and still acts once.
	copied = nil
	r.in.PushCommand(uint16(cmdCopy))
	r.frame(func(c *ui.Context) {
		field(c)
		r.m.Match(c)
	})
	if copied != nil {
		t.Fatalf("copied in the frame the field was already built: %q", copied)
	}
	r.frame(field)
	if string(copied) != "hello" {
		t.Fatalf("the queued copy was lost: %q", copied)
	}
	// A disabled edit command does nothing.
	copied = nil
	r.m.SetEnabled(cmdCopy, false)
	r.in.PushCommand(uint16(cmdCopy))
	r.fired(field)
	r.frame(field)
	if copied != nil {
		t.Fatalf("a disabled Copy acted: %q", copied)
	}
}

func TestMatchAllocatesNothing(t *testing.T) {
	r := newRig(t)
	pm := ui.PrimaryMod()
	var buf ui.TextBuffer
	build := func(c *ui.Context) {
		r.m.Match(c)
		c.NextSize(ui.Px(100), ui.Px(20))
		c.TextField("f", &buf)
		r.m.Fired(cmdNew)
		for _, ok := r.m.Next(); ok; _, ok = r.m.Next() {
		}
	}
	frame := func() {
		r.chord('N', pm)
		r.chord('S', pm|ui.ModShift)
		r.in.PushCommand(uint16(cmdQuit))
		r.frame(build)
	}
	for i := 0; i < 5; i++ {
		frame()
	}
	if n := testing.AllocsPerRun(50, frame); n != 0 {
		// Say what allocates (as cmd/pilot -allocsites does): a frame that
		// allocates every time shows its stacks here, and a one-off in the
		// process (a runtime thread or GC worker start) shows none.
		t.Fatalf("%v allocations per frame; allocation sites of 50 more frames (none: a one-off):\n%s",
			n, profileAllocs(func() {
				for i := 0; i < 50; i++ {
					frame()
				}
			}))
	}
}

// profileAllocs runs f with every allocation profiled and returns the call
// stacks that allocated during it.
func profileAllocs(f func()) string {
	old := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = old }()
	snapshot := func() map[[32]uintptr]int64 {
		runtime.GC()
		runtime.GC()
		recs := make([]runtime.MemProfileRecord, 1024)
		n, ok := runtime.MemProfile(recs, true)
		for !ok {
			recs = make([]runtime.MemProfileRecord, n+256)
			n, ok = runtime.MemProfile(recs, true)
		}
		m := make(map[[32]uintptr]int64, n)
		for _, r := range recs[:n] {
			m[r.Stack0] += r.AllocObjects
		}
		return m
	}
	before := snapshot()
	f()
	var sb strings.Builder
	for stk, n := range snapshot() {
		if n-before[stk] <= 0 {
			continue
		}
		fmt.Fprintf(&sb, "  %d object(s):\n", n-before[stk])
		pcs := stk[:]
		for i, pc := range pcs {
			if pc == 0 {
				pcs = pcs[:i]
				break
			}
		}
		frames := runtime.CallersFrames(pcs)
		for {
			fr, more := frames.Next()
			fmt.Fprintf(&sb, "    %s:%d\n", fr.Function, fr.Line)
			if !more {
				break
			}
		}
	}
	return sb.String()
}

// modalRig is a window with a modal overlay open, holding a text field with a
// selection, and optionally a field in the main tree.
type modalRig struct {
	*rig
	main, modal ui.TextBuffer
	copied      []string
	fired       []ID
	withModal   bool
	withMain    bool
	mainKey     ui.Key
}

func newModalRig(t *testing.T, cmds []Command) *modalRig {
	m, err := NewModel(cmds, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &modalRig{rig: newRig(t), withModal: true}
	r.m = m
	r.main.SetString("main text")
	r.main.SelectAll()
	r.modal.SetString("hello")
	r.modal.SelectAll()
	r.c.ClipboardSet = func(b []byte) { r.copied = append(r.copied, string(b)) }
	for i := 0; i < 4; i++ {
		r.step()
	}
	return r
}

func (r *modalRig) build(c *ui.Context) {
	r.m.Match(c)
	if r.withMain {
		c.NextSize(ui.Px(200), ui.Px(24))
		r.mainKey = c.KeyOf(c.TextField("main", &r.main).ID)
	}
	if r.withModal {
		c.BeginOverlay(1, ui.OverlayModal, [4]float32{0, 0, 400, 300}, ui.PlaceCenter)
		c.NextSize(ui.Px(200), ui.Px(24))
		c.TextField("modal", &r.modal)
		c.EndOverlay()
	}
	for id, ok := r.m.Next(); ok; id, ok = r.m.Next() {
		r.fired = append(r.fired, id)
	}
}

// step runs a frame and returns what fired in it, with the copies it made.
func (r *modalRig) step() []ID {
	r.fired, r.copied = nil, nil
	r.frame(r.build)
	return r.fired
}

func TestCommandsWhileAModalIsOpen(t *testing.T) {
	pm := ui.PrimaryMod()
	cmds := append(testCommands(), Command{ID: 20, Title: "Help", InModal: true, Shortcut: Shortcut{Key: 'H', Primary: true}})

	t.Run("other commands stay blocked and their keys stay free", func(t *testing.T) {
		r := newModalRig(t, cmds)
		r.in.PushCommand(uint16(cmdSave)) // from a native menu
		r.in.PushCommand(uint16(cmdQuit))
		r.chord('N', pm) // a shortcut
		var free bool
		r.fired, r.copied = nil, nil
		r.frame(func(c *ui.Context) {
			r.build(c)
			_, claimed := c.KeyAt(0)
			free = !claimed
		})
		if len(r.fired) != 0 {
			t.Fatalf("commands fired beneath a modal: %v", r.fired)
		}
		if !free {
			t.Fatal("a blocked shortcut was claimed: the dialog's own widgets could not take it")
		}
	})

	t.Run("a command marked InModal fires", func(t *testing.T) {
		r := newModalRig(t, cmds)
		r.in.PushCommand(20)
		if got := r.step(); len(got) != 1 || got[0] != 20 {
			t.Fatalf("system command fired %v", got)
		}
		r.chord('H', pm)
		var claimed bool
		r.fired = nil
		r.frame(func(c *ui.Context) {
			r.build(c)
			_, claimed = c.KeyAt(0)
		})
		if len(r.fired) != 1 || r.fired[0] != 20 || !claimed {
			t.Fatalf("shortcut fired %v, claimed %v", r.fired, claimed)
		}
	})

	t.Run("edit commands reach the focused field in the modal", func(t *testing.T) {
		r := newModalRig(t, cmds)
		if !r.c.TextFieldFocused() {
			t.Fatal("the field in the modal does not have the focus")
		}
		r.in.PushCommand(uint16(cmdCopy))
		if got := r.step(); len(got) != 0 {
			t.Fatalf("Copy fired as a command: %v", got)
		}
		if len(r.copied) != 1 || r.copied[0] != "hello" {
			t.Fatalf("Copy from a native menu gave %q, want the selection once", r.copied)
		}
		// the same through a shortcut the field did not claim (here: none, the
		// field claims its chords; ⌘C acts once)
		r.chord('C', pm)
		if got := r.step(); len(got) != 0 || len(r.copied) != 1 {
			t.Fatalf("⌘C: fired %v, copied %q", got, r.copied)
		}
		// Undo through the field's history, from the menu
		r.chord('X', pm) // cut: deletes the selection
		r.step()
		if r.modal.String() != "" {
			t.Fatalf("cut left %q", r.modal.String())
		}
		r.in.PushCommand(uint16(cmdUndo))
		if got := r.step(); len(got) != 0 || r.modal.String() != "hello" {
			t.Fatalf("Undo from the menu: fired %v, text %q", got, r.modal.String())
		}
	})

	t.Run("edit commands without a field in the modal are dropped", func(t *testing.T) {
		r := newModalRig(t, cmds)
		r.withMain, r.withModal = true, true
		r.c.SetFocus(0)
		r.step()
		r.step()
		// a field in the main tree has the focus, beneath the modal: not eligible
		r.c.SetFocus(r.mainKey)
		r.in.PushCommand(uint16(cmdCopy))
		if got := r.step(); len(got) != 0 || len(r.copied) != 0 {
			t.Fatalf("Copy beneath a modal: fired %v, copied %q", got, r.copied)
		}
		r.c.SetFocus(0)
		r.step()
		r.in.PushCommand(uint16(cmdPaste))
		if got := r.step(); len(got) != 0 {
			t.Fatalf("Paste with no field focused fired beneath a modal: %v", got)
		}
	})

	t.Run("without a modal everything works", func(t *testing.T) {
		r := newModalRig(t, cmds)
		r.withModal = false
		r.step()
		r.step()
		r.in.PushCommand(uint16(cmdSave))
		if got := r.step(); len(got) != 1 || got[0] != cmdSave {
			t.Fatalf("fired %v", got)
		}
	})
}
