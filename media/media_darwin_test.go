//go:build darwin

package media

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"gonativeui/app/perm"
	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/internal/testlock"
)

// Live tests: real AVAudioPlayers play through the default output, at 5 %
// volume. A runner with no audio output (CI) skips them.

var runLoop struct {
	fn, mode uintptr
	fr       *ffi.Frame
}

func init() {
	if cf.Init() != nil {
		panic("CoreFoundation")
	}
	runLoop.fn = cf.Sym(cf.CoreFoundation(), "CFRunLoopRunInMode")
	runLoop.mode = cf.Const(cf.CoreFoundation(), "kCFRunLoopDefaultMode")
	runLoop.fr = new(ffi.Frame)
}

// runLoopOnce runs the main run loop's pending work (the main queue, where
// the players' delegate is called), without waiting.
func runLoopOnce() {
	runLoop.fr.Reset().Arg(runLoop.mode).ArgF64(0).ArgBool(true).Call(runLoop.fn)
}

func skipWithoutAudio(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, ErrSystem) && os.Getenv("CI") != "" {
		t.Skipf("no audio device on this runner: %v", err)
	}
	t.Fatal(err)
}

// openPlayer makes a quiet Player for pcm, closed when the test ends.
func openPlayer(t *testing.T, pcm []byte, f Format) *Player {
	t.Helper()
	testlock.Hold(t, "audio")
	var p *Player
	var err error
	onMain(func() {
		if p, err = NewPlayer(pcm, f); err == nil {
			p.SetVolume(0.05)
		}
	})
	if err != nil {
		skipWithoutAudio(t, err)
	}
	t.Cleanup(func() { onMain(p.Close) })
	return p
}

// play starts p and returns when it did.
func play(t *testing.T, p *Player) time.Time {
	t.Helper()
	var at time.Time
	var err error
	onMain(func() { at, err = time.Now(), p.Play() })
	if err != nil {
		skipWithoutAudio(t, err)
	}
	return at
}

func position(p *Player) (d time.Duration) {
	onMain(func() { d = p.Position() })
	return d
}

func near(got, want, tol time.Duration) bool { return got >= want-tol && got <= want+tol }

// endedOnTime checks that a play that should last want (the clip's length at
// its rate) had its Ended event delivered took after Play: no earlier than
// want less 50 ms, and no later than want plus 300 ms. The system's end comes
// after the clip's, by the output's latency: 90-145 ms on the CI runner, about
// 120 ms on a Mac mini, about 200 ms for a process's first play; our delivery
// adds under a millisecond (measured, 168 plays). A miss is measured once
// more with again, which plays the same way and returns its took and want: a
// loaded runner delays one end now and then (264-490 ms late), and a real
// fault (a wrong rate, a seek that does not seek, an end that is not
// delivered at once) misses twice. It reports whether it played again.
func endedOnTime(t *testing.T, what string, took, want time.Duration, again func() (took, want time.Duration)) bool {
	t.Helper()
	ok := func(d, w time.Duration) bool { return d >= w-50*time.Millisecond && d <= w+300*time.Millisecond }
	if ok(took, want) {
		return false
	}
	t.Logf("%s ended after %v, want about %v; measured once more", what, took, want)
	if took, want = again(); !ok(took, want) {
		t.Errorf("%s ended after %v, want about %v (and before that too)", what, took, want)
	}
	return true
}

// playToEnd plays p and returns how long after Play its next Ended event was
// delivered.
func playToEnd(t *testing.T, p *Player) time.Duration {
	t.Helper()
	n := len(eventsFor(p.ID()))
	start := play(t, p)
	return endAfter(t, p, n, start)
}

// endAfter waits for p's event n+1, an Ended, and returns how long after
// start it was delivered.
func endAfter(t *testing.T, p *Player, n int, start time.Time) time.Duration {
	t.Helper()
	got := waitEvents(p.ID(), n+1, 3*time.Second)
	if len(got) != n+1 || got[n].ev.Kind != Ended {
		t.Fatalf("events: %+v, want one more Ended", got)
	}
	return got[n].at.Sub(start)
}

// sample reads p's position and the time, together on the main thread.
func sample(p *Player) (d time.Duration, at time.Time) {
	onMain(func() { d, at = p.Position(), time.Now() })
	return d, at
}

