package pilot

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"gonativeui/fs"
	"gonativeui/ui"
)

// fakeSrc is a Source that records requests; the tests deliver the results. It
// numbers requests and leases like fs.Service and counts the Release calls that
// the Service would ignore (a lease that is not the one handed out).
type fakeSrc struct {
	lists    []string
	keys     []fs.SortKey // the sort key of each List
	watched  []string
	released int
	opened   []string
	req      uint64
	poll     []fs.Result // what the next Poll returns

	lease   uint32
	leased  map[*fs.Listing]uint32
	ignored int
}

func (f *fakeSrc) List(id uint32, path string, key fs.SortKey, desc bool) uint64 {
	f.lists = append(f.lists, path)
	f.keys = append(f.keys, key)
	f.req++
	return f.req
}
func (f *fakeSrc) Watch(id uint32, path string) { f.watched = append(f.watched, path) }
func (f *fakeSrc) Unwatch(id uint32)            {}
func (f *fakeSrc) Poll(dst []fs.Result) []fs.Result {
	dst = append(dst, f.poll...)
	f.poll = f.poll[:0]
	return dst
}
func (f *fakeSrc) Release(l *fs.Listing, lease uint32) {
	f.released++
	want, ok := f.leased[l]
	switch {
	case ok && lease == want:
		delete(f.leased, l)
	case ok || lease != 0:
		f.ignored++
	}
}
func (f *fakeSrc) Open(id uint32, path string) { f.opened = append(f.opened, path) }
func (f *fakeSrc) Close()                      {}
func (f *fakeSrc) last() string                { return f.lists[len(f.lists)-1] }

// result is a finished listing as Poll would hand it over: leased, and
// answering request req.
func (f *fakeSrc) result(id uint32, l *fs.Listing, req uint64, refresh bool) fs.Result {
	f.lease++
	if f.leased == nil {
		f.leased = map[*fs.Listing]uint32{}
	}
	f.leased[l] = f.lease
	return fs.Result{Kind: fs.ResultListing, ID: id, Req: req, Refresh: refresh, Listing: l, Lease: f.lease}
}

// refresh is a listing of a changed folder: the Service's own request, numbered
// after every List made so far.
func (f *fakeSrc) refresh(id uint32, l *fs.Listing) fs.Result {
	f.req++
	return f.result(id, l, f.req, true)
}

func listing(key fs.SortKey, desc bool, items ...item) *fs.Listing {
	l := sceneListing(items)
	fs.Sort(l, key, desc)
	return l
}

// deliver hands the pane the listing that answers what it asked for last (a
// navigation, a relist), or, when it asked for nothing, a refresh of a changed
// folder.
func deliver(p *pane, l *fs.Listing) {
	f, ok := p.src.(interface {
		result(id uint32, l *fs.Listing, req uint64, refresh bool) fs.Result
		refresh(id uint32, l *fs.Listing) fs.Result
	})
	if !ok {
		p.onResult(&fs.Result{Kind: fs.ResultListing, ID: p.id, Listing: l})
		return
	}
	var r fs.Result
	if p.nav.pending || p.relPending {
		r = f.result(p.id, l, p.floor, false)
	} else {
		r = f.refresh(p.id, l)
	}
	p.onResult(&r)
}

// curRow is the row the cursor is on, -1 if it is on none; anchorRow likewise.
func (v *view) curRow() int { return v.rowOf(v.cursor()) }
func (v *view) anchorRow() int {
	if it, ok := v.ts.Sel.Anchor(); ok {
		return v.rowOf(int32(it))
	}
	return -1
}

// settle runs the pane's slices of work until none is left.
func settle(t testing.TB, p *pane) {
	t.Helper()
	for n := 0; p.work(); n++ {
		if n > 100000 {
			t.Fatal("pane work does not end")
		}
	}
}

func names(p *pane) string {
	v := &p.v
	var sb strings.Builder
	for r := 0; r < v.nRows(); r++ {
		if r > 0 {
			sb.WriteByte(' ')
		}
		sb.Write(v.l.Name(int(v.entryAt(r))))
	}
	return sb.String()
}

