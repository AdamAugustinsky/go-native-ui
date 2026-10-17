package pilot

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"gonativeui/fs"
	"gonativeui/ui"
)

// ---- redraw after model work ----

// A listing that arrives in an idle window is on screen in the frame it is
// taken up in: no later frame is needed to show what the work finished.
func TestRefreshIsDrawnInTheFrameItArrives(t *testing.T) {
	r := newDrive(t)
	a := r.d.panes[0]
	r.settle()
	pix := func() []byte { img, _ := r.o.Frame(); return slices.Clone(img.Pix) }
	idle := pix()

	changed := append([]item{{"aaa-new.txt", fs.KindFile, 0, 1, "2026-10-17 12:00"}}, sceneLeft...)
	r.src.poll = append(r.src.poll, r.src.refresh(a.id, listing(fs.SortName, false, changed...)))
	first := pix()
	if a.v.nEntries() != len(sceneLeft)+1 || a.rc.active {
		t.Fatalf("the refresh is not in: %d entries, reconciling %v", a.v.nEntries(), a.rc.active)
	}
	if bytes.Equal(first, idle) {
		t.Fatal("the frame the refresh arrived in still shows the old listing")
	}
	if !bytes.Equal(first, pix()) {
		t.Error("the next frame differs: the first one was drawn before the work finished")
	}
}

// ---- bench session ----

// The session's waits cover relists (sorting, refreshing), not only
// navigation and the work on a delivered listing; a wait that runs out with
// something outstanding fails the session instead of ending quietly.
func TestSessionWaitsForRelistsAndFailsWhenTheWaitRunsOut(t *testing.T) {
	src := &fakeSrc{}
	pl := New(Options{Src: src, Home: "/"})
	for _, p := range pl.panes {
		p.setInitial(nativePath("/d"), listing(fs.SortName, false, dirItems...))
		settle(t, p)
	}
	s := script{pl: pl}
	if s.busy() {
		t.Fatal("a settled pane is not busy")
	}
	pl.panes[0].sortBy(fs.SortSize)
	if !s.busy() {
		t.Fatal("a sort the Source has not answered is not outstanding")
	}
	deliver(pl.panes[0], listing(fs.SortSize, false, dirItems...))
	if !s.busy() {
		t.Fatal("the answer is delivered but not reconciled yet")
	}
	settle(t, pl.panes[0])
	if s.busy() {
		t.Fatal("answered and reconciled: not busy")
	}
	pl.Refresh(0, nil)
	if !s.busy() {
		t.Fatal("a refresh the Source has not answered is not outstanding")
	}

	// the first wait of the session, at its last frame
	idx := slices.IndexFunc(benchSteps[:], func(st step) bool { return st.op == opWait })
	s.idx, s.t = idx, benchSteps[idx].n-1
	var in ui.Input
	s.frame(&in)
	if s.err == nil {
		t.Fatal("a wait that ran out with a relist outstanding passed")
	}
	if s.idx != idx+1 {
		t.Error("the session goes on after recording the failure")
	}

	// the same wait with nothing outstanding is fine
	deliver(pl.panes[0], listing(fs.SortSize, false, dirItems...))
	settle(t, pl.panes[0])
	s.err, s.idx, s.t = nil, idx, benchSteps[idx].n-1
	s.frame(&in)
	if s.err != nil {
		t.Errorf("a finished wait failed: %v", s.err)
	}
}

// ---- which listing a pane takes ----

func otherItems() []item {
	return []item{{"other1", fs.KindFile, 0, 1, "2026-10-01 10:00"}, {"other2", fs.KindFile, 0, 2, "2026-10-01 10:00"}}
}

