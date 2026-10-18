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
// No session bus, no portal, or a portal without color-scheme: Known is false
// and the app shows light. Nothing is retried in a loop.

const (
	portalName    = "org.freedesktop.portal.Desktop"
	portalPath    = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	settingsIface = "org.freedesktop.portal.Settings"
	appearanceNS  = "org.freedesktop.appearance"

	// firstWait bounds how long the first SystemAppearance call waits for the
	// portal. The goroutine starts at package init, long before the first
	// frame, and a running portal answers in well under a millisecond once
	// connected (see TestPortalAnswerTime); a portal that has to be started
	// by D-Bus activation takes far longer than any wait that fits the
	// startup budget, and then the answer crossfades in.
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
	conn       *dbus.Conn
}

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
	if _, err := conn.Subscribe(ctx, dbus.Match{Sender: portalName, Path: portalPath, Interface: settingsIface, Member: "SettingChanged"}, f.onChanged); err != nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); f.readKey(ctx, conn, "color-scheme") }()
	go func() { defer wg.Done(); f.readKey(ctx, conn, "accent-color") }()
	wg.Wait()
}

// readKey asks the portal for one appearance setting and applies it.
func (f *portalFollower) readKey(ctx context.Context, conn *dbus.Conn, key string) {
	r, err := conn.CallContext(ctx, portalName, portalPath, settingsIface, "ReadOne", "ss", appearanceNS, key)
	var e *dbus.Error
	if errors.As(err, &e) && e.Name == "org.freedesktop.DBus.Error.UnknownMethod" {
		// Version 1 portals only have Read, which wraps the value in a second variant.
		r, err = conn.CallContext(ctx, portalName, portalPath, settingsIface, "Read", "ss", appearanceNS, key)
	}
	if err != nil || len(r.Body) != 1 {
		return // no portal, or no such key: unset
	}
	f.apply(key, r.Body[0])
}

// onChanged handles SettingChanged(namespace, key, value) on the client's
// reader goroutine.
func (f *portalFollower) onChanged(m *dbus.Message) {
	if len(m.Body) != 3 {
		return
	}
	ns, _ := m.Body[0].(string)
	key, _ := m.Body[1].(string)
	if ns != appearanceNS {
		return
	}
	f.apply(key, m.Body[2])
}

// apply stores a setting's value (as received: possibly in nested variants)
// and publishes the result.
func (f *portalFollower) apply(key string, v any) {
	for {
		vv, ok := v.(dbus.Variant)
		if !ok {
			break
		}
		v = vv.Val
	}
	f.mu.Lock()
	switch key {
	case "color-scheme":
		if n, ok := v.(uint32); ok {
			f.haveScheme, f.dark = true, n == 1 // 1 prefers dark; 2 prefers light; 0 no preference
		}
	case "accent-color":
		f.accent = 0
		if s, ok := v.(dbus.Struct); ok && len(s) == 3 {
			r, g, b := unit(s[0]), unit(s[1]), unit(s[2])
			if r >= 0 && g >= 0 && b >= 0 { // out of range means unset
				f.accent = uint32(r)<<24 | uint32(g)<<16 | uint32(b)<<8 | 0xFF
			}
		}
	default:
		f.mu.Unlock()
		return
	}
	var c uint64
	if f.haveScheme {
		c = pKnown | uint64(f.accent)<<8
		if f.dark {
			c |= pDark
		}
	}
	f.publish(c) // under the lock: two answers cannot publish out of order
	f.mu.Unlock()
}

// unit converts a colour component in 0..1 to 0..255, or -1 when it is out of
// range.
func unit(v any) int {
	x, ok := v.(float64)
	if !ok || !(x >= 0 && x <= 1) { // also NaN
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