func selected(p *pane) string {
	v := &p.v
	var sb strings.Builder
	for r := 0; r < v.nRows(); r++ {
		if e := v.entryAt(r); v.isSel(e) {
			if sb.Len() > 0 {
				sb.WriteByte(' ')
			}
			sb.Write(v.l.Name(int(e)))
		}
	}
	return sb.String()
}

func curName(p *pane) string {
	if p.v.cursor() < 0 {
		return ""
	}
	return string(p.v.l.Name(int(p.v.cursor())))
}

func newTestPane(t testing.TB, src Source, dir string, l *fs.Listing) *pane {
	p := newPane(1, src, nativePath("/home/u"))
	p.setInitial(dir, l)
	settle(t, p)
	if f, ok := src.(*fakeSrc); ok { // every Release carried the lease it was handed out with
		t.Cleanup(func() {
			if f.ignored != 0 {
				t.Errorf("%d Release calls the Service would have ignored (stale or missing lease)", f.ignored)
			}
		})
	}
	return p
}

var dirItems = []item{
	{"zeta", fs.KindDir, 0, 0, "2026-10-01 10:00"},
	{"alpha", fs.KindDir, 0, 0, "2026-10-02 10:00"},
	{".hidden", fs.KindFile, fs.FlagHidden, 5, "2026-10-03 10:00"},
	{"b.txt", fs.KindFile, 0, 300, "2026-10-04 10:00"},
	{"a.txt", fs.KindFile, 0, 100, "2026-10-05 10:00"},
	{"c.txt", fs.KindFile, 0, 200, "2026-10-06 10:00"},
	{"file10", fs.KindFile, 0, 1000, "2026-10-07 10:00"},
	{"file2", fs.KindFile, 0, 2000, "2026-10-08 10:00"},
}

func TestRowsHideHiddenAndSort(t *testing.T) {
	p := newTestPane(t, &fakeSrc{}, nativePath("/d"), listing(fs.SortName, false, dirItems...))
	if got, want := names(p), "alpha zeta a.txt b.txt c.txt file2 file10"; got != want {
		t.Errorf("rows = %q, want %q", got, want)
	}
	p.toggleHidden()
	settle(t, p)
	if got, want := names(p), ".hidden alpha zeta a.txt b.txt c.txt file2 file10"; !strings.HasSuffix(got, "file10") || !strings.Contains(got, ".hidden") {
		t.Errorf("rows = %q, want %q", got, want)
	}
	if p.v.nRows() != 8 {
		t.Errorf("%d rows with hidden shown, want 8", p.v.nRows())
	}
}

