package pilot

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"gonativeui/core"
	"gonativeui/fs"
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget"
)

type navKind uint8

const (
	navUser navKind = iota // a folder opened, the path field: recorded in the history
	navHist                // back or forward: the history position moved already
	navUp                  // to the parent: selects the folder it came from
	navGone                // the folder disappeared: the nearest ancestor that exists
)

// pane is one side of Pilot: a directory, how it is shown and the state that
// goes with it (the table state, the text buffers). build.go draws it from this.
type pane struct {
	id   uint32
	src  Source
	home string

	dir     string // the directory shown
	parent  string // its parent (dir itself at a root)
	base    string // the folder name for the title
	v       view
	sortKey fs.SortKey
	desc    bool

	hideHidden bool
	filterOpen bool
	filter     ui.TextBuffer // the type-to-filter field
	path       ui.TextBuffer // the path field
	q          []byte        // the query the rows were built for
	fver       uint32        // counts changes of q and hideHidden

	pathErr string // shown under the path field (in the status line): why Enter did not navigate
	pathReq uint64 // the request the path field's Enter made, while its listing is awaited
	pathOK  bool   // a folder was shown while the path field was open: the field goes away
	msg     string // status line: an error or a note
	msgErr  bool

	hist history

	nav struct {
		pending bool
		path    string
		req     uint64
		kind    navKind
		sel     string // folder name to select when the listing arrives
		gone    string // navGone: the folder that disappeared
		hidx    int    // navHist: the history position of the folder
	}

	// Which listings the pane takes. Every List the pane makes has a number
	// (the Source's numbers only grow) and supersedes the ones before: floor is
	// the latest, and a listing that answers a lower one is stale however late
	// Poll hands it over. relPending: a relist of dir (sorting, a refresh, putting
	// back after a failed navigation) is owed. goneInNav: dir was reported gone
	// while a navigation elsewhere was already in flight.
	floor      uint64
	relPending bool
	goneInNav  bool

	rc        reconcile
	jobPos    int  // rows job: next index in l.Order
	jobBusy   bool // rows are being (re)built
	rowFilter rowFilter
	pendSel   int32 // entry to select once its row is known, -1: none
	pendRow   int

	// what Build maintains for the pane and takes back
	topEntry  int32   // entry in the first visible row, -1 none
	topFrac   float32 // how far the row has scrolled out, 0..1
	topRow    int
	reveal    int // row to scroll into view at the next build, -1 none
	scrollRow int // row to put at the top at the next build (after a swap), -1 none
	scrollOff float32

	cols [3]widget.Column // the table's columns: Name, Date Modified, Size (the user can resize them)

	// the path bar: the components of dir from the root, and the bar's items
	// (symbols set when it is drawn); pathEdit shows the path field in its
	// place (⌘L) until it is left
	crumbs    []crumb
	crumbItem []rexui.PathItem
	pathEdit  bool
	rootTitle string
}

// crumb is one component of the path bar: its name and the folder it opens.
type crumb struct{ title, path string }

func newPane(id uint32, src Source, home string) *pane {
	p := &pane{id: id, src: src, home: home, hideHidden: true, pendSel: -1, topEntry: -1, reveal: -1, scrollRow: -1}
	p.v.reset()
	p.q = make([]byte, 0, 256)
	return p
}

func (p *pane) setDir(dir string) {
	p.dir = dir
	p.parent = parentPath(dir)
	p.base = filepath.Base(dir)
	if p.base == "." || p.base == string(filepath.Separator) {
		p.base = dir
		if p.rootTitle != "" && dir == p.parent {
			p.base = p.rootTitle
		}
	}
	p.setCrumbs()
}

