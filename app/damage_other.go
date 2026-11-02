//go:build !darwin

package app

// damage holds nothing here: partial frames (D92) are drawn on macOS only.
type damage struct{}

// encodeFrame encodes the frame's draw list into a.fr, whole. It always
// returns true: the frame is presented.
func (a *App) encodeFrame() bool {
	a.R.Encode(a.fr.CmdBuf, a.fr.Target, a.fr.W, a.fr.H, a.cfg.Clear, &a.list)
	return true
}

func (a *App) checkDamage()   {}
func (a *App) releaseDamage() {}