// The pane's selection is the table's: by entry index, counted with the size of
// what is selected, and a range runs over display rows (here with the hidden
// file out of sight).
func TestSelectionOps(t *testing.T) {
	p := newTestPane(t, &fakeSrc{}, nativePath("/d"), listing(fs.SortName, false, dirItems...))
	v := &p.v
	sel := &v.ts.Sel
	// rows: 0 alpha, 1 zeta, 2 a.txt, 3 b.txt, 4 c.txt, 5 file2, 6 file10
	v.selectOnly(v.entryAt(2))
	if selected(p) != "a.txt" || v.nSel() != 1 || v.selBytes() != 100 {
		t.Fatalf("click: %q %d %d", selected(p), v.nSel(), v.selBytes())
	}
	sel.Toggle(uint32(v.entryAt(4))) // ⌘-click adds
	if selected(p) != "a.txt c.txt" || v.selBytes() != 300 || v.cursor() != v.entryAt(4) {
		t.Fatalf("⌘-click: %q %d", selected(p), v.selBytes())
	}
	sel.Toggle(uint32(v.entryAt(2))) // ⌘-click again removes
	if selected(p) != "c.txt" || v.selBytes() != 200 {
		t.Fatalf("⌘-click off: %q %d", selected(p), v.selBytes())
	}
	// shift-click selects the range from the anchor (the last plain or ⌘ click), replacing the rest
	sel.SelectRange(v.order(), v.anchorRow(), 6)
	if got := selected(p); got != "a.txt b.txt c.txt file2 file10" {
		// anchor was row 2 (the toggled-off row), so rows 2..6 are selected
		t.Fatalf("shift-click: %q", got)
	}
	sel.SelectRange(v.order(), v.anchorRow(), 3) // shrinking the range keeps the anchor
	if got := selected(p); got != "a.txt b.txt" || v.curRow() != 3 || v.anchorRow() != 2 {
		t.Fatalf("shift-click back: %q cur %d anchor %d", got, v.curRow(), v.anchorRow())
	}
	sel.SelectRange(v.order(), 0, v.nRows()-1) // all the rows shown
	if v.nSel() != 7 || v.selBytes() != 100+300+200+1000+2000 {
		t.Fatalf("select all: %d %d", v.nSel(), v.selBytes())
	}
	v.selectOnly(v.entryAt(0))
	if selected(p) != "alpha" || v.nSel() != 1 {
		t.Fatalf("move: %q", selected(p))
	}
	sel.SelectRange(v.order(), v.anchorRow(), v.nRows()-1) // extends from row 0
	if v.nSel() != 7 || v.curRow() != 6 {
		t.Fatalf("extend to the end: %d rows selected, cursor %d", v.nSel(), v.curRow())
	}
	sel.Clear()
	if v.nSel() != 0 || v.selBytes() != 0 || selected(p) != "" {
		t.Fatal("clear")
	}
}

func TestSelectionSurvivesARefreshByName(t *testing.T) {
	src := &fakeSrc{}
	a := listing(fs.SortName, false, dirItems...)
	p := newTestPane(t, src, nativePath("/d"), a)
	v := &p.v
	v.selectOnly(v.entryAt(2))            // a.txt
	v.ts.Sel.Toggle(uint32(v.entryAt(4))) // c.txt
	v.ts.Sel.Toggle(uint32(v.entryAt(1))) // zeta (a folder: no size)
	if v.cursor() < 0 || curName(p) != "zeta" {
		t.Fatal("setup")
	}

	// The directory changed: a.txt is gone, b.txt grew, new files appeared, and
	// the order is by size now (the pane's sort has been changed).
	changed := []item{
		{"zeta", fs.KindDir, 0, 0, "2026-10-01 10:00"},
		{"alpha", fs.KindDir, 0, 0, "2026-10-02 10:00"},
		{"b.txt", fs.KindFile, 0, 9999, "2026-10-04 10:00"},
		{"c.txt", fs.KindFile, 0, 200, "2026-10-06 10:00"},
		{"new.txt", fs.KindFile, 0, 1, "2026-10-09 10:00"},
		{"file10", fs.KindFile, 0, 1000, "2026-10-07 10:00"},
	}
	p.sortKey = fs.SortSize
	deliver(p, listing(fs.SortSize, false, changed...))
	if !p.work() && p.rc.active {
		t.Fatal("reconcile claims to be done in one slice while active")
	}
	settle(t, p)
	if got, want := names(p), "alpha zeta new.txt c.txt file10 b.txt"; got != want {
		t.Fatalf("rows = %q, want %q", got, want)
	}
	if got := selected(p); got != "zeta c.txt" {
		t.Errorf("selection = %q, want %q", got, "zeta c.txt")
	}
	if v.nSel() != 2 || v.selBytes() != 200 {
		t.Errorf("count %d bytes %d, want 2 and 200", v.nSel(), v.selBytes())
	}
	if curName(p) != "zeta" || v.curRow() != 1 {
		t.Errorf("cursor %q at row %d, want zeta at 1", curName(p), v.curRow())
	}
	if src.released != 1 {
		t.Errorf("%d listings released, want the old one", src.released)
	}
}

