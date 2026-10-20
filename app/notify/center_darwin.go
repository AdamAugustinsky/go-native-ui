//go:build darwin

package notify

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// macOS: UNUserNotificationCenter.
//
// The delegate (GNUINotificationDelegate) is set by Setup and never released
// (the center keeps it weakly). The system calls it on a thread of its own;
// its methods use their callback's frame (D36), no lock and no Go
// allocation: they read the response, store it in its reserved slot
// (respond), and only then call the completion handler the system gave them,
// before returning. Only a response that must wait for a free record keeps
// the handler (objc.KeepBlock), to call it once the response is stored. The
// delegate also observes the application becoming active (a
// reconciliation).
//
// Requests (permission, settings, posting, listing) are made from any
// goroutine through internal/cf (a frame and an autorelease pool of their
// own). Their completion handlers are per-request blocks (objc.BlockKind)
// whose context is a ticket or a pending entry; each is called once, on a
// thread of the framework's.
//
// The center exists only for a bundled app: +currentNotificationCenter
// raises an Objective-C exception (which would end the process) in a bare
// executable, so it is never sent unless the main bundle is a .app with an
// identifier (available).

type nativeCenter struct{}

func (nativeCenter) supported() bool { return true }

// launched: the app object has started, and so finished launching
// (platform.StartApp sets its delegate just before finishLaunching, on the
// main thread, which Setup runs on too).
func (nativeCenter) launched() bool { return platform.AppDelegate() != 0 }

// The user info keys of a note that wants a response.
const (
	keyKName = "gnui.k" // NSNumber: the response key
	keyDName = "gnui.d" // NSData: the UserData
)

var un struct {
	availOnce sync.Once
	avail     bool

	regOnce, observeOnce sync.Once
	center               uintptr // retained

	delegate   uintptr
	keyK, keyD uintptr // NSString, kept

	clsCenter, clsContent, clsRequest, clsSound, clsCategory, clsAction, clsTextAction uintptr
	clsAttachment, clsArray, clsSet, clsNumber, clsData                                uintptr

	auth, settings, add, list, listPending *objc.BlockKind

	trace      bool    // GNUI_NOTIFY_TRACE: report each call from the system and its thread
	isMainFunc uintptr // pthread_main_np
}

// traced reports a call from the system on stderr when GNUI_NOTIFY_TRACE is
// set: what it was and whether it came on the main thread (the delegate's
// and the completions' threads are not documented).
func traced(fr *ffi.Frame, what string) {
	if un.trace {
		println("notify:", what, "main_thread:", un.isMainFunc != 0 && fr.Call0(un.isMainFunc) != 0)
	}
}

// Selectors, resolved by register on the main thread; read only after.
var sel struct {
	alloc, init, release, retain, appActive                                            objc.SEL
	currentCenter, setDelegate, requestAuth, getSettings, authStatus                   objc.SEL
	setCategories, categoryWith, actionWith, textActionWith, setWithArray              objc.SEL
	setTitle, setSubtitle, setBody, setThread, setCategory, setUserInfo, setSound      objc.SEL
	setAttachments, defaultSound, soundNamed, attachmentWith, requestWith, addRequest  objc.SEL
	removeDelivered, removePending, getDelivered, getPending, arrayWith, numberWithULL objc.SEL
	count, objectAt, request, identifier, content, threadIdentifier, userInfo, date    objc.SEL
	since1970, objectForKey, ulongValue, bytes, length, isKindOfClass, respondsTo      objc.SEL
	notification, actionIdentifier, userText, utf8String, lengthOfBytes, code, desc    objc.SEL
	willPresent, didReceive                                                            objc.SEL
}

