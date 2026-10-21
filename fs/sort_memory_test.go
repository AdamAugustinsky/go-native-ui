package fs

import (
	"os"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSortScratchIdleLifetime(t *testing.T) {
	l := synth("file10", "file2", "É.txt")
	Sort(l, SortName, false)
	runtime.GC() // unlike sync.Pool, warm scratch survives GC
	if n := testing.AllocsPerRun(10, func() {
		trimSorters() // a stale callback must not discard hot scratch
		Sort(l, SortName, false)
	}); n != 0 {
		t.Fatalf("hot sort allocated %v", n)
	}
	sorterMu.Lock()
	sorterLastPut = time.Now().Add(-sorterIdle)
	sorterMu.Unlock()
	trimSorters()
	sorterMu.Lock()
	retained := len(sorterFree)
	sorterMu.Unlock()
	if retained != 0 {
		t.Fatalf("idle pool retains %d sorters", retained)
	}
	Sort(l, SortName, false) // a cold call rebuilds scratch and still sorts correctly
	if got := string(l.Name(int(l.Order[0]))); got != "file2" {
		t.Fatal(got)
	}
}

func TestSortConcurrentWithIdleExpiry(t *testing.T) {
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			l := synth("file10", "file2", "É.txt")
			for range 100 {
				Sort(l, SortName, false)
				if got := string(l.Name(int(l.Order[0]))); got != "file2" {
					t.Errorf("concurrent sort: %s", got)
				}
			}
		})
	}
	for range 100 {
		sorterMu.Lock()
		sorterLastPut = time.Now().Add(-sorterIdle)
		sorterMu.Unlock()
		trimSorters()
	}
	wg.Wait()
}

// An opt-in fresh-process probe: measure the public Sort path without any
// earlier test's warmed pools, then allow the documented idle lifetime to end.
func TestSortIdleHeapProbe(t *testing.T) {
	if os.Getenv("GNUI_SORT_IDLE_PROBE") == "" {
		t.Skip("fresh-process memory probe")
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	func() {
		l := new(Listing)
		for i := 0; i < 1_000_000; i++ {
			off := len(l.Names)
			l.Names = append(l.Names, "file_"...)
			l.Names = strconv.AppendInt(l.Names, int64(i), 10)
			l.Names = append(l.Names, ".txt"...)
			l.Entries = append(l.Entries, Entry{NameOff: uint32(off), NameLen: uint16(len(l.Names) - off)})
		}
		Sort(l, SortName, false)
	}()
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("after sort and GC: %d additional live bytes", int64(after.HeapAlloc)-int64(before.HeapAlloc))
	time.Sleep(31 * time.Second)
	runtime.GC()
	runtime.ReadMemStats(&after)
	delta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("after idle and GC: %d additional live bytes", delta)
	if delta > 1<<20 {
		t.Errorf("idle sort scratch still retains %d bytes", delta)
	}
}
