//go:build darwin

package platform

import (
	"bufio"
	"bytes"
	"image"
	"image/png"
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
	"weak"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// ---- a slow owner: another process that promises an image and takes its time ----

// The test binary run with GNUI_PB_SLOW is that other process: it puts a promised
// public.png on the general pasteboard (an NSPasteboardItemDataProvider that
// sleeps GNUI_PB_SLOW before it provides the data), says "ready", and serves
// requests on its main run loop for a while. init runs on the main thread,
// before TestMain.
func init() {
	if os.Getenv("GNUI_PB_SLOW") == "" {
		return
	}
	runSlowOwner()
	os.Exit(0)
}

func slowPNG() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 12, 8))
	rand.New(rand.NewSource(7)).Read(img.Pix)
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func runSlowOwner() {
	delay, _ := time.ParseDuration(os.Getenv("GNUI_PB_SLOW"))
	data := slowPNG()
	pbImageInit()
	cls := objc.Define("GNUISlowProvider", objc.Cls("NSObject"), []string{"NSPasteboardItemDataProvider"})
	cls.Method(objc.Sel("pasteboard:item:provideDataForType:"), "v@:@@@", func(c *ffi.CB) {
		time.Sleep(delay)
		fr := c.Frame()
		item, typ := c.Int(3), c.Int(4)
		d := fr.Reset().Arg(uintptr(clsPBNSData)).Arg(uintptr(selPBDataWithBytes)).ArgPtr(unsafe.Pointer(&data[0])).Arg(uintptr(len(data))).Call(objc.MsgSend).R0()
		fr.Call4(objc.MsgSend, item, uintptr(selPBSetDataFT), d, typ)
	})
	provider := objc.Send(cls.Register(), objc.Sel("new"))
	itemCls, selInit, selAlloc := objc.Cls("NSPasteboardItem"), objc.Sel("init"), objc.Sel("alloc")
	item := objc.Send(objc.Send(itemCls, selAlloc), selInit)
	clsArr, selArr := objc.Cls("NSArray"), objc.Sel("arrayWithObject:")
	types := objc.ID(objc.F.Reset().Arg(uintptr(clsArr)).Arg(uintptr(selArr)).Arg(uintptr(pbPNG)).Call(objc.MsgSend).R0())
	objc.Send2(item, objc.Sel("setDataProvider:forTypes:"), uintptr(provider), uintptr(types))
	items := objc.ID(objc.F.Reset().Arg(uintptr(clsArr)).Arg(uintptr(selArr)).Arg(uintptr(item)).Call(objc.MsgSend).R0())
	pb := objc.Send(clsPasteboard, selGeneralPB)
	objc.Send(pb, selClearContents)
	objc.SendBool1(pb, objc.Sel("writeObjects:"), uintptr(items))
	os.Stdout.WriteString("ready\n")
	loop, selRun, clsDate, selDate := objc.Send(objc.Cls("NSRunLoop"), objc.Sel("currentRunLoop")), objc.Sel("runUntilDate:"), objc.Cls("NSDate"), objc.Sel("dateWithTimeIntervalSinceNow:")
	date := objc.ID(objc.F.Reset().Arg(uintptr(clsDate)).Arg(uintptr(selDate)).ArgF64(25).Call(objc.MsgSend).R0())
	objc.Send1(loop, selRun, uintptr(date))
}