// moving returns once p's position has moved (Play's start-up, the output's,
// is not playing at any rate), with the reading that saw it move.
func moving(t *testing.T, p *Player) (time.Duration, time.Time) {
	t.Helper()
	p0, t0 := sample(p)
	for {
		time.Sleep(5 * time.Millisecond)
		p1, t1 := sample(p)
		if p1 != p0 {
			return p1, t1
		}
		if t1.Sub(t0) > 2*time.Second {
			t.Fatalf("the position stayed at %v for 2 s", p0)
		}
	}
}

// rateOf measures the rate p plays at from what it reports: its position's
// advance over at least 500 ms from when it is first seen to move, against
// the time between the two readings (each taken with its position, so a late
// wake-up only lengthens the window).
func rateOf(t *testing.T, p *Player) float64 {
	t.Helper()
	p0, t0 := moving(t, p)
	time.Sleep(500 * time.Millisecond)
	p1, t1 := sample(p)
	return float64(p1-p0) / float64(t1.Sub(t0))
}

// playsAt checks a rate measured by rateOf against want, within 20 % (1× for
// 1.5× is 33 % slow). A miss is measured once more with again: a runner that
// stalls the output for a moment slows one window, a wrong rate both.
func playsAt(t *testing.T, what string, got float64, want float32, again func() float64) {
	t.Helper()
	ok := func(r float64) bool { return r >= float64(want)*0.8 && r <= float64(want)*1.2 }
	if ok(got) {
		return
	}
	t.Logf("%s at %.2f×, want %v×; measured once more", what, got, want)
	if got = again(); !ok(got) {
		t.Errorf("%s at %.2f×, want %v× (and before that too)", what, got, want)
	}
}

func TestPlayerPlaysToTheEnd(t *testing.T) {
	Warm()
	Warm()
	f := Format{48000, 1, Int16}
	const length = 2 * time.Second // room for a rate's 500 ms after a slow start
	p := openPlayer(t, sine(f, length, 440), f)
	if p.Duration() != length {
		t.Fatalf("Duration = %v", p.Duration())
	}
	start := play(t, p)
	rate := rateOf(t, p)
	plays := 1
	if endedOnTime(t, "a 2 s clip", endAfter(t, p, 0, start), length, func() (time.Duration, time.Duration) { return playToEnd(t, p), length }) {
		plays++
	}
	playsAt(t, "a 2 s clip played", rate, 1, func() float64 {
		n := len(eventsFor(p.ID()))
		start := play(t, p)
		r := rateOf(t, p)
		endAfter(t, p, n, start)
		plays++
		return r
	})
	time.Sleep(300 * time.Millisecond)
	if n := len(eventsFor(p.ID())); n != plays {
		t.Errorf("%d events for %d playbacks", n, plays)
	}
	var playing bool
	var pos time.Duration
	onMain(func() { playing, pos = p.Playing(), p.Position() })
	if playing || pos != 0 {
		t.Errorf("after the end: playing %v at %v; want stopped at 0", playing, pos)
	}

	// Play again: from the start, and another Ended.
	again := func() (time.Duration, time.Duration) { return playToEnd(t, p), length }
	took, want := again()
	endedOnTime(t, "played again, it", took, want, again)
}

// TestEndIsDeliveredAtOnce: the system's end call (the delegate's
// audioPlayerDidFinishPlaying:successfully:, made here on the main thread as
// AVFoundation makes it) reaches the app as one Ended event within a turn of
// the main thread. This is the part of an end's latency that is ours; the
// system's own is endedOnTime's.
func TestEndIsDeliveredAtOnce(t *testing.T) {
	f := Format{48000, 1, Int16}
	p := openPlayer(t, sine(f, 5*time.Second, 440), f)
	play(t, p)
	var at time.Time
	onMain(func() {
		at = time.Now()
		objc.F.Reset().Arg(uintptr(players.del[p.n.slot])).Arg(uintptr(sel.didFinish)).Arg(uintptr(p.n.obj)).ArgBool(true).Call(objc.MsgSend)
	})
	got := waitEvents(p.ID(), 1, time.Second)
	if len(got) != 1 || got[0].ev.Kind != Ended {
		t.Fatalf("events: %+v, want one Ended", got)
	}
	if took := got[0].at.Sub(at); took > 200*time.Millisecond {
		t.Errorf("the end was delivered %v after the system's call, want within a turn (2 ms here)", took)
	}
}

