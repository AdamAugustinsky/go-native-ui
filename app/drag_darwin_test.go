//go:build darwin

package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The whole drag-in path through a real window: the app's view is registered
// for the kinds the frame's target takes, the system's questions are answered
// from the frame's layout, and what the system sends reaches the target's
// signals. The "system" is a fake NSDraggingInfo with a real, private
// NSPasteboard, sent to the window's real view: a real drag needs a pointer.
// It runs in a child process (nested_darwin_test.go), where the main thread is
// the test's.

func TestDragInThroughARealWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a window")
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), nestedChild+"=dragin")
	out, err := cmd.CombinedOutput()
	s := string(out)
	if i := strings.Index(s, "SKIP:"); i >= 0 {
		t.Skip(strings.TrimSpace(s[i+5:]))
	}
	if err != nil || !strings.Contains(s, "dragin: ok") {
		if len(s) > 3000 {
			s = s[:3000]
		}
		t.Fatalf("the child failed (%v):\n%s", err, s)
	}
	t.Log(s[strings.Index(s, "dragin: ok"):])
}

// dragInBuilder has one target (60,60)-(260,160) that takes text and files.
type dragInBuilder struct {
	a          *App
	over, drop int // frames in which the target was Over / Dropped
	op         ui.DropOp
	pos        [2]float32
	text       string
	frames     int
}

func (b *dragInBuilder) Init(a *App) error { b.a = a; return nil }

func (b *dragInBuilder) Build(c *ui.Context) {
	b.frames++
	c.NextPos(60, 60)
	c.NextSize(ui.Px(200), ui.Px(100))
	id := c.Box(ui.FloatingX|ui.FloatingY, "target")
	c.AcceptDrop(id, ui.DropSpec{Kinds: ui.DropText | ui.DropFiles, Ops: ui.DropCopy | ui.DropLink})
	if s := c.DropSignal(id); s.Over {
		b.over++
		b.op, b.pos = s.Op, s.Pos
	} else if s.Dropped {
		b.drop++
		b.text = c.Drop().Text()
	}
}

var fake struct {
	pb   objc.ID
	x, y float64
	mask uintptr
}

func runDragInChild() {
	b := &dragInBuilder{}
	var st StartupReport
	a, err := openWindowed(Config{Title: "dragin", Width: 400, Height: 300}, b, &st, false)
	if err != nil {
		fmt.Println("SKIP: cannot open a window here:", err)
		return
	}
	defer a.close()

	def := objc.Define("GNUIAppFakeDragInfo", objc.Cls("NSObject"), nil)
	def.Method(objc.Sel("draggingLocation"), "{CGPoint=dd}@:", func(c *ffi.CB) { c.RetF64(0, fake.x); c.RetF64(1, fake.y) })
	def.Method(objc.Sel("draggingPasteboard"), "@@:", func(c *ffi.CB) { c.Ret(uintptr(fake.pb)) })
	def.Method(objc.Sel("draggingSourceOperationMask"), "Q@:", func(c *ffi.CB) { c.Ret(fake.mask) })
	def.Method(objc.Sel("draggingSequenceNumber"), "q@:", func(c *ffi.CB) { c.Ret(7) })
	info := objc.Send(objc.Send(def.Register(), objc.Sel("alloc")), objc.Sel("init"))
	pbCls := objc.Cls("NSPasteboard")
	fake.pb = objc.Retain(objc.Send(pbCls, objc.Sel("pasteboardWithUniqueName")))
	fake.mask = 1 | 2 // copy and link
	objc.Send(fake.pb, objc.Sel("clearContents"))
	objc.Send2(fake.pb, objc.Sel("setString:forType:"), uintptr(objc.Str("from another app")), uintptr(objc.Str("public.utf8-plain-text")))
	view := objc.ID(a.Win.Native().NSView)
	entered, exited, performed, updated := objc.Sel("draggingEntered:"), objc.Sel("draggingExited:"), objc.Sel("performDragOperation:"), objc.Sel("draggingUpdated:")
	registered, count := objc.Sel("registeredDraggedTypes"), objc.Sel("count")

	var res struct {
		registeredBefore, registeredAfter int
		enterOp, outsideOp                uintptr
		accepted                          bool
		step                              int
		at                                int64
	}
	// Positions are given in pixels from the top left, as the app sees them.
	at := func(x, y float64) {
		s := float64(a.scale)
		fake.x, fake.y = x/s, float64(a.h)/s-y/s
	}
	res.registeredBefore = int(objc.Send(objc.Send(view, registered), count))

	a.hook = func() {
		if time.Now().UnixNano()-res.at < 80e6 && res.step > 0 { // let a frame or two run between steps
			return
		}
		res.at = time.Now().UnixNano()
		switch res.step {
		case 0: // after the first frames the window has registered for the target's kinds
			if b.frames < 3 {
				return
			}
			res.registeredAfter = int(objc.Send(objc.Send(view, registered), count))
			at(100, 100)
			res.enterOp = uintptr(objc.Send1(view, entered, uintptr(info)))
			at(350, 250) // outside the target
			res.outsideOp = uintptr(objc.Send1(view, updated, uintptr(info)))
			at(100, 100)
			objc.Send1(view, updated, uintptr(info))
		case 1: // the frame after has seen the hover
			if b.over == 0 {
				return
			}
			res.accepted = objc.SendBool1(view, performed, uintptr(info))
		case 2:
			if b.drop == 0 {
				return
			}
			objc.Send1(view, exited, uintptr(info))
			a.done = true
		}
		res.step++
	}
	// The hook runs once per turn of the loop, and an idle loop does not turn.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for tick := time.NewTicker(30 * time.Millisecond); ; {
			select {
			case <-tick.C:
				platform.Wake()
			case <-stop:
				return
			}
		}
	}()
	time.AfterFunc(8*time.Second, func() { a.done = true; platform.Wake() })
	if err := a.loop(); err != nil {
		fmt.Println("loop:", err)
	}
	ok := res.registeredBefore >= 2 && res.registeredAfter == res.registeredBefore /* the first frame registered the kinds of its target: files (the file URL and the promise types) and text (plain, link, RTF, HTML) */ && res.enterOp == 1 && res.outsideOp == 0 && res.accepted &&
		b.over > 0 && b.op == ui.DropCopy && b.pos == [2]float32{40, 40} && b.drop == 1 && b.text == "from another app" && res.step == 3
	if ok {
		fmt.Printf("dragin: ok, registered types %d -> %d, hover frames %d, drop frames %d\n", res.registeredBefore, res.registeredAfter, b.over, b.drop)
	} else {
		fmt.Printf("dragin: FAILED %+v over=%d op=%v pos=%v drop=%d text=%q\n", res, b.over, b.op, b.pos, b.drop, b.text)
	}
}
