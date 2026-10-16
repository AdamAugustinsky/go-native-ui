// Throwaway phase-0 probe: GC cost of 1M file entries stored with Go strings
// (pointerful) vs offsets into one []byte (pointer-free).
//
//	go run ./probes/gcscan -mode strings
//	go run ./probes/gcscan -mode offsets
package main

import (
	"flag"
	"fmt"
	"runtime"
	"runtime/metrics"
	"strconv"
	"time"
)

type EntryS struct {
	Name  string
	Size  int64
	ModNs int64
	Kind  uint8
}

type EntryO struct {
	NameOff uint32
	NameLen uint16
	Kind    uint8
	_       uint8
	Size    int64
	ModNs   int64
}

var keepS []EntryS
var keepO []EntryO
var keepNames []byte

func main() {
	mode := flag.String("mode", "offsets", "strings|offsets")
	n := flag.Int("n", 1_000_000, "entries")
	flag.Parse()

	var ms0 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	switch *mode {
	case "strings":
		keepS = make([]EntryS, 0, *n)
		for i := 0; i < *n; i++ {
			keepS = append(keepS, EntryS{Name: "file_" + strconv.Itoa(i) + "_typical_name.txt", Size: int64(i)})
		}
	case "offsets":
		keepO = make([]EntryO, 0, *n)
		keepNames = make([]byte, 0, *n*32)
		for i := 0; i < *n; i++ {
			off := len(keepNames)
			keepNames = append(keepNames, "file_"...)
			keepNames = strconv.AppendInt(keepNames, int64(i), 10)
			keepNames = append(keepNames, "_typical_name.txt"...)
			keepO = append(keepO, EntryO{NameOff: uint32(off), NameLen: uint16(len(keepNames) - off), Size: int64(i)})
		}
	}
	runtime.GC()
	var ms1 runtime.MemStats
	runtime.ReadMemStats(&ms1)

	samples := []metrics.Sample{{Name: "/cpu/classes/gc/total:cpu-seconds"}, {Name: "/gc/scan/heap:bytes"}}
	metrics.Read(samples)
	gc0 := samples[0].Value.Float64()

	const cycles = 20
	var worstPause time.Duration
	t0 := time.Now()
	for i := 0; i < cycles; i++ {
		runtime.GC()
	}
	wall := time.Since(t0) / cycles
	var ms2 runtime.MemStats
	runtime.ReadMemStats(&ms2)
	for i := 0; i < cycles; i++ {
		p := time.Duration(ms2.PauseNs[(ms2.NumGC+255-uint32(i))%256])
		if p > worstPause {
			worstPause = p
		}
	}
	metrics.Read(samples)
	gcCPU := (samples[0].Value.Float64() - gc0) / cycles
	fmt.Printf("mode=%-8s entries=%d heap_live=%.1fMB (%.1f B/entry) heap_objects=%d scan_heap=%.1fMB | per GC cycle: wall=%v gc_cpu=%.2fms worst_STW_pause=%v\n",
		*mode, *n, float64(ms1.HeapAlloc-ms0.HeapAlloc)/1e6, float64(ms1.HeapAlloc-ms0.HeapAlloc)/float64(*n),
		ms1.HeapObjects, float64(samples[1].Value.Uint64())/1e6, wall, gcCPU*1000, worstPause)
	runtime.KeepAlive(keepS)
	runtime.KeepAlive(keepO)
	runtime.KeepAlive(keepNames)
}
