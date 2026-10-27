//go:build darwin

package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"gonativeui/platform"
	"gonativeui/ui"
)

// A panel (Config.Panel) opens hidden with its first frame drawn, builds
// nothing while hidden however often it is woken, draws again once shown,
// keeps its App and ui state across Hide and Show, and 100 Show/Hide pairs
// leave no window object behind. The case runs in a child process, as an
// app (Main) with the panel as its only window.
func TestPanelShowHideKeepsStateAndBuildsNothingHidden(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a panel")
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), nestedChild+"=panel")
	out, err := cmd.CombinedOutput()
	s := string(out)
	if i := strings.Index(s, "SKIP:"); i >= 0 {
		t.Skip(strings.TrimSpace(s[i+5:]))
	}
	ok := false
	for _, l := range strings.Split(s, "\n") {
		if f, found := strings.CutPrefix(l, "FAIL: "); found {
			t.Error(f)
		}
		if strings.HasPrefix(l, "panel: ") {
			t.Log(l)
			ok = ok || l == "panel: done"
		}
	}
	if err != nil || !ok {
		t.Fatalf("the child did not finish (%v):\n%s", err, s)
	}
}

type panelBuilder struct {
	a      *App
	builds int
	showT  int64   // when Show was asked for; 0 once the first build after it ran
	showMs float64 // Show to the first build after it
}

func (b *panelBuilder) Init(a *App) error { b.a = a; return nil }

func (b *panelBuilder) Build(c *ui.Context) {
	b.builds++
	if b.showT != 0 {
		b.showMs, b.showT = float64(platform.Now()-b.showT)/1e6, 0
	}
}

type panelScene struct {
	DefaultHandler
	b        panelBuilder
	win      *Win
	ui       *ui.Context
	hidden   int // builds when the panel was last hidden
	cycles   int
	objects  platform.WindowObjects
	failures int
}

func (s *panelScene) fail(format string, args ...any) {
	s.failures++
	fmt.Printf("FAIL: "+format+"\n", args...)
}

// wakes wakes the panel's window from a goroutine, n times over d, then
// posts step.
func (s *panelScene) wakes(n int, d time.Duration, step uint64) {
	go func() {
		for i := 0; i < n; i++ {
			time.Sleep(d / time.Duration(n))
			s.win.Wake()
		}
		time.Sleep(50 * time.Millisecond)
		Post(Event{ID: step}, nil)
	}()
}

func (s *panelScene) Start(a *App) error {
	w, err := a.OpenWindow(Config{Title: "panel", Width: 300, Height: 80, Panel: &platform.Panel{NonActivating: true, Floating: true, Radius: 12}}, &s.b)
	if err != nil {
		fmt.Println("SKIP: cannot open a panel here:", err)
		a.Quit()
		return nil
	}
	s.win, s.ui = w, w.App().UI
	if s.b.builds == 0 || w.App().Win.Visible() || !w.App().shelved {
		s.fail("opened: builds %d, visible %v, shelved %v; want its first frame drawn, hidden", s.b.builds, w.App().Win.Visible(), w.App().shelved)
	}
	if c := w.App().cfg.Clear; c != [4]float32{} {
		s.fail("a panel's clear colour is %v, want transparent", c)
	}
	s.hidden = s.b.builds
	s.wakes(25, 500*time.Millisecond, 1)
	return nil
}

func (s *panelScene) Event(a *App, e *Event) {
	if e.Kind != EvPost {
		a.DefaultEvent(e)
		return
	}
	w := s.win.App()
	switch e.ID {
	case 1: // hidden since it opened, woken 25 times
		if s.b.builds != s.hidden {
			s.fail("a hidden panel built %d times", s.b.builds-s.hidden)
		}
		s.b.showT = platform.Now()
		s.win.Show()
		go func() { time.Sleep(300 * time.Millisecond); Post(Event{ID: 2}, nil) }()
	case 2: // shown
		if !w.Win.Visible() || s.b.builds == s.hidden || w.shelved {
			s.fail("shown: visible %v, builds %d (%d before), shelved %v", w.Win.Visible(), s.b.builds, s.hidden, w.shelved)
		}
		fmt.Printf("panel: show to the first build %.2f ms\n", s.b.showMs)
		fmt.Printf("panel: state %s\n", w.StateLine(nil, platform.Now()))
		if s.win.App() != w || w.UI != s.ui {
			s.fail("Show made a new App or ui context")
		}
		s.win.Hide()
		s.hidden = s.b.builds
		s.wakes(15, 300*time.Millisecond, 3)
	case 3: // hidden again, woken 15 times
		if w.Win.Visible() || s.b.builds != s.hidden {
			s.fail("hidden: visible %v, built %d times", w.Win.Visible(), s.b.builds-s.hidden)
		}
		s.objects = platform.LiveWindowObjects()
		Post(Event{ID: 4}, nil)
	case 4, 5: // 100 Show/Hide pairs, one command a turn
		if e.ID == 4 {
			s.win.Show()
			Post(Event{ID: 5}, nil)
			return
		}
		s.win.Hide()
		if s.cycles++; s.cycles < 100 {
			Post(Event{ID: 4}, nil)
			return
		}
		go func() { time.Sleep(100 * time.Millisecond); Post(Event{ID: 6}, nil) }()
	case 6:
		if o := platform.LiveWindowObjects(); o != s.objects {
			s.fail("100 Show/Hide pairs: window objects %+v → %+v", s.objects, o)
		}
		if w.Win.Visible() || !w.shelved {
			s.fail("after the cycles: visible %v, shelved %v", w.Win.Visible(), w.shelved)
		}
		fmt.Printf("panel: %d builds in all, %d cycles\n", s.b.builds, s.cycles)
		if s.failures == 0 {
			fmt.Println("panel: done")
		}
		a.Quit()
	}
}

func runPanelChild() {
	if err := Main(AppConfig{}, &panelScene{}); err != nil {
		fmt.Println("FAIL: Main:", err)
	}
}
