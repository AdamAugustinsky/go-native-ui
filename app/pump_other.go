//go:build !darwin && !windows

package app

import "gonativeui/platform"

// pump is the loop's wait. One window per app here (Linux: D30 and D68 are
// built for macOS and Windows): its Wait is the pump.
func (a *App) pump(timeoutNs int64) {
	if len(a.wins) > 0 {
		w := a.wins[0]
		w.waited = w.Win.Wait(timeoutNs)
	}
}

// windowEvents is the window's events from the last pump.
func (a *App) windowEvents() []platform.Event {
	e := a.waited
	a.waited = nil
	return e
}
