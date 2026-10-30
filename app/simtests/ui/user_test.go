package uisim_test

import (
	crand "crypto/rand"
	"encoding/binary"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/cmd/gallery/gallery"
	"gonativeui/cmd/rexlook/rex"
	"gonativeui/examples/notes/notes"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The simulated user (app.SimUser, D-pending (ui-dst)): one seed drives a
// stream of clicks, drags, keys, text, compositions, scrolls, resizes and
// scale changes into real widget trees on the virtual 60 Hz clock, and
// app.SimInvariants (with the layout audit and the frame-cause check) runs
// after every step. A failure prints its seed and the command that replays
// it. By default each target runs seed 1 for userSteps steps and the scene
// replays it to the same trace hash.
//
//	GNUI_SIM_SEED=0x1234 go test ./app/simtests/ui -run 'TestSimUser/notes' -count=1 -v    # replay one seed
//	GNUI_SIM_SEEDS=50 GNUI_SIM_STEPS=2000 go test ./app/simtests/ui -run TestSimUser -count=1  # seeds 1..50
//	GNUI_SIM_EXPLORE=10m go test ./app/simtests/ui -run TestSimUser -count=1 -v -timeout 0   # random seeds
//
// tools/simexplore.sh runs the exploration for this suite and Pilot's.

const userSteps = 300

// userWorkerTimeout is the real-time watchdog on the app's workers at each
// barrier: a loaded CI runner (Windows arm64 on WARP) can take more than the
// default five seconds without anything being stuck. A hang still fails.
const userWorkerTimeout = time.Minute

type userTarget struct {
	name   string
	w, h   int
	build  func() (app.Builder, app.Config, error)
	checks []app.SimCheck // nil: the simulator's defaults
}

var userTargets = []userTarget{
	{"scene", 900, 700, func() (app.Builder, app.Config, error) {
		// A chat-sized minimum: below about 360 pt the Markdown column is too
		// narrow for a code block's Copy button (#44).
		return &scene{ids: []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}}, app.Config{MinWidth: 360, MinHeight: 240}, nil
	}, nil},
	{"notes", 1100, 720, func() (app.Builder, app.Config, error) {
		b, err := notes.NewBench() // its drawn confirmation dialog: the simulator has no system alert
		if err != nil {
			return nil, app.Config{}, err
		}
		return b.Notes, notes.Config(), nil
	}, nil},
	{"gallery", 1100, 720, func() (app.Builder, app.Config, error) {
		b, err := gallery.New(gallery.Options{Still: true})
		return b, gallery.Config(), err
	}, []app.SimCheck{app.SimAuditAllowing(galleryAllowed), app.SimOwedFrames, app.SimInvariants}},
	{"rexlook", 1057, 639, func() (app.Builder, app.Config, error) {
		return rex.NewApp(), rex.Config(), nil
	}, []app.SimCheck{app.SimAuditAllowing(rexlookAllowed), app.SimOwedFrames, app.SimInvariants}},
}

// rexlookAllowed is rex's documented audit exception (rexlookAllow in
// cmd/rexlook/rex): its icon buttons' icons are a pixel or three larger than
// their hit boxes, as the reference draws them.
func rexlookAllowed(f ui.Finding) bool {
	return f.Check == ui.AuditOverflow && strings.Contains(f.OtherWhere, "{floating background clickable}")
}

// galleryAllowed is an outline's disclosure chevron at 1×: rexui.OutlineLook
// nudges it DisclosureDX (0.5 pt, measured on AppKit) right of its 13 pt hit
// box's middle, and at 1× the chevron is as wide as the box, so it reaches a
// pixel past it; the ink is inside.
//
// Off macOS it also takes the gallery's fontWide samples (catalogueFontWide in
// cmd/gallery/gallery): their frames are AppKit's, measured on macOS, and the
// system's font is wider than San Francisco.
func galleryAllowed(f ui.Finding) bool {
	if f.Check != ui.AuditOverflow {
		return false
	}
	if f.Delta <= 1 && strings.HasPrefix(f.Where, "image {floating} in ListItem ") && strings.HasPrefix(f.OtherWhere, "box {floating clickable} in ListItem ") {
		return true
	}
	if runtime.GOOS == "darwin" {
		return false
	}
	for _, w := range [...]string{`Link "Learn more"`, `Label "Large Title`, `Group "Name:"`, `Group "Email address:"`} {
		if strings.HasPrefix(f.Where, w) {
			return true
		}
	}
	return false
}

