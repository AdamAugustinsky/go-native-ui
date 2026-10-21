package app

import (
	"fmt"
	"os"
	"strconv"

	"gonativeui/platform"
)

// StateLine appends the one-line state dump that is printed when frames stop
// coming: the app's pacer and counters, then the window's frame-delivery state
// (platform.DebugState; macOS only, "win{valid=0}" elsewhere). now is
// platform.Now(). Format (one line, key=value, ages are "N.Ns-ago"):
//
//	app{frames=410 warm=1000/1000 meas=410/900 needdraw=1 ready=0 paced=1 parked=1 hidden=1 requested=119.8s-ago retry=0 evframes=412 lastevframe=119.9s-ago lastdraw=119.9s-ago beginfail=3/consec=3 owed=B settle=0 pending=0 inhandler=0} win{valid=1 occ=0(hidden) lastvis=hidden@118.2s-ago key=0 active=0 mini=0 link=metal paused=1/1 armed=1@119.8s-ago drawable=0 live=0 relink=0 ticks=1234 sinceframe=0 lasttick=119.9s-ago lastframe=119.9s-ago pauses=2 idle=4}
//
// parked: hidden, a draw found no frame, and the loop waits for an event
// (pacer). owed lists why another frame is owed: A animating, H input with
// the pointer inside, S resize settling, I events waiting, B bench (- =
// nothing). It allocates nothing when b has room.
func (a *App) StateLine(b []byte, now int64) []byte {
	b = append(b, "app{frames="...)
	b = strconv.AppendInt(b, int64(a.frames), 10)
	if bn := a.bench; bn != nil {
		b = append(b, " warm="...)
		b = strconv.AppendInt(b, int64(min(bn.presented, bn.o.Warm)), 10)
		b = append(b, '/')
		b = strconv.AppendInt(b, int64(bn.o.Warm), 10)
		b = append(b, " meas="...)
		b = strconv.AppendInt(b, int64(bn.measuredN), 10)
		b = append(b, '/')
		b = strconv.AppendInt(b, int64(bn.o.Frames), 10)
	}
	b = append(b, " needdraw="...)
	b = appendBool(b, a.p.needDraw)
	b = append(b, " ready="...)
	b = appendBool(b, a.p.frameReady)
	b = append(b, " paced="...)
	b = appendBool(b, a.p.paced)
	b = append(b, " parked="...)
	b = appendBool(b, a.p.parked)
	b = append(b, " hidden="...)
	b = appendBool(b, a.p.hidden)
	b = append(b, " requested="...)
	b = appendAge(b, now, a.p.requestedAt)
	b = append(b, " retry="...)
	if a.p.retryAt > now {
		b = strconv.AppendFloat(b, float64(a.p.retryAt-now)/1e6, 'f', 0, 64)
		b = append(b, "ms"...)
	} else {
		b = append(b, '0')
	}
	b = append(b, " evframes="...)
	b = strconv.AppendInt(b, int64(a.frameEvents), 10)
	b = append(b, " lastevframe="...)
	b = appendAge(b, now, a.lastFrameEv)
	b = append(b, " lastdraw="...)
	b = appendAge(b, now, a.lastDrawT)
	b = append(b, " beginfail="...)
	b = strconv.AppendInt(b, int64(a.beginFails), 10)
	b = append(b, "/consec="...)
	b = strconv.AppendInt(b, int64(a.consecFails), 10)
	b = append(b, " owed="...)
	b = a.owedWhy.appendTo(b)
	b = append(b, " settle="...)
	b = appendBool(b, a.settle || a.resized)
	b = append(b, " textdrop="...)
	b = strconv.AppendUint(b, a.UI.AtlasOverflowFrames(), 10)
	if a.UI.TextDegraded() {
		b = append(b, "(degraded)"...)
	}
	b = append(b, " pending="...)
	b = appendBool(b, a.tr.pending())
	b = append(b, "} "...)
	if a.Win != nil {
		st := a.Win.DebugState()
		b = st.Append(b)
	} else {
		b = append(b, "win{none}"...)
	}
	return b
}

func (m owedMask) appendTo(b []byte) []byte {
	if m == 0 {
		return append(b, '-')
	}
	for i, c := range "AHSIB" {
		if m&(1<<uint(i)) != 0 {
			b = append(b, byte(c))
		}
	}
	return b
}

func appendBool(b []byte, v bool) []byte {
	if v {
		return append(b, '1')
	}
	return append(b, '0')
}

func appendAge(b []byte, now, t int64) []byte {
	if t == 0 {
		return append(b, "never"...)
	}
	b = strconv.AppendFloat(b, float64(now-t)/1e9, 'f', 1, 64)
	return append(b, "s-ago"...)
}

// dumpIfStalled prints the state line to stderr once a second while a repaint
// is owed and no frame was drawn for over a second. Called from the loop when
// verbose.
func (a *App) dumpIfStalled(now int64) {
	if !a.p.needDraw || now-a.lastDrawT < silenceNs || now-a.lastDumpT < silenceNs {
		return
	}
	a.lastDumpT = now
	a.diagBuf = append(a.diagBuf[:0], "app: no frame for "...)
	a.diagBuf = strconv.AppendFloat(a.diagBuf, float64(now-a.lastDrawT)/1e9, 'f', 1, 64)
	a.diagBuf = append(a.diagBuf, "s: "...)
	a.diagBuf = a.StateLine(a.diagBuf, now)
	a.diagBuf = append(a.diagBuf, '\n')
	os.Stderr.Write(a.diagBuf)
}

// benchTimeout prints the state line to stderr and returns the error of a
// bench that ran out of time. The error says whether the window was occluded,
// which is the usual reason a CAMetalDisplayLink stops ticking and a bench
// stops getting frames.
func (a *App) benchTimeout(now int64) error {
	a.diagBuf = append(a.diagBuf[:0], "app: bench stalled: "...)
	a.diagBuf = a.StateLine(a.diagBuf, now)
	a.diagBuf = append(a.diagBuf, '\n')
	os.Stderr.Write(a.diagBuf)
	bn := a.bench
	return fmt.Errorf("app: bench timed out after %v (+%.1f s of display stalls) with %d of %d measured frames (%d drawn; last %.1f s ago); %s",
		bn.o.Timeout, float64(bn.stalled)/1e9, bn.measuredN, bn.o.Frames, a.frames, float64(now-a.lastDrawT)/1e9, a.occlusionNote())
}

// occlusionNote says in words what the window state is, for the timeout error.
func (a *App) occlusionNote() string {
	if a.Win == nil {
		return "no window"
	}
	return occlusionNote(a.Win.DebugState())
}

func occlusionNote(st platform.DebugState) string {
	if !st.Valid {
		return "window state unavailable on this OS"
	}
	occ := strconv.FormatUint(uint64(st.Occlusion), 10)
	switch {
	case st.Mini:
		return "the window is MINIATURIZED: the display link does not tick, so no frame could be drawn"
	case !st.Visible:
		return "the window is OCCLUDED (occlusionState=" + occ + ", not visible): the display link does not tick while it is covered, hidden or on another Space, so no frame could be drawn; the result says nothing about frame delivery"
	}
	return "the window is VISIBLE (occlusionState=" + occ + ") yet frames stopped: see the state line (ticks since the last frame " +
		strconv.FormatUint(st.TicksSinceFrame, 10) + ", frame armed " + strconv.FormatBool(st.FrameArmed) + ")"
}
