//go:build darwin && gnui_publicapi

package platform

import "gonativeui/internal/objc"

// The gnui_publicapi build: no private AppKit selector is named or called
// (DECISIONS.md D53). The window buttons stay at AppKit's default place
// (lights_darwin.go), which SystemButtons reports.

// privateAPIBuild is false in a gnui_publicapi build.
const privateAPIBuild = false

func (w *Window) titlebarMetricsUsable() bool { return false }

func (w *Window) placeNative([3]objc.ID) {}
