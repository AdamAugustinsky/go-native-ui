//go:build darwin && !gnuidebug

package objc

func initMainCheck() {}

// checkMain checks the main-thread rule in gnuidebug builds only.
func checkMain() {}
