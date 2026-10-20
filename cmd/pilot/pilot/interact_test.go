package pilot

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/fs"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// driver is a Pilot that feeds the input a test hands it, one frame at a time,
// the way translated platform events arrive.
type driver struct {
	*Pilot
	next func(in *ui.Input)
}

func (d *driver) Script(frame int, in *ui.Input) bool {
	in.Inside, in.Focused = true, true
	if d.next != nil {
		d.next(in)
		d.next = nil
	}
	return true
}

type drive struct {
	t    *testing.T
	d    *driver
	o    *app.Offscreen
	src  *fakeSrc
	x, y float32
}

func newDrive(t *testing.T) *drive { return newDriveWith(t, func(*Options) {}) }

// newDriveWith is newDrive with the options changed first.
func newDriveWith(t *testing.T, set func(*Options)) *drive {
	t.Helper()
	src := &fakeSrc{}
	opt := SceneOptions()
	set(&opt)
	opt.Src = src
	d := &driver{Pilot: New(opt)}
	dr := &drive{t: t, d: d, src: src}
	t.Cleanup(func() {
		if src.ignored != 0 {
			t.Errorf("%d Release calls the Service would have ignored (stale or missing lease)", src.ignored)
		}
	})
	dr.o = newOffscreen(t, d)
	for i := 0; i < 5; i++ {
		dr.o.Frame()
	}
	for n := 0; n < 1000 && d.IconsPending(); n++ { // the system's icons decode on workers
		time.Sleep(time.Millisecond)
		dr.o.Frame()
	}
	dr.o.Frame()
	return dr
}

func (r *drive) frames(n int) {
	for i := 0; i < n; i++ {
		r.o.Frame()
	}
}

func (r *drive) feed(f func(in *ui.Input)) {
	r.d.next = f
	r.o.Frame()
}

func (r *drive) at(x, y float32) { r.x, r.y = x, y }

func (r *drive) press(clicks uint8, mods ui.Mods) {
	r.feed(func(in *ui.Input) {
		in.MouseX, in.MouseY, in.Mods = r.x, r.y, mods
		in.SetButton(ui.ButtonLeft, true, clicks)
	})
}

func (r *drive) release() {
	r.feed(func(in *ui.Input) {
		in.MouseX, in.MouseY = r.x, r.y
		in.SetButton(ui.ButtonLeft, false, 0)
	})
}

func (r *drive) click(mods ui.Mods) { r.press(1, mods); r.release() }

func (r *drive) key(code ui.KeyCode, mods ui.Mods) {
	r.feed(func(in *ui.Input) {
		in.MouseX, in.MouseY = r.x, r.y
		in.Mods = mods
		in.PushKey(ui.KeyEvent{Code: code, Mods: mods, Down: true})
	})
}

func (r *drive) text(s string) {
	r.feed(func(in *ui.Input) {
		in.MouseX, in.MouseY = r.x, r.y
		in.AppendText(s)
	})
}

// center is the middle of the box with key k, as of the last frame.
func (r *drive) center(k ui.Key) (x, y float32) {
	b := r.o.UI.RectOf(k)
	return (b[0] + b[2]) / 2, (b[1] + b[3]) / 2
}

// atBand points at the end of pane i's path bar band, beside the components.
func (r *drive) atBand(i int) {
	_, y := r.center(pathBandKey(i))
	r.at(r.d.Pilot.geo[i].rect[2]-20, y)
}

func (r *drive) rowAt(pane, row int) {
	s := script{pl: r.d.Pilot}
	r.at(s.listPoint(pane, row))
}

func (r *drive) settle() {
	r.frames(3)
	for n := 0; n < 1000 && (r.d.panes[0].work() || r.d.panes[1].work()); n++ {
		r.frames(1)
	}
	r.frames(2)
}

