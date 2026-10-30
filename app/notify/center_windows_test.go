//go:build windows

package notify

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/testlock"
	"gonativeui/internal/win32"
	"gonativeui/platform"
)

// The toast's XML escapes the note's text, and the launch strings carry the
// key, action, thread and UserData both ways.
func TestToastXMLAndActivations(t *testing.T) {
	wrt.mu.Lock()
	wrt.scheme = "a3kb"
	wrt.cats = map[string]Category{"msg": {ID: "msg", Actions: []Action{
		{ID: 1, Title: "Reply", Reply: true, ReplyButton: "Send", ReplyPlaceholder: `Say "hi"`},
		{ID: 2, Title: "Delete <all>", Destructive: true},
	}}}
	wrt.mu.Unlock()
	t.Cleanup(func() { wrt.mu.Lock(); wrt.scheme, wrt.cats = "", nil; wrt.mu.Unlock() })
	x, err := toastXML(&Note{Title: "Ana & <Bob>", Body: "it's \"lunch\"\x01", Category: "msg", Thread: "t/1", UserData: []byte{0, 1, 2}}, 0xABC, `C:\tmp\a.png`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<text>Ana &amp; &lt;Bob&gt;</text>`, `<text>it's "lunch"</text>`, `activationType="protocol"`,
		`<input id="reply" type="text" placeHolderContent="Say &quot;hi&quot;"/>`, `content="Send"`, `hint-inputId="reply"`,
		`content="Delete &lt;all&gt;"`, `hint-buttonStyle="Critical"`, `<image src="file:///C:/tmp/a.png"/>`, `<audio silent="true"/>`,
		`launch="a3kb:gnui-notify?k=abc&amp;a=0&amp;t=`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("the XML lacks %s:\n%s", want, x)
		}
	}
	plain, _ := toastXML(&Note{Title: "x", Category: "msg", Sound: SoundDefault}, 0, "")
	if strings.Contains(plain, "<actions>") || strings.Contains(plain, "<audio") {
		t.Errorf("a plain note with the default sound: %s", plain)
	}
	for _, c := range []activation{{key: 1, action: ActionClick}, {key: ^uint64(0), action: 7, thread: "chat é", data: []byte("ud\x00")}} {
		u := activationURL("a3kb", c.key, c.action, c.thread, c.data)
		if !IsActivation(u) {
			t.Errorf("IsActivation(%q) = false", u)
		}
		got, ok := parseActivation(u)
		if !ok || got.key != c.key || got.action != c.action || got.thread != c.thread || !bytes.Equal(got.data, c.data) {
			t.Errorf("parse(%q) = %+v, %v; want %+v", u, got, ok, c)
		}
	}
	for _, s := range []string{"a3kb:other", `C:\x`, "gnui-notify?k=1", "https://x/gnui-notify?k=1"} {
		if IsActivation(s) {
			t.Errorf("IsActivation(%q) = true", s)
		}
	}
	if _, ok := parseActivation("a3kb:gnui-notify?k=zz"); ok {
		t.Error("a bad key parsed")
	}
}

