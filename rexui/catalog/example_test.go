package catalog_test

import (
	"fmt"
	"runtime"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/rexui/catalog"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The catalogue draws rexui's components as the samples of tools/appkitref,
// under the same names, so each can be set next to its AppKit counterpart.
// Here one sample is built in a frame and measured: AppKit's regular push
// button is 24 points high.
func Example() {
	runtime.LockOSThread() // the icon atlas rasterises through AppKit on macOS: one thread
	defer runtime.UnlockOSThread()
	ts := fakeui.New()
	c := ui.New(ts)
	c.AtlasSync(&fakeui.Uploader{})
	k := base.NewKit(c, ts, rex.Theme())
	st := catalog.NewState(k) // before the kit's first Begin
	i := catalog.Index("push-regular")

	var in ui.Input
	var list gpu.DrawList
	in.Inside, in.Focused = true, true
	in.MouseX, in.MouseY = -100, -100
	var id ui.ID
	for n := 0; n < 3; n++ {
		c.Begin(&in, 400, 300, 1, 1.0/60)
		k.Begin(c)
		id = st.Build(k, i)
		k.End()
		c.End(&list)
		in.ClearFrame()
	}
	r := c.Rect(id)
	fmt.Println(catalog.Samples[i].Name, r[3]-r[1], "pt high;", catalog.Index("no such sample"))
	// Output: push-regular 24 pt high; -1
}