func TestClickingSelectsAndSwitchesPanes(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a, b := pl.panes[0], pl.panes[1]
	// rows of the left pane with hidden files hidden: 0 app, 1 cmd, 2 docs, 3 fs, 4 latest, 5 ui, 6 build.sh ...
	r.rowAt(0, 2)
	r.click(0)
	if selected(a) != "docs" || pl.active != 0 {
		t.Fatalf("click: selected %q", selected(a))
	}
	r.rowAt(0, 4)
	r.click(ui.ModShift)
	if selected(a) != "docs fs latest" {
		t.Errorf("shift-click: %q", selected(a))
	}
	r.rowAt(0, 6)
	r.click(pl.primary())
	if selected(a) != "docs fs latest build.sh" {
		t.Errorf("modifier-click: %q", selected(a))
	}
	r.rowAt(1, 1)
	r.click(0)
	if pl.active != 1 || selected(b) != "Reports" {
		t.Errorf("clicking the other pane: active %d, selected %q", pl.active, selected(b))
	}
	if a.v.nSel() != 4 {
		t.Error("the other pane keeps its selection")
	}
}

// The keys of the list are the table's: the arrows move the cursor and select,
// Shift extends, Page Up and Down, Home and End jump, select all selects all,
// Esc clears, Enter opens the cursor row, Backspace goes up.
func TestListKeysGoThroughTheTable(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a := pl.panes[0]
	prim := pl.primary()
	// rows with the hidden files out of sight: 0 app, 1 cmd, 2 docs, 3 fs, 4 latest, 5 ui, 6 build.sh …
	r.rowAt(0, 0)
	r.click(0)
	if selected(a) != "app" {
		t.Fatalf("setup: selected %q", selected(a))
	}
	r.key(ui.KeyDown, 0)
	if selected(a) != "cmd" {
		t.Errorf("down: %q", selected(a))
	}
	r.key(ui.KeyDown, ui.ModShift)
	r.key(ui.KeyDown, ui.ModShift)
	if selected(a) != "cmd docs fs" {
		t.Errorf("shift+down extends: %q", selected(a))
	}
	r.key(ui.KeyUp, ui.ModShift)
	if selected(a) != "cmd docs" {
		t.Errorf("shift+up shrinks: %q", selected(a))
	}
	r.key(ui.KeyPageDown, 0)
	if want := min(pl.geo[0].visRows-1+2, a.v.nRows()-1); a.v.curRow() != want {
		t.Errorf("page down: cursor on row %d, want %d", a.v.curRow(), want)
	}
	r.key(ui.KeyPageUp, 0)
	r.key(ui.KeyEnd, 0)
	if a.v.curRow() != a.v.nRows()-1 || a.v.nSel() != 1 {
		t.Errorf("end: cursor on row %d of %d, %d selected", a.v.curRow(), a.v.nRows(), a.v.nSel())
	}
	r.key(ui.KeyHome, 0)
	if selected(a) != "app" {
		t.Errorf("home: %q", selected(a))
	}
	r.key('A', prim)
	if a.v.nSel() != a.v.nRows() {
		t.Errorf("select all: %d of %d", a.v.nSel(), a.v.nRows())
	}
	r.key(ui.KeyEscape, 0)
	if a.v.nSel() != 0 {
		t.Error("esc clears the selection")
	}
	r.key(ui.KeyHome, 0)
	r.key(ui.KeyEnter, 0)
	if want := nativePath("/Users/rex/Documents/app"); r.src.last() != want {
		t.Errorf("enter on a folder lists %q, want %q", r.src.last(), want)
	}
	// ↑/↓ with the filter focused drive the list too (Home and End do as well)
	deliver(a, listing(fs.SortName, false, sceneLeft...))
	r.settle()
	r.text("o") // opens the filter
	r.settle()
	r.key(ui.KeyDown, 0)
	r.key(ui.KeyDown, 0)
	r.key(ui.KeyUp, ui.ModShift)
	// (from no cursor, the first ↓ goes to the first row, the second to the next;
	// shift+↑ then takes the range from that one back to the first)
	if a.v.nSel() != 2 || a.v.curRow() != 0 {
		t.Errorf("↓↓ and shift+↑ from the filter: %d selected, cursor on row %d", a.v.nSel(), a.v.curRow())
	}
}

