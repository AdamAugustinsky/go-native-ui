package theme

// The geometry of the window buttons the system draws over a custom titlebar
// on macOS (and that ButtonsTraffic draws for offscreen output), in one place,
// so that a live window and an offscreen render lay the titlebar out alike.

const (
	// TrafficDiameterPt is the diameter of a macOS window button in points.
	TrafficDiameterPt = 14
	// TrafficSpacingPt is the distance in points between the centres of two
	// neighbouring buttons (macOS 26 and later; earlier systems use 20).
	TrafficSpacingPt = 23
)

// TrafficCentrePt is the centre of the close button in points from the
// window's top left. An app with a custom titlebar gives it to the window as
// app.Config.TrafficLights, so that the system's buttons sit where
// ButtonsTraffic draws its own.
var TrafficCentrePt = [2]float32{23, 23}

// trafficGeometry is the centre and radius in pixels of button i at scale
// pixels per point, and the centre's y.
func trafficGeometry(scale float32, i int) (cx, cy, r float32) {
	kk := scale / 2 // the geometry was measured at two pixels per point
	return 2 * (TrafficCentrePt[0] + float32(i)*TrafficSpacingPt) * kk, 2 * TrafficCentrePt[1] * kk, 2 * (TrafficDiameterPt / 2) * kk
}

// TrafficDiscs is the rectangles (x0, y0, x1, y1) of the three window buttons
// of the reference geometry (TrafficCentrePt, TrafficSpacingPt,
// TrafficDiameterPt) at scale pixels per point, in pixels, snapped to whole
// pixels: close, minimise, zoom. ButtonsTraffic draws them.
func TrafficDiscs(scale float32) [3][4]float32 {
	var d [3][4]float32
	for i := range d {
		cx, cy, r := trafficGeometry(scale, i)
		d[i] = [4]float32{Snap(cx - r), Snap(cy - r), Snap(cx + r), Snap(cy + r)}
	}
	return d
}

// TrafficRect is the rectangle of the three buttons together, in pixels: what
// ui.Context.SystemButtons holds when the system draws buttons where
// ButtonsTraffic does.
func TrafficRect(scale float32) [4]float32 {
	cx0, cy, r := trafficGeometry(scale, 0)
	cx2, _, _ := trafficGeometry(scale, 2)
	return [4]float32{cx0 - r, cy - r, cx2 + r, cy + r}
}

// FreeBand is the part of a titlebar band of the given width that the window
// buttons in rect (x0, y0, x1, y1 in pixels, as ui.Context.SystemButtons) leave
// to the app, as the band's x0 and x1: rect plus gap of room, on whichever side
// of the band the buttons are. An empty rect leaves the whole band. Chrome
// implementations use it for ButtonsNone (the system's buttons) and for
// ButtonsTraffic (TrafficRect) alike.
func FreeBand(width, gap float32, rect [4]float32) [2]float32 {
	if rect[2] <= rect[0] || rect[3] <= rect[1] {
		return [2]float32{0, width}
	}
	if (rect[0]+rect[2])/2 < width/2 {
		return [2]float32{Snap(rect[2] + gap), width}
	}
	return [2]float32{0, Snap(rect[0] - gap)}
}
