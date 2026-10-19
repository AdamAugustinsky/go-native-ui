//go:build darwin && gnui_publicapi

package platform

import "gonativeui/internal/objc"

// The public-only counterparts of material_private_darwin.go, for a build
// with the gnui_publicapi tag (Mac App Store apps): the sidebar and the
// inspector are regular NSGlassEffectView glass, and the window's corner
// radius is not known (the backdrop glass keeps square corners, which the
// window's mask rounds).

const privateAPI = false

func setSplitGlass(objc.ID, MaterialArea) {}

func windowCornerRadius(objc.ID) (float64, bool) { return 0, false }
