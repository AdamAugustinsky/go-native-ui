package ui

import (
	"strconv"

	"gonativeui/text"
)

// Layout audit (phase 6). Context.Audit looks at the finished frame for the
// mistakes a person sees as misalignment and that no type checks catch: an
// alignment that cannot apply, children outside their parent, siblings on
// top of each other, content running under a pane, a column whose title is
// not over its values, and an icon off the centre of its capsule. It is for
// tests and debugging (widgettest.AuditLayout runs it on a test's frames):
// nothing calls it in a running app, and a frame that is not audited pays
// nothing for it beyond two bytes of its boxes' records. It reads the layout
// as End left it, walks the boxes a few times (linear in them, and in the
// boxes beside each opaque floating one), and allocates only for what it
// reports. Distances are in pixels; "a point" is Context.Scale pixels.

// AuditCheck is one check of Context.Audit.
type AuditCheck uint8

const (
	// AuditAlign: a box was given NextAlign (other than AlignLeft) but draws
	// no text and has children. Align moves a box's own text only; the
	// children stay where Justify and Cross put them.
	AuditAlign AuditCheck = iota
	// AuditOverflow: a box reaches outside its parent's rectangle by more than
	// half a pixel on an axis on which the parent neither clips (Clip) nor
	// scrolls. Overlay roots have no parent and are kept inside the window by
	// their placement. A floating box that only draws an outline and has no
	// children (a focus ring, a bezel drawn round its control) is a
	// decoration and is not checked.
	AuditOverflow
	// AuditOverlap: two flow children of a linear box (AxisX or AxisY) overlap
	// (a negative gap, say). Floating children and flows are not checked.
	AuditOverlap
	// AuditCovered: content runs under a floating sibling that hides it, by
	// more than a point both ways. Content is text and images (measured by
	// their glyph and image boxes) and backgrounds and borders that scroll;
	// fixed shapes stacked on purpose (layered backgrounds, a spinner's
	// petals) are not. The sibling hides it when it is opaque (DrawBackground
	// with four opaque corner colours at full opacity) and drawn after the
	// content, or when it is a material region (the system's material behind
	// the window's pixels): then content of any sibling that reaches into it
	// without lying inside it counts. A part that a Clip ancestor below the
	// common parent cuts off (a scroll area whose viewport ends at the pinned
	// header) does not count. A floating box that content is meant to pass
	// under, such as a toolbar with a scroll-edge effect, says so with
	// NextAuditExempt(AuditCovered.Mask()). An overlay over the main tree is
	// not a finding: covering is what menus, popovers and dialogs are for.
	// Each floating box is reported once.
	AuditCovered
	// AuditColumn: in a table, a cell's content is not on its column header's
	// alignment edge, or not on the edge of the column's first cell, within
	// half a point: the left edges for a left-aligned column, the right edges
	// for a right-aligned one and the centres for a centred one, taken from
	// the alignment of the cell's widest text. The edges are those of the
	// union of the glyph and image boxes in the header and in the cell (an
	// icon and a name, a title and its sort mark), without the spaces at the
	// ends of a text. A header is a box with RoleColumnHeader and a cell one
	// with RoleCell (a cell inside a cell is part of it); the cells of a
	// RoleRow are matched to the headers of their RoleTable (or of the window,
	// without one) by order. Each column is reported once against its header
	// and once against its first cell.
	AuditColumn
	// AuditCentre: content meant to be centred is not, within half a point:
	// the children of a box with JustifyCenter, as a group, along its axis;
	// and the only flow child of a capsule (a box whose corners are rounded by
	// half its shorter side, less a pixel) or of a box with RoleButton, when
	// that child is an icon (an image, or text of one glyph) and the box draws
	// no text: the icon's image box, or its glyph box (the glyph's advance by
	// the font's ascent and descent), must have its centre on the box's centre
	// on both axes.
	AuditCentre

	numAuditChecks
)

// AuditMask is a set of AuditChecks.
type AuditMask uint8

// AuditAll is every check.
const AuditAll AuditMask = 1<<numAuditChecks - 1

// Mask is the set of check k alone.
func (k AuditCheck) Mask() AuditMask { return 1 << k }

var auditNames = [numAuditChecks]string{"align", "overflow", "overlap", "covered", "column", "centre"}

