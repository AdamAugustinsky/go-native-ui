//go:build darwin

package media

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"gonativeui/app/perm"
	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
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

func TestPlayerPlaysToTheEnd(t *testing.T) {
	Warm()
	Warm()
	f := Format{48000, 1, Int16}
	p := openPlayer(t, sine(f, time.Second, 440), f)
	if p.Duration() != time.Second {
		t.Fatalf("Duration = %v", p.Duration())
	}
	start := play(t, p)
	time.Sleep(300 * time.Millisecond)
	p1 := position(p)
	time.Sleep(300 * time.Millisecond)
	p2 := position(p)
	if !near(p1, 300*time.Millisecond, 150*time.Millisecond) || !near(p2-p1, 300*time.Millisecond, 100*time.Millisecond) {
		t.Errorf("positions %v then %v, 300 ms apart: want about 300 ms, advancing at 1×", p1, p2)
	}
	got := waitEvents(p.ID(), 1, 3*time.Second)
	if len(got) != 1 || got[0].ev.Kind != Ended {
		t.Fatalf("events: %+v, want one Ended", got)
	}
	if took := got[0].at.Sub(start); !near(took, time.Second, 250*time.Millisecond) {
		t.Errorf("a 1 s clip ended after %v", took)
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(eventsFor(p.ID())); n != 1 {
		t.Errorf("%d events for one playback", n)
	}
	var playing bool
	var pos time.Duration
	onMain(func() { playing, pos = p.Playing(), p.Position() })
	if playing || pos != 0 {
		t.Errorf("after the end: playing %v at %v; want stopped at 0", playing, pos)
	}

	// Play again: from the start, and a second Ended.
	start = play(t, p)
	got = waitEvents(p.ID(), 2, 3*time.Second)
	if len(got) != 2 || got[1].ev.Kind != Ended {
		t.Fatalf("events after playing again: %+v", got)
	}
	if took := got[1].at.Sub(start); !near(took, time.Second, 250*time.Millisecond) {
		t.Errorf("played again, it ended after %v", took)
	}
}

func TestPlayerRates(t *testing.T) {
	f := Format{48000, 1, Int16}
	for _, tc := range []struct {
		name      string
		rate      float32
		switchAt  time.Duration // set the rate this far in, at 1× before
		wantTotal time.Duration
	}{
		{"2x", 2, 0, 500 * time.Millisecond},
		{"1.5x", 1.5, 0, 667 * time.Millisecond},
		{"2x from 300 ms", 2, 300 * time.Millisecond, 300*time.Millisecond + 350*time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := openPlayer(t, sine(f, time.Second, 440), f)
			if tc.switchAt == 0 {
				onMain(func() { p.SetRate(tc.rate) })
			}
			start := play(t, p)
			if tc.switchAt > 0 {
				time.Sleep(tc.switchAt)
				onMain(func() { p.SetRate(tc.rate) })
			} else {
				time.Sleep(200 * time.Millisecond)
				if d := position(p); !near(d, time.Duration(float32(200*time.Millisecond)*tc.rate), 120*time.Millisecond) {
					t.Errorf("200 ms in at %v×: position %v", tc.rate, d)
				}
			}
			got := waitEvents(p.ID(), 1, 3*time.Second)
			if len(got) != 1 || got[0].ev.Kind != Ended {
				t.Fatalf("events: %+v", got)
			}
			if took := got[0].at.Sub(start); !near(took, tc.wantTotal, 200*time.Millisecond) {
				t.Errorf("a 1 s clip at %v× ended after %v, want about %v", tc.rate, took, tc.wantTotal)
			}
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
	play(t, p)
	time.Sleep(150 * time.Millisecond)
	var held time.Duration
	var playing bool
	onMain(func() { p.Pause(); held, playing = p.Position(), p.Playing() })
	if playing || !near(held, 750*time.Millisecond, 100*time.Millisecond) {
		t.Errorf("paused 150 ms after a seek to 600 ms: playing %v at %v", playing, held)
	}
	time.Sleep(300 * time.Millisecond)
	if d := position(p); !near(d, held, 2*time.Millisecond) {
		t.Errorf("paused at %v, 300 ms later at %v", held, d)
	}
	if got := eventsFor(p.ID()); len(got) != 0 {
		t.Errorf("events while paused: %+v", got)
	}
	resumed := play(t, p)
	got := waitEvents(p.ID(), 1, 2*time.Second)
	if len(got) != 1 {
		t.Fatalf("events: %+v", got)
	}
	if took := got[0].at.Sub(resumed); !near(took, time.Second-held, 200*time.Millisecond) {
		t.Errorf("resumed at %v of a 1 s clip, it ended after %v", held, took)
	}

	// Seeking clamps to the clip, also while playing.
	onMain(func() { p.Seek(5 * time.Second); at = p.Position() })
	if at > p.Duration() {
		t.Errorf("Seek past the end: %v", at)
	}
	onMain(func() { p.Seek(-time.Second); at = p.Position() })
	if at != 0 {
		t.Errorf("Seek before the start: %v", at)
	}
	play(t, p)
	time.Sleep(100 * time.Millisecond)
	onMain(func() { p.Seek(900 * time.Millisecond) })
	start := time.Now()
	if got := waitEvents(p.ID(), 2, 2*time.Second); len(got) != 2 {
		t.Fatalf("events after a seek while playing: %+v", got)
	} else if took := got[1].at.Sub(start); !near(took, 100*time.Millisecond, 150*time.Millisecond) {
		t.Errorf("seeked to 900 ms while playing, it ended after %v", took)
	}
}

func TestPlayerCloseSendsNoEnd(t *testing.T) {
	f := Format{48000, 1, Int16}
	p := openPlayer(t, sine(f, 300*time.Millisecond, 440), f)
	play(t, p)
	time.Sleep(100 * time.Millisecond)
	onMain(p.Close)
	time.Sleep(500 * time.Millisecond)
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
	start := play(t, p)
	got := waitEvents(p.ID(), 1, 2*time.Second)
	if len(got) != 1 || got[0].ev.Kind != Ended {
		t.Fatalf("events: %+v", got)
	}
	if took := got[0].at.Sub(start); !near(took, 400*time.Millisecond, 200*time.Millisecond) {
		t.Errorf("a 400 ms float clip ended after %v", took)
	}
}

// Position and Playing are read every frame while playing (DECISIONS D27).
func TestPlayerPerFrameAllocs(t *testing.T) {
	f := Format{48000, 1, Int16}
	p := openPlayer(t, sine(f, time.Second, 440), f)
	play(t, p)
	time.Sleep(100 * time.Millisecond)
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