// setCrumbs makes the path bar's components: the folders from the root to
// dir, the root named by the volume's name.
func (p *pane) setCrumbs() {
	p.crumbs = p.crumbs[:0]
	for d := p.dir; d != ""; {
		title := filepath.Base(d)
		parent := parentPath(d)
		if parent == d {
			title = d
			if p.rootTitle != "" {
				title = p.rootTitle
			}
		}
		p.crumbs = append(p.crumbs, crumb{title, d})
		if parent == d {
			break
		}
		d = parent
	}
	for i, j := 0, len(p.crumbs)-1; i < j; i, j = i+1, j-1 {
		p.crumbs[i], p.crumbs[j] = p.crumbs[j], p.crumbs[i]
	}
	p.crumbItem = p.crumbItem[:0]
	for _, c := range p.crumbs {
		p.crumbItem = append(p.crumbItem, rexui.PathItem{Title: c.title})
	}
}

func (p *pane) canUp() bool { return p.dir != "" && p.parent != p.dir }

func (p *pane) loading() bool { return p.nav.pending || p.v.l == nil }

// release gives a listing back to the Source with the lease it came with
// (0 for listings the Source did not make).
func (p *pane) release(l *fs.Listing, lease uint32) {
	if l != nil && p.src != nil {
		p.src.Release(l, lease)
	}
}

// ---- opening a directory ----

// setInitial installs a listing made before the window existed.
func (p *pane) setInitial(dir string, l *fs.Listing) {
	p.install(dir, l, 0, "")
	p.hist.visit(dir)
	p.watch()
}

func (p *pane) watch() {
	if p.src != nil {
		p.src.Watch(p.id, p.dir)
	}
}

// request lists path for display; the result arrives through onResult. It
// supersedes any other List of the pane.
func (p *pane) request(path string, kind navKind, sel string) {
	if p.src == nil {
		p.setMsg("Not available", true)
		return
	}
	p.nav.pending, p.nav.path, p.nav.kind, p.nav.sel = true, path, kind, sel
	p.relPending = false
	p.nav.req = p.src.List(p.id, path, p.sortKey, p.desc)
	p.floor = p.nav.req
	p.msg, p.pathErr = "", ""
}

// relist asks for dir again, as a change on disk, another sort order or a
// failed navigation (the Source lists its last path again on a change: put it
// back) needs. A navigation in flight is not disturbed.
func (p *pane) relist() {
	if p.src == nil || p.dir == "" || p.nav.pending {
		return
	}
	p.floor = p.src.List(p.id, p.dir, p.sortKey, p.desc)
	p.relPending = true
}

// dropPathEdit ends the path field without going anywhere: the text goes back
// to the folder shown, and the reason it was refused with it.
func (p *pane) dropPathEdit() {
	p.path.SetString(p.dir)
	p.clearPathErr()
	p.pathEdit = false
	// The navigation the field asked for goes with it: its listing, when it
	// comes, is nobody's (another navigation, started since, is left alone).
	if p.nav.pending && p.pathReq != 0 && p.nav.req == p.pathReq {
		p.nav.pending = false
	}
	p.pathReq = 0
}

// clearPathErr takes away the reason the path was refused, in the status line
// as well (a failed navigation sets both).
func (p *pane) clearPathErr() {
	if p.msg == p.pathErr {
		p.msg = ""
	}
	p.pathErr = ""
}

func (p *pane) setMsg(s string, isErr bool) { p.msg, p.msgErr = s, isErr }

// onResult takes what the Source reports for the pane.
func (p *pane) onResult(r *fs.Result) {
	switch r.Kind {
	case fs.ResultListing:
		p.onListing(r)
	case fs.ResultGone:
		p.relPending = false // the Source dropped what was queued for the folder
		if p.nav.pending && p.nav.kind != navGone && p.nav.path != p.dir {
			// the user is already going elsewhere (the Source leaves that
			// request standing); if it fails, the pane moves up from here
			p.goneInNav = true
			return
		}
		if !p.nav.pending || p.nav.kind != navGone {
			p.nav.gone = p.dir
		}
		p.goUpFromGone()
	case fs.ResultWatchError:
		// no live updates for this directory; the listing itself is fine
	case fs.ResultOpenError:
		p.setMsg(errText(r.Err), true)
	}
}