// String is the check's short name: align, overflow, overlap, covered,
// column or centre.
func (k AuditCheck) String() string {
	if k < numAuditChecks {
		return auditNames[k]
	}
	return "check" + strconv.Itoa(int(k))
}

// Finding is one thing Context.Audit found.
type Finding struct {
	// Check is the check that found it.
	Check AuditCheck
	// Key and Rect are the box it is about and its rectangle (x0, y0, x1,
	// y1, window pixels, before snapping).
	Key  Key
	Rect [4]float32
	// Other and OtherRect are the box it is measured against: the parent of
	// an overflow, the earlier sibling of an overlap, the floating box over
	// covered content, the column header of a cell, the box that should
	// centre its content. Zero for AuditAlign.
	Other     Key
	OtherRect [4]float32
	// Delta is how far off it is, in pixels: the overflow or overlap, the
	// distance to the header's edge or to the centre, the side of the covered
	// part. 0 for AuditAlign.
	Delta float32
	// Where and OtherWhere describe Key and Other for a person: the box's text
	// or role and name, and those of the nearest named ancestors.
	Where, OtherWhere string
}

// String is the finding in one line.
func (f Finding) String() string {
	b := make([]byte, 0, 128)
	b = append(b, f.Check.String()...)
	b = append(b, ": "...)
	b = append(b, f.Where...)
	switch f.Check {
	case AuditAlign:
		b = append(b, " has NextAlign but no text: its children are not moved (use NextJustify / NextCross)"...)
	case AuditOverflow:
		b = append(b, " overflows "...)
	case AuditOverlap:
		b = append(b, " overlaps "...)
	case AuditCovered:
		b = append(b, " runs under "...)
	case AuditColumn:
		b = append(b, " is off the edge of "...)
	case AuditCentre:
		b = append(b, " is off the centre of "...)
	}
	if f.Check != AuditAlign {
		b = append(b, f.OtherWhere...)
		b = append(b, " by "...)
		b = strconv.AppendFloat(b, float64(f.Delta), 'f', 1, 32)
		b = append(b, " px"...)
	}
	b = append(b, " at "...)
	b = appendRect(b, f.Rect)
	return string(b)
}

func appendRect(b []byte, r [4]float32) []byte {
	b = append(b, '(')
	for i, v := range r {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = strconv.AppendFloat(b, float64(v), 'f', 1, 32)
	}
	return append(b, ')')
}

// NextAuditExempt exempts the next box from the checks m of Audit: what it
// does is deliberate. As the box a finding would be about, it is not
// reported; as the box it would be measured against, it allows that: a parent
// that its children may overflow, a floating box that content may run under
// (a scroll-edge effect), a header whose cells may sit elsewhere, a capsule
// whose icon is optically off-centre.
func (c *Context) NextAuditExempt(m AuditMask) { c.nextAudit |= m }

// Audit appends to dst what the checks of AuditCheck find in the last
// finished frame (call it after End) and returns it. It allocates only to
// grow dst and to describe its findings.
func (c *Context) Audit(dst []Finding) []Finding {
	if len(c.order) == 0 {
		return dst
	}
	tol := float32(0.5)
	ptTol := 0.5*max(c.scale, 1) + 0.01
	boxes := c.boxes
	for _, id := range c.order {
		b := &boxes[id]
		if b.hid {
			continue
		}
		// (a) an alignment without text
		if b.alignNext && b.align != AlignLeft && b.first != 0 && b.flags&DrawText == 0 && !b.exempt(AuditAlign) {
			dst = c.report(dst, AuditAlign, id, 0, 0)
		}
		if p := b.parent; p != 0 {
			dst = c.auditOverflow(dst, id, p, tol)
		}
		if b.first == 0 {
			continue
		}
		if b.axis != AxisFlow {
			dst = c.auditOverlap(dst, id, tol)
		}
		dst = c.auditCovers(dst, id, 2*ptTol)
		dst = c.auditCentre(dst, id, ptTol)
	}
	return c.auditColumns(dst, ptTol)
}

func (b *box) exempt(k AuditCheck) bool { return b.auditOff&k.Mask() != 0 }

// urect is b's rectangle before snapping.
func (b *box) urect() [4]float32 {
	return [4]float32{b.origin[0], b.origin[1], b.origin[0] + b.size[0], b.origin[1] + b.size[1]}
}

