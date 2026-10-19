package platform

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"gonativeui/internal/dbus"
)

// System appearance on Linux (see appearance.go, D32): the desktop portal's
// org.freedesktop.appearance settings, over the in-house D-Bus client (D31).
//
// A goroutine started at package init connects to the session bus, subscribes
// to the portal's SettingChanged signal, then reads color-scheme and
// accent-color (both calls in flight at once). Nothing of this runs on the
// main thread. SystemAppearance is a pair of atomic loads; only its first call
// may wait, for at most firstWait, for the first answer. After that the answer
// is simply there, or arrives later: it bumps the serial and calls Wake, and
// the app reads again.
//
// So the first frame is in the system's mode when the portal answers within
// firstWait, which is the normal case (a running portal answers in about a
// millisecond). A portal that has to be started on demand can take longer: the
// first frame is then light (Known is false) and crossfades to the system's
// mode when the answer arrives. No session bus, no portal, or a portal without
// color-scheme: Known stays false and the app shows light. Nothing is retried
// in a loop.
//
// A change signal is read in place from the D-Bus client's receive buffer
// (dbus.Conn.SubscribeSignal) and decoded into fixed fields: once warm, it
// allocates nothing. The startup calls and their replies do allocate; they
// happen once.

const (
	portalName    = "org.freedesktop.portal.Desktop"
	portalPath    = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	settingsIface = "org.freedesktop.portal.Settings"
	appearanceNS  = "org.freedesktop.appearance"

	// firstWait bounds how long the first SystemAppearance call waits for the
	// portal. The goroutine starts at package init, long before the first
	// frame, and a running portal answers in about a millisecond once
	// connected (0.2–1.2 ms in CI, see TestPortalAnswerTime). A portal that
	// has to be started by D-Bus activation can take far longer than any wait
	// that fits the startup budget: the first frame is then light, and the
	// late answer crossfades in (TestPortalSlowAnswer).
	firstWait = 25 * time.Millisecond

	// portalTimeout bounds each call of the follower goroutine.
	portalTimeout = 30 * time.Second
)

type portalFollower struct {
	dial func(ctx context.Context) (*dbus.Conn, error)

	ready  chan struct{} // closed when the first answer, or the first failure, is in
	done   atomic.Bool   // ready is closed
	served atomic.Bool   // SystemAppearance has been called
	cache  atomic.Uint64 // accent<<8 | pValid | pKnown | pDark
	serial atomic.Uint32

	started  time.Time
	answered atomic.Int64 // nanoseconds from start to ready, for tests

	mu         sync.Mutex // the fields below (the portal's answers arrive on several goroutines)
	haveScheme bool
	dark       bool
	accent     uint32
	// gen counts, per setting, the change signals applied. An initial read
	// whose reply was overtaken by a signal is stale and is dropped.
	gen  [numKeys]uint32
	conn *dbus.Conn

	// beforeApply, when set (tests), runs between the arrival of an initial
	// read's reply and its application.
	beforeApply func(key int)
}

// The settings followed, as indexes into portalFollower.gen.
const (
	keyScheme = iota
	keyAccent
	numKeys
)

var keyNames = [numKeys]string{keyScheme: "color-scheme", keyAccent: "accent-color"}

const (
	pDark  = 1 << 0
	pKnown = 1 << 1
)

func newPortalFollower(dial func(ctx context.Context) (*dbus.Conn, error)) *portalFollower {
	return &portalFollower{dial: dial, ready: make(chan struct{})}
}

func dialSession(ctx context.Context) (*dbus.Conn, error) { return dbus.DialSession(ctx) }

var portal = newPortalFollower(dialSession)

func init() { portal.start() }

// SystemAppearance returns the current system appearance (see appearance.go).
func SystemAppearance() Appearance { return portal.get() }

// AppearanceSerial counts system appearance changes (see appearance.go).
func AppearanceSerial() uint32 { return portal.serial.Load() }

// SetDarkChrome does nothing on Linux: client-side decorations are drawn by
// the theme (see appearance.go).
func (w *Window) SetDarkChrome(dark bool) {}

func (f *portalFollower) start() {
	f.started = time.Now()
	go f.run()
}

// get returns the cached appearance. The first call may wait up to firstWait.
func (f *portalFollower) get() Appearance {
	if !f.served.Load() {
		if !f.done.Load() {
			t := time.NewTimer(firstWait)
			select {
			case <-f.ready:
			case <-t.C:
			}
			t.Stop()
		}
		// From here on a change that lands is reported with the serial. Stored
		// before the cache is read: publish stores the cache before it looks.
		f.served.Store(true)
	}
	v := f.cache.Load()
	return Appearance{Dark: v&pDark != 0, Known: v&pKnown != 0, Accent: uint32(v >> 8)}
}

// finish marks the first answer (or failure) as in.
func (f *portalFollower) finish() {
	if f.done.CompareAndSwap(false, true) {
		f.answered.Store(int64(time.Since(f.started)))
		close(f.ready)
	}
}