// available: the main bundle is an application bundle with an identifier,
// and UserNotifications is there.
func (nativeCenter) available() bool {
	un.availOnce.Do(func() {
		if cf.Init() != nil {
			return
		}
		fr := cf.Acquire()
		defer cf.Release(fr)
		defer cf.Pool(fr)()
		b := cf.Msg(fr, cf.Cls(fr, "NSBundle"), cf.Sel(fr, "mainBundle"))
		if b == 0 || cf.Msg(fr, b, cf.Sel(fr, "bundleIdentifier")) == 0 ||
			!strings.HasSuffix(cf.GoString(fr, cf.Msg(fr, b, cf.Sel(fr, "bundlePath"))), ".app") {
			return
		}
		if _, err := cf.Framework("UserNotifications"); err != nil || cf.Cls(fr, "UNUserNotificationCenter") == 0 {
			return
		}
		un.avail = true
	})
	return un.avail
}

// register resolves what the delegate and the requests use, and defines the
// delegate's class and the completion blocks. Main thread.
func register() {
	un.regOnce.Do(func() {
		var lib uintptr
		if cf.Init() == nil {
			lib, _ = cf.Framework("UserNotifications")
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		for _, s := range []struct {
			p    *objc.SEL
			name string
		}{
			{&sel.alloc, "alloc"}, {&sel.init, "init"}, {&sel.release, "release"}, {&sel.retain, "retain"},
			{&sel.appActive, "gnuiAppActive:"},
			{&sel.currentCenter, "currentNotificationCenter"}, {&sel.setDelegate, "setDelegate:"},
			{&sel.requestAuth, "requestAuthorizationWithOptions:completionHandler:"},
			{&sel.getSettings, "getNotificationSettingsWithCompletionHandler:"}, {&sel.authStatus, "authorizationStatus"},
			{&sel.setCategories, "setNotificationCategories:"},
			{&sel.categoryWith, "categoryWithIdentifier:actions:intentIdentifiers:options:"},
			{&sel.actionWith, "actionWithIdentifier:title:options:"},
			{&sel.textActionWith, "actionWithIdentifier:title:options:textInputButtonTitle:textInputPlaceholder:"},
			{&sel.setWithArray, "setWithArray:"},
			{&sel.setTitle, "setTitle:"}, {&sel.setSubtitle, "setSubtitle:"}, {&sel.setBody, "setBody:"},
			{&sel.setThread, "setThreadIdentifier:"}, {&sel.setCategory, "setCategoryIdentifier:"},
			{&sel.setUserInfo, "setUserInfo:"}, {&sel.setSound, "setSound:"}, {&sel.setAttachments, "setAttachments:"},
			{&sel.defaultSound, "defaultSound"}, {&sel.soundNamed, "soundNamed:"},
			{&sel.attachmentWith, "attachmentWithIdentifier:URL:options:error:"},
			{&sel.requestWith, "requestWithIdentifier:content:trigger:"},
			{&sel.addRequest, "addNotificationRequest:withCompletionHandler:"},
			{&sel.removeDelivered, "removeDeliveredNotificationsWithIdentifiers:"},
			{&sel.removePending, "removePendingNotificationRequestsWithIdentifiers:"},
			{&sel.getDelivered, "getDeliveredNotificationsWithCompletionHandler:"},
			{&sel.getPending, "getPendingNotificationRequestsWithCompletionHandler:"},
			{&sel.arrayWith, "arrayWithObjects:count:"}, {&sel.numberWithULL, "numberWithUnsignedLongLong:"},
			{&sel.count, "count"}, {&sel.objectAt, "objectAtIndex:"}, {&sel.request, "request"},
			{&sel.identifier, "identifier"}, {&sel.content, "content"}, {&sel.threadIdentifier, "threadIdentifier"},
			{&sel.userInfo, "userInfo"}, {&sel.date, "date"}, {&sel.since1970, "timeIntervalSince1970"},
			{&sel.objectForKey, "objectForKey:"}, {&sel.ulongValue, "unsignedLongLongValue"},
			{&sel.bytes, "bytes"}, {&sel.length, "length"}, {&sel.isKindOfClass, "isKindOfClass:"},
			{&sel.respondsTo, "respondsToSelector:"}, {&sel.notification, "notification"},
			{&sel.actionIdentifier, "actionIdentifier"}, {&sel.userText, "userText"},
			{&sel.utf8String, "UTF8String"}, {&sel.lengthOfBytes, "lengthOfBytesUsingEncoding:"},
			{&sel.code, "code"}, {&sel.desc, "localizedDescription"},
			{&sel.willPresent, "userNotificationCenter:willPresentNotification:withCompletionHandler:"},
			{&sel.didReceive, "userNotificationCenter:didReceiveNotificationResponse:withCompletionHandler:"},
		} {
			*s.p = objc.Sel(s.name)
		}
		for _, c := range []struct {
			p    *uintptr
			name string
		}{
			{&un.clsCenter, "UNUserNotificationCenter"}, {&un.clsContent, "UNMutableNotificationContent"},
			{&un.clsRequest, "UNNotificationRequest"}, {&un.clsSound, "UNNotificationSound"},
			{&un.clsCategory, "UNNotificationCategory"}, {&un.clsAction, "UNNotificationAction"},
			{&un.clsTextAction, "UNTextInputNotificationAction"}, {&un.clsAttachment, "UNNotificationAttachment"},
			{&un.clsArray, "NSArray"}, {&un.clsSet, "NSSet"}, {&un.clsNumber, "NSNumber"}, {&un.clsData, "NSData"},
		} {
			*c.p = uintptr(objc.Cls(c.name))
		}
		if un.trace = os.Getenv("GNUI_NOTIFY_TRACE") != ""; un.trace {
			if sys, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW); err == nil {
				un.isMainFunc = cf.Sym(sys, "pthread_main_np")
			}
		}
		un.keyK = uintptr(objc.Retain(objc.Str(keyKName)))
		un.keyD = uintptr(objc.Retain(objc.Str(keyDName)))
		if lib != 0 {
			fr := cf.Acquire()
			if s := cf.Const(lib, "UNNotificationDefaultActionIdentifier"); s != 0 {
				defaultActionID = cf.GoString(fr, s)
			}
			if s := cf.Const(lib, "UNNotificationDismissActionIdentifier"); s != 0 {
				dismissActionID = cf.GoString(fr, s)
			}
			cf.Release(fr)
		}

		d := objc.Define("GNUINotificationDelegate", objc.Cls("NSObject"), []string{"UNUserNotificationCenterDelegate"})
		d.Method(sel.willPresent, "v@:@@@?", willPresent)
		d.Method(sel.didReceive, "v@:@@@?", didReceive)
		d.Method(sel.appActive, "v@:@", func(c *ffi.CB) {
			traced(c.Frame(), "appActive")
			appActive()
		})
		cls := d.Register()
		un.delegate = uintptr(objc.Send(objc.Send(cls, sel.alloc), sel.init)) // kept for the process

		authSig := "v20@?0B8@12" // void (^)(BOOL, NSError *)
		if runtime.GOARCH == "amd64" {
			authSig = "v20@?0c8@12"
		}
		un.auth = objc.NewBlockKind(authSig, true, func(ctx uintptr, c *ffi.CB) {
			fr := c.Frame()
			traced(fr, "permission")
			pool := fr.Call0(objc.PoolPush)
			completeAuth(Ticket(ctx), byte(c.Int(1)) != 0, errText(fr, c.Int(2)))
			fr.Call1(objc.PoolPop, pool)
		}, nil)
		un.settings = objc.NewBlockKind("v16@?0@8", true, func(ctx uintptr, c *ffi.CB) {
			fr := c.Frame()
			traced(fr, "settings")
			completeStatus(Ticket(ctx), int64(send0(fr, c.Int(1), sel.authStatus)))
		}, nil)
		un.add = objc.NewBlockKind("v16@?0@8", true, func(ctx uintptr, c *ffi.CB) {
			fr := c.Frame()
			traced(fr, "post")
			pool := fr.Call0(objc.PoolPush)
			var code int64
			var msg []byte
			if e := c.Int(1); e != 0 {
				if code = int64(send0(fr, e, sel.code)); code == 0 {
					code = -1
				}
				msg = errText(fr, e)
			}
			added(int(ctx), code, msg)
			fr.Call1(objc.PoolPop, pool)
		}, nil)
		un.list = objc.NewBlockKind("v16@?0@8", true, func(ctx uintptr, c *ffi.CB) {
			fr := c.Frame()
			traced(fr, "delivered")
			pool := fr.Call0(objc.PoolPush)
			list := readDelivered(fr, c.Int(1), false)
			fr.Call1(objc.PoolPop, pool)
			listed(int(ctx), list)
		}, nil)
		un.listPending = objc.NewBlockKind("v16@?0@8", true, func(ctx uintptr, c *ffi.CB) {
			fr := c.Frame()
			traced(fr, "pending")
			pool := fr.Call0(objc.PoolPush)
			list := readDelivered(fr, c.Int(1), true)
			fr.Call1(objc.PoolPop, pool)
			listed(int(ctx), list)
		}, nil)
	})
}

