//go:build darwin

package platform

import (
	"bytes"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Images, file URLs and the concealed hint on NSPasteboard.
//
// The readers run on the pasteboard worker's thread (pasteboard_worker_darwin.go)
// with a frame of its own, never on the main thread: a pasteboard read waits for
// the owner of the data, which may be another process that is slow to answer.
// They take the pasteboard as an argument, so another pasteboard (the drag
// pasteboard of a drop) can use them:
//
//	pbAppendPNG(fr, pb, dst)     the bytes of public.png
//	pbTIFFData(fr, pb)           the NSData of public.tiff (borrowed; convertToPNG converts it)
//	pbAppendFiles(fr, pb, dst)   file URLs (public.file-url, then NSFilenamesPboardType) as NUL-ended paths
//	pbAppendText(fr, pb, dst)    public.utf8-plain-text
//
// Each runs inside an autorelease pool of the caller, copies into dst with no
// Go allocation once dst has the capacity, and measures before it copies, so a
// pasteboard over MaxPaste costs nothing on the Go heap. Every object it is
// handed by AppKit is checked with isKindOfClass: before it is messaged, because
// a pasteboard holds whatever its owner put there. convertToPNG
// (clipboard_imageio_darwin.go) is the conversion side.

var (
	pbImgOnce sync.Once
	// types
	pbPNG, pbTIFF, pbFileURL, pbFilenames, pbConcealed, pbTransient objc.ID
	clsPBNSData, clsPBNSURL, clsPBArray, clsPBString, clsPBItem     objc.ID
	// selectors
	selPBDataForType, selPBSetDataFT, selPBDataWithBytes, selPBNSDataEmpty                       objc.SEL
	selPBBytes, selPBLength, selPBCount, selPBObjectAt                                           objc.SEL
	selPBURLWithString, selPBFilePathURL, selPBPath, selPBPropListForType                        objc.SEL
	selPBTypes, selPBIsKind, selPBIsFileURL, selPBHost, selPBUTF8                                objc.SEL
	selPBGeneral, selPBStringForType, selPBLenBytes, selPBRetain, selPBRelease, selPBChangeCount objc.SEL
)

func pbImageInit() {
	pbInit()
	pbImgOnce.Do(func() {
		pbPNG, pbTIFF = objc.Retain(objc.Str("public.png")), objc.Retain(objc.Str("public.tiff"))
		pbFileURL, pbFilenames = objc.Retain(objc.Str("public.file-url")), objc.Retain(objc.Str("NSFilenamesPboardType"))
		pbConcealed, pbTransient = objc.Retain(objc.Str("org.nspasteboard.ConcealedType")), objc.Retain(objc.Str("org.nspasteboard.TransientType"))
		clsPBNSData, clsPBNSURL = objc.Cls("NSData"), objc.Cls("NSURL")
		clsPBArray, clsPBString, clsPBItem = objc.Cls("NSArray"), objc.Cls("NSString"), objc.Cls("NSPasteboardItem")
		selPBDataForType, selPBSetDataFT = objc.Sel("dataForType:"), objc.Sel("setData:forType:")
		selPBDataWithBytes, selPBNSDataEmpty = objc.Sel("dataWithBytes:length:"), objc.Sel("data")
		selPBBytes, selPBLength = objc.Sel("bytes"), objc.Sel("length")
		selPBCount, selPBObjectAt = objc.Sel("count"), objc.Sel("objectAtIndex:") // selPBItems: drag_darwin.go
		selPBURLWithString, selPBFilePathURL, selPBPath = objc.Sel("URLWithString:"), objc.Sel("filePathURL"), objc.Sel("path")
		selPBPropListForType = objc.Sel("propertyListForType:")
		selPBTypes, selPBIsKind, selPBIsFileURL = objc.Sel("types"), objc.Sel("isKindOfClass:"), objc.Sel("isFileURL")
		selPBHost, selPBUTF8 = objc.Sel("host"), objc.Sel("UTF8String")
		selPBGeneral, selPBStringForType, selPBLenBytes = objc.Sel("generalPasteboard"), objc.Sel("stringForType:"), objc.Sel("lengthOfBytesUsingEncoding:")
		selPBRetain, selPBRelease = objc.Sel("retain"), objc.Sel("release")
		selPBChangeCount = objc.Sel("changeCount")
	})
}

// What a pasteboard reader found.
type pbResult uint8

const (
	pbAbsent pbResult = iota // the pasteboard has none of it
	pbFound                  // appended
	pbTooBig                 // it has it, over MaxPaste: nothing was copied
)

// Messages on a worker's frame (the objc.Send helpers are the main thread's).
func pbSend(fr *ffi.Frame, recv uintptr, sel objc.SEL) uintptr {
	return fr.Call2(objc.MsgSend, recv, uintptr(sel))
}

func pbSend1(fr *ffi.Frame, recv uintptr, sel objc.SEL, a uintptr) uintptr {
	return fr.Call3(objc.MsgSend, recv, uintptr(sel), a)
}

// pbIsKind reports whether obj is an instance of cls (nil is not).
func pbIsKind(fr *ffi.Frame, obj uintptr, cls objc.ID) bool {
	return obj != 0 && fr.Reset().Arg(obj).Arg(uintptr(selPBIsKind)).Arg(uintptr(cls)).Call(objc.MsgSend).Bool()
}

// pbGrow makes room for n more bytes in dst without the append's doubling
// past MaxPaste: n must fit in MaxPaste together with len(dst).
func pbGrow(dst []byte, n int) []byte {
	if cap(dst)-len(dst) >= n {
		return dst
	}
	c := min(max(len(dst)+n, 2*cap(dst)), MaxPaste+1)
	out := make([]byte, len(dst), max(c, len(dst)+n))
	copy(out, dst)
	return out
}

// pbAppendString appends the UTF-8 bytes of the NSString str to dst when they
// are at most room, measuring them before copying. An object that is not a
// string, or an empty one, is pbAbsent.
func pbAppendString(fr *ffi.Frame, str uintptr, dst []byte, room int) ([]byte, pbResult) {
	if !pbIsKind(fr, str, clsPBString) {
		return dst, pbAbsent
	}
	n := int(pbSend1(fr, str, selPBLenBytes, 4 /* NSUTF8StringEncoding */))
	if n <= 0 {
		return dst, pbAbsent
	}
	if n > room {
		return dst, pbTooBig
	}
	p := pbSend(fr, str, selPBUTF8)
	if p == 0 {
		return dst, pbAbsent
	}
	dst = pbGrow(dst, n)
	return append(dst, unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)...), pbFound
}

