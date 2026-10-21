package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/login"
	"gonativeui/app/notify"
	"gonativeui/app/prefs"
	"gonativeui/app/winstate"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// recorder is a host that remembers what it was asked and posts, badges or
// stores nothing: no test touches the notification center, the Dock or the
// Keychain.
type recorder struct {
	mu     sync.Mutex
	posted []notify.Note
	badge  int
	title  string
	token  []byte
	logins []bool
	err    error // what every call answers
}

func (r *recorder) Post(n notify.Note) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.posted = append(r.posted, n)
	return r.err
}
func (r *recorder) SetBadge(n int) { r.mu.Lock(); r.badge = n; r.mu.Unlock() }
func (r *recorder) SetStatusTitle(s string) {
	r.mu.Lock()
	r.title = s
	r.mu.Unlock()
}
func (r *recorder) StoreToken(t []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.token = append([]byte(nil), t...)
	return r.err
}
func (r *recorder) LoadToken() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.token, r.err
}
func (r *recorder) SetLogin(on bool) (login.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logins = append(r.logins, on)
	if on {
		return login.Enabled, r.err
	}
	return login.NotRegistered, r.err
}

// open is the app in an offscreen window, over a temporary directory.
func open(t *testing.T, dir string, rec *recorder) (*pings, *widgettest.Rig) {
	t.Helper()
	p, err := newPings(appID, dir)
	if err != nil {
		t.Fatal(err)
	}
	p.sys = rec
	t.Cleanup(func() { p.prefs.Close(); p.state.Close() })
	cfg := p.config()
	r := widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, p)
	r.Frames(4)
	return p, r
}

// waitLine plays frames until the window's last line is want.
func waitLine(t *testing.T, p *pings, r *widgettest.Rig, want string) {
	t.Helper()
	for i := 0; i < 200 && p.line != want; i++ {
		time.Sleep(time.Millisecond)
		r.Frames(1)
	}
	if p.line != want {
		t.Fatalf("the line is %q, want %q", p.line, want)
	}
}

func TestButtonsReachTheServices(t *testing.T) {
	rec := &recorder{}
	p, r := open(t, t.TempDir(), rec)

	r.Click(ui.RoleButton, "Notify")
	r.Frames(2)
	if len(rec.posted) != 1 || rec.posted[0].Category != catMessage || rec.posted[0].Title != "Ana" || p.line != "notification posted" {
		t.Errorf("posted %+v, line %q", rec.posted, p.line)
	}

	r.Click(ui.RoleButton, "Add unread")
	r.Click(ui.RoleButton, "Add unread")
	r.Frames(2)
	if rec.badge != 2 || rec.title != "2" {
		t.Errorf("badge %d, status title %q, want 2", rec.badge, rec.title)
	}
	r.Click(ui.RoleButton, "Clear")
	r.Frames(2)
	if rec.badge != 0 || rec.title != "" {
		t.Errorf("after Clear: badge %d, status title %q", rec.badge, rec.title)
	}
}

func TestKeychainAndLoginRunOffTheFrame(t *testing.T) {
	rec := &recorder{}
	p, r := open(t, t.TempDir(), rec)

	r.Click(ui.RoleButton, "Save token")
	waitLine(t, p, r, "token saved")
	r.Click(ui.RoleButton, "Read token")
	waitLine(t, p, r, "token: token-for-Ana")

	r.Click(ui.RoleSwitch, "Start at login")
	waitLine(t, p, r, "login item: enabled")
	if len(rec.logins) != 1 || !rec.logins[0] || !p.atLogin {
		t.Errorf("login calls %v, switch %v", rec.logins, p.atLogin)
	}
}