// goUpFromGone shows the parent of a folder that disappeared (the nearest
// ancestor that exists, one step at a time).
func (p *pane) goUpFromGone() {
	if !p.canUp() { // a root that is gone: nothing above to go to
		p.setMsg(p.dir+" was removed", true)
		return
	}
	p.request(p.parent, navGone, "")
}

func (p *pane) onListing(r *fs.Result) {
	l, lease := r.Listing, r.Lease
	if l == nil {
		return
	}
	// Take only the answer to the latest request. The Source drops superseded
	// results itself, but a Poll batch can still hold a listing that an earlier
	// event in the same batch (a folder gone, a new navigation) made obsolete,
	// and the pane does not rely on that order: such a listing must never be
	// shown under the path of a later request. A relist of a changed folder
	// (Refresh) is the only listing nobody asked for, and only when no
	// navigation is in flight.
	switch {
	case r.Req < p.floor,
		p.nav.pending && r.Refresh,
		!p.nav.pending && !p.relPending && !r.Refresh:
		p.release(l, lease)
		return
	}
	p.floor = max(p.floor, r.Req)
	if p.nav.pending {
		p.onNavListing(l, lease)
		return
	}
	if !r.Refresh {
		p.relPending = false
	}
	if l.Err != nil {
		err := l.Err // (before the listing goes back: a worker may reuse it at once)
		p.release(l, lease)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || onWindows && errors.Is(err, winErrNotDirectory) {
			// Focus-gain relisting also covers a missed deletion notification.
			p.onResult(&fs.Result{Kind: fs.ResultGone, ID: p.id})
			return
		}
		p.setMsg(errText(err), true)
		return
	}
	p.startReconcile(l, lease)
}

// onNavListing takes the answer to the navigation in flight.
func (p *pane) onNavListing(l *fs.Listing, lease uint32) {
	p.nav.pending = false
	if l.Err != nil {
		err := l.Err
		p.release(l, lease)
		if p.nav.kind == navGone {
			if parent := parentPath(p.nav.path); parent != p.nav.path {
				p.request(parent, navGone, "")
				return
			}
		}
		if p.goneInNav { // the folder shown is gone and the way out failed: up from here
			p.goneInNav = false
			p.nav.gone = p.dir
			p.goUpFromGone()
			return
		}
		p.setMsg(errText(err), true)
		p.pathErr = errText(err)
		p.relist()
		return
	}
	kind, path, sel, hidx := p.nav.kind, p.nav.path, p.nav.sel, p.nav.hidx
	p.install(path, l, lease, sel)
	switch kind {
	case navUser, navUp:
		p.hist.visit(path)
	case navHist:
		p.hist.goTo(hidx) // the position moves when the folder is shown, not when it is asked for
	case navGone:
		p.hist.replaceCurrent(path)
		p.setMsg(p.nav.gone+" was removed; showing "+path, true)
	}
	p.watch()
}

// install makes l the pane's listing in dir, dropping the selection and the
// scroll position (a different directory).
func (p *pane) install(dir string, l *fs.Listing, lease uint32, sel string) {
	if p.rc.active {
		p.release(p.rc.l, p.rc.lease)
		p.rc.active, p.rc.l, p.rc.lease = false, nil, 0
	}
	p.release(p.v.l, p.v.lease)
	p.goneInNav = false
	p.setDir(dir)
	p.v.reset()
	p.v.l, p.v.lease = l, lease
	p.v.ts.Sel.Reserve(withSlack(len(l.Entries)))
	p.path.SetString(dir)
	p.pathErr, p.msg = "", ""
	p.pathOK = p.pathEdit // the path field, if open, has done its work
	p.filter.Reset()
	p.filterOpen = false
	p.q = p.q[:0]
	p.pendSel = findName(l, sel)
	p.scrollRow, p.scrollOff = 0, 0
	p.topEntry, p.topRow, p.reveal = -1, 0, -1
	p.startRows()
}

