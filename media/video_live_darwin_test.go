//go:build darwin

package media

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/gpu"
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

// videoWin is a window showing one video, 320×192 pixels inside a clip box
// whose height the test sets (0 scrolls the video out of view), the way an
// app's transcript shows one.
type videoWin struct {
	a      *app.App
	k      *widget.Kit
	v      atomic.Pointer[Video]
	clipH  atomic.Int32
	builds atomic.Int64
	drawn  atomic.Int64 // frames that drew a frame of the video

	unchecked bool // a frame rate went unchecked: the window was covered
}

func newVideoWin() *videoWin {
	b := &videoWin{}
	b.clipH.Store(192)
	return b
}

func (b *videoWin) Init(a *app.App) error {
	b.a, b.k = a, widget.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}

func (b *videoWin) Build(c *ui.Context) {
	b.builds.Add(1)
	k := b.k
	k.Begin(c)
	c.NextPos(8, 8)
	c.NextSize(ui.Px(320), ui.Px(float32(b.clipH.Load())))
	clip := c.BoxKeyed(ui.Clip|theme.Floating, ui.KeyN(c.ParentKey(), 1), nil)
	c.Push(clip)
	var tex gpu.TextureID
	var w, h int
	if v := b.v.Load(); v != nil && k.ImageShown(1) {
		var next int64
		tex, w, h, next = v.Frame(b.a.R, c.Input().Time)
		if next > 0 {
			c.WakeIn(next)
		}
		if tex != 0 {
			b.drawn.Add(1)
		}
	}
	c.NextPos(0, 0)
	k.NextSize(ui.Px(320), ui.Px(192))
	c.NextFlags(theme.Floating)
	k.ExternalImage(1, tex, w, h, ui.FitFill, 12, "Video")
	c.Pop()
	k.End()
}

// buildsIn counts the frames b builds in d.
func (b *videoWin) buildsIn(d time.Duration) int64 {
	n := b.builds.Load()
	time.Sleep(d)
	return b.builds.Load() - n
}

// resumes reports whether b builds 3 frames within d by itself.
func (b *videoWin) resumes(d time.Duration) bool {
	start := b.builds.Load()
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if b.builds.Load()-start >= 3 {
			return true
		}
	}
	return false
}

// covered is the state line of b's window if it is covered or hidden, or was
// at some time since since (platform.Now), "" if it showed all along. A
// covered window rightly draws nothing (another process's test window can
// cover it, locally and on CI), so a frame rate is checked only in one that
// shows, as widgettest's waits skip a test whose window is covered.
func (b *videoWin) covered(l *widgettest.LiveApp, since int64) string {
	var st platform.DebugState
	l.Do(func(*app.App) { st = b.a.Win.DebugState() })
	if st.Visible && st.LastVisibleT < since {
		return ""
	}
	return string(st.Append(nil))
}

// tooFew reports a frame rate, measured since since, below its floor: an
// error in a window that showed all along; in one covered meanwhile, a note,
// and the test is skipped at its end (its rates went unchecked).
func (b *videoWin) tooFew(t *testing.T, l *widgettest.LiveApp, since int64, msg string) {
	t.Helper()
	if st := b.covered(l, since); st != "" {
		t.Logf("%s, but the window is covered: %s", msg, st)
		b.unchecked = true
		return
	}
	t.Error(msg)
}