func TestRefreshKeepsTheScrollAnchor(t *testing.T) {
	var items []item
	for i := 0; i < 500; i++ {
		items = append(items, item{fmt.Sprintf("f%04d", i), fs.KindFile, 0, int64(i), "2026-10-01 10:00"})
	}
	p := newTestPane(t, &fakeSrc{}, nativePath("/d"), listing(fs.SortName, false, items...))
	// The view is scrolled so that f0123 is half a row from the top.
	p.topRow, p.topEntry, p.topFrac = 123, p.v.entryAt(123), 0.5
	more := append([]item{{"a-new", fs.KindFile, 0, 1, "2026-10-01 10:00"}}, items...)
	deliver(p, listing(fs.SortName, false, more...))
	settle(t, p)
	if p.scrollRow != 124 || p.scrollOff != 0.5 {
		t.Errorf("scroll restore row %d off %v, want 124 (one row was added above) and 0.5", p.scrollRow, p.scrollOff)
	}
}

func TestEntriesTakeTheirHiddenAndQueryStateThroughARefresh(t *testing.T) {
	p := newTestPane(t, &fakeSrc{}, nativePath("/d"), listing(fs.SortName, false, dirItems...))
	p.filterOpen = true
	p.filter.SetString("TXT")
	p.setQuery()
	settle(t, p)
	if got := names(p); got != "a.txt b.txt c.txt" {
		t.Fatalf("filtered rows = %q", got)
	}
	deliver(p, listing(fs.SortName, false, append(dirItems, item{"d.txt", fs.KindFile, 0, 1, "2026-10-09 10:00"})...))
	settle(t, p)
	if got := names(p); got != "a.txt b.txt c.txt d.txt" {
		t.Errorf("rows after a refresh = %q", got)
	}
	p.closeFilter()
	settle(t, p)
	if p.v.nRows() != 8 { // 9 entries, one hidden
		t.Errorf("%d rows after the filter closed, want 8", p.v.nRows())
	}
}

func TestFilterKeepsTheCursorRowInStep(t *testing.T) {
	p := newTestPane(t, &fakeSrc{}, nativePath("/d"), listing(fs.SortName, false, dirItems...))
	v := &p.v
	v.selectOnly(v.entryAt(4)) // c.txt
	p.filterOpen = true
	p.filter.SetString("txt")
	p.setQuery()
	settle(t, p)
	if v.curRow() != 2 || curName(p) != "c.txt" {
		t.Errorf("cursor at row %d (%q), want c.txt at 2", v.curRow(), curName(p))
	}
	p.filter.SetString("zzz")
	p.setQuery()
	settle(t, p)
	if v.nRows() != 0 || v.curRow() != -1 || v.nSel() != 1 {
		t.Errorf("no matches: %d rows, cursor %d, %d selected (selection stays)", v.nRows(), v.curRow(), v.nSel())
	}
}

func TestNavigationHistory(t *testing.T) {
	src := &fakeSrc{}
	p := newTestPane(t, src, nativePath("/a"), listing(fs.SortName, false, dirItems...))
	if p.hist.canBack() || p.hist.canForward() {
		t.Fatal("history starts empty")
	}
	for _, d := range []string{nativePath("/a/b"), nativePath("/a/b/c"), nativePath("/x")} {
		p.request(d, navUser, "")
		if got := src.last(); got != d {
			t.Fatalf("requested %q, want %q", got, d)
		}
		deliver(p, listing(fs.SortName, false, dirItems...))
		settle(t, p)
		if p.dir != d {
			t.Fatalf("dir %q, want %q", p.dir, d)
		}
	}
	if !p.hist.canBack() || p.hist.canForward() {
		t.Fatal("back only")
	}
	p.goBack()
	deliver(p, listing(fs.SortName, false, dirItems...))
	if p.dir != nativePath("/a/b/c") || !p.hist.canForward() {
		t.Fatalf("back: %q", p.dir)
	}
	p.goBack()
	deliver(p, listing(fs.SortName, false, dirItems...))
	p.goForward()
	deliver(p, listing(fs.SortName, false, dirItems...))
	if p.dir != nativePath("/a/b/c") {
		t.Fatalf("forward: %q", p.dir)
	}
	// A new place drops the forward list.
	p.request(nativePath("/y"), navUser, "")
	deliver(p, listing(fs.SortName, false, dirItems...))
	if p.hist.canForward() || p.hist.dirs[p.hist.i] != nativePath("/y") {
		t.Errorf("forward list kept after a new place: %+v", p.hist)
	}
	if got := src.watched[len(src.watched)-1]; got != nativePath("/y") {
		t.Errorf("watching %q, want /y", got)
	}
}