// findName is the index of the entry called name in l, -1 if there is none.
func findName(l *fs.Listing, name string) int32 {
	if name == "" {
		return -1
	}
	for i := range l.Entries {
		if int(l.Entries[i].NameLen) == len(name) && string(l.Name(i)) == name {
			return int32(i)
		}
	}
	if caseFolds { // the folder was typed in another case than the file system keeps
		for i := range l.Entries {
			if int(l.Entries[i].NameLen) == len(name) && strings.EqualFold(string(l.Name(i)), name) {
				return int32(i)
			}
		}
	}
	return -1
}

// caseFolds: the usual file systems of these OSes ignore case in names.
var caseFolds = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// ---- rows ----

// A frame's slice of the background-style work (filtering, name matching,
// finding rows) is limited by time, not by a count: the cost per entry depends
// on how cold the entry is in the cache (a million entries sorted by size are
// visited at random).
const (
	workSlice = 1 * time.Millisecond
	chunk     = 1024 // entries between two looks at the clock
)

type budget struct {
	start time.Time
	units int
}

func (b *budget) over() bool {
	if core.SimulationForTesting() != nil {
		b.units++
		return b.units >= 32
	}
	return time.Since(b.start) >= workSlice
}

// startRows (re)builds the visible rows for the current query and hidden
// setting. Without either the rows are just the listing's order. A job runs
// while the rows are filtered, or a row has to be found (cursor, anchor, the
// folder to select).
func (p *pane) startRows() {
	v := &p.v
	if v.l == nil {
		return
	}
	p.fver++
	v.rows = v.rows[:0]
	p.pendRow = -1
	v.filtered = p.hideHidden || len(p.q) > 0
	p.jobPos = 0
	p.rowFilter.scan = 0
	p.jobBusy = v.filtered || p.pendSel >= 0
	if v.filtered && cap(v.rows) < len(v.l.Order) {
		v.rows = make([]uint32, 0, withSlack(len(v.l.Order)))
	}
}

type rowFilter struct {
	bits []uint64
	scan int
}

// filterChunk scans names sequentially into match bits, then emits one chunk
// of the sorted permutation. Both passes fit the pane's existing time budget.
func filterChunk(l *fs.Listing, q []byte, hide bool, rows []uint32, pos, n int, f *rowFilter) ([]uint32, int) {
	if len(q) > 0 && f.scan < len(l.Entries) {
		end := min(f.scan+n, len(l.Entries))
		f.bits = fs.FilterBits(l, q, f.bits, f.scan, end)
		f.scan = end
		return rows, pos
	}
	end := min(pos+n, len(l.Order))
	for _, e := range l.Order[pos:end] {
		if hide && l.Entries[e].Flags&fs.FlagHidden != 0 {
			continue
		}
		if len(q) == 0 || f.bits[e/64]&(uint64(1)<<(e%64)) != 0 {
			rows = append(rows, e)
		}
	}
	return rows, end
}

// scanTargets notes the row of the folder to select among rows, which are the
// entries of rows base, base+1, ...
func (p *pane) scanTargets(rows []uint32, base int) {
	pend := uint32(p.pendSel) // -1 never matches
	for i, e := range rows {
		if e == pend {
			p.pendRow = base + i
		}
	}
}

// rowsStep advances the rows job for one time slice.
func (p *pane) rowsStep(b *budget) {
	v := &p.v
	n := len(v.l.Order)
	step := chunk
	if len(p.q) > 0 {
		step /= 2
	}
	for p.jobPos < n {
		if v.filtered {
			n0 := len(v.rows)
			v.rows, p.jobPos = filterChunk(v.l, p.q, p.hideHidden, v.rows, p.jobPos, step, &p.rowFilter)
			p.scanTargets(v.rows[n0:], n0)
		} else {
			end := min(p.jobPos+4*step, n)
			p.scanTargets(v.l.Order[p.jobPos:end], p.jobPos)
			p.jobPos = end
		}
		if b.over() {
			break
		}
	}
	if p.jobPos >= n {
		p.jobBusy = false
		p.rowsDone()
	}
}

