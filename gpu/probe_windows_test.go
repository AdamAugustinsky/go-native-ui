//go:build windows

package gpu

import (
	"runtime"
	"testing"
)

// Temporary probe: what does the screen capture see in other arrangements?
func TestProbeReveal(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for _, c := range []struct {
		name          string
		hidden, again bool
	}{{"hidden+again", true, true}, {"visible control", false, false}} {
		r := revealRun(t, c.hidden, c.again)
		t.Logf("%s: cloakedAt=%v cloakedEnd=%v printed=%06x", c.name, r.cloakedAt, r.cloakedEnd, r.printed)
		for _, u := range r.runs {
			t.Logf("%s: screen %06x x%d (%+.1f ms from the present)", c.name, u.colour, u.n, float64(u.first-r.revealedAt)/1e6)
		}
	}
}
