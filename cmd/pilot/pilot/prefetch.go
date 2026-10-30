package pilot

import (
	"gonativeui/fs"
	"gonativeui/text"
	"gonativeui/theme"
)

const (
	nameMargin  = 32
	nameVisible = 64
	nameChunk   = 16
	nameSlots   = 2*(nameVisible+2*nameMargin) + 2
)

type nameRange struct {
	listing     *fs.Listing
	lease, ver  uint32
	request     uint64
	font        text.FontKey
	desc        text.FontDesc
	first, last int
	retry       bool
}

// prepareNames runs before any boxes are built. Imports can evict run handles,
// so they must precede the frame's text measurements. Actual table bounds
// refine the next request in drawList; the margin covers ordinary scrolling.
func (pl *Pilot) prepareNames() {
	if pl.shapes == nil {
		return
	}
	f := pl.k.Theme().Styles[theme.StyleBody].Font
	if pl.opt.Mono {
		f = pl.k.Theme().Font.Mono
	}
	for i, p := range pl.panes {
		first := p.topRow
		if p.scrollRow >= 0 {
			first = p.scrollRow
		}
		pl.queueNames(i, f, first, first+max(pl.geo[i].visRows, 32))
	}
	pl.shapes.Poll(pl.a.Text, nameSlots)
}

// queueNames copies only the bounded neighborhood, never the listing. A
// relist, filter, font or range change cancels the previous generation of work.
// Requests are visible-first, then ahead, then behind. Chunking avoids
// cancelling almost-identical requests on every row of a steady scroll.
func (pl *Pilot) queueNames(i int, font text.FontKey, first, last int) {
	if pl.shapes == nil {
		return
	}
	p, old := pl.panes[i], &pl.nameRanges[i]
	d, ok := pl.a.Text.FontDescription(font)
	if !ok {
		return
	}
	n := p.v.nRows()
	first = min(max(first, 0), n)
	last = min(max(last, first), min(n, first+nameVisible))
	base := first / nameChunk * nameChunk
	lo, hi := max(base-nameMargin, 0), min(base+nameVisible+nameMargin, n)
	next := nameRange{listing: p.v.l, lease: p.v.lease, ver: p.fver, request: p.floor,
		font: font, desc: d, first: lo, last: hi}
	if *old == next {
		return
	}
	if old.listing != next.listing || old.lease != next.lease || old.ver != next.ver || old.request != next.request ||
		old.font != next.font || old.desc != next.desc || old.first != lo || old.last != hi {
		pl.shapes.Cancel(p.id)
	}
	*old = next
	for _, span := range [3][2]int{{first, last}, {last, hi}, {lo, first}} {
		for row := span[0]; row < span[1]; row++ {
			name := p.v.l.Name(int(p.v.entryAt(row)))
			if len(name) <= text.PrefetchTextLimit && !pl.shapes.Submit(p.id, pl.a.Text, font, name) {
				old.retry = true
			}
		}
	}
}

// initialNames starts work as soon as an initial listing lands, while Init
// finishes setting up the window or waits for the other pane. There is no
// synchronous shaping wait: a frame that overtakes a worker uses Shape.
func (pl *Pilot) initialNames(i int) {
	if pl.shapes == nil {
		return
	}
	p := pl.panes[i]
	if p.v.l == nil {
		return
	}
	d := pl.nameDesc
	d.Scale = pl.a.EffectiveScale()
	f := pl.a.Text.Font(d)
	pl.shapes.Cancel(p.id)
	count := 0
	for _, e := range p.v.l.Order[:min(len(p.v.l.Order), 512)] {
		if p.hideHidden && p.v.l.Entries[e].Flags&fs.FlagHidden != 0 {
			continue
		}
		pl.shapes.Submit(p.id, pl.a.Text, f, p.v.l.Name(int(e)))
		count++
		if count == nameVisible+nameMargin {
			break
		}
	}
	actual, _ := pl.a.Text.FontDescription(f)
	pl.nameRanges[i] = nameRange{listing: p.v.l, lease: p.v.lease, ver: p.fver, request: p.floor,
		font: f, desc: actual, last: count}
}