// A pasteboard whose owner answers in 1.5 s does not hold up the main loop: a
// real window's event loop (Window.Wait, on the process's main thread) goes on
// handling other events while the provider sleeps, and the paste comes out of
// Wait when the owner has the data.
func TestPasteboardSlowOwnerDoesNotBlock(t *testing.T) {
	newPasteRig(t)
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "GNUI_PB_SLOW=1500ms")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	ready := make(chan string, 1)
	go func() { l, _ := bufio.NewReader(out).ReadString('\n'); ready <- l }()
	select {
	case l := <-ready:
		if strings.TrimSpace(l) != "ready" {
			t.Skipf("the slow owner did not start: %q", l)
		}
	case <-time.After(20 * time.Second):
		t.Skip("the slow owner did not start")
	}

	var res struct {
		err      error
		request  time.Duration // how long RequestPasteKinds took
		took     time.Duration // until the paste came out of Wait
		wakes    int           // other events (EvWake, from a goroutine) handled before it
		maxGap   time.Duration // the longest time between two of them
		pastes   int
		ev       Event
		data     []byte
		timedOut bool
	}
	onMain(func() {
		w, err := Open(Options{Title: "paste", Width: 200, Height: 150, Hidden: true})
		if err != nil {
			res.err = err
			return
		}
		defer w.Close()
		w.Wait(0)
		stop := make(chan struct{})
		defer close(stop)
		go func() { // another source of events: the loop must handle them while the owner is slow
			for {
				select {
				case <-stop:
					return
				case <-time.After(20 * time.Millisecond):
					Wake()
				}
			}
		}()
		start := time.Now()
		w.RequestPasteKinds(1, WantImage)
		res.request = time.Since(start)
		last := start
		for res.pastes == 0 {
			if time.Since(start) > 20*time.Second {
				res.timedOut = true
				return
			}
			for _, e := range w.Wait(int64(500 * time.Millisecond)) {
				switch e.Kind {
				case EvWake:
					now := time.Now()
					res.wakes++
					res.maxGap = max(res.maxGap, now.Sub(last))
					last = now
				case EvPaste:
					res.pastes++
					res.ev = e
					res.data = bytes.Clone(w.q.textOf(e.Text))
				}
			}
		}
		res.took = time.Since(start)
	})
	switch {
	case res.err != nil:
		t.Skipf("no window: %v", res.err)
	case res.timedOut:
		t.Fatal("no paste came out of Wait")
	}
	if res.request > 150*time.Millisecond {
		t.Errorf("RequestPasteKinds blocked for %v while the owner was preparing the data", res.request)
	}
	if res.ev.Flags != 0 || res.ev.Paste != PasteImage || res.ev.Tag != 1 || !bytes.Equal(res.data, slowPNG()) {
		t.Fatalf("answer %+v, %d bytes", res.ev, len(res.data))
	}
	if res.took < 1200*time.Millisecond {
		t.Errorf("the image arrived after %v: the owner's delay was not on the path", res.took)
	}
	if res.wakes < 30 || res.maxGap > 150*time.Millisecond {
		t.Errorf("the main loop handled %d other events (longest gap %v) while the owner was slow; want it to keep running", res.wakes, res.maxGap)
	}
}

// ---- closing a window ----

// canaryWindow makes a window as the tests use them, and a weak pointer to see
// when it is collected.
func canaryWindow() weak.Pointer[Window] {
	w := &Window{q: newEventQueue()}
	return weak.Make(w)
}

func collected(wp weak.Pointer[Window]) bool {
	for i := 0; i < 100; i++ {
		runtime.GC()
		if wp.Value() == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// holdWorker makes the worker's next request block until the returned release is
// called, as a read of a slow owner does; started is signalled when it is inside.
func holdWorker(t *testing.T) (started <-chan struct{}, release func()) {
	st, rel := make(chan struct{}, 1), make(chan struct{})
	hook := func() {
		select {
		case st <- struct{}{}:
		default:
		}
		<-rel
	}
	pbServeHook.Store(&hook)
	var once sync.Once
	release = func() { once.Do(func() { pbServeHook.Store(nil); close(rel) }) }
	t.Cleanup(release)
	return st, release
}

func idle(t *testing.T) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); pastePending() && time.Now().Before(end); time.Sleep(time.Millisecond) {
	}
	if pastePending() {
		t.Fatal("the pasteboard worker did not go idle")
	}
}

