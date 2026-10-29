//go:build windows

package notify

import (
	"cmp"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
	"gonativeui/platform"
)

// Windows: WinRT toasts (the phase 8 plan's W6, D71), posted by an unpackaged
// executable as its AUMID, as probe P6 found works on both runners.
//
// Every WinRT call is made on one goroutine locked to an OS thread in the
// multithreaded apartment (wrt.jobs), started by Setup: the requests come from
// any goroutine, and Setup runs before app.Main has made the main thread an
// STA. A request made from that thread (a completion that calls back into
// the service) runs inline. The thread waits on its channel while idle.
//
// A note's response key, thread, action and UserData travel in its launch
// string and each action's arguments: "<scheme>:gnui-notify?k=…&a=…&t=…&d=…"
// (activation, without "<scheme>:" when the app has no scheme). The toast's
// tag is the note's ID and its group is fixed ("gnui"), so a later toast
// with the same ID replaces it, and the history's listing reads the key and
// thread back from the launch attribute.
//
// A toast that wants a response keeps its ToastNotification and three
// delegates (Activated, Dismissed, Failed: COM objects of internal/com,
// agile, called on the system's threads) in a record of wrt.recs until it
// is activated, dismissed by the user, hidden, replaced or removed. The
// delegates only parse and store (respond); releasing the toast is sent to
// the WinRT thread. A click after the app quit starts the app with the
// launch URL instead (activationType protocol): Setup takes it from
// os.Args, and a running app gets it from a second instance through
// platform.SetOpenURLFilter. Each key is answered once, whichever way comes
// first (wrt.answered).

type nativeCenter struct{}

// Interface ids (windows.ui.notifications.h, windows.data.xml.dom.h,
// windows.foundation.collections.h, windows.foundation.h). The handler ids
// are the parameterized TypedEventHandler ids P6 used.
var (
	iidToastManagerStatics  = mustGUID("50AC103F-D235-4598-BBEF-98FE4D1A3AD4")
	iidToastManagerStatics2 = mustGUID("7AB93C52-0E48-4750-BA9D-1A4113981847")
	iidToastHistory2        = mustGUID("3BC3D253-2F31-4092-9129-8AD5ABF067DA")
	iidToastFactory         = mustGUID("04124B20-82C6-4229-B109-FD9ED4662B53")
	iidToast2               = mustGUID("9DFB9FD1-143A-490E-90BF-B9FBA7132DE7")
	iidXMLDocumentIO        = mustGUID("6CD0E74E-EE65-4489-9EBF-CA43E87BA637")
	iidXMLDocument          = mustGUID("F7F3A506-1E87-42D6-BCFB-B8C809FA5494")
	iidActivatedArgs        = mustGUID("E3BF92F3-C197-436F-8265-0625824F8DAC")
	iidActivatedArgs2       = mustGUID("AB7DA512-CC61-568E-81BE-304AC31038FA")
	iidStringObjectMap      = mustGUID("1B0D3570-0877-5EC2-8A2C-3B9539506ACA") // IMap<HSTRING, IInspectable>
	iidPropertyValue        = mustGUID("4BD682DD-7554-40E9-9A9B-82654EDE7E62")
	iidActivatedHandler     = mustGUID("AB54DE2D-97D9-5528-B6AD-105AFE156530")
	iidDismissedHandler     = mustGUID("61C2402F-0ED0-5A18-AB69-59F4AA99A368")
	iidFailedHandler        = mustGUID("95E3E803-C969-5E3A-9753-EA2AD22A9A33")
)

// Vtable slots, from IInspectable's first.
const (
	wi                        = win32.InspectableFirst
	vtManagerNotifierWithID   = wi + 1 // IToastNotificationManagerStatics: CreateToastNotifier, CreateToastNotifierWithId
	vtManager2History         = wi + 0 // IToastNotificationManagerStatics2::get_History
	vtHistoryRemoveGroupedTag = wi + 2 // IToastNotificationHistory: RemoveGroup, RemoveGroupWithId, RemoveGroupedTagWithId
	vtHistory2GetWithID       = wi + 1 // IToastNotificationHistory2: GetHistory, GetHistoryWithId
	vtVectorGetAt             = wi + 0 // IVectorView<T>: GetAt, get_Size
	vtVectorSize              = wi + 1
	vtXMLLoad                 = wi + 0 // IXmlDocumentIO::LoadXml
	vtXMLDocumentElement      = wi + 2 // IXmlDocument: get_Doctype, get_Implementation, get_DocumentElement
	vtXMLGetAttribute         = wi + 1 // IXmlElement: get_TagName, GetAttribute
	vtFactoryCreate           = wi + 0 // IToastNotificationFactory::CreateToastNotification
	vtToastContent            = wi + 0 // IToastNotification: get_Content, put/get_ExpirationTime, add/remove_Dismissed, _Activated, _Failed
	vtToastAddDismissed       = wi + 3
	vtToastRemoveDismissed    = wi + 4
	vtToastAddActivated       = wi + 5
	vtToastRemoveActivated    = wi + 6
	vtToastAddFailed          = wi + 7
	vtToastRemoveFailed       = wi + 8
	vtToast2PutTag            = wi + 0 // IToastNotification2: put_Tag, get_Tag, put_Group, get_Group
	vtToast2GetTag            = wi + 1
	vtToast2PutGroup          = wi + 2
	vtNotifierShow            = wi + 0 // IToastNotifier: Show, Hide, get_Setting
	vtNotifierHide            = wi + 1
	vtNotifierSetting         = wi + 2
	vtActivatedArguments      = wi + 0 // IToastActivatedEventArgs::get_Arguments
	vtActivated2UserInput     = wi + 0 // IToastActivatedEventArgs2::get_UserInput
	vtMapLookup               = wi + 0 // IMap<K, V>: Lookup, get_Size, HasKey
	vtMapHasKey               = wi + 2
	vtPropertyGetString       = wi + 13 // IPropertyValue: get_Type, get_IsNumericScalar, GetUInt8 … GetBoolean, GetString
	vtDismissedReason         = wi + 0  // IToastDismissedEventArgs::get_Reason
)

