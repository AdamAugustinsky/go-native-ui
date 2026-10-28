//go:build windows

package tray

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gonativeui/app"
	"gonativeui/internal/win32"
	"gonativeui/menu"
	"gonativeui/platform"
)

// The notification-area icon (W5) on the CI runners, in a child process
// running app.Main with no window: the icon read back from the shell
// (Shell_NotifyIconGetRect), clicks and the menu driven through the icon's
// own callback message, the menu's items read back while it is open and
// one chosen with the keyboard, TaskbarCreated, and the GDI, USER and idle
// budgets. How it looks, light and dark, is the hand checklist's.

func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	if os.Getenv("GNUI_TRAY_CHILD") != "" {
		trayChild()
		return
	}
	os.Exit(m.Run())
}

func TestNotifyIconDataSize(t *testing.T) {
	if n := unsafe.Sizeof(notifyIconData{}); n != 976 {
		t.Fatalf("NOTIFYICONDATAW is %d bytes, want 976", n)
	}
	if n := unsafe.Sizeof(notifyIconIdentifier{}); n != 40 {
		t.Fatalf("NOTIFYICONIDENTIFIER is %d bytes, want 40", n)
	}
	a, b := itemGUID(`C:\x\app.exe`, 1), itemGUID(`c:\X\APP.EXE`, 1)
	if a != b || a == itemGUID(`C:\x\app.exe`, 2) || a == itemGUID(`C:\y\app.exe`, 1) {
		t.Fatal("the GUID is not one per executable path and serial")
	}
}

func TestMenuBuiltFromTheModel(t *testing.T) {
	m := testModel(t)
	m.SetEnabled(cmdQuit, false)
	m.SetChecked(cmdPause, true)
	h := buildMenu(m, 0, 0)
	if h == 0 {
		t.Fatal("no menu")
	}
	defer call(pDestroyMenu, h)
	got := readMenu(h)
	want := "Open && show|-|Pause [checked]|Quit [grayed]|More>[About]"
	if got != want {
		t.Fatalf("the menu reads %q, want %q", got, want)
	}
}

func TestScaledIcon(t *testing.T) {
	px := scaled(testImage(64, 32), 16)
	if a := px[0] >> 24; a != 0 {
		t.Errorf("the letterbox's top-left alpha is %d, want 0 (a 2:1 image in a square)", a)
	}
	if p := px[8*16+8]; p>>24 != 0xFF || p>>16&0xFF < 0xF0 || p&0xFF > 0x10 {
		t.Errorf("the centre is %08X, want opaque red", p)
	}
}

const (
	cmdOpen menu.ID = iota + 1
	cmdPause
	cmdQuit
	cmdAbout
)

