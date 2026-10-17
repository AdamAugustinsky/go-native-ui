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
	if got, want := r.src.last(), "/Users/rex/Projects/pilot/cmd"; got != want {
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
	if sel := string(a.path.Selected()); sel != "/Users/rex/Projects/pilot" {
		t.Errorf("the path is selected: %q", sel)
	}
	r.text("~/Docs")
	if a.path.String() != "~/Docs" {
		t.Fatalf("typing replaces the selected path: %q", a.path.String())
	}
	r.key(ui.KeyEnter, 0)
	if r.src.last() != "/Users/rex/Docs" {
		t.Errorf("Enter lists %q", r.src.last())
	}
	// the listing fails: the text stays, the reason shows
	bad := new(fs.Listing)
	bad.Err = fs.ErrUnsupported
	deliver(a, bad)
	r.frames(2)
	if a.pathErr == "" || a.path.String() != "~/Docs" || a.dir != "/Users/rex/Projects/pilot" {
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