// start installs the delegate. Main thread.
func (nativeCenter) start() error {
	register()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	c := objc.Send(objc.ID(un.clsCenter), sel.currentCenter)
	if c == 0 {
		return ErrUnavailable
	}
	un.center = uintptr(objc.Retain(c))
	objc.Send1(c, sel.setDelegate, un.delegate)
	observeActivation()
	return nil
}

// observeActivation has the delegate told when the application becomes
// active (by name: AppKit may not be loaded yet). Main thread.
func observeActivation() {
	un.observeOnce.Do(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		nc := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
		objc.AddObserver(nc, objc.ID(un.delegate), sel.appActive, objc.Str("NSApplicationDidBecomeActiveNotification"), 0)
	})
}

// ---- The delegate (any thread; no lock, no Go allocation) ----

// didReceive is userNotificationCenter:didReceiveNotificationResponse:
// withCompletionHandler:. The response is stored before the completion
// handler tells the system it was handled: at once, or, for one that waits
// for a free record, the handler is kept and called once it is stored.
func didReceive(c *ffi.CB) {
	fr := c.Frame()
	traced(fr, "didReceiveResponse")
	resp, handler := c.Int(3), c.Int(4)
	pool := fr.Call0(objc.PoolPush)
	key, data := userInfoOf(fr, send0(fr, send0(fr, send0(fr, send0(fr, resp, sel.notification), sel.request), sel.content), sel.userInfo))
	action := actionOf(utf8Of(fr, send0(fr, resp, sel.actionIdentifier)))
	var reply []byte
	if resp != 0 && fr.Reset().Arg(resp).Arg(uintptr(sel.respondsTo)).Arg(uintptr(sel.userText)).Call(objc.MsgSend).Bool() {
		reply = utf8Of(fr, send0(fr, resp, sel.userText))
	}
	p := respond(key, action, reply, data) // a parked response has its own copy
	fr.Call1(objc.PoolPop, pool)
	switch {
	case p == nil:
		if handler != 0 {
			objc.CallBlock(fr, objc.ID(handler))
		}
	case handler == 0:
		p.ack = func() {}
		p.wait()
	default:
		kept := new(objc.KeptBlock)
		objc.KeepBlock(fr, kept, objc.ID(handler))
		p.ack = func() {
			fr := cf.Acquire()
			objc.CallKeptBlock(fr, kept)
			cf.Release(fr)
		}
		p.wait()
	}
}