// A Poll batch can hold a folder's removal followed by a listing of it that
// was finished before: the listing must not be taken for the answer to the
// request that the removal made (the parent), whatever the order Poll uses.
func TestListingInvalidatedByGoneInTheSameBatchIsNotShown(t *testing.T) {
	src := &fakeSrc{}
	dir := nativePath("/a/b/c")
	p := newTestPane(t, src, dir, listing(fs.SortName, false, dirItems...))
	p.relist() // a refresh is in flight
	older := src.result(p.id, listing(fs.SortName, false, otherItems()...), p.floor, false)
	l0 := p.v.l

	p.onResult(&fs.Result{Kind: fs.ResultGone, ID: p.id})
	if src.last() != nativePath("/a/b") || !p.nav.pending {
		t.Fatalf("the removal asks for the parent: listed %q pending %v", src.last(), p.nav.pending)
	}
	p.onResult(&older)
	if p.dir != dir || p.v.l != l0 || !p.nav.pending {
		t.Fatalf("the stale listing was taken: dir %q, nav pending %v", p.dir, p.nav.pending)
	}
	if src.released != 1 {
		t.Errorf("the stale listing is given back: %d releases", src.released)
	}
	parent := listing(fs.SortName, false, dirItems...)
	deliver(p, parent)
	if p.dir != nativePath("/a/b") || p.v.l != parent {
		t.Errorf("the parent's own listing: dir %q", p.dir)
	}
}

func TestOnlyTheLatestRequestIsAnswered(t *testing.T) {
	src := &fakeSrc{}
	p := newTestPane(t, src, nativePath("/d"), listing(fs.SortName, false, dirItems...))
	l0 := p.v.l

	// a relist in flight, then a navigation: the relist's listing is not the navigation's
	p.relist()
	stale := src.result(p.id, listing(fs.SortName, false, otherItems()...), p.floor, false)
	p.request(nativePath("/x"), navUser, "")
	p.onResult(&stale)
	if p.dir != nativePath("/d") || !p.nav.pending || p.v.l != l0 {
		t.Fatalf("an older answer was taken for the navigation: dir %q pending %v", p.dir, p.nav.pending)
	}
	// a changed folder's refresh does not answer a navigation either
	ref := src.refresh(p.id, listing(fs.SortName, false, otherItems()...))
	p.onResult(&ref)
	if p.dir != nativePath("/d") || !p.nav.pending {
		t.Fatalf("a refresh answered the navigation: dir %q", p.dir)
	}
	x := listing(fs.SortName, false, dirItems...)
	deliver(p, x)
	if p.dir != nativePath("/x") || p.v.l != x {
		t.Fatalf("the navigation's answer: dir %q", p.dir)
	}

	// nothing was asked for: a listing that is not a refresh is not taken
	unasked := src.result(p.id, listing(fs.SortName, false, otherItems()...), src.req+1, false)
	p.onResult(&unasked)
	if p.v.l != x || p.rc.active {
		t.Error("an unsolicited listing was taken")
	}
	// but a relist of a changed folder is
	ref = src.refresh(p.id, listing(fs.SortName, false, otherItems()...))
	p.onResult(&ref)
	if !p.rc.active {
		t.Error("a refresh of the shown folder was dropped")
	}
	// ... unless a later request has been made since
	settle(t, p)
	old := src.refresh(p.id, listing(fs.SortName, false, dirItems...))
	p.relist()
	p.onResult(&old)
	if p.rc.active {
		t.Error("a refresh older than the pane's latest request was taken")
	}
}

// If the folder shown is reported gone while the user is already on the way
// elsewhere, that navigation is not replaced; only if it fails does the pane
// move up from the vanished folder.
func TestGoneWhileNavigatingElsewhere(t *testing.T) {
	src := &fakeSrc{}
	dir := nativePath("/a/b/c")
	p := newTestPane(t, src, dir, listing(fs.SortName, false, dirItems...))
	p.request(nativePath("/x"), navUser, "")
	n := len(src.lists)
	p.onResult(&fs.Result{Kind: fs.ResultGone, ID: p.id})
	if len(src.lists) != n || p.nav.path != nativePath("/x") {
		t.Fatalf("the user's navigation was replaced: %q", src.last())
	}
	deliver(p, listing(fs.SortName, false, dirItems...))
	if p.dir != nativePath("/x") || p.goneInNav {
		t.Errorf("the navigation shows: dir %q", p.dir)
	}

	// it fails: up from the folder that is gone
	p = newTestPane(t, src, dir, listing(fs.SortName, false, dirItems...))
	p.request(nativePath("/x"), navUser, "")
	p.onResult(&fs.Result{Kind: fs.ResultGone, ID: p.id})
	bad := new(fs.Listing)
	bad.Err = os.ErrNotExist
	deliver(p, bad)
	if src.last() != nativePath("/a/b") || p.nav.kind != navGone {
		t.Errorf("after the failure the pane asks for %q (kind %v)", src.last(), p.nav.kind)
	}

	// a navigation to the folder itself is cancelled by the removal: go up
	p = newTestPane(t, src, dir, listing(fs.SortName, false, dirItems...))
	p.request(dir, navUser, "")
	p.onResult(&fs.Result{Kind: fs.ResultGone, ID: p.id})
	if src.last() != nativePath("/a/b") || p.nav.kind != navGone {
		t.Errorf("the removal of the folder being relisted asks for %q", src.last())
	}
}