// TestVideoInWindow: in a real window, a paused video draws no frame, a
// playing one draws at its own rate (30 fps here, not the display's), one
// scrolled out of view draws none; and the pixel-buffer leases come back to
// zero after the video scrolls away, after live resizes, and when its window
// closes in the middle of playing. A covered window skips it (tooFew).
func TestVideoInWindow(t *testing.T) {
	plain, _ := testClips(t, widgettest.OnMain)
	b := newVideoWin()
	l := widgettest.StartLive(t, app.Config{Title: "video", Width: 360, Height: 240}, b)
	var err error
	l.Do(func(a *app.App) {
		var v *Video
		if v, err = OpenVideo(plain, VideoOptions{Muted: true, Loop: true}); err == nil {
			b.v.Store(v)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	v := b.v.Load()
	defer l.Do(func(*app.App) { v.Close() })
	l.Until("the poster", 600, func() bool { return b.drawn.Load() > 0 })

	leases := func(what string) {
		t.Helper()
		var pending, leased int
		l.Do(func(a *app.App) { pending, leased = a.R.ExternalCounts() })
		if pending != 0 || leased != 0 {
			t.Errorf("%s: %d imports pending, %d leased (want 0, 0)", what, pending, leased)
		}
	}

	// Paused: no frames at all.
	time.Sleep(time.Second)
	if n := b.buildsIn(time.Second); n != 0 {
		t.Logf("paused: %d frames in a second; once more", n)
		if n = b.buildsIn(time.Second); n != 0 { // once more: a system change can draw one
			t.Errorf("paused: %d frames in a second, want 0", n)
		}
	}

	// Playing: frames at the video's 30 fps.
	since := platform.Now()
	l.Do(func(*app.App) { v.Play() })
	time.Sleep(300 * time.Millisecond)
	// At most the video's rate, never the display's. The floor holds only on a
	// real display: CI's virtual one drops display-link ticks (5 a second
	// seen, with the display link restarted).
	n := b.buildsIn(time.Second)
	t.Logf("playing: %d frames in a second", n)
	if n > 45 {
		t.Errorf("playing: %d frames in a second, want about 30", n)
	} else if n < 12 && os.Getenv("CI") == "" {
		b.tooFew(t, l, since, fmt.Sprintf("playing: %d frames in a second, want about 30", n))
	}
	if !v.Playing() || v.Position() < 300*time.Millisecond {
		t.Errorf("playing: %v at %v", v.Playing(), v.Position())
	}

	// Scrolled out of view: no frames, and the leases drain without one.
	b.clipH.Store(0)
	l.Do(func(*app.App) {}) // the layout that hides it
	time.Sleep(300 * time.Millisecond)
	n = b.buildsIn(time.Second)
	t.Logf("scrolled away: %d frames in a second", n)
	if n > 2 {
		t.Errorf("scrolled away while playing: %d frames in a second", n)
	}
	leases("scrolled away")

	// Back in view, then live resizes while it plays. On CI, frames need only
	// come back by themselves within a few seconds (the ticks it drops).
	since = platform.Now()
	b.clipH.Store(192)
	l.Do(func(*app.App) {})
	time.Sleep(300 * time.Millisecond)
	if n := b.buildsIn(500 * time.Millisecond); n < 5 && (os.Getenv("CI") == "" || !b.resumes(5*time.Second)) {
		b.tooFew(t, l, since, fmt.Sprintf("back in view: %d frames in half a second", n))
	}
	var main *app.Win
	l.Do(func(a *app.App) { main = a.Windows(nil)[0] })
	for i := 0; i < 6; i++ {
		l.Do(func(*app.App) { main.SetFrame(80, 80, 360+float64(i*20), 240+float64(i*10)) })
		time.Sleep(80 * time.Millisecond)
	}
	l.Do(func(*app.App) { v.Pause() })
	time.Sleep(300 * time.Millisecond)
	leases("after resizes, paused")

	// A second window playing a video closes in the middle of it.
	b2 := newVideoWin()
	var w2 *app.Win
	l.Do(func(a *app.App) {
		if w2, err = a.OpenWindow(app.WindowConfig{Title: "video 2", Width: 360, Height: 240}, b2); err != nil {
			return
		}
		w2.SetFrame(480, 80, 360, 240)
		var v2 *Video
		if v2, err = OpenVideo(plain, VideoOptions{Muted: true, Loop: true}); err == nil {
			b2.v.Store(v2)
			v2.Play()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	v2 := b2.v.Load()
	for deadline := time.Now().Add(10 * time.Second); b2.drawn.Load() < 20; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			st := b2.covered(l, platform.Now()-int64(2*time.Second)) // 2 s shown are 60 frames
			l.Do(func(*app.App) { w2.Close(); v2.Close() })
			if st != "" {
				t.Skipf("the second window drew %d frames of its video, but it is covered: %s", b2.drawn.Load(), st)
			}
			t.Fatalf("the second window drew %d frames of its video", b2.drawn.Load())
		}
	}
	if videos, held := VideoCounts(); videos != 2 || held != 2 {
		t.Errorf("two videos playing: %d open, %d buffers held", videos, held)
	}
	l.Do(func(*app.App) { w2.Close() })
	time.Sleep(300 * time.Millisecond)
	leases("the second window closed mid-playback")
	l.Do(func(*app.App) { v2.Close() })
	if videos, held := VideoCounts(); videos != 1 || held != 1 {
		t.Errorf("one video left: %d open, %d buffers held", videos, held)
	}
	l.Do(func(*app.App) { v.Close() })
	if videos, held := VideoCounts(); videos != 0 || held != 0 {
		t.Errorf("all closed: %d open, %d buffers held", videos, held)
	}
	if b.unchecked {
		t.Skip("the window was covered: a frame rate went unchecked")
	}
}