func TestDoubleClickOpensAFolder(t *testing.T) {
	r := newDrive(t)
	r.rowAt(0, 1) // cmd
	r.press(1, 0)
	r.release()
	r.press(2, 0)
	r.release() // the table reports the double click when the button comes up
	if got, want := r.src.last(), nativePath("/Users/rex/Documents/cmd"); got != want {
		t.Fatalf("double-click lists %q, want %q", got, want)
	}
}

func TestTypingFiltersTheList(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a := pl.panes[0]
	r.rowAt(0, 0)
	r.click(0)
	r.text("m")
	if !a.filterOpen || a.filter.String() != "m" || r.o.UI.FocusKey() != filterKey(0) {
		t.Fatalf("typing: filter open %v, text %q", a.filterOpen, a.filter.String())
	}
	r.settle()
	if a.v.nRows() == 0 {
		t.Fatal("no rows match m")
	}
	for r := 0; r < a.v.nRows(); r++ {
		n := a.v.l.Name(int(a.v.entryAt(r)))
		if !strings.Contains(strings.ToLower(string(n)), "m") {
			t.Errorf("%q matches the filter m", n)
		}
	}
	// ↑/↓ and Enter still drive the list with the filter focused
	r.key(ui.KeyDown, 0)
	if a.v.curRow() < 0 {
		t.Error("down with the filter focused moves the cursor")
	}
	r.key(ui.KeyEscape, 0)
	if a.filterOpen || len(a.q) != 0 || r.o.UI.FocusKey() != listKey(0) {
		t.Error("Esc closes the filter and returns the focus to the list")
	}
	r.settle()
	if a.v.nRows() != 19 { // 21 entries, two hidden
		t.Errorf("%d rows after Esc, want 19", a.v.nRows())
	}
}

func TestPathFieldNavigates(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a := pl.panes[0]
	r.key('L', pl.primary())
	if r.o.UI.FocusKey() != pathKey(0) {
		t.Fatal("the shortcut focuses the path field")
	}
	r.frames(2)
	if sel := string(a.path.Selected()); sel != nativePath("/Users/rex/Documents") {
		t.Errorf("the path is selected: %q", sel)
	}
	r.text("~/Docs")
	if a.path.String() != "~/Docs" {
		t.Fatalf("typing replaces the selected path: %q", a.path.String())
	}
	r.key(ui.KeyEnter, 0)
	if r.src.last() != nativePath("/Users/rex/Docs") {
		t.Errorf("Enter lists %q", r.src.last())
	}
	// the listing fails: the text stays, the reason shows
	bad := new(fs.Listing)
	bad.Err = fs.ErrUnsupported
	deliver(a, bad)
	r.frames(2)
	if a.pathErr == "" || a.path.String() != "~/Docs" || a.dir != nativePath("/Users/rex/Documents") {
		t.Errorf("path error %q text %q", a.pathErr, a.path.String())
	}
	// Esc restores the directory and gives the focus back to the list
	r.key('L', pl.primary())
	r.key(ui.KeyEscape, 0)
	if a.path.String() != a.dir || r.o.UI.FocusKey() != listKey(0) {
		t.Errorf("Esc: text %q focus %v", a.path.String(), r.o.UI.FocusKey())
	}
}

func TestHeaderClickSorts(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a := pl.panes[0]
	s := script{pl: pl}
	r.at(s.headerPoint(0, 1))
	r.click(0)
	if a.sortKey != fs.SortSize || a.desc || r.src.last() != a.dir {
		t.Fatalf("size header: key %v desc %v, listed %q", a.sortKey, a.desc, r.src.last())
	}
	r.click(0)
	if a.sortKey != fs.SortSize || !a.desc {
		t.Errorf("again reverses: desc %v", a.desc)
	}
	r.at(s.headerPoint(0, 2))
	r.click(0)
	if a.sortKey != fs.SortModified || a.desc {
		t.Errorf("date header: key %v desc %v", a.sortKey, a.desc)
	}
}

