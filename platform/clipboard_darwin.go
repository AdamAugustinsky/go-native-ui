//go:build darwin

package platform

import (
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"gonativeui/internal/objc"
)

// The general pasteboard. NSPasteboardTypeString is the UTI
// "public.utf8-plain-text".

var (
	pbOnce                                                        sync.Once
	clsPasteboard, clsNSString                                    objc.ID
	pbType                                                        objc.ID
	selGeneralPB, selStringForType, selClearContents, selSetStrFT objc.SEL
	selAllocStr, selInitBytes                                     objc.SEL
)

func pbInit() {
	pbOnce.Do(func() {
		loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit") // Open does too; tests and tools may not have
		clsPasteboard, clsNSString = objc.Cls("NSPasteboard"), objc.Cls("NSString")
		pbType = objc.Retain(objc.Str("public.utf8-plain-text"))
		selGeneralPB, selStringForType = objc.Sel("generalPasteboard"), objc.Sel("stringForType:")
		selClearContents, selSetStrFT = objc.Sel("clearContents"), objc.Sel("setString:forType:")
		selAllocStr, selInitBytes = objc.Sel("alloc"), objc.Sel("initWithBytes:length:encoding:")
	})
}

// Clipboard returns the plain text on the system clipboard, "" if there is
// none. Main thread.
func Clipboard() string {
	pbInit()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	pb := objc.Send(clsPasteboard, selGeneralPB)
	if pb == 0 {
		return ""
	}
	return string(objc.AppendUTF8(nil, objc.Send1(pb, selStringForType, uintptr(pbType))))
}

// SetClipboard replaces the system clipboard with the plain text s. Main
// thread.
func SetClipboard(s string) {
	pbInit()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	pb := objc.Send(clsPasteboard, selGeneralPB)
	if pb == 0 {
		return
	}
	s = strings.ToValidUTF8(s, "\uFFFD")
	b := make([]byte, len(s)+1) // a non-nil pointer even for ""
	copy(b, s)
	str := objc.ID(objc.F.Reset().Arg(uintptr(objc.Send(clsNSString, selAllocStr))).Arg(uintptr(selInitBytes)).
		ArgPtr(unsafe.Pointer(&b[0])).Arg(uintptr(len(s))).Arg(4 /* NSUTF8StringEncoding */).Call(objc.MsgSend).R0())
	if str == 0 {
		return
	}
	objc.Send(pb, selClearContents)
	objc.Send2(pb, selSetStrFT, uintptr(str), uintptr(pbType))
	objc.Release(str)
	runtime.KeepAlive(b)
}