// rowsDone runs when the rows are complete: it takes up the folder to select.
func (p *pane) rowsDone() {
	if e := p.pendSel; e >= 0 {
		p.pendSel = -1
		if p.pendRow >= 0 {
			p.v.selectOnly(e)
			p.reveal = p.pendRow
		}
	}
}

// setQuery applies the filter field's text (and the hidden setting) to the rows.
func (p *pane) setQuery() {
	q := p.filter.Bytes()
	if !p.filterOpen {
		q = nil
	}
	changed := !bytes.Equal(p.q, q)
	p.q = append(p.q[:0], q...)
	if changed { // a new search starts at the top, as results do; a view left far down would otherwise stripe rows that are not there
		p.scrollRow, p.scrollOff = 0, 0
	}
	if p.v.l != nil {
		p.startRows()
	}
}

func (p *pane) closeFilter() {
	if !p.filterOpen && p.filter.Len() == 0 {
		return
	}
	p.filter.Reset()
	p.filterOpen = false
	p.setQuery()
}

func (p *pane) toggleHidden() {
	p.hideHidden = !p.hideHidden
	p.setQuery()
}

// ---- keeping the selection across a new listing of the same directory ----

// reconcile carries the selection, the cursor and the scroll position over to a
// fresh listing of the directory by name, a time slice per frame, while the old
// listing stays on screen. Phases: 0 hashes the names of the selected entries,
// the cursor and the anchor of the old listing, 1 looks for them in the new one
// and notes where each went (the map Selection.Remap takes), 2 builds the new
// rows, 3 finds the row of the top entry, then the swap.
type reconcile struct {
	active bool
	l      *fs.Listing
	lease  uint32 // the Source lease l came with
	phase  int
	pos    int
	ver    uint32 // the selection's version when it began
	fver   uint32

	tab     nameMap  // name hash -> entry of the old listing, for what is carried over
	m       []uint32 // entry of the old listing -> entry of the new one, widget.NoItem if gone
	found   int      // entries of tab found in the new listing
	hTop    uint64
	newTop  int32
	topFrac float32

	rows    []uint32
	rowsPos int
	filter  rowFilter

	topRow int
}

func (p *pane) startReconcile(l *fs.Listing, lease uint32) {
	rc := &p.rc
	if rc.active {
		p.release(rc.l, rc.lease)
	}
	v := &p.v
	sel := &v.ts.Sel
	rc.active, rc.l, rc.lease, rc.phase, rc.pos = true, l, lease, 0, 0
	rc.ver, rc.fver = sel.Version(), p.fver
	rc.hTop, rc.newTop, rc.topRow, rc.found = 0, -1, -1, 0
	rc.rowsPos = 0
	rc.filter.scan = 0
	rc.rows = rc.rows[:0]
	rc.tab.reset(sel.Count() + 2)
	if v.l != nil {
		n := len(v.l.Entries)
		if cap(rc.m) < n {
			rc.m = make([]uint32, withSlack(n))
		}
		rc.m = rc.m[:n]
		for i := range rc.m {
			rc.m[i] = widget.NoItem
		}
		if e, ok := sel.Cursor(); ok && int(e) < n {
			rc.tab.add(nameHash(v.l.Name(int(e))), e)
		}
		if e, ok := sel.Anchor(); ok && int(e) < n {
			rc.tab.add(nameHash(v.l.Name(int(e))), e)
		}
		if p.topEntry >= 0 && p.topRow > 0 {
			rc.hTop = nameHash(v.l.Name(int(p.topEntry)))
			rc.topFrac = p.topFrac
		}
	}
	// the new listing may be bigger: have the selection's bitsets ready for it
	sel.Reserve(withSlack(len(l.Entries)))
	switch {
	case sel.Count() > 0:
		rc.phase = 0
	case rc.tab.n > 0 || rc.hTop != 0:
		rc.phase = 1
	default:
		rc.phase = 2
	}
}

