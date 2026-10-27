//go:build perfmem

package fs

// PerfMemory counts only free slots, whose slice headers workers cannot mutate.
func (s *Service) PerfMemory() map[string]uint64 {
	m := map[string]uint64{}
	s.mu.Lock()
	for i, l := range s.all {
		if s.busy[i] {
			m["BusyListings"]++
			continue
		}
		m["FreeListings"]++
		m["FreeEntryBytes"] += uint64(cap(l.Entries) * 24)
		m["FreeNameBytes"] += uint64(cap(l.Names))
		m["FreeOrderBytes"] += uint64(cap(l.Order) * 4)
	}
	s.mu.Unlock()
	sorterMu.Lock()
	for _, s := range sorterFree {
		m["FreeSorters"]++
		m["FreeSorterBytes"] += uint64(cap(s.keys) + 4*cap(s.off))
	}
	sorterMu.Unlock()
	return m
}