func TestHistoryIsBounded(t *testing.T) {
	var h history
	for i := 0; i < 3*histMax; i++ {
		h.visit(fmt.Sprint("/d", i))
	}
	if h.n != histMax || h.dirs[h.i] != fmt.Sprint("/d", 3*histMax-1) {
		t.Fatalf("n %d i %d cur %q", h.n, h.i, h.dirs[h.i])
	}
	steps := 0
	for h.canBack() {
		h.goTo(h.i - 1)
		steps++
	}
	if steps != histMax-1 {
		t.Errorf("%d steps back, want %d", steps, histMax-1)
	}
}

func TestUpSelectsTheFolderYouCameFrom(t *testing.T) {
	src := &fakeSrc{}
	p := newTestPane(t, src, nativePath("/a/zeta"), listing(fs.SortName, false, dirItems...))
	p.goUp()
	if src.last() != nativePath("/a") || !p.nav.pending {
		t.Fatalf("up requests %q", src.last())
	}
	deliver(p, listing(fs.SortName, false, dirItems...))
	settle(t, p)
	if p.dir != nativePath("/a") || selected(p) != "zeta" || p.v.curRow() != 1 || p.reveal != 1 {
		t.Errorf("dir %q selected %q row %d reveal %d", p.dir, selected(p), p.v.curRow(), p.reveal)
	}
	if p.pendSel != -1 {
		t.Error("the pending selection is taken")
	}
}

func TestFailedNavigationKeepsThePaneAndShowsWhy(t *testing.T) {
	src := &fakeSrc{}
	p := newTestPane(t, src, nativePath("/a"), listing(fs.SortName, false, dirItems...))
	p.goTo("~/nope")
	if src.last() != nativePath("/home/u/nope") {
		t.Fatalf("~ expands to %q", src.last())
	}
	bad := new(fs.Listing)
	bad.Err = &os.PathError{Op: "open", Path: nativePath("/home/u/nope"), Err: os.ErrPermission}
	deliver(p, bad)
	if p.dir != nativePath("/a") || p.msg != "Permission denied" || !p.msgErr || p.pathErr != "Permission denied" {
		t.Errorf("dir %q msg %q path error %q", p.dir, p.msg, p.pathErr)
	}
	if src.last() != nativePath("/a") {
		t.Errorf("the service lists %q again, want the directory shown (/a)", src.last())
	}
	if p.v.l == nil || p.v.nRows() == 0 {
		t.Error("the old listing stays")
	}
}

func TestGoneMovesToTheNearestAncestor(t *testing.T) {
	src := &fakeSrc{}
	p := newTestPane(t, src, nativePath("/a/b/c"), listing(fs.SortName, false, dirItems...))
	p.onResult(&fs.Result{Kind: fs.ResultGone, ID: p.id})
	if src.last() != nativePath("/a/b") {
		t.Fatalf("goes to %q, want /a/b", src.last())
	}
	bad := new(fs.Listing)
	bad.Err = os.ErrNotExist
	deliver(p, bad) // /a/b is gone as well
	if src.last() != nativePath("/a") {
		t.Fatalf("then %q, want /a", src.last())
	}
	deliver(p, listing(fs.SortName, false, dirItems...))
	if p.dir != nativePath("/a") || !strings.Contains(p.msg, nativePath("/a/b/c")+" was removed") {
		t.Errorf("dir %q msg %q", p.dir, p.msg)
	}
}

