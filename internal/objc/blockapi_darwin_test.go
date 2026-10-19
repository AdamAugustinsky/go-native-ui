//go:build darwin

package objc

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// Block copy per API (D36's assumptions): each API that keeps a completion
// block must copy it, and calls it as the cancellation table says. The
// block passed is a per-request literal, poisoned when the passing call
// returns (Request.Sent), so an API that kept the literal itself instead of
// a copy would reach the stale handler; the tests count the calls of both.
//
// UNUserNotificationCenter and AVCaptureDevice cannot be called from an
// unbundled test binary (no bundle proxy; no usage description), so they
// are not covered here.

// apiResult is what one request's completion saw.
type apiResult struct {
	calls  atomic.Int32
	arg    atomic.Int64
	onMain atomic.Bool
}

var (
	apiOnce       sync.Once
	apiKind       *BlockKind // void (^)(NSInteger)
	apiResults    [16]apiResult
	apiStale      atomic.Int32
	fnPthreadMain uintptr
	appKitErr     error
	nsApp         ID
)

func apiSetup() {
	apiOnce.Do(func() {
		sys, _ := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
		fnPthreadMain, _ = purego.Dlsym(sys, "pthread_main_np")
		apiKind = NewBlockKind("v16@?0q8", true, func(ctx uintptr, c *ffi.CB) {
			r := &apiResults[ctx]
			r.arg.Store(int64(c.Int(1)))
			r.onMain.Store(c.Frame().Call0(fnPthreadMain) != 0)
			r.calls.Add(1)
		}, func(*ffi.CB) { apiStale.Add(1) })
		if _, appKitErr = purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_NOW|purego.RTLD_GLOBAL); appKitErr != nil {
			return
		}
		onMain(func() {
			nsApp = Send(Cls("NSApplication"), Sel("sharedApplication"))
			Send1(nsApp, Sel("setActivationPolicy:"), 1) // accessory: no Dock icon
			Send(nsApp, Sel("finishLaunching"))
		})
	})
}

var nsModalResponseAbort int64 = -1001

// attachedSheet returns the sheet attached to win.
func attachedSheet(win ID) ID { return Send(win, Sel("attachedSheet")) }

func newWindow() ID {
	w := Send(Cls("NSWindow"), Sel("alloc"))
	sel := Sel("initWithContentRect:styleMask:backing:defer:") // before the builder: Sel uses F
	w = ID(F.Reset().Arg(uintptr(w)).Arg(uintptr(sel)).
		ArgRect(200, 200, 480, 320).Arg(15).Arg(2).ArgBool(false).Call(MsgSend).R0())
	SendBool1(w, Sel("setReleasedWhenClosed:"), 0)
	Send1(w, Sel("makeKeyAndOrderFront:"), 0)
	return w
}

// sheet is one way to put a sheet on a window: begin it with a completion
// block and return the sheet's window.
type sheet struct {
	name  string
	begin func(win ID, block ID) (sheetWindow ID)
}

var sheets = []sheet{
	{"NSAlert", func(win, block ID) ID {
		a := Send(Send(Cls("NSAlert"), Sel("alloc")), Sel("init"))
		Send1(a, Sel("setMessageText:"), uintptr(Str("F1 feasibility")))
		Send2(a, Sel("beginSheetModalForWindow:completionHandler:"), uintptr(win), uintptr(block))
		return Send(a, Sel("window"))
	}},
	{"NSSavePanel", func(win, block ID) ID {
		p := Send(Cls("NSSavePanel"), Sel("savePanel"))
		Send2(p, Sel("beginSheetModalForWindow:completionHandler:"), uintptr(win), uintptr(block))
		return p
	}},
	{"NSOpenPanel", func(win, block ID) ID {
		p := Send(Cls("NSOpenPanel"), Sel("openPanel"))
		Send2(p, Sel("beginSheetModalForWindow:completionHandler:"), uintptr(win), uintptr(block))
		return p
	}},
}