// ToastDismissalReason.
const (
	dismissUser    = 0
	dismissHidden  = 1
	dismissTimeout = 2
)

// NotificationSetting: Enabled; the others are kinds of "disabled".
const settingEnabled = 0

const (
	toastGroup = "gnui"
	replyInput = "reply"
	maxRecs    = 320 // toasts kept for their events (the response slots, and room)
	maxTag     = 64  // a toast's tag, in UTF-16 units
	maxActions = 5
	nAnswered  = 64 // keys answered recently: a response comes once
)

// rec is a toast kept for its events.
type rec struct {
	gen     atomic.Uint32 // moves on when the record is freed; the delegates' value carries it
	live    bool          // WinRT thread
	toast   uintptr       // IToastNotification, held
	tokens  [3]int64      // the handlers' EventRegistrationTokens
	key     atomic.Uint64
	dismiss atomic.Bool // the category reports a user's dismissal
	id      string      // the tag
	seq     uint64      // the order it was posted in (eviction of the oldest)
}

var wrt struct {
	once     sync.Once
	startErr error
	jobs     chan func()
	tid      uint32 // the WinRT thread's id

	mu      sync.Mutex // cfg and cats
	aumid   string
	scheme  string
	cats    map[string]Category
	started bool // start ran

	// The WinRT thread's own state.
	notifier uintptr // IToastNotifier for the AUMID
	hid      win32.HSTRING
	recs     [maxRecs]rec
	seq      uint64

	activated, dismissed, failed *com.Kind

	answered [nAnswered]atomic.Uint64
	nAns     atomic.Uint32

	// Counts, for the tests.
	shown, hidden, events atomic.Int64
}

var (
	pGetCurrentThreadID = win32.Proc(win32.LoadSystem("kernel32.dll"), "GetCurrentThreadId")
	shell32             = win32.LoadSystem("shell32.dll")
	pSetAppID           = win32.Proc(shell32, "SetCurrentProcessExplicitAppUserModelID")
	pGetAppID           = win32.Proc(shell32, "GetCurrentProcessExplicitAppUserModelID")
	advapi32            = win32.LoadSystem("advapi32.dll")
	pRegOpenKeyExW      = win32.Proc(advapi32, "RegOpenKeyExW")
	pRegCloseKey        = win32.Proc(advapi32, "RegCloseKey")
	pCoTaskMemFree      = win32.Proc(win32.LoadSystem("ole32.dll"), "CoTaskMemFree")
)

func (nativeCenter) supported() bool { return win32.WinRTAvailable() }

// available: an AUMID is known (Setup's AppID, or the process's).
func (nativeCenter) available() bool { return appID() != "" }

func (nativeCenter) launched() bool { return platform.AppStarted() }

// appID is the AUMID: Setup's, else the process's explicit one.
func appID() string {
	wrt.mu.Lock()
	id := wrt.aumid
	wrt.mu.Unlock()
	if id != "" || pGetAppID == 0 {
		return id
	}
	var p uintptr
	if win32.HR(call(pGetAppID, uintptr(unsafe.Pointer(&p)))) < 0 || p == 0 {
		return ""
	}
	defer call(pCoTaskMemFree, p)
	return utf16At(p)
}

