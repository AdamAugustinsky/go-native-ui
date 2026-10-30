package pilot

import (
	crand "crypto/rand"
	"encoding/binary"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/ui"
)

// pilotNarrowColumns is the finding of a pane narrower than its columns'
// least widths (#44): the cells and their headers run past the row
// and the header strip, under the pane's clip, at Pilot's own minimum window
// size.
func pilotNarrowColumns(f ui.Finding) bool {
	return f.Check == ui.AuditOverflow && (strings.HasPrefix(f.Where, "Cell ") || strings.HasPrefix(f.Where, "ColumnHeader ")) &&
		strings.Contains(f.OtherWhere, `in Table "files"`)
}

// TestPilotSimUser drives Pilot on its synthetic folders with the simulated
// user (app.SimUser, D-pending (ui-dst)): clicks, drags, keys, the filter
// field with text and compositions, scrolls, resizes and scale changes, with
// app.SimInvariants after every step and the pane model checked at the end.
// The environment selects the seeds as in app/simtests/ui (TestSimUser):
//
//	GNUI_SIM_SEED=0x1234 go test ./cmd/pilot/pilot -run TestPilotSimUser -count=1 -v
//	GNUI_SIM_SEEDS=50 GNUI_SIM_STEPS=2000 go test ./cmd/pilot/pilot -run TestPilotSimUser -count=1
//	GNUI_SIM_EXPLORE=10m go test ./cmd/pilot/pilot -run TestPilotSimUser -count=1 -v -timeout 0
func TestPilotSimUser(t *testing.T) {
	steps := 300
	if v := os.Getenv("GNUI_SIM_STEPS"); v != "" {
		var err error
		steps, err = strconv.Atoi(v)
		pilotSimMust(t, err)
	}
	pilotSimChecks = []app.SimCheck{app.SimAuditAllowing(pilotNarrowColumns), app.SimOwedFrames, app.SimInvariants}
	// The watchdog bounds native work in real time; a loaded runner (Windows
	// arm64 on WARP) takes over five seconds at a barrier. A hang still fails.
	pilotSimWorkerTimeout = time.Minute
	defer func() { pilotSimChecks, pilotSimWorkerTimeout = nil, 0 }()
	run := func(seed uint64) {
		t.Helper()
		start := time.Now()
		ok := t.Run(strconv.FormatUint(seed, 16), func(t *testing.T) {
			s, b := newPilotSim(t, seed, 5000)
			u := app.NewSimUser(seed)
			err := u.Run(s, 0, steps)
			if err == nil {
				err = u.Settle(s, 0)
			}
			if err != nil {
				t.Fatalf("pilot seed=%#x steps=%d: %v\nreplay: GNUI_SIM_SEED=%#x GNUI_SIM_STEPS=%d go test ./cmd/pilot/pilot -run '^TestPilotSimUser$' -count=1 -v", seed, steps, err, seed, steps)
			}
			for i, p := range b.panes {
				if p.loading() || p.relPending || p.jobBusy || p.rc.active {
					t.Fatalf("pilot seed=%#x: pane %d did not settle after the input", seed, i)
				}
			}
			if b.files.badRelease || b.files.IgnoredReleases() != 0 {
				t.Fatalf("pilot seed=%#x: leases: bad=%v ignored=%d", seed, b.files.badRelease, b.files.IgnoredReleases())
			}
			t.Logf("pilot seed=%#x steps=%d events=%d trace=%016x", seed, steps, u.Events, s.TraceHash())
		})
		if !ok {
			t.FailNow()
		}
		t.Logf("seed %#x in %s", seed, time.Since(start).Round(time.Millisecond))
	}
	if v := os.Getenv("GNUI_SIM_EXPLORE"); v != "" {
		d, err := time.ParseDuration(v)
		pilotSimMust(t, err)
		for end := time.Now().Add(d); time.Now().Before(end); {
			var b [8]byte
			_, _ = crand.Read(b[:])
			run(binary.LittleEndian.Uint64(b[:]))
		}
		return
	}
	if v := os.Getenv("GNUI_SIM_SEED"); v != "" {
		seed, err := strconv.ParseUint(v, 0, 64)
		pilotSimMust(t, err)
		run(seed)
		return
	}
	n := 1
	if v := os.Getenv("GNUI_SIM_SEEDS"); v != "" {
		var err error
		n, err = strconv.Atoi(v)
		pilotSimMust(t, err)
	}
	for seed := uint64(1); seed <= uint64(n); seed++ {
		run(seed)
	}
}