// UNNotificationPresentationOptions.
const (
	presentBadge  = 1 << 0
	presentSound  = 1 << 1
	presentList   = 1 << 3
	presentBanner = 1 << 4
)

// willPresent is userNotificationCenter:willPresentNotification:
// withCompletionHandler:: a note arriving while the app is active is shown
// when Foreground is on.
func willPresent(c *ffi.CB) {
	traced(c.Frame(), "willPresent")
	var opts uintptr
	if presentInForeground() {
		opts = presentBanner | presentList | presentSound | presentBadge
	}
	if h := c.Int(4); h != 0 {
		objc.CallBlock(c.Frame(), objc.ID(h), opts)
	}
}

// userInfoOf reads a note's response key and UserData from its user info
// (the data is the dictionary's own bytes: valid while the note lives).
func userInfoOf(fr *ffi.Frame, info uintptr) (key uint64, data []byte) {
	if info == 0 {
		return 0, nil
	}
	if k := send1(fr, info, sel.objectForKey, un.keyK); k != 0 && isKind(fr, k, un.clsNumber) {
		key = uint64(send0(fr, k, sel.ulongValue))
	}
	if d := send1(fr, info, sel.objectForKey, un.keyD); d != 0 && isKind(fr, d, un.clsData) {
		if n := send0(fr, d, sel.length); n > 0 {
			if p := send0(fr, d, sel.bytes); p != 0 {
				data = unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)
			}
		}
	}
	return key, data
}