func (c *Context) report(dst []Finding, k AuditCheck, id, other ID, delta float32) []Finding {
	f := Finding{Check: k, Key: c.boxes[id].key, Rect: c.boxes[id].urect(), Delta: delta, Where: c.describe(id)}
	if other != 0 {
		f.Other, f.OtherRect, f.OtherWhere = c.boxes[other].key, c.boxes[other].urect(), c.describe(other)
	}
	return append(dst, f)
}

// auditOverflow is check (b) for box id in its parent p.
func (c *Context) auditOverflow(dst []Finding, id, p ID, tol float32) []Finding {
	b, pb := &c.boxes[id], &c.boxes[p]
	if pb.flags&Clip != 0 || b.size[0] <= 0 || b.size[1] <= 0 || b.exempt(AuditOverflow) || pb.exempt(AuditOverflow) || b.outline() {
		return dst
	}
	var worst float32
	for a := 0; a < 2; a++ {
		if pb.scrollOn(a) {
			continue
		}
		lo := pb.origin[a] - b.origin[a]
		hi := b.origin[a] + b.size[a] - (pb.origin[a] + pb.size[a])
		worst = max(worst, lo, hi)
	}
	if worst > tol {
		dst = c.report(dst, AuditOverflow, id, p, worst)
	}
	return dst
}

// auditOverlap is check (c) for the flow children of linear box p.
func (c *Context) auditOverlap(dst []Finding, p ID, tol float32) []Finding {
	pb := &c.boxes[p]
	la := pb.layoutAxis()
	var prev ID
	for ch := pb.first; ch != 0; ch = c.boxes[ch].next {
		cb := &c.boxes[ch]
		if cb.floatingOn(la) || cb.hid || cb.size[0] <= 0 || cb.size[1] <= 0 {
			continue
		}
		if prev != 0 {
			q := &c.boxes[prev]
			d := q.origin[la] + q.size[la] - cb.origin[la]
			o := 1 - la
			cross := min(q.origin[o]+q.size[o], cb.origin[o]+cb.size[o]) - max(q.origin[o], cb.origin[o])
			if d > tol && cross > tol && !cb.exempt(AuditOverlap) && !q.exempt(AuditOverlap) {
				dst = c.report(dst, AuditOverlap, ch, prev, d)
			}
		}
		prev = ch
	}
	return dst
}

// outline reports whether b is a floating outline without children, a
// decoration drawn round its control (a focus ring, a bezel) rather than
// content: the overflow check leaves it out.
func (b *box) outline() bool {
	return b.flags&floatingMask != 0 && b.first == 0 &&
		b.flags&(DrawBorder|DrawBackground|DrawText|DrawImage) == DrawBorder
}

// opaque reports whether b hides what is drawn beneath its rectangle.
func (b *box) opaque() bool {
	if b.flags&DrawBackground == 0 || b.opacity < 1 {
		return false
	}
	for _, bg := range b.bg {
		if bg&255 != 255 {
			return false
		}
	}
	return true
}

// draws reports whether b draws anything of its own.
func (b *box) draws() bool {
	f := b.flags
	switch {
	case f&DrawText != 0 && b.txt.Len > 0, f&DrawImage != 0 && b.img.Tex != 0:
		return true
	case f&DrawBorder != 0 && b.borderW > 0 && b.borderColor&255 != 0:
		return true
	case f&DrawBackground != 0:
		return b.bg[0]&255|b.bg[1]&255|b.bg[2]&255|b.bg[3]&255 != 0
	}
	return false
}

// floatingDraw reports whether b is drawn after its parent's flow children
// (emit).
func (b *box) floatingDraw() bool { return b.flags&floatingMask != 0 }

// auditCovers is check (d) for the floating children of p that hide what is
// beneath them: an opaque one hides the siblings drawn before it (every flow
// child, and the floating ones built before it); a material region shows the
// system's material behind the window's pixels, so content of any sibling
// that reaches into it without being inside it runs under (or onto) its edge.
func (c *Context) auditCovers(dst []Finding, p ID, tol float32) []Finding {
	boxes := c.boxes
	all := [4]float32{-1e9, -1e9, 1e9, 1e9}
	for f := boxes[p].first; f != 0; f = boxes[f].next {
		fb := &boxes[f]
		if !fb.floatingDraw() || fb.hid || fb.exempt(AuditCovered) {
			continue
		}
		mat := c.material(fb)
		if !mat && !fb.opaque() {
			continue
		}
		cover := fb.urect()
		before := true
		for s := boxes[p].first; s != 0; s = boxes[s].next {
			if s == f {
				before = false
				continue
			}
			if before || mat || !boxes[s].floatingDraw() {
				var found bool
				if dst, found = c.coveredIn(dst, s, f, cover, all, false, mat, tol); found {
					break // one finding per floating box
				}
			}
		}
	}
	return dst
}

