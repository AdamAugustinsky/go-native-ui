//go:build windows

package applive

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"gonativeui/app"
	"gonativeui/internal/win32"
	"gonativeui/menu"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

// The drawn menu bar from a real keyboard on Windows (Gap 2 of a3-app's
// Windows parity): keys typed with SendInput, as a user types them, reach a
// window whose text field has the focus. An Alt tap and F10 move the focus to
// the bar and back, Alt+F opens File, S chooses Save, and a letter typed
// afterwards reaches the field: Windows did not enter the menu loop of a menu
// bar the window does not have (WM_SYSCOMMAND SC_KEYMENU), which would eat
// the keys that follow. It needs the foreground (the "activation" lock, held
// by child); a runner that does not give it skips.

const (
	vkAlt    = 0x12
	vkF10    = 0x79
	vkEscape = 0x1B
)

type keyInput struct {
	typ         uint32
	_           uint32
	vk, scan    uint16
	flags, time uint32
	extra       uintptr
	_           [8]byte
}

var (
	user32w     = win32.LoadSystem("user32.dll")
	pSendInput  = win32.Proc(user32w, "SendInput")
	pMapVK      = win32.Proc(user32w, "MapVirtualKeyW")
	pForeground = win32.Proc(user32w, "GetForegroundWindow")
	pWindowPID  = win32.Proc(user32w, "GetWindowThreadProcessId")
	typedInputs [8]keyInput
)

// typeKeys presses the virtual keys in order and releases them in reverse
// (a chord; one key is a press and release), with their scan codes, as a
// keyboard sends them. It reports whether the system took them all.
func typeKeys(vks ...uint16) bool {
	n := 0
	key := func(vk uint16, up bool) {
		k := keyInput{typ: 1, vk: vk, scan: uint16(win32.Call(pMapVK, uintptr(vk), 0))}
		if up {
			k.flags = 2 // KEYEVENTF_KEYUP
		}
		typedInputs[n] = k
		n++
	}
	for _, vk := range vks {
		key(vk, false)
	}
	for i := len(vks) - 1; i >= 0; i-- {
		key(vks[i], true)
	}
	return win32.Call(pSendInput, uintptr(n), uintptr(unsafe.Pointer(&typedInputs[0])), unsafe.Sizeof(typedInputs[0])) == uintptr(n)
}

// menuView is a window with a drawn menu bar and a focused text field.
type menuView struct {
	a     *app.App
	k     *widget.Kit
	m     *menu.Model
	field ui.TextBuffer
	fkey  ui.Key
	cmds  []menu.ID
	sem   ui.SemTree
}

func (v *menuView) Init(a *app.App) error {
	v.a = a
	v.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	v.k.SetMenu(v.m)
	return nil
}

func (v *menuView) Build(c *ui.Context) {
	k := v.k
	k.Begin(c)
	k.NextSize(widget.Fill, widget.Fill)
	k.Column("root")
	k.MenuBar()
	s := k.TextField("Name", &v.field, "Name…")
	if v.fkey == 0 {
		v.fkey = s.Key
		c.SetFocus(s.Key)
	}
	k.EndColumn()
	for id := menu.ID(1); id <= 3; id++ {
		if k.Command(id) {
			v.cmds = append(v.cmds, id)
		}
	}
	k.End()
}

// focus names what has the focus: a menu item's title, "field", or "other".
func (v *menuView) focus() string {
	c := v.a.UI
	f := c.FocusKey()
	if f == v.fkey {
		return "field"
	}
	c.Semantics(&v.sem)
	for _, n := range v.sem.Nodes {
		if n.Key == f && n.Role == ui.RoleMenuItem {
			return strings.ReplaceAll(string(v.sem.Text(n.Name)), " ", "_")
		}
	}
	return "other"
}

func init() {
	scenarios["menukeys"] = func() {
		m, err := menu.NewModel(
			[]menu.Command{{ID: 1, Title: "New"}, {ID: 2, Title: "Save"}, {ID: 3, Title: "Show Hidden"}},
			[]menu.Menu{
				{Title: "File", Items: []menu.Item{{Kind: menu.ItemCommand, Command: 1}, {Kind: menu.ItemCommand, Command: 2}}},
				{Title: "View", Items: []menu.Item{{Kind: menu.ItemCommand, Command: 3}}},
			}, []int{0, 1})
		if err != nil {
			fail("model: %v", err)
		}
		v := &menuView{m: m}
		const pause = 400 * time.Millisecond
		step := func(name string, keys ...uint16) func(*app.App) {
			return func(a *app.App) {
				say("%s=%s open=%v", name, v.focus(), v.k.MenuOpen())
				if len(keys) > 0 && !typeKeys(keys...) {
					fail("SendInput refused the keys after %s", name)
				}
				after(pause)
			}
		}
		h := &handler{}
		h.start = func(a *app.App) error {
			if _, err := a.OpenWindow(cfg, v); err != nil {
				return err
			}
			after(300 * time.Millisecond)
			return nil
		}
		h.steps = []func(*app.App){
			func(a *app.App) {
				v.a.Win.Activate()
				after(600 * time.Millisecond)
			},
			func(a *app.App) {
				var pid uint32
				win32.Call(pWindowPID, win32.Call(pForeground), uintptr(unsafe.Pointer(&pid)))
				if int(pid) != os.Getpid() {
					say("skip=foreground")
					a.Quit()
					return
				}
				step("start", vkAlt)(a) // an Alt tap
			},
			step("tap1", vkAlt),
			step("tap2", vkF10),
			step("f10", vkEscape),
			step("esc", vkAlt, 'F'),
			step("altF", 'S'),
			step("chose", 'X'),
			func(a *app.App) {
				say("end=%s open=%v cmds=%v text=%q", v.focus(), v.k.MenuOpen(), len(v.cmds), v.field.String())
				if slices.Equal(v.cmds, []menu.ID{2}) {
					say("saved=1")
				}
				a.Quit()
			},
		}
		if err := app.Main(app.AppConfig{ID: "dev.gonativeui.applive.menukeys"}, h); err != nil {
			fail("main: %v", err)
		}
	}
}

func TestMenuBarFromTheKeyboard(t *testing.T) {
	r := child(t, "menukeys", 60*time.Second)
	if r.get("skip") != "" {
		t.Skipf("the window did not get the foreground\n%s", r.out)
	}
	// one line per step, its first field naming it
	lines := map[string]string{}
	for _, l := range strings.Split(r.out, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "R ") {
			continue
		}
		f := strings.Fields(l[2:])
		if len(f) > 0 {
			if k, _, ok := strings.Cut(f[0], "="); ok {
				lines[k] = strings.Join(f, " ")
			}
		}
	}
	for _, c := range []struct{ key, want string }{
		{"start", "start=field open=false"},
		{"tap1", "tap1=File open=false"},
		{"tap2", "tap2=field open=false"},
		{"f10", "f10=File open=false"},
		{"esc", "esc=field open=false"},
		{"altF", "altF=New open=true"},
		{"chose", "chose=field open=false"},
	} {
		if lines[c.key] != c.want {
			t.Errorf("after the keys before %s: %q, want %q", c.key, lines[c.key], c.want)
		}
	}
	if r.get("saved") != "1" {
		t.Errorf("S in the File menu did not deliver Save once: %s", lines["end"])
	}
	if !strings.Contains(lines["end"], `text="x"`) || !strings.Contains(lines["end"], "end=field") {
		t.Errorf("the letter typed after the menus did not reach the field: %s\n%s", lines["end"], r.out)
	}
}