func (p *pane) reconcileStep(b *budget) {
	rc, v := &p.rc, &p.v
	// The new listing is reconciled with the selection and the filter as they
	// were when each phase began. If either changed since, what was carried over
	// or built is out of date and would overwrite what the person just did.
	if rc.ver != v.ts.Sel.Version() { // the selection or the cursor changed: start over
		l, lease := rc.l, rc.lease
		rc.l, rc.lease = nil, 0
		rc.active = false
		p.startReconcile(l, lease)
	}
	if rc.phase == 3 && rc.fver != p.fver { // the query or the hidden setting changed: build the rows again
		rc.phase, rc.pos = 2, 0
	}
	if rc.phase == 0 && v.l != nil {
		sel := &v.ts.Sel
		for n := 0; ; n++ {
			it, ok := sel.NextSet(uint32(rc.pos))
			if !ok {
				break
			}
			rc.pos = int(it) + 1
			if int(it) < len(v.l.Entries) {
				rc.tab.add(nameHash(v.l.Name(int(it))), it)
			}
			if n&255 == 255 && b.over() {
				return
			}
		}
		rc.pos, rc.phase = 0, 1
	}
	if rc.phase == 1 {
		l := rc.l
		for rc.pos < len(l.Entries) {
			i := rc.pos
			rc.pos++
			h := nameHash(l.Name(i))
			if old, ok := rc.tab.get(h); ok && rc.m[old] == widget.NoItem {
				rc.m[old] = uint32(i)
				rc.found++
			}
			if h == rc.hTop && rc.newTop < 0 {
				rc.newTop = int32(i)
			}
			if rc.found == rc.tab.n && (rc.hTop == 0 || rc.newTop >= 0) { // everything looked for is found
				rc.pos = len(l.Entries)
			}
			if rc.pos&255 == 0 && b.over() {
				return
			}
		}
		rc.phase = 2
		rc.pos = 0
	}
	if rc.phase == 2 {
		if rc.fver != p.fver { // the query or the hidden setting changed: the rows built so far are for the old one
			rc.fver = p.fver
			rc.rows, rc.rowsPos = rc.rows[:0], 0
			rc.filter.scan = 0
		}
		if p.hideHidden || len(p.q) > 0 {
			if rc.rowsPos == 0 && cap(rc.rows) < len(rc.l.Order) {
				rc.rows = make([]uint32, 0, withSlack(len(rc.l.Order)))
			}
			step := chunk
			if len(p.q) > 0 {
				step /= 2
			}
			for rc.rowsPos < len(rc.l.Order) {
				rc.rows, rc.rowsPos = filterChunk(rc.l, p.q, p.hideHidden, rc.rows, rc.rowsPos, step, &rc.filter)
				if b.over() {
					break
				}
			}
			if rc.rowsPos < len(rc.l.Order) {
				return
			}
		}
		rc.phase, rc.pos = 3, 0
		rc.topRow = -1
	}
	if rc.phase == 3 {
		if rc.newTop >= 0 {
			rows := rc.l.Order
			if p.hideHidden || len(p.q) > 0 {
				rows = rc.rows
			}
			for rc.pos < len(rows) {
				end := min(rc.pos+4*chunk, len(rows))
				for i, e := range rows[rc.pos:end] {
					if int32(e) == rc.newTop {
						rc.topRow = rc.pos + i
					}
				}
				rc.pos = end
				if b.over() {
					return
				}
			}
		}
		p.swapIn()
	}
}

// swapIn replaces the shown listing by the reconciled one.
func (p *pane) swapIn() {
	p.fver++ // invalidate speculative work even when a pooled listing reuses its address
	rc, v := &p.rc, &p.v
	old, oldLease := v.l, v.lease
	v.l, v.lease = rc.l, rc.lease
	v.ts.Sel.Remap(rc.m, len(rc.l.Entries))
	v.filtered = p.hideHidden || len(p.q) > 0
	if v.filtered {
		v.rows, rc.rows = rc.rows, v.rows
	} else {
		v.rows = v.rows[:0]
	}
	rc.active, rc.l, rc.lease = false, nil, 0
	p.jobBusy, p.pendSel = false, -1
	if rc.topRow >= 0 {
		p.scrollRow, p.scrollOff = rc.topRow, rc.topFrac
	}
	p.release(old, oldLease)
	p.msg = ""
}