func testModel(t interface{ Fatal(...any) }) *menu.Model {
	m, err := menu.NewModel(
		[]menu.Command{{ID: cmdOpen, Title: "Open & show"}, {ID: cmdPause, Title: "Pause"}, {ID: cmdQuit, Title: "Quit"}, {ID: cmdAbout, Title: "About"}},
		[]menu.Menu{
			{Title: "Tray", Items: []menu.Item{
				{Kind: menu.ItemCommand, Command: cmdOpen}, {Kind: menu.ItemSeparator}, {Kind: menu.ItemSeparator},
				{Kind: menu.ItemCommand, Command: cmdPause}, {Kind: menu.ItemCommand, Command: cmdQuit},
				{Kind: menu.ItemSubmenu, Submenu: 1}, {Kind: menu.ItemSeparator},
			}},
			{Title: "More", Items: []menu.Item{{Kind: menu.ItemCommand, Command: cmdAbout}}},
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

var (
	pGetMenuItemCount = win32.Proc(user32, "GetMenuItemCount")
	pGetMenuStringW   = win32.Proc(user32, "GetMenuStringW")
	pGetMenuState     = win32.Proc(user32, "GetMenuState")
	pGetSubMenu       = win32.Proc(user32, "GetSubMenu")
	pFindWindowW      = win32.Proc(user32, "FindWindowW")
	pSetTimer         = win32.Proc(user32, "SetTimer")
	pKillTimer        = win32.Proc(user32, "KillTimer")
	pEndMenu          = win32.Proc(user32, "EndMenu")
	pSendMessageW     = win32.Proc(user32, "SendMessageW")
	pNotifyIconRect   = win32.Proc(shell32, "Shell_NotifyIconGetRect")
	pGetGuiResources  = win32.Proc(user32, "GetGuiResources")
	pGetCurrentProc   = win32.Proc(win32.LoadSystem("kernel32.dll"), "GetCurrentProcess")
)

var menuText [128]uint16

// readMenu reads a menu back: titles, "-" for separators, states and
// submenus.
func readMenu(h uintptr) string {
	const byPos, mfGrayedState, mfCheckedState, mfSep = 0x400, 0x1, 0x8, 0x800
	n := int(int32(call(pGetMenuItemCount, h)))
	var parts []string
	for i := 0; i < n; i++ {
		st := call(pGetMenuState, h, uintptr(i), byPos)
		if st&mfSep != 0 {
			parts = append(parts, "-")
			continue
		}
		k := call(pGetMenuStringW, h, uintptr(i), uintptr(unsafe.Pointer(&menuText[0])), uintptr(len(menuText)), byPos)
		s := syscall.UTF16ToString(menuText[:k])
		if sub := call(pGetSubMenu, h, uintptr(i)); sub != 0 {
			s += ">[" + readMenu(sub) + "]"
		}
		if st&mfCheckedState != 0 {
			s += " [checked]"
		}
		if st&mfGrayedState != 0 {
			s += " [grayed]"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "|")
}

func testImage(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	return img
}

func testPNG() []byte {
	var b bytes.Buffer
	png.Encode(&b, testImage(32, 32))
	return b.Bytes()
}

func TestTrayLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "GNUI_TRAY_CHILD=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		if line, ok := strings.CutPrefix(sc.Text(), "R "); ok {
			for _, f := range strings.Fields(line) {
				if k, v, ok := strings.Cut(f, "="); ok {
					kv[k] = v
				}
			}
		}
	}
	t.Logf("%s", out.String())
	if err != nil || ctx.Err() != nil {
		t.Fatalf("child: %v", err)
	}
	for k, want := range map[string]string{
		"new_err": "nil", "rect_hr": "0x00000000", "left_click": "left", "right_click": "right",
		"menu_items": "Open_&&_show|-|Pause_[checked]|Quit_[grayed]|More>[About]", "command": strconv.Itoa(int(cmdOpen)),
		"menus": "1", "readds": "1", "rect_after_readd": "0x00000000", "visible_after_hide": "false",
		"balloon_err": "nil", "title_err": "nil", "removed_rect_ok": "false", "after_remove": "tray:_the_item_was_removed",
	} {
		if kv[k] != want {
			t.Errorf("%s = %q, want %q", k, kv[k], want)
		}
	}
	if kv["with_guid"] != "true" {
		t.Logf("the icon was added by (window, ID): the shell refused its GUID")
	}
	for _, k := range []string{"gdi_growth", "user_growth"} {
		if n, err := strconv.Atoi(kv[k]); err != nil || n > 2 {
			t.Errorf("%s = %q over 20 icons added and removed, want ≤ 2", k, kv[k])
		}
	}
	// W5's gate: idle with a tray icon, ≤ 1 wakeup/s (3 s, and the step's
	// own post).
	if n, err := strconv.Atoi(kv["idle_pumps"]); err != nil || n > 4 {
		t.Errorf("%q pump turns in 3 s idle with a tray icon, want ≤ 4", kv["idle_pumps"])
	}
}

// ---- The child ----

func say(format string, args ...any) { fmt.Printf("R "+format+"\n", args...) }

func errText(err error) string {
	if err == nil {
		return "nil"
	}
	return strings.ReplaceAll(err.Error(), " ", "_")
}

type steps struct {
	app.DefaultHandler
	start  func(a *app.App) error
	list   []func(a *app.App)
	next   int
	item   *Item
	clicks []string
	cmd    uint64
}

func (h *steps) Start(a *app.App) error { return h.start(a) }

func (h *steps) Event(a *app.App, e *app.Event) {
	switch {
	case e.Kind == app.EvPost && e.Source == platform.SourceApp && h.next < len(h.list):
		h.next++
		h.list[h.next-1](a)
		return
	case e.Kind == app.EvCommand:
		h.cmd = e.ID
		return
	}
	if h.item != nil {
		if c, ok := h.item.Clicked(e); ok {
			if c.Right {
				h.clicks = append(h.clicks, "right")
			} else {
				h.clicks = append(h.clicks, "left")
			}
			return
		}
	}
	a.DefaultEvent(e)
}

var posts = make(chan time.Duration, 8)

func after(d time.Duration) { posts <- d }

func poster() {
	runtime.LockOSThread()
	for d := range posts {
		time.Sleep(d)
		for !app.Post(app.Event{Source: platform.SourceApp}, nil) {
			time.Sleep(time.Millisecond)
		}
	}
}

var rectOut [4]int32

func iconRect(t *Item) string {
	id := notifyIconIdentifier{hwnd: platform.AppWindow(), id: t.serial}
	if t.withGUID {
		id.guid = t.nid.guid
	}
	id.size = uint32(unsafe.Sizeof(id))
	hr := call(pNotifyIconRect, uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&rectOut)))
	return fmt.Sprintf("0x%08X", uint32(hr))
}