func TestDividerDragResizesThePanes(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	l, rt := pl.geo[0].rect, pl.geo[1].rect // the gutter is between the panes
	r.at((l[2]+rt[0])/2, (l[1]+l[3])/2)
	w0 := pl.geo[0].rect[2] - pl.geo[0].rect[0]
	r.press(1, 0)
	r.feed(func(in *ui.Input) { r.x += 120; in.MouseX, in.MouseY = r.x, r.y; in.SetButton(ui.ButtonLeft, true, 0) })
	r.release()
	r.frames(2)
	if w := pl.geo[0].rect[2] - pl.geo[0].rect[0]; w < w0+100 {
		t.Errorf("left pane %v wide, was %v: the drag did not widen it", w, w0)
	}
}

func TestRegainingFocusRelistsBothPanes(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a, b := pl.panes[0], pl.panes[1]
	r.rowAt(0, 2)
	r.click(0)
	r.frames(2)
	n := len(r.src.lists)
	r.frames(5)
	if len(r.src.lists) != n {
		t.Fatal("frames without a focus change list nothing")
	}
	r.feed(func(in *ui.Input) { in.FocusGained = true })
	if got := r.src.lists[n:]; len(got) != 2 || got[0] != a.dir || got[1] != b.dir {
		t.Fatalf("regaining focus lists %q, want both panes' folders", got)
	}
	// the new listings arrive with a file more: the selection stays
	changed := append([]item{{"zz-new.txt", fs.KindFile, 0, 1, "2026-10-17 12:00"}}, sceneLeft...)
	deliver(a, listing(fs.SortName, false, changed...))
	deliver(b, listing(fs.SortName, false, sceneRight...))
	r.settle()
	if selected(a) != "docs" || a.v.nEntries() != len(sceneLeft)+1 {
		t.Errorf("after the relist: selected %q, %d entries", selected(a), a.v.nEntries())
	}
	// no relist while a navigation is in flight (its result is fresh anyway)
	a.request(nativePath("/x"), navUser, "")
	n = len(r.src.lists)
	r.feed(func(in *ui.Input) { in.FocusGained = true })
	if got := r.src.lists[n:]; len(got) != 1 || got[0] != b.dir {
		t.Errorf("with a navigation pending, lists %q", got)
	}
}

// Tab in a text field leaves it without a tab character typed into the field.
// From the filter it goes on to the other pane's table; from the path field,
// the last stop of the loop, to the sidebar, and the edited path is dropped.
func TestTabInATextFieldLeavesIt(t *testing.T) {
	r := newDrive(t)
	r.o.UI.SetTabReachesControls(false) // Keyboard navigation off
	pl := r.d.Pilot
	a, b := pl.panes[0], pl.panes[1]

	// the filter field
	r.rowAt(0, 0)
	r.click(0)
	r.text("m")
	if r.o.UI.FocusKey() != filterKey(0) {
		t.Fatal("setup: the filter has the focus")
	}
	r.feed(func(in *ui.Input) {
		in.MouseX, in.MouseY = r.x, r.y
		// The platforms send no character with Tab (and the translator drops
		// one that does: app's TestTabCharacterIsNotText), so Tab is only a key.
		in.PushKey(ui.KeyEvent{Code: ui.KeyTab, Down: true})
	})
	r.frames(1) // the pane the focus went to becomes the active one in the next frame
	if pl.active != 1 || r.o.UI.FocusKey() != listKey(1) {
		t.Errorf("Tab in the filter: active %d focus %v", pl.active, r.o.UI.FocusKey())
	}
	if a.filter.String() != "m" || string(a.q) != "m" || !a.filterOpen {
		t.Errorf("the filter took the Tab: text %q query %q open %v", a.filter.String(), a.q, a.filterOpen)
	}

	// the path field of the other pane (now active)
	r.key('L', pl.primary())
	r.text("~/x")
	if r.o.UI.FocusKey() != pathKey(1) || b.path.String() != "~/x" {
		t.Fatalf("setup: path field of pane 1: focus %v text %q", r.o.UI.FocusKey(), b.path.String())
	}
	r.feed(func(in *ui.Input) {
		in.MouseX, in.MouseY = r.x, r.y
		in.PushKey(ui.KeyEvent{Code: ui.KeyTab, Down: true})
	})
	r.frames(1) // the field leaves with the focus, in the next frame
	if side := sidebarKey(r.o.UI); side == 0 || r.o.UI.FocusKey() != side || b.pathEdit {
		t.Errorf("Tab in the path field: focus %v (sidebar %v), path edit %v", r.o.UI.FocusKey(), side, b.pathEdit)
	}
	if b.path.String() != b.dir || b.pathErr != "" {
		t.Errorf("the edited path stays after Tab: %q (dir %q)", b.path.String(), b.dir)
	}
}

