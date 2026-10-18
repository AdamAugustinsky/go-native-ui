package main

import "gonativeui/ui"

// benchScript is the synthetic input of -bench. Every frame the pointer moves
// to another row (hover) and the wheel scrolls scrollRows rows toward the end
// with precise deltas, as a trackpad flick, so each frame builds rows it has
// never built. On top of that:
//
//	frame 200        jump to row 900000
//	frame 290        click: selects the hovered row
//	frames 300-359   the wheel stops and Down moves the selection (and scrolls it into view)
//	frame 420        jump back to row 4000
//	frame 500        another click
//
// Frame counting starts at the first frame after the startup frame; the
// measured windows start at warm and are period frames long. The events above
// repeat in every window (at the same place within it), so each measured
// window holds the same work and the bench gate may pass on a clean one (D27).
// The script ends at total.
func (l *listApp) benchScript(warm, period, total int) func(frame int, in *ui.Input) bool {
	return func(f int, in *ui.Input) bool {
		if f >= total {
			return false
		}
		if f >= warm {
			l.counting = true
		}
		ev := f // the frame within the script's events
		if f >= warm+period {
			ev = warm + (f-warm)%period
		}
		in.Inside, in.Focused = true, true
		// The pointer sweeps down the list and wraps; x stays over the name column.
		span := max(int(l.listH)-int(l.rowPx), 1)
		in.MouseX = 240
		in.MouseY = l.listY0 + float32(f*37%span) + l.rowPx/2

		keys := ev >= 300 && ev < 360
		if !keys {
			in.AddScroll(0, -float32(l.scrollRows)*l.rowPx, true) // toward the end
		}
		switch ev {
		case 200:
			l.jump = 900000
		case 420:
			l.jump = 4000
		case 290, 500:
			in.SetButton(ui.ButtonLeft, true, 1)
		case 291, 501:
			in.SetButton(ui.ButtonLeft, false, 0)
		}
		if keys && ev%5 == 0 {
			in.PushKey(ui.KeyEvent{Code: ui.KeyDown, Down: true})
		}
		return true
	}
}
