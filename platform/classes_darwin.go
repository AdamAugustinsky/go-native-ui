//go:build darwin

package platform

import (
	"sync"

	"gonativeui/internal/objc"
)

var classesOnce sync.Once
var viewClass, delegateClass objc.ID

// registerClasses defines GNUIView and GNUIWindowDelegate. Each concern adds
// its own methods in its registerXxx function; the classes are created once,
// here, when all of them have.
func registerClasses() {
	objc.Trace = debugIMP
	view := objc.Define("GNUIView", objc.Cls("NSView"), []string{"NSTextInputClient"})
	registerViewKeyboard(view)
	registerViewTextInput(view)
	registerViewDraw(view)
	registerViewMouse(view)
	viewClass = view.Register()
	delegate := objc.Define("GNUIWindowDelegate", objc.Cls("NSObject"), []string{"NSWindowDelegate"})
	registerWindowDelegate(delegate)
	registerShell(delegate)
	delegateClass = delegate.Register()
	registerShellClasses() // GNUIContainer, GNUIHostClip, GNUIWindow (host_darwin.go)
	clsNSArray, clsNSAttributedString = objc.Cls("NSArray"), objc.Cls("NSAttributedString")
	clsMetalLink = objc.Cls("CAMetalDisplayLink") // macOS 14+
	clsRunLoop = objc.Cls("NSRunLoop")
}
