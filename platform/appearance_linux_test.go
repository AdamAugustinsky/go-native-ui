package platform

import (
	"context"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"gonativeui/internal/dbus"
	"gonativeui/internal/dbus/dbustest"
)

// A fake xdg-desktop-portal on a private bus: it owns org.freedesktop.portal.Desktop
// and answers Settings.ReadOne (or only the older Read) for the appearance keys.
type fakePortal struct {
	conn *dbus.Conn

	mu       sync.Mutex
	vals     map[string]dbus.Variant
	delay    time.Duration
	version1 bool // only Read, which wraps the value in a second variant
}

func colorScheme(n uint32) dbus.Variant { return dbus.Variant{Sig: "u", Val: n} }
func accentColor(r, g, b float64) dbus.Variant {
	return dbus.Variant{Sig: "(ddd)", Val: dbus.Struct{r, g, b}}
}

func startFakePortal(t *testing.T, b *dbustest.Bus, vals map[string]dbus.Variant, opts ...func(*fakePortal)) *fakePortal {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dbus.Dial(ctx, b.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	p := &fakePortal{conn: c, vals: vals}
	for _, o := range opts {
		o(p)
	}
	c.Export(portalPath, settingsIface, func(m *dbus.Message) (string, []any, error) {
		p.mu.Lock()
		delay, v1 := p.delay, p.version1
		p.mu.Unlock()
		time.Sleep(delay)
		if (m.Member == "ReadOne" && v1) || (m.Member != "ReadOne" && m.Member != "Read") {
			return "", nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownMethod"}
		}
		ns, _ := m.Body[0].(string)
		key, _ := m.Body[1].(string)
		p.mu.Lock()
		v, ok := p.vals[key]
		p.mu.Unlock()
		if ns != appearanceNS || !ok {
			return "", nil, &dbus.Error{Name: "org.freedesktop.portal.Error.NotFound", Body: []any{"Requested setting not found"}}
		}
		if m.Member == "Read" {
			return "v", []any{dbus.Variant{Sig: "v", Val: v}}, nil
		}
		return "v", []any{v}, nil
	})
	// The previous test's portal may still be letting go of the name.
	for {
		r, err := c.RequestName(ctx, portalName, dbus.NameDoNotQueue)
		if err != nil {
			t.Fatalf("RequestName: %v", err)
		}
		if r == dbus.NameReplyPrimary {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("RequestName: the name stays taken (reply %d)", r)
		case <-time.After(5 * time.Millisecond):
		}
	}
	return p
}

// set changes a setting and announces it.
func (p *fakePortal) set(ns, key string, v dbus.Variant) {
	p.mu.Lock()
	if ns == appearanceNS {
		p.vals[key] = v
	}
	p.mu.Unlock()
	p.conn.Emit(portalPath, settingsIface, "SettingChanged", "ssv", ns, key, v)
}

func follower(b *dbustest.Bus) *portalFollower {
	return newPortalFollower(func(ctx context.Context) (*dbus.Conn, error) { return dbus.Dial(ctx, b.Address) })
}

func startFollower(b *dbustest.Bus) *portalFollower {
	f := follower(b)
	f.start()
	return f
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPortalReadsColorSchemeAndAccent(t *testing.T) {
	dbustest.Each(t, func(t *testing.T, b *dbustest.Bus) {
		for _, c := range []struct {
			name     string
			scheme   uint32
			version1 bool
			want     Appearance
		}{
			{"prefer dark", 1, false, Appearance{Dark: true, Known: true, Accent: 0x336699FF}},
			{"prefer light", 2, false, Appearance{Known: true, Accent: 0x336699FF}},
			{"no preference is light", 0, false, Appearance{Known: true, Accent: 0x336699FF}},
			{"version 1 portal, dark", 1, true, Appearance{Dark: true, Known: true, Accent: 0x336699FF}},
		} {
			t.Run(c.name, func(t *testing.T) {
				startFakePortal(t, b, map[string]dbus.Variant{"color-scheme": colorScheme(c.scheme), "accent-color": accentColor(0.2, 0.4, 0.6)},
					func(p *fakePortal) { p.version1 = c.version1 })
				f := startFollower(b)
				t0 := time.Now()
				got := f.get()
				if got != c.want {
					t.Errorf("got %+v, want %+v", got, c.want)
				}
				t.Logf("first call took %v; connect + subscribe + read took %v", time.Since(t0), time.Duration(f.answered.Load()))
				if f.serial.Load() != 0 {
					t.Error("an answer that arrived before the first call bumped the serial")
				}
			})
		}
	})
}

func TestPortalMissingAndInvalidKeys(t *testing.T) {
	for _, c := range []struct {
		name string
		vals map[string]dbus.Variant
		want Appearance
	}{
		{"no color-scheme: unknown", map[string]dbus.Variant{"accent-color": accentColor(1, 0, 0)}, Appearance{}},
		{"no accent-color", map[string]dbus.Variant{"color-scheme": colorScheme(1)}, Appearance{Dark: true, Known: true}},
		{"accent below range", map[string]dbus.Variant{"color-scheme": colorScheme(2), "accent-color": accentColor(-1, -1, -1)}, Appearance{Known: true}},
		{"accent above range", map[string]dbus.Variant{"color-scheme": colorScheme(2), "accent-color": accentColor(0.5, 1.5, 0.5)}, Appearance{Known: true}},
		{"accent edges", map[string]dbus.Variant{"color-scheme": colorScheme(2), "accent-color": accentColor(0, 1, 0)}, Appearance{Known: true, Accent: 0x00FF00FF}},
		{"color-scheme of the wrong type", map[string]dbus.Variant{"color-scheme": {Sig: "s", Val: "dark"}}, Appearance{}},
		{"accent of the wrong type", map[string]dbus.Variant{"color-scheme": colorScheme(1), "accent-color": {Sig: "s", Val: "red"}}, Appearance{Dark: true, Known: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := dbustest.Broker(t)
			startFakePortal(t, b, c.vals)
			f := startFollower(b)
			if got := f.get(); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestPortalSettingChanged(t *testing.T) {
	dbustest.Each(t, func(t *testing.T, b *dbustest.Bus) {
		p := startFakePortal(t, b, map[string]dbus.Variant{"color-scheme": colorScheme(2), "accent-color": accentColor(0.2, 0.4, 0.6)})
		f := startFollower(b)

		// Before the app has read anything a change needs no announcement: the
		// first read sees it.
		<-f.ready
		takeWake()
		p.set(appearanceNS, "color-scheme", colorScheme(1))
		waitUntil(t, "the change to reach the cache", func() bool { return f.cache.Load()&pDark != 0 })
		if f.serial.Load() != 0 || takeWake() {
			t.Error("a change before the first read bumped the serial or woke the loop")
		}
		if got := f.get(); !got.Dark || !got.Known {
			t.Fatalf("first read after the change: %+v", got)
		}

		// After it, a change bumps the serial and wakes once.
		serial := f.serial.Load()
		takeWake()
		p.set(appearanceNS, "color-scheme", colorScheme(2))
		waitUntil(t, "the serial", func() bool { return f.serial.Load() != serial })
		waitUntil(t, "the wake", func() bool { return wakePending.Load() })
		takeWake()
		if f.serial.Load() != serial+1 {
			t.Errorf("serial %d -> %d", serial, f.serial.Load())
		}
		if got := f.get(); got.Dark {
			t.Errorf("after prefer-light: %+v", got)
		}

		// Another namespace, the same value again, and a malformed signal change nothing.
		serial = f.serial.Load()
		p.set("org.gnome.desktop.interface", "color-scheme", colorScheme(1))
		p.set(appearanceNS, "color-scheme", colorScheme(2))
		p.conn.Emit(portalPath, settingsIface, "SettingChanged", "ssv", appearanceNS, "color-scheme", dbus.Variant{Sig: "s", Val: "dark"})
		p.conn.Emit(portalPath, settingsIface, "SettingChanged", "s", appearanceNS)
		p.set(appearanceNS, "contrast", dbus.Variant{Sig: "u", Val: uint32(1)})
		// The accent change is the marker: signals arrive in order.
		p.set(appearanceNS, "accent-color", accentColor(1, 0, 0))
		waitUntil(t, "the accent", func() bool { return f.get().Accent == 0xFF0000FF })
		if f.serial.Load() != serial+1 {
			t.Errorf("serial moved by %d, want 1", f.serial.Load()-serial)
		}
		if got := f.get(); got.Dark || !got.Known {
			t.Errorf("unrelated signals changed the mode: %+v", got)
		}
	})
}

// A portal that answers after the first call's bound (one that D-Bus has to
// start on demand) must not hold up the first frame: the first call returns
// Known false after firstWait, later calls do not wait, and the late answer
// reports dark with a serial bump and a wake. That is the documented limit of
// "the first frame is in the system's mode" on Linux (D32).
func TestPortalSlowAnswer(t *testing.T) {
	b := dbustest.Broker(t)
	startFakePortal(t, b, map[string]dbus.Variant{"color-scheme": colorScheme(1)}, func(p *fakePortal) { p.delay = 100 * time.Millisecond })
	f := startFollower(b)
	takeWake()
	t0 := time.Now()
	got := f.get()
	first := time.Since(t0)
	if got.Known || got.Dark {
		t.Errorf("first call: %+v", got)
	}
	if f.done.Load() {
		t.Error("the portal answered before its delay: the test does not test the slow case")
	}
	if first < firstWait-5*time.Millisecond || first > firstWait+100*time.Millisecond {
		t.Errorf("first call took %v, want about %v", first, firstWait)
	}
	t0 = time.Now()
	f.get()
	if d := time.Since(t0); d > 5*time.Millisecond {
		t.Errorf("second call took %v", d)
	}
	waitUntil(t, "the late answer", func() bool { return f.serial.Load() == 1 })
	waitUntil(t, "the wake", func() bool { return wakePending.Load() })
	takeWake()
	if got := f.get(); !got.Known || !got.Dark {
		t.Errorf("after the answer: %+v", got)
	}
}

func TestPortalAbsent(t *testing.T) {
	b := dbustest.Broker(t)
	f := startFollower(b) // a bus, but nobody owns the portal's name
	t0 := time.Now()
	got := f.get()
	if got != (Appearance{}) {
		t.Errorf("got %+v", got)
	}
	if d := time.Since(t0); d > firstWait+100*time.Millisecond {
		t.Errorf("took %v", d)
	}
	if !f.done.Load() {
		t.Error("the follower is still waiting")
	}
	// It gives up: a portal that shows up later is not picked up by retrying.
	startFakePortal(t, b, map[string]dbus.Variant{"color-scheme": colorScheme(1)})
	time.Sleep(100 * time.Millisecond)
	if got := f.get(); got.Known || f.serial.Load() != 0 {
		t.Errorf("late portal: %+v serial %d", got, f.serial.Load())
	}
}

func TestPortalNoSessionBus(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	f := newPortalFollower(dialSession)
	f.start()
	t0 := time.Now()
	got := f.get()
	if got != (Appearance{}) {
		t.Errorf("got %+v", got)
	}
	if d := time.Since(t0); d > firstWait/2 {
		t.Errorf("no bus took %v to report", d)
	}
}

func TestPortalBusDies(t *testing.T) {
	b := dbustest.Broker(t)
	startFakePortal(t, b, map[string]dbus.Variant{"color-scheme": colorScheme(1)})
	f := startFollower(b)
	if got := f.get(); !got.Dark {
		t.Fatalf("got %+v", got)
	}
	b.Stop()
	<-f.conn.Done()
	if got := f.get(); !got.Dark || !got.Known {
		t.Errorf("after the bus ended: %+v", got)
	}
}

// The main-thread path: nothing allocates once the answer is in.
func TestPortalGetAllocs(t *testing.T) {
	b := dbustest.Broker(t)
	startFakePortal(t, b, map[string]dbus.Variant{"color-scheme": colorScheme(1), "accent-color": accentColor(0.2, 0.4, 0.6)})
	f := startFollower(b)
	f.get()
	if n := testing.AllocsPerRun(1000, func() { f.get(); f.serial.Load() }); n != 0 {
		t.Errorf("get allocates %v per call", n)
	}
	// And the package-level pair, whatever the test machine's bus is.
	SystemAppearance()
	if n := testing.AllocsPerRun(1000, func() { SystemAppearance(); AppearanceSerial() }); n != 0 {
		t.Errorf("SystemAppearance allocates %v per call", n)
	}
}

// How long a running portal takes to answer, from the start of the goroutine
// (connect, authenticate, Hello, AddMatch, two reads). This is what firstWait
// is measured against; the numbers are logged for the record.
func TestPortalAnswerTime(t *testing.T) {
	dbustest.Each(t, func(t *testing.T, b *dbustest.Bus) {
		startFakePortal(t, b, map[string]dbus.Variant{"color-scheme": colorScheme(1), "accent-color": accentColor(0.2, 0.4, 0.6)})
		var ds []time.Duration
		for i := 0; i < 30; i++ {
			f := startFollower(b)
			<-f.ready
			if !f.get().Known {
				t.Fatal("not known")
			}
			ds = append(ds, time.Duration(f.answered.Load()))
			f.conn.Close()
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		t.Logf("%s: portal answer after goroutine start: min %v, median %v, p90 %v, max %v (firstWait %v)",
			b.Name, ds[0], ds[len(ds)/2], ds[len(ds)*9/10], ds[len(ds)-1], firstWait)
		if ds[len(ds)/2] > 100*time.Millisecond {
			t.Errorf("median %v: far above firstWait", ds[len(ds)/2])
		}
	})
}

func TestSetDarkChromeIsANoop(t *testing.T) {
	(&Window{}).SetDarkChrome(true)
}

// An initial read whose reply is overtaken by a change signal must not
// overwrite the newer value: the reply is in, a newer SettingChanged is
// applied, and only then does the read's goroutine get to apply the reply.
func TestPortalStaleReadDroppedAfterSignal(t *testing.T) {
	b := dbustest.Broker(t)
	p := startFakePortal(t, b, map[string]dbus.Variant{"color-scheme": colorScheme(2), "accent-color": accentColor(0.2, 0.4, 0.6)})
	f := follower(b)
	reached, release := make(chan int, 2), make(chan struct{})
	f.beforeApply = func(k int) {
		if k == keyScheme {
			reached <- k
			<-release
		}
	}
	f.start()
	select {
	case <-reached: // the reply (prefer light) is in and not yet applied
	case <-time.After(5 * time.Second):
		t.Fatal("the read never answered")
	}
	p.set(appearanceNS, "color-scheme", colorScheme(1)) // newer: prefer dark
	waitUntil(t, "the signal to be applied", func() bool { return f.cache.Load()&pDark != 0 })
	close(release)
	select {
	case <-f.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the follower never finished")
	}
	if got := f.get(); !got.Dark || !got.Known || got.Accent != 0x336699FF {
		t.Errorf("the stale reply won: %+v", got)
	}
}

// settingChanged is a marshalled SettingChanged signal from the portal.
func settingChanged(t *testing.T, serial uint32, key string, v dbus.Variant) []byte {
	t.Helper()
	m := &dbus.Message{Type: dbus.TypeSignal, Serial: serial, Sender: ":1.99", Path: portalPath, Interface: settingsIface, Member: "SettingChanged",
		Signature: "ssv", Body: []any{appearanceNS, key, v}}
	raw, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A change signal from the portal allocates nothing once warm: a real
// system-mode switch during a measured session must not show up in the
// allocation gate, which counts the whole process. The signals are written to
// the follower's socket by the broker's Inject, so no sender or router in this
// process allocates inside the window. Like the bench gate (D27), it passes on
// the first window with exactly 0 mallocs; an allocation in the signal path
// recurs in every window and fails.
func TestPortalSignalPathAllocs(t *testing.T) {
	b := dbustest.Broker(t)
	startFakePortal(t, b, map[string]dbus.Variant{"color-scheme": colorScheme(2), "accent-color": accentColor(0.9, 0.8, 0.7)})
	f := startFollower(b)
	if got := f.get(); !got.Known || got.Dark {
		t.Fatalf("start: %+v", got)
	}
	// Alternating scheme and accent changes, each of which changes the value,
	// ending where they began.
	const n = 100
	var msgs [n][]byte
	for i := range msgs {
		j := i / 2
		switch {
		case i%2 == 0 && j%2 == 0:
			msgs[i] = settingChanged(t, uint32(i+1), "color-scheme", colorScheme(1))
		case i%2 == 0:
			msgs[i] = settingChanged(t, uint32(i+1), "color-scheme", colorScheme(2))
		case j%2 == 0:
			msgs[i] = settingChanged(t, uint32(i+1), "accent-color", accentColor(0.1, 0.2, 0.3))
		default:
			msgs[i] = settingChanged(t, uint32(i+1), "accent-color", accentColor(0.9, 0.8, 0.7))
		}
	}
	to := f.conn.Name()
	window := func() uint64 {
		want := f.serial.Load() + n
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for _, raw := range msgs {
			if err := b.Inject(to, raw); err != nil {
				t.Fatal(err)
			}
		}
		for f.serial.Load() != want {
			time.Sleep(100 * time.Microsecond)
		}
		runtime.ReadMemStats(&m1)
		return m1.Mallocs - m0.Mallocs
	}
	window() // warm-up: the first signals grow the buffers and prime the runtime
	var counts []uint64
	for w := 0; w < 8; w++ {
		c := window()
		counts = append(counts, c)
		if c == 0 {
			t.Logf("window %d of %d signals: 0 mallocs (earlier windows: %v)", w+1, n, counts[:w])
			if got := f.get(); got.Dark || got.Accent != 0xE6CCB3FF {
				t.Errorf("end state %+v", got)
			}
			return
		}
	}
	t.Errorf("every window allocated: %v mallocs per %d signals", counts, n)
}
