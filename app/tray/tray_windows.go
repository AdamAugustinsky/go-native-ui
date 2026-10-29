//go:build windows

package tray

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/draw"
	_ "image/png" // Icon.PNG
	"os"
	"strings"
	"syscall"
	"unsafe"

	"gonativeui/app"
	"gonativeui/internal/win32"
	"gonativeui/menu"
	"gonativeui/platform"
)

// Windows: Shell_NotifyIconW (the phase 8 plan's W5), version 4, one
// notification-area icon per Item, owned by the hidden app window
// (platform.AppWindow), whose message handlers take the icons' callback
// message and TaskbarCreated (icons are added again once Explorer
// restarts). Each icon has a GUID made from the executable's path and the
// item's serial: Windows ties an icon's GUID to the executable that first
// used it, and a stale icon a crashed run left is deleted by GUID before
// the new one is added. Where the shell refuses the GUID the icon is added
// by (window, ID) alone.
//
// The menu is a popup menu (TrackPopupMenuEx) built from the model each time
// it opens, so its items' state is the model's then; the app window owns
// it, and its loop is a native modal loop in which the app's windows keep
// drawing (platform: WM_ENTERMENULOOP on the app window). A chosen command
// is delivered as the app's EvCommand (platform.DeliverAppCommand), a click
// as an EvService of platform.SourceTray through a ring, as on macOS. The
// menu is dark when the system's apps are, through uxtheme's undocumented
// ordinals (D76), unless Options.PublicAPIOnly or the gnui_publicapi tag.
//
// Reference: shellapi.h (NOTIFYICONDATAW, Shell_NotifyIconW,
// Shell_NotifyIconGetRect), winuser.h (TrackPopupMenuEx).

const (
	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nimSetVersion = 4

	nifMessage  = 0x01
	nifIcon     = 0x02
	nifTip      = 0x04
	nifState    = 0x08
	nifInfo     = 0x10
	nifGUID     = 0x20
	nifShowTip  = 0x80
	nisHidden   = 0x1
	niifInfo    = 0x1
	notifyV4    = 4
	ninSelect   = 0x400
	ninKeySel   = 0x401
	wmContext   = 0x007B
	wmNull      = 0x0000
	wmTrayEvent = 0x8000 + 0x5A // WM_APP+0x5A: the icons' callback message

	mfString    = 0x0000
	mfGrayed    = 0x0001
	mfChecked   = 0x0008
	mfPopup     = 0x0010
	mfSeparator = 0x0800

	tpmRightAlign   = 0x0008
	tpmBottomAlign  = 0x0020
	tpmNoNotify     = 0x0080
	tpmReturnCmd    = 0x0100
	tpmRightButton  = 0x0002
	smMenuDropAlign = 40
	smCxSmIcon      = 49
)

// notifyIconData is NOTIFYICONDATAW (the Vista+ layout with guidItem and
// hBalloonIcon): 976 bytes on 64-bit, as probe P7 read on both runners.
type notifyIconData struct {
	size        uint32
	hwnd        uintptr
	id          uint32
	flags       uint32
	callbackMsg uint32
	icon        uintptr
	tip         [128]uint16
	state       uint32
	stateMask   uint32
	info        [256]uint16
	version     uint32
	infoTitle   [64]uint16
	infoFlags   uint32
	guid        win32.GUID
	balloonIcon uintptr
}

var _ [976 - unsafe.Sizeof(notifyIconData{})]struct{}

// notifyIconIdentifier is NOTIFYICONIDENTIFIER.
type notifyIconIdentifier struct {
	size uint32
	hwnd uintptr
	id   uint32
	guid win32.GUID
}