func TestPlayerRates(t *testing.T) {
	f := Format{48000, 1, Int16}
	const length = 2 * time.Second // at 2×, 500 ms of measuring take 1 s of it
	for _, tc := range []struct {
		name     string
		rate     float32
		switchAt time.Duration // set the rate this far in, at 1× before
	}{
		{"2x", 2, 0},
		{"1.5x", 1.5, 0},
		{"2x from 300 ms", 2, 300 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := openPlayer(t, sine(f, length, 440), f)
			// One play: its rate (measured from the position) and its end,
			// after the rest of the clip at the rate from when it was set.
			playOnce := func() (rate float64, took, want time.Duration) {
				n := len(eventsFor(p.ID()))
				onMain(func() { p.SetRate(1) })
				if tc.switchAt == 0 {
					onMain(func() { p.SetRate(tc.rate) })
				}
				start := play(t, p)
				want = time.Duration(float32(length) / tc.rate)
				if tc.switchAt > 0 {
					time.Sleep(tc.switchAt)
					var at time.Duration
					var when time.Time
					onMain(func() { p.SetRate(tc.rate); at, when = p.Position(), time.Now() })
					want = when.Sub(start) + time.Duration(float32(length-at)/tc.rate)
				}
				rate = rateOf(t, p)
				return rate, endAfter(t, p, n, start), want
			}
			rate, took, want := playOnce()
			what := fmt.Sprintf("a 2 s clip at %v×", tc.rate)
			endedOnTime(t, what, took, want, func() (time.Duration, time.Duration) {
				_, took, want := playOnce()
				return took, want
			})
			playsAt(t, what, rate, tc.rate, func() float64 {
				rate, _, _ := playOnce()
				return rate
			})
			if p.Rate() != tc.rate {
				t.Errorf("Rate = %v", p.Rate())
			}
		})
	}
	p := openPlayer(t, sine(f, 10*time.Millisecond, 440), f)
	onMain(func() { p.SetRate(5) })
	if p.Rate() != MaxRate {
		t.Errorf("SetRate(5): Rate = %v", p.Rate())
	}
	onMain(func() { p.SetRate(0.1) })
	if p.Rate() != MinRate {
		t.Errorf("SetRate(0.1): Rate = %v", p.Rate())
	}
}

func TestPlayerSeekAndPause(t *testing.T) {
	f := Format{48000, 1, Int16}
	p := openPlayer(t, sine(f, time.Second, 440), f)
	var at time.Duration
	onMain(func() { p.Seek(600 * time.Millisecond); at = p.Position() })
	if !near(at, 600*time.Millisecond, 2*time.Millisecond) {
		t.Errorf("after Seek(600 ms): %v", at)
	}
	// Paused once it has played a little (from when it moves: the output's
	// start-up is not playing): past the seek's time, no further than it
	// could have played since Play, and there it holds.
	played := play(t, p)
	moving(t, p)
	time.Sleep(100 * time.Millisecond)
	var held time.Duration
	var playing bool
	var pausedAt time.Time
	onMain(func() { p.Pause(); held, playing, pausedAt = p.Position(), p.Playing(), time.Now() })
	if playing || held <= 600*time.Millisecond || held > 600*time.Millisecond+pausedAt.Sub(played)+50*time.Millisecond {
		t.Errorf("paused %v after Play from a seek to 600 ms: playing %v at %v", pausedAt.Sub(played), playing, held)
	}
	time.Sleep(300 * time.Millisecond)
	if d := position(p); !near(d, held, 2*time.Millisecond) {
		t.Errorf("paused at %v, 300 ms later at %v", held, d)
	}
	if got := eventsFor(p.ID()); len(got) != 0 {
		t.Errorf("events while paused: %+v", got)
	}
	// Played on, it ends after the rest of the clip.
	resumed := play(t, p)
	endedOnTime(t, fmt.Sprintf("resumed at %v of a 1 s clip, it", held), endAfter(t, p, 0, resumed), time.Second-held, func() (time.Duration, time.Duration) {
		n := len(eventsFor(p.ID()))
		onMain(func() { p.Seek(held) })
		return endAfter(t, p, n, play(t, p)), time.Second - held
	})

	// Seeking clamps to the clip, also while playing.
	onMain(func() { p.Seek(5 * time.Second); at = p.Position() })
	if at > p.Duration() {
		t.Errorf("Seek past the end: %v", at)
	}
	onMain(func() { p.Seek(-time.Second); at = p.Position() })
	if at != 0 {
		t.Errorf("Seek before the start: %v", at)
	}
	// A seek while playing to 100 ms before the end: it ends after those.
	seekPlaying := func() (time.Duration, time.Duration) {
		n := len(eventsFor(p.ID()))
		play(t, p)
		time.Sleep(100 * time.Millisecond)
		var start time.Time
		onMain(func() { p.Seek(900 * time.Millisecond); start = time.Now() })
		return endAfter(t, p, n, start), 100 * time.Millisecond
	}
	took, want := seekPlaying()
	endedOnTime(t, "seeked to 900 ms while playing, it", took, want, seekPlaying)
}

