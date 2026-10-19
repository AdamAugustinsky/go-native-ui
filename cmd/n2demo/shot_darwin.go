//go:build darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gonativeui/internal/objc"
	"gonativeui/ui"
)

// shoot draws the open sheet into a PNG by asking its views to draw themselves
// into a bitmap: no screen recording is involved, so it works where a capture
// tool cannot, at the price of leaving out what the window server adds (the
// shadow, the material under the sheet, which also makes its dark appearance
// useless: light text on an undrawn background) and any content a panel hosts
// in another process (the open panel's browser). It runs once, a moment after
// the sheet attached, then quits the app.
func (d *demo) shoot(c *ui.Context) {
	if d.shotDir == "" || d.doName == "" || d.shotDone || !d.started {
		return
	}
	win := objc.ID(d.a.Win.Native().NSWindow)
	sheet := objc.Send(win, objc.Sel("attachedSheet"))
	if sheet == 0 {
		return
	}
	now := c.Input().Time
	if d.shotAt == 0 {
		d.shotAt = now + 1500e6 // let the sheet's animation finish
	}
	if now < d.shotAt {
		c.WakeIn(100e6)
		return
	}
	d.shotDone = true
	if err := os.MkdirAll(d.shotDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "n2demo:", err)
		d.a.Quit()
		return
	}
	path := filepath.Join(d.shotDir, d.doName+".png")
	if err := writePNG(sheet, path); err != nil {
		fmt.Fprintln(os.Stderr, "n2demo:", err)
	} else {
		fmt.Println(path)
	}
	d.a.Quit()
}

// writePNG draws the sheet's frame view (its content with the window's own
// chrome) into a bitmap and writes it as a PNG.
func writePNG(sheet objc.ID, path string) error {
	content := objc.Send(sheet, objc.Sel("contentView"))
	frame := objc.Send(content, objc.Sel("superview"))
	if frame == 0 {
		frame = content
	}
	// Selectors are looked up first: Sel uses the frame the calls are built in.
	selBounds, selMakeRep, selCache := objc.Sel("bounds"), objc.Sel("bitmapImageRepForCachingDisplayInRect:"), objc.Sel("cacheDisplayInRect:toBitmapImageRep:")
	selPNG, selWrite := objc.Sel("representationUsingType:properties:"), objc.Sel("writeToFile:atomically:")
	b := objc.SendRect(frame, selBounds)
	rep := objc.ID(objc.F.Reset().Arg(uintptr(frame)).Arg(uintptr(selMakeRep)).ArgRect(b.X, b.Y, b.W, b.H).Call(objc.MsgSend).R0())
	if rep == 0 {
		return fmt.Errorf("no bitmap for %v", b)
	}
	objc.F.Reset().Arg(uintptr(frame)).Arg(uintptr(selCache)).ArgRect(b.X, b.Y, b.W, b.H).Arg(uintptr(rep)).Call(objc.MsgSend)
	data := objc.Send2(rep, selPNG, 4 /* PNG */, 0)
	if data == 0 {
		return fmt.Errorf("no PNG data")
	}
	str := objc.Str(path)
	if !objc.F.Reset().Arg(uintptr(data)).Arg(uintptr(selWrite)).Arg(uintptr(str)).Arg(1).Call(objc.MsgSend).Bool() {
		return fmt.Errorf("cannot write %s", path)
	}
	return nil
}
