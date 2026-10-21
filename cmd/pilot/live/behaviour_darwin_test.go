//go:build darwin

package live

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/cmd/pilot/pilot"
	"gonativeui/fs"
	"gonativeui/internal/objc"
	"gonativeui/internal/testlock"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// fixtureDirs makes two small folders to browse (the real file service, so
// the panes list what is on disk).
func fixtureDirs(t *testing.T) [2]string {
	t.Helper()
	root := t.TempDir()
	var d [2]string
	long := []string{"alpha.txt", "beta.txt", "gamma.md", "delta.pdf"}
	for i := 0; i < 80; i++ { // more rows than the window shows
		long = append(long, fmt.Sprintf("file-%02d.txt", i))
	}
	for i, files := range [2][]string{long, {"one.txt", "two.txt"}} {
		d[i] = filepath.Join(root, []string{"left", "right"}[i])
		if err := os.MkdirAll(filepath.Join(d[i], "folder"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(d[i], f), []byte(f), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return d
}

func listNow(t *testing.T, dir string) <-chan pilot.Ready {
	t.Helper()
	l := new(fs.Listing)
	if err := fs.ListDir(dir, l); err != nil {
		t.Fatal(err)
	}
	fs.Sort(l, fs.SortName, false)
	ch := make(chan pilot.Ready, 1)
	ch <- pilot.Ready{Dir: dir, L: l, At: platform.Now()}
	return ch
}

// startPilot opens the real window on two fixture folders.
func startPilot(t *testing.T, o pilot.Options) (*widgettest.LiveApp, *pilot.Pilot) {
	t.Helper()
	d := fixtureDirs(t)
	o.Home = filepath.Dir(d[0])
	o.Src = fs.NewService(platform.Wake)
	o.Initial = [2]<-chan pilot.Ready{listNow(t, d[0]), listNow(t, d[1])}
	pl := pilot.New(o)
	t.Cleanup(pl.Close)
	l := widgettest.StartLive(t, pilot.Config(), pl)
	l.Frames(4)
	return l, pl
}

// makeKey makes the window AppKit's key window of the active app, for what
// the system itself decides: the menu bar's Edit items are validated through
// the key window's responders, and StartLive's window only counts as key. Hold
// the "activation" lock first (testlock.Hold): another test process may take
// the activation meanwhile.
func makeKey(t *testing.T, l *widgettest.LiveApp) {
	t.Helper()
	isKey := func() (key bool) {
		for _, w := range widgettest.Windows() {
			key = key || w.Visible && w.Title == "Pilot" && w.Key
		}
		return
	}
	for try := 0; try < 50; try++ { // the system takes a moment, and sometimes asks again
		var key bool
		l.Do(func(a *app.App) {
			if key = isKey(); key {
				return
			}
			objc.Send(widgettest.NSApp(), objc.Sel("activate")) // macOS 14; the older call is ignored
			objc.Send1(widgettest.NSApp(), objc.Sel("activateIgnoringOtherApps:"), 1)
			for _, w := range widgettest.Windows() {
				if w.Visible && w.Title == "Pilot" {
					objc.Send1(w.Window, objc.Sel("makeKeyAndOrderFront:"), 0)
				}
			}
		})
		if key {
			l.Frames(2)
			return
		}
		l.Frames(3)
		time.Sleep(100 * time.Millisecond)
	}
	t.Skip("this Mac does not make the window the key window (the screen is locked, or another app holds the focus)")
}

// clip and setClip use the pasteboard on the main thread, where AppKit has it.
func clip(l *widgettest.LiveApp) (s string) {
	l.Do(func(*app.App) { s = platform.Clipboard() })
	return
}
func setClip(l *widgettest.LiveApp, s string) { l.Do(func(*app.App) { platform.SetClipboard(s) }) }

// filterField is the active pane's filter field in the last frame, its text
// and its selection.
func filterField(l *widgettest.LiveApp) (ok bool, value string, sel [2]int32) {
	tr := l.Tree()
	for _, n := range tr.Nodes {
		if n.Role == ui.RoleSearchField && string(tr.Text(n.Name)) == "filter" {
			return true, string(tr.Text(n.Value)), n.TextSel
		}
	}
	return false, "", [2]int32{}
}

func fieldValue(t *testing.T, l *widgettest.LiveApp) string {
	t.Helper()
	ok, v, _ := filterField(l)
	if !ok {
		t.Fatalf("no filter field:\n%s", l.Dump())
	}
	return v
}

// editMenu performs Edit ▸ title through AppKit, as a click on the menu does.
func editMenu(t *testing.T, l *widgettest.LiveApp, title string) {
	t.Helper()
	var ok bool
	l.Do(func(a *app.App) {
		it, found := widgettest.FindMenuItem("Edit", title)
		if found && it.Enabled {
			ok = it.Perform()
		}
	})
	if !ok {
		t.Fatalf("Edit ▸ %s is missing, disabled or did not run", title)
	}
	l.Frames(3)
}

// An edit command acts once whichever way it comes: the key in the field, or
// the Edit menu's item. A paste that arrived twice, a cut that copied
// nothing or an undo that took two steps would show here.
func TestEditCommandsActOnceFromTheKeyAndTheMenu(t *testing.T) {
	testlock.Hold(t, "activation")
	testlock.Hold(t, "pasteboard") // the general pasteboard is the system's: other tests, in other processes too, write it; after "activation", the order everywhere
	l, _ := startPilot(t, pilot.Options{})
	makeKey(t, l)
	orig := clip(l)
	t.Cleanup(func() { setClip(l, orig) }) // the user's pasteboard is put back
	setClip(l, "sentinel")
	l.Press(ui.KeyCode('F'), ui.ModSuper)
	l.Type("alpha")
	if got := fieldValue(t, l); got != "alpha" {
		t.Fatalf("typed alpha, the filter holds %q", got)
	}
	// Select All by the key, then by the menu
	l.Press(ui.KeyCode('A'), ui.ModSuper)
	if _, _, sel := filterField(l); sel[1]-sel[0] != 5 && sel[0]-sel[1] != 5 {
		t.Errorf("⌘A: selection %v, want all 5 bytes", sel)
	}
	editMenu(t, l, "Select All")
	if _, _, sel := filterField(l); sel[1]-sel[0] != 5 && sel[0]-sel[1] != 5 {
		t.Errorf("Edit ▸ Select All: selection %v, want all 5 bytes", sel)
	}
	// Copy by the menu leaves the field and fills the clipboard
	editMenu(t, l, "Copy")
	if got := clip(l); got != "alpha" {
		t.Errorf("Edit ▸ Copy: clipboard %q", got)
	}
	if got := fieldValue(t, l); got != "alpha" {
		t.Errorf("Copy changed the field to %q", got)
	}
	// Cut by the menu empties the field
	setClip(l, "sentinel")
	editMenu(t, l, "Cut")
	if got, c := fieldValue(t, l), clip(l); got != "" || c != "alpha" {
		t.Errorf("Edit ▸ Cut: field %q, clipboard %q", got, c)
	}
	// Paste once by the menu, once by the key: one copy each
	editMenu(t, l, "Paste")
	if got := fieldValue(t, l); got != "alpha" {
		t.Errorf("Edit ▸ Paste: field %q, want one alpha", got)
	}
	l.Press(ui.KeyCode('V'), ui.ModSuper)
	if got := fieldValue(t, l); got != "alphaalpha" {
		t.Errorf("⌘V: field %q, want two alphas", got)
	}
	// Cut by the key, then paste by the key: back where it was
	l.Press(ui.KeyCode('A'), ui.ModSuper)
	l.Press(ui.KeyCode('X'), ui.ModSuper)
	if got, c := fieldValue(t, l), clip(l); got != "" || c != "alphaalpha" {
		t.Errorf("⌘X: field %q, clipboard %q", got, c)
	}
	l.Press(ui.KeyCode('V'), ui.ModSuper)
	if got := fieldValue(t, l); got != "alphaalpha" {
		t.Errorf("⌘V after ⌘X: field %q", got)
	}
	// Delete by the menu, and Undo takes that one step back, by the menu and
	// then Redo by the key
	l.Press(ui.KeyCode('A'), ui.ModSuper)
	editMenu(t, l, "Delete")
	if got := fieldValue(t, l); got != "" {
		t.Errorf("Edit ▸ Delete: field %q", got)
	}
	editMenu(t, l, "Undo")
	if got := fieldValue(t, l); got != "alphaalpha" {
		t.Errorf("Edit ▸ Undo: field %q, want the delete taken back", got)
	}
	l.Press(ui.KeyCode('Z'), ui.ModSuper|ui.ModShift)
	if got := fieldValue(t, l); got != "" {
		t.Errorf("⇧⌘Z: field %q, want the delete done again", got)
	}
}

// state reads, in the last frame, whether the keyboard focus is shown and
// whether the node with the role and name has a focus ring box.
func focusState(t *testing.T, l *widgettest.LiveApp, role ui.Role, name string) (visible, ring bool) {
	t.Helper()
	n, ok := l.Find(role, name)
	l.Do(func(a *app.App) {
		visible = a.UI.FocusVisible()
		if ok {
			_, ring = a.UI.BoxOf(ui.KeyN(n.Key, 9004)) // base.Kit.FocusRing's box
		}
	})
	return
}

// ringBecomes waits (the ring fades, in wall time) for the ring of the node
// to be there or gone, and reports whether it got there.
func ringBecomes(t *testing.T, l *widgettest.LiveApp, role ui.Role, name string, want bool) bool {
	t.Helper()
	for range 40 {
		if _, ring := focusState(t, l, role, name); ring == want {
			return true
		}
		l.Frames(5)
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func clickRow(t *testing.T, l *widgettest.LiveApp, name string) {
	t.Helper()
	l.Click(ui.RoleRow, name)
}

// A focus ring is the keyboard's: a click on the list, a toolbar button or a
// sidebar row shows none; Tab does; a text field shows its own however it got
// the focus (a click, the toolbar's search button).
func TestFocusRingsComeFromTheKeyboardExceptTextEntry(t *testing.T) {
	l, _ := startPilot(t, pilot.Options{})
	clickRow(t, l, "alpha.txt")
	l.Frames(12)
	if v, _ := focusState(t, l, ui.RoleRow, "alpha.txt"); v {
		t.Error("a click on a row shows the keyboard focus")
	}
	l.Click(ui.RoleButton, "Sidebar")
	l.Frames(12)
	if v, ring := focusState(t, l, ui.RoleButton, "Sidebar"); v || ring {
		t.Errorf("a click on the toolbar's Sidebar button: focus shown %v, ring %v", v, ring)
	}
	l.Click(ui.RoleButton, "Sidebar") // back
	l.Frames(12)
	l.Click(ui.RoleListItem, "Macintosh HD")
	l.Frames(12)
	if v, ring := focusState(t, l, ui.RoleListItem, "Macintosh HD"); v || ring {
		t.Errorf("a click on a sidebar row: focus shown %v, ring %v", v, ring)
	}
	// Tab goes to the other pane's list with the focus shown
	l.Press(ui.KeyTab, 0)
	l.Frames(12)
	if v, _ := focusState(t, l, ui.RoleRow, "one.txt"); !v {
		t.Error("Tab did not show the keyboard focus")
	}
	clickRow(t, l, "one.txt")
	l.Frames(12)
	if v, _ := focusState(t, l, ui.RoleRow, "one.txt"); v {
		t.Error("a click on a row after Tab still shows the keyboard focus")
	}
	// the toolbar's Filter button puts the focus in the field, which shows its ring
	l.Click(ui.RoleButton, "Filter")
	l.Frames(12)
	if !ringBecomes(t, l, ui.RoleSearchField, "filter", true) {
		t.Errorf("the filter field has the focus and no ring:\n%s", l.Dump())
	}
	// a click in the list takes the focus out of it, and the ring with it
	clickRow(t, l, "two.txt")
	if !ringBecomes(t, l, ui.RoleSearchField, "filter", false) {
		t.Error("the filter field shows its ring after the list was clicked")
	}
	// a click in the field shows it
	n := l.Must(ui.RoleSearchField, "filter")
	x, y := widgettest.Centre(n)
	l.ClickAt(x, y, 0)
	if !ringBecomes(t, l, ui.RoleSearchField, "filter", true) {
		t.Error("a click in the filter field shows no ring")
	}
}

// firstRow is the name of the first row of the active table the last frame
// built (the left one: its rows come first in the tree).
func firstRow(l *widgettest.LiveApp) string {
	tr := l.Tree()
	for _, n := range tr.Nodes {
		if n.Role == ui.RoleRow {
			return string(tr.Text(n.Name))
		}
	}
	return ""
}

// A trackpad scroll moves the list, with its inertia ending where it stops,
// and the other way brings the first rows back.
func TestTrackpadScrollsTheList(t *testing.T) {
	l, _ := startPilot(t, pilot.Options{})
	if got := firstRow(l); got != "alpha.txt" && got != "folder" {
		t.Fatalf("the first row is %q", got)
	}
	n := l.Must(ui.RoleRow, "alpha.txt")
	x, y := widgettest.Centre(n)
	first := firstRow(l)
	l.Scroll(x, y, 0, -400) // a trackpad: content goes up
	l.Frames(30)
	moved := firstRow(l)
	if moved == first {
		t.Fatalf("a scroll of 400 pt left the first row at %q", first)
	}
	l.Scroll(x, y, 0, 800)
	l.Frames(60)
	if got := firstRow(l); got != first {
		t.Errorf("scrolling back up: the first row is %q, want %q", got, first)
	}
}

// cmdKey presses ⌘ and the key with the virtual key code vk (US layout:
// widgettest's Press knows letters, digits and a few named keys only).
func cmdKey(l *widgettest.LiveApp, vk uint16, chars string) {
	l.Native(func(w widgettest.Win) {
		widgettest.Post(w.KeyEvent(true, chars, chars, vk, ui.ModSuper, false))
		widgettest.Post(w.KeyEvent(false, chars, chars, vk, ui.ModSuper, false))
	})
	l.Frames(2)
}

// ⌘+ and ⌘−, the View menu's items and the actual-size key zoom the whole
// window: the rows grow and shrink with it, and the list is still there.
func TestZoomKeysAndMenuResizeTheWindowContent(t *testing.T) {
	l, _ := startPilot(t, pilot.Options{})
	rowH := func() float32 {
		n := l.Must(ui.RoleRow, "alpha.txt")
		return n.Bounds[3] - n.Bounds[1]
	}
	zoom := func() (z float64) {
		l.Do(func(a *app.App) { z = float64(a.Zoom()) })
		return
	}
	h0 := rowH()
	if zoom() != 1 {
		t.Fatalf("the window starts at zoom %v", zoom())
	}
	cmdKey(l, 0x18, "=") // ⌘= : the key without Shift zooms in too
	l.Frames(6)
	z1 := zoom()
	h1 := rowH()
	if z1 <= 1 || h1 <= h0*1.05 {
		t.Fatalf("⌘=: zoom %v, row height %v px from %v", z1, h1, h0)
	}
	l.Do(func(a *app.App) {
		it, ok := widgettest.FindMenuItem("View", "Zoom In")
		if !ok || !it.Enabled || !it.Perform() {
			t.Error("View ▸ Zoom In is missing or disabled")
		}
	})
	l.Frames(6)
	if z2 := zoom(); z2 <= z1 {
		t.Errorf("View ▸ Zoom In: zoom %v after %v", z2, z1)
	}
	cmdKey(l, 0x1B, "-")
	cmdKey(l, 0x1B, "-")
	l.Frames(6)
	if z := zoom(); z != 1 {
		t.Errorf("two ⌘− after two ⌘+: zoom %v, want 1", z)
	}
	cmdKey(l, 0x18, "=")
	l.Press(ui.KeyCode('0'), ui.ModSuper)
	l.Frames(6)
	if z, h := zoom(), rowH(); z != 1 || h != h0 {
		t.Errorf("⌘0: zoom %v, row height %v px, want 1 and %v", z, h, h0)
	}
	cmdKey(l, 0x1B, "-")
	l.Frames(6)
	if z, h := zoom(), rowH(); z >= 1 || h >= h0 {
		t.Errorf("⌘−: zoom %v, row height %v px from %v", z, h, h0)
	}
}

// The window follows the system's light and dark: forced through the app's
// own appearance (nothing else on this Mac changes), the app sees the mode
// and declares it to the chrome the system draws, and it goes back.
func TestFollowsTheAppearanceLive(t *testing.T) {
	l, _ := startPilot(t, pilot.Options{})
	t.Cleanup(func() { l.Do(func(*app.App) { widgettest.ResetAppAppearance() }) })
	var c *ui.Context
	l.Do(func(a *app.App) { c = a.UI })
	for _, dark := range []bool{true, false, true, false} {
		l.Do(func(*app.App) { widgettest.SetAppAppearance(dark) })
		l.Until(fmt.Sprintf("the system is dark: %v", dark), 240, func() bool {
			d, ok := c.ChromeDark() // runs in a frame
			return c.SystemAppearance().Dark == dark && ok && d == dark
		})
		l.Frames(60) // the crossfade
	}
}

// Nothing animates and nothing draws while the window is left alone: after
// the start has settled, a second of waiting builds no frame.
func TestIdleDrawsNoFrames(t *testing.T) {
	l, _ := startPilot(t, pilot.Options{})
	for range 10 { // the icons decode, the first listing settles, the watchers start
		l.Frames(5)
		time.Sleep(200 * time.Millisecond)
	}
	var before int
	for try := 0; try < 5; try++ {
		before = l.Built()
		time.Sleep(1200 * time.Millisecond)
		if l.Built() == before {
			return
		}
	}
	t.Errorf("the window kept drawing: %d frames in the last 1.2 s of 5 tries", l.Built()-before)
}

// Another window of the process takes the key: Pilot is inactive, and active
// again when the window is gone.
func TestInactiveWhenAnotherWindowHasTheKey(t *testing.T) {
	testlock.Hold(t, "activation")
	l, _ := startPilot(t, pilot.Options{})
	l.RealFocus() // the focus is AppKit's from here, which this test is about
	makeKey(t, l)
	var c *ui.Context
	l.Do(func(a *app.App) { c = a.UI })
	l.Until("Pilot has the key", 120, func() bool { return c.Input().Focused })
	var other widgettest.PlainWindow
	l.Do(func(a *app.App) {
		other = l.OpenPlainWindow("other", 40, 40)
		objc.Send1(other.Window, objc.Sel("makeKeyAndOrderFront:"), 0)
	})
	l.Until("Pilot lost the key", 240, func() bool { return !c.Input().Focused })
	l.Frames(10)
	l.Do(func(a *app.App) { other.Close() })
	makeKey(t, l)
	l.Until("Pilot has the key again", 240, func() bool { return c.Input().Focused })
}
