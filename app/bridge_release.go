//go:build gnui_release

package app

// inspectStart does nothing in a build with the gnui_release tag: the agent
// bridge (bridge_dev.go) is not compiled in, and GNUI_INSPECT is not read.
func (a *App) inspectStart() func() { return noInspect }

func noInspect() {}
