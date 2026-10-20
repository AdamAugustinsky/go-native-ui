//go:build darwin

package live

import (
	"image"
	"testing"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/widget/widgettest"
)

// checkboxScene draws AppKit's own checkbox (on) at x points from the left of
// a 160×60 point view, and returns the pixels and the view's scale.
func checkboxScene(x float64) (*image.RGBA, float64) {
	initFrame := objc.Sel("initWithFrame:")
	scene := objc.Send(objc.Cls("NSView"), objc.Sel("alloc"))
	scene = objc.ID(objc.F.Reset().Arg(uintptr(scene)).Arg(uintptr(initFrame)).ArgRect(0, 0, 160, 60).Call(objc.MsgSend).R0())
	b := objc.Send(objc.Cls("NSButton"), objc.Sel("alloc"))
	b = objc.ID(objc.F.Reset().Arg(uintptr(b)).Arg(uintptr(initFrame)).ArgRect(x, 20, 22, 22).Call(objc.MsgSend).R0())
	objc.Send1(b, objc.Sel("setButtonType:"), 3) // NSButtonTypeSwitch
	objc.Send1(b, objc.Sel("setTitle:"), uintptr(objc.Str("")))
	objc.Send1(b, objc.Sel("setState:"), 1)
	objc.Send1(scene, objc.Sel("addSubview:"), uintptr(b))
	img, err := widgettest.ViewImage(scene)
	if err != nil {
		panic(err)
	}
	return img, float64(img.Bounds().Dx()) / 160
}

// The default tolerance holds AppKit to AppKit: two drawings of the same
// control match, and the same control one pixel to the side does not, in the
// whole view and in the crop around the control.
func TestAppKitControlNoiseAndShift(t *testing.T) {
	l, _ := startProbe(t)
	l.Frames(2)
	l.Do(func(a *app.App) {
		first, scale := checkboxScene(40)
		second, _ := checkboxScene(40)
		shifted, _ := checkboxScene(40 + 1/scale)
		t.Logf("scale %v, view %v", scale, first.Bounds())
		if r := widgettest.CompareImages(first, second, widgettest.Tolerance{}); !r.OK {
			t.Errorf("two drawings of the same control differ: %s", r.Why)
		}
		if r := widgettest.CompareImages(first, shifted, widgettest.Tolerance{}); r.OK {
			t.Errorf("the control moved by one pixel and the default tolerance did not see it (%d of %d pixels differ, largest ΔE %.1f)", r.Different, r.Pixels, r.Worst)
		}
		rect := image.Rect(int(30*scale), int(10*scale), int(80*scale), int(50*scale))
		crop := func(img *image.RGBA) *image.RGBA {
			c, err := widgettest.Crop(img, rect)
			if err != nil {
				t.Errorf("%v", err)
				return image.NewRGBA(image.Rect(0, 0, 1, 1))
			}
			return c
		}
		if r := widgettest.CompareImages(crop(first), crop(shifted), widgettest.Tolerance{}); r.OK {
			t.Errorf("cropped: the control moved by one pixel and the default tolerance did not see it (%d of %d pixels differ)", r.Different, r.Pixels)
		}
		if r := widgettest.CompareImages(crop(first), crop(second), widgettest.Tolerance{}); !r.OK {
			t.Errorf("cropped: two drawings of the same control differ: %s", r.Why)
		}
	})
}
