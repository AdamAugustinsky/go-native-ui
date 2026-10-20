package pilot

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"gonativeui/fs"
	"gonativeui/ui"
)

// atNode points at the middle of the node of the last frame with the role and
// name.
func (r *drive) atNode(role ui.Role, name string) {
	r.t.Helper()
	var tr ui.SemTree
	r.o.UI.Semantics(&tr)
	for i := range tr.Nodes {
		if n := &tr.Nodes[i]; n.Role == role && string(tr.Text(n.Name)) == name {
			r.at((n.Bounds[0]+n.Bounds[2])/2, (n.Bounds[1]+n.Bounds[3])/2)
			return
		}
	}
	r.t.Fatalf("no %d named %q in the semantic tree", role, name)
}

// nodeNamed reports whether the last frame's tree has a node with the role and
// name.
func (r *drive) hasNode(role ui.Role, name string) bool {
	var tr ui.SemTree
	r.o.UI.Semantics(&tr)
	for i := range tr.Nodes {
		if n := &tr.Nodes[i]; n.Role == role && string(tr.Text(n.Name)) == name {
			return true
		}
	}
	return false
}

// A press on a place in the sidebar opens it in the pane that has the
// keyboard, and the place is the selected row once the folder shows.
func TestSidebarOpensAPlaceInTheActivePane(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a, b := pl.panes[0], pl.panes[1]
	if pl.side.Sel != pl.placeFor(a.dir) || pl.side.Sel == 0 {
		t.Fatalf("the scene's folder is a place: selected row %d, place %d", pl.side.Sel, pl.placeFor(a.dir))
	}
	r.atNode(ui.RoleListItem, "Downloads")
	r.click(0)
	if got, want := r.src.last(), nativePath("/Users/rex/Downloads"); got != want {
		t.Fatalf("a press on Downloads lists %q, want %q", got, want)
	}
	deliver(a, listing(fs.SortName, false, sceneRight...))
	r.settle()
	if a.dir != nativePath("/Users/rex/Downloads") || pl.side.Sel != pl.placeFor(a.dir) {
		t.Errorf("the pane shows %q, the selected row is %d (want %d)", a.dir, pl.side.Sel, pl.placeFor(a.dir))
	}
	// the other pane is its own: its folder is not the sidebar's selection until it has the keyboard
	r.rowAt(1, 0)
	r.click(0)
	if pl.active != 1 || b.dir != nativePath("/Users/rex/Downloads") {
		t.Fatalf("setup: active %d, right pane %q", pl.active, b.dir)
	}
	r.at(pl.geo[0].rect[0]+60, pl.geo[0].rect[1]+60)
	r.click(0)
	if pl.active != 0 {
		t.Errorf("a click in the left list: active %d", pl.active)
	}
}

// The toolbar's back, forward and up act on the active pane; back and forward
// are the capsule Finder has, up the item after it. Hidden from Tab and
// disabled when there is nowhere to go.
func TestToolbarNavigates(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a := pl.panes[0]
	n := len(r.src.lists)
	r.atNode(ui.RoleButton, "Back")
	r.click(0)
	r.atNode(ui.RoleButton, "Forward")
	r.click(0)
	if len(r.src.lists) != n {
		t.Fatalf("back and forward with no history list %q", r.src.last())
	}
	r.atNode(ui.RoleButton, "Enclosing Folder")
	r.click(0)
	if got, want := r.src.last(), nativePath("/Users/rex"); got != want {
		t.Fatalf("up lists %q, want %q", got, want)
	}
	deliver(a, listing(fs.SortName, false, sceneRight...))
	r.settle()
	if a.dir != nativePath("/Users/rex") || !a.hist.canBack() {
		t.Fatalf("up: showing %q, can go back %v", a.dir, a.hist.canBack())
	}
	r.atNode(ui.RoleButton, "Back")
	r.click(0)
	if got, want := r.src.last(), nativePath("/Users/rex/Documents"); got != want {
		t.Errorf("back lists %q, want %q", got, want)
	}
}