// configure takes Setup's AppID and Scheme and checks the AUMID's
// registration.
func (nativeCenter) configure(o Options) error {
	id := o.AppID
	if id == "" {
		id = appID()
	}
	if id == "" {
		return fmt.Errorf("%w: no AUMID (set Options.AppID to app.AppConfig.ID)", ErrNotRegistered)
	}
	if !aumidRegistered(id) {
		return fmt.Errorf(`%w: HKCU\Software\Classes\AppUserModelId\%s is missing (install.Install writes it)`, ErrNotRegistered, id)
	}
	if o.Scheme != "" && !validScheme(o.Scheme) {
		return fmt.Errorf("%w: %q is not a URL scheme", ErrInvalid, o.Scheme)
	}
	wrt.mu.Lock()
	defer wrt.mu.Unlock()
	if wrt.started && id != wrt.aumid {
		return fmt.Errorf("%w: the AUMID cannot change once Setup succeeded", ErrInvalid)
	}
	wrt.aumid, wrt.scheme = id, o.Scheme
	return nil
}

func validScheme(s string) bool {
	for i, c := range s {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.')
		if !ok {
			return false
		}
	}
	return s != ""
}

// aumidRegistered reports whether HKCU\Software\Classes\AppUserModelId\<id>
// exists.
func aumidRegistered(id string) bool {
	sub, err := syscall.UTF16FromString(`Software\Classes\AppUserModelId\` + id)
	if err != nil {
		return false
	}
	const keyRead = 0x20019
	var hk uintptr
	if call(pRegOpenKeyExW, uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(&sub[0])), 0, keyRead, uintptr(unsafe.Pointer(&hk))) != 0 {
		return false
	}
	call(pRegCloseKey, hk)
	return true
}

// start makes the process's AUMID the app's, starts the WinRT thread with
// the notifier and the delegates' kinds, takes over activation URLs, and
// delivers one the process was started with. Main thread, before app.Main.
func (nativeCenter) start() error {
	id := appID()
	if p, err := syscall.UTF16PtrFromString(id); err == nil && pSetAppID != 0 {
		call(pSetAppID, uintptr(unsafe.Pointer(p)))
	}
	wrt.once.Do(func() {
		wrt.jobs = make(chan func(), 64)
		ready := make(chan error)
		go winrtThread(id, ready)
		wrt.startErr = <-ready
	})
	if wrt.startErr != nil {
		return wrt.startErr
	}
	wrt.mu.Lock()
	wrt.started = true
	wrt.mu.Unlock()
	platform.SetOpenURLFilter(takeActivation)
	for _, a := range os.Args[1:] {
		takeActivation(a)
	}
	return nil
}

// winrtThread is the WinRT thread: it joins the multithreaded apartment,
// makes the notifier and runs the jobs.
func winrtThread(id string, ready chan<- error) {
	runtime.LockOSThread()
	wrt.tid = uint32(call(pGetCurrentThreadID))
	if hr := win32.RoInitialize(win32.RoInitMultiThreaded); hr < 0 && hr != win32.RPCEChangedMode {
		ready <- fmt.Errorf("%w: RoInitialize 0x%08X", ErrUnavailable, uint32(hr))
		return
	}
	hid, hr := win32.NewHSTRING(id)
	if hr < 0 {
		ready <- fmt.Errorf("%w: the AUMID: 0x%08X", ErrInvalid, uint32(hr))
		return
	}
	wrt.hid = hid
	var mgr uintptr
	if hr := win32.RoGetActivationFactory("Windows.UI.Notifications.ToastNotificationManager", &iidToastManagerStatics, &mgr); hr < 0 {
		ready <- fmt.Errorf("%w: ToastNotificationManager 0x%08X", ErrUnavailable, uint32(hr))
		return
	}
	hr = win32.HR(cm(mgr, vtManagerNotifierWithID, uintptr(hid), uintptr(unsafe.Pointer(&wrt.notifier))))
	win32.Release(mgr)
	if hr < 0 {
		ready <- fmt.Errorf("%w: CreateToastNotifierWithId 0x%08X", ErrUnavailable, uint32(hr))
		return
	}
	makeKinds()
	ready <- nil
	for job := range wrt.jobs {
		job()
	}
}

// run runs f on the WinRT thread and waits for it; inline on that thread.
func run(f func()) {
	if uint32(call(pGetCurrentThreadID)) == wrt.tid {
		f()
		return
	}
	done := make(chan struct{})
	wrt.jobs <- func() { f(); close(done) }
	<-done
}

// later runs f on the WinRT thread without waiting (from a delegate).
func later(f func()) {
	select {
	case wrt.jobs <- f:
	default:
		go func() { wrt.jobs <- f }()
	}
}

// ---- The delegates (the system's threads) ----

func makeKinds() {
	// Invoke(sender, args) is each delegate's slot 3.
	wrt.activated = com.NewKind("ToastActivated", maxRecs, []win32.GUID{iidActivatedHandler}, []com.Method{{N: 2, Fn: onActivated}})
	wrt.dismissed = com.NewKind("ToastDismissed", maxRecs, []win32.GUID{iidDismissedHandler}, []com.Method{{N: 2, Fn: onDismissed}})
	wrt.failed = com.NewKind("ToastFailed", maxRecs, []win32.GUID{iidFailedHandler}, []com.Method{{N: 2, Fn: onFailed}})
}

// recOf is the record a delegate's value names (index and generation), nil
// once it was freed.
func recOf(v uintptr) (*rec, int) {
	i, gen := int(v&0xFFFF), uint32(v>>16)
	if i >= maxRecs || wrt.recs[i].gen.Load() != gen {
		return nil, -1
	}
	return &wrt.recs[i], i
}

func onActivated(o *com.Object, a com.Args) uintptr {
	wrt.events.Add(1)
	var args, args2, input uintptr
	var arg string
	if a[1] != 0 && win32.QueryInterface(a[1], &iidActivatedArgs, &args) >= 0 {
		var hs win32.HSTRING
		if win32.HR(cm(args, vtActivatedArguments, uintptr(unsafe.Pointer(&hs)))) >= 0 {
			arg = hs.String()
			hs.Delete()
		}
		win32.Release(args)
	}
	var reply []byte
	if a[1] != 0 && win32.QueryInterface(a[1], &iidActivatedArgs2, &args2) >= 0 {
		if win32.HR(cm(args2, vtActivated2UserInput, uintptr(unsafe.Pointer(&input)))) >= 0 && input != 0 {
			reply = []byte(lookupString(input, replyInput))
			win32.Release(input)
		}
		win32.Release(args2)
	}
	if act, ok := parseActivation(arg); ok && firstAnswer(act.key) {
		if p := respond(act.key, act.action, reply, act.data); p != nil {
			p.ack, p.drop = func() {}, func() {}
			p.wait()
		}
	}
	if r, i := recOf(o.Value()); r != nil {
		gen := r.gen.Load()
		later(func() { freeRec(i, gen) })
	}
	return 0
}

// lookupString reads the string value of key in a ValueSet ("" if none).
func lookupString(set uintptr, key string) string {
	var m, v, pv uintptr
	if win32.QueryInterface(set, &iidStringObjectMap, &m) < 0 {
		return ""
	}
	defer win32.Release(m)
	hk, hr := win32.NewHSTRING(key)
	if hr < 0 {
		return ""
	}
	defer hk.Delete()
	var has uint8
	if win32.HR(cm(m, vtMapHasKey, uintptr(hk), uintptr(unsafe.Pointer(&has)))) < 0 || has == 0 {
		return ""
	}
	if win32.HR(cm(m, vtMapLookup, uintptr(hk), uintptr(unsafe.Pointer(&v)))) < 0 || v == 0 {
		return ""
	}
	defer win32.Release(v)
	if win32.QueryInterface(v, &iidPropertyValue, &pv) < 0 {
		return ""
	}
	defer win32.Release(pv)
	var hs win32.HSTRING
	if win32.HR(cm(pv, vtPropertyGetString, uintptr(unsafe.Pointer(&hs)))) < 0 {
		return ""
	}
	defer hs.Delete()
	return hs.String()
}

func onDismissed(o *com.Object, a com.Args) uintptr {
	wrt.events.Add(1)
	r, i := recOf(o.Value())
	if r == nil {
		return 0
	}
	reason := int32(-1)
	if a[1] != 0 {
		cm(a[1], vtDismissedReason, uintptr(unsafe.Pointer(&reason)))
	}
	switch reason {
	case dismissTimeout:
		return 0 // in the action centre now: its events may still come
	case dismissUser:
		if key := r.key.Load(); r.dismiss.Load() && firstAnswer(key) {
			if p := respond(key, ActionDismiss, nil, nil); p != nil {
				p.ack, p.drop = func() {}, func() {}
				p.wait()
			}
		}
	}
	gen := r.gen.Load()
	later(func() { freeRec(i, gen) })
	return 0
}

func onFailed(o *com.Object, a com.Args) uintptr {
	wrt.events.Add(1)
	if r, i := recOf(o.Value()); r != nil {
		gen := r.gen.Load()
		later(func() { freeRec(i, gen) })
	}
	return 0
}

// firstAnswer reports whether no response to the note with key came yet,
// and remembers this one: the toast's event and the protocol launch may
// both bring it. A plain note's (key 0) always counts.
func firstAnswer(key uint64) bool {
	if key == 0 {
		return true
	}
	n := wrt.nAns.Load()
	for i := uint32(0); i < min(n, nAnswered); i++ {
		if wrt.answered[i].Load() == key {
			return false
		}
	}
	wrt.answered[(wrt.nAns.Add(1)-1)%nAnswered].Store(key)
	return true
}

// takeActivation takes an activation URL (a command-line argument, or a
// second instance's): its response is stored, and it is not offered as
// EvOpenURLs.
func takeActivation(u string) bool {
	if !IsActivation(u) {
		return false
	}
	act, ok := parseActivation(u)
	if !ok || !firstAnswer(act.key) {
		return true
	}
	if p := respond(act.key, act.action, nil, act.data); p != nil {
		p.ack, p.drop = func() {}, func() {}
		go p.wait()
	}
	return true
}

// activation is what a launch string or an action's arguments carry.
type activation struct {
	key    uint64
	action int64
	thread string
	data   []byte
}

var b64 = base64.RawURLEncoding

// activationURL is the launch string or an action's arguments.
func activationURL(scheme string, key uint64, action int64, thread string, data []byte) string {
	var b strings.Builder
	if scheme != "" {
		b.WriteString(scheme)
		b.WriteByte(':')
	}
	b.WriteString(activationTag)
	b.WriteString("k=")
	b.WriteString(strconv.FormatUint(key, 16))
	b.WriteString("&a=")
	b.WriteString(strconv.FormatInt(action, 10))
	if thread != "" {
		b.WriteString("&t=")
		b.WriteString(b64.EncodeToString([]byte(thread)))
	}
	if len(data) > 0 {
		b.WriteString("&d=")
		b.WriteString(b64.EncodeToString(data))
	}
	return b.String()
}

// parseActivation reads an activation URL (with or without its scheme).
func parseActivation(s string) (activation, bool) {
	i := strings.Index(s, activationTag)
	if i < 0 {
		return activation{}, false
	}
	var a activation
	ok := false
	for _, kv := range strings.Split(s[i+len(activationTag):], "&") {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "k":
			n, err := strconv.ParseUint(v, 16, 64)
			if err != nil {
				return activation{}, false
			}
			a.key, ok = n, true
		case "a":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return activation{}, false
			}
			a.action = n
		case "t":
			t, err := b64.DecodeString(v)
			if err != nil {
				return activation{}, false
			}
			a.thread = string(t)
		case "d":
			d, err := b64.DecodeString(v)
			if err != nil || len(d) > MaxUserData {
				return activation{}, false
			}
			a.data = d
		}
	}
	return a, ok
}

// ---- Requests ----

// setting reads the notifier's setting for the app; ok false when it cannot
// be read (P6: without a Start-menu shortcut it fails with 0x80070490).
func setting() (v int32, ok bool) {
	run(func() {
		v = -1
		ok = win32.HR(cm(wrt.notifier, vtNotifierSetting, uintptr(unsafe.Pointer(&v)))) >= 0
	})
	return v, ok
}

// authorize: Windows asks nobody. The app may post unless the user (or a
// policy) turned its toasts off.
func (nativeCenter) authorize(tk Ticket, p Permission) error {
	v, ok := setting()
	var msg []byte
	if ok && v != settingEnabled {
		msg = []byte("notifications are turned off for this app in Windows' settings")
	}
	completeAuth(tk, !ok || v == settingEnabled, msg)
	return nil
}

func (nativeCenter) settings(tk Ticket) error {
	st := StatusAuthorized
	if v, ok := setting(); ok && v != settingEnabled {
		st = StatusDenied
	}
	completeStatus(tk, int64(st))
	return nil
}

func (nativeCenter) categories(cats []Category) error {
	m := make(map[string]Category, len(cats))
	for _, c := range cats {
		if len(c.Actions) > maxActions {
			return fmt.Errorf("%w: category %q: a toast shows at most %d actions", ErrInvalid, c.ID, maxActions)
		}
		m[c.ID] = c
	}
	wrt.mu.Lock()
	wrt.cats = m
	wrt.mu.Unlock()
	return nil
}

// toastXML is the toast for note n posted as id: its text, its actions (with
// key) and the launch strings that carry the key.
func toastXML(n *Note, key uint64, att string) (string, error) {
	wrt.mu.Lock()
	scheme := wrt.scheme
	cat, hasCat := wrt.cats[n.Category]
	wrt.mu.Unlock()
	activate := "foreground"
	if scheme != "" {
		activate = "protocol"
	}
	var data []byte
	if key != 0 {
		data = n.UserData
	}
	var b strings.Builder
	b.WriteString(`<toast launch="`)
	attr(&b, activationURL(scheme, key, ActionClick, n.Thread, data))
	b.WriteString(`" activationType="`)
	b.WriteString(activate)
	b.WriteString(`"><visual><binding template="ToastGeneric">`)
	for _, t := range [...]string{n.Title, n.Subtitle, n.Body} {
		if t != "" {
			b.WriteString("<text>")
			text(&b, t)
			b.WriteString("</text>")
		}
	}
	if att != "" {
		b.WriteString(`<image src="`)
		attr(&b, "file:///"+strings.ReplaceAll(att, `\`, "/"))
		b.WriteString(`"/>`)
	}
	b.WriteString(`</binding></visual>`)
	if key != 0 && hasCat && len(cat.Actions) > 0 {
		b.WriteString("<actions>")
		for _, a := range cat.Actions {
			if a.Reply {
				b.WriteString(`<input id="` + replyInput + `" type="text" placeHolderContent="`)
				attr(&b, a.ReplyPlaceholder)
				b.WriteString(`"/>`)
				break
			}
		}
		for _, a := range cat.Actions {
			b.WriteString(`<action content="`)
			if a.Reply {
				attr(&b, cmp.Or(a.ReplyButton, a.Title))
			} else {
				attr(&b, a.Title)
			}
			b.WriteString(`" arguments="`)
			attr(&b, activationURL(scheme, key, a.ID, n.Thread, data))
			b.WriteString(`" activationType="`)
			b.WriteString(activate)
			b.WriteString(`"`)
			if a.Reply {
				b.WriteString(` hint-inputId="` + replyInput + `"`)
			}
			if a.Destructive {
				b.WriteString(` hint-buttonStyle="Critical"`)
			}
			b.WriteString("/>")
		}
		b.WriteString("</actions>")
	}
	switch {
	case n.Sound == "":
		b.WriteString(`<audio silent="true"/>`)
	case n.Sound != SoundDefault && strings.HasPrefix(n.Sound, "ms-"):
		b.WriteString(`<audio src="`)
		attr(&b, n.Sound)
		b.WriteString(`"/>`)
	}
	b.WriteString("</toast>")
	return b.String(), nil
}

// text and attr escape XML character data and attribute values.
func text(b *strings.Builder, s string) { escape(b, s, false) }
func attr(b *strings.Builder, s string) { escape(b, s, true) }

func escape(b *strings.Builder, s string, quote bool) {
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"' && quote:
			b.WriteString("&quot;")
		case r == '\'' && quote:
			b.WriteString("&apos;")
		case r < 0x20 && r != '\t' && r != '\n' && r != '\r', r == 0xFFFE, r == 0xFFFF:
			// not allowed in XML 1.0: dropped
		default:
			b.WriteRune(r)
		}
	}
}

// nativeToast is a prepared toast: made, not shown.
type nativeToast struct {
	toast   uintptr // IToastNotification, held
	id      string
	key     uint64
	dismiss bool
	ctx     int
}

func (nativeCenter) prepare(n *Note, id string, key uint64, att string, ctx int) (prepared, error) {
	if len(syscall.StringToUTF16(id))-1 > maxTag {
		return nil, fmt.Errorf("%w: a toast's ID is at most %d UTF-16 units", ErrInvalid, maxTag)
	}
	x, err := toastXML(n, key, att)
	if err != nil {
		return nil, err
	}
	wrt.mu.Lock()
	dismiss := wrt.cats[n.Category].NotifyDismiss
	wrt.mu.Unlock()
	t := &nativeToast{id: id, key: key, dismiss: key != 0 && dismiss, ctx: ctx}
	run(func() { err = makeToast(t, x) })
	if err != nil {
		return nil, err
	}
	return t, nil
}

// makeToast makes t's ToastNotification from its XML. WinRT thread.
func makeToast(t *nativeToast, x string) error {
	var insp, io, doc, fac, t2 uintptr
	if hr := win32.RoActivateInstance("Windows.Data.Xml.Dom.XmlDocument", &insp); hr < 0 {
		return fmt.Errorf("%w: XmlDocument 0x%08X", ErrUnavailable, uint32(hr))
	}
	defer win32.Release(insp)
	if hr := win32.QueryInterface(insp, &iidXMLDocumentIO, &io); hr < 0 {
		return fmt.Errorf("%w: IXmlDocumentIO 0x%08X", ErrUnavailable, uint32(hr))
	}
	defer win32.Release(io)
	hx, hr := win32.NewHSTRING(x)
	if hr < 0 {
		return fmt.Errorf("%w: the note's text has a NUL", ErrInvalid)
	}
	hr = win32.HR(cm(io, vtXMLLoad, uintptr(hx)))
	hx.Delete()
	if hr < 0 {
		return fmt.Errorf("%w: the toast's XML was refused (0x%08X)", ErrInvalid, uint32(hr))
	}
	if hr := win32.QueryInterface(insp, &iidXMLDocument, &doc); hr < 0 {
		return fmt.Errorf("%w: IXmlDocument 0x%08X", ErrUnavailable, uint32(hr))
	}
	defer win32.Release(doc)
	if hr := win32.RoGetActivationFactory("Windows.UI.Notifications.ToastNotification", &iidToastFactory, &fac); hr < 0 {
		return fmt.Errorf("%w: ToastNotification 0x%08X", ErrUnavailable, uint32(hr))
	}
	hr = win32.HR(cm(fac, vtFactoryCreate, doc, uintptr(unsafe.Pointer(&t.toast))))
	win32.Release(fac)
	if hr < 0 {
		return fmt.Errorf("%w: CreateToastNotification 0x%08X", ErrInvalid, uint32(hr))
	}
	if win32.QueryInterface(t.toast, &iidToast2, &t2) >= 0 {
		htag, _ := win32.NewHSTRING(t.id)
		hgrp, _ := win32.NewHSTRING(toastGroup)
		cm(t2, vtToast2PutTag, uintptr(htag))
		cm(t2, vtToast2PutGroup, uintptr(hgrp))
		htag.Delete()
		hgrp.Delete()
		win32.Release(t2)
	}
	return nil
}

func (nativeCenter) send(p prepared) {
	t := p.(*nativeToast)
	var hr int32
	run(func() {
		replaced(t.id)
		if t.key != 0 {
			keep(t)
		}
		hr = win32.HR(cm(wrt.notifier, vtNotifierShow, t.toast))
		if hr >= 0 {
			wrt.shown.Add(1)
		} else if t.key != 0 {
			replaced(t.id)
		}
		win32.Release(t.toast) // a record holds its own reference
		t.toast = 0
	})
	if hr < 0 {
		added(t.ctx, int64(uint32(hr)), []byte(fmt.Sprintf("the toast was refused (0x%08X)", uint32(hr))))
		return
	}
	added(t.ctx, 0, nil)
}

func (nativeCenter) discard(p prepared) {
	t := p.(*nativeToast)
	run(func() {
		win32.Release(t.toast)
		t.toast = 0
	})
}

// keep puts t's toast in a free record (the oldest one's when none is) and
// registers its delegates. WinRT thread.
func keep(t *nativeToast) {
	i, oldest := -1, -1
	for j := range wrt.recs {
		if !wrt.recs[j].live {
			i = j
			break
		}
		if oldest < 0 || wrt.recs[j].seq < wrt.recs[oldest].seq {
			oldest = j
		}
	}
	if i < 0 {
		freeRec(oldest, wrt.recs[oldest].gen.Load())
		i = oldest
	}
	r := &wrt.recs[i]
	win32.AddRef(t.toast)
	wrt.seq++
	r.live, r.toast, r.id, r.seq = true, t.toast, t.id, wrt.seq
	r.key.Store(t.key)
	r.dismiss.Store(t.dismiss)
	v := uintptr(i) | uintptr(r.gen.Load())<<16
	for k, h := range [...]struct {
		kind *com.Kind
		add  uintptr
	}{{wrt.activated, vtToastAddActivated}, {wrt.dismissed, vtToastAddDismissed}, {wrt.failed, vtToastAddFailed}} {
		o, _, err := h.kind.New(v)
		if err != nil {
			continue
		}
		cm(t.toast, h.add, o.Ptr(), uintptr(unsafe.Pointer(&r.tokens[k])))
		o.Release() // the toast holds its reference now
	}
}

// freeRec unregisters record i's delegates and releases its toast, if it
// still is generation gen. WinRT thread.
func freeRec(i int, gen uint32) {
	r := &wrt.recs[i]
	if !r.live || r.gen.Load() != gen {
		return
	}
	r.gen.Add(1)
	for k, rm := range [...]uintptr{vtToastRemoveActivated, vtToastRemoveDismissed, vtToastRemoveFailed} {
		if r.tokens[k] != 0 {
			cm(r.toast, rm, uintptr(r.tokens[k]))
		}
	}
	win32.Release(r.toast)
	r.live, r.toast, r.id, r.tokens = false, 0, "", [3]int64{}
	r.key.Store(0)
}

// replaced frees the records of the toasts with id: a new one takes their
// place. WinRT thread.
func replaced(id string) {
	for i := range wrt.recs {
		if r := &wrt.recs[i]; r.live && r.id == id {
			freeRec(i, r.gen.Load())
		}
	}
}

func (nativeCenter) remove(ids []string) {
	run(func() {
		var mgr2, hist uintptr
		if win32.RoGetActivationFactory("Windows.UI.Notifications.ToastNotificationManager", &iidToastManagerStatics2, &mgr2) < 0 {
			return
		}
		hr := win32.HR(cm(mgr2, vtManager2History, uintptr(unsafe.Pointer(&hist))))
		win32.Release(mgr2)
		if hr < 0 {
			return
		}
		defer win32.Release(hist)
		hgrp, _ := win32.NewHSTRING(toastGroup)
		defer hgrp.Delete()
		for _, id := range ids {
			// A toast still on screen is hidden through the notifier (its
			// record has it); the history removes it from the action centre.
			for i := range wrt.recs {
				if r := &wrt.recs[i]; r.live && r.id == id {
					if win32.HR(cm(wrt.notifier, vtNotifierHide, r.toast)) >= 0 {
						wrt.hidden.Add(1)
					}
					freeRec(i, r.gen.Load())
				}
			}
			htag, hr := win32.NewHSTRING(id)
			if hr < 0 {
				continue
			}
			cm(hist, vtHistoryRemoveGroupedTag, uintptr(htag), uintptr(hgrp), uintptr(wrt.hid))
			htag.Delete()
		}
	})
}

// pending: Windows has no scheduled toasts here.
func (nativeCenter) pending(ctx int) error {
	go listed(ctx, nil)
	return nil
}

// delivered lists the app's toasts in the action centre
// (ToastNotificationHistory), oldest first; their key and thread come from
// their launch strings. A toast that is not ours (none should be) is listed
// without a key.
func (nativeCenter) delivered(ctx int) error {
	var list []deliveredNote
	var err error
	run(func() { list, err = history() })
	if err != nil {
		return err
	}
	go listed(ctx, list)
	return nil
}

// history reads the action centre's toasts of the AUMID. WinRT thread.
func history() ([]deliveredNote, error) {
	var mgr2, hist, hist2, view uintptr
	hr := win32.RoGetActivationFactory("Windows.UI.Notifications.ToastNotificationManager", &iidToastManagerStatics2, &mgr2)
	if hr >= 0 {
		hr = win32.HR(cm(mgr2, vtManager2History, uintptr(unsafe.Pointer(&hist))))
		win32.Release(mgr2)
	}
	if hr >= 0 {
		hr = win32.QueryInterface(hist, &iidToastHistory2, &hist2)
		win32.Release(hist)
	}
	if hr >= 0 {
		hr = win32.HR(cm(hist2, vtHistory2GetWithID, uintptr(wrt.hid), uintptr(unsafe.Pointer(&view))))
		win32.Release(hist2)
	}
	if hr < 0 {
		return nil, fmt.Errorf("%w: the toast history: 0x%08X", ErrUnavailable, uint32(hr))
	}
	defer win32.Release(view)
	var n uint32
	if win32.HR(cm(view, vtVectorSize, uintptr(unsafe.Pointer(&n)))) < 0 {
		return nil, errors.New("notify: the toast history's size")
	}
	hlaunch, _ := win32.NewHSTRING("launch")
	defer hlaunch.Delete()
	list := make([]deliveredNote, 0, n)
	for i := uint32(0); i < n; i++ {
		var t uintptr
		if win32.HR(cm(view, vtVectorGetAt, uintptr(i), uintptr(unsafe.Pointer(&t)))) < 0 || t == 0 {
			continue
		}
		d := deliveredNote{date: int64(i) + 1} // the history gives no time: its order
		var t2, doc, el uintptr
		if win32.QueryInterface(t, &iidToast2, &t2) >= 0 {
			var hs win32.HSTRING
			if win32.HR(cm(t2, vtToast2GetTag, uintptr(unsafe.Pointer(&hs)))) >= 0 {
				d.id = hs.String()
				hs.Delete()
			}
			win32.Release(t2)
		}
		if win32.HR(cm(t, vtToastContent, uintptr(unsafe.Pointer(&doc)))) >= 0 && doc != 0 {
			if win32.HR(cm(doc, vtXMLDocumentElement, uintptr(unsafe.Pointer(&el)))) >= 0 && el != 0 {
				var hs win32.HSTRING
				if win32.HR(cm(el, vtXMLGetAttribute, uintptr(hlaunch), uintptr(unsafe.Pointer(&hs)))) >= 0 {
					if a, ok := parseActivation(hs.String()); ok {
						d.key, d.thread = a.key, a.thread
					}
					hs.Delete()
				}
				win32.Release(el)
			}
			win32.Release(doc)
		}
		win32.Release(t)
		if d.id != "" {
			list = append(list, d)
		}
	}
	return list, nil
}

// ---- Plumbing ----

// call calls fn; what a pointer argument converted in the call expression
// points to is moved to the heap.
//
//go:uintptrescapes
func call(fn uintptr, args ...uintptr) uintptr { return win32.Call(fn, args...) }

// cm calls vtable slot idx of obj, with call's rule.
//
//go:uintptrescapes
func cm(obj, idx uintptr, args ...uintptr) uintptr { return win32.Com(obj, idx, args...) }

// utf16At reads a NUL-terminated UTF-16 string the system owns.
func utf16At(p uintptr) string {
	n := 0
	for *(*uint16)(unsafe.Add(win32.Ptr(p), 2*n)) != 0 {
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice((*uint16)(win32.Ptr(p)), n))
}

// mustGUID parses "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx".
func mustGUID(s string) win32.GUID {
	p := strings.Split(s, "-")
	u := func(h string) uint64 {
		v, err := strconv.ParseUint(h, 16, 64)
		if err != nil {
			panic("notify: bad GUID " + s)
		}
		return v
	}
	if len(p) != 5 || len(p[3])+len(p[4]) != 16 {
		panic("notify: bad GUID " + s)
	}
	g := win32.GUID{Data1: uint32(u(p[0])), Data2: uint16(u(p[1])), Data3: uint16(u(p[2]))}
	rest := p[3] + p[4]
	for i := range g.Data4 {
		g.Data4[i] = byte(u(rest[2*i : 2*i+2]))
	}
	return g
}