// TestPlayerCloseSendsNoEnd: closed while it plays (once its position
// moves, well before the end of a 1 s clip), a Player sends no Ended, also
// not when the clip's end has passed.
func TestPlayerCloseSendsNoEnd(t *testing.T) {
	f := Format{48000, 1, Int16}
	p := openPlayer(t, sine(f, time.Second, 440), f)
	play(t, p)
	moving(t, p)
	onMain(p.Close)
	time.Sleep(1500 * time.Millisecond)
	if got := eventsFor(p.ID()); len(got) != 0 {
		t.Errorf("events after Close: %+v", got)
	}
	var err error
	var playing bool
	var pos time.Duration
	onMain(func() { err, playing, pos = p.Play(), p.Playing(), p.Position(); p.Close() })
	if !errors.Is(err, ErrClosed) || playing || pos != 0 {
		t.Errorf("a closed Player: Play %v, playing %v, at %v", err, playing, pos)
	}
	if n := events().Outstanding(); n != 0 {
		t.Errorf("%d event records reserved with no player playing", n)
	}
}

func TestPlayerFloatStereo(t *testing.T) {
	f := Format{44100, 2, Float32}
	p := openPlayer(t, sine(f, 400*time.Millisecond, 660), f)
	if p.Duration() != 400*time.Millisecond {
		t.Fatalf("Duration = %v", p.Duration())
	}
	again := func() (time.Duration, time.Duration) { return playToEnd(t, p), 400 * time.Millisecond }
	took, want := again()
	endedOnTime(t, "a 400 ms float clip", took, want, again)
}

// Position and Playing are read every frame while playing (DECISIONS D27).
func TestPlayerPerFrameAllocs(t *testing.T) {
	f := Format{48000, 1, Int16}
	p := openPlayer(t, sine(f, time.Second, 440), f)
	play(t, p)
	moving(t, p) // measured while it plays
	var n float64
	var playing bool
	var at time.Duration
	onMain(func() {
		n = testing.AllocsPerRun(2000, func() {
			_ = p.Position()
			_ = p.Playing()
		})
		playing, at = p.Playing(), p.Position()
	})
	if n != 0 {
		t.Errorf("Position and Playing allocate %v times a frame", n)
	}
	if !playing || at == 0 {
		t.Errorf("measured while not playing: playing %v at %v", playing, at)
	}
}

