package rexui_test

import (
	"reflect"
	"testing"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/third_party/lucide"
)

// Every icon rexui gives an app is a Lucide icon of the vendored set: the
// sidebar's places, the list glyphs, and what ToolbarIcon and SidebarTileIcon
// make.
func TestRexuiIconsAreLucide(t *testing.T) {
	var specs []icon.Spec
	v := reflect.ValueOf(rexui.SidebarIcons)
	for i := 0; i < v.NumField(); i++ {
		specs = append(specs, v.Field(i).Interface().(icon.Spec))
	}
	specs = append(specs, rexui.ListGlyphSpecs[:]...)
	specs = append(specs, rexui.ToolbarIcon("panel-left", '▤'), rexui.SidebarTileIcon("wifi", '■'))
	for _, sp := range specs {
		if sp.Lucide == "" {
			t.Errorf("%+v is not a Lucide icon", sp)
			continue
		}
		if _, ok := lucide.Lookup(sp.Lucide); !ok {
			t.Errorf("no Lucide icon %q", sp.Lucide)
		}
		if sp.Pt <= 0 || sp.Fallback == 0 {
			t.Errorf("%s: size %v, fallback %q", sp.Lucide, sp.Pt, sp.Fallback)
		}
	}
}
