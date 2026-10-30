//go:build darwin

package webview

import (
	"sync/atomic"
	"time"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// LoadTime is how long the first New took to load WebKit and register the
// classes (0 before it).
func LoadTime() time.Duration { return wk.loadTime }

var (
	mediaDecision atomic.Int64
	mediaBlock    objc.ID
)

// AskMedia calls v's UI delegate as WebKit does for a page's camera (typ 0),
// microphone (1) or both (2), and returns the decision it gave the handler
// (WKPermissionDecision: 1 grant, 2 deny; -1 none). It is the delegate's
// path where WebKit hides navigator.mediaDevices from a process with no
// NSCameraUsageDescription (a test binary). Main thread.
func AskMedia(v *View, typ int) int {
	if mediaBlock == 0 {
		mediaBlock = objc.NewGlobalBlock("v16@?0q8", func(c *ffi.CB) { mediaDecision.Store(int64(c.Int(1))) })
	}
	mediaDecision.Store(-1)
	b := v.be.(*view)
	objc.F.Reset().Arg(uintptr(b.del)).Arg(uintptr(selMediaPermission)).Arg(uintptr(b.wv)).Arg(0).Arg(0).Arg(uintptr(typ)).Arg(uintptr(mediaBlock)).Call(objc.MsgSend)
	return int(mediaDecision.Load())
}

// CornerRadiusOf is the corner radius of v's layer, in points.
func CornerRadiusOf(v *View) float64 {
	return objc.SendF64(objc.Send(v.be.(*view).wv, selLayer), objc.Sel("cornerRadius"))
}

// Ready reports whether v's web view is there: from New on, on macOS.
func Ready(*View) bool { return true }
