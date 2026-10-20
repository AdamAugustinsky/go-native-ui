//go:build darwin

package platform

// privateAPI reports whether this window may use private AppKit interfaces:
// not in a build with the gnui_publicapi tag, nor when the app opened it with
// Options.PublicAPIOnly. Every private-API use in the framework is behind it
// (DECISIONS.md D53).
func (w *Window) privateAPI() bool { return privateAPIBuild && !w.publicOnly }