// ---- reconciliation ----

func stepOnce(p *pane) { b := budget{}; p.reconcileStep(&b) } // a budget that is always over: one chunk per call

func toPhase(t *testing.T, p *pane, phase int) {
	t.Helper()
	for n := 0; p.rc.active && p.rc.phase < phase; n++ {
		if n > 1000 {
			t.Fatal("reconciliation does not advance")
		}
		stepOnce(p)
	}
	if !p.rc.active || p.rc.phase != phase {
		t.Fatalf("not in phase %d (active %v, phase %d)", phase, p.rc.active, p.rc.phase)
	}
}

// bigItems is a folder big enough for a time slice that is always over to
// stop inside each phase of a reconciliation.
func bigItems() []item {
	var it []item
	for i := 0; i < 3000; i++ {
		it = append(it, item{fmt.Sprintf("f%04d.txt", i), fs.KindFile, 0, int64(i), "2026-10-09 10:00"})
	}
	return it
}

func bigPane(t *testing.T) *pane {
	return newTestPane(t, &fakeSrc{}, nativePath("/d"), listing(fs.SortName, false, bigItems()...))
}

// refreshed is the folder with a file more at the top.
func refreshed() *fs.Listing {
	return listing(fs.SortName, false, append(bigItems(), item{"aaa.txt", fs.KindFile, 0, 7, "2026-10-09 10:00"})...)
}

// What the person selects while a new listing is being reconciled is not
// overwritten by the selection the reconciliation began with.
func TestSelectionChangedDuringReconciliationSurvives(t *testing.T) {
	for _, phase := range []int{0, 1, 2, 3} {
		p := bigPane(t)
		v := &p.v
		e := findName(v.l, "f2500.txt")
		v.selectOnly(e, v.rowOf(e))
		deliver(p, refreshed())
		toPhase(t, p, phase)
		c := findName(v.l, "f0200.txt")
		v.selectOnly(c, v.rowOf(c)) // a click in the listing still on screen
		settle(t, p)
		if selected(p) != "f0200.txt" || curName(p) != "f0200.txt" {
			t.Errorf("phase %d: selected %q cursor %q after the click, want f0200.txt", phase, selected(p), curName(p))
		}
		if v.curRow != v.rowOf(v.cur) || v.curRow < 0 {
			t.Errorf("phase %d: cursor row %d", phase, v.curRow)
		}
	}
}

// A filter typed while the rows of the new listing are being built (or have
// just been) applies to the listing that ends up on screen.
func TestFilterChangedDuringReconciliationApplies(t *testing.T) {
	for _, phase := range []int{2, 3} {
		p := bigPane(t)
		v := &p.v
		e := findName(v.l, "f0150.txt")
		v.selectOnly(e, v.rowOf(e)) // a cursor to find in phase 3
		deliver(p, refreshed())
		toPhase(t, p, phase)
		p.filterOpen = true
		p.filter.SetString("f0150")
		p.setQuery()
		settle(t, p)
		if got := names(p); got != "f0150.txt" {
			t.Errorf("phase %d: rows %q, want f0150.txt only", phase, got)
		}
		if v.curRow != 0 || curName(p) != "f0150.txt" {
			t.Errorf("phase %d: cursor row %d (%q)", phase, v.curRow, curName(p))
		}
		p.closeFilter()
		settle(t, p)
		if p.v.nRows() != 3001 {
			t.Errorf("phase %d: %d rows after the filter closed, want 3001", phase, p.v.nRows())
		}
	}
}

