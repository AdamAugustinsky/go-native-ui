package platform

import "strconv"

// DebugState is a snapshot of the window's frame-delivery state, for the
// diagnostics line an app prints when frames stop (app.BenchReport timeouts,
// -v). Taking it allocates nothing. Times are Now() nanoseconds, 0 = never.
// Only macOS fills it in (Valid); elsewhere it is the zero value.
type DebugState struct {
	// Valid is true when the state was filled in (macOS only).
	Valid bool
	// Now is the time of the snapshot, in Now() nanoseconds.
	Now int64

	Occlusion    uint32 // [NSWindow occlusionState]; bit 2 = visible
	Visible      bool   // Occlusion&2 != 0
	LastVisible  int8   // the last EvVisible: 1 visible, 0 hidden, -1 none yet
	LastVisibleT int64
	Key          bool // isKeyWindow
	AppActive    bool // [NSApp isActive]
	Mini         bool // miniaturized

	MetalLink    bool // the frame source is a CAMetalDisplayLink (else the view's CADisplayLink)
	LinkPaused   bool // what we believe
	LinkPausedOS bool // what the link says
	FrameArmed   bool // a RequestFrame waits for a tick
	ArmedT       int64
	Drawable     bool // a tick's drawable is held
	Live         bool // live-resize mode
	Relink       bool // the metal link is to be attached again when the live resize ends

	Ticks           uint64 // link ticks seen
	TicksSinceFrame uint64 // ... since the last EvFrame
	LastTickT       int64
	LastFrameT      int64 // the last EvFrame
	Pauses          uint64
	IdleTicks       int
	Restarts        uint64 // display links that stopped calling back and were replaced (macOS)
	Trims           uint64 // times an idle window gave back its spare drawables (macOS)
}

func appendAge(b []byte, now, t int64) []byte {
	if t == 0 {
		return append(b, "never"...)
	}
	b = strconv.AppendFloat(b, float64(now-t)/1e9, 'f', 1, 64)
	return append(b, "s-ago"...)
}

func appendBool(b []byte, v bool) []byte {
	if v {
		return append(b, '1')
	}
	return append(b, '0')
}

// Append appends the state as a line of key=value pairs:
//
//	win{valid=1 occ=2(visible) lastvis=visible@3.1s-ago key=1 active=1 mini=0 link=metal paused=0/0 armed=1@0.0s-ago drawable=0 live=0 relink=0 ticks=912 sinceframe=0 lasttick=0.0s-ago lastframe=0.0s-ago pauses=3 idle=0}
//
// (paused is ours/the link's; armed=1@age says how long a request has been
// waiting for a tick; ages are relative to Now.)
func (s *DebugState) Append(b []byte) []byte {
	if !s.Valid {
		return append(b, "win{valid=0}"...)
	}
	b = append(b, "win{valid=1 occ="...)
	b = strconv.AppendUint(b, uint64(s.Occlusion), 10)
	if s.Visible {
		b = append(b, "(visible)"...)
	} else {
		b = append(b, "(hidden)"...)
	}
	b = append(b, " lastvis="...)
	switch s.LastVisible {
	case 1:
		b = append(b, "visible@"...)
		b = appendAge(b, s.Now, s.LastVisibleT)
	case 0:
		b = append(b, "hidden@"...)
		b = appendAge(b, s.Now, s.LastVisibleT)
	default:
		b = append(b, "none"...)
	}
	b = append(b, " key="...)
	b = appendBool(b, s.Key)
	b = append(b, " active="...)
	b = appendBool(b, s.AppActive)
	b = append(b, " mini="...)
	b = appendBool(b, s.Mini)
	b = append(b, " link="...)
	if s.MetalLink {
		b = append(b, "metal"...)
	} else {
		b = append(b, "view"...)
	}
	b = append(b, " paused="...)
	b = appendBool(b, s.LinkPaused)
	b = append(b, '/')
	b = appendBool(b, s.LinkPausedOS)
	b = append(b, " armed="...)
	b = appendBool(b, s.FrameArmed)
	if s.FrameArmed {
		b = append(b, '@')
		b = appendAge(b, s.Now, s.ArmedT)
	}
	b = append(b, " drawable="...)
	b = appendBool(b, s.Drawable)
	b = append(b, " live="...)
	b = appendBool(b, s.Live)
	b = append(b, " relink="...)
	b = appendBool(b, s.Relink)
	b = append(b, " ticks="...)
	b = strconv.AppendUint(b, s.Ticks, 10)
	b = append(b, " sinceframe="...)
	b = strconv.AppendUint(b, s.TicksSinceFrame, 10)
	b = append(b, " lasttick="...)
	b = appendAge(b, s.Now, s.LastTickT)
	b = append(b, " lastframe="...)
	b = appendAge(b, s.Now, s.LastFrameT)
	b = append(b, " pauses="...)
	b = strconv.AppendUint(b, s.Pauses, 10)
	b = append(b, " idle="...)
	b = strconv.AppendInt(b, int64(s.IdleTicks), 10)
	if s.Restarts > 0 {
		b = append(b, " restarts="...)
		b = strconv.AppendUint(b, s.Restarts, 10)
	}
	if s.Trims > 0 {
		b = append(b, " trims="...)
		b = strconv.AppendUint(b, s.Trims, 10)
	}
	return append(b, '}')
}
