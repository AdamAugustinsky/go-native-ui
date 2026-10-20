//go:build darwin && gnui_publicapi

package platform

import "gonativeui/internal/objc"

// Without the private API the glass variants are not read.

func glassVariant(objc.ID) (int64, bool) { return 0, false }

func splitVariant(int) (int64, bool) { return 0, false }