// A service this OS or this process lacks answers with an error, which the
// window shows in words; nothing else happens.
func TestUnavailableServicesAreReported(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{notify.ErrUnsupported, "not available on this OS yet"},
		{notify.ErrNotSetUp, "needs a signed bundle (gnui run)"},
		{login.ErrUnavailable, "needs a signed bundle (gnui run)"},
		{login.ErrNotFound, "this bundle has no \"hidden\" launch agent (see gnui.json)"},
	} {
		rec := &recorder{err: c.err}
		p, r := open(t, t.TempDir(), rec)
		r.Click(ui.RoleButton, "Notify")
		r.Frames(2)
		if want := "notify: " + c.want; p.line != want {
			t.Errorf("%v: the line is %q, want %q", c.err, p.line, want)
		}
		r.Click(ui.RoleButton, "Read token")
		waitLine(t, p, r, c.want)
	}
}

// What a text field holds is a preference: it is on disk after Flush, and the
// next start finds it; the window state saved the same way is applied to the
// screens of the next run.
func TestPreferencesAndWindowStateSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	p, r := open(t, dir, &recorder{})
	r.Click(ui.RoleTextField, "Name")
	r.Key('A', ui.PrimaryMod())
	r.Type("Bruno")
	r.Frames(2)
	p.Event(p.a, &app.Event{Kind: app.EvQuitRequested}) // flushes
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
		t.Fatal(err)
	}
	st := winstate.State{Frame: winstate.Rect{X: 100, Y: 80, W: 600, H: 420}}
	if err := p.state.Save(st); err != nil {
		t.Fatal(err)
	}
	p.state.Flush()

	q, err := newPings(appID, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.prefs.Close(); q.state.Close() })
	if got := q.prefs.Get().Name; got != "Bruno" || q.name.String() != "Bruno" {
		t.Errorf("the preference is %q, the field %q", got, q.name.String())
	}
	screen := winstate.Screen{ID: "main", Frame: winstate.Rect{W: 1440, H: 900}, Visible: winstate.Rect{Y: 25, W: 1440, H: 875}}
	pl := winstate.Apply(winstate.Config{Width: 480, Height: 380}, q.state.State(), []winstate.Screen{screen})
	if !pl.Restored || pl.Frame != st.Frame {
		t.Errorf("the window would open at %+v (restored %v), want %+v", pl.Frame, pl.Restored, st.Frame)
	}
	// A file the next version of the app cannot read starts the app on the defaults.
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{not json"), 0o600)
	if _, err := prefs.Open(filepath.Join(dir, "settings.json"), Settings{}, prefs.Options{}); !errors.Is(err, prefs.ErrCorrupt) {
		t.Errorf("a corrupt file gave %v", err)
	}
}

// Marking a notification as read from its action clears the badge.
func TestNotificationActionClearsTheBadge(t *testing.T) {
	rec := &recorder{}
	p, r := open(t, t.TempDir(), rec)
	r.Click(ui.RoleButton, "Add unread")
	r.Frames(2)
	p.Event(p.a, &app.Event{Kind: app.EvService, Source: platform.SourceNotifyResponse, A: actRead})
	if rec.badge != 0 || p.unread != 0 || p.line != "marked as read" {
		t.Errorf("badge %d, unread %d, line %q", rec.badge, p.unread, p.line)
	}
}

func TestLayoutAndNames(t *testing.T) {
	_, r := open(t, t.TempDir(), &recorder{})
	r.AuditLayout("services")
	widgettest.LintNames(t, "services", r.Tree())
}

// A steady-state frame allocates nothing.
func TestSteadyFramesAllocateNothing(t *testing.T) {
	p, err := newPings(appID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.sys = &recorder{}
	cfg := p.config()
	o := widgettest.NewOffscreen(t, cfg, int(cfg.Width), int(cfg.Height), 1, p)
	for range 300 {
		o.Frame()
	}
	runtime.GC()
	for range 300 {
		o.Frame()
	}
	var best uint64
	for w := 0; w < 12; w++ {
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for range 200 {
			o.Frame()
		}
		runtime.ReadMemStats(&m1)
		if m1.Mallocs == m0.Mallocs && m1.TotalAlloc == m0.TotalAlloc {
			return
		}
		if w == 0 || m1.Mallocs-m0.Mallocs < best {
			best = m1.Mallocs - m0.Mallocs
		}
	}
	t.Errorf("every one of 12 runs of 200 frames allocated (fewest: %d mallocs)", best)
}
