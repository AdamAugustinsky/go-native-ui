//go:build darwin

package app

import "gonativeui/platform"

// pump is the loop's wait: the app-wide pump, which hands every window its
// events (D30).
func (a *App) pump(timeoutNs int64) { platform.Pump(timeoutNs) }

// windowEvents is the window's events from the last pump.
func (a *App) windowEvents() []platform.Event { return a.Win.Events() }