// The toolbar's search button and ⌘F open the filter in the active pane and
// give it the keyboard; ⌘⌥S (Ctrl+B elsewhere) hides and shows the sidebar and the toolbar's
// first item does as well.
func TestFilterButtonAndSidebarToggle(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a := pl.panes[0]
	r.atNode(ui.RoleButton, "Filter")
	r.click(0)
	if !a.filterOpen || r.o.UI.FocusKey() != filterKey(0) {
		t.Fatalf("the search button: filter open %v, focus %v", a.filterOpen, r.o.UI.FocusKey())
	}
	r.key(ui.KeyEscape, 0)
	if a.filterOpen {
		t.Fatal("Esc closes the filter")
	}
	r.key('F', pl.primary())
	if !a.filterOpen || r.o.UI.FocusKey() != filterKey(0) {
		t.Errorf("⌘F: filter open %v, focus %v", a.filterOpen, r.o.UI.FocusKey())
	}
	r.key(ui.KeyEscape, 0)

	w0 := pl.geo[0].rect[0]
	if pl.mac {
		r.key('S', pl.primary()|ui.ModAlt)
	} else {
		r.key('B', pl.primary()) // Ctrl+Alt is AltGr on Windows
	}
	r.frames(2)
	if pl.sidebar || pl.geo[0].rect[0] >= w0 || r.hasNode(ui.RoleListItem, "Downloads") {
		t.Errorf("⌘⌥S: sidebar shown %v, the list starts at %v (was %v)", pl.sidebar, pl.geo[0].rect[0], w0)
	}
	r.atNode(ui.RoleButton, "Sidebar")
	r.click(0)
	r.frames(2)
	if !pl.sidebar || pl.geo[0].rect[0] != w0 {
		t.Errorf("the toolbar's item: sidebar shown %v, the list starts at %v (want %v)", pl.sidebar, pl.geo[0].rect[0], w0)
	}
}

// The path bar shows the folder's components, each of which opens it; a press
// beside them, or ⌘L, types a path.
func TestPathBarOpensAncestorsAndTypesAPath(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a := pl.panes[0]
	if len(a.crumbs) != 4 || a.crumbs[0].title != "Macintosh HD" || a.crumbs[3].title != "Documents" {
		t.Fatalf("the components: %+v", a.crumbs)
	}
	r.atNode(ui.RoleButton, "rex")
	r.click(0)
	if got, want := r.src.last(), nativePath("/Users/rex"); got != want {
		t.Fatalf("a press on rex lists %q, want %q", got, want)
	}
	r.atBand(0)
	r.press(1, 0)
	r.release()
	r.frames(2)
	if !a.pathEdit || r.o.UI.FocusKey() != pathKey(0) {
		t.Fatalf("a press on the band: editing %v, focus %v", a.pathEdit, r.o.UI.FocusKey())
	}
	r.key(ui.KeyEscape, 0)
	r.frames(2)
	if a.pathEdit || r.o.UI.FocusKey() != listKey(0) {
		t.Errorf("Esc: editing %v, focus %v", a.pathEdit, r.o.UI.FocusKey())
	}
}

// The list's icons are the system's: once decoded, every row of the first
// pane shows an image (macOS and Windows always have them; where a system has
// none the symbol stands in, which is not an image node).
func TestRowsShowSystemIcons(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("the system's icons are macOS's and Windows'")
	}
	r := newDrive(t)
	pl := r.d.Pilot
	var tr ui.SemTree
	r.o.UI.Semantics(&tr)
	n := 0
	for i := range tr.Nodes {
		if tr.Nodes[i].Role == ui.RoleImage && tr.Nodes[i].Bounds[0] < pl.geo[1].rect[0] {
			n++
		}
	}
	if rows := min(pl.panes[0].v.nRows(), pl.geo[0].visRows); n < rows {
		t.Errorf("%d icons for %d rows", n, rows)
	}
}

// A home folder or a startup volume that does not answer must not hold up the
// window: the first frame is drawn at once with the places that need no look
// at the disk (the home folder, the volume under its usual name), and the
// others, and the volume's real name, come in when the worker gets its answer.
func TestSlowFileSystemProbesDoNotHoldUpTheFirstFrame(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	timer := time.AfterFunc(4*time.Second, release) // (what a stalled probe would take)
	defer timer.Stop()

	opt := SceneOptions()
	opt.Places, opt.RootTitle = nil, ""
	opt.Probe = Probe{
		IsDir:      func(string) bool { <-gate; return true },
		VolumeName: func() string { <-gate; return "Big Disk" },
	}
	pl := New(opt)
	t0 := time.Now()
	o := newOffscreen(t, pl)
	o.Frame()
	if d := time.Since(t0); d > time.Second {
		t.Fatalf("the window took %v to draw its first frame with the probes blocked", d)
	}
	r := &drive{t: t, o: o}
	if !r.hasNode(ui.RoleListItem, "rex") || !r.hasNode(ui.RoleListItem, startupVolumeName()) {
		t.Errorf("the first frame lacks the places that need no probe")
	}
	if r.hasNode(ui.RoleListItem, "Documents") || r.hasNode(ui.RoleListItem, "Big Disk") {
		t.Errorf("places and names that need a probe are there before it answered")
	}
	release()
	for n := 0; n < 500 && !(r.hasNode(ui.RoleListItem, "Documents") && r.hasNode(ui.RoleListItem, "Big Disk")); n++ {
		time.Sleep(2 * time.Millisecond)
		o.Frame()
	}
	for _, name := range []string{"Documents", "Downloads", "Desktop", "Big Disk"} {
		if !r.hasNode(ui.RoleListItem, name) {
			t.Errorf("%q did not appear once the probe answered", name)
		}
	}
}