func send0(fr *ffi.Frame, recv uintptr, s objc.SEL) uintptr {
	if recv == 0 {
		return 0
	}
	return fr.Reset().Arg(recv).Arg(uintptr(s)).Call(objc.MsgSend).R0()
}

func send1(fr *ffi.Frame, recv uintptr, s objc.SEL, a uintptr) uintptr {
	if recv == 0 {
		return 0
	}
	return fr.Reset().Arg(recv).Arg(uintptr(s)).Arg(a).Call(objc.MsgSend).R0()
}

func isKind(fr *ffi.Frame, obj, cls uintptr) bool {
	return fr.Reset().Arg(obj).Arg(uintptr(sel.isKindOfClass)).Arg(cls).Call(objc.MsgSend).Bool()
}

// utf8Of is the UTF-8 text of an NSString, in its own memory (valid until
// the autorelease pool pops).
func utf8Of(fr *ffi.Frame, s uintptr) []byte {
	if s == 0 {
		return nil
	}
	p := send0(fr, s, sel.utf8String)
	n := send1(fr, s, sel.lengthOfBytes, 4 /* NSUTF8StringEncoding */)
	if p == 0 || n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)
}

func errText(fr *ffi.Frame, e uintptr) []byte {
	if e == 0 {
		return nil
	}
	return utf8Of(fr, send0(fr, e, sel.desc))
}

// readDelivered lists an NSArray of UNNotification, or of
// UNNotificationRequest (requests: the pending ones). It allocates: once per
// listing.
func readDelivered(fr *ffi.Frame, arr uintptr, requests bool) []deliveredNote {
	n := int(send0(fr, arr, sel.count))
	list := make([]deliveredNote, 0, n)
	for i := 0; i < n; i++ {
		note := send1(fr, arr, sel.objectAt, uintptr(i))
		req := note
		if !requests {
			req = send0(fr, note, sel.request)
		}
		content := send0(fr, req, sel.content)
		d := deliveredNote{
			id:     string(utf8Of(fr, send0(fr, req, sel.identifier))),
			thread: string(utf8Of(fr, send0(fr, content, sel.threadIdentifier))),
		}
		d.key, _ = userInfoOf(fr, send0(fr, content, sel.userInfo))
		if !requests { // a request not shown yet has no date
			if date := send0(fr, note, sel.date); date != 0 {
				d.date = int64(fr.Reset().Arg(date).Arg(uintptr(sel.since1970)).Call(objc.MsgSend).F64(0) * 1e9)
			}
		}
		list = append(list, d)
	}
	return list
}

// ---- Requests (any goroutine) ----

// call runs f with a frame of its own inside an autorelease pool.
func call(f func(fr *ffi.Frame) error) error {
	fr := cf.Acquire()
	defer cf.Release(fr)
	pop := cf.Pool(fr)
	defer pop()
	return f(fr)
}

// send sends a message with integer and pointer arguments.
func send(fr *ffi.Frame, recv uintptr, s objc.SEL, args ...uintptr) uintptr {
	fr.Reset().Arg(recv).Arg(uintptr(s))
	for _, a := range args {
		fr.Arg(a)
	}
	return fr.Call(objc.MsgSend).R0()
}

// nsArray makes an autoreleased NSArray of objs (retained by it).
func nsArray(fr *ffi.Frame, objs []uintptr) uintptr {
	p := make([]uintptr, len(objs)+1)
	copy(p, objs)
	r := fr.Reset().Arg(un.clsArray).Arg(uintptr(sel.arrayWith)).ArgPtr(unsafe.Pointer(&p[0])).Arg(uintptr(len(objs))).Call(objc.MsgSend).R0()
	runtime.KeepAlive(p)
	return r
}

