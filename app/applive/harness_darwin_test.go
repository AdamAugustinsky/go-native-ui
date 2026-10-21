//go:build darwin

package applive

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/app"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/ui"
)

func init() { runtime.LockOSThread() }

// TestMain runs a scenario when the binary is a scenario's child, the tests
// otherwise.
func TestMain(m *testing.M) {
	if name := os.Getenv("GNUI_APPLIVE"); name != "" {
		runScenario(name)
		return
	}
	os.Exit(m.Run())
}

// result is what a child printed and how it ended.
type result struct {
	kv   map[string]string
	out  string
	code int
	took time.Duration
}

func (r result) get(k string) string { return r.kv[k] }

func (r result) int(k string) int {
	n, _ := strconv.Atoi(r.kv[k])
	return n
}

// child runs scenario name with env in a child process and waits for it at
// most limit (a deadlock fails the test).
func child(t *testing.T, name string, limit time.Duration, env ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(append(os.Environ(), "GNUI_APPLIVE="+name, "GNUI_NOANIM=1"), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	t0 := time.Now()
	err := cmd.Run()
	r := result{kv: map[string]string{}, out: out.String(), took: time.Since(t0)}
	if ctx.Err() != nil {
		t.Fatalf("%s %v: did not end within %v (deadlock?)\n%s", name, env, limit, r.out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		r.code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	sc := bufio.NewScanner(strings.NewReader(r.out))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "R ") {
			continue
		}
		for _, f := range strings.Fields(line[2:]) {
			if k, v, ok := strings.Cut(f, "="); ok {
				r.kv[k] = v
			}
		}
	}
	if why := r.kv["skip"]; why != "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s on CI: %s", name, why)
		}
		t.Skipf("%s: %s", name, why)
	}
	return r
}

// ---- The child's side ----

// say prints one result line.
func say(format string, args ...any) { fmt.Printf("R "+format+"\n", args...) }

// fail ends the child with a message.
func fail(format string, args ...any) {
	fmt.Printf("FAIL "+format+"\n", args...)
	os.Exit(2)
}

var scenarios = map[string]func(){}

func runScenario(name string) {
	f := scenarios[name]
	if f == nil {
		fail("no scenario %q", name)
	}
	watchdog(90 * time.Second)
	f()
	os.Exit(0)
}

// watchdog ends a child that hangs, so the parent reports it rather than its
// own timeout.
func watchdog(d time.Duration) {
	time.AfterFunc(d, func() { fmt.Println("FAIL watchdog"); os.Exit(3) })
}

// The reply counter: replyToApplicationShouldTerminate: swizzled to count
// its calls, then call AppKit's. AppKit calls it itself once the delegate
// returned NSTerminateNow or NSTerminateCancel (terminate: answers through
// it), so what must be 0 is the calls made while the delegate method runs
// (early) and the framework's own (platform.RepliesSent) for a latched answer.
var replies, earlyReplies atomic.Int64

func swizzleReply() {
	lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		fail("libobjc: %v", err)
	}
	getMethod, _ := purego.Dlsym(lib, "class_getInstanceMethod")
	setImp, _ := purego.Dlsym(lib, "method_setImplementation")
	cls, sel := objc.Cls("NSApplication"), objc.Sel("replyToApplicationShouldTerminate:")
	m := objc.F.Call2(getMethod, uintptr(cls), uintptr(sel))
	if m == 0 {
		fail("no replyToApplicationShouldTerminate:")
	}
	var orig uintptr
	imp := ffi.NewCallback(func(c *ffi.CB) {
		replies.Add(1)
		if platform.QuitAsking() {
			earlyReplies.Add(1)
		}
		c.Frame().Call3(orig, c.Int(0), c.Int(1), c.Int(2))
	})
	orig = objc.F.Call2(setImp, m, imp)
}

// nsApp is NSApplication.sharedApplication.
func nsApp() objc.ID { return objc.Send(objc.Cls("NSApplication"), objc.Sel("sharedApplication")) }