// pbAppendText appends the pasteboard's plain text to dst.
func pbAppendText(fr *ffi.Frame, pb uintptr, dst []byte) ([]byte, pbResult) {
	return pbAppendString(fr, pbSend1(fr, pb, selPBStringForType, uintptr(pbType)), dst, MaxPaste-len(dst))
}

// pbAppendPNG appends the bytes of the pasteboard's public.png to dst.
func pbAppendPNG(fr *ffi.Frame, pb uintptr, dst []byte) ([]byte, pbResult) {
	data := pbSend1(fr, pb, selPBDataForType, uintptr(pbPNG))
	if !pbIsKind(fr, data, clsPBNSData) {
		return dst, pbAbsent
	}
	n := int(pbSend(fr, data, selPBLength))
	if n > MaxPaste-len(dst) {
		return dst, pbTooBig
	}
	if n < 8 {
		return dst, pbAbsent
	}
	p := pbSend(fr, data, selPBBytes)
	if p == 0 {
		return dst, pbAbsent
	}
	src := unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)
	if [8]byte(src) != pngSig {
		return dst, pbAbsent
	}
	return append(pbGrow(dst, n), src...), pbFound
}

// pbTIFFData returns the pasteboard's public.tiff data (0: none) and its
// length. The NSData belongs to the caller's autorelease pool: retain it to
// keep it past the pool (convertToPNG releases what it is given).
func pbTIFFData(fr *ffi.Frame, pb uintptr) (data uintptr, n int) {
	data = pbSend1(fr, pb, selPBDataForType, uintptr(pbTIFF))
	if !pbIsKind(fr, data, clsPBNSData) {
		return 0, 0
	}
	return data, int(pbSend(fr, data, selPBLength))
}

// pbLocalHost reports whether an NSURL host (0: none) names this machine: no
// host, or "localhost".
func pbLocalHost(fr *ffi.Frame, host uintptr) bool {
	if host == 0 {
		return true
	}
	if !pbIsKind(fr, host, clsPBString) {
		return false
	}
	n := int(pbSend1(fr, host, selPBLenBytes, 4))
	if n == 0 {
		return true
	}
	p := pbSend(fr, host, selPBUTF8)
	return n == 9 && p != 0 && strings.EqualFold(string(unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)), "localhost")
}