// sidebarKey is the key of the sidebar's box, from the semantic tree.
func sidebarKey(c *ui.Context) ui.Key {
	var tr ui.SemTree
	c.Semantics(&tr)
	for _, n := range tr.Nodes {
		if string(tr.Text(n.Name)) == "Sidebar" {
			return n.Key
		}
	}
	return 0
}

// Tab's loop is ui's (focus classes: the sidebar and the tables are lists, the
// filter and path fields text), with the setting for keyboard navigation off,
// as it is by default: the sidebar, each pane's table, and a pane's filter or
// path field while shown, in build order, wrapping, and back with Shift+Tab.
// Toolbar buttons and column headers are never in it, however many times it is
// walked; the pane that gets the focus becomes the active one.
func TestTabLoop(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	walk := func(mods ui.Mods, steps int) []ui.Key {
		var got []ui.Key
		for i := 0; i < steps; i++ {
			r.key(ui.KeyTab, mods)
			r.frames(1)
			got = append(got, r.o.UI.FocusKey())
			var tr ui.SemTree
			r.o.UI.Semantics(&tr)
			for _, n := range tr.Nodes {
				if n.State&ui.SemFocused != 0 && (n.Role == ui.RoleButton || n.Role == ui.RoleColumnHeader) {
					t.Errorf("Tab reached a %s", widgettest.RoleName(n.Role))
				}
			}
		}
		return got
	}
	check := func(what string, got []ui.Key, want ...ui.Key) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: %d stops, want %d", what, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: stop %d is %#x, want %#x", what, i, uint64(got[i]), uint64(want[i]))
			}
		}
	}
	r.o.UI.SetTabReachesControls(false) // Keyboard navigation off, macOS's default (the app sets it from the system)
	r.frames(2)
	pl.focusList(r.o.UI)
	r.frames(1)
	sb, l0, l1 := sidebarKey(r.o.UI), listKey(0), listKey(1)
	if r.o.UI.FocusKey() != l0 || sb == 0 {
		t.Fatalf("setup: focus %v, sidebar key %v", r.o.UI.FocusKey(), sb)
	}
	check("forward", walk(0, 4), l1, sb, l0, l1)
	check("back", walk(ui.ModShift, 4), l0, sb, l1, l0)

	// a filter and a path field in the loop, after their panes' tables
	pl.panes[0].filterOpen = true
	pl.panes[1].pathEdit = true
	r.frames(2)
	pl.setActive(r.o.UI, 0)
	pl.focusList(r.o.UI)
	r.frames(1)
	check("with fields", walk(0, 6), filterKey(0), l1, pathKey(1), sb, l0, filterKey(0))

	// the sidebar hidden: not in it
	pl.panes[0].filterOpen, pl.panes[1].pathEdit = false, false
	pl.sidebar = false
	r.frames(2)
	pl.setActive(r.o.UI, 0)
	pl.focusList(r.o.UI)
	r.frames(1)
	check("no sidebar", walk(0, 3), l1, l0, l1)
}

