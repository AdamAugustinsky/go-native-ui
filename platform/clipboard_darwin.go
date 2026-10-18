//go:build darwin

package platform

import (
	"sync"
	"unicode/utf8"
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
	selAllocStr, selInitBytes, selLenBytes                        objc.SEL
)

func pbInit() {
	pbOnce.Do(func() {
		loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit") // Open does too; tests and tools may not have
		clsPasteboard, clsNSString = objc.Cls("NSPasteboard"), objc.Cls("NSString")
		pbType = objc.Retain(objc.Str("public.utf8-plain-text"))
		selGeneralPB, selStringForType = objc.Sel("generalPasteboard"), objc.Sel("stringForType:")
		selClearContents, selSetStrFT = objc.Sel("clearContents"), objc.Sel("setString:forType:")
		selAllocStr, selInitBytes = objc.Sel("alloc"), objc.Sel("initWithBytes:length:encoding:")
		selLenBytes = objc.Sel("lengthOfBytesUsingEncoding:")
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
	SetClipboardText(unsafe.Slice(unsafe.StringData(s), len(s))) // read-only: the call copies
}

// appendValidUTF8 appends b to dst with each run of invalid bytes replaced by
// one U+FFFD (what strings.ToValidUTF8 does), without allocating once dst has
// the capacity.
func appendValidUTF8(dst, b []byte) []byte {
	bad := false
	for i := 0; i < len(b); {
		if c := b[i]; c < utf8.RuneSelf {
			dst = append(dst, c)
			i++
			bad = false
			continue
		}
		r, n := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && n == 1 {
			if !bad {
				dst = append(dst, "\uFFFD"...)
			}
			bad = true
			i++
			continue
		}
		dst = append(dst, b[i:i+n]...)
		i += n
		bad = false
	}
	return dst
}
