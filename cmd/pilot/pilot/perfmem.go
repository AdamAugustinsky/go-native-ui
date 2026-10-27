//go:build perfmem

package pilot

import "gonativeui/fs"

// PerfMemory reports the image cache's own accounting, not additional RSS.
func (p *Pilot) PerfMemory() any {
	m := map[string]any{}
	if p.sys != nil {
		m["Images"] = p.sys.cache.Stats()
	}
	if s, ok := p.src.(*fs.Service); ok {
		m["FreePool"] = s.PerfMemory()
	}
	var owned, rows uint64
	for _, p := range p.panes {
		listings := []*fs.Listing{p.v.l}
		if p.rc.active {
			listings = append(listings, p.rc.l)
		}
		for _, l := range listings {
			if l != nil {
				owned += uint64(cap(l.Entries)*24 + cap(l.Names) + cap(l.Order)*4)
			}
		}
		rows += uint64(4 * (cap(p.v.rows) + cap(p.rc.rows) + cap(p.rc.m)))
	}
	m["OwnedListingBytes"], m["RowScratchBytes"] = owned, rows
	return m
}