func guiObjects() (gdi, user int) {
	p := call(pGetCurrentProc)
	return int(call(pGetGuiResources, p, 0)), int(call(pGetGuiResources, p, 1))
}

// menuDriver runs inside the menu's loop (a thread timer on the main
// thread): it reads the menu back, then picks its first item with the
// keyboard; a second tick ends the menu if that did not.
var (
	menuSeen string
	menuTick int
	menuCB   = syscall.NewCallback(func(hwnd, msg, id, _ uintptr) uintptr {
		menuTick++
		call(pKillTimer, 0, id)
		switch menuTick {
		case 1:
			if tr.lastMenu != 0 {
				menuSeen = readMenu(tr.lastMenu)
			}
			cls, _ := syscall.UTF16PtrFromString("#32768")
			if w := call(pFindWindowW, uintptr(unsafe.Pointer(cls)), 0); w != 0 {
				const wmKeyDown, vkDown, vkReturn = 0x0100, 0x28, 0x0D
				call(pPostMessageW, w, wmKeyDown, vkDown, 0)
				call(pPostMessageW, w, wmKeyDown, vkReturn, 0)
			}
		default:
			call(pEndMenu)
		}
		return 0
	})
)

func trayChild() {
	time.AfterFunc(75*time.Second, func() { fmt.Println("FAIL watchdog"); os.Exit(3) })
	go poster()
	var gdi0, user0 int
	var p0 uint64
	h := &steps{}
	post := func(t *Item, ev uint32) {
		call(pPostMessageW, platform.AppWindow(), wmTrayEvent, 100|200<<16, uintptr(ev|t.serial<<16))
	}
	h.start = func(a *app.App) error {
		t, err := New(Options{Icon: PNGIcon(testPNG(), false), Tooltip: "gnui tray test", Title: "00:01", RightClickMenu: true})
		say("new_err=%s", errText(err))
		if err != nil {
			a.Quit()
			return nil
		}
		h.item = t
		say("with_guid=%v rect_hr=%s", t.withGUID, iconRect(t))
		post(t, ninSelect)
		post(t, wmContext) // no menu yet: a right click is an event
		after(400 * time.Millisecond)
		return nil
	}
	h.list = []func(*app.App){
		func(a *app.App) {
			say("left_click=%s right_click=%s", strings.Join(h.clicks[:min(1, len(h.clicks))], ","), strings.Join(h.clicks[min(1, len(h.clicks)):], ","))
			m := testModel(panicker{})
			m.SetEnabled(cmdQuit, false)
			m.SetChecked(cmdPause, true)
			h.item.SetMenu(m, 0)
			call(pSetTimer, 0, 0, 300, menuCB)
			call(pSetTimer, 0, 0, 2500, menuCB)
			post(h.item, wmContext) // the menu, tracked when the pump dispatches it
			after(3500 * time.Millisecond)
		},
		func(a *app.App) {
			say("menu_items=%s command=%d menus=%d", strings.ReplaceAll(menuSeen, " ", "_"), h.cmd, tr.menus)
			call(pSendMessageW, platform.AppWindow(), uintptr(platform.TaskbarCreatedMessage()), 0, 0)
			say("readds=%d rect_after_readd=%s", tr.readds, iconRect(h.item))
			h.item.SetVisible(false)
			say("visible_after_hide=%v rect_hidden=%s", h.item.Visible(), iconRect(h.item))
			h.item.SetVisible(true)
			say("balloon_err=%s title_err=%s", errText(h.item.Balloon("Saved", "Uploading…")), errText(h.item.SetTitle("00:02")))
			gdi0, user0 = guiObjects()
			for i := 0; i < 20; i++ {
				t, err := New(Options{Icon: PNGIcon(testPNG(), i%2 == 0)})
				if err != nil {
					say("cycle_err=%s", errText(err))
					break
				}
				t.Remove()
			}
			gdi, user := guiObjects()
			say("gdi_growth=%d user_growth=%d", gdi-gdi0, user-user0)
			p0, _ = platform.PumpStats()
			after(3 * time.Second)
		},
		func(a *app.App) {
			p1, _ := platform.PumpStats()
			say("idle_pumps=%d", p1-p0)
			h.item.Remove()
			h.item.Remove()
			say("removed_rect_ok=%v after_remove=%s", iconRect(h.item) == "0x00000000", errText(h.item.SetTitle("x")))
			a.Quit()
		},
	}
	if err := app.Main(app.AppConfig{ID: "dev.gonativeui.traytest", KeepRunning: true, Policy: app.PolicyAccessory}, h); err != nil {
		fmt.Println("FAIL main:", err)
		os.Exit(2)
	}
}

type panicker struct{}

func (panicker) Fatal(args ...any) { panic(fmt.Sprint(args...)) }
