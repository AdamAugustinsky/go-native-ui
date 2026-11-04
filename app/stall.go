package app

import "time"

// StallWatch (AppConfig.Stall) writes a report when the app stops answering or
// stops drawing, so that a freeze a person saw can be read afterwards: a
// bundled app has no standard error, and a hang over by the time anyone looks
// leaves no hang report. Two things count as a stall:
//
//   - StallPhase: the main thread has spent After in one phase of a loop turn
//     (a Build, an encode, a present, the events' dispatch, a pump past its
//     timeout) while no frame was presented. A watchdog goroutine notices it,
//     so it is reported while the main thread is still stuck.
//   - StallFrames: a window that can be seen has owed a repaint for After and
//     drawn none, while the loop runs (a display link that stopped calling
//     back, a drawable that never comes). The loop notices it and adds its
//     state line (App.StateLine), which names the frame source's state.
//
// Each stall writes one file, stall-<time>.log, in Dir: the kind, the phase,
// how long, the state line when there is one, and every goroutine's stack. On
// macOS a sample of the process's native stacks (/usr/bin/sample, about 2 s)
// follows in stall-<time>-sample.txt, at most once a minute. When the window
// draws again the file gets the stall's whole length. Only the newest Keep
// reports are kept.
//
// macOS and Windows only (A3's systems); elsewhere AppConfig.Stall does
// nothing, the Linux gallery being at its size budget.
//
// The cost while nothing stalls: two atomic stores per phase of a loop turn,
// and a goroutine that wakes four times a second to read them.
type StallWatch struct {
	// After is how long counts as a stall. 0 means 2 s.
	After time.Duration
	// Dir is where the reports go. "" means paths.Logs of the app's ID
	// (~/Library/Logs/<id> on macOS).
	Dir string
	// Keep is how many reports are kept. 0 means 10.
	Keep int
	// Report, if set, is told of each report after it is written, on the
	// watchdog's goroutine (never the main thread).
	Report func(StallReport)
}

// StallKind says what stalled.
type StallKind uint8

const (
	// StallPhase: the main thread spent StallWatch.After in one phase.
	StallPhase StallKind = iota + 1
	// StallFrames: a visible window owed a repaint for After and drew none.
	StallFrames
)

// String is the kind in words, as the report writes it.
func (k StallKind) String() string {
	switch k {
	case StallPhase:
		return "main thread stuck in one phase"
	case StallFrames:
		return "no frame drawn while one is owed"
	}
	return "stall"
}

// StallReport is one stall, as written.
type StallReport struct {
	// Kind is what stalled.
	Kind StallKind
	// Phase is the loop's phase when it was noticed: pump, events, begin
	// (acquiring the drawable), build, encode, present, loop, app.
	Phase string
	// For is how long it had lasted when it was noticed.
	For time.Duration
	// State is the window's state line (StallFrames only: a stuck main thread
	// cannot be asked for it).
	State string
	// Path is the report's file ("" if it could not be written).
	Path string
}

// The loop's phases. Those under phaseUnwatched may legitimately last (a
// menu's or a dialog's own loop, no window open).
const (
	phaseIdle uint32 = iota
	phaseMenus
	phaseDialogs
	phaseUnwatched
	phasePump
	phaseEvents
	phaseBegin
	phaseBuild
	phaseEncode
	phasePresent
	phaseLoop
	phaseApp
)

var loopPhaseNames = [...]string{"idle", "menus", "dialogs", "", "pump", "events", "begin", "build", "encode", "present", "loop", "app"}

// phaseStalled reports whether the main thread, in phase p since since (and
// for a pump, its timeout ending at deadline), with the last frame presented
// at present, has stalled at now.
func phaseStalled(p uint32, since, deadline, present, now, after int64) bool {
	if p <= phaseUnwatched || now-present < after {
		return false
	}
	if p == phasePump {
		return deadline != 0 && now-deadline >= after
	}
	return now-since >= after
}

// framesStalled reports whether a window owing a repaint since owed (0: none
// owed, or it cannot be seen) has stalled at now.
func framesStalled(owed, now, after int64) bool { return owed != 0 && now-owed >= after }
