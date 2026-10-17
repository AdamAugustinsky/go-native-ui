package app

import (
	"strings"
	"testing"

	"gonativeui/platform"
)

func TestStateLineFormatAndNoAllocs(t *testing.T) {
	o, err := NewOffscreen(Config{}, 100, 100, 1, &tiny{})
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	a := o.App
	a.p, a.frames, a.owedWhy, a.beginFails, a.consecFails = newPacer(), 410, owedBench|owedSettle, 3, 1
	a.bench = &benchRun{o: BenchOptions{Warm: 1000, Frames: 900}, presented: 1410, measuredN: 410}
	now := int64(130e9)
	a.p.requestedAt, a.lastDrawT, a.lastFrameEv = now-int64(119.8e9), now-int64(119.9e9), 0
	buf := make([]byte, 0, 2048)
	line := string(a.StateLine(buf, now))
	for _, want := range []string{
		"app{frames=410 warm=1000/1000 meas=410/900 needdraw=1 ready=1 paced=1 requested=119.8s-ago",
		"lastevframe=never lastdraw=119.9s-ago beginfail=3/consec=1 owed=SB",
		"textdrop=0 pending=0} win{none}",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("state line lacks %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, "\n") {
		t.Error("the state line must be one line")
	}
	if n := testing.AllocsPerRun(100, func() { buf = a.StateLine(buf[:0], now) }); n != 0 {
		t.Errorf("%v allocs", n)
	}
}

func TestWindowDebugStateFormatAndNoAllocs(t *testing.T) {
	now := int64(130e9)
	st := platform.DebugState{
		Valid: true, Now: now, Occlusion: 0, LastVisible: 0, LastVisibleT: now - int64(118.2e9),
		MetalLink: true, LinkPaused: true, LinkPausedOS: true, FrameArmed: true, ArmedT: now - int64(119.8e9),
		Ticks: 1234, TicksSinceFrame: 7, LastTickT: now - int64(119.9e9), LastFrameT: now - int64(119.9e9), Pauses: 2, IdleTicks: 4,
	}
	buf := make([]byte, 0, 1024)
	line := string(st.Append(buf))
	want := "win{valid=1 occ=0(hidden) lastvis=hidden@118.2s-ago key=0 active=0 mini=0 link=metal paused=1/1 armed=1@119.8s-ago drawable=0 live=0 relink=0 ticks=1234 sinceframe=7 lasttick=119.9s-ago lastframe=119.9s-ago pauses=2 idle=4}"
	if line != want {
		t.Errorf("got\n%s\nwant\n%s", line, want)
	}
	if n := testing.AllocsPerRun(100, func() { buf = st.Append(buf[:0]) }); n != 0 {
		t.Errorf("%v allocs", n)
	}
}

// A bench that times out says whether the window was covered, which is why a
// CAMetalDisplayLink stops ticking, so the failure can be read.
func TestOcclusionNote(t *testing.T) {
	for _, c := range []struct {
		st   platform.DebugState
		want string
	}{
		{platform.DebugState{}, "unavailable"},
		{platform.DebugState{Valid: true, Occlusion: 0}, "OCCLUDED"},
		{platform.DebugState{Valid: true, Occlusion: 2, Visible: true}, "VISIBLE"},
		{platform.DebugState{Valid: true, Occlusion: 2, Visible: true, Mini: true}, "MINIATURIZED"},
	} {
		if got := occlusionNote(c.st); !strings.Contains(got, c.want) {
			t.Errorf("%+v: %q lacks %q", c.st, got, c.want)
		}
	}
}