func TestErrText(t *testing.T) {
	if got := errText(&os.PathError{Err: os.ErrNotExist}); got != "No such file or directory" {
		t.Error(got)
	}
	if got := errText(errors.New(strings.Repeat("x", 500))); len(got) != 120 {
		t.Errorf("long message is %d bytes", len(got))
	}
}

// ---- keyboard map, through a Pilot without a window ----

type keyRig struct {
	t   *testing.T
	pl  *Pilot
	src *fakeSrc
	c   *ui.Context
}

func newKeyRig(t *testing.T, mac bool) *keyRig {
	t.Helper()
	src := &fakeSrc{}
	pl := New(Options{Src: src, Home: nativePath("/home/u"), Loc: time.UTC})
	pl.mac = mac
	pl.panes[0].setInitial(nativePath("/a/b"), listing(fs.SortName, false, dirItems...))
	pl.panes[1].setInitial(nativePath("/other"), listing(fs.SortName, false, dirItems[:3]...))
	for _, p := range pl.panes {
		settle(t, p)
	}
	r := &keyRig{t: t, pl: pl, src: src, c: ui.New(nil)}
	pl.geo[0].visRows, pl.geo[1].visRows = 4, 4
	var in ui.Input
	r.c.Begin(&in, 1200, 760, 1, 1.0/60)
	return r
}

func (r *keyRig) press(code ui.KeyCode, mods ui.Mods) {
	r.pl.key(r.c, ui.KeyEvent{Code: code, Mods: mods, Down: true})
	settle(r.t, r.pl.panes[r.pl.active])
}

func (r *keyRig) sel() string { return selected(r.pl.panes[r.pl.active]) }

func TestKeyboardMap(t *testing.T) {
	for _, mac := range []bool{true, false} {
		t.Run(fmt.Sprint("mac=", mac), func(t *testing.T) {
			r := newKeyRig(t, mac)
			a := r.pl.panes[0]
			// rows: 0 alpha, 1 zeta, 2 a.txt, 3 b.txt, 4 c.txt, 5 file2, 6 file10
			// (the arrow keys, Page Up and Down, Home and End, select all and Enter
			// belong to the table: see TestListKeysGoThroughTheTable)
			r.press(ui.KeyEscape, 0)
			a.v.selectOnly(a.v.entryAt(2))
			r.press(ui.KeyEscape, 0)
			if a.v.nSel() != 0 {
				t.Error("esc clears the selection")
			}

			// a folder is opened, then Backspace goes to the parent and selects
			// the folder it came from
			a.openRow(0)
			if r.src.last() != nativePath("/a/b/alpha") {
				t.Errorf("opening a folder lists %q", r.src.last())
			}
			deliver(a, listing(fs.SortName, false, dirItems...))
			settle(t, a)

			// Backspace goes to the parent and selects the folder it came from
			r.press(ui.KeyBackspace, 0)
			if r.src.last() != nativePath("/a/b") {
				t.Errorf("backspace lists %q", r.src.last())
			}
			deliver(a, listing(fs.SortName, false, dirItems...))
			settle(t, a)
			if r.sel() != "alpha" {
				t.Errorf("came from alpha, selected %q", r.sel())
			}

			// back / forward
			if mac {
				r.press(ui.KeyLeftBracket, ui.ModSuper)
			} else {
				r.press(ui.KeyLeft, ui.ModAlt)
			}
			if r.src.last() != nativePath("/a/b/alpha") {
				t.Errorf("back lists %q", r.src.last())
			}
			deliver(a, listing(fs.SortName, false, dirItems...))
			if mac {
				r.press(ui.KeyRightBracket, ui.ModSuper)
			} else {
				r.press(ui.KeyRight, ui.ModAlt)
			}
			if r.src.last() != nativePath("/a/b") {
				t.Errorf("forward lists %q", r.src.last())
			}
			deliver(a, listing(fs.SortName, false, dirItems...))

			// the parent shortcut
			if mac {
				r.press(ui.KeyUp, ui.ModSuper)
			} else {
				r.press(ui.KeyUp, ui.ModAlt)
			}
			if r.src.last() != nativePath("/a") {
				t.Errorf("parent shortcut lists %q", r.src.last())
			}

			// hidden files
			if mac {
				r.press(ui.KeyPeriod, ui.ModSuper|ui.ModShift)
			} else {
				r.press('H', ui.ModCtrl)
			}
			if a.hideHidden {
				t.Error("hidden shortcut")
			}

			// Tab goes to the other pane's table; that pane keeps its own state
			r.press(ui.KeyTab, 0)
			if r.pl.active != 1 {
				t.Error("tab")
			}
			r.pl.panes[1].v.selectOnly(r.pl.panes[1].v.entryAt(0))
			if r.sel() != "alpha" || a.v.nSel() != 0 {
				t.Errorf("right pane selection %q, left %d", r.sel(), a.v.nSel())
			}
			r.press(ui.KeyTab, ui.ModShift)
			if r.pl.active != 0 {
				t.Error("tab back")
			}
		})
	}
}