// The toasts themselves, in a child process (the service, the AUMID and the
// WinRT thread are the process's): run on both CI runners.
func TestLiveToasts(t *testing.T) {
	if os.Getenv("GNUI_TOAST_CHILD") != "" {
		t.Skip("the child runs TestLiveToastsChild")
	}
	testlock.Hold(t, "activation") // its windows, menu or toasts take the foreground from other packages' tests
	cmd := exec.Command(os.Args[0], "-test.run=^TestLiveToastsChild$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), "GNUI_TOAST_CHILD=1")
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	if !bytes.Contains(out, []byte("--- PASS: TestLiveToastsChild")) {
		t.Fatal("the child did not pass")
	}
}

var (
	advapi             = syscall.NewLazyDLL("advapi32.dll")
	regCreateKeyExW    = advapi.NewProc("RegCreateKeyExW")
	regSetValueExW     = advapi.NewProc("RegSetValueExW")
	regDeleteTreeW     = advapi.NewProc("RegDeleteTreeW")
	regCloseKeyForTest = advapi.NewProc("RegCloseKey")
)

// registerTestAUMID writes the AUMID's key as install.Install would, and
// removes it at the end.
func registerTestAUMID(t *testing.T, id string) {
	t.Helper()
	sub, _ := syscall.UTF16PtrFromString(`Software\Classes\AppUserModelId\` + id)
	var hk syscall.Handle
	if rc, _, _ := regCreateKeyExW.Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(sub)), 0, 0, 0, 0x20006, 0, uintptr(unsafe.Pointer(&hk)), 0); rc != 0 {
		t.Fatalf("RegCreateKeyExW: %d", rc)
	}
	name, _ := syscall.UTF16PtrFromString("DisplayName")
	val, _ := syscall.UTF16FromString("gnui notify test")
	regSetValueExW.Call(uintptr(hk), uintptr(unsafe.Pointer(name)), 0, 1, uintptr(unsafe.Pointer(&val[0])), uintptr(2*len(val)))
	regCloseKeyForTest.Call(uintptr(hk))
	t.Cleanup(func() { regDeleteTreeW.Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(sub))) })
}

// readHistory is the action centre's toasts of the AUMID.
func readHistory(t *testing.T) []deliveredNote {
	t.Helper()
	var list []deliveredNote
	var err error
	run(func() { list, err = history() })
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// waitHistory waits up to 5 s for the history to hold exactly ids.
func waitHistory(t *testing.T, ids ...string) []deliveredNote {
	t.Helper()
	var list []deliveredNote
	deadline := time.Now().Add(5 * time.Second)
	for {
		list = readHistory(t)
		var got []string
		for _, d := range list {
			got = append(got, d.id)
		}
		slices.Sort(got)
		if slices.Equal(got, ids) || time.Now().After(deadline) {
			if !slices.Equal(got, ids) && !(len(got) == 0 && len(ids) == 0) {
				t.Fatalf("the history holds %q, want %q", got, ids)
			}
			return list
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestLiveToastsChild(t *testing.T) {
	if os.Getenv("GNUI_TOAST_CHILD") == "" {
		t.Skip("run by TestLiveToasts in a child process")
	}
	var rnd [6]byte
	rand.Read(rnd[:])
	id := "GonativeUI.NotifyTest." + hex.EncodeToString(rnd[:])
	if err := (nativeCenter{}).configure(Options{AppID: id}); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("configure before the AUMID is registered: %v, want ErrNotRegistered", err)
	}
	registerTestAUMID(t, id)
	const scheme = "gnui-notify-test"
	s := relaunchWith(t, Options{AppID: id, Scheme: scheme}, nativeCenter{})
	if !Available() {
		t.Fatal("not Available after Setup")
	}
	waitUntil(t, "the launch's listing", func() bool { return len(s.results()) > 0 })
	if r := s.results(); len(r) != 1 || r[0].Kind != ResultRestored || r[0].Restored != 0 {
		t.Fatalf("after Setup: %+v, want one empty ResultRestored", r)
	}
	s.reset()
	t.Cleanup(func() { // whatever a failure left in the action centre
		run(func() {
			var mgr2, hist uintptr
			if win32.RoGetActivationFactory("Windows.UI.Notifications.ToastNotificationManager", &iidToastManagerStatics2, &mgr2) >= 0 {
				if win32.HR(cm(mgr2, vtManager2History, uintptr(unsafe.Pointer(&hist)))) >= 0 {
					cm(hist, wi+6, uintptr(wrt.hid)) // IToastNotificationHistory::ClearWithId
					win32.Release(hist)
				}
				win32.Release(mgr2)
			}
		})
	})

	tk, err := Status()
	if err != nil {
		t.Fatal(err)
	}
	if st := waitResult(t, s, tk); st.Status != StatusAuthorized {
		t.Logf("Status: %d (the runner turned toasts off?)", st.Status)
	}
	if err := RegisterCategories(Category{ID: "msg", NotifyDismiss: true, Actions: []Action{{ID: 1, Title: "Reply", Reply: true}, {ID: 2, Title: "Open"}}}); err != nil {
		t.Fatal(err)
	}
	live0 := com.Leaks().Live

	// A note with actions: shown, kept with its three delegates, and listed
	// with its key and thread.
	r1 := mustPost(t, Note{ID: "n1", Thread: "chat-1", Title: "Ana & <Bob>", Body: "Lunch?", Category: "msg", UserData: []byte("ud-1")})
	if res := waitResult(t, s, r1.Request); res.ErrCode != 0 || res.Reservation != Reserved {
		t.Fatalf("the post: %+v", res)
	}
	if n := com.Leaks().Live - live0; n != 3 {
		t.Fatalf("%d delegates alive for one toast, want 3", n)
	}
	list := waitHistory(t, "n1")
	if list[0].key == 0 || list[0].thread != "chat-1" {
		t.Fatalf("listed as %+v", list[0])
	}
	key1 := list[0].key

	// The same ID again replaces it: one toast, one set of delegates.
	r2 := mustPost(t, Note{ID: "n1", Thread: "chat-1", Title: "Ana", Body: "Lunch at 1?", Category: "msg", UserData: []byte("ud-2")})
	if res := waitResult(t, s, r2.Request); res.ErrCode != 0 {
		t.Fatalf("the second post: %+v", res)
	}
	// A plain note keeps nothing.
	rp := mustPost(t, Note{ID: "p1", Title: "plain"})
	if res := waitResult(t, s, rp.Request); res.ErrCode != 0 || res.Reservation != NoResponse {
		t.Fatalf("the plain post: %+v", res)
	}
	list = waitHistory(t, "n1", "p1")
	if n := com.Leaks().Live - live0; n != 3 {
		t.Fatalf("%d delegates alive after a replacement and a plain toast, want 3", n)
	}
	var key2 uint64
	for _, d := range list {
		if d.id == "n1" {
			key2 = d.key
		}
	}
	if key2 == 0 || key2 == key1 {
		t.Fatalf("the replacement's key %x (the first's %x)", key2, key1)
	}

	// A click on an action after a restart: the protocol URL, handed over by
	// a second instance, is the response, once; other URLs still open.
	s.reset()
	u := activationURL(scheme, key2, 2, "chat-1", []byte("ud-2"))
	if !platform.OfferOpenURLs([]string{u, "https://example.com/x"}) || !platform.OfferOpenURLs([]string{u}) {
		t.Fatal("the offer was refused")
	}
	waitUntil(t, "the response", func() bool { return len(s.responses()) > 0 })
	time.Sleep(200 * time.Millisecond)
	drain()
	resp := s.responses()
	if len(resp) != 1 || resp[0].Action != 2 || string(resp[0].UserData) != "ud-2" || resp[0].Ticket != r2.Response {
		t.Fatalf("responses %+v, want one: action 2, ud-2, ticket %d", resp, r2.Response)
	}
	opened := false
	for i, e := range s.evs {
		if e.Kind == platform.AppEvOpenURLs && string(s.dataOf(i)) == "https://example.com/x" {
			opened = true
		}
	}
	if !opened {
		t.Fatal("the other URL was not delivered as EvOpenURLs")
	}

	// Removed: gone from the action centre, the delegates released.
	if err := Remove("n1", "p1"); err != nil {
		t.Fatal(err)
	}
	waitHistory(t)
	if n := com.Leaks().Live - live0; n != 0 {
		t.Fatalf("%d delegates alive after Remove, want 0", n)
	}

	// The runners time a toast out after a few seconds (P6): its Dismissed
	// event reaches the delegate, which keeps it (the action centre has it).
	ev0 := wrt.events.Load()
	r3 := mustPost(t, Note{ID: "n3", Title: "times out", Category: "msg", UserData: []byte("ud-3")})
	if res := waitResult(t, s, r3.Request); res.ErrCode != 0 {
		t.Fatalf("the third post: %+v", res)
	}
	deadline := time.Now().Add(20 * time.Second)
	for wrt.events.Load() == ev0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if wrt.events.Load() == ev0 {
		t.Error("no event from the toast in 20 s (P6 saw TimedOut within 7 s on both runners)")
	}
	if n := com.Leaks().Live - live0; n != 3 {
		t.Errorf("%d delegates alive after a timeout, want 3 (the toast is in the action centre)", n)
	}
	if err := Remove("n3"); err != nil {
		t.Fatal(err)
	}
	waitHistory(t)
	if c := com.Leaks(); c.Live != live0 || c.Stale != 0 {
		t.Fatalf("COM objects at the end: %+v (live at the start %d)", c, live0)
	}
	if st := ReadStats(); st.Reserved != 0 {
		t.Fatalf("%d records held at the end", st.Reserved)
	}
}