// A window closed while its read is slow gets no answer, and the worker does not
// keep it alive: the request and the answer hold it weakly, and the records of a
// consumed request are cleared.
func TestPasteboardClosedWindowIsDroppedAndNotRetained(t *testing.T) {
	r := newPasteRig(t)
	SetClipboardText([]byte("hello"))

	// Closed during a slow read: the answer is dropped.
	started, release := holdWorker(t)
	r.w.q.reset()
	r.w.RequestPasteKinds(1, WantText)
	<-started
	forgetPaste(r.w) // what Window.Close does
	release()
	idle(t)
	r.w.reapPaste()
	if len(r.w.q.ev) != 0 {
		t.Errorf("the closed window got %+v", r.w.q.ev)
	}

	// ... and it does not hold up a newer request of another window, which gets its answer.
	started, release = holdWorker(t)
	r.w.RequestPasteKinds(2, WantText)
	<-started
	other := &Window{q: newEventQueue()}
	other.RequestPasteKinds(3, WantText) // replaces the running one's slot
	forgetPaste(r.w)
	release()
	awaitPaste(t, other, false)
	if len(other.q.ev) != 1 || other.q.ev[0].Tag != 3 || string(other.q.textOf(other.q.ev[0].Text)) != "hello" {
		t.Errorf("another window's answer after a close: %+v", other.q.ev)
	}

	// The window of a read still in progress can be collected.
	started, release = holdWorker(t)
	wp := func() weak.Pointer[Window] {
		w := &Window{q: newEventQueue()}
		w.RequestPasteKinds(4, WantText)
		<-started
		forgetPaste(w)
		return weak.Make(w)
	}()
	if !collected(wp) {
		t.Error("a window closed during a read is kept alive by the worker")
	}
	release()
	idle(t)

	// The window of a request whose answer was taken can be collected too.
	wp = func() weak.Pointer[Window] {
		w := &Window{q: newEventQueue()}
		w.RequestPasteKinds(5, WantText)
		awaitPaste(t, w, false)
		return weak.Make(w)
	}()
	if !collected(wp) {
		t.Error("a window whose paste was delivered is kept alive by the worker's records")
	}
	// ... as is one whose request was replaced before it ran.
	started, release = holdWorker(t)
	r.w.RequestPasteKinds(6, WantText)
	<-started
	wp = func() weak.Pointer[Window] {
		w := &Window{q: newEventQueue()}
		w.RequestPasteKinds(7, WantText) // waits behind the held one
		return weak.Make(w)
	}()
	r.w.RequestPasteKinds(8, WantText) // replaces the waiting one
	if !collected(wp) {
		t.Error("a window whose request was replaced is kept alive")
	}
	release()
	r.w.q.reset()
	awaitPaste(t, r.w, false)
	if len(r.w.q.ev) != 1 || r.w.q.ev[0].Tag != 8 {
		t.Errorf("after the replacements: %+v", r.w.q.ev)
	}
}

// ---- the owner changes during a read ----

// Items that answer like a pasteboard whose owner changes while they are read:
// the first asks for the change (changeCount moves), and for as long as the
// change is under way the others have nothing.
var (
	fakeItemOnce sync.Once
	fakeItemCls  objc.ID
	fakeCC       atomic.Uintptr
	fakeBumps    atomic.Int32 // changes still to make: the first item's next questions move changeCount
	fakeStale    atomic.Int32 // questions of the first item that find the pasteboard in the middle of a change
	fakeInChange atomic.Bool
	fakeFirst    atomic.Uintptr
	fakeItemURLs sync.Map // item -> its NSString
)

func newFakeFileItems(urls ...string) []uintptr {
	pbImageInit()
	fakeItemOnce.Do(func() {
		d := objc.Define("GNUIFakeItem", objc.Cls("NSPasteboardItem"), nil)
		d.Method(objc.Sel("stringForType:"), "@@:@", func(c *ffi.CB) {
			self := c.Int(0)
			if self == fakeFirst.Load() {
				fakeInChange.Store(false)
				if fakeStale.Load() > 0 {
					fakeStale.Add(-1)
					fakeInChange.Store(true)
					if fakeBumps.Load() > 0 {
						fakeBumps.Add(-1)
						fakeCC.Add(1)
					}
				}
			} else if fakeInChange.Load() {
				c.Ret(0)
				return
			}
			v, _ := fakeItemURLs.Load(uintptr(self))
			s, _ := v.(uintptr)
			c.Ret(s)
		})
		fakeItemCls = d.Register()
	})
	items := make([]uintptr, len(urls))
	for i, u := range urls {
		items[i] = uintptr(objc.Send(objc.Send(fakeItemCls, objc.Sel("alloc")), objc.Sel("init")))
		fakeItemURLs.Store(items[i], nsStringBytes([]byte(u)))
	}
	fakeFirst.Store(items[0])
	return items
}

