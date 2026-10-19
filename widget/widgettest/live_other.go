//go:build !darwin

package widgettest

import "gonativeui/app"

// readSystemButtons has nothing to ask off macOS: the buttons the OS draws
// there are in a title bar above the content, not over it.
func readSystemButtons(*app.App) [4]float32 { return [4]float32{} }