// The scroll position follows the top entry even when that entry is also the
// cursor or the anchor.
func TestScrollPositionKeptWhenTheTopEntryIsTheCursor(t *testing.T) {
	src := &fakeSrc{}
	p := newTestPane(t, src, nativePath("/d"), listing(fs.SortName, false, dirItems...))
	v := &p.v
	e := findName(v.l, "b.txt")
	row := v.rowOf(e)
	v.selectOnly(e, row) // cursor and anchor
	p.topEntry, p.topRow, p.topFrac = e, row, 0.25
	deliver(p, listing(fs.SortName, false, append(slices.Clone(dirItems), item{"aaa.txt", fs.KindFile, 0, 7, "2026-10-09 10:00"})...)) // appears above
	settle(t, p)
	want := v.rowOf(findName(v.l, "b.txt"))
	if want != row+1 {
		t.Fatalf("setup: b.txt moved from row %d to %d", row, want)
	}
	if p.scrollRow != want || p.scrollOff != 0.25 {
		t.Errorf("scroll row %d offset %v, want %d and 0.25 (the top entry moved down one row)", p.scrollRow, p.scrollOff, want)
	}
}

// ---- sorting during navigation ----

func TestSortWhileNavigatingChangesTheRequest(t *testing.T) {
	src := &fakeSrc{}
	p := newTestPane(t, src, nativePath("/d"), listing(fs.SortName, false, dirItems...))
	p.request(nativePath("/x"), navUp, "d")
	first := p.nav.req
	stale := src.result(p.id, listing(fs.SortName, false, dirItems...), first, false)
	n := len(src.lists)
	p.sortBy(fs.SortSize)
	if len(src.lists) != n+1 || src.last() != nativePath("/x") || src.keys[len(src.keys)-1] != fs.SortSize {
		t.Fatalf("the folder on its way is asked for again in the new order: lists %q keys %v", src.lists[n:], src.keys)
	}
	if p.nav.req == first || p.nav.kind != navUp || p.nav.sel != "d" {
		t.Errorf("the navigation keeps its kind and the folder to select: %+v", p.nav)
	}
	p.onResult(&stale) // the answer in the old order
	if !p.nav.pending || p.dir != nativePath("/d") {
		t.Fatal("the old order's answer was taken")
	}
	deliver(p, listing(fs.SortSize, false, dirItems...))
	if p.dir != nativePath("/x") || p.sortKey != fs.SortSize {
		t.Errorf("dir %q sort %v", p.dir, p.sortKey)
	}
}

// ---- a listing is read before it goes back ----

type scribbleSrc struct{ fakeSrc }

// Release makes the listing reusable at once, as a worker taking it would.
func (f *scribbleSrc) Release(l *fs.Listing, lease uint32) {
	f.fakeSrc.Release(l, lease)
	l.Reset()
}

func TestErrorIsReadBeforeTheListingGoesBack(t *testing.T) {
	src := &scribbleSrc{}
	p := newTestPane(t, src, nativePath("/d"), listing(fs.SortName, false, dirItems...))
	// a refresh that failed
	bad := new(fs.Listing)
	bad.Err = os.ErrPermission
	r := src.refresh(p.id, bad)
	p.onResult(&r)
	if p.msg != "Permission denied" || !p.msgErr {
		t.Errorf("refresh error: status %q", p.msg)
	}
	// a navigation that failed
	p.request(nativePath("/x"), navUser, "")
	bad = new(fs.Listing)
	bad.Err = os.ErrNotExist
	deliver(p, bad)
	if p.pathErr != "No such file or directory" || p.msg != p.pathErr {
		t.Errorf("navigation error: path %q status %q", p.pathErr, p.msg)
	}
}

// ---- history ----