func TestMaxPlayers(t *testing.T) {
	f := Format{16000, 1, Int16}
	pcm := sine(f, 10*time.Millisecond, 440)
	testlock.Hold(t, "audio")
	var ps []*Player
	var err error
	onMain(func() {
		for range MaxPlayers {
			var p *Player
			if p, err = NewPlayer(pcm, f); err != nil {
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
		skipWithoutAudio(t, err)
	}
	onMain(func() { _, err = NewPlayer(pcm, f) })
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("player %d: %v, want ErrBusy", MaxPlayers+1, err)
	}
	onMain(func() {
		ps[3].Close()
		var p *Player
		if p, err = NewPlayer(pcm, f); err == nil {
			ps[3] = p
		}
	})
	if err != nil {
		t.Errorf("a player after closing one: %v", err)
	}
}

// ---- Recording ----

// The system's WAV writer (AudioToolbox's AudioFile, which AVAudioRecorder
// writes with) produces files parseWAV reads back exactly.

type audioDesc struct {
	SampleRate                                                      float64
	FormatID, Flags, BytesPerPacket, FramesPerPacket, BytesPerFrame uint32
	ChannelsPerFrame, BitsPerChannel, Reserved                      uint32
}

// Heap memory for the C calls (globals do not move).
var (
	atDesc  = new(audioDesc)
	atFile  = new(uintptr)
	atBytes = new(uint32)
)

func TestParseSystemWAV(t *testing.T) {
	lib, err := cf.Framework("AudioToolbox")
	if err != nil {
		t.Fatal(err)
	}
	create, write, closeFile := cf.Sym(lib, "AudioFileCreateWithURL"), cf.Sym(lib, "AudioFileWriteBytes"), cf.Sym(lib, "AudioFileClose")
	fr := cf.Acquire()
	defer cf.Release(fr)
	for _, f := range []Format{{48000, 1, Int16}, {16000, 2, Int16}, {44100, 1, Float32}} {
		pcm := sine(f, 50*time.Millisecond, 500)
		path := filepath.Join(t.TempDir(), "sys.wav")
		*atDesc = audioDesc{SampleRate: float64(f.SampleRate), FormatID: formatLPCM, Flags: 4 | 8, // signed integer, packed
			BytesPerPacket: uint32(f.FrameBytes()), FramesPerPacket: 1, BytesPerFrame: uint32(f.FrameBytes()),
			ChannelsPerFrame: uint32(f.Channels), BitsPerChannel: uint32(8 * f.Sample.Bytes())}
		if f.Sample == Float32 {
			atDesc.Flags = 1 | 8 // float, packed
		}
		url := cf.FileURL(fr, path, false)
		st := int32(fr.Reset().Arg(url).Arg32(0x57415645). // kAudioFileWAVEType
									ArgPtr(unsafe.Pointer(atDesc)).Arg32(1).ArgPtr(unsafe.Pointer(atFile)).Call(create).R0())
		cf.CFRelease(fr, url)
		if st != 0 {
			t.Fatalf("%+v: AudioFileCreateWithURL: %d", f, st)
		}
		buf := append(make([]byte, 0, len(pcm)), pcm...)
		*atBytes = uint32(len(buf))
		st = int32(fr.Reset().Arg(*atFile).ArgBool(false).Arg(0).ArgPtr(unsafe.Pointer(atBytes)).ArgPtr(unsafe.Pointer(&buf[0])).Call(write).R0())
		fr.Call1(closeFile, *atFile)
		if st != 0 || *atBytes != uint32(len(buf)) {
			t.Fatalf("%+v: AudioFileWriteBytes: %d, %d bytes", f, st, *atBytes)
		}
		file, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got, data, err := parseWAV(file)
		if err != nil || got != f || !bytes.Equal(data, pcm) {
			t.Errorf("%+v: the system's WAV (%d bytes) parsed as %+v, %d bytes, %v", f, len(file), got, len(data), err)
		}
	}
}

// The real microphone, when this process may use it. Access cannot be
// granted headlessly, so the test is skipped otherwise; the denied and
// undetermined paths are tested with the state faked.
func TestRecorderLive(t *testing.T) {
	st, err := perm.Status(perm.Microphone)
	if err != nil {
		t.Fatal(err)
	}
	if st != perm.Authorized {
		t.Skipf("microphone access is %v for this process; grant it (System Settings › Privacy & Security › Microphone) to run the live capture", st)
	}
	testlock.Hold(t, "audio")
	r := NewRecorder(RecorderOptions{SampleRate: 16000})
	onMain(func() { err = r.Start() })
	if err != nil {
		skipWithoutAudio(t, err)
	}
	defer onMain(r.Close)
	time.Sleep(600 * time.Millisecond)
	var el time.Duration
	var lv Meter
	var allocs float64
	onMain(func() {
		el, lv = r.Elapsed(), r.Level()
		allocs = testing.AllocsPerRun(500, func() { _, _ = r.Elapsed(), r.Level() })
	})
	if !near(el, 600*time.Millisecond, 250*time.Millisecond) || lv.Average < Silence || lv.Average > 0 {
		t.Errorf("600 ms in: elapsed %v, level %+v", el, lv)
	}
	if allocs != 0 {
		t.Errorf("Elapsed and Level allocate %v times a frame", allocs)
	}
	onMain(func() { err = r.Stop() })
	if err != nil {
		t.Fatal(err)
	}
	if got := waitEvents(r.ID(), 1, 3*time.Second); len(got) != 1 || got[0].ev.Kind != Recorded {
		t.Fatalf("events: %+v", got)
	}
	var pcm []byte
	var f Format
	onMain(func() { pcm, f, err = r.Take() })
	if err != nil || f != r.Format() || !near(f.Duration(len(pcm)), 600*time.Millisecond, 250*time.Millisecond) {
		t.Errorf("Take: %d bytes (%v) as %+v, %v", len(pcm), f.Duration(len(pcm)), f, err)
	}
}