// material reports whether b is a material region, which the system draws
// behind the window's pixels. ui has no materials yet; the branch that adds
// them (Context.Material) answers here.
func (c *Context) material(b *box) bool { return false }

// coveredIn reports the first box of id's subtree, clipped by clip, that
// draws under cover f, and whether there was one. Content is text and images,
// and backgrounds and borders that scroll (scrolled: below a scrolling box):
// shapes that do not move and are covered on purpose, such as layered
// backgrounds or the petals of a spinner, are not reported. Under a material
// (mat), content that lies inside it is its own and is not reported either.
func (c *Context) coveredIn(dst []Finding, id, f ID, cover, clip [4]float32, scrolled, mat bool, tol float32) ([]Finding, bool) {
	b := &c.boxes[id]
	if b.hid || b.exempt(AuditCovered) {
		return dst, false
	}
	vis := intersect(b.urect(), clip)
	area := vis // what it draws: the glyph or image box of text and images
	content := scrolled && b.draws()
	if _, ink, ok := c.textInk(b); ok {
		area, content = intersect(ink, clip), true
	} else if ink, ok := c.iconInk(b); ok {
		area, content = intersect(ink, clip), true
	}
	hit := intersect(area, cover)
	if content && hit[2]-hit[0] > tol && hit[3]-hit[1] > tol && (!mat || hit != area) {
		return c.report(dst, AuditCovered, id, f, min(hit[2]-hit[0], hit[3]-hit[1])), true
	}
	if b.flags&Clip != 0 {
		clip = vis
		if empty(intersect(clip, cover)) {
			return dst, false
		}
	}
	scrolled = scrolled || b.flags&scrollMask != 0
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		var found bool
		if dst, found = c.coveredIn(dst, ch, f, cover, clip, scrolled, mat, tol); found {
			return dst, true
		}
	}
	return dst, false
}

