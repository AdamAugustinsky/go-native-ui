package widgettest

// SetClock makes the Player read its clock from f instead of platform.Now.
func SetClock(p *Player, f func() int64) { p.SetClock(f) }
