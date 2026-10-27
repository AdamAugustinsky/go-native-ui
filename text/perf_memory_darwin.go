//go:build darwin

package text

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"
)

func perfMemory(s *System) {
	if !PerfEnabled {
		return
	}
	f, err := os.Create(os.Getenv("GNUI_TEXT_PERF") + ".memory")
	if err != nil {
		return
	}
	defer f.Close()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	c := s.runs
	runBytes := cap(c.table)*4 + cap(c.ents)*int(unsafe.Sizeof(runEnt{})) + cap(c.text) + cap(c.glyph)*2 + cap(c.xs)*4 + cap(c.faces)*2 + cap(c.clus)*4
	fmt.Fprintf(f, "heap_alloc=%d heap_inuse=%d heap_sys=%d num_gc=%d fonts=%d faces=%d run_entries=%d run_reserved_bytes=%d glyph_slots=%d glyph_table_bytes=%d para_stats=%+v\n", ms.HeapAlloc, ms.HeapInuse, ms.HeapSys, ms.NumGC, len(s.fonts), len(s.faces), len(c.ents), runBytes, len(s.atlas.glyphs.slots), cap(s.atlas.glyphs.slots)*int(unsafe.Sizeof(glyphSlot{})), s.ParaStats())
	for i, p := range s.atlas.pages {
		fmt.Fprintf(f, "page=%d color=%v bytes=%d used_padded_pixels=%d shelves=%d occupied_height=%d\n", i, p.color, len(p.pix), p.usedPixels, len(p.pack.shelves), p.pack.nextY)
	}
}