// auditCentre is check (f) for box p.
func (c *Context) auditCentre(dst []Finding, p ID, tol float32) []Finding {
	pb := &c.boxes[p]
	if pb.exempt(AuditCentre) {
		return dst
	}
	if pb.justify == JustifyCenter && pb.axis != AxisFlow {
		la := pb.layoutAxis()
		lo, hi := float32(1e9), float32(-1e9)
		for ch := pb.first; ch != 0; ch = c.boxes[ch].next {
			cb := &c.boxes[ch]
			if cb.floatingOn(la) || cb.hid {
				continue
			}
			lo, hi = min(lo, cb.origin[la]), max(hi, cb.origin[la]+cb.size[la])
		}
		inner0, inner1 := pb.origin[la]+pb.pad[la], pb.origin[la]+pb.size[la]-pb.pad[la+2]
		if lo <= hi && hi-lo < inner1-inner0 {
			if d := abs32((lo+hi)/2 - (inner0+inner1)/2); d > tol {
				return c.report(dst, AuditCentre, pb.first, p, d)
			}
		}
	}
	// a capsule or a button with one icon in it
	if pb.flags&DrawText != 0 && pb.txt.Len > 0 {
		return dst
	}
	var only ID
	for ch := pb.first; ch != 0; ch = c.boxes[ch].next {
		cb := &c.boxes[ch]
		if cb.flags&floatingMask != 0 || cb.hid {
			continue
		}
		if only != 0 {
			return dst
		}
		only = ch
	}
	if only == 0 || c.boxes[only].first != 0 || c.boxes[only].exempt(AuditCentre) {
		return dst
	}
	r := pb.rect
	w, h := r[2]-r[0], r[3]-r[1]
	if w <= 0 || h <= 0 {
		return dst
	}
	capsule := true
	for _, rad := range pb.radii {
		capsule = capsule && rad >= min(w, h)/2-1
	}
	if s := c.semOf(p); !capsule && (s == nil || s.role != RoleButton) {
		return dst
	}
	ink, ok := c.iconInk(&c.boxes[only])
	if !ok {
		return dst
	}
	dx := abs32((ink[0]+ink[2])/2 - (r[0]+r[2])/2)
	dy := abs32((ink[1]+ink[3])/2 - (r[1]+r[3])/2)
	if d := max(dx, dy); d > tol {
		dst = c.report(dst, AuditCentre, only, p, d)
	}
	return dst
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// iconInk is the image or glyph box of b when b shows an icon: an image, or
// display text of one glyph.
func (c *Context) iconInk(b *box) ([4]float32, bool) {
	if b.flags&DrawImage != 0 && b.img.Tex != 0 {
		r := b.rect
		if b.img.Fit != FitNatural {
			return [4]float32{r[0] + b.pad[0], r[1] + b.pad[1], r[2] - b.pad[2], r[3] - b.pad[3]}, true
		}
		uv := b.img.UV
		w, h := uv[2]-uv[0], uv[3]-uv[1]
		cx := (r[0] + b.pad[0] + r[2] - b.pad[2]) / 2
		cy := (r[1] + b.pad[1] + r[3] - b.pad[3]) / 2
		x0, y0 := snap(cx-w/2), snap(cy-h/2)
		return [4]float32{x0, y0, x0 + w, y0 + h}, true
	}
	if n, ink, ok := c.textInk(b); ok && n == 1 {
		return ink, true
	}
	return [4]float32{}, false
}

// textInk is the glyph box of b's one line of display text where emitText
// draws it, without the spaces at either end: from the pen position of the
// first glyph that is not a space to the advance of the last, by the font's
// ascent and descent. n is the number of glyphs inside it. ok is false for a
// box without such text (none, wrapped or rich).
func (c *Context) textInk(b *box) (n int, ink [4]float32, ok bool) {
	if b.flags&DrawText == 0 || b.flags&WrapText != 0 || b.txt.Len == 0 || c.recOf(b) != nil {
		return 0, ink, false
	}
	if c.sh.RunGeneration() != b.runGen {
		b.run = c.sh.Shape(b.font, c.fr.Cur().Bytes(b.txt))
		b.runGen = c.sh.RunGeneration()
	}
	v := c.sh.Run(b.run)
	if len(v.Glyphs) == 0 {
		return 0, ink, false
	}
	i0, i1 := 0, len(v.Glyphs)-1
	if len(v.Clusters) == len(v.Glyphs) {
		space := func(i int) bool {
			off := int(v.Clusters[i] &^ text.ClusterRTL)
			return off < len(v.Text) && (v.Text[off] == ' ' || v.Text[off] == '\t')
		}
		for i0 <= i1 && space(i0) {
			i0++
		}
		for i1 >= i0 && space(i1) {
			i1--
		}
		if i0 > i1 {
			return 0, ink, false
		}
	}
	end := v.Advance
	if i1+1 < len(v.X) {
		end = v.X[i1+1]
	}
	fi := c.fontInfo(b.font)
	r := b.rect
	x0, x1 := r[0]+b.pad[0], r[2]-b.pad[2]
	y0, y1 := r[1]+b.pad[1], r[3]-b.pad[3]
	x := x0
	if avail := x1 - x0; v.Advance <= avail+0.01 {
		switch b.align {
		case AlignCenter:
			x = x0 + (avail-v.Advance)/2
		case AlignRight:
			x = x1 - v.Advance
		}
	}
	baseline := snap(y0 + ((y1-y0)-(fi.ascent+fi.descent))/2 + fi.ascent)
	return i1 - i0 + 1, [4]float32{x + v.X[i0], baseline - fi.ascent, x + end, baseline + fi.descent}, true
}

// maxAuditCols bounds what the column check remembers: the headers of this
// many tables of up to MaxAuditColumns columns each.
const (
	maxAuditTables  = 8
	maxAuditColumns = 32
)

type auditHeader struct {
	id   ID
	ink  [4]float32 // the union of the header's text and images
	ok   bool       // it has some
	done bool       // a cell off the header's edge was reported
	// the first cell of the column with text or images, its alignment edge
	// and whether a cell off that edge was reported
	first      ID
	firstEdge  float32
	firstAlign Align
	odd        bool
}

// auditColumns is check (e).
func (c *Context) auditColumns(dst []Finding, tol float32) []Finding {
	var tables [maxAuditTables]ID
	var cols [maxAuditTables][maxAuditColumns]auditHeader
	var ncols [maxAuditTables]int
	nt := 0
	find := func(t ID) int {
		for i := 0; i < nt; i++ {
			if tables[i] == t {
				return i
			}
		}
		return -1
	}
	for i := range c.sems {
		s := &c.sems[i]
		if s.role != RoleColumnHeader || c.boxes[s.id].hid {
			continue
		}
		t := c.ancestorRole(s.id, RoleTable, 0)
		ti := find(t)
		if ti < 0 && nt < maxAuditTables {
			tables[nt], ti = t, nt
			nt++
		}
		if ti < 0 || ncols[ti] == maxAuditColumns {
			continue
		}
		h := auditHeader{id: s.id}
		h.ink, _, h.ok = c.inkOf(s.id)
		cols[ti][ncols[ti]] = h
		ncols[ti]++
	}
	if nt == 0 {
		return dst
	}
	var row ID
	idx := 0
	for i := range c.sems {
		s := &c.sems[i]
		if s.role != RoleCell || c.ancestorRole(s.id, RoleCell, RoleRow) != 0 {
			continue // a cell inside a cell is part of it
		}
		r := c.ancestorRole(s.id, RoleRow, RoleTable)
		if r != row {
			row, idx = r, 0
		}
		col := idx
		idx++
		if r == 0 || c.boxes[s.id].hid || c.boxes[s.id].exempt(AuditColumn) {
			continue
		}
		ti := find(c.ancestorRole(r, RoleTable, 0))
		if ti < 0 || col >= ncols[ti] {
			continue
		}
		h := &cols[ti][col]
		if c.boxes[h.id].exempt(AuditColumn) {
			continue
		}
		ci, al, ok := c.inkOf(s.id)
		if !ok {
			continue
		}
		edge := func(r [4]float32) float32 {
			switch al {
			case AlignRight:
				return r[2]
			case AlignCenter:
				return (r[0] + r[2]) / 2
			}
			return r[0]
		}
		// against the header
		if d := abs32(edge(ci) - edge(h.ink)); h.ok && !h.done && d > tol {
			h.done = true
			dst = c.report(dst, AuditColumn, s.id, h.id, d)
		}
		// against the column's first cell: one row's content that pushes
		// its text along (an extra child in the cell's flow)
		switch {
		case h.first == 0:
			h.first, h.firstEdge, h.firstAlign = s.id, edge(ci), al
		case !h.odd && al == h.firstAlign:
			if d := abs32(edge(ci) - h.firstEdge); d > tol {
				h.odd = true
				dst = c.report(dst, AuditColumn, s.id, h.first, d)
			}
		}
	}
	return dst
}

// inkOf is the union of the glyph and image boxes of id's subtree (its text
// and images) and the alignment of its widest text (the name beside an icon
// of the symbol font, which is centred in its box); ok is false when it has
// neither text nor images.
func (c *Context) inkOf(id ID) (ink [4]float32, al Align, ok bool) {
	ink, ok = c.inkUnion(id)
	if t := c.widestText(id); t != 0 {
		al = c.boxes[t].align
	}
	return ink, al, ok
}

// widestText is the box of id's subtree with the widest line of text, 0 if
// there is none.
func (c *Context) widestText(id ID) ID {
	b := &c.boxes[id]
	if b.hid {
		return 0
	}
	var best ID
	if b.flags&DrawText != 0 && b.txt.Len > 0 && b.flags&WrapText == 0 {
		best = id
	}
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		if t := c.widestText(ch); t != 0 && (best == 0 || c.boxes[t].textW > c.boxes[best].textW) {
			best = t
		}
	}
	return best
}