// With Keyboard navigation on, Tab reaches the toolbar's buttons too (ui's
// rule), and still every table, the sidebar and the fields; a pane the focus
// moves to becomes the active one.
func TestTabReachesTheToolbarWithKeyboardNavigation(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	r.o.UI.SetTabReachesControls(true)
	r.frames(2)
	pl.focusList(r.o.UI)
	r.frames(1)
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		r.key(ui.KeyTab, 0)
		r.frames(1)
		var tr ui.SemTree
		r.o.UI.Semantics(&tr)
		for _, n := range tr.Nodes {
			if n.State&ui.SemFocused != 0 {
				seen[widgettest.RoleName(n.Role)+" "+string(tr.Text(n.Name))] = true
			}
		}
		if fk := r.o.UI.FocusKey(); fk == listKey(1) && pl.active != 1 {
			t.Fatalf("the focus is in the right table, the active pane is %d", pl.active)
		}
	}
	for _, want := range []string{"Button Sidebar", "Button Enclosing Folder", "Button Filter"} {
		if !seen[want] {
			t.Errorf("Tab never reached %q; reached %v", want, seen)
		}
	}
}

// shown reports what the screen has now, from the semantic tree: whether a
// node with this role and name is there, and its value.
func (r *drive) shown(role ui.Role, name string) (value string, ok bool) {
	var tr ui.SemTree
	r.o.UI.Semantics(&tr)
	for _, n := range tr.Nodes {
		if n.Role == role && string(tr.Text(n.Name)) == name {
			return string(tr.Text(n.Value)), true
		}
	}
	return "", false
}

// statusLines are the labels of the status lines as shown.
func (r *drive) statusLines() (out []string) {
	var tr ui.SemTree
	r.o.UI.Semantics(&tr)
	for _, n := range tr.Nodes {
		if n.Role == ui.RoleLabel && n.Bounds[1] > 700 {
			out = append(out, string(tr.Text(n.Name)))
		}
	}
	return out
}

func (r *drive) statusHas(s string) bool {
	for _, l := range r.statusLines() {
		if l == s {
			return true
		}
	}
	return false
}

// Enter on a path that cannot be listed keeps the field open with the text
// that was typed and says why, until the text is changed or cancelled (Finder's
// Go to Folder); a path that can be listed closes the field on the folder.
func TestFailedPathSubmissionStaysOpenAndSaysWhy(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a := pl.panes[0]
	r.key('L', pl.primary())
	r.text("~/Nope")
	r.key(ui.KeyEnter, 0)
	bad := new(fs.Listing)
	bad.Err = os.ErrNotExist
	deliver(a, bad)
	r.frames(3)
	v, open := r.shown(ui.RoleTextField, "path")
	if !open {
		t.Fatal("the path field went away on a failed path")
	}
	if v != "~/Nope" {
		t.Errorf("the field shows %q, want what was typed", v)
	}
	if !r.statusHas("No such file or directory") {
		t.Errorf("the reason is not shown: status %q", r.statusLines())
	}
	if r.o.UI.FocusKey() != pathKey(0) {
		t.Error("the field lost the keyboard")
	}
	// the relist that follows the failure does not take the reason away
	deliver(a, listing(fs.SortName, false, dirItems...))
	r.frames(3)
	if !r.statusHas("No such file or directory") {
		t.Errorf("the reason went with the relist: status %q", r.statusLines())
	}
	// correcting the text takes it away; the corrected path opens and closes the field
	r.text("x")
	r.frames(2)
	if r.statusHas("No such file or directory") {
		t.Error("the reason stays after the text changed")
	}
	r.key('A', pl.primary())
	r.text("/ok")
	r.key(ui.KeyEnter, 0)
	if _, open := r.shown(ui.RoleTextField, "path"); !open {
		t.Error("the field closed before the folder was shown")
	}
	deliver(a, listing(fs.SortName, false, dirItems...))
	r.frames(3)
	if _, open := r.shown(ui.RoleTextField, "path"); open {
		t.Error("the field stays open on the folder it went to")
	}
	if r.o.UI.FocusKey() != listKey(0) {
		t.Errorf("focus %v after the folder was shown, want its list", r.o.UI.FocusKey())
	}
}

