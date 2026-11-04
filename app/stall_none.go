//go:build !darwin && !windows

package app

// No stall watch on this OS (StallWatch): the methods the loop calls do nothing.
type stallWatch struct{}

func newStallWatch(StallWatch, string) *stallWatch { return nil }
func (s *stallWatch) mark(uint32, int64) uint32    { return phaseIdle }
func (s *stallWatch) close()                       {}
func (a *App) checkFrames(int64)                   {}
func (a *App) drewFrame(int64)                     {}
