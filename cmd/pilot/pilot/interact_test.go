package pilot

import (
	"strings"
	"testing"

	"gonativeui/app"
	"gonativeui/fs"
	"gonativeui/ui"
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

func newDrive(t *testing.T) *drive {
	t.Helper()
	src := &fakeSrc{}
	opt := SceneOptions()
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
	if a.v.nSel != 4 {
		t.Error("the other pane keeps its selection")
	}
}

func TestDoubleClickOpensAFolder(t *testing.T) {
	r := newDrive(t)
	r.rowAt(0, 1) // cmd
	r.press(1, 0)
	r.release()
	r.press(2, 0)
	if got, want := r.src.last(), nativePath("/Users/rex/Projects/pilot/cmd"); got != want {
		t.Fatalf("double-click lists %q, want %q", got, want)
	}
	r.release()
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
	if a.v.curRow < 0 {
		t.Error("down with the filter focused moves the cursor")
	}
	r.key(ui.KeyEscape, 0)
	if a.filterOpen || len(a.q) != 0 || r.o.UI.FocusKey() != 0 {
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
	if sel := string(a.path.Selected()); sel != nativePath("/Users/rex/Projects/pilot") {
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
	if a.pathErr == "" || a.path.String() != "~/Docs" || a.dir != nativePath("/Users/rex/Projects/pilot") {
		t.Errorf("path error %q text %q", a.pathErr, a.path.String())
	}
	// Esc restores the directory and gives the focus back to the list
	r.key('L', pl.primary())
	r.key(ui.KeyEscape, 0)
	if a.path.String() != a.dir || r.o.UI.FocusKey() != 0 {
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
	_, g, _ := pl.paneRects()
	r.at((g[0]+g[2])/2, (g[1]+g[3])/2)
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

// Tab in a text field leaves it for the other pane: no tab character is typed
// into the field, the focus returns to a list, and the keyboard is the other
// pane's.
func TestTabInATextFieldSwitchesPanes(t *testing.T) {
	r := newDrive(t)
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
		in.PushKey(ui.KeyEvent{Code: ui.KeyTab, Down: true})
		in.AppendText("\t") // what the platform sends along with the key
	})
	if pl.active != 1 || r.o.UI.FocusKey() != 0 {
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
		in.AppendText("\t")
	})
	if pl.active != 0 || r.o.UI.FocusKey() != 0 {
		t.Errorf("Tab in the path field: active %d focus %v", pl.active, r.o.UI.FocusKey())
	}
	if b.path.String() != b.dir || b.pathErr != "" {
		t.Errorf("the edited path stays after Tab: %q (dir %q)", b.path.String(), b.dir)
	}
}