func (c *Context) inkUnion(id ID) (ink [4]float32, ok bool) {
	b := &c.boxes[id]
	if b.hid {
		return ink, false
	}
	if _, r, has := c.textInk(b); has {
		ink, ok = r, true
	} else if b.flags&DrawImage != 0 && b.img.Tex != 0 {
		ink, ok = c.iconInk(b)
	}
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		r, has := c.inkUnion(ch)
		switch {
		case !has:
		case ok:
			ink = [4]float32{min(ink[0], r[0]), min(ink[1], r[1]), max(ink[2], r[2]), max(ink[3], r[3])}
		default:
			ink, ok = r, true
		}
	}
	return ink, ok
}

// ancestorRole is the nearest ancestor of id (not id itself) with role r,
// or 0 when there is none or an ancestor with role stop comes first.
func (c *Context) ancestorRole(id ID, r, stop Role) ID {
	for a := c.boxes[id].parent; a != 0; a = c.boxes[a].parent {
		if s := c.semOf(a); s != nil {
			if s.role == r {
				return a
			}
			if stop != RoleNone && s.role == stop {
				return 0
			}
		}
	}
	return 0
}

var roleNames = [...]string{
	RoleNone: "", RoleWindow: "Window", RoleGroup: "Group", RoleLabel: "Label", RoleImage: "Image",
	RoleButton: "Button", RoleCheckbox: "Checkbox", RoleSwitch: "Switch", RoleRadio: "Radio",
	RoleRadioGroup: "RadioGroup", RoleSlider: "Slider", RoleTextField: "TextField",
	RoleSearchField: "SearchField", RoleComboBox: "ComboBox", RoleList: "List", RoleListItem: "ListItem",
	RoleTable: "Table", RoleRow: "Row", RoleCell: "Cell", RoleColumnHeader: "ColumnHeader",
	RoleScrollArea: "ScrollArea", RoleScrollBar: "ScrollBar", RoleSplitter: "Splitter",
	RoleTabList: "TabList", RoleTab: "Tab", RoleTabPanel: "TabPanel", RoleMenuBar: "MenuBar",
	RoleMenu: "Menu", RoleMenuItem: "MenuItem", RoleMenuItemCheckbox: "MenuItemCheckbox",
	RoleDialog: "Dialog", RoleTooltip: "Tooltip", RoleProgressBar: "ProgressBar", RoleLink: "Link",
}

