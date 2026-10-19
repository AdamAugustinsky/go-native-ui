//go:build darwin && gnui_publicapi

package platform

import "testing"

// A gnui_publicapi build never uses AppKit's private titlebar metrics: the
// buttons, their hover areas, hit test and SystemButtons are AppKit's
// default, and AppKit's own titlebar height and offset untouched.
func TestWindowButtonsPublicBuild(t *testing.T) {
	var opened, native bool
	var fails []string
	onMain(func() {
		def, defHeight, ok := defaultLights(lightsTestOptions)
		if !ok {
			return
		}
		w, err := Open(lightsTestOptions)
		if err != nil {
			return
		}
		defer w.Close()
		opened, native = true, w.tb.native
		fails = checkDefault(w, def, defHeight)
	})
	if !opened {
		t.Skip("cannot open a window here")
	}
	if native {
		t.Error("a gnui_publicapi build places the buttons with AppKit's private metrics")
	}
	for _, f := range fails {
		t.Error(f)
	}
}