// Measured on macOS 26 (MacBook, arm64): every API copies its block and
// calls it once on the main thread when the sheet ends. Closing the window
// under a sheet does not call it at all (waited 10 s), contrary to the
// cancellation table's assumption; the workaround, which the test checks,
// is to end the attached sheet with NSModalResponseAbort before closing the
// window, which calls the completion exactly once. A request whose window
// closed without it must be cancelled, or its slot stays taken.
func TestBlockCopyPerAPISheets(t *testing.T) {
	if os.Getenv("GNUI_NO_WINDOWS") != "" {
		t.Skip("GNUI_NO_WINDOWS")
	}
	apiSetup()
	if appKitErr != nil {
		t.Skip("AppKit:", appKitErr)
	}
	ctx := uintptr(0)
	for _, sh := range sheets {
		for _, end := range []string{"endSheet", "abort, then close", "close"} {
			ctx++
			res := &apiResults[ctx]
			stale0 := apiStale.Load()
			var began bool
			onMain(func() {
				win := newWindow()
				r, err := apiKind.New(ctx)
				if err != nil {
					return
				}
				sw := sh.begin(win, r.Block())
				r.Sent()
				began = sw != 0
				runLoop(1, func() bool { return SendBool(win, Sel("attachedSheet")) })
				switch end {
				case "endSheet":
					Send2(win, Sel("endSheet:returnCode:"), uintptr(sw), 1000)
				case "abort, then close":
					Send2(win, Sel("endSheet:returnCode:"), uintptr(attachedSheet(win)), uintptr(nsModalResponseAbort))
					Send(win, Sel("close"))
				case "close":
					Send(win, Sel("close"))
				}
				wait := 10.0
				if end == "close" {
					wait = 2
				}
				runLoop(wait, func() bool { return res.calls.Load() > 0 })
				runLoop(0.3, func() bool { return false }) // a second call would land here
				if end == "endSheet" {
					Send(win, Sel("close"))
				}
				r.Cancel() // no-op once completed; frees the slot if the API never called
				Release(win)
			})
			calls, stale := res.calls.Load(), apiStale.Load()-stale0
			t.Logf("%s, %s: began %v; completion called %d times (on main thread: %v) with %d; stale calls %d",
				sh.name, end, began, calls, res.onMain.Load(), res.arg.Load(), stale)
			if !began {
				t.Errorf("%s: the sheet did not begin", sh.name)
				continue
			}
			if stale != 0 {
				t.Errorf("%s, %s: %d calls through the poisoned literal: the API kept the block without copying it", sh.name, end, stale)
			}
			switch {
			case end == "close" && calls > 1:
				t.Errorf("%s, %s: the completion was called %d times, want at most once", sh.name, end, calls)
			case end != "close" && calls != 1:
				t.Errorf("%s, %s: the completion was called %d times, want exactly once", sh.name, end, calls)
			case end == "abort, then close" && res.arg.Load() != nsModalResponseAbort:
				t.Errorf("%s, %s: the completion got %d, want NSModalResponseAbort", sh.name, end, res.arg.Load())
			}
		}
	}
}

// AVAudioPlayerNode scheduleBuffer:completionHandler: and AVAudioNode
// installTapOnBus:bufferSize:format:block:, driven by offline manual
// rendering (no audio device needed).
var (
	avOnce             sync.Once
	avErr              error
	tapKind            *BlockKind // void (^)(AVAudioPCMBuffer *, AVAudioTime *)
	tapCalls, tapStale atomic.Int32
	tapOnMain          atomic.Int32
)

