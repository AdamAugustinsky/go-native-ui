//go:build darwin

package hotkey

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/allocsite"
	"gonativeui/internal/childapp"
	"gonativeui/ui"
)

func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	childapp.Register("hotkeys", func() { app.Main(app.AppConfig{KeepRunning: true}, &scene{}) })
	childapp.Register("holder", func() { app.Main(app.AppConfig{KeepRunning: true}, &holder{}) })
	if childapp.Serve() {
		return
	}
	os.Exit(m.Run())
}

func must(ok bool, format string, args ...any) {
	if !ok {
		childapp.Fail(format, args...)
	}
}

// Chords nobody uses.
var (
	chordA = Shortcut{Key: ui.KeyF19, Mods: ui.ModCtrl | ui.ModAlt | ui.ModShift, Primary: true} // ⌃⌥⇧⌘F19
	chordB = Shortcut{Key: ui.KeyF18, Mods: ui.ModCtrl | ui.ModAlt | ui.ModShift, Primary: true} // ⌃⌥⇧⌘F18
)

type scene struct {
	presses []ID
}

func (s *scene) Start(a *app.App) error {
	app.Post(app.Event{ID: 1}, nil)
	return nil
}

func (s *scene) Event(a *app.App, e *app.Event) {
	if id, ok := Pressed(e); ok {
		s.presses = append(s.presses, id)
		return
	}
	if e.Kind == app.EvPost && e.ID == 1 {
		s.run()
		a.Quit()
		return
	}
	a.DefaultEvent(e)
}

func (s *scene) run() {
	// Register, read back, the same chord again, another ID on it.
	must(Register(1, chordA) == nil, "Register(1, ⌃⌥⇧⌘F19)")
	got, ok := Registered(1)
	must(ok && got == chordA, "Registered(1) = %+v %v", got, ok)
	must(Register(1, chordA) == nil, "the same chord again")
	must(errors.Is(Register(2, chordA), ErrTaken), "a second ID on a held chord is not ErrTaken")
	_, ok = Registered(2)
	must(!ok, "a refused hotkey is registered")
	must(errors.Is(Register(3, Shortcut{Key: 0x1FF, Mods: ui.ModCtrl}), ErrInvalid), "a key Carbon has no code for")

	// A press through Carbon's own handler, before Simulate returns.
	must(Simulate(1) == nil, "Simulate(1)")
	must(len(s.presses) == 1 && s.presses[0] == 1, "presses after one Simulate: %v", s.presses)
	must(Simulate(2) != nil, "Simulate of an ID not registered")

	// Presses allocate nothing (the handler and the delivery). The Go
	// runtime's own allocations (a new thread, its timers) are not the
	// presses': the gates pass over them (DECISIONS D27); macOS CI's whole
	// run had a few.
	s.presses = make([]ID, 0, 2000)
	restore := allocsite.Enable()
	var s0, s1 allocsite.Snapshot
	s0.Take() // the storage, before the collection
	s1.Take()
	runtime.GC()
	s0.Take()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	t0 := time.Now()
	for i := 0; i < 1000; i++ {
		Simulate(1)
	}
	el := time.Since(t0)
	runtime.ReadMemStats(&m1)
	must(len(s.presses) == 1000, "%d of 1000 presses delivered", len(s.presses))
	own := int64(0)
	if d := m1.Mallocs - m0.Mallocs; d != 0 {
		runtime.GC()
		s1.Take()
		sites := allocsite.Diff(&s0, &s1)
		own = allocsite.Objects(sites, allocsite.Ours) + allocsite.Objects(sites, allocsite.Other)
		var b strings.Builder
		allocsite.Write(&b, sites)
		fmt.Printf("%d allocations in the process over the presses:\n%s", d, b.String())
	}
	restore()
	childapp.Say("mallocs_per_press=%.3f ns_per_press=%d", float64(own)/1000, el.Nanoseconds()/1000)

	// A new chord for an ID releases the old one; Unregister releases it.
	must(Register(1, chordB) == nil, "Register(1, ⌃⌥⇧⌘F18)")
	must(Register(2, chordA) == nil, "⌃⌥⇧⌘F19 is still held after ID 1 moved")
	Unregister(2)
	Unregister(2) // twice: nothing
	_, ok = Registered(2)
	must(!ok, "Registered after Unregister")
	must(Simulate(2) != nil, "Simulate after Unregister")
	must(Register(3, chordA) == nil, "⌃⌥⇧⌘F19 is not free after Unregister")
	UnregisterAll()
	_, ok1 := Registered(1)
	_, ok3 := Registered(3)
	must(!ok1 && !ok3 && hk.n == 0, "UnregisterAll left %d", hk.n)
	must(Register(4, chordB) == nil, "⌃⌥⇧⌘F18 is not free after UnregisterAll")
	UnregisterAll()

	// Full.
	for i := 0; i < MaxHotkeys; i++ {
		must(Register(ID(100+i), Shortcut{Key: ui.KeyF1 + ui.KeyCode(i%20), Mods: ui.ModCtrl | ui.ModAlt | ui.ModShift, Primary: i >= 20}) == nil, "hotkey %d of %d", i+1, MaxHotkeys)
	}
	must(errors.Is(Register(200, chordA), ErrFull), "past MaxHotkeys")
	UnregisterAll()
	childapp.Say("done=1")
}

