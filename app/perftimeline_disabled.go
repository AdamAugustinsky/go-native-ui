//go:build !perftimeline

package app

func perfFrame(*App, int) {}
func perfTimeline(*App)   {}