func TestEscapeClosesTheFilterBeforeTheSelection(t *testing.T) {
	r := newKeyRig(t, true)
	a := r.pl.panes[0]
	a.v.selectOnly(a.v.entryAt(0))
	a.filterOpen = true
	a.filter.SetString("a")
	a.setQuery()
	settle(t, a)
	r.press(ui.KeyEscape, 0)
	if a.filterOpen || len(a.q) != 0 || a.v.nSel() != 1 {
		t.Fatalf("first Esc: filter open %v query %q selected %d", a.filterOpen, a.q, a.v.nSel())
	}
	r.press(ui.KeyEscape, 0)
	if a.v.nSel() != 0 {
		t.Error("second Esc clears the selection")
	}
}

func TestTypingStartsTheFilter(t *testing.T) {
	r := newKeyRig(t, true)
	var in ui.Input
	in.Focused = true
	in.AppendText("a")
	if !r.pl.typed(&in) {
		t.Error("a printable letter starts the filter")
	}
	in.ClearFrame()
	in.AppendText(" ")
	if r.pl.typed(&in) {
		t.Error("a leading space does not")
	}
	in.ClearFrame()
	in.Mods = ui.ModSuper
	in.AppendText("l")
	if r.pl.typed(&in) {
		t.Error("a shortcut does not")
	}
}

// A directory that gains a few files between two refreshes must not make the
// pane reallocate its row and selection buffers: they are made with slack.
func TestRefreshOfAGrowingDirectoryAllocatesNothing(t *testing.T) {
	mk := func(n int) *fs.Listing {
		items := make([]item, n)
		for i := range items {
			items[i] = item{fmt.Sprintf("f%05d", i), fs.KindFile, 0, int64(i), "2026-10-01 10:00"}
		}
		return listing(fs.SortName, false, items...)
	}
	p := newTestPane(t, &fakeSrc{}, nativePath("/d"), mk(1000))
	p.v.selectOnly(p.v.entryAt(5))
	lists := [...]*fs.Listing{mk(1000), mk(1000), mk(1000)}
	for _, l := range lists[:2] { // warm: the pane's two row buffers and the reconcile tables
		deliver(p, l)
		settle(t, p)
	}
	bigger := mk(1003)
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	deliver(p, bigger)
	settle(t, p)
	runtime.ReadMemStats(&m1)
	if p.v.nEntries() != 1003 || p.v.nSel() != 1 {
		t.Fatalf("refresh did not land: %d entries, %d selected", p.v.nEntries(), p.v.nSel())
	}
	if n := m1.Mallocs - m0.Mallocs; n != 0 {
		t.Errorf("a refresh with 3 more entries allocated %d times (%d bytes)", n, m1.TotalAlloc-m0.TotalAlloc)
	}
}

