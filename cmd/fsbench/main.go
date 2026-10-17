// Command fsbench lists a directory repeatedly with package fs and prints
// one JSON object: the numbers behind phase 4's listing criteria, for CI
// on every OS.
//
//	fsbench [-n 9] [-q family] DIR
//	fsbench -mk 100000 DIR      # fill DIR with synthetic files first
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"gonativeui/fs"
)

type report struct {
	OS, Arch             string
	Dir                  string
	Entries              int
	Runs                 int
	ListMs               float64 `json:"list_ms"`
	ListSortMs           float64 `json:"list_sort_ms"`
	SortMs               float64 `json:"sort_ms"`
	FilterMs             float64 `json:"filter_ms"`
	FilterQuery          string  `json:"filter_query"`
	FilterMatches        int     `json:"filter_matches"`
	BytesPerEntry        float64 `json:"bytes_per_entry"`      // entries + names + order
	NameBytesPerEntry    float64 `json:"name_bytes_per_entry"` // names alone
	ListAllocsPerRun     float64 `json:"list_allocs_per_run"`  // warm
	ListAllocBytesPerRun float64 `json:"list_alloc_bytes_per_run"`
}

func median(d []time.Duration) float64 {
	slices.Sort(d)
	return float64(d[len(d)/2]) / float64(time.Millisecond)
}

func main() {
	n := flag.Int("n", 9, "runs per measurement (median reported)")
	q := flag.String("q", "a", "filter query")
	mk := flag.Int("mk", 0, "create this many empty files in DIR and exit")
	flag.Parse()
	dir := flag.Arg(0)
	if dir == "" {
		fmt.Fprintln(os.Stderr, "usage: fsbench [-n runs] [-q query] [-mk count] DIR")
		os.Exit(2)
	}
	if *mk > 0 {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fatal(err)
		}
		for i := 0; i < *mk; i++ {
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("file_%07d_some_typical_name.txt", (i*7919)%*mk)))
			if err != nil {
				fatal(err)
			}
			f.Close()
		}
		return
	}

	l := new(fs.Listing)
	var list, listSort, sortT, filt []time.Duration
	var idx []uint32
	for i := 0; i < *n+2; i++ { // two warm-up runs
		t0 := time.Now()
		if err := fs.ListDir(dir, l); err != nil {
			fatal(err)
		}
		t1 := time.Now()
		fs.Sort(l, fs.SortName, false)
		t2 := time.Now()
		idx = fs.Filter(l, []byte(*q), idx[:0])
		t3 := time.Now()
		if i < 2 {
			continue
		}
		list = append(list, t1.Sub(t0))
		listSort = append(listSort, t2.Sub(t0))
		sortT = append(sortT, t2.Sub(t1))
		filt = append(filt, t3.Sub(t2))
	}
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	const reps = 5
	for i := 0; i < reps; i++ {
		fs.ListDir(dir, l)
	}
	runtime.ReadMemStats(&m1)

	r := report{
		OS: runtime.GOOS, Arch: runtime.GOARCH, Dir: dir, Entries: len(l.Entries), Runs: *n,
		ListMs: median(list), ListSortMs: median(listSort), SortMs: median(sortT), FilterMs: median(filt),
		FilterQuery: *q, FilterMatches: len(idx),
		ListAllocsPerRun:     float64(m1.Mallocs-m0.Mallocs) / reps,
		ListAllocBytesPerRun: float64(m1.TotalAlloc-m0.TotalAlloc) / reps,
	}
	if len(l.Entries) > 0 {
		r.BytesPerEntry = float64(len(l.Entries)*24+len(l.Names)+len(l.Order)*4) / float64(len(l.Entries))
		r.NameBytesPerEntry = float64(len(l.Names)) / float64(len(l.Entries))
	}
	out, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(out))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
