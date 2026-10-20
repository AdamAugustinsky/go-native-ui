package app

import (
	"os"
	"runtime"
	"sort"
	"testing"
	"time"
)

type heapNode struct {
	next *heapNode
	pad  [6]uintptr
}

// TestMeasureReadMemStats prints the stop-the-world cost of the call the HUD
// makes twice a frame (debugState.mallocs), over heaps like an app's: GNUI_MEASURE=1 go test ./app -run MeasureReadMemStats -v
func TestMeasureReadMemStats(t *testing.T) {
	if os.Getenv("GNUI_MEASURE") == "" {
		t.Skip("a measurement, not a test: set GNUI_MEASURE=1")
	}
	measure := func(name string, churn bool) {
		stop := make(chan struct{})
		done := make(chan struct{})
		if churn { // background allocation, as workers and decoders do: GC cycles run during the samples
			go func() {
				defer close(done)
				var keep [][]byte
				for {
					select {
					case <-stop:
						return
					default:
					}
					keep = append(keep[:0], make([]byte, 1<<20))
					time.Sleep(200 * time.Microsecond)
				}
			}()
		} else {
			close(done)
		}
		d := &debugState{}
		for i := 0; i < 50; i++ {
			d.mallocs()
		}
		const n = 4000
		ns := make([]int64, n)
		for i := range ns {
			t0 := time.Now()
			d.mallocs()
			ns[i] = int64(time.Since(t0))
			time.Sleep(100 * time.Microsecond)
		}
		close(stop)
		<-done
		sort.Slice(ns, func(i, j int) bool { return ns[i] < ns[j] })
		t.Logf("%-46s p50 %6.1f µs  p99 %6.1f µs  max %7.1f µs", name, float64(ns[n/2])/1e3, float64(ns[n*99/100])/1e3, float64(ns[n-1])/1e3)
	}
	measure("empty heap", false)
	measure("empty heap, GC churning", true)
	// 200 MB live: pointer-free buffers (an image library's pixels)
	pix := make([][]byte, 200)
	for i := range pix {
		pix[i] = make([]byte, 1<<20)
	}
	measure("200 MB pointer-free live", false)
	measure("200 MB pointer-free live, GC churning", true)
	// 200 MB live of pointers: 3.5 M linked nodes of 64 bytes
	var head *heapNode
	for i := 0; i < 3_300_000; i++ {
		head = &heapNode{next: head}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("live heap now %.0f MB, %d goroutines", float64(ms.HeapAlloc)/1e6, runtime.NumGoroutine())
	measure("200 MB pointer-free + 200 MB pointers", false)
	measure("200 MB pointer-free + 200 MB pointers, GC churning", true)
	runtime.KeepAlive(head)
	runtime.KeepAlive(pix)
}
