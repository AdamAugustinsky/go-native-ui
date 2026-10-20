//go:build darwin

package notify

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// The real delegate (GNUINotificationDelegate), called the way the system
// calls it, with stand-ins for the system's response: a test binary is not a
// bundle, so the notification center itself cannot be used here (the live
// test, live_darwin_test.go, does that). The stand-in answers the messages
// the delegate sends (notification, request, content, userInfo,
// actionIdentifier, userText), and the completion handler is a block that
// records what was stored when it was called.

var mainQ = make(chan func())

func TestMain(m *testing.M) {
	done := make(chan int, 1)
	go func() { done <- m.Run() }()
	for {
		select {
		case f := <-mainQ:
			f()
		case code := <-done:
			os.Exit(code)
		}
	}
}

// onMain runs f on the main thread and waits for it.
func onMain(f func()) {
	d := make(chan struct{})
	mainQ <- func() { defer close(d); f() }
	<-d
}

// A stand-in response: the user info and the strings it returns are set by
// the test before the call.
var stand struct {
	once                sync.Once
	text, plain         uintptr // instances: with userText, and without (a click)
	info, action, reply atomic.Uintptr

	acks, ackStored atomic.Uint64 // completion handler calls, and slots.stored when it ran
	ack             uintptr       // the completion handler (a global block)
	presentOpts     atomic.Int64
	present         uintptr

	gcdAsync, gcdQueue, gcdCB uintptr
	gcdDone                   chan struct{}
	gcdResp                   atomic.Uintptr
}

func standIns(t *testing.T) {
	t.Helper()
	onMain(register)
	stand.once.Do(func() {
		onMain(func() {
			self := func(c *ffi.CB) { c.Ret(c.Int(0)) }
			def := func(name string, text bool) uintptr {
				d := objc.Define(name, objc.Cls("NSObject"), nil)
				for _, s := range []string{"notification", "request", "content"} {
					d.Method(objc.Sel(s), "@@:", self)
				}
				d.Method(objc.Sel("userInfo"), "@@:", func(c *ffi.CB) { c.Ret(stand.info.Load()) })
				d.Method(objc.Sel("actionIdentifier"), "@@:", func(c *ffi.CB) { c.Ret(stand.action.Load()) })
				if text {
					d.Method(objc.Sel("userText"), "@@:", func(c *ffi.CB) { c.Ret(stand.reply.Load()) })
				}
				cls := d.Register()
				return uintptr(objc.Send(objc.Send(cls, objc.Sel("alloc")), objc.Sel("init")))
			}
			stand.text = def("GNUITestTextResponse", true)
			stand.plain = def("GNUITestResponse", false)
			stand.ack = uintptr(objc.NewGlobalBlock("v8@?0", func(*ffi.CB) {
				stand.ackStored.Store(slots.stored.Load())
				stand.acks.Add(1)
			}))
			stand.present = uintptr(objc.NewGlobalBlock("v16@?0Q8", func(c *ffi.CB) { stand.presentOpts.Store(int64(c.Int(1))) }))
		})
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
		if err != nil {
			t.Fatal(err)
		}
		async, _ := purego.Dlsym(lib, "dispatch_async_f")
		global, _ := purego.Dlsym(lib, "dispatch_get_global_queue")
		stand.gcdAsync = async
		stand.gcdQueue = new(ffi.Frame).Call2(global, 0x19 /* QOS_CLASS_USER_INITIATED */, 0)
		stand.gcdDone = make(chan struct{}, 1)
		stand.gcdCB = ffi.NewCallback(func(c *ffi.CB) { // on a GCD thread: the call with its own frame
			fr := c.Frame()
			fr.Reset().Arg(un.delegate).Arg(uintptr(sel.didReceive)).Arg(0).Arg(stand.gcdResp.Load()).Arg(stand.ack).Call(objc.MsgSend)
			stand.gcdDone <- struct{}{}
		})
	})
}

// info makes an NSDictionary (kept) like a note's user info: the response
// key and the UserData.
func info(key uint64, data []byte) uintptr {
	var d uintptr
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		m := objc.Send(objc.Cls("NSMutableDictionary"), objc.Sel("dictionary"))
		if key != 0 {
			objc.Send2(m, objc.Sel("setObject:forKey:"), uintptr(objc.Send1(objc.Cls("NSNumber"), objc.Sel("numberWithUnsignedLongLong:"), uintptr(key))), un.keyK)
		}
		if len(data) > 0 {
			p := bytes.Clone(data)
			nd := objc.Send2(objc.Cls("NSData"), objc.Sel("dataWithBytes:length:"), uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)))
			objc.Send2(m, objc.Sel("setObject:forKey:"), uintptr(nd), un.keyD)
			runtime.KeepAlive(p)
		}
		d = uintptr(objc.Retain(m))
	})
	return d
}

func nsString(s string) uintptr {
	var r uintptr
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		r = uintptr(objc.Retain(objc.Str(s)))
		objc.F.Call1(objc.PoolPop, pool)
	})
	return r
}

