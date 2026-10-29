// Command winprobe is the program the Windows packaging test builds with
// gnui package -target windows/<arch> and runs: it prints, as JSON, what its
// manifest made of the process (the DPI awareness, the code page) and what
// FindResourceW finds in its own image.
package main

import (
	"encoding/json"
	"os"
	"syscall"
)

func main() {
	user32 := syscall.NewLazyDLL("user32.dll")
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	ctx, _, _ := user32.NewProc("GetThreadDpiAwarenessContext").Call()
	aw, _, _ := user32.NewProc("GetAwarenessFromDpiAwarenessContext").Call(ctx)
	pmv2, _, _ := user32.NewProc("AreDpiAwarenessContextsEqual").Call(ctx, ^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
	acp, _, _ := kernel32.NewProc("GetACP").Call()
	find := kernel32.NewProc("FindResourceW")
	res := map[string]bool{}
	for name, typ := range map[string]uintptr{"manifest": 24, "version": 16, "groupIcon": 14} {
		h, _, _ := find.Call(0, 1, typ) // the image's own module, resource ID 1
		res[name] = h != 0
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"awareness": aw, "perMonitorV2": pmv2 != 0, "codePage": acp, "resources": res,
	})
}