func TestErrTextWindows(t *testing.T) {
	for en, want := range map[syscall.Errno]string{267: "Not a directory", 123: "Invalid name or path", 53: "Network path not found"} {
		err := &os.PathError{Op: "fs.list", Path: "C:/x", Err: en}
		if got := errTextWith(true, err); got != want {
			t.Errorf("errno %d: %q, want %q", en, got, want)
		}
	}
	if got := errTextWith(false, &os.PathError{Err: syscall.Errno(267)}); got == "Not a directory" {
		t.Error("Windows error numbers mean something else elsewhere")
	}
}

// A root has nowhere above it: a pane at a root that disappears (a drive that
// is gone) says so instead of walking up for ever, and up does nothing there.
func TestGoneAtARootStops(t *testing.T) {
	src := &fakeSrc{}
	root := nativePath("/")
	p := newTestPane(t, src, root, listing(fs.SortName, false, dirItems...))
	if p.canUp() {
		t.Error("a root has no parent to go up to")
	}
	p.goUp()
	p.relist()
	n := len(src.lists)
	p.onResult(&fs.Result{Kind: fs.ResultGone, ID: p.id})
	if p.relPending {
		t.Error("a relist the Source dropped with the folder is still awaited")
	}
	if len(src.lists) != n || p.nav.pending || !p.msgErr || !strings.Contains(p.msg, "was removed") {
		t.Errorf("requests %d→%d, pending %v, msg %q", n, len(src.lists), p.nav.pending, p.msg)
	}
	// and a chain of failing ancestors ends at the root
	p = newTestPane(t, src, nativePath("/a/b"), listing(fs.SortName, false, dirItems...))
	p.onResult(&fs.Result{Kind: fs.ResultGone, ID: p.id})
	for i := 0; i < 5 && p.nav.pending; i++ {
		bad := new(fs.Listing)
		bad.Err = os.ErrNotExist
		deliver(p, bad)
	}
	if p.nav.pending || !p.msgErr {
		t.Errorf("walking up did not end at the root: pending %v msg %q (last %q)", p.nav.pending, p.msg, src.last())
	}
}

// Opening a file asks the Source (a non-blocking call) and a failure that
// comes back through Poll lands in the status line.
func TestOpenFileIsAsync(t *testing.T) {
	src := &fakeSrc{}
	p := newTestPane(t, src, nativePath("/d"), listing(fs.SortName, false, dirItems...))
	p.v.selectOnly(p.v.entryAt(2)) // a.txt
	p.openCursor()
	if len(src.opened) != 1 || src.opened[0] != joinPath(nativePath("/d"), "a.txt") {
		t.Fatalf("opened %q", src.opened)
	}
	if p.msgErr {
		t.Errorf("status %q reported as an error before anything failed", p.msg)
	}
	p.onResult(&fs.Result{Kind: fs.ResultOpenError, ID: p.id, Err: os.ErrNotExist})
	if !p.msgErr || p.msg == "" {
		t.Errorf("open failure not shown: %q (err %v)", p.msg, p.msgErr)
	}
}

// A listing is released with the lease it arrived with, including when a
// refresh replaces it.
func TestListingsReleasedWithTheirLease(t *testing.T) {
	src := &leaseSrc{}
	p := newTestPane(t, src, nativePath("/d"), listing(fs.SortName, false, dirItems...))
	r11 := src.refresh(p.id, listing(fs.SortName, false, dirItems...))
	r11.Lease = 11
	p.onResult(&r11)
	settle(t, p) // the refresh is swapped in; the initial listing (lease 0) goes back
	r12 := src.refresh(p.id, listing(fs.SortName, false, dirItems...))
	r12.Lease = 12
	p.onResult(&r12)
	settle(t, p) // lease 11 goes back
	if got := fmt.Sprint(src.leases); got != "[0 11]" {
		t.Errorf("released leases %s, want [0 11]", got)
	}
}

type leaseSrc struct {
	fakeSrc
	leases []uint32
}

func (f *leaseSrc) Release(l *fs.Listing, lease uint32) { f.leases = append(f.leases, lease) }