// A new owner in the middle of the enumeration of the files must not leave a
// partial list: the read is made again against the new contents, once, and fails
// when they change again.
func TestPasteFilesOwnerChangesDuringRead(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pbImageInit()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	pb := newFakePB()
	items := newFakeFileItems("file:///tmp/a", "file:///tmp/b")
	fakeItems.Store(nsArray(items...))
	defer fakeBumps.Store(0)
	defer fakeStale.Store(0)
	rd := &pbReader{fr: new(ffi.Frame)}
	serve := func() pbOut { return rd.serve(&pbReq{pb: pb, kinds: WantFiles}) }

	// Steady: both.
	if o := serve(); o.flags != 0 || o.files != 2 || string(rd.buf[:o.n]) != "/tmp/a\x00/tmp/b\x00" {
		t.Fatalf("no change: %+v %q", o, rd.buf)
	}
	// One change, during the first pass: the second pass finds the new contents, whole.
	fakeBumps.Store(1)
	fakeStale.Store(2) // the measuring and the copying of the first attempt see a half-changed pasteboard
	if o := serve(); o.flags != 0 || o.files != 2 || string(rd.buf[:o.n]) != "/tmp/a\x00/tmp/b\x00" {
		t.Errorf("one change: %+v %q, want the complete list", o, rd.buf[:o.n])
	}
	// It keeps changing: a clean failure, not what half of it gave.
	fakeBumps.Store(1000)
	fakeStale.Store(1000)
	if o := serve(); o.flags != FlagFailed || o.n != 0 || len(rd.buf) != 0 {
		t.Errorf("a pasteboard that never settles: %+v %q", o, rd.buf)
	}
}

// ---- one worker, one conversion at a time ----

