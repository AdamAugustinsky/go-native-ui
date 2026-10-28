//go:build windows

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
	"unsafe"

	"gonativeui/app"
	"gonativeui/internal/allocsite"
	"gonativeui/internal/childapp"
	"gonativeui/internal/testlock"
	"gonativeui/internal/win32"
	"gonativeui/platform"
	"gonativeui/ui"
)

// Global hotkeys on Windows (the phase 8 plan's W7, D70), and the palette
// they open (W2): live scenarios in child processes of the test binary, on
// their main thread, as on macOS. Key presses are typed with SendInput,
// which reaches RegisterHotKey on both runners (probe P7; windows-11-arm's
// shell surfaces are cleared by CI's step before the tests).

func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	childapp.Register("hotkeys", func() { app.Main(app.AppConfig{KeepRunning: true}, &scene{}) })
	childapp.Register("holder", func() { app.Main(app.AppConfig{KeepRunning: true}, &holder{}) })
	childapp.Register("palette", func() { app.Main(app.AppConfig{KeepRunning: true}, &palette{}) })
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
	chordA    = Shortcut{Key: ui.KeyF19, Mods: ui.ModCtrl | ui.ModAlt | ui.ModShift} // Ctrl+Alt+Shift+F19
	chordB    = Shortcut{Key: ui.KeyF18, Mods: ui.ModCtrl | ui.ModAlt | ui.ModShift}
	chordHeld = Shortcut{Key: ui.KeyF17, Mods: ui.ModCtrl | ui.ModAlt | ui.ModShift}
)

// ---- SendInput ------------------------------------------------------------------

const (
	inputKeyboard = 1
	keyEventUp    = 0x0002
	vkShiftKey    = 0x10
	vkControlKey  = 0x11
	vkMenuKey     = 0x12
)

// input is INPUT on 64-bit Windows (40 bytes), keybdInput KEYBDINPUT.
type input struct {
	typ uint32
	_   uint32
	u   [32]byte
}

type keybdInput struct {
	vk, scan    uint16
	flags, time uint32
	extra       uintptr
}

var (
	user32t     = win32.LoadSystem("user32.dll")
	pSendInput  = win32.Proc(user32t, "SendInput")
	pMapVKTest  = win32.Proc(user32t, "MapVirtualKeyW")
	chordInputs [8]input
)

// typeChord types s with SendInput (Ctrl, Alt and Shift around its key)
// and reports how many key events the system took.
func typeChord(s Shortcut) uintptr {
	vk, _ := virtualKey(s.Key)
	keys := [4]uint16{vkControlKey, vkMenuKey, vkShiftKey, uint16(vk)}
	n := 0
	key := func(vk uint16, up bool) {
		k := keybdInput{vk: vk, scan: uint16(win32.Call(pMapVKTest, uintptr(vk), 0))}
		if up {
			k.flags = keyEventUp
		}
		chordInputs[n] = input{typ: inputKeyboard}
		*(*keybdInput)(unsafe.Pointer(&chordInputs[n].u)) = k
		n++
	}
	for _, k := range keys {
		key(k, false)
	}
	for i := len(keys) - 1; i >= 0; i-- {
		key(keys[i], true)
	}
	return win32.Call(pSendInput, uintptr(n), uintptr(unsafe.Pointer(&chordInputs[0])), unsafe.Sizeof(input{}))
}

// ---- Register, ErrTaken, Simulate, a typed press ---------------------------------

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
	if e.Kind != app.EvPost {
		a.DefaultEvent(e)
		return
	}
	switch e.ID {
	case 1:
		s.run()
		// A real press: the chord typed with SendInput reaches the app as one
		// press of hotkey 1 (MOD_NOREPEAT), whichever window has the
		// foreground.
		must(Register(1, chordA) == nil, "Register(1, Ctrl+Alt+Shift+F19) again")
		s.presses = s.presses[:0]
		sent := typeChord(chordA)
		childapp.Say("typed=%d", sent)
		go func() {
			for i := 0; i < 60; i++ {
				time.Sleep(50 * time.Millisecond)
				app.Post(app.Event{ID: 2}, nil)
			}
			app.Post(app.Event{ID: 3}, nil)
		}()
	case 2:
		if len(s.presses) > 0 {
			s.finish(a)
		}
	case 3:
		s.finish(a)
	}
}

func (s *scene) finish(a *app.App) {
	if s.presses == nil {
		return
	}
	childapp.Say("typed_presses=%d", len(s.presses))
	must(len(s.presses) == 1 && s.presses[0] == 1, "the typed chord delivered %v, want one press of 1", s.presses)
	UnregisterAll()
	s.presses = nil
	childapp.Say("done=1")
	a.Quit()
}

