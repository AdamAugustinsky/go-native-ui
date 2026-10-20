//go:build darwin

package live

import (
	"os"
	"sync"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// idleTimes builds the app's frames and notes, for each, when it was built
// and whether the system's appearance or values changed since the frame
// before (platform.AppearanceSerial counts those changes: the appearance, the
// accent and system colours, the accessibility display settings, the scroller
// style).
type idleTimes struct {
	app.Builder
	mu     sync.Mutex
	start  time.Time
	serial uint32
	frames []idleFrame
}

type idleFrame struct {
	at      time.Duration
	changed bool // a system change reached the app since the frame before
}

func (b *idleTimes) Init(a *app.App) error {
	if in, ok := b.Builder.(app.Initer); ok {
		return in.Init(a)
	}
	return nil
}

func (b *idleTimes) Build(c *ui.Context) {
	b.Builder.Build(c)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.start.IsZero() {
		b.start = time.Now()
	}
	s := platform.AppearanceSerial()
	b.frames = append(b.frames, idleFrame{time.Since(b.start), s != b.serial})
	b.serial = s
}

// unexplained are the frames after the first second that no system change
// explains: not the one that shows a change, nor the animation that follows
// it (the fade to the new values, at most about 1 s).
func (b *idleTimes) unexplained() []time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []time.Duration
	var lastChange time.Duration = -time.Hour
	for _, f := range b.frames {
		if f.changed {
			lastChange = f.at
		}
		if f.at > time.Second && f.at-lastChange > 1500*time.Millisecond {
			out = append(out, f.at)
		}
	}
	return out
}

// An app left alone draws no frame between 1 s and 15 s after its launch,
// except for the system's own news: on a Mac whose mouse is found by AppKit
// some 5 s after launch, the preferred scroller style turns from overlay to
// legacy and AppKit posts NSPreferredScrollerStyleDidChangeNotification, a real
// change of what the app shows (scroll bars that take space), which reaches the
// app as an appearance wake and a fade. Every other frame fails the test, and
// the overlay's log (GNUI_DEBUG=overlay,log, set here unless the caller set
// GNUI_DEBUG) says what each was for.
func TestIdleAppDrawsNothingAfterOneSecondButTheSystemsNews(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 16 s")
	}
	if os.Getenv("GNUI_DEBUG") == "" {
		t.Setenv("GNUI_DEBUG", "overlay,log")
	}
	b := &idleTimes{Builder: &probe{}}
	l := widgettest.StartLive(t, probeConfig(), b)
	time.Sleep(16 * time.Second)
	_ = l
	if u := b.unexplained(); len(u) > 0 {
		t.Errorf("frames after the first second that no system change explains: %v", u)
	}
	b.mu.Lock()
	for _, f := range b.frames {
		if f.changed {
			t.Logf("a system change reached the app at %v", f.at.Round(time.Millisecond))
		}
	}
	b.mu.Unlock()
}