// run connects, subscribes and reads. It returns when the first answers are
// in; the connection then stays open, and the reader goroutine of the D-Bus
// client delivers signals.
func (f *portalFollower) run() {
	defer f.finish()
	ctx, cancel := context.WithTimeout(context.Background(), portalTimeout)
	defer cancel()
	conn, err := f.dial(ctx)
	if err != nil {
		return // no session bus
	}
	f.conn = conn
	// Subscribe before reading, so a change between the two is not lost.
	if _, err := conn.SubscribeSignal(ctx, dbus.Match{Sender: portalName, Path: portalPath, Interface: settingsIface, Member: "SettingChanged"}, f.onChanged); err != nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(numKeys)
	for k := range keyNames {
		go func() { defer wg.Done(); f.readKey(ctx, conn, k) }()
	}
	wg.Wait()
}

// readKey asks the portal for one appearance setting and applies the answer,
// unless a change signal for it has come in since the question was asked: the
// signal is then newer than the reply.
func (f *portalFollower) readKey(ctx context.Context, conn *dbus.Conn, k int) {
	f.mu.Lock()
	gen := f.gen[k]
	f.mu.Unlock()
	r, err := conn.CallContext(ctx, portalName, portalPath, settingsIface, "ReadOne", "ss", appearanceNS, keyNames[k])
	var e *dbus.Error
	if errors.As(err, &e) && e.Name == "org.freedesktop.DBus.Error.UnknownMethod" {
		// Version 1 portals only have Read, which wraps the value in a second variant.
		r, err = conn.CallContext(ctx, portalName, portalPath, settingsIface, "Read", "ss", appearanceNS, keyNames[k])
	}
	if err != nil || len(r.Body) != 1 {
		return // no portal, or no such key: unset
	}
	if f.beforeApply != nil {
		f.beforeApply(k)
	}
	v := r.Body[0]
	for { // the value, out of its variants
		vv, ok := v.(dbus.Variant)
		if !ok {
			break
		}
		v = vv.Val
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gen[k] != gen {
		return
	}
	switch k {
	case keyScheme:
		if n, ok := v.(uint32); ok {
			f.setScheme(n)
		}
	case keyAccent:
		if s, ok := v.(dbus.Struct); ok && len(s) == 3 {
			f.setAccent(unit(s[0]), unit(s[1]), unit(s[2]))
		} else {
			f.setAccent(-1, -1, -1)
		}
	}
}

// onChanged handles SettingChanged(namespace, key, value). It runs on the
// D-Bus client's reader goroutine, on the signal in place, and allocates
// nothing: the namespace and key are compared as bytes, the value is read
// into numbers.
func (f *portalFollower) onChanged(s *dbus.Signal) {
	if string(s.Signature) != "ssv" {
		return
	}
	r := s.Body()
	ns, key, sig := r.Str(), r.Str(), r.Variant()
	if !r.Ok() || string(ns) != appearanceNS {
		return
	}
	switch {
	case string(key) == "color-scheme" && string(sig) == "u":
		n := r.Uint32()
		if r.Ok() {
			f.mu.Lock()
			f.gen[keyScheme]++
			f.setScheme(n)
			f.mu.Unlock()
		}
	case string(key) == "accent-color" && string(sig) == "(ddd)":
		r.Struct()
		cr, cg, cb := unitF(r.Float64()), unitF(r.Float64()), unitF(r.Float64())
		if r.Ok() {
			f.mu.Lock()
			f.gen[keyAccent]++
			f.setAccent(cr, cg, cb)
			f.mu.Unlock()
		}
	}
}

// setScheme stores color-scheme (1 prefers dark, 2 prefers light, 0 has no
// preference) and publishes. The caller holds f.mu.
func (f *portalFollower) setScheme(n uint32) {
	f.haveScheme, f.dark = true, n == 1
	f.publishLocked()
}

// setAccent stores the accent colour (components 0..255, or -1 for out of
// range, which means unset) and publishes. The caller holds f.mu.
func (f *portalFollower) setAccent(r, g, b int) {
	f.accent = 0
	if r >= 0 && g >= 0 && b >= 0 {
		f.accent = uint32(r)<<24 | uint32(g)<<16 | uint32(b)<<8 | 0xFF
	}
	f.publishLocked()
}

// publishLocked makes the stored settings the cached appearance. Under the
// lock, two answers cannot publish out of order.
func (f *portalFollower) publishLocked() {
	var c uint64
	if f.haveScheme {
		c = pKnown | uint64(f.accent)<<8
		if f.dark {
			c |= pDark
		}
	}
	f.publish(c)
}

// unit converts a decoded colour component to 0..255, or -1 when it is not a
// float64 or out of range.
func unit(v any) int {
	x, ok := v.(float64)
	if !ok {
		return -1
	}
	return unitF(x)
}

// unitF converts a colour component in 0..1 to 0..255, or -1 when it is out of
// range.
func unitF(x float64) int {
	if !(x >= 0 && x <= 1) { // also NaN
		return -1
	}
	return int(math.Round(x * 255))
}

// publish makes c the cached appearance. Once the app has read the cache, a
// different value is a change it must hear about.
func (f *portalFollower) publish(c uint64) {
	if old := f.cache.Swap(c); old != c && f.served.Load() {
		f.serial.Add(1)
		Wake()
	}
}

// ReadSystemValues reports nothing on Linux (see appearance.go): the theme's
// own values apply (D34).
func ReadSystemValues(dst *SystemValues) { *dst = SystemValues{} }