func (s *scene) run() {
	// Register, read back, the same chord again, another ID on it.
	must(Register(1, chordA) == nil, "Register(1, Ctrl+Alt+Shift+F19)")
	got, ok := Registered(1)
	must(ok && got == chordA, "Registered(1) = %+v %v", got, ok)
	must(Register(1, chordA) == nil, "the same chord again")
	must(errors.Is(Register(2, chordA), ErrTaken), "a second ID on a held chord is not ErrTaken")
	_, ok = Registered(2)
	must(!ok, "a refused hotkey is registered")
	must(errors.Is(Register(3, Shortcut{Key: 0x1FF, Mods: ui.ModCtrl}), ErrInvalid), "a key Windows has no virtual key for")

	// A press through the app window's procedure, before Simulate returns.
	must(Simulate(1) == nil, "Simulate(1)")
	must(len(s.presses) == 1 && s.presses[0] == 1, "presses after one Simulate: %v", s.presses)
	must(Simulate(2) != nil, "Simulate of an ID not registered")

	// Presses allocate nothing (the handler and the delivery), as on macOS.
	s.presses = make([]ID, 0, 2000)
	restore := allocsite.Enable()
	var s0, s1 allocsite.Snapshot
	s0.Take()
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
	must(Register(1, chordB) == nil, "Register(1, Ctrl+Alt+Shift+F18)")
	must(Register(2, chordA) == nil, "Ctrl+Alt+Shift+F19 is still held after ID 1 moved")
	Unregister(2)
	Unregister(2) // twice: nothing
	_, ok = Registered(2)
	must(!ok, "Registered after Unregister")
	must(Simulate(2) != nil, "Simulate after Unregister")
	must(Register(3, chordA) == nil, "Ctrl+Alt+Shift+F19 is not free after Unregister")
	UnregisterAll()
	_, ok1 := Registered(1)
	_, ok3 := Registered(3)
	must(!ok1 && !ok3 && hk.n == 0, "UnregisterAll left %d", hk.n)
	must(Register(4, chordB) == nil, "Ctrl+Alt+Shift+F18 is not free after UnregisterAll")
	UnregisterAll()

	// Full.
	for i := 0; i < MaxHotkeys; i++ {
		s := Shortcut{Key: ui.KeyF1 + ui.KeyCode(i%20), Mods: ui.ModCtrl | ui.ModAlt | ui.ModShift}
		if i >= 20 {
			s.Key = ui.KeyNum0 + ui.KeyCode(i-20)%10
			if i >= 30 {
				s = Shortcut{Key: ui.KeyF13 + ui.KeyCode(i-30), Mods: ui.ModCtrl | ui.ModAlt}
			}
		}
		must(Register(ID(100+i), s) == nil, "hotkey %d of %d (%+v)", i+1, MaxHotkeys, s)
	}
	must(errors.Is(Register(200, chordA), ErrFull), "past MaxHotkeys")
	UnregisterAll()
}

func TestLiveHotkeys(t *testing.T) {
	testlock.Hold(t, "activation") // it types: the keys reach the foreground window, maybe another test's
	r := childapp.Run(t, "hotkeys", 60*time.Second)
	if r.KV["done"] != "1" {
		t.Fatalf("the scenario did not finish\n%s", r.Out)
	}
	t.Logf("mallocs per press %s, ns per press %s; the typed chord: %s key events, %s presses", r.KV["mallocs_per_press"], r.KV["ns_per_press"], r.KV["typed"], r.KV["typed_presses"])
	if r.KV["mallocs_per_press"] != "0.000" {
		t.Errorf("a press allocates: %s Go objects of ours\n%s", r.KV["mallocs_per_press"], r.Out)
	}
}

// ---- A chord another process holds -------------------------------------------------

// holder holds chordHeld until its stdin closes; as the taker it tries the
// chord and reports what Register said.
type holder struct{}

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
		if os.Getenv("GNUI_HOTKEY_TAKER") != "" {
			err := Register(1, chordHeld)
			childapp.Say("taken=%v registered=%v", errors.Is(err, ErrTaken), err == nil)
			UnregisterAll()
			a.Quit()
			return
		}
		must(Register(1, chordHeld) == nil, "the holder cannot register Ctrl+Alt+Shift+F17")
		childapp.Say("held=1")
		go func() { bufio.NewReader(os.Stdin).ReadString('\n'); app.Post(app.Event{ID: 2}, nil) }()
	case 2:
		UnregisterAll()
		a.Quit()
	}
}

// A chord another process holds fails with ErrTaken on Windows
// (ERROR_HOTKEY_ALREADY_REGISTERED), which a settings screen shows.
func TestHotkeyHeldByAnotherProcess(t *testing.T) {
	h := exec.Command(os.Args[0], "-test.run=^$")
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
	r := childapp.Run(t, "holder", 30*time.Second, "GNUI_HOTKEY_TAKER=1")
	if r.KV["taken"] != "true" {
		t.Fatalf("a chord another process holds: ErrTaken %s, registered %s\n%s", r.KV["taken"], r.KV["registered"], r.Out)
	}
}