func TestFailedBackAndForwardLeaveTheHistoryAlone(t *testing.T) {
	src := &fakeSrc{}
	p := newTestPane(t, src, nativePath("/a"), listing(fs.SortName, false, dirItems...))
	visit := func(d string) {
		t.Helper()
		p.request(nativePath(d), navUser, "")
		deliver(p, listing(fs.SortName, false, dirItems...))
		settle(t, p)
	}
	fail := func() {
		t.Helper()
		bad := new(fs.Listing)
		bad.Err = os.ErrNotExist
		deliver(p, bad)
	}
	hist := func() string { return strings.Join(p.hist.dirs[:p.hist.n], " ") + " @" + p.hist.dirs[p.hist.i] }
	visit("/b")
	visit("/c")
	want := strings.Join([]string{nativePath("/a"), nativePath("/b"), nativePath("/c")}, " ") + " @" + nativePath("/c")
	if hist() != want {
		t.Fatalf("setup: %s", hist())
	}

	p.goBack() // /b was deleted
	if src.last() != nativePath("/b") {
		t.Fatalf("Back lists %q", src.last())
	}
	fail()
	if p.dir != nativePath("/c") || hist() != want || !p.msgErr {
		t.Fatalf("after the failed Back: dir %q history %s", p.dir, hist())
	}
	p.goBack() // and again: the same place is tried, not the one before
	if src.last() != nativePath("/b") {
		t.Errorf("the second Back lists %q", src.last())
	}
	fail()

	// a new place after the failures goes after what is shown, not after /b
	visit("/d")
	want = strings.Join([]string{nativePath("/a"), nativePath("/b"), nativePath("/c"), nativePath("/d")}, " ") + " @" + nativePath("/d")
	if hist() != want {
		t.Fatalf("after /d: %s", hist())
	}
	p.goBack()
	deliver(p, listing(fs.SortName, false, dirItems...))
	if p.dir != nativePath("/c") || p.hist.i != 2 || !p.hist.canForward() {
		t.Fatalf("Back from /d: dir %q at %d", p.dir, p.hist.i)
	}
	p.goForward() // /d is gone now
	fail()
	if p.dir != nativePath("/c") || p.hist.i != 2 {
		t.Errorf("failed Forward moved the position: dir %q at %d", p.dir, p.hist.i)
	}

	// Back pressed twice before the first arrives goes two back
	p.goBack()
	p.goBack()
	if src.last() != nativePath("/a") {
		t.Errorf("two Backs list %q, want /a", src.last())
	}
	deliver(p, listing(fs.SortName, false, dirItems...))
	if p.dir != nativePath("/a") || p.hist.i != 0 {
		t.Errorf("two Backs: dir %q at %d", p.dir, p.hist.i)
	}
}

// A folder that disappears is replaced in the history by the ancestor shown
// in its place, so Back does not return to something that is gone.
func TestHistoryFollowsAFolderThatWentAway(t *testing.T) {
	src := &fakeSrc{}
	p := newTestPane(t, src, nativePath("/a"), listing(fs.SortName, false, dirItems...))
	p.request(nativePath("/a/b"), navUser, "")
	deliver(p, listing(fs.SortName, false, dirItems...))
	p.onResult(&fs.Result{Kind: fs.ResultGone, ID: p.id})
	deliver(p, listing(fs.SortName, false, dirItems...))
	if p.dir != nativePath("/a") || p.hist.dirs[p.hist.i] != nativePath("/a") {
		t.Errorf("dir %q, history entry %q", p.dir, p.hist.dirs[p.hist.i])
	}
}

// ---- keyboard focus and input methods ----

// A click into the other pane that is not a click into one of its fields
// takes the keyboard from the field that had it.
func TestClickingTheOtherPaneLeavesTheTextField(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	r.key('L', pl.primary())
	if r.o.UI.FocusKey() != pathKey(0) {
		t.Fatal("setup: path field focused")
	}
	g := &pl.geo[1]
	r.at(g.rect[0]+40, g.rect[3]-4) // the status line of the right pane
	r.click(0)
	if pl.active != 1 || r.o.UI.FocusKey() != 0 {
		t.Fatalf("active %d focus %v: the left field kept the keyboard", pl.active, r.o.UI.FocusKey())
	}
	cur := pl.panes[1].v.curRow
	before := pl.panes[0].path.String()
	r.key(ui.KeyDown, 0)
	r.text("x")
	if pl.panes[1].v.curRow == cur && pl.panes[1].v.nRows() > 1 && !pl.panes[1].filterOpen {
		t.Error("keys do not reach the active pane's list")
	}
	if pl.panes[0].path.String() != before {
		t.Errorf("the left path field was edited: %q", pl.panes[0].path.String())
	}

	// a column header of the other pane does the same from the left filter
	r.at(pl.geo[0].list[0]+30, pl.geo[0].list[1]+20)
	r.click(0)
	r.text("m")
	if r.o.UI.FocusKey() != filterKey(0) {
		t.Fatal("setup: left filter focused")
	}
	s := script{pl: pl}
	r.at(s.headerPoint(1, 0))
	r.click(0)
	if pl.active != 1 || r.o.UI.FocusKey() == filterKey(0) {
		t.Errorf("header click: active %d focus %v", pl.active, r.o.UI.FocusKey())
	}
	// clicking into a field of the other pane still focuses that field
	L := pl.layoutPane(pl.geo[0].rect[2]-pl.geo[0].rect[0], pl.geo[0].rect[3]-pl.geo[0].rect[1], false)
	r.at(pl.geo[0].rect[0]+(L.path[0]+L.path[2])/2, pl.geo[0].rect[1]+(L.path[1]+L.path[3])/2)
	r.click(0)
	if pl.active != 0 || r.o.UI.FocusKey() != pathKey(0) {
		t.Errorf("click into a field: active %d focus %v", pl.active, r.o.UI.FocusKey())
	}
}

