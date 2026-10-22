//go:build darwin

package media

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/testlock"
)

// Live tests of OpenPlayer: a real AAC/M4A file made with AVAudioFile, a
// system sound, and files that are not playable. Quiet, and short.

// writeM4A writes d of a 440 Hz sine as AAC in an M4A file at path, through
// AVAudioFile (AVFoundation).
func writeM4A(t *testing.T, path string, d time.Duration) {
	t.Helper()
	if _, err := cf.Framework("AVFoundation"); err != nil {
		t.Fatal(err)
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()
	sel := func(s string) uintptr { return cf.Sel(fr, s) }
	cls := func(s string) uintptr { return cf.Cls(fr, s) }
	num := func(v int) uintptr { return cf.Msg(fr, cls("NSNumber"), sel("numberWithInteger:"), uintptr(v)) }
	const rate = 44100
	n := int(d * rate / time.Second)

	// Arguments are evaluated before the frame is reset: no cf calls inside
	// a chain on the same frame.
	fa, sf := cf.Msg(fr, cls("AVAudioFormat"), sel("alloc")), sel("initStandardFormatWithSampleRate:channels:")
	fmtObj := fr.Reset().Arg(fa).Arg(sf).ArgF64(rate).Arg32(1).Call(cf.MsgAddr()).R0()
	ba, sb := cf.Msg(fr, cls("AVAudioPCMBuffer"), sel("alloc")), sel("initWithPCMFormat:frameCapacity:")
	buf := fr.Reset().Arg(ba).Arg(sb).Arg(fmtObj).Arg32(uint32(n)).Call(cf.MsgAddr()).R0()
	if fmtObj == 0 || buf == 0 {
		t.Fatal("AVAudioPCMBuffer")
	}
	sl := sel("setFrameLength:")
	fr.Reset().Arg(buf).Arg(sl).Arg32(uint32(n)).Call(cf.MsgAddr())
	chans := cf.Msg(fr, buf, sel("floatChannelData"))
	ch := unsafe.Slice((*float32)(ffi.PtrFromC(*(*uintptr)(ffi.PtrFromC(chans)))), n)
	for i := range ch {
		ch[i] = float32(0.25 * math.Sin(2*math.Pi*440*float64(i)/rate))
	}

	st := cf.Dict(fr)
	defer cf.CFRelease(fr, st)
	set := func(key string, v uintptr) {
		lib, _ := cf.Framework("AVFoundation")
		cf.DictSet(fr, st, cf.Const(lib, key), v)
	}
	set("AVFormatIDKey", num(0x61616320)) // kAudioFormatMPEG4AAC, 'aac '
	set("AVSampleRateKey", num(rate))
	set("AVNumberOfChannelsKey", num(1))

	url := cf.FileURL(fr, path, false)
	defer cf.CFRelease(fr, url)
	errOut := make([]uintptr, 1)
	file := cf.Msg(fr, cf.Msg(fr, cls("AVAudioFile"), sel("alloc")), sel("initForWriting:settings:error:"),
		url, st, uintptr(unsafe.Pointer(&errOut[0])))
	if file == 0 {
		t.Fatalf("AVAudioFile: %s", cf.GoString(fr, cf.Msg(fr, errOut[0], sel("localizedDescription"))))
	}
	if !cf.MsgBool(fr, file, sel("writeFromBuffer:error:"), buf, uintptr(unsafe.Pointer(&errOut[0]))) {
		t.Fatalf("writeFromBuffer: %s", cf.GoString(fr, cf.Msg(fr, errOut[0], sel("localizedDescription"))))
	}
	cf.Msg(fr, file, sel("release")) // closes the file
	cf.Msg(fr, buf, sel("release"))
	cf.Msg(fr, fmtObj, sel("release"))
}

// openFilePlayer opens path, quiet, and waits for its Opened or Failed event
// (up to 3 s); the Player is closed when the test ends.
func openFilePlayer(t *testing.T, path string) (p *Player, kind EventKind) {
	t.Helper()
	testlock.Hold(t, "audio")
	var err error
	onMain(func() {
		if p, err = OpenPlayer(path); err == nil {
			p.SetVolume(0.05)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { onMain(p.Close) })
	got := waitEvents(p.ID(), 1, 3*time.Second)
	if len(got) != 1 {
		t.Fatalf("events for opening %s: %+v, want one", filepath.Base(path), got)
	}
	return p, got[0].ev.Kind
}

func state(p *Player) (ready bool, dur time.Duration, err error) {
	onMain(func() { ready, dur, err = p.Ready(), p.Duration(), p.Err() })
	return
}

func skipIfNoAudio(t *testing.T, p *Player) {
	t.Helper()
	if _, _, err := state(p); errors.Is(err, ErrSystem) {
		skipWithoutAudio(t, err)
	}
}

func TestOpenPlayerM4A(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.m4a")
	const length = 2 * time.Second // room for a rate's 500 ms at 2×
	writeM4A(t, path, length)

	testlock.Hold(t, "audio")
	var p *Player
	var err error
	var readyAtOpen bool
	var durAtOpen time.Duration
	var playErr error
	onMain(func() {
		if p, err = OpenPlayer(path); err != nil {
			return
		}
		p.SetVolume(0.05)
		readyAtOpen, durAtOpen, playErr = p.Ready(), p.Duration(), p.Play()
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { onMain(p.Close) })
	if readyAtOpen || durAtOpen != 0 || !errors.Is(playErr, ErrState) {
		t.Errorf("right after OpenPlayer: ready %v, duration %v, Play %v; want not ready, 0, ErrState", readyAtOpen, durAtOpen, playErr)
	}
	got := waitEvents(p.ID(), 1, 3*time.Second)
	if len(got) != 1 {
		t.Fatalf("events: %+v", got)
	}
	if got[0].ev.Kind != Opened {
		skipIfNoAudio(t, p)
		t.Fatalf("event %v (%v), want Opened", got[0].ev.Kind, p.Err())
	}
	ready, dur, err := state(p)
	if !ready || err != nil || !near(dur, length, 60*time.Millisecond) {
		t.Fatalf("after Opened: ready %v, duration %v, err %v; want a %v clip", ready, dur, err, length)
	}

	// At 1×, then at 2× (pitch kept), from the start each time: the rate
	// from the position (rateOf), the end (endedOnTime), and one Ended for
	// each playback.
	plays := 0
	for _, rate := range []float32{1, 2} {
		onMain(func() { p.SetRate(rate) })
		playOnce := func() (float64, time.Duration) {
			n := len(eventsFor(p.ID()))
			start := play(t, p)
			r := rateOf(t, p)
			plays++
			return r, endAfter(t, p, n, start)
		}
		what := fmt.Sprintf("a %v M4A at %v×", length, rate)
		want := time.Duration(float32(dur) / rate)
		got, took := playOnce()
		endedOnTime(t, what, took, want, func() (time.Duration, time.Duration) { _, took := playOnce(); return took, want })
		playsAt(t, what, got, rate, func() float64 { r, _ := playOnce(); return r })
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(eventsFor(p.ID())); n != 1+plays {
		t.Errorf("%d events for one open and %d playbacks", n, plays)
	}
}

func TestOpenPlayerKeepsRateAndVolumeSetWhileOpening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.m4a")
	writeM4A(t, path, 2*time.Second) // room for a rate's 500 ms at 2×
	testlock.Hold(t, "audio")
	var p *Player
	onMain(func() {
		p, _ = OpenPlayer(path)
		p.SetRate(2)
		p.SetVolume(0.05)
	})
	t.Cleanup(func() { onMain(p.Close) })
	if got := waitEvents(p.ID(), 1, 3*time.Second); len(got) != 1 || got[0].ev.Kind != Opened {
		skipIfNoAudio(t, p)
		t.Fatalf("events: %+v", got)
	}
	// From 300 ms: the rate from the position (rateOf), and the end after
	// the rest of the clip at 2×.
	playOnce := func() (rate float64, took, want time.Duration) {
		n := len(eventsFor(p.ID()))
		var playErr error
		var at, dur time.Duration
		var start time.Time
		onMain(func() {
			p.Ready() // attaches
			p.Seek(300 * time.Millisecond)
			at, dur = p.Position(), p.Duration()
			start, playErr = time.Now(), p.Play()
		})
		if playErr != nil || !near(at, 300*time.Millisecond, 5*time.Millisecond) {
			t.Fatalf("Play %v, Seek(300 ms) gave %v", playErr, at)
		}
		rate = rateOf(t, p)
		return rate, endAfter(t, p, n, start), (dur - at) / 2
	}
	const what = "the rest from 300 ms at 2× (set before the file was ready)"
	rate, took, want := playOnce()
	endedOnTime(t, what, took, want, func() (time.Duration, time.Duration) { _, took, want := playOnce(); return took, want })
	playsAt(t, what, rate, 2, func() float64 { rate, _, _ := playOnce(); return rate })
	if p.Rate() != 2 || p.Volume() != 0.05 {
		t.Errorf("rate %v, volume %v", p.Rate(), p.Volume())
	}
}

func TestOpenPlayerSystemSound(t *testing.T) {
	const tink = "/System/Library/Sounds/Tink.aiff"
	if _, err := os.Stat(tink); err != nil {
		t.Skip(err)
	}
	p, kind := openFilePlayer(t, tink)
	if kind != Opened {
		skipIfNoAudio(t, p)
		t.Fatalf("event %v (%v)", kind, p.Err())
	}
	_, dur, _ := state(p)
	if dur < 100*time.Millisecond || dur > 3*time.Second {
		t.Fatalf("Tink lasts %v?", dur)
	}
	again := func() (time.Duration, time.Duration) { return playToEnd(t, p), dur }
	took, want := again()
	endedOnTime(t, "Tink", took, want, again)
}

func TestOpenPlayerErrors(t *testing.T) {
	dir := t.TempDir()
	notAudio := filepath.Join(dir, "notes.mp3")
	if err := os.WriteFile(notAudio, []byte("this is a text file, not an MP3 stream"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.m4a")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	onMain(func() { _, err = OpenPlayer("") })
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("OpenPlayer(\"\") = %v, want ErrInvalid", err)
	}
	for _, tc := range []struct {
		name, path string
		want       error
	}{
		{"missing", filepath.Join(dir, "nope.m4a"), fs.ErrNotExist},
		{"not audio", notAudio, ErrFormat},
		{"empty", empty, ErrFormat},
		{"a directory", dir, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, kind := openFilePlayer(t, tc.path)
			if kind != Failed {
				t.Fatalf("event %v, want Failed", kind)
			}
			var ready bool
			var perr, playErr error
			var dur time.Duration
			onMain(func() { ready, dur, perr, playErr = p.Ready(), p.Duration(), p.Err(), p.Play() })
			if ready || dur != 0 || perr == nil || playErr == nil {
				t.Fatalf("ready %v, duration %v, Err %v, Play %v", ready, dur, perr, playErr)
			}
			if tc.want != nil && !errors.Is(perr, tc.want) {
				t.Errorf("Err = %v, want %v", perr, tc.want)
			}
			t.Logf("Err: %v", perr)
			if n := events().Outstanding(); n != 0 {
				t.Errorf("%d event records left reserved", n)
			}
		})
	}
}

func TestOpenPlayerCloseWhileOpening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.m4a")
	writeM4A(t, path, 300*time.Millisecond)
	testlock.Hold(t, "audio")
	for i := range 40 {
		var p *Player
		var err error
		onMain(func() { p, err = OpenPlayer(path) })
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(i) * 500 * time.Microsecond)
		onMain(p.Close)
		onMain(func() {
			if p.Ready() || p.Playing() || p.Position() != 0 || !errors.Is(p.Play(), ErrClosed) {
				t.Error("a closed file Player is alive")
			}
		})
	}
	// The goroutines finish (3 s at most: a loaded runner's are slow).
	for t0 := time.Now(); events().Outstanding() != 0 && time.Since(t0) < 3*time.Second; {
		time.Sleep(10 * time.Millisecond)
	}
	if n := events().Outstanding(); n != 0 {
		t.Errorf("%d event records left reserved 3 s on", n)
	}
	// Every slot came back.
	var ps []*Player
	onMain(func() {
		for range MaxPlayers {
			p, err := OpenPlayer(path)
			if err != nil {
				t.Errorf("slot leaked: %v", err)
				break
			}
			ps = append(ps, p)
		}
	})
	onMain(func() {
		for _, p := range ps {
			p.Close()
		}
	})
}

func TestOpenPlayerMaxPlayers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.m4a")
	writeM4A(t, path, 100*time.Millisecond)
	testlock.Hold(t, "audio")
	var ps []*Player
	var err error
	onMain(func() {
		for range MaxPlayers {
			var p *Player
			if p, err = OpenPlayer(path); err != nil {
				return
			}
			ps = append(ps, p)
		}
	})
	defer onMain(func() {
		for _, p := range ps {
			p.Close()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	onMain(func() { _, err = OpenPlayer(path) })
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("player %d: %v, want ErrBusy", MaxPlayers+1, err)
	}
	onMain(func() { err = nil; ps[0].Close(); ps[0], err = OpenPlayer(path) })
	if err != nil {
		t.Fatalf("after Close: %v", err)
	}
}

// Position and Playing stay allocation-free on a file Player.
func TestOpenPlayerPerFrameAllocs(t *testing.T) {
	testOpenPlayerPerFrameAllocs(t, false)
}

func TestOpenPlayerPerFrameAllocsDelayedHandoff(t *testing.T) {
	testOpenPlayerPerFrameAllocs(t, true)
}

func testOpenPlayerPerFrameAllocs(t *testing.T, delayed bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clip.m4a")
	// Cover moving's two-second progress bound, with another second for
	// sampling and the measured reads. The readiness wait stays unchanged.
	writeM4A(t, path, 3*time.Second)
	p, kind := openFilePlayer(t, path)
	if kind != Opened {
		skipIfNoAudio(t, p)
		t.Fatalf("event %v", kind)
	}
	start := play(t, p)
	defer func() {
		if !t.Failed() {
			return
		}
		var ready, playing bool
		var at, dur, elapsed, handoff time.Duration
		var err error
		queued := time.Now()
		onMain(func() {
			handoff, elapsed = time.Since(queued), time.Since(start)
			ready, playing, at, dur, err = p.Ready(), p.Playing(), p.Position(), p.Duration(), p.Err()
		})
		t.Logf("failure state on main: since Play=%v handoff=%v ready=%v playing=%v position=%v duration=%v err=%v", elapsed, handoff, ready, playing, at, dur, err)
		for _, e := range eventsFor(p.ID()) {
			t.Logf("player event: kind=%v ended=%v failed=%v since Play=%v", e.ev.Kind, e.ev.Kind == Ended, e.ev.Kind == Failed, e.at.Sub(start))
		}
	}()
	if delayed {
		// Reproduce a delayed first sample, not a playback-readiness wait:
		// a one-second fixture finishes and resets to zero during this turn.
		onMain(func() { time.Sleep(1200 * time.Millisecond) })
	}
	moving(t, p) // measured while it plays
	var n float64
	var playing bool
	var at time.Duration
	onMain(func() {
		n = testing.AllocsPerRun(2000, func() {
			_ = p.Position()
			_ = p.Playing()
			_ = p.Ready()
			_ = p.Duration()
		})
		playing, at = p.Playing(), p.Position()
	})
	if n != 0 {
		t.Errorf("Position, Playing, Ready and Duration allocate %v times a frame", n)
	}
	if !playing || at == 0 {
		t.Errorf("measured while not playing: playing %v at %v", playing, at)
	}
}