// sendQuitEvent sends the Dock's Quit to this process: a kAEQuitApplication
// Apple event addressed to itself (handled inside the send). If the
// delegate was not asked (sending refused: an Automation grant), it
// dispatches the event to the handler NSApp installed instead.
func sendQuitEvent() string {
	n := quitAsked
	return sendAppleEvent(0x61657674, 0x71756974, nil, func() bool { return quitAsked == n }) // 'aevt' 'quit'
}

// quitAsked counts the quit requests the scenario's handler saw, and quitID
// is the last one's id (what ReplyQuit answers).
var (
	quitAsked int
	quitID    uint64
)

// sendAppleEvent sends an Apple event of class and id to this process, with
// fill adding its parameters; it reports how it was delivered. When the send
// fails and missed (nil: never) says the event was not handled, it is
// dispatched to the installed handler directly.
func sendAppleEvent(class, id uint32, fill func(ev objc.ID), missed func() bool) string {
	cls := objc.Cls("NSAppleEventDescriptor")
	selCur, selMake := objc.Sel("currentProcessDescriptor"), objc.Sel("appleEventWithEventClass:eventID:targetDescriptor:returnID:transactionID:")
	target := objc.Send(cls, selCur)
	ev := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(selMake)).Arg(uintptr(class)).Arg(uintptr(id)).
		Arg(uintptr(target)).Arg(^uintptr(0)).Arg(0).Call(objc.MsgSend).R0())
	if fill != nil {
		fill(ev)
	}
	selSend := objc.Sel("sendEventWithOptions:timeout:error:")
	errOut := new(uintptr)
	objc.F.Reset().Arg(uintptr(ev)).Arg(uintptr(selSend)).Arg(1 /* kAENoReply */).ArgF64(10).
		ArgPtr(unsafe.Pointer(errOut)).Call(objc.MsgSend)
	if *errOut == 0 || missed == nil || !missed() {
		return "send"
	}
	// Fallback: the handler NSApp installed, called directly.
	mgr := objc.Send(objc.Cls("NSAppleEventManager"), objc.Sel("sharedAppleEventManager"))
	desc := objc.Send(ev, objc.Sel("aeDesc"))
	rawReply := new([2]uintptr)
	objc.Send3(mgr, objc.Sel("dispatchRawAppleEvent:withRawReply:handlerRefCon:"), uintptr(desc), uintptr(unsafe.Pointer(rawReply)), 0)
	return "dispatch"
}

// later runs f on the main thread from a run-loop timer in the default mode,
// as the Dock's Apple event arrives: from the pump, outside the dispatcher
// (an Apple event sent to the process itself is handled inside the send).
func later(f func()) {
	laterFns = append(laterFns, f)
	if laterObj == 0 {
		d := objc.Define("GNUITestLater", objc.Cls("NSObject"), nil)
		d.Method(objc.Sel("gnuiLater:"), "v@:@", func(*ffi.CB) {
			fs := laterFns
			laterFns = nil
			for _, f := range fs {
				f()
			}
		})
		laterObj = objc.Send(objc.Send(d.Register(), objc.Sel("alloc")), objc.Sel("init"))
	}
	sel := objc.Sel("performSelector:withObject:afterDelay:")
	arg := objc.Sel("gnuiLater:")
	objc.F.Reset().Arg(uintptr(laterObj)).Arg(uintptr(sel)).Arg(uintptr(arg)).Arg(0).ArgF64(0).Call(objc.MsgSend)
}

var (
	laterObj objc.ID
	laterFns []func()
)

// laterAfter runs f on the main thread from a run-loop timer d from now, in
// the default mode (so also inside a nested run loop of that mode).
func laterAfter(d time.Duration, f func()) {
	if afterObj == 0 {
		def := objc.Define("GNUITestAfter", objc.Cls("NSObject"), nil)
		def.Method(objc.Sel("gnuiAfter:"), "v@:@", func(c *ffi.CB) {
			id := int64(objc.Send(objc.ID(c.Int(2)), objc.Sel("longLongValue")))
			f := afterFns[id]
			delete(afterFns, id)
			f()
		})
		afterObj = objc.Send(objc.Send(def.Register(), objc.Sel("alloc")), objc.Sel("init"))
		afterFns = map[int64]func(){}
	}
	afterSeq++
	afterFns[afterSeq] = f
	num := objc.Send1(objc.Cls("NSNumber"), objc.Sel("numberWithLongLong:"), uintptr(afterSeq))
	sel, arg := objc.Sel("performSelector:withObject:afterDelay:"), objc.Sel("gnuiAfter:") // not inside the frame's chain
	objc.F.Reset().Arg(uintptr(afterObj)).Arg(uintptr(sel)).Arg(uintptr(arg)).Arg(uintptr(num)).ArgF64(d.Seconds()).Call(objc.MsgSend)
}

