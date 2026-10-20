//go:build gnui_release

package app

import "gonativeui/platform"

// devBuild is false in a build with the gnui_release tag: no Debug menu, no
// chords, no inspector panel, and no caller program counters. (GNUI_DEBUG=overlay
// still turns the overlay on: its HUD, outlines and causes.)
const devBuild = false

func (a *App) debugIngress(*platform.Event) bool                 { return false }
func (a *App) debugMenu(*platform.MenuSpec)                      {}
func (a *App) debugMenuState(uint16) (enabled, checked, ok bool) { return false, false, false }
func buildInspector(*InspectSnapshot, *dbgLines, *symCache)      {}