// userSeeds reads the seeds and steps from the environment; explore is the
// time to spend on random seeds instead (0: none).
func userSeeds(t *testing.T) (seeds []uint64, steps int, explore time.Duration) {
	t.Helper()
	steps = userSteps
	if v := os.Getenv("GNUI_SIM_STEPS"); v != "" {
		var err error
		steps, err = strconv.Atoi(v)
		must(t, err)
	}
	if v := os.Getenv("GNUI_SIM_EXPLORE"); v != "" {
		var err error
		explore, err = time.ParseDuration(v)
		must(t, err)
		return nil, steps, explore
	}
	seeds = []uint64{1}
	if v := os.Getenv("GNUI_SIM_SEEDS"); v != "" {
		n, err := strconv.Atoi(v)
		must(t, err)
		seeds = seeds[:0]
		for i := 1; i <= n; i++ {
			seeds = append(seeds, uint64(i))
		}
	}
	if v := os.Getenv("GNUI_SIM_SEED"); v != "" {
		seed, err := strconv.ParseUint(v, 0, 64)
		must(t, err)
		seeds = []uint64{seed}
	}
	return seeds, steps, 0
}

// randomSeed is an exploration seed: from the system, so parallel runs differ.
func randomSeed() uint64 {
	var b [8]byte
	_, _ = crand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:])
}

// runUser drives one target with one seed. It returns the trace hash, or
// the failure with the replay command.
func runUser(tg userTarget, seed uint64, steps int) (hash uint64, err error) {
	b, cfg, err := tg.build()
	if err != nil {
		return 0, err
	}
	s, err := app.NewSim(cfg, tg.w, tg.h, 1, b, app.SimOptions{Seed: seed, Checks: tg.checks, WorkerTimeout: userWorkerTimeout})
	if err != nil {
		return 0, err
	}
	defer s.Close()
	if err = s.Advance(time.Second); err != nil {
		return 0, err
	}
	u := app.NewSimUser(seed)
	if err = u.Run(s, 0, steps); err != nil {
		return 0, err
	}
	if err = u.Settle(s, 0); err != nil {
		return 0, err
	}
	// D27's gate on what the user left: a hover path over the settled
	// window, once to warm what it shows, then measured.
	w, h, _ := s.Window(0).Size()
	path := func() error {
		for i := 0; i < 12; i++ {
			x, y := float32(w)*float32(i%4*2+1)/8, float32(h)*float32(i/4*2+1)/6
			if err := s.Event(0, platform.Event{Kind: platform.EvPointerMove, X: x, Y: y}); err != nil {
				return err
			}
			if err := s.Advance(100 * time.Millisecond); err != nil {
				return err
			}
		}
		return s.Advance(15 * time.Second)
	}
	if err = path(); err != nil {
		return 0, err
	}
	s.CheckAllocations(true)
	err = path()
	s.CheckAllocations(false)
	if err != nil {
		return 0, err
	}
	return s.TraceHash(), nil
}

func userFail(t *testing.T, tg userTarget, seed uint64, steps int, err error) {
	t.Helper()
	t.Fatalf("%s seed=%#x steps=%d: %v\nreplay: GNUI_SIM_SEED=%#x GNUI_SIM_STEPS=%d go test ./app/simtests/ui -run 'TestSimUser/%s$' -count=1 -v",
		tg.name, seed, steps, err, seed, steps, tg.name)
}

func TestSimUser(t *testing.T) {
	seeds, steps, explore := userSeeds(t)
	for _, tg := range userTargets {
		t.Run(tg.name, func(t *testing.T) {
			if explore > 0 {
				end, runs := time.Now().Add(explore/time.Duration(len(userTargets))), 0
				for ; time.Now().Before(end); runs++ {
					seed := randomSeed()
					t.Logf("%s seed=%#x", tg.name, seed)
					if _, err := runUser(tg, seed, steps); err != nil {
						userFail(t, tg, seed, steps, err)
					}
				}
				t.Logf("%s: %d random seeds of %d steps passed", tg.name, runs, steps)
				return
			}
			for _, seed := range seeds {
				start := time.Now()
				hash, err := runUser(tg, seed, steps)
				if err != nil {
					userFail(t, tg, seed, steps, err)
				}
				if tg.name == "scene" { // the replay: the same seed is the same run
					again, err := runUser(tg, seed, steps)
					if err != nil {
						userFail(t, tg, seed, steps, err)
					}
					if again != hash {
						t.Fatalf("%s seed=%#x replay diverged: %016x != %016x", tg.name, seed, hash, again)
					}
				}
				t.Logf("%s seed=%#x steps=%d trace=%016x in %s", tg.name, seed, steps, hash, time.Since(start).Round(time.Millisecond))
			}
		})
	}
}