func (r *drive) preedit(s string) {
	r.feed(func(in *ui.Input) {
		in.MouseX, in.MouseY = r.x, r.y
		in.SetPreedit([]byte(s), len(s), 0)
	})
}

func (r *drive) endComposition(commit string) {
	r.feed(func(in *ui.Input) {
		in.MouseX, in.MouseY = r.x, r.y
		in.SetPreedit(nil, 0, 0)
		if commit != "" {
			in.AppendText(commit)
		}
	})
}

// An input method that starts composing while the list has the focus starts
// the filter and the filter owns the composition; what is composed is committed
// into the filter when the focus goes elsewhere, and the composition that is
// still on is not taken up by the filter again.
func TestCompositionInTheFilter(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a := pl.panes[0]
	r.rowAt(0, 0)
	r.click(0)
	r.preedit("に")
	if !a.filterOpen || r.o.UI.FocusKey() != filterKey(0) {
		t.Fatalf("composing starts the filter: open %v focus %v", a.filterOpen, r.o.UI.FocusKey())
	}
	r.frames(1)
	if r.o.UI.IMECancel() {
		t.Error("the filter that began the composition is told to drop it")
	}
	r.endComposition("日本")
	if a.filter.String() != "日本" || string(a.q) != "日本" {
		t.Errorf("committed text: filter %q query %q", a.filter.String(), a.q)
	}

	// compose again, then click into the list: the text is committed in the
	// filter, the focus is left alone (not taken back by the composition)
	r.preedit("ご")
	r.frames(1)
	r.rowAt(0, 3)
	r.press(1, 0)
	if r.o.UI.FocusKey() != 0 {
		t.Errorf("the list click did not keep the focus: %v (the composition took it back)", r.o.UI.FocusKey())
	}
	if a.filter.String() != "日本ご" {
		t.Errorf("the composition was not committed into the filter: %q", a.filter.String())
	}
	if !r.o.UI.IMECancel() {
		t.Error("the platform's composition is not discarded")
	}
	r.release()
	r.endComposition("")
	r.frames(2)
	if r.o.UI.FocusKey() != 0 {
		t.Errorf("focus %v after the composition ended", r.o.UI.FocusKey())
	}
}

// While an input method composes in a field the keys are its; Tab does not
// switch panes under it. A click on the other field commits into the first.
func TestCompositionAndTabAndFieldClicks(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a := pl.panes[0]
	r.rowAt(0, 0)
	r.click(0)
	r.preedit("に")
	r.frames(1)
	r.key(ui.KeyTab, 0)
	r.key(ui.KeyEscape, 0)
	if pl.active != 0 || r.o.UI.FocusKey() != filterKey(0) || !a.filterOpen {
		t.Fatalf("keys while composing: active %d focus %v filter open %v", pl.active, r.o.UI.FocusKey(), a.filterOpen)
	}
	L := pl.layoutPane(pl.geo[0].rect[2]-pl.geo[0].rect[0], pl.geo[0].rect[3]-pl.geo[0].rect[1], true)
	r.at(pl.geo[0].rect[0]+(L.path[0]+L.path[2])/2, pl.geo[0].rect[1]+(L.path[1]+L.path[3])/2)
	r.press(1, 0)
	if r.o.UI.FocusKey() != pathKey(0) {
		t.Fatalf("focus %v, want the path field", r.o.UI.FocusKey())
	}
	if a.filter.String() != "に" || string(a.q) != "に" {
		t.Errorf("filter %q query %q: the composition is committed into the field it belongs to", a.filter.String(), a.q)
	}
	if a.path.String() != a.dir {
		t.Errorf("the path field took the composition: %q", a.path.String())
	}
	if !r.o.UI.IMECancel() {
		t.Error("the platform's composition is not discarded")
	}
	r.release()
	r.endComposition("")
}