// setText sends a setter an NSString of s (none for "").
func setText(fr *ffi.Frame, obj uintptr, s objc.SEL, text string) {
	if text == "" {
		return
	}
	str := cf.String(fr, text)
	send(fr, obj, s, str)
	cf.CFRelease(fr, str)
}

// The system's option bits.
const (
	authBadge, authSound, authAlert, authProvisional = 1 << 0, 1 << 1, 1 << 2, 1 << 6
	actAuth, actDestructive, actForeground           = 1 << 0, 1 << 1, 1 << 2
	catCustomDismiss                                 = 1 << 0
)

func (nativeCenter) authorize(tk Ticket, p Permission) error {
	var opts uintptr
	for _, m := range [...]struct {
		p Permission
		o uintptr
	}{{Badge, authBadge}, {Sound, authSound}, {Alert, authAlert}, {Provisional, authProvisional}} {
		if p&m.p != 0 {
			opts |= m.o
		}
	}
	return call(func(fr *ffi.Frame) error {
		blk, err := un.auth.New(uintptr(tk))
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBusy, err)
		}
		send(fr, un.center, sel.requestAuth, opts, uintptr(blk.Block()))
		blk.Sent()
		return nil
	})
}

func (nativeCenter) settings(tk Ticket) error {
	return call(func(fr *ffi.Frame) error {
		blk, err := un.settings.New(uintptr(tk))
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBusy, err)
		}
		send(fr, un.center, sel.getSettings, uintptr(blk.Block()))
		blk.Sent()
		return nil
	})
}

func (nativeCenter) categories(cats []Category) error {
	return call(func(fr *ffi.Frame) error {
		objs := make([]uintptr, 0, len(cats))
		for _, c := range cats {
			acts := make([]uintptr, 0, len(c.Actions))
			for _, a := range c.Actions {
				var opts uintptr
				if a.AuthRequired {
					opts |= actAuth
				}
				if a.Destructive {
					opts |= actDestructive
				}
				if a.Foreground {
					opts |= actForeground
				}
				id, title := cf.String(fr, actionPrefix+strconv.FormatInt(a.ID, 10)), cf.String(fr, a.Title)
				var act uintptr
				if a.Reply {
					btn, ph := cf.String(fr, cmp.Or(a.ReplyButton, a.Title)), cf.String(fr, a.ReplyPlaceholder)
					act = send(fr, un.clsTextAction, sel.textActionWith, id, title, opts, btn, ph)
					cf.CFRelease(fr, btn)
					cf.CFRelease(fr, ph)
				} else {
					act = send(fr, un.clsAction, sel.actionWith, id, title, opts)
				}
				cf.CFRelease(fr, id)
				cf.CFRelease(fr, title)
				if act == 0 {
					return fmt.Errorf("%w: category %q: the system refused action %d", ErrInvalid, c.ID, a.ID)
				}
				acts = append(acts, act)
			}
			var opts uintptr
			if c.NotifyDismiss {
				opts |= catCustomDismiss
			}
			id := cf.String(fr, c.ID)
			cat := send(fr, un.clsCategory, sel.categoryWith, id, nsArray(fr, acts), nsArray(fr, nil), opts)
			cf.CFRelease(fr, id)
			if cat == 0 {
				return fmt.Errorf("%w: the system refused category %q", ErrInvalid, c.ID)
			}
			objs = append(objs, cat)
		}
		send(fr, un.center, sel.setCategories, send(fr, un.clsSet, sel.setWithArray, nsArray(fr, objs)))
		return nil
	})
}

// nativeRequest is a prepared UNNotificationRequest (retained) and the block
// its result comes back through.
type nativeRequest struct {
	req uintptr
	blk objc.Request
}