// Esc on a failed path closes the field and takes the reason away.
func TestEscapeEndsAFailedPath(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a := pl.panes[0]
	r.key('L', pl.primary())
	r.text("~/Nope")
	r.key(ui.KeyEnter, 0)
	bad := new(fs.Listing)
	bad.Err = os.ErrNotExist
	deliver(a, bad)
	r.frames(2)
	r.key(ui.KeyEscape, 0)
	r.frames(2)
	if _, open := r.shown(ui.RoleTextField, "path"); open || r.statusHas("No such file or directory") {
		t.Errorf("Esc: field open %v, status %q", open, r.statusLines())
	}
}

// The path bar's empty part is a named button that the keyboard and assistive
// technology operate, as the pointer does.
func TestPathBandOpensTheEditorForEveryInput(t *testing.T) {
	for _, how := range []string{"accessibility", "keyboard"} {
		t.Run(how, func(t *testing.T) {
			r := newDrive(t)
			pl := r.d.Pilot
			var tr ui.SemTree
			r.o.UI.Semantics(&tr)
			var key ui.Key
			n := 0
			for _, nd := range tr.Nodes {
				if nd.Role == ui.RoleButton && string(tr.Text(nd.Name)) == "Edit Path" {
					if n == 0 {
						key = nd.Key
					}
					n++
				}
			}
			if n != 2 {
				t.Fatalf("%d \"Edit Path\" buttons in the tree, want one per pane", n)
			}
			if how == "accessibility" {
				r.feed(func(in *ui.Input) { in.PushAction(ui.SemRequest{Key: key, Action: ui.ActPress}) })
			} else {
				r.o.UI.SetFocus(key)
				r.frames(1)
				r.key(ui.KeyEnter, 0)
			}
			r.frames(2)
			if !pl.panes[0].pathEdit || r.o.UI.FocusKey() != pathKey(0) {
				t.Errorf("path edit %v, focus %v", pl.panes[0].pathEdit, r.o.UI.FocusKey())
			}
		})
	}
}

// Activating the window asks the system for the icon style on a worker: a
// frame with the focus gained does not wait for the answer (a read of the
// system's preferences that blocks would freeze the window), and a style that
// changed is taken in the next frame after it came.
func TestActivationReadsTheIconStyleOffTheFrame(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var calls atomic.Int32
	style := atomic.Uint64{}
	old := iconStyle
	iconStyle = func() uint64 {
		if calls.Add(1) > 1 { // the read at the start is quick
			<-gate
		}
		return style.Load()
	}
	t.Cleanup(func() { iconStyle = old })
	// (a function defer, not a cleanup: it must run before the fixture's
	// cleanups, whatever way the test ends, and a Fatal does not stop it)
	timer := time.AfterFunc(4*time.Second, release)
	defer timer.Stop()
	defer release()

	r := newDrive(t)
	pl := r.d.Pilot
	pl.sys.bundles[bundleID{name: 1}] = &bundleRec{}
	style.Store(7)
	t0 := time.Now()
	for i := 0; i < 5; i++ { // activated again and again while the answer is out
		r.feed(func(in *ui.Input) { in.FocusGained = true })
	}
	if d := time.Since(t0); d > time.Second {
		t.Fatalf("five activations took %v: a frame waits for the icon style", d)
	}
	if pl.sys.style != 0 || len(pl.sys.bundles) != 1 {
		t.Fatalf("the style changed before it was read")
	}
	release()
	for n := 0; n < 500 && pl.sys.style != 7; n++ {
		time.Sleep(2 * time.Millisecond)
		r.frames(1)
	}
	if pl.sys.style != 7 || len(pl.sys.bundles) != 0 {
		t.Errorf("style %d, %d bundle icons: a changed style is not taken (and the application icons asked for again)", pl.sys.style, len(pl.sys.bundles))
	}
}