var (
	shell32          = win32.LoadSystem("shell32.dll")
	user32           = win32.LoadSystem("user32.dll")
	pShellNotifyIcon = win32.Proc(shell32, "Shell_NotifyIconW")
	pExtractIconEx   = win32.Proc(shell32, "ExtractIconExW")
	pLoadIconW       = win32.Proc(user32, "LoadIconW")
	pCreatePopupMenu = win32.Proc(user32, "CreatePopupMenu")
	pAppendMenuW     = win32.Proc(user32, "AppendMenuW")
	pDestroyMenu     = win32.Proc(user32, "DestroyMenu")
	pTrackPopupMenu  = win32.Proc(user32, "TrackPopupMenuEx")
	pSetForeground   = win32.Proc(user32, "SetForegroundWindow")
	pPostMessageW    = win32.Proc(user32, "PostMessageW")
	pGetSysMetrics   = win32.Proc(user32, "GetSystemMetrics")
	pMetricsForDpi   = win32.Proc(user32, "GetSystemMetricsForDpi")
	pDpiForSystem    = win32.Proc(user32, "GetDpiForSystem")
	pRegGetValueW    = win32.Proc(win32.LoadSystem("advapi32.dll"), "RegGetValueW")
	personalizeKey   = mustUTF16(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`)
	systemLightName  = mustUTF16("SystemUsesLightTheme")
)

// taskbarLight reports whether the taskbar is light (SystemUsesLightTheme;
// missing, as before Windows 10 1903: dark).
func taskbarLight() bool {
	const rrfRtDWORD = 0x10
	tr.dword, tr.dwordLen = 0, 4
	r := call(pRegGetValueW, uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(personalizeKey)),
		uintptr(unsafe.Pointer(systemLightName)), rrfRtDWORD, 0, uintptr(unsafe.Pointer(&tr.dword)), uintptr(unsafe.Pointer(&tr.dwordLen)))
	return r == 0 && tr.dword != 0
}

func mustUTF16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

var tr struct {
	registered bool
	ring       *platform.Ring
	items      []*Item // in the order they were added
	next       uint32
	exe        string
	readds     int     // icons added again after TaskbarCreated (tests)
	menus      int     // menus tracked (tests)
	lastMenu   uintptr // the HMENU being tracked, 0 when none (tests)
	dword      uint32  // registry reads' out parameters (heap)
	dwordLen   uint32
}

// Item is one notification-area icon.
type Item struct {
	serial    uint32
	nid       notifyIconData // the call's argument, kept with the item (heap)
	withGUID  bool
	added     bool
	icon      uintptr // the HICON shown
	ownIcon   bool    // icon is ours to destroy
	title     string
	tooltip   string
	model     *menu.Model
	menuIdx   int
	rightMenu bool
	hidden    bool
	public    bool
	removed   bool
}

// register makes the click ring and takes the app window's messages, once.
func register() {
	if tr.registered {
		return
	}
	tr.registered = true
	tr.ring = platform.NewRing(platform.SourceTray, 16, 64)
	tr.exe, _ = os.Executable()
	platform.HandleAppMessage(wmTrayEvent, onTrayEvent)
	if m := platform.TaskbarCreatedMessage(); m != 0 {
		platform.HandleAppMessage(m, onTaskbarCreated)
	}
}

// New adds an icon to the notification area.
func New(o Options) (*Item, error) {
	if !platform.AppStarted() || platform.AppWindow() == 0 {
		return nil, ErrNotStarted
	}
	register()
	icon, own, err := makeIcon(o.Icon)
	if err != nil {
		return nil, err
	}
	tr.next++
	t := &Item{serial: tr.next, icon: icon, ownIcon: own, title: o.Title, tooltip: o.Tooltip,
		rightMenu: o.RightClickMenu, hidden: o.Hidden, public: o.PublicAPIOnly}
	t.nid.guid = itemGUID(tr.exe, t.serial)
	if !t.add() {
		t.dropIcon()
		return nil, ErrUnsupported
	}
	tr.items = append(tr.items, t)
	return t, nil
}

// itemGUID is the GUID of the serial-th icon of the executable at exe.
func itemGUID(exe string, serial uint32) win32.GUID {
	h := sha256.Sum256(fmt.Appendf(nil, "gonativeui.tray\x00%s\x00%d", strings.ToLower(exe), serial))
	g := win32.GUID{
		Data1: uint32(h[0])<<24 | uint32(h[1])<<16 | uint32(h[2])<<8 | uint32(h[3]),
		Data2: uint16(h[4])<<8 | uint16(h[5]),
		Data3: (uint16(h[6])<<8|uint16(h[7]))&0x0FFF | 0x5000, // version 5 (name-based)
	}
	copy(g.Data4[:], h[8:16])
	g.Data4[0] = g.Data4[0]&0x3F | 0x80 // the RFC 4122 variant
	return g
}

// fill sets the call's argument from the item's state.
func (t *Item) fill(flags uint32) {
	guid := t.nid.guid
	t.nid = notifyIconData{hwnd: platform.AppWindow(), id: t.serial, guid: guid, callbackMsg: wmTrayEvent, icon: t.icon}
	t.nid.size = uint32(unsafe.Sizeof(t.nid))
	if t.withGUID {
		flags |= nifGUID
	}
	t.nid.flags = flags
	tip := t.tooltip
	if t.title != "" {
		if tip != "" {
			tip += "\n"
		}
		tip += t.title // Windows shows no text beside the icon: the title is in the tooltip
	}
	putText(t.nid.tip[:], tip)
	if t.hidden {
		t.nid.state = nisHidden
	}
	t.nid.stateMask = nisHidden
}

func (t *Item) shell(msg uintptr) bool {
	return win32.Bool(call(pShellNotifyIcon, msg, uintptr(unsafe.Pointer(&t.nid))))
}

// add adds the icon (again, after TaskbarCreated) and sets version 4.
func (t *Item) add() bool {
	all := uint32(nifMessage | nifIcon | nifTip | nifShowTip | nifState)
	t.withGUID = true
	t.fill(0)
	t.shell(nimDelete) // a stale icon of a crashed run
	t.fill(all)
	if !t.shell(nimAdd) {
		t.withGUID = false // the shell refuses the GUID (another path claimed it): by (window, ID)
		t.fill(all)
		if !t.shell(nimAdd) {
			return false
		}
	}
	t.added = true
	t.fill(0)
	t.nid.version = notifyV4
	t.shell(nimSetVersion)
	return true
}

// modify applies the item's state with flags.
func (t *Item) modify(flags uint32) error {
	t.fill(flags | nifTip | nifShowTip)
	if !t.shell(nimModify) {
		return ErrUnsupported
	}
	return nil
}

func putText(dst []uint16, s string) {
	u, _ := syscall.UTF16FromString(strings.ReplaceAll(s, "\x00", ""))
	if len(u) > len(dst) {
		u = u[:len(dst)-1]
		if n := len(u); n > 0 && u[n-1] >= 0xD800 && u[n-1] < 0xDC00 {
			u = u[:n-1] // half a surrogate pair
		}
		u = append(u, 0)
	}
	copy(dst, u)
}

// iconSize is the notification area's icon size at the system DPI.
func iconSize() int {
	if pMetricsForDpi != 0 && pDpiForSystem != 0 {
		if n := int(call(pMetricsForDpi, smCxSmIcon, call(pDpiForSystem))); n > 0 {
			return n
		}
	}
	if n := int(call(pGetSysMetrics, smCxSmIcon)); n > 0 {
		return n
	}
	return 16
}

// makeIcon makes the HICON for ic: the PNG scaled to the notification area's
// size (a template recoloured for the taskbar's theme), or, for a symbol
// (macOS only) or no image, the executable's own small icon.
func makeIcon(ic Icon) (icon uintptr, own bool, err error) {
	if len(ic.PNG) > 0 {
		img, _, err := image.Decode(bytes.NewReader(ic.PNG))
		if err != nil {
			return 0, false, ErrImage
		}
		n := iconSize()
		px := scaled(img, n)
		if ic.Template {
			c := uint32(0x000000)
			if !taskbarLight() {
				c = 0xFFFFFF
			}
			for i, p := range px {
				px[i] = p&0xFF000000 | c
			}
		}
		if icon = win32.IconFromPixels(n, n, px); icon == 0 {
			return 0, false, ErrImage
		}
		return icon, true, nil
	}
	if tr.exe != "" && pExtractIconEx != 0 {
		var small uintptr
		if p, err := syscall.UTF16PtrFromString(tr.exe); err == nil &&
			call(pExtractIconEx, uintptr(unsafe.Pointer(p)), 0, 0, uintptr(unsafe.Pointer(&small)), 1) > 0 && small != 0 {
			return small, true, nil
		}
	}
	const idiApplication = 32512
	return call(pLoadIconW, 0, idiApplication), false, nil // shared: never destroyed
}

// scaled draws img into an n×n square, centred with its aspect ratio, as
// straight-alpha 0xAARRGGBB, averaging the source pixels each one covers.
func scaled(img image.Image, n int) []uint32 {
	src := image.NewNRGBA(img.Bounds())
	draw.Draw(src, src.Rect, img, img.Bounds().Min, draw.Src)
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	out := make([]uint32, n*n)
	if sw == 0 || sh == 0 {
		return out
	}
	scale := float64(n) / float64(max(sw, sh))
	dw, dh := max(1, int(float64(sw)*scale+0.5)), max(1, int(float64(sh)*scale+0.5))
	ox, oy := (n-dw)/2, (n-dh)/2
	for y := 0; y < dh; y++ {
		y0, y1 := y*sh/dh, max((y+1)*sh/dh, y*sh/dh+1)
		for x := 0; x < dw; x++ {
			x0, x1 := x*sw/dw, max((x+1)*sw/dw, x*sw/dw+1)
			var r, g, b, a, cnt uint64
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[sy*src.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					pa := uint64(p[3])
					r, g, b, a = r+uint64(p[0])*pa, g+uint64(p[1])*pa, b+uint64(p[2])*pa, a+pa
					cnt++
				}
			}
			if a == 0 {
				continue
			}
			out[(oy+y)*n+ox+x] = uint32(a/cnt)<<24 | uint32(r/a)<<16 | uint32(g/a)<<8 | uint32(b/a)
		}
	}
	return out
}

func (t *Item) dropIcon() {
	if t.ownIcon {
		win32.DestroyIcon(t.icon)
	}
	t.icon, t.ownIcon = 0, false
}

// SetIcon changes the image; an Icon with no PNG shows the executable's
// icon (Windows has no SF Symbols).
func (t *Item) SetIcon(ic Icon) error {
	if t.removed {
		return ErrRemoved
	}
	icon, own, err := makeIcon(ic)
	if err != nil {
		return err
	}
	old, oldOwn := t.icon, t.ownIcon
	t.icon, t.ownIcon = icon, own
	err = t.modify(nifIcon)
	if oldOwn {
		win32.DestroyIcon(old)
	}
	return err
}

// SetTitle changes the text shown with the icon: in its tooltip on
// Windows, after the Tooltip on a line of its own.
func (t *Item) SetTitle(s string) error {
	if t.removed {
		return ErrRemoved
	}
	t.title = s
	return t.modify(0)
}

// SetTooltip changes the tooltip.
func (t *Item) SetTooltip(s string) error {
	if t.removed {
		return ErrRemoved
	}
	t.tooltip = s
	return t.modify(0)
}

// SetVisible shows or hides the icon, keeping it.
func (t *Item) SetVisible(on bool) error {
	if t.removed {
		return ErrRemoved
	}
	t.hidden = !on
	return t.modify(nifState)
}

// Visible reports whether the icon is shown (not hidden or removed).
func (t *Item) Visible() bool { return !t.removed && !t.hidden }

// SetMenu makes menu idx of m the icon's menu; nil m removes it. The menu is
// built from the model each time it opens.
func (t *Item) SetMenu(m *menu.Model, idx int) error {
	if t.removed {
		return ErrRemoved
	}
	if m != nil && (idx < 0 || idx >= len(m.Menus()) || len(m.Menus()[idx].Items) == 0) {
		return ErrMenu
	}
	t.model, t.menuIdx = m, idx
	return nil
}

// Clicked decodes a click on this item from an app event.
func (t *Item) Clicked(e *app.Event) (Click, bool) {
	if e.Kind != app.EvService || e.Source != platform.SourceTray || uint32(e.A) != t.serial {
		return Click{}, false
	}
	return Click{Right: e.B&1 != 0}, true
}

// Balloon shows a balloon from the icon (NIF_INFO): the fallback for an app
// whose toasts are not registered or are turned off. Windows 10 and 11 show
// it as a toast of the shell's; Focus Assist may hold it back.
func (t *Item) Balloon(title, body string) error {
	if t.removed {
		return ErrRemoved
	}
	t.fill(nifInfo | nifTip | nifShowTip)
	putText(t.nid.infoTitle[:], title)
	putText(t.nid.info[:], body)
	t.nid.infoFlags = niifInfo
	if !t.shell(nimModify) {
		return ErrUnsupported
	}
	return nil
}

// Remove takes the icon off the notification area and frees it. Later calls
// return ErrRemoved; a second Remove does nothing.
func (t *Item) Remove() {
	if t.removed {
		return
	}
	t.removed = true
	t.fill(0)
	t.shell(nimDelete)
	t.dropIcon()
	t.model = nil
	for i, it := range tr.items {
		if it == t {
			tr.items = append(tr.items[:i], tr.items[i+1:]...)
			break
		}
	}
}

// onTaskbarCreated adds the icons again: Explorer restarted.
func onTaskbarCreated(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	for _, t := range tr.items {
		if t.add() {
			tr.readds++
		}
	}
	return 0
}

// onTrayEvent is an icon's callback message (version 4: the event in
// lParam's low word, the icon's ID in its high word, the anchor point in
// wParam).
func onTrayEvent(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	ev, id := uint32(lp&0xFFFF), uint32(lp>>16&0xFFFF)
	var t *Item
	for _, it := range tr.items {
		if it.serial == id || len(tr.items) == 1 {
			t = it
			break
		}
	}
	if t == nil || t.removed {
		return 0
	}
	right := false
	switch ev {
	case ninSelect, ninKeySel:
	case wmContext:
		right = true
	default:
		return 0
	}
	x, y := int32(int16(wp&0xFFFF)), int32(int16(wp>>16&0xFFFF))
	if t.model != nil && (right || !t.rightMenu) {
		t.track(x, y)
		return 0
	}
	var b int64
	if right {
		b = 1
	}
	tr.ring.Offer(int64(t.serial), b, nil)
	return 0
}

// track shows the menu at (x, y) and delivers the chosen command.
func (t *Item) track(x, y int32) {
	h := buildMenu(t.model, t.menuIdx, 0)
	if h == 0 {
		return
	}
	defer call(pDestroyMenu, h) // and its submenus
	app := platform.AppWindow()
	if !t.public {
		darkMenus(app)
	}
	flags := uintptr(tpmReturnCmd | tpmNoNotify | tpmRightButton | tpmBottomAlign)
	if call(pGetSysMetrics, smMenuDropAlign) != 0 {
		flags |= tpmRightAlign
	}
	// The owner must be in the foreground for the menu to close when the
	// user clicks elsewhere; the posted WM_NULL lets it close at once
	// (KB135788).
	call(pSetForeground, app)
	tr.menus++
	tr.lastMenu = h
	cmd := call(pTrackPopupMenu, h, flags, uintptr(x), uintptr(y), app, 0)
	tr.lastMenu = 0
	call(pPostMessageW, app, wmNull, 0, 0)
	if id := uint16(cmd); id != 0 {
		platform.DeliverAppCommand(id)
	}
}

// buildMenu makes the popup menu of menus[idx] of m, its submenus included
// (depth bounds a model whose submenus refer to each other).
func buildMenu(m *menu.Model, idx, depth int) uintptr {
	ms := m.Menus()
	if idx < 0 || idx >= len(ms) || depth > 8 {
		return 0
	}
	h := call(pCreatePopupMenu)
	if h == 0 {
		return 0
	}
	sep := false // a separator is wanted before the next item
	n := 0
	for _, it := range ms[idx].Items {
		switch it.Kind {
		case menu.ItemSeparator:
			sep = n > 0
			continue
		case menu.ItemSubmenu:
			sub := buildMenu(m, it.Submenu, depth+1)
			if sub == 0 {
				continue
			}
			title := it.Title
			if title == "" && it.Submenu < len(ms) {
				title = ms[it.Submenu].Title
			}
			if sep {
				call(pAppendMenuW, h, mfSeparator, 0, 0)
				sep = false
			}
			appendItem(h, mfPopup, sub, title)
			n++
		case menu.ItemCommand:
			cmd, ok := m.Command(it.Command)
			if !ok {
				continue
			}
			title := cmd.Title
			if title == "" {
				title = menu.RoleTitle(cmd.Role)
			}
			if title == "" || cmd.ID == 0 || cmd.ID > 0xFFFF {
				continue
			}
			flags := uintptr(mfString)
			if !m.Enabled(cmd.ID) {
				flags |= mfGrayed
			}
			if m.Checked(cmd.ID) {
				flags |= mfChecked
			}
			if sep {
				call(pAppendMenuW, h, mfSeparator, 0, 0)
				sep = false
			}
			appendItem(h, flags, uintptr(cmd.ID), title)
			n++
		}
	}
	return h
}

// appendItem appends an item titled title; "&" is doubled (Windows reads one
// as the mnemonic's mark).
func appendItem(h, flags, id uintptr, title string) {
	u, err := syscall.UTF16PtrFromString(strings.ReplaceAll(strings.ReplaceAll(title, "\x00", ""), "&", "&&"))
	if err != nil {
		return
	}
	call(pAppendMenuW, h, flags, id, uintptr(unsafe.Pointer(u)))
}

// call calls fn; what a pointer argument converted in the call expression
// points to is moved to the heap.
//
//go:uintptrescapes
func call(fn uintptr, args ...uintptr) uintptr { return win32.Call(fn, args...) }