// The delegate stores a response in its slot before it calls the system's
// completion handler, on the main thread, on a GCD thread and on another
// thread of the program; the text, the action and the UserData arrive.
func TestNativeDelegateStoresBeforeAnswering(t *testing.T) {
	f, s := fresh(t, Options{})
	standIns(t)
	if err := RegisterCategories(chat); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("ã", MaxReply) // twice MaxReply in bytes: cut
	replies := []string{"see you at 1", "ok 🙂", long}
	var keys []uint64
	var uds [][]byte
	for i := range replies {
		ud := []byte{'u', byte('0' + i)}
		r := mustPost(t, Note{Title: "Ana", Category: "msg", UserData: ud})
		if r.Reservation != Reserved {
			t.Fatal(r)
		}
		keys, uds = append(keys, f.lastAdd().key), append(uds, ud)
	}
	drain()
	s.reset()
	stand.action.Store(nsString(actionPrefix + "1"))
	calls := []struct {
		name string
		call func()
	}{
		{"main thread", func() {
			onMain(func() { objc.Send3(objc.ID(un.delegate), sel.didReceive, 0, stand.text, stand.ack) })
		}},
		{"GCD thread", func() {
			stand.gcdResp.Store(stand.text)
			new(ffi.Frame).Call3(stand.gcdAsync, stand.gcdQueue, 0, stand.gcdCB)
			<-stand.gcdDone
		}},
		{"other thread", func() {
			fr := cf.Acquire()
			fr.Reset().Arg(un.delegate).Arg(uintptr(sel.didReceive)).Arg(0).Arg(stand.text).Arg(stand.ack).Call(objc.MsgSend)
			cf.Release(fr)
		}},
	}
	for i, c := range calls {
		stand.info.Store(info(keys[i], uds[i]))
		stand.reply.Store(nsString(replies[i]))
		stored, acks := slots.stored.Load(), stand.acks.Load()
		c.call()
		if stand.acks.Load() != acks+1 || stand.ackStored.Load() != stored+1 {
			t.Fatalf("%s: handler called %d times, with %d responses stored (want 1, %d)", c.name, stand.acks.Load()-acks, stand.ackStored.Load(), stored+1)
		}
	}
	drain()
	got := s.responses()
	if len(got) != len(replies) {
		t.Fatalf("%d responses delivered", len(got))
	}
	for i, r := range got {
		want := replies[i]
		if len(want) > MaxReply {
			want = want[:MaxReply]
		}
		if r.Action != 1 || string(r.Reply) != want || !bytes.Equal(r.UserData, uds[i]) {
			t.Fatalf("%s: delivered %+v (reply %d bytes, want %d)", calls[i].name, r.Action, len(r.Reply), len(want))
		}
	}

	// A click (no userText), and the dismissal: no reply.
	mustPost(t, Note{Title: "x", UserData: []byte("click")})
	clickKey := f.lastAdd().key
	stand.info.Store(info(clickKey, []byte("click")))
	stand.action.Store(nsString(defaultActionID))
	s.reset()
	onMain(func() { objc.Send3(objc.ID(un.delegate), sel.didReceive, 0, stand.plain, stand.ack) })
	drain()
	if got := s.responses(); len(got) != 1 || got[0].Action != ActionClick || got[0].Reply != nil || string(got[0].UserData) != "click" {
		t.Fatalf("a click: %+v", got)
	}

	// A plain note (no key in its user info): answered, nothing delivered.
	stand.info.Store(info(0, nil))
	s.reset()
	plain, acks := slots.plain.Load(), stand.acks.Load()
	onMain(func() { objc.Send3(objc.ID(un.delegate), sel.didReceive, 0, stand.plain, stand.ack) })
	drain()
	if len(s.responses()) != 0 || slots.plain.Load() != plain+1 || stand.acks.Load() != acks+1 {
		t.Fatal("a plain note's click was delivered or not answered")
	}
}

// Shown in the foreground only when asked: the options the delegate gives
// the system's handler.
func TestNativeWillPresent(t *testing.T) {
	fresh(t, Options{})
	standIns(t)
	for _, c := range []struct {
		fg   bool
		want int64
	}{{true, presentBanner | presentList | presentSound | presentBadge}, {false, 0}} {
		Foreground(c.fg)
		stand.presentOpts.Store(-1)
		onMain(func() { objc.Send3(objc.ID(un.delegate), sel.willPresent, 0, 0, stand.present) })
		if got := stand.presentOpts.Load(); got != c.want {
			t.Fatalf("Foreground(%v): options %d, want %d", c.fg, got, c.want)
		}
	}
}

// The delegate's path allocates nothing once warm: reading the response,
// storing it, answering, and the delivery.
func TestNativeDelegateAllocs(t *testing.T) {
	f, s := fresh(t, Options{})
	standIns(t)
	const n = 64
	infos := make([]uintptr, n)
	for i := range infos {
		mustPost(t, Note{Title: "x", UserData: []byte("ud")})
		infos[i] = info(f.lastAdd().key, []byte("ud"))
		if i%16 == 15 {
			drain()
		}
	}
	drain()
	s.evs = make([]platform.AppEvent, 0, 2*n)
	s.offs = make([]int, 0, 2*n)
	s.data = make([]byte, 0, 1<<16)
	s.nResp = 0
	stand.action.Store(nsString(actionPrefix + "2"))
	stand.reply.Store(nsString(strings.Repeat("réponse ", 30)))
	var allocs float64
	onMain(func() {
		i := 0
		allocs = testing.AllocsPerRun(n-2, func() {
			stand.info.Store(infos[i])
			i++
			objc.Send3(objc.ID(un.delegate), sel.didReceive, 0, stand.text, stand.ack)
			platform.DispatchApp()
		})
	})
	if allocs != 0 {
		t.Fatalf("%v allocations per response", allocs)
	}
	if s.nResp != n-1 {
		t.Fatalf("%d responses delivered of %d", s.nResp, n-1)
	}
	drain()
}
