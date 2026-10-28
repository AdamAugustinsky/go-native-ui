package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
)

type report struct {
	Probe     string     `json:"probe"`
	Arch      string     `json:"arch"`
	Windows   [3]uint32  `json:"windows"` // major, minor, build (RtlGetVersion)
	MF        *mfResult  `json:"mediaFoundation,omitempty"`
	WGC       *wgcResult `json:"wgc,omitempty"`
	TimedOut  bool       `json:"timedOut,omitempty"`
	ElapsedMs float64    `json:"elapsedMs"`
}

var (
	mu  sync.Mutex
	out = &report{Probe: "P4", Arch: runtime.GOARCH}
)

func emit(code int, timedOut bool) {
	mu.Lock()
	out.TimedOut = timedOut
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	os.Exit(code)
}

func main() {
	seconds := flag.Float64("seconds", 2, "how long to capture (WGC) and how long a file to write (MF, at least 3 s)")
	skipWGC := flag.Bool("skip-wgc", false, "skip the Windows.Graphics.Capture part")
	skipMF := flag.Bool("skip-mf", false, "skip the Media Foundation part")
	hw := flag.Bool("hw", true, "MF_READWRITE_ENABLE_HARDWARE_TRANSFORMS on the sink writer")
	keep := flag.Bool("keep", false, "keep the MP4 (its path is in the report)")
	timeout := flag.Duration("timeout", 60*time.Second, "give up and print what was found")
	flag.Parse()
	runtime.LockOSThread() // the window, the message pump and the apartment are this thread's

	start := time.Now()
	time.AfterFunc(*timeout, func() {
		emit(1, true)
	})
	out.Windows = osVersion()
	if !*skipMF {
		r := probeMF(max(*seconds, 3), *hw, *keep)
		mu.Lock()
		out.MF = r
		mu.Unlock()
	}
	if !*skipWGC {
		r := probeWGC(*seconds)
		mu.Lock()
		out.WGC = r
		mu.Unlock()
	}
	out.ElapsedMs = float64(time.Since(start).Microseconds()) / 1000
	emit(0, false)
}