func TestBlockCopyPerAPIAudio(t *testing.T) {
	apiSetup()
	avOnce.Do(func() {
		_, avErr = purego.Dlopen("/System/Library/Frameworks/AVFoundation.framework/AVFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		tapKind = NewBlockKind("v24@?0@8@16", false, func(ctx uintptr, c *ffi.CB) {
			tapCalls.Add(1) // first: a call that claimed before Cancel may still finish after it
			if c.Frame().Call0(fnPthreadMain) != 0 {
				tapOnMain.Add(1)
			}
		}, func(*ffi.CB) { tapStale.Add(1) })
	})
	if avErr != nil {
		t.Skip("AVFoundation:", avErr)
	}
	base := Leaks()
	const nbuf, first = 3, 8 // contexts first..first+nbuf-1 of apiResults
	for i := 0; i < nbuf; i++ {
		apiResults[first+i] = apiResult{}
	}
	stale0 := apiStale.Load()
	var failed string
	var rendered, tapsBeforeRemove, tapsAfterRemove int32
	var reqs [nbuf]Request
	var doneBeforeStop [nbuf]int32
	onMain(func() {
		alloc := func(cls string) ID { return Send(Cls(cls), Sel("alloc")) }
		engine := Send(alloc("AVAudioEngine"), Sel("init"))
		player := Send(alloc("AVAudioPlayerNode"), Sel("init"))
		Send1(engine, Sel("attachNode:"), uintptr(player))
		// Every Sel, Cls and Send runs before its builder chain starts: they use F.
		selFormat, fa := Sel("initStandardFormatWithSampleRate:channels:"), alloc("AVAudioFormat")
		format := ID(F.Reset().Arg(uintptr(fa)).Arg(uintptr(selFormat)).ArgF64(48000).Arg32(2).Call(MsgSend).R0())
		mixer := Send(engine, Sel("mainMixerNode"))
		Send3(engine, Sel("connect:to:format:"), uintptr(player), uintptr(mixer), uintptr(format))
		selManual := Sel("enableManualRenderingMode:format:maximumFrameCount:error:")
		if !F.Reset().Arg(uintptr(engine)).Arg(uintptr(selManual)).Arg(0).Arg(uintptr(format)).Arg32(4096).Arg(0).Call(MsgSend).Bool() {
			failed = "enableManualRenderingMode failed"
			return
		}
		if !SendBool1(engine, Sel("startAndReturnError:"), 0) {
			failed = "the engine did not start"
			return
		}
		selPCM, selLength := Sel("initWithPCMFormat:frameCapacity:"), Sel("setFrameLength:")
		selSchedule := Sel("scheduleBuffer:completionHandler:")
		for i := range reqs {
			ba := alloc("AVAudioPCMBuffer")
			buf := ID(F.Reset().Arg(uintptr(ba)).Arg(uintptr(selPCM)).Arg(uintptr(format)).Arg32(9600).Call(MsgSend).R0())
			F.Reset().Arg(uintptr(buf)).Arg(uintptr(selLength)).Arg32(9600).Call(MsgSend)
			reqs[i], _ = apiKind.New(uintptr(first + i))
			Send2(player, selSchedule, uintptr(buf), uintptr(reqs[i].Block()))
			reqs[i].Sent()
			Release(buf)
		}
		tap, _ := tapKind.New(0)
		selTap := Sel("installTapOnBus:bufferSize:format:block:")
		F.Reset().Arg(uintptr(mixer)).Arg(uintptr(selTap)).Arg(0).Arg32(1024).Arg(0).Arg(uintptr(tap.Block())).Call(MsgSend)
		tap.Sent()
		Send(player, Sel("play"))

		oa, of := alloc("AVAudioPCMBuffer"), Send(engine, Sel("manualRenderingFormat"))
		out := ID(F.Reset().Arg(uintptr(oa)).Arg(uintptr(selPCM)).Arg(uintptr(of)).Arg32(4096).Call(MsgSend).R0())
		selRender := Sel("renderOffline:toBuffer:error:")
		render := func(n int) {
			for k := 0; k < n; k++ {
				if st := F.Reset().Arg(uintptr(engine)).Arg(uintptr(selRender)).Arg32(4096).Arg(uintptr(out)).Arg(0).Call(MsgSend).R0(); st == 0 {
					rendered++
				}
			}
		}
		render(3) // 12288 of the 28800 frames: the first buffer plays out
		runLoop(2, func() bool { return apiResults[first].calls.Load() > 0 })
		runLoop(0.5, func() bool { return tapCalls.Load() > 0 })
		selRemove := Sel("removeTapOnBus:")
		Send1(mixer, selRemove, 0)
		tap.Cancel()
		// Runs that claimed before Cancel may still be inside; once the slot's
		// claims drain, every run that will ever succeed has counted itself.
		runLoop(2, func() bool { return slots[tap.slot].word.Load()&claimMask == 0 })
		tapsBeforeRemove = tapCalls.Load()
		render(3) // 24576 frames in all: the third buffer is still pending
		runLoop(0.3, func() bool { return false })
		tapsAfterRemove = tapCalls.Load() - tapsBeforeRemove

		for i := range reqs {
			doneBeforeStop[i] = apiResults[first+i].calls.Load()
		}
		Send(player, Sel("stop")) // the third buffer: called on stop
		runLoop(2, func() bool { return apiResults[first+2].calls.Load() > 0 })
		runLoop(0.3, func() bool { return false })
		Send(engine, Sel("stop"))
		for _, r := range reqs {
			r.Cancel()
		}
		Release(out)
		Release(format)
		Release(player)
		Release(engine)
	})
	if failed != "" {
		t.Skip(failed)
	}
	for i := 0; i < nbuf; i++ {
		r := &apiResults[first+i]
		t.Logf("scheduleBuffer %d: completion called %d times (%d before the node stopped; on main thread: %v)", i, r.calls.Load(), doneBeforeStop[i], r.onMain.Load())
		if r.calls.Load() != 1 {
			t.Errorf("buffer %d: the completion was called %d times, want exactly once", i, r.calls.Load())
		}
	}
	t.Logf("rendered %d cycles; tap called %d times before removal (%d on the main thread), %d after; stale tap calls %d",
		rendered, tapsBeforeRemove, tapOnMain.Load(), tapsAfterRemove, tapStale.Load())
	if doneBeforeStop[nbuf-1] != 0 {
		t.Error("the last buffer completed before the node stopped; the test no longer covers the stop")
	}
	if tapsBeforeRemove == 0 {
		t.Error("the tap was never called")
	}
	if tapsAfterRemove != 0 {
		t.Errorf("the tap's handler ran %d times after removeTapOnBus: and Cancel", tapsAfterRemove)
	}
	if s := apiStale.Load() - stale0; s != 0 {
		t.Errorf("%d completion calls through a poisoned literal", s)
	}
	if got := Leaks().Blocks; got != base.Blocks {
		t.Errorf("%d blocks live after the run, want %d", got, base.Blocks)
	}
}