func (nativeCenter) prepare(n *Note, id string, key uint64, att string, ctx int) (prepared, error) {
	var r nativeRequest
	err := call(func(fr *ffi.Frame) error {
		content := send(fr, send(fr, un.clsContent, sel.alloc), sel.init)
		if content == 0 {
			return ErrUnavailable
		}
		defer send(fr, content, sel.release)
		setText(fr, content, sel.setTitle, n.Title)
		setText(fr, content, sel.setSubtitle, n.Subtitle)
		setText(fr, content, sel.setBody, n.Body)
		setText(fr, content, sel.setThread, n.Thread)
		if key != 0 {
			setText(fr, content, sel.setCategory, n.Category)
			info := cf.Dict(fr)
			cf.DictSet(fr, info, un.keyK, send(fr, un.clsNumber, sel.numberWithULL, uintptr(key)))
			if len(n.UserData) > 0 {
				d := cf.Data(fr, n.UserData)
				cf.DictSet(fr, info, un.keyD, d)
				cf.CFRelease(fr, d)
			}
			send(fr, content, sel.setUserInfo, info)
			cf.CFRelease(fr, info)
		}
		switch n.Sound {
		case "":
		case SoundDefault:
			send(fr, content, sel.setSound, send(fr, un.clsSound, sel.defaultSound))
		default:
			s := cf.String(fr, n.Sound)
			send(fr, content, sel.setSound, send(fr, un.clsSound, sel.soundNamed, s))
			cf.CFRelease(fr, s)
		}
		if att != "" {
			url := cf.FileURL(fr, att, false)
			aid := cf.String(fr, filepath.Base(att))
			out := cf.Out(fr)
			a := send(fr, un.clsAttachment, sel.attachmentWith, aid, url, 0, uintptr(out))
			nserr := cf.Word(fr)
			cf.CFRelease(fr, aid)
			cf.CFRelease(fr, url)
			if a == 0 {
				return fmt.Errorf("%w: the system refused it: %s", errAttachment, errText(fr, nserr))
			}
			send(fr, content, sel.setAttachments, nsArray(fr, []uintptr{a}))
		}
		ident := cf.String(fr, id)
		req := send(fr, un.clsRequest, sel.requestWith, ident, content, 0)
		cf.CFRelease(fr, ident)
		if req == 0 {
			return fmt.Errorf("%w: the system refused the note", ErrInvalid)
		}
		blk, err := un.add.New(uintptr(ctx))
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBusy, err)
		}
		r = nativeRequest{req: send(fr, req, sel.retain), blk: blk}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (nativeCenter) send(p prepared) {
	r := p.(*nativeRequest)
	call(func(fr *ffi.Frame) error {
		send(fr, un.center, sel.addRequest, r.req, uintptr(r.blk.Block()))
		r.blk.Sent()
		send(fr, r.req, sel.release)
		return nil
	})
}

func (nativeCenter) discard(p prepared) {
	r := p.(*nativeRequest)
	r.blk.Cancel()
	r.blk.Sent()
	call(func(fr *ffi.Frame) error {
		send(fr, r.req, sel.release)
		return nil
	})
}

func (nativeCenter) remove(ids []string) {
	call(func(fr *ffi.Frame) error {
		strs := make([]uintptr, len(ids))
		for i, id := range ids {
			strs[i] = cf.String(fr, id)
		}
		arr := nsArray(fr, strs)
		for _, s := range strs {
			cf.CFRelease(fr, s)
		}
		send(fr, un.center, sel.removeDelivered, arr)
		send(fr, un.center, sel.removePending, arr)
		return nil
	})
}

func (nativeCenter) delivered(ctx int) error { return list(un.list, sel.getDelivered, ctx) }
func (nativeCenter) pending(ctx int) error   { return list(un.listPending, sel.getPending, ctx) }

// list asks the center for a listing (s) whose result comes back through a
// block of kind k.
func list(k *objc.BlockKind, s objc.SEL, ctx int) error {
	return call(func(fr *ffi.Frame) error {
		blk, err := k.New(uintptr(ctx))
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBusy, err)
		}
		send(fr, un.center, s, uintptr(blk.Block()))
		blk.Sent()
		return nil
	})
}