var (
	afterObj objc.ID
	afterFns map[int64]func()
	afterSeq int64
)

// runNested runs a nested run loop in the default mode for d, as AppKit's
// own loops do inside a call the app makes (an animated zoom, a modal
// session).
func runNested(d time.Duration) {
	rl := objc.Send(objc.Cls("NSRunLoop"), objc.Sel("currentRunLoop"))
	mode := objc.Str("kCFRunLoopDefaultMode")
	cls, sel, run := objc.Cls("NSDate"), objc.Sel("dateWithTimeIntervalSinceNow:"), objc.Sel("runMode:beforeDate:")
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		date := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).ArgF64(0.02).Call(objc.MsgSend).R0())
		objc.Send2(rl, run, uintptr(mode), uintptr(date))
	}
}

// quitLater sends the Dock's Quit from the pump (later) and reports how; with
// VIA=terminate, a terminate: sent by someone else than the app instead (the
// route that asks the delegate's applicationShouldTerminate:).
func quitLater() {
	later(func() {
		if os.Getenv("VIA") == "terminate" {
			say("via=terminate")
			objc.Send1(nsApp(), objc.Sel("terminate:"), 0)
			return
		}
		say("via=%s", sendQuitEvent())
	})
}

// sendQuitWithReply sends the Dock's Quit (a quit Apple event without a
// reason) to this process and waits for its reply. An event sent to the
// process itself is handled inside the send, on the sending thread: the main
// thread here. It returns the reply's error number (0: none) or, when the
// send failed, sendErr (the NSError's code). A sender in another process is
// startQuitSender.
func sendQuitWithReply() (errn, sendErr int) {
	cls := objc.Cls("NSAppleEventDescriptor")
	target := objc.Send(cls, objc.Sel("currentProcessDescriptor"))
	selMake := objc.Sel("appleEventWithEventClass:eventID:targetDescriptor:returnID:transactionID:")
	ev := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(selMake)).Arg(fourCC("aevt")).Arg(fourCC("quit")).
		Arg(uintptr(target)).Arg(^uintptr(0)).Arg(0).Call(objc.MsgSend).R0())
	selSend := objc.Sel("sendEventWithOptions:timeout:error:")
	errOut := new(uintptr)
	reply := objc.ID(objc.F.Reset().Arg(uintptr(ev)).Arg(uintptr(selSend)).Arg(3 /* kAEWaitReply */).ArgF64(30).
		ArgPtr(unsafe.Pointer(errOut)).Call(objc.MsgSend).R0())
	if *errOut != 0 {
		return 0, int(int64(objc.Send(objc.ID(*errOut), objc.Sel("code"))))
	}
	if d := objc.Send1(reply, objc.Sel("paramDescriptorForKeyword:"), fourCC("errn")); d != 0 {
		errn = int(int32(uint32(objc.Send(d, objc.Sel("int32Value")))))
	}
	return errn, 0
}

// cmdQ is ⌘Q as the keyboard sends it, to window number win (0: none).
func cmdQ(win uintptr) objc.ID {
	s := objc.Str("q")
	cls := objc.Cls("NSEvent")
	sel := objc.Sel("keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:charactersIgnoringModifiers:isARepeat:keyCode:")
	ts := objc.SendF64(objc.Send(objc.Cls("NSProcessInfo"), objc.Sel("processInfo")), objc.Sel("systemUptime"))
	f := objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).Arg(10).ArgPoint(10, 10).Arg(1 << 20).ArgF64(ts).Arg(win).Arg(0).Arg(uintptr(s)).Arg(uintptr(s))
	const code = 0x0C
	f.Arg32(uint32(code) << 16) // isARepeat (NO) at byte 0, the keyCode at byte 2 of the packed stack
	return objc.ID(f.Call(objc.MsgSend).R0())
}

