//go:build darwin

package tray

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/childapp"
	"gonativeui/internal/objc"
	"gonativeui/menu"
)

func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	childapp.Register("tray", scenarioTray)
	if childapp.Serve() {
		return
	}
	os.Exit(m.Run())
}

func must(ok bool, format string, args ...any) {
	if !ok {
		childapp.Fail(format, args...)
	}
}

func str(id objc.ID) string { return string(objc.AppendUTF8(nil, id)) }

func samplePNG() []byte {
	im := image.NewNRGBA(image.Rect(0, 0, 36, 36))
	for y := 8; y < 28; y++ {
		for x := 8; x < 28; x++ {
			im.Set(x, y, color.NRGBA{A: 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, im)
	return b.Bytes()
}

// trayModel: Open, Pause (disabled), a separator, Sound (checked) and a
// submenu with one more command; Quit has a role and no title.
func trayModel() *menu.Model {
	m, err := menu.NewModel([]menu.Command{
		{ID: 11, Title: "Open", Scope: menu.ScopeApp, Symbol: "macwindow"},
		{ID: 12, Title: "Pause", Scope: menu.ScopeApp},
		{ID: 13, Title: "Sound", Scope: menu.ScopeApp},
		{ID: 14, Title: "Deep", Scope: menu.ScopeApp},
		{ID: 15, Scope: menu.ScopeApp, Role: menu.RoleQuit},
	}, []menu.Menu{
		{Title: "Tray", Items: []menu.Item{
			{Kind: menu.ItemSeparator},
			{Kind: menu.ItemCommand, Command: 11}, {Kind: menu.ItemCommand, Command: 12},
			{Kind: menu.ItemSeparator}, {Kind: menu.ItemSeparator},
			{Kind: menu.ItemCommand, Command: 13},
			{Kind: menu.ItemSubmenu, Submenu: 1, Title: "More"},
			{Kind: menu.ItemSeparator},
			{Kind: menu.ItemCommand, Command: 15},
			{Kind: menu.ItemSeparator},
		}},
		{Title: "More", Items: []menu.Item{{Kind: menu.ItemCommand, Command: 14}}},
	}, nil)
	if err != nil {
		childapp.Fail("model: %v", err)
	}
	m.SetEnabled(12, false)
	m.SetChecked(13, true)
	return m
}

type scenario struct {
	t1, t2 *Item
	cmds   []uint64
	clicks []Click
}

func (s *scenario) Start(a *app.App) error {
	time.AfterFunc(50*time.Millisecond, func() { app.Post(app.Event{ID: 1}, nil) })
	return nil
}

func (s *scenario) Event(a *app.App, e *app.Event) {
	switch {
	case e.Kind == app.EvCommand:
		s.cmds = append(s.cmds, e.ID)
	case e.Kind == app.EvService && s.t2 != nil:
		if c, ok := s.t2.Clicked(e); ok {
			s.clicks = append(s.clicks, c)
		} else if _, ok := s.t1.Clicked(e); ok {
			childapp.Fail("a click on the other item")
		}
	case e.Kind == app.EvPost && e.ID == 1:
		s.setup()
		time.AfterFunc(200*time.Millisecond, func() { app.Post(app.Event{ID: 2}, nil) })
	case e.Kind == app.EvPost && e.ID == 2:
		s.finish(a)
	default:
		a.DefaultEvent(e)
	}
}

func (s *scenario) setup() {
	// Errors that need no status item.
	if _, err := New(Options{Icon: SymbolIcon("no.such.symbol.xyz")}); !errors.Is(err, ErrImage) {
		childapp.Fail("an unknown symbol: %v", err)
	}
	if _, err := New(Options{Icon: PNGIcon([]byte("not an image"), true)}); !errors.Is(err, ErrImage) {
		childapp.Fail("junk image bytes: %v", err)
	}

	// A status item with a symbol, a title, a tooltip and a menu.
	t1, err := New(Options{Icon: SymbolIcon("bell.fill"), Title: "3", Tooltip: "Tip"})
	must(err == nil, "New: %v", err)
	s.t1 = t1
	btn := t1.button
	img := objc.Send(btn, objc.Sel("image"))
	must(img != 0, "the button has no image")
	must(objc.SendBool(img, objc.Sel("isTemplate")), "the symbol is not a template")
	must(str(objc.Send(btn, objc.Sel("title"))) == "3", "title %q", str(objc.Send(btn, objc.Sel("title"))))
	must(str(objc.Send(btn, objc.Sel("toolTip"))) == "Tip", "tooltip")
	must(objc.Send(btn, objc.Sel("imagePosition")) == nsImageLeft, "image position")
	must(t1.Visible(), "a new item is not visible")
	must(objc.Send(t1.si, objc.Sel("menu")) == 0, "a menu before SetMenu")
	must(t1.SetTitle("") == nil, "SetTitle")
	must(objc.Send(btn, objc.Sel("imagePosition")) == nsImageOnly, "image position without a title")
	must(t1.SetTitle("7") == nil, "SetTitle")

	m := trayModel()
	must(t1.SetMenu(m, 9) == ErrMenu, "menu index out of range")
	must(t1.SetMenu(m, 0) == nil, "SetMenu")
	nm := objc.Send(t1.si, objc.Sel("menu"))
	must(nm != 0 && nm == t1.menu, "the status item's menu is not ours")
	objc.Send(nm, objc.Sel("update"))
	var desc []string
	n := int(objc.Send(nm, objc.Sel("numberOfItems")))
	for i := 0; i < n; i++ {
		it := objc.Send1(nm, objc.Sel("itemAtIndex:"), uintptr(i))
		if objc.SendBool(it, objc.Sel("isSeparatorItem")) {
			desc = append(desc, "-")
			continue
		}
		en, ck := 0, objc.Send(it, objc.Sel("state"))
		if objc.SendBool(it, objc.Sel("isEnabled")) {
			en = 1
		}
		desc = append(desc, fmt.Sprintf("%s/%d/%d/%d", strings.ReplaceAll(str(objc.Send(it, objc.Sel("title"))), " ", "_"), objc.Send(it, objc.Sel("tag")), en, ck))
	}
	got := strings.Join(desc, ",")
	want := "Open/11/1/0,Pause/12/0/0,-,Sound/13/1/1,More/0/1/0,-,Quit/15/1/0"
	must(got == want, "menu items %s, want %s", got, want)
	sub := objc.Send(objc.Send1(nm, objc.Sel("itemAtIndex:"), 4), objc.Sel("submenu"))
	must(sub != 0 && objc.Send(sub, objc.Sel("numberOfItems")) == 1, "the submenu")

	// The model's state is read when the menu opens.
	m.SetEnabled(12, true)
	m.SetChecked(13, false)
	objc.Send(nm, objc.Sel("update"))
	it12 := objc.Send1(nm, objc.Sel("itemAtIndex:"), 1)
	it13 := objc.Send1(nm, objc.Sel("itemAtIndex:"), 3)
	must(objc.SendBool(it12, objc.Sel("isEnabled")) && objc.Send(it13, objc.Sel("state")) == 0, "the model's new state is not shown")

	// Choosing an item delivers the command to the app.
	objc.Send1(nm, objc.Sel("performActionForItemAtIndex:"), 0)
	objc.Send1(nm, objc.Sel("performActionForItemAtIndex:"), 6)
	objc.Send1(sub, objc.Sel("performActionForItemAtIndex:"), 0)

	// A second item: an image, clicks as events, the menu kept off the item.
	s.t2, err = New(Options{Icon: PNGIcon(samplePNG(), true), RightClickMenu: true})
	must(err == nil, "New with a PNG: %v", err)
	t2 := s.t2
	img2 := objc.Send(t2.button, objc.Sel("image"))
	w, h := objc.SendPoint(img2, selSize)
	must(img2 != 0 && w == 18 && h == 18 && objc.SendBool(img2, objc.Sel("isTemplate")), "the PNG is %vx%v", w, h)
	must(t2.SetMenu(m, 0) == nil, "SetMenu on the second")
	must(objc.Send(t2.si, objc.Sel("menu")) == 0 && t2.menu != 0, "the right-click menu is attached to the item")
	// The button's action, as a left click sends it.
	objc.Send1(t2.target, selClick, uintptr(t2.button))

	// Hidden, then shown.
	t3, err := New(Options{Icon: SymbolIcon("moon"), Hidden: true})
	must(err == nil && !t3.Visible(), "a hidden item is visible")
	must(t3.SetVisible(true) == nil && t3.Visible(), "SetVisible(true)")
	must(t3.SetVisible(false) == nil && !t3.Visible(), "SetVisible(false)")
	must(t3.SetIcon(SymbolIcon("no.such.symbol.xyz")) == ErrImage, "SetIcon with an unknown symbol")
	must(t3.SetIcon(Icon{}) == nil && objc.Send(t3.button, objc.Sel("image")) == 0, "SetIcon with no image")
	t3.Remove()
	must(!t3.Visible() && t3.SetTitle("x") == ErrRemoved && t3.SetMenu(nil, 0) == ErrRemoved, "calls after Remove")
	t3.Remove() // twice: nothing
	must(len(tr.items) == 2, "%d items registered after one Remove", len(tr.items))
}

func (s *scenario) finish(a *app.App) {
	must(fmt.Sprint(s.cmds) == "[11 15 14]", "commands %v", s.cmds)
	must(len(s.clicks) == 1 && !s.clicks[0].Right, "clicks %v", s.clicks)
	// The item is gone from the status bar after Remove.
	// The status bar draws an item in a window of the button's; it is on
	// screen while the item is in the bar and gone when the item is removed.
	win := objc.Send(s.t1.button, objc.Sel("window"))
	objc.Retain(win)
	onScreen := objc.SendBool(win, objc.Sel("isVisible"))
	s.t1.Remove()
	s.t2.Remove()
	must(len(tr.items) == 0, "items left")
	if !onScreen {
		childapp.Say("note=the_status_item_was_not_on_screen_before_Remove")
	}
	childapp.Say("on_screen=%v off_screen=%v", onScreen, !objc.SendBool(win, objc.Sel("isVisible")))
	objc.Release(win)
	childapp.Say("commands=%d clicks=%d done=1", len(s.cmds), len(s.clicks))
	a.Quit()
}

func scenarioTray() {
	app.Main(app.AppConfig{KeepRunning: true}, &scenario{})
}

func TestLiveTray(t *testing.T) {
	r := childapp.Run(t, "tray", 60*time.Second)
	if r.KV["done"] != "1" {
		t.Fatalf("the scenario did not finish\n%s", r.Out)
	}
	if r.KV["on_screen"] != "true" {
		t.Skipf("the status item was not on screen (a full menu bar, or no display): %v", r.KV)
	}
	if r.KV["off_screen"] != "true" {
		t.Errorf("a removed item is still on screen: %v", r.KV)
	}
}
