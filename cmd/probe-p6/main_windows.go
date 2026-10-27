package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

type report struct {
	Probe         string            `json:"probe"`
	Arch          string            `json:"arch"`
	Windows       [3]uint32         `json:"windows"` // major, minor, build (RtlGetVersion)
	AUMID         string            `json:"aumid"`
	Steps         map[string]string `json:"steps"` // the process-wide steps' HRESULTs
	Variants      []*variantResult  `json:"variants"`
	ShortcutHR    string            `json:"shortcutHR,omitempty"`
	ShortcutSteps map[string]string `json:"shortcutSteps,omitempty"`
	COMObjects    com.Counts        `json:"comObjects"` // our delegates: Live is 0 once the toasts let go
	TimedOut      bool              `json:"timedOut,omitempty"`
	ElapsedMs     float64           `json:"elapsedMs"`
}

var (
	mu  sync.Mutex
	out = &report{Probe: "P6", Arch: runtime.GOARCH, Steps: map[string]string{}}
)

func emit(code int, timedOut bool) {
	mu.Lock()
	out.TimedOut = timedOut
	out.COMObjects = com.Leaks()
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	os.Exit(code)
}

func main() {
	id := flag.String("id", "GonativeUI.ProbeP6", "the AUMID to post as")
	shortcut := flag.Bool("shortcut", true, "also post with a Start-menu shortcut carrying the AUMID (the second variant)")
	keep := flag.Bool("keep", false, "keep the registry key, the shortcut and the toasts")
	wait := flag.Duration("wait", 4*time.Second, "how long to wait for events and the history after each Show")
	timeout := flag.Duration("timeout", 60*time.Second, "give up and print what was found")
	flag.Parse()
	runtime.LockOSThread() // the STA is this thread's

	start := time.Now()
	time.AfterFunc(*timeout, func() { emit(1, true) })
	out.Windows, out.AUMID = osVersion(), *id
	if !win32.WinRTAvailable() {
		out.Steps["combase"] = "missing"
		emit(0, false)
	}
	step := func(k string, hr int32) {
		mu.Lock()
		out.Steps[k] = hrText(hr)
		mu.Unlock()
	}
	step("RoInitialize(STA)", win32.RoInitialize(win32.RoInitSingleThreaded))
	idw := utf16(*id)
	step("SetCurrentProcessExplicitAppUserModelID", win32.HR(call(pSetAppID, uintptr(unsafe.Pointer(&idw[0])))))
	step("register AUMID", registerAUMID(*id, "probe-p6"))

	r := postToast(0, "registry", *id, *wait)
	mu.Lock()
	out.Variants = append(out.Variants, r)
	mu.Unlock()

	if *shortcut {
		steps := map[string]string{}
		hr := makeShortcut(*id, steps)
		mu.Lock()
		out.ShortcutHR, out.ShortcutSteps = hrText(hr), steps
		mu.Unlock()
		if hr >= 0 {
			r := postToast(1, "registry+shortcut", *id, *wait)
			mu.Lock()
			out.Variants = append(out.Variants, r)
			mu.Unlock()
		}
	}
	if !*keep {
		step("clear history", clearHistory(*id))
		step("unregister AUMID", unregisterAUMID(*id))
		if *shortcut {
			if err := os.Remove(shortcutPath()); err != nil && !os.IsNotExist(err) {
				mu.Lock()
				out.Steps["remove shortcut"] = err.Error()
				mu.Unlock()
			}
		}
	}
	mu.Lock()
	out.ElapsedMs = float64(time.Since(start).Microseconds()) / 1000
	mu.Unlock()
	emit(0, false)
}
