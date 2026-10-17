package pilot

import (
	"math/bits"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gonativeui/fs"
	"gonativeui/ui"
)

type navKind uint8

const (
	navUser navKind = iota // a folder opened, the path field: recorded in the history
	navHist                // back or forward: the history position moved already
	navUp                  // to the parent: selects the folder it came from
	navGone                // the folder disappeared: the nearest ancestor that exists
)

// pane is one side of Pilot: a directory, how it is shown and the state that
// goes with it. It owns no widgets; build.go draws it from this.
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

	pathErr string // shown in the path field: why Enter did not navigate
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
	}

	rc      reconcile
	jobPos  int   // rows job: next index in l.Order
	jobBusy bool  // rows are being (re)built
	pendSel int32 // entry to select once its row is known, -1: none
	pendRow int

	// what Build maintains for the pane and takes back
	topEntry  int32   // entry in the first visible row, -1 none
	topFrac   float32 // how far the row has scrolled out, 0..1
	topRow    int
	reveal    int // row to scroll into view at the next build, -1 none
	scrollRow int // row to put at the top at the next build (after a swap), -1 none
	scrollOff float32
}

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

// request lists path for display; the result arrives through onResult.
func (p *pane) request(path string, kind navKind, sel string) {
	if p.src == nil {
		p.setMsg("Not available", true)
		return
	}
	p.nav.pending, p.nav.path, p.nav.kind, p.nav.sel = true, path, kind, sel
	p.nav.req = p.src.List(p.id, path, p.sortKey, p.desc)
	p.msg, p.pathErr = "", ""
}

func (p *pane) setMsg(s string, isErr bool) { p.msg, p.msgErr = s, isErr }

// onResult takes what the Source reports for the pane.
func (p *pane) onResult(r *fs.Result) {
	switch r.Kind {
	case fs.ResultListing:
		p.onListing(r.Listing, r.Lease)
	case fs.ResultGone:
		if !p.nav.pending || p.nav.kind != navGone {
			p.nav.gone = p.dir
		}
		if !p.canUp() { // a root that is gone: nothing above to go to
			p.setMsg(p.dir+" was removed", true)
			return
		}
		p.request(p.parent, navGone, "")
	case fs.ResultWatchError:
		// no live updates for this directory; the listing itself is fine
	case fs.ResultOpenError:
		p.setMsg(errText(r.Err), true)
	}
}

func (p *pane) onListing(l *fs.Listing, lease uint32) {
	if p.nav.pending {
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
			p.setMsg(errText(err), true)
			p.pathErr = errText(err)
			if p.dir != "" { // the Service lists its last path again on a change: put it back
				p.src.List(p.id, p.dir, p.sortKey, p.desc)
			}
			return
		}
		kind, path, sel := p.nav.kind, p.nav.path, p.nav.sel
		p.install(path, l, lease, sel)
		switch kind {
		case navUser, navUp:
			p.hist.visit(path)
		case navGone:
			p.setMsg(p.nav.gone+" was removed; showing "+path, true)
		}
		p.watch()
		return
	}
	if l.Err != nil {
		p.release(l, lease)
		p.setMsg(errText(l.Err), true)
		return
	}
	p.startReconcile(l, lease)
}

// install makes l the pane's listing in dir, dropping the selection and the
// scroll position (a different directory).
func (p *pane) install(dir string, l *fs.Listing, lease uint32, sel string) {
	if p.rc.active {
		p.release(p.rc.l, p.rc.lease)
		p.rc.active, p.rc.l, p.rc.lease = false, nil, 0
	}
	p.release(p.v.l, p.v.lease)
	p.setDir(dir)
	p.v.reset()
	p.v.l, p.v.lease = l, lease
	p.v.sizeSel(len(l.Entries))
	p.path.SetString(dir)
	p.pathErr, p.msg = "", ""
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

type budget struct{ start time.Time }

func (b *budget) over() bool { return time.Since(b.start) >= workSlice }

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
	v.curRow, v.anchorRow, p.pendRow = -1, -1, -1
	v.filtered = p.hideHidden || len(p.q) > 0
	p.jobPos = 0
	p.jobBusy = v.filtered || v.cur >= 0 || v.anchor >= 0 || p.pendSel >= 0
	if v.filtered && cap(v.rows) < len(v.l.Order) {
		v.rows = make([]uint32, 0, withSlack(len(v.l.Order)))
	}
}