func TestPasteboardWorkerConvertsOneAtATime(t *testing.T) {
	r := newPasteRig(t)
	const w, h = 1500, 1000
	px := make([]byte, 3*w*h)
	rand.New(rand.NewSource(5)).Read(px)
	pbPutData("public.tiff", tiffRGB(w, h, px))
	var active, peak, started atomic.Int32
	hook := func(start bool) {
		if !start {
			active.Add(-1)
			return
		}
		started.Add(1)
		n := active.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
	}
	pbConvHook.Store(&hook)
	t.Cleanup(func() { pbConvHook.Store(nil) })
	r.w.q.reset()
	const requests = 12
	for i := 0; i < requests; i++ {
		r.w.RequestPasteKinds(uint64(i+1), WantImage) // each replaces the one before
		time.Sleep(3 * time.Millisecond)
	}
	e := waitConverted(t, r.w)
	for pastePending() {
		time.Sleep(5 * time.Millisecond)
		r.w.reapPaste()
	}
	if e.Tag != requests || e.Flags != 0 {
		t.Errorf("answer %+v, want the last request's", e)
	}
	if peak.Load() != 1 {
		t.Errorf("%d conversions ran at once, want 1", peak.Load())
	}
	if started.Load() >= requests {
		t.Errorf("%d conversions started for %d replaced requests: the replaced ones were not dropped", started.Load(), requests)
	}
	var n int
	for _, e := range r.w.q.ev {
		if e.Kind == EvPaste {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d answers, want only the last request's", n)
	}
}

// ---- hostile and large pasteboard contents ----

func nsStringBytes(b []byte) uintptr {
	if len(b) == 0 {
		b = []byte{0}[:0]
	}
	p := unsafe.Pointer(unsafe.SliceData(append(b, 0)))
	cls, selA, selI := objc.Cls("NSString"), objc.Sel("alloc"), objc.Sel("initWithBytes:length:encoding:")
	alloc := objc.Send(cls, selA)
	s := objc.F.Reset().Arg(uintptr(alloc)).Arg(uintptr(selI)).ArgPtr(p).Arg(uintptr(len(b))).Arg(4).Call(objc.MsgSend).R0()
	runtime.KeepAlive(b)
	return s
}

// pbPutList replaces the pasteboard with the property list of typ.
func pbPutList(typ string, objs ...uintptr) {
	pbImageInit()
	clsArr, selArr, selSet, tstr := objc.Cls("NSArray"), objc.Sel("arrayWithObjects:count:"), objc.Sel("setPropertyList:forType:"), objc.Str(typ)
	var p unsafe.Pointer
	if len(objs) > 0 {
		p = unsafe.Pointer(&objs[0])
	}
	arr := objc.ID(objc.F.Reset().Arg(uintptr(clsArr)).Arg(uintptr(selArr)).ArgPtr(p).Arg(uintptr(len(objs))).Call(objc.MsgSend).R0())
	pb := objc.Send(clsPasteboard, selGeneralPB)
	objc.Send(pb, selClearContents)
	objc.Send2(pb, selSet, uintptr(arr), uintptr(tstr))
}

// pbPutFileURLs writes one pasteboard item per string, each with the string as its
// public.file-url (whatever it holds).
func pbPutFileURLStrings(t *testing.T, urls ...string) {
	t.Helper()
	pbImageInit()
	clsItem, selAlloc, selInit, selSetStr, selArr, selWrite := objc.Cls("NSPasteboardItem"), objc.Sel("alloc"), objc.Sel("init"), objc.Sel("setString:forType:"), objc.Sel("arrayWithObjects:count:"), objc.Sel("writeObjects:")
	clsArr := objc.Cls("NSArray")
	items := make([]uintptr, len(urls))
	for i, u := range urls {
		item := objc.Send(objc.Send(clsItem, selAlloc), selInit)
		objc.Send2(item, selSetStr, nsStringBytes([]byte(u)), uintptr(pbFileURL))
		items[i] = uintptr(item)
	}
	arr := objc.ID(objc.F.Reset().Arg(uintptr(clsArr)).Arg(uintptr(selArr)).ArgPtr(unsafe.Pointer(&items[0])).Arg(uintptr(len(items))).Call(objc.MsgSend).R0())
	pb := objc.Send(clsPasteboard, selGeneralPB)
	objc.Send(pb, selClearContents)
	if !objc.SendBool1(pb, selWrite, uintptr(arr)) {
		t.Skip("the pasteboard cannot take these items here")
	}
}

// fakePB is a pasteboard (to the readers, which only message it) whose answers
// the test chooses: a hostile owner can put anything in a property list.
var (
	fakePBOnce  sync.Once
	fakePBClass objc.ID
	fakeItems   atomic.Uintptr // what -pasteboardItems returns
	fakeList    atomic.Uintptr // what -propertyListForType: returns
)

func newFakePB() uintptr {
	pbImageInit()
	fakePBOnce.Do(func() {
		d := objc.Define("GNUIFakePasteboard", objc.Cls("NSObject"), nil)
		d.Method(objc.Sel("pasteboardItems"), "@@:", func(c *ffi.CB) { c.Ret(fakeItems.Load()) })
		d.Method(objc.Sel("changeCount"), "q@:", func(c *ffi.CB) { c.Ret(fakeCC.Load()) })
		d.Method(objc.Sel("propertyListForType:"), "@@:@", func(c *ffi.CB) { c.Ret(fakeList.Load()) })
		d.Method(objc.Sel("dataForType:"), "@@:@", func(c *ffi.CB) { c.Ret(0) })
		d.Method(objc.Sel("stringForType:"), "@@:@", func(c *ffi.CB) { c.Ret(0) })
		fakePBClass = d.Register()
	})
	return uintptr(objc.Send(objc.Send(fakePBClass, objc.Sel("alloc")), objc.Sel("init")))
}

func nsArray(objs ...uintptr) uintptr {
	clsArr, selArr := objc.Cls("NSArray"), objc.Sel("arrayWithObjects:count:")
	var p unsafe.Pointer
	if len(objs) > 0 {
		p = unsafe.Pointer(&objs[0])
	}
	return uintptr(objc.F.Reset().Arg(uintptr(clsArr)).Arg(uintptr(selArr)).ArgPtr(p).Arg(uintptr(len(objs))).Call(objc.MsgSend).R0())
}

func nsNumber(n int) uintptr {
	cls, sel := objc.Cls("NSNumber"), objc.Sel("numberWithInt:")
	return uintptr(objc.Send1(cls, sel, uintptr(n)))
}

func nsDict() uintptr {
	cls, sel := objc.Cls("NSDictionary"), objc.Sel("dictionary")
	return uintptr(objc.Send(cls, sel))
}

func fileURLItem(u string) uintptr {
	clsItem, selAlloc, selInit, selSetStr := objc.Cls("NSPasteboardItem"), objc.Sel("alloc"), objc.Sel("init"), objc.Sel("setString:forType:")
	item := objc.Send(objc.Send(clsItem, selAlloc), selInit)
	objc.Send2(item, selSetStr, nsStringBytes([]byte(u)), uintptr(pbFileURL))
	return uintptr(item)
}

// Malformed values where the readers expect lists and strings: elements that are
// not strings, items that are not pasteboard items, a value that is not a list.
// A message such an object does not understand raises an Objective-C
// exception, which would end the process. (NSPasteboard itself refuses to
// write most of these, so the readers are run against a pasteboard of the
// test's making.)
func TestPasteFilesFromMalformedPasteboards(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pbImageInit()
	fr := new(ffi.Frame)
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	pb := newFakePB()
	run := func() (string, int, pbResult) {
		out, n, res := pbAppendFiles(fr, pb, []byte("keep"))
		return string(out), n, res
	}

	// Legacy lists.
	fakeItems.Store(0)
	good, nul := nsStringBytes([]byte("/tmp/good")), nsStringBytes([]byte("/tmp/a\x00b"))
	fakeList.Store(nsArray(nsNumber(7), good, nsDict(), nsStringBytes(nil), nul, nsNumber(1)))
	if out, n, res := run(); res != pbFound || n != 1 || out != "keep/tmp/good\x00" {
		t.Errorf("a list with numbers, a dictionary, an empty string and a NUL path: %q %d %v", out, n, res)
	}
	for name, v := range map[string]uintptr{"a string": good, "a number": nsNumber(3), "a dictionary": nsDict(), "an empty list": nsArray(), "nothing": 0} {
		fakeList.Store(v)
		if out, n, res := run(); res != pbAbsent || n != 0 || out != "keep" {
			t.Errorf("%s as the list: %q %d %v", name, out, n, res)
		}
	}

	// Items.
	fakeList.Store(0)
	fakeItems.Store(nsArray(nsNumber(1), good, nsDict(), fileURLItem("file:///tmp/ok")))
	if out, n, res := run(); res != pbFound || n != 1 || out != "keep/tmp/ok\x00" {
		t.Errorf("items of other classes: %q %d %v", out, n, res)
	}
	for name, v := range map[string]uintptr{"a string": good, "a number": nsNumber(3), "a dictionary": nsDict()} {
		fakeItems.Store(v)
		if out, n, res := run(); res != pbAbsent || n != 0 || out != "keep" {
			t.Errorf("%s as the items: %q %d %v", name, out, n, res)
		}
	}
}

// Only file URLs for this machine are paths, and a path never has a NUL.
func TestPasteFilesValidatesURLs(t *testing.T) {
	r := newPasteRig(t)
	pbPutFileURLStrings(t,
		"http://example.com/x.png",
		"file://otherhost/share/x",
		"file://localhost/tmp/local%20one",
		"mailto:a@b.c",
		"file:///tmp/plain",
		"not a url at all",
	)
	e := r.request(1, WantFiles)
	got := PasteFilePaths(r.w.q.textOf(e.Text))
	// (NSURL's treatment of a %00 differs between macOS versions; a NUL inside a path is tested with the legacy lists, above.)
	if e.Flags != 0 || len(got) != 2 || got[0] != "/tmp/local one" || got[1] != "/tmp/plain" {
		t.Errorf("paths %q (flags %v)", got, e.Flags)
	}
}

// A file list over MaxPaste is measured as it is read: the entry that would
// pass the limit is not copied, and the buffer does not grow past it.
func TestPasteFilesOverMaxIsMeasuredBeforeCopying(t *testing.T) {
	r := newPasteRig(t)
	pbImageInit()
	chunk := bytes.Repeat([]byte("p"), 1<<20)
	big := nsStringBytes(chunk)
	objs := make([]uintptr, 17)
	for i := range objs {
		objs[i] = big
	}
	pbPutList("NSFilenamesPboardType", objs...)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	e := r.request(1, WantFiles)
	runtime.ReadMemStats(&after)
	if e.Flags != FlagFailed|FlagTooBig || len(r.w.q.text) != 0 {
		t.Errorf("%+v, %d bytes kept", e, len(r.w.q.text))
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 17<<20+(1<<20) {
		t.Errorf("a refused list allocated %d bytes", grown)
	}
	if c := cap(pasteboardReader().buf); c > MaxPaste {
		t.Errorf("the worker keeps a %d-byte buffer after a refusal", c)
	}
	// 15 one-megabyte paths fit.
	pbPutList("NSFilenamesPboardType", objs[:15]...)
	e = r.request(2, WantFiles)
	if e.Flags != 0 || e.Code != 15 || int(e.Text.Len) != 15*(1<<20+1) {
		t.Errorf("15 paths: %+v", e)
	}
	if c := cap(pasteboardReader().buf); c > MaxPaste+1 {
		t.Errorf("buffer of %d bytes for %d", c, e.Text.Len)
	}
}

// pbPutTypes puts several types on the pasteboard at once, like a program that
// offers an image and its text.
func pbPutTypes(types map[string][]byte) {
	pbImageInit()
	pb := objc.Send(clsPasteboard, selGeneralPB)
	objc.Send(pb, selClearContents)
	for typ, b := range types {
		tstr := objc.Str(typ)
		p := unsafe.Pointer(unsafe.SliceData(append(b, 0)))
		d := objc.ID(objc.F.Reset().Arg(uintptr(clsPBNSData)).Arg(uintptr(selPBDataWithBytes)).ArgPtr(p).Arg(uintptr(len(b))).Call(objc.MsgSend).R0())
		objc.Send2(pb, selPBSetDataFT, uintptr(d), uintptr(tstr))
	}
}

// An image that is too big is passed over for the text the clipboard also has.
func TestPasteOversizedImageFallsBackToText(t *testing.T) {
	r := newPasteRig(t)
	big := make([]byte, MaxPaste+1<<20)
	copy(big, pngSig[:])
	pbPutTypes(map[string][]byte{"public.png": big, "public.utf8-plain-text": []byte("alt text")})
	e := r.request(1, WantImage|WantText)
	if e.Flags != 0 || e.Paste != PasteText || string(r.w.q.textOf(e.Text)) != "alt text" {
		t.Errorf("png over MaxPaste with text: %+v %q", e, r.w.q.textOf(e.Text))
	}
	if e := r.request(2, WantImage); e.Flags != FlagFailed|FlagTooBig {
		t.Errorf("image only: %+v", e)
	}
	// A TIFF that makes a PNG over MaxPaste, too.
	const w, h = 2400, 3000
	px := make([]byte, 3*w*h)
	rand.New(rand.NewSource(3)).Read(px)
	pbPutTypes(map[string][]byte{"public.tiff": tiffRGB(w, h, px), "public.utf8-plain-text": []byte("alt text")})
	e = r.request(3, WantImage|WantText)
	if e.Flags != 0 || e.Paste != PasteText || string(r.w.q.textOf(e.Text)) != "alt text" {
		t.Errorf("a TIFF whose PNG is over MaxPaste, with text: %+v", e)
	}
}