// pressCmdQ hands ⌘Q to the main menu's key equivalents at once, as AppKit
// does with the keyboard's: its Quit item acts before this returns.
func pressCmdQ(win uintptr) bool {
	menu := objc.Send(nsApp(), objc.Sel("mainMenu"))
	return objc.SendBool1(menu, objc.Sel("performKeyEquivalent:"), uintptr(cmdQ(win)))
}

// postCmdQ posts ⌘Q to window number win (0: no window), as the keyboard
// would.
func postCmdQ(win uintptr) {
	objc.Send2(nsApp(), objc.Sel("postEvent:atStart:"), uintptr(cmdQ(win)), 0)
}

// openPlainWindow opens a second, plain AppKit window (two windows of ours
// come with F4) and returns it.
func openPlainWindow() objc.ID {
	sel := objc.Sel("initWithContentRect:styleMask:backing:defer:")
	w := objc.Send(objc.Cls("NSWindow"), objc.Sel("alloc"))
	w = objc.ID(objc.F.Reset().Arg(uintptr(w)).Arg(uintptr(sel)).ArgRect(80, 80, 240, 160).Arg(1 | 2 | 4 | 8).Arg(2).ArgBool(false).Call(objc.MsgSend).R0())
	objc.Send1(w, objc.Sel("setReleasedWhenClosed:"), 0)
	objc.Send(w, objc.Sel("orderFrontRegardless"))
	return w
}

// visibleWindows counts NSApp's visible windows.
func visibleWindows() int {
	ws := objc.Send(nsApp(), objc.Sel("windows"))
	n, vis := int(objc.Send(ws, objc.Sel("count"))), 0
	for i := 0; i < n; i++ {
		if objc.SendBool(objc.Send1(ws, objc.Sel("objectAtIndex:"), uintptr(i)), objc.Sel("isVisible")) {
			vis++
		}
	}
	return vis
}

// view is a window's builder: a plain box; it counts its builds and may
// refuse to close.
type view struct {
	builds  atomic.Int64
	refuse  bool
	onClose func() bool
}

func (v *view) Build(c *ui.Context) {
	v.builds.Add(1)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Box(0, "bg")
}

func (v *view) CloseRequested(a *app.App) bool {
	if v.onClose != nil {
		return v.onClose()
	}
	return !v.refuse
}

var winCfg = app.WindowConfig{Title: "applive", Width: 320, Height: 200}

// openWindows opens n windows for a scenario: ours first, then plain ones.
// It reports a skip when no window can be opened.
func openWindows(a *app.App, n int, v *view) (*app.Win, bool) {
	if n == 0 {
		return nil, true
	}
	w, err := a.OpenWindow(winCfg, v)
	if err != nil {
		say("skip=no-window:%s", strings.ReplaceAll(err.Error(), " ", "_"))
		return nil, false
	}
	for i := 1; i < n; i++ {
		openPlainWindow()
	}
	return w, true
}

// ourWindow is the NSWindow of our window.
func ourWindow(a *app.App) objc.ID { return objc.ID(a.Win.Native().NSWindow) }

// postEvent posts an EvPost with id and data from a goroutine, retrying while
// the ring is full.
func postEvent(id uint64, data string) {
	for !app.Post(app.Event{ID: id}, []byte(data)) {
		time.Sleep(time.Millisecond)
	}
}

// after posts id once d has passed.
func after(d time.Duration, id uint64) { time.AfterFunc(d, func() { postEvent(id, "") }) }

// lastTerminate names the delegate's last answer.
func lastTerminate() string {
	switch platform.LastShouldTerminate() {
	case 0:
		return "cancel"
	case 1:
		return "now"
	case 2:
		return "later"
	}
	return "none"
}