// work advances the pane's background-style work for one frame and reports
// whether more is left (the next frame is wanted).
func (p *pane) work() bool {
	if !p.rc.active && !p.jobBusy {
		return false
	}
	b := budget{start: time.Now()}
	if p.rc.active {
		p.reconcileStep(&b)
	}
	if p.jobBusy && !p.rc.active && !b.over() {
		p.rowsStep(&b)
	}
	return p.rc.active || p.jobBusy
}

// ---- actions ----

// openRow opens the entry in row r: a folder is entered, a file goes to the
// OS default application.
func (p *pane) openRow(r int) {
	v := &p.v
	if r < 0 || r >= v.nRows() {
		return
	}
	p.openEntry(v.entryAt(r))
}

// openEntry opens entry e of the listing shown.
func (p *pane) openEntry(e int32) {
	v := &p.v
	if v.l == nil || e < 0 || int(e) >= len(v.l.Entries) {
		return
	}
	ent := &v.l.Entries[e]
	full := joinPath(p.dir, string(v.l.Name(int(e))))
	if ent.Kind == fs.KindDir || ent.Flags&fs.FlagSymlinkDir != 0 {
		p.request(full, navUser, "")
		return
	}
	if p.src == nil {
		p.setMsg("Not available", true)
		return
	}
	p.src.Open(p.id, full) // does not block; a failure comes back as ResultOpenError
	p.setMsg("Opening "+string(v.l.Name(int(e))), false)
}

// openCursor opens the entry the cursor is on, if its row is shown.
func (p *pane) openCursor() {
	if e := p.v.cursor(); e >= 0 && p.v.rowOf(e) >= 0 {
		p.openEntry(e)
	}
}

func (p *pane) goUp() {
	if !p.canUp() {
		return
	}
	p.request(p.parent, navUp, filepath.Base(p.dir))
}

// histPos is the history position Back and Forward start from: the one being
// asked for if a Back or Forward is in flight (so that pressing Back twice
// goes two back), else the one shown.
func (p *pane) histPos() int {
	if p.nav.pending && p.nav.kind == navHist {
		return p.nav.hidx
	}
	return p.hist.i
}

// goHist shows the folder at history position i. The position becomes the
// current one only when that folder is on screen: if it cannot be listed
// (deleted since), the pane and its history stay as they were.
func (p *pane) goHist(i int) {
	if d, ok := p.hist.at(i); ok {
		p.request(d, navHist, "")
		p.nav.hidx = i
	}
}

func (p *pane) goBack()    { p.goHist(p.histPos() - 1) }
func (p *pane) goForward() { p.goHist(p.histPos() + 1) }

// goTo navigates to what was typed in the path field.
func (p *pane) goTo(text string) {
	p.request(ResolvePath(text, p.dir, p.home), navUser, "")
}

// sortBy sorts by key; the same key again reverses the order. The new order
// comes from the worker; selection and scroll position carry over.
func (p *pane) sortBy(key fs.SortKey) {
	if key == p.sortKey {
		p.desc = !p.desc
	} else {
		p.sortKey, p.desc = key, false
	}
	switch {
	case p.src == nil || p.dir == "":
	case p.nav.pending:
		// the header shows the new order, so the folder on its way must come in it
		p.nav.req = p.src.List(p.id, p.nav.path, p.sortKey, p.desc)
		p.floor = p.nav.req
	default:
		if sorter, ok := p.src.(interface {
			Resort(uint32, *fs.Listing, uint32, fs.SortKey, bool) uint64
		}); ok && p.v.l != nil {
			if req := sorter.Resort(p.id, p.v.l, p.v.lease, p.sortKey, p.desc); req != 0 {
				p.floor, p.relPending = req, true
				return
			}
		}
		p.relist()
	}
}