// filterChunk appends to rows the visible entries among l.Order[pos:pos+n] and
// returns the grown rows and the new position.
func filterChunk(l *fs.Listing, q []byte, hide bool, rows []uint32, pos, n int) ([]uint32, int) {
	end := min(pos+n, len(l.Order))
	sub := fs.Listing{Entries: l.Entries, Names: l.Names, Order: l.Order[pos:end]}
	n0 := len(rows)
	rows = fs.Filter(&sub, q, rows)
	if hide {
		w := n0
		for _, e := range rows[n0:] {
			if l.Entries[e].Flags&fs.FlagHidden == 0 {
				rows[w] = e
				w++
			}
		}
		rows = rows[:w]
	}
	return rows, end
}

// scanTargets notes the rows of the cursor, the anchor and the folder to select
// among rows, which are the entries of rows base, base+1, ...
func (p *pane) scanTargets(rows []uint32, base int) {
	v := &p.v
	cur, anchor, pend := uint32(v.cur), uint32(v.anchor), uint32(p.pendSel) // -1 never matches
	for i, e := range rows {
		switch e {
		case cur:
			v.curRow = base + i
		case anchor:
			v.anchorRow = base + i
		case pend:
			p.pendRow = base + i
		}
		if e == cur && e == anchor {
			v.anchorRow = base + i
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
			v.rows, p.jobPos = filterChunk(v.l, p.q, p.hideHidden, v.rows, p.jobPos, step)
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
			p.v.selectOnly(e, p.pendRow)
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
	p.q = append(p.q[:0], q...)
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
// listing stays on screen. Phases: 0 hashes the selected names of the old
// listing, 1 looks for them in the new one, 2 builds the new rows, 3 finds the
// rows of the cursor, the anchor and the top entry, then the swap.
type reconcile struct {
	active bool
	l      *fs.Listing
	lease  uint32 // the Source lease l came with
	phase  int
	pos    int
	ver    uint32 // the view's selection version when it began
	fver   uint32

	tab                 nameSet
	hCur, hAnchor, hTop uint64
	newSel              []uint64
	nNew                int
	newBytes            int64
	newCur, newAnchor   int32
	newTop              int32
	topFrac             float32

	rows    []uint32
	rowsPos int

	curRow, anchorRow, topRow int
}

func (p *pane) startReconcile(l *fs.Listing, lease uint32) {
	rc := &p.rc
	if rc.active {
		p.release(rc.l, rc.lease)
	}
	v := &p.v
	rc.active, rc.l, rc.lease, rc.phase, rc.pos = true, l, lease, 0, 0
	rc.ver, rc.fver = v.ver, p.fver
	rc.hCur, rc.hAnchor, rc.hTop = 0, 0, 0
	rc.nNew, rc.newBytes, rc.newCur, rc.newAnchor, rc.newTop = 0, 0, -1, -1, -1
	rc.curRow, rc.anchorRow, rc.topRow = -1, -1, -1
	rc.rowsPos = 0
	rc.rows = rc.rows[:0]
	if v.l != nil {
		if v.cur >= 0 {
			rc.hCur = nameHash(v.l.Name(int(v.cur)))
		}
		if v.anchor >= 0 {
			rc.hAnchor = nameHash(v.l.Name(int(v.anchor)))
		}
		if p.topEntry >= 0 && p.topRow > 0 {
			rc.hTop = nameHash(v.l.Name(int(p.topEntry)))
			rc.topFrac = p.topFrac
		}
	}
	w := (len(l.Entries) + 63) >> 6
	if cap(rc.newSel) < w {
		rc.newSel = make([]uint64, withSlack(w))
	}
	rc.newSel = rc.newSel[:w]
	clear(rc.newSel)
	rc.tab.reset(v.nSel)
	switch {
	case v.nSel > 0:
		rc.phase = 0
	case rc.hCur != 0 || rc.hAnchor != 0 || rc.hTop != 0:
		rc.phase = 1
	default:
		rc.phase = 2
	}
}

func (p *pane) reconcileStep(b *budget) {
	rc, v := &p.rc, &p.v
	if rc.ver != v.ver && rc.phase < 2 { // the selection changed meanwhile: start over
		l, lease := rc.l, rc.lease
		rc.l, rc.lease = nil, 0
		rc.active = false
		p.startReconcile(l, lease)
	}
	if rc.phase == 0 && v.l != nil {
		for rc.pos < len(v.sel) {
			x := v.sel[rc.pos]
			for x != 0 {
				i := rc.pos<<6 + trailing(x)
				x &= x - 1
				rc.tab.add(nameHash(v.l.Name(i)))
			}
			rc.pos++
			if rc.pos&15 == 0 && b.over() {
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
			if rc.tab.has(h) {
				w, m := i>>6, uint64(1)<<(uint(i)&63)
				if rc.newSel[w]&m == 0 {
					rc.newSel[w] |= m
					rc.nNew++
					if e := &l.Entries[i]; e.Kind != fs.KindDir {
						rc.newBytes += e.Size
					}
				}
			}
			if h == rc.hCur && rc.newCur < 0 {
				rc.newCur = int32(i)
			}
			if h == rc.hAnchor && rc.newAnchor < 0 {
				rc.newAnchor = int32(i)
			}
			if h == rc.hTop && rc.newTop < 0 {
				rc.newTop = int32(i)
			}
			if rc.nNew == rc.tab.n && p.foundAll() { // everything looked for is found
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
				rc.rows, rc.rowsPos = filterChunk(rc.l, p.q, p.hideHidden, rc.rows, rc.rowsPos, step)
				if b.over() {
					break
				}
			}
			if rc.rowsPos < len(rc.l.Order) {
				return
			}
		}
		rc.phase, rc.pos = 3, 0
	}
	if rc.phase == 3 {
		if rc.newCur >= 0 || rc.newAnchor >= 0 || rc.newTop >= 0 {
			rows := rc.l.Order
			if p.hideHidden || len(p.q) > 0 {
				rows = rc.rows
			}
			for rc.pos < len(rows) {
				end := min(rc.pos+4*chunk, len(rows))
				for i, e := range rows[rc.pos:end] {
					switch int32(e) {
					case rc.newCur:
						rc.curRow = rc.pos + i
					case rc.newAnchor:
						rc.anchorRow = rc.pos + i
					case rc.newTop:
						rc.topRow = rc.pos + i
					}
					if int32(e) == rc.newCur && int32(e) == rc.newAnchor {
						rc.anchorRow = rc.pos + i
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

func (p *pane) foundAll() bool {
	rc := &p.rc
	return (rc.hCur == 0 || rc.newCur >= 0) && (rc.hAnchor == 0 || rc.newAnchor >= 0) && (rc.hTop == 0 || rc.newTop >= 0)
}

// swapIn replaces the shown listing by the reconciled one.
func (p *pane) swapIn() {
	rc, v := &p.rc, &p.v
	old, oldLease := v.l, v.lease
	v.l, v.lease = rc.l, rc.lease
	v.sel, rc.newSel = rc.newSel, v.sel
	v.nSel, v.selBytes = rc.nNew, rc.newBytes
	v.cur, v.anchor = rc.newCur, rc.newAnchor
	v.curRow, v.anchorRow = rc.curRow, rc.anchorRow
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
	e := v.entryAt(r)
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

// openCursor opens the cursor row, or, with a single selected folder, that one.
func (p *pane) openCursor() { p.openRow(p.v.curRow) }

func (p *pane) goUp() {
	if !p.canUp() {
		return
	}
	p.request(p.parent, navUp, filepath.Base(p.dir))
}

func (p *pane) goBack() {
	if d, ok := p.hist.back(); ok {
		p.request(d, navHist, "")
	}
}

func (p *pane) goForward() {
	if d, ok := p.hist.forward(); ok {
		p.request(d, navHist, "")
	}
}

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
	if p.src != nil && p.dir != "" && !p.nav.pending {
		p.src.List(p.id, p.dir, p.sortKey, p.desc)
	}
}

func trailing(x uint64) int { return bits.TrailingZeros64(x) }
