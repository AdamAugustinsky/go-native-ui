//go:build darwin

package platform

import (
	"fmt"
	"testing"
	"time"
)

// An app that zooms its whole window gives the window the zoomed band
// (SetTitlebar): the buttons, at the system's size, stay centred in it with
// their hover area, hit test and SystemButtons on them, through AppKit's
// layouts after it, and a return to 100 % puts them where they were. Where the
// buttons stay at AppKit's default place (PublicAPIOnly) they do not move and
// SystemButtons says where they are.
func TestSetTitlebarFollowsTheZoom(t *testing.T) {
	for _, public := range []bool{false, true} {
		name := "private metrics"
		if public {
			name = "PublicAPIOnly"
		}
		t.Run(name, func(t *testing.T) {
			const bar, zoom = 46, 1.5
			orig := [2]float32{21, bar / 2}
			var opened, native bool
			var fails []string
			var atZoom, back, band float64
			onMain(func() {
				w, err := Open(Options{Title: "titlebar zoom", Width: 500, Height: 300, Titlebar: bar, TrafficLights: orig, PublicAPIOnly: public})
				if err != nil {
					return
				}
				defer w.Close()
				opened = true
				native = lightsNativeHere(w) && !public
				drag := func(h float32) { w.SetDragRegions([]DragRegion{{Rect: [4]float32{0, 0, 500 * w.scale, h * w.scale}}}) }
				drag(bar)
				pumpFor(w, 100*time.Millisecond)

				s0 := seeLights(w)
				space := s0.centre[1][0] - s0.centre[0][0]
				want0 := s0.centre[0] // AppKit's default place, when the buttons are not placed
				if native {
					want0 = [2]float64{float64(orig[0]), float64(orig[1])}
				}
				if msg := checkLights(s0, want0, space); msg != "" {
					fails = append(fails, "at 100 %: "+msg)
				}

				h := float32(bar * zoom)
				lights := [2]float32{orig[0], orig[1] * zoom} // x stays (see app.App.syncTitlebar)
				drag(h)
				w.SetTitlebar(h, lights)
				pumpFor(w, 100*time.Millisecond)
				want := want0
				if native {
					want = [2]float64{float64(lights[0]), float64(lights[1])}
				}
				s := seeLights(w)
				atZoom, band = s.centre[0][1], float64(h)/2
				if msg := checkLights(s, want, space); msg != "" {
					fails = append(fails, "at 150 %: "+msg)
				}
				if native && !near(atZoom, band) {
					fails = append(fails, fmt.Sprintf("the buttons are centred at y %.2f, the zoomed band's centre is %.2f", atZoom, band))
				}
				if w.SystemButtons() != s.sys || s.sys == [4]float32{} {
					fails = append(fails, "SystemButtons changed under the check or is empty")
				}
				if s.union[3]-s.union[1] != s0.union[3]-s0.union[1] {
					fails = append(fails, "the buttons changed size with the zoom")
				}

				lightsEvents(w, func(step string) {
					if msg := checkLights(seeLights(w), want, space); msg != "" {
						fails = append(fails, "at 150 % after "+step+": "+msg)
					}
				})

				drag(bar)
				w.SetTitlebar(bar, orig)
				pumpFor(w, 100*time.Millisecond)
				s = seeLights(w)
				back = s.centre[0][1]
				if msg := checkLights(s, want0, space); msg != "" {
					fails = append(fails, "back at 100 %: "+msg)
				}
			})
			if !opened {
				t.Skip("cannot open a window here")
			}
			t.Logf("native=%v: close button centre y %.2f at 150 %% (band centre %.2f), %.2f at 100 %%", native, atZoom, band, back)
			for _, f := range fails {
				t.Error(f)
			}
		})
	}
}
