//go:build darwin && !gnui_publicapi

package platform

import (
	"testing"

	"gonativeui/internal/objc"
)

// A titlebar-metrics method whose signature is not the one it is called with
// (here: the offset taking a CGRect) is never called: the buttons stay at
// AppKit's default. The check runs once per process.
func TestWindowButtonsSignatureMismatch(t *testing.T) {
	var opened bool
	var fails []string
	var lookups [2]int
	onMain(func() {
		def, defHeight, ok := defaultLights(lightsTestOptions)
		if !ok {
			return
		}
		defer func() { lightsABI, lightsEncodingOf = 0, methodEncoding }()
		lightsABI = 0
		n := 0
		lightsEncodingOf = func(obj objc.ID, sel objc.SEL) string {
			n++
			if sel == selSetButtonGroupOffset {
				return "v@:{CGRect={CGPoint=dd}{CGSize=dd}}"
			}
			return methodEncoding(obj, sel)
		}
		for i := range lookups {
			w, err := Open(lightsTestOptions)
			if err != nil {
				return
			}
			opened = true
			if w.tb.native {
				fails = append(fails, "a mismatched signature still places the buttons with AppKit's private metrics")
			}
			fails = append(fails, checkDefault(w, def, defHeight)...)
			w.Close()
			lookups[i] = n
		}
	})
	if !opened {
		t.Skip("cannot open a window here")
	}
	for _, f := range fails {
		t.Error(f)
	}
	if lookups[0] == 0 || lookups[1] != lookups[0] {
		t.Errorf("signature lookups after the first and second window: %v, want some and then no more", lookups)
	}
}

// The real signatures are the expected ones wherever the selectors exist.
func TestWindowButtonsSignatures(t *testing.T) {
	var responds, ok bool
	var enc [4]string
	onMain(func() {
		w, err := Open(lightsTestOptions)
		if err != nil {
			return
		}
		defer w.Close()
		responds = lightsNativeHere(w)
		for i, s := range []objc.SEL{selSetTitlebarHeight, selTitlebarHeight, selSetButtonGroupOffset, selButtonGroupOffset} {
			enc[i] = methodEncoding(w.win, s)
		}
		saved := lightsABI
		lightsABI = 0
		ok = lightsABIOK(w.win)
		lightsABI = saved
	})
	if responds && !ok {
		t.Errorf("AppKit's titlebar metrics have the encodings %q, which the check refuses", enc)
	}
}

// When AppKit does not put the close button where it was asked (forced here:
// every offset handed to it is skewed by a growing amount, so calibration
// never lands), the window undoes what it set: AppKit's height and offset go
// back to their defaults, and the buttons, their hover areas, hit test and
// SystemButtons are AppKit's default again, through later layouts too.
func TestWindowButtonsCalibrationRollback(t *testing.T) {
	var opened bool
	var fails []string
	var calls float64
	var native bool
	onMain(func() {
		def, defHeight, ok := defaultLights(lightsTestOptions)
		if !ok {
			return
		}
		w0, err := Open(lightsTestOptions)
		if err != nil {
			return
		}
		native = lightsNativeHere(w0)
		w0.Close()
		if !native {
			return
		}
		lightsOffsetSkew, lightsOffsetCalls = [2]float64{9, 4}, 0
		w, err := Open(lightsTestOptions)
		calls = lightsOffsetCalls
		lightsOffsetSkew = [2]float64{}
		if err != nil {
			return
		}
		defer w.Close()
		opened = true
		if w.tb.native {
			fails = append(fails, "calibration failed, yet the window still places the buttons with AppKit's metrics")
		}
		fails = append(fails, checkDefault(w, def, defHeight)...)
	})
	if !native {
		t.Skip("AppKit has no titlebar metrics here")
	}
	if !opened {
		t.Skip("cannot open a window here")
	}
	if calls < 2 {
		t.Errorf("the offset was handed to AppKit %v times: the calibration never ran", calls)
	}
	for _, f := range fails {
		t.Error(f)
	}
}