func TestLiveHotkeys(t *testing.T) {
	r := childapp.Run(t, "hotkeys", 60*time.Second)
	if r.KV["done"] != "1" {
		t.Fatalf("the scenario did not finish\n%s", r.Out)
	}
	t.Logf("mallocs per press %s, ns per press %s", r.KV["mallocs_per_press"], r.KV["ns_per_press"])
	if r.KV["mallocs_per_press"] != "0.000" {
		t.Errorf("a press allocates: %s Go objects of ours\n%s", r.KV["mallocs_per_press"], r.Out)
	}
}

// holder holds ⌃⌥⇧⌘F17 until its stdin closes.
type holder struct{}

var chordHeld = Shortcut{Key: ui.KeyF17, Mods: ui.ModCtrl | ui.ModAlt | ui.ModShift, Primary: true}

func (holder) Start(a *app.App) error {
	app.Post(app.Event{ID: 1}, nil)
	return nil
}

func (holder) Event(a *app.App, e *app.Event) {
	if e.Kind != app.EvPost {
		a.DefaultEvent(e)
		return
	}
	switch e.ID {
	case 1:
		if len(os.Args) > 0 && os.Getenv("GNUI_HOTKEY_TAKER") != "" {
			err := Register(1, chordHeld)
			childapp.Say("taken=%v registered=%v", errors.Is(err, ErrTaken), err == nil)
			a.Quit()
			return
		}
		must(Register(1, chordHeld) == nil, "the holder cannot register ⌃⌥⇧⌘F17")
		childapp.Say("held=1")
		go func() { bufio.NewReader(os.Stdin).ReadString('\n'); app.Post(app.Event{ID: 2}, nil) }()
	case 2:
		UnregisterAll()
		a.Quit()
	}
}

// A chord another process holds: what RegisterEventHotKey says in the second
// process. The answer is recorded in the phase 8 plan's W7 note; the test
// requires only that the second registration either fails with ErrTaken or
// succeeds, never another error.
func TestHotkeyHeldByAnotherProcess(t *testing.T) {
	h := exec.Command(os.Args[0])
	h.Env = append(os.Environ(), "GNUI_CHILDAPP=holder")
	in, _ := h.StdinPipe()
	out, _ := h.StdoutPipe()
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { in.Close(); h.Wait() }()
	sc := bufio.NewScanner(out)
	held := false
	for sc.Scan() {
		if strings.Contains(sc.Text(), "held=1") {
			held = true
			break
		}
		if strings.HasPrefix(sc.Text(), "FAIL") {
			t.Fatalf("holder: %s", sc.Text())
		}
	}
	if !held {
		t.Fatal("the holder did not register its chord")
	}
	t.Setenv("GNUI_HOTKEY_TAKER", "1")
	r := childapp.Run(t, "holder", 30*time.Second)
	t.Logf("a chord another process holds: ErrTaken %s, registered %s", r.KV["taken"], r.KV["registered"])
	if r.KV["taken"] != "true" && r.KV["registered"] != "true" {
		t.Fatalf("the second process got another error:\n%s", r.Out)
	}
}

func TestLabels(t *testing.T) {
	s := Shortcut{Key: 'K', Mods: ui.ModAlt, Primary: true}
	if got := string(s.AppendLabel(nil)); got != "⌥⌘K" {
		t.Errorf("⌥⌘K's label on macOS is %q", got)
	}
}
