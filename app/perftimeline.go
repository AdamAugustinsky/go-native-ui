//go:build perftimeline

package app

import (
	"encoding/json"
	"fmt"
	"gonativeui/platform"
	"os"
)

var firstFrameTimes [6]int64

func perfFrame(a *App, i int) {
	if a.frames == 0 {
		firstFrameTimes[i] = platform.Now()
	}
}
func perfTimeline(a *App) {
	tr := &a.Surf.Trace
	b, _ := json.Marshal(map[string]any{"Open": platform.OpenTimeline(), "Frame": firstFrameTimes, "Present": map[string]int64{"Start": tr.Start, "Acquired": tr.Acquired, "Committed": tr.Committed, "Done": tr.Done, "OnGlass": tr.OnGlass.Load()}})
	fmt.Fprintln(os.Stderr, "PERF_DETAIL", string(b))
}