func TestLabels(t *testing.T) {
	s := Shortcut{Key: 'K', Mods: ui.ModAlt, Primary: true}
	if got := string(s.AppendLabel(nil)); got != "Ctrl+Alt+K" {
		t.Errorf("Ctrl+Alt+K's label on Windows is %q", got)
	}
	if vk, ok := virtualKey('K'); !ok || vk != 'K' { // the runners' layout is US
		t.Errorf("K's virtual key is %#x %v", vk, ok)
	}
	if vk, ok := virtualKey(ui.KeySlash); !ok || vk != 0xBF { // VK_OEM_2 on US
		t.Errorf("/'s virtual key is %#x %v", vk, ok)
	}
	if winMods(Shortcut{Key: 'K', Mods: ui.ModAlt, Primary: true}.Effective()) != modAlt|modControl {
		t.Error("Primary is not Ctrl on Windows")
	}
}

// ---- The palette: a hotkey shows a panel, which takes the keyboard and hides
// when another window is activated -------------------------------------------------

type panelBuilder struct {
	builds int
	pressT int64   // when the press arrived; 0 once the first build after it ran
	ms     float64 // the press to that build
}

func (b *panelBuilder) Build(c *ui.Context) {
	b.builds++
	if b.pressT != 0 {
		b.ms, b.pressT = float64(platform.Now()-b.pressT)/1e6, 0
	}
}

type plainBuilder struct{}

func (plainBuilder) Build(*ui.Context) {}

type palette struct {
	main, panel *app.Win
	b           panelBuilder
	presses     int
	built       int // builds before the press
	waited      int
	retries     int // presses again because Windows refused the foreground
}

func (p *palette) Start(a *app.App) error {
	var err error
	if p.main, err = a.OpenWindow(app.WindowConfig{Title: "main", Width: 400, Height: 300}, plainBuilder{}); err != nil {
		childapp.Fail("main window: %v", err)
	}
	pc := &platform.Panel{Floating: true, HideOnDeactivate: true, Shadow: true, Radius: 12}
	if p.panel, err = a.OpenWindow(app.WindowConfig{Title: "palette", Width: 560, Height: 72, Panel: pc}, &p.b); err != nil {
		childapp.Fail("panel: %v", err)
	}
	must(!p.panel.App().Win.Visible(), "the panel opened visible")
	must(Register(7, chordA) == nil, "Register(7, Ctrl+Alt+Shift+F19)")
	go func() { time.Sleep(500 * time.Millisecond); app.Post(app.Event{ID: 1}, nil) }()
	return nil
}

func (p *palette) later(ms int, id uint64) {
	go func() { time.Sleep(time.Duration(ms) * time.Millisecond); app.Post(app.Event{ID: id}, nil) }()
}

func (p *palette) Event(a *app.App, e *app.Event) {
	if id, ok := Pressed(e); ok && id == 7 {
		p.presses++
		p.b.pressT = platform.Now()
		p.panel.Show()
		return
	}
	if e.Kind != app.EvPost {
		a.DefaultEvent(e)
		return
	}
	pw := p.panel.App().Win
	switch e.ID {
	case 1: // the chord, typed
		p.built = p.b.builds
		childapp.Say("typed=%d", typeChord(chordA))
		p.later(100, 2)
	case 2: // wait for the press and the panel
		if (p.presses == 0 || !pw.Visible() || p.b.builds == p.built) && p.waited < 30 {
			p.waited++
			p.later(100, 2)
			return
		}
		must(p.presses == 1, "the typed chord delivered %d presses", p.presses)
		must(pw.Visible(), "the panel is not visible after the press")
		must(p.b.builds > p.built, "the shown panel built nothing")
		p.later(300, 3)
	case 3:
		if !pw.KeyWindow() && p.retries < 3 {
			// windows-11-arm's shell takes the foreground now and then, and
			// Windows refuses SetForegroundWindow meanwhile; a new press (new
			// input) gives the app the right again, as a user pressing again
			// would.
			p.retries++
			childapp.Say("retry=%d", p.retries)
			typeChord(chordA)
			p.later(400, 3)
			return
		}
		childapp.Say("press_to_build_ms=%.2f key=%v retries=%d", p.b.ms, pw.KeyWindow(), p.retries)
		must(pw.KeyWindow(), "the palette shown from the hotkey is not the foreground window after %d presses", p.retries+1)
		p.main.Focus()
		p.waited = 0
		p.later(100, 4)
	case 4: // another window activated: the palette hides
		if pw.Visible() && p.waited < 30 {
			p.waited++
			p.later(100, 4)
			return
		}
		must(!pw.Visible(), "the palette is still visible after the main window was activated")
		must(p.main.App().Win.KeyWindow(), "the main window is not the foreground window")
		hidden := p.b.builds
		p.panel.Wake()
		p.later(300, 5)
		p.built = hidden
	case 5:
		must(p.b.builds == p.built, "the hidden palette built %d times when woken", p.b.builds-p.built)
		UnregisterAll()
		childapp.Say("done=1")
		a.Quit()
	}
}

func TestLivePalette(t *testing.T) {
	testlock.Hold(t, "activation") // it types and takes the foreground
	r := childapp.Run(t, "palette", 60*time.Second)
	if r.KV["done"] != "1" {
		t.Fatalf("the scenario did not finish\n%s", r.Out)
	}
	t.Logf("the press to the palette's first build: %s ms (typed %s key events)", r.KV["press_to_build_ms"], r.KV["typed"])
}
