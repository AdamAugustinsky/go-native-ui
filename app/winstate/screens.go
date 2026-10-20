package winstate

import "gonativeui/platform"

// ScreensFromPlatform converts the displays platform.Screens reports (call that
// on the main thread) to the form Apply takes. It keeps their order, primary
// first.
func ScreensFromPlatform(in []platform.ScreenInfo) []Screen {
	if len(in) == 0 {
		return nil
	}
	out := make([]Screen, len(in))
	for i, s := range in {
		out[i] = Screen{
			ID:      s.ID,
			Frame:   Rect{s.X, s.Y, s.W, s.H},
			Visible: Rect{s.VisibleX, s.VisibleY, s.VisibleW, s.VisibleH},
			Scale:   s.Scale,
		}
	}
	return out
}