// pbEachPath calls f with the NSString of each path the pasteboard offers as a
// file: the file URL of each pasteboard item (only file URLs on this machine: an
// empty host or localhost) and, if there is no such item, the entries of the
// NSFilenamesPboardType list that older programs write. Entries that are not
// what they should be (an item that is not a pasteboard item, a value that is
// not a string or an array, a URL that is not a file URL) are not visited. f
// returns false to stop. It reports how many paths were visited.
func pbEachPath(fr *ffi.Frame, pb uintptr, f func(str uintptr) bool) (n int) {
	items := pbSend(fr, pb, selPBItems)
	if pbIsKind(fr, items, clsPBArray) {
		for i, cnt := 0, int(pbSend(fr, items, selPBCount)); i < cnt; i++ {
			item := pbSend1(fr, items, selPBObjectAt, uintptr(i))
			if !pbIsKind(fr, item, clsPBItem) {
				continue
			}
			s := pbSend1(fr, item, selPBStringForType, uintptr(pbFileURL))
			if !pbIsKind(fr, s, clsPBString) {
				continue
			}
			url := pbSend1(fr, uintptr(clsPBNSURL), selPBURLWithString, s)
			if !pbIsKind(fr, url, clsPBNSURL) || !fr.Reset().Arg(url).Arg(uintptr(selPBIsFileURL)).Call(objc.MsgSend).Bool() ||
				!pbLocalHost(fr, pbSend(fr, url, selPBHost)) {
				continue
			}
			fu := pbSend(fr, url, selPBFilePathURL) // also resolves file reference URLs (file:///.file/id=)
			if !pbIsKind(fr, fu, clsPBNSURL) {
				continue
			}
			path := pbSend(fr, fu, selPBPath)
			if !pbIsKind(fr, path, clsPBString) {
				continue
			}
			n++
			if !f(path) {
				return n
			}
		}
	}
	if n > 0 {
		return n
	}
	list := pbSend1(fr, pb, selPBPropListForType, uintptr(pbFilenames))
	if pbIsKind(fr, list, clsPBArray) {
		for i, cnt := 0, int(pbSend(fr, list, selPBCount)); i < cnt; i++ {
			path := pbSend1(fr, list, selPBObjectAt, uintptr(i))
			if !pbIsKind(fr, path, clsPBString) {
				continue
			}
			n++
			if !f(path) {
				return n
			}
		}
	}
	return n
}

// pbAppendFiles appends the pasteboard's file paths (pbEachPath) to dst, each
// ended by a NUL, and returns how many. The paths are measured first, without
// copying a byte, against MaxPaste (the NULs counted): a list over it is
// pbTooBig and dst is as it was, and one that fits is copied into room made for
// exactly its size. A path with a NUL inside is skipped, since it would split in
// two.
func pbAppendFiles(fr *ffi.Frame, pb uintptr, dst []byte) ([]byte, int, pbResult) {
	start := len(dst)
	total := 0
	pbEachPath(fr, pb, func(str uintptr) bool {
		if n := int(pbSend1(fr, str, selPBLenBytes, 4)); n > 0 {
			total += n + 1
		}
		return total <= MaxPaste
	})
	switch {
	case total > MaxPaste-start:
		return dst, 0, pbTooBig
	case total == 0:
		return dst, 0, pbAbsent
	}
	dst = pbGrow(dst, total)
	n, big := 0, false
	pbEachPath(fr, pb, func(str uintptr) bool {
		before := len(dst)
		var res pbResult
		if dst, res = pbAppendString(fr, str, dst, MaxPaste-(len(dst)-start)-1); res == pbTooBig { // (the list changed since it was measured)
			big = true
			return false
		} else if res == pbFound {
			if bytes.IndexByte(dst[before:], 0) >= 0 {
				dst = dst[:before]
				return true
			}
			dst = append(dst, 0)
			n++
		}
		return true
	})
	if big {
		return dst[:start], 0, pbTooBig
	}
	if n == 0 {
		return dst[:start], 0, pbAbsent
	}
	return dst, n, pbFound
}

// pbMarkConcealed adds the types that tell clipboard managers not to record
// what was just copied (see ClipOptions.Concealed). Call it after the data was
// set, in the same session (no clearContents between).
func pbMarkConcealed(pb objc.ID) {
	empty := objc.Send(clsPBNSData, selPBNSDataEmpty)
	objc.Send2(pb, selPBSetDataFT, uintptr(empty), uintptr(pbConcealed))
	objc.Send2(pb, selPBSetDataFT, uintptr(empty), uintptr(pbTransient))
}

// SetClipboardImage replaces the system clipboard with the PNG file png (see
// paste.go). Main thread.
//
// The pasteboard gets public.png, which AppKit's own image readers
// (NSImage), Preview, browsers and most editors read. With Concealed the
// types org.nspasteboard.ConcealedType and org.nspasteboard.TransientType
// come with it.
func SetClipboardImage(png []byte, opts ...ClipOptions) error {
	n, err := checkPNG(png)
	if err != nil {
		return err
	}
	o := clipOpts(opts)
	pbImageInit()
	pool := objc.F.Call0(objc.PoolPush)
	if pb := objc.Send(clsPasteboard, selGeneralPB); pb != 0 {
		data := objc.ID(objc.F.Reset().Arg(uintptr(clsPBNSData)).Arg(uintptr(selPBDataWithBytes)).
			ArgPtr(unsafe.Pointer(&png[0])).Arg(uintptr(n)).Call(objc.MsgSend).R0()) // copies
		if data != 0 {
			objc.Send(pb, selClearContents)
			objc.Send2(pb, selPBSetDataFT, uintptr(data), uintptr(pbPNG))
			if o.Concealed {
				pbMarkConcealed(pb)
			}
		}
	}
	objc.F.Call1(objc.PoolPop, pool)
	runtime.KeepAlive(png)
	return nil
}

// SetClipboardTextWith is SetClipboardText with options (see ClipOptions).
// Main thread.
func SetClipboardTextWith(b []byte, o ClipOptions) { setClipboardText(b, o.Concealed) }