// describe names box id for a person: its own label, then the labels of up
// to three named ancestors ("Date" in ColumnHeader "Date" in Table "Files"),
// and its key.
func (c *Context) describe(id ID) string {
	b := make([]byte, 0, 96)
	b = c.appendLabel(b, id, true)
	n := 0
	for a := c.boxes[id].parent; a != 0 && n < 3; a = c.boxes[a].parent {
		l := len(b)
		b = append(b, " in "...)
		if b2 := c.appendLabel(b, a, false); len(b2) > len(b) {
			b = b2
			n++
		} else {
			b = b[:l]
		}
	}
	b = append(b, " [key "...)
	b = strconv.AppendUint(b, uint64(c.boxes[id].key), 16)
	b = append(b, ']')
	return string(b)
}

// appendLabel appends box id's role, semantic name and display text; for an
// anonymous box, "box" when always is set.
func (c *Context) appendLabel(dst []byte, id ID, always bool) []byte {
	l := len(dst)
	bx := &c.boxes[id]
	if s := c.semOf(id); s != nil && int(s.role) < len(roleNames) {
		dst = append(dst, roleNames[s.role]...)
		if s.name.Len > 0 {
			dst = strconv.AppendQuote(append(dst, ' '), clip32(string(c.semA.Bytes(s.name))))
		}
	}
	if bx.flags&DrawText != 0 && bx.txt.Len > 0 && bx.lastFrame == c.frame {
		if len(dst) > l {
			dst = append(dst, ' ')
		}
		dst = strconv.AppendQuote(dst, clip32(string(c.fr.Cur().Bytes(bx.txt))))
	}
	if len(dst) == l && always {
		switch {
		case bx.flags&DrawImage != 0:
			dst = append(dst, "image"...)
		case bx.ovRoot:
			dst = append(dst, "overlay"...)
		default:
			dst = append(dst, "box"...)
		}
		dst = appendKind(dst, bx.flags)
	}
	return dst
}

// appendKind appends what an anonymous box does, "{floating border}".
func appendKind(dst []byte, f Flags) []byte {
	l := len(dst)
	for _, k := range [...]struct {
		f Flags
		s string
	}{{floatingMask, "floating"}, {DrawBackground, "background"}, {DrawBorder, "border"}, {Clip, "clip"}, {scrollMask, "scroll"}, {Clickable, "clickable"}} {
		if f&k.f != 0 {
			if len(dst) == l {
				dst = append(dst, " {"...)
			} else {
				dst = append(dst, ' ')
			}
			dst = append(dst, k.s...)
		}
	}
	if len(dst) > l {
		dst = append(dst, '}')
	}
	return dst
}

func clip32(s string) string {
	if len(s) > 32 {
		return s[:32] + "…"
	}
	return s
}
