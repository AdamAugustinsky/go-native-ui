//go:build darwin

package platform

import (
	"runtime"
	"sync"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Images, file URLs and the concealed hint on NSPasteboard.
//
// The readers take the pasteboard as an argument, so another pasteboard (the
// drag pasteboard of a drop) can use them:
//
//	pbAppendPNG(pb, dst)    the bytes of public.png
//	pbTIFFData(pb)          the NSData of public.tiff (borrowed; convertToPNG converts it on a worker)
//	pbAppendFiles(pb, dst)  file URLs (public.file-url, then NSFilenamesPboardType) as NUL-ended paths
//
// All run on the main thread inside an autorelease pool of the caller, copy
// into dst with no Go allocation once dst has the capacity, and measure
// before they copy, so a pasteboard over MaxPaste costs nothing on the Go
// heap. convertToPNG (clipboard_imageio_darwin.go) is the worker side.

var (
	pbImgOnce sync.Once
	// types
	pbPNG, pbTIFF, pbFileURL, pbFilenames, pbConcealed, pbTransient objc.ID
	clsPBNSData, clsPBNSURL                                         objc.ID
	// selectors
	selPBDataForType, selPBSetDataFT, selPBDataWithBytes, selPBNSDataEmpty objc.SEL
	selPBBytes, selPBLength, selPBItems, selPBCount, selPBObjectAt         objc.SEL
	selPBURLWithString, selPBFilePathURL, selPBPath, selPBPropListForType  objc.SEL
	selPBTypes                                                             objc.SEL
)

func pbImageInit() {
	pbInit()
	pbImgOnce.Do(func() {
		pbPNG, pbTIFF = objc.Retain(objc.Str("public.png")), objc.Retain(objc.Str("public.tiff"))
		pbFileURL, pbFilenames = objc.Retain(objc.Str("public.file-url")), objc.Retain(objc.Str("NSFilenamesPboardType"))
		pbConcealed, pbTransient = objc.Retain(objc.Str("org.nspasteboard.ConcealedType")), objc.Retain(objc.Str("org.nspasteboard.TransientType"))
		clsPBNSData, clsPBNSURL = objc.Cls("NSData"), objc.Cls("NSURL")
		selPBDataForType, selPBSetDataFT = objc.Sel("dataForType:"), objc.Sel("setData:forType:")
		selPBDataWithBytes, selPBNSDataEmpty = objc.Sel("dataWithBytes:length:"), objc.Sel("data")
		selPBBytes, selPBLength = objc.Sel("bytes"), objc.Sel("length")
		selPBItems, selPBCount, selPBObjectAt = objc.Sel("pasteboardItems"), objc.Sel("count"), objc.Sel("objectAtIndex:")
		selPBURLWithString, selPBFilePathURL, selPBPath = objc.Sel("URLWithString:"), objc.Sel("filePathURL"), objc.Sel("path")
		selPBPropListForType = objc.Sel("propertyListForType:")
		selPBTypes = objc.Sel("types")
	})
}

// What a pasteboard reader found.
type pbResult uint8

const (
	pbAbsent pbResult = iota // the pasteboard has none of it
	pbFound                  // appended
	pbTooBig                 // it has it, over MaxPaste: nothing was copied
)

// pbAppendPNG appends the bytes of the pasteboard's public.png to dst.
func pbAppendPNG(pb objc.ID, dst []byte) ([]byte, pbResult) {
	data := objc.Send1(pb, selPBDataForType, uintptr(pbPNG))
	if data == 0 {
		return dst, pbAbsent
	}
	n := int(objc.Send(data, selPBLength))
	if n > MaxPaste {
		return dst, pbTooBig
	}
	if n < 8 {
		return dst, pbAbsent
	}
	p := objc.Send(data, selPBBytes)
	if p == 0 {
		return dst, pbAbsent
	}
	src := unsafe.Slice((*byte)(ffi.PtrFromC(uintptr(p))), n)
	if [8]byte(src) != pngSig {
		return dst, pbAbsent
	}
	return append(dst, src...), pbFound
}

// pbTIFFData returns the pasteboard's public.tiff data (0: none) and its
// length. The NSData belongs to the caller's autorelease pool: Retain it to
// hand it to a worker (convertToPNG releases what it is given).
func pbTIFFData(pb objc.ID) (data objc.ID, n int) {
	data = objc.Send1(pb, selPBDataForType, uintptr(pbTIFF))
	if data == 0 {
		return 0, 0
	}
	return data, int(objc.Send(data, selPBLength))
}

// pbAppendFiles appends the pasteboard's file URLs to dst as paths, each ended
// by a NUL, and returns how many. It reads public.file-url of each pasteboard
// item and, if there is none, the NSFilenamesPboardType list that older
// programs write. Items that are not file URLs are left out. The paths are
// bounded by MaxPaste: more is pbTooBig and dst is as it was.
func pbAppendFiles(pb objc.ID, dst []byte) ([]byte, int, pbResult) {
	start := len(dst)
	n := 0
	items := objc.Send(pb, selPBItems)
	for i, cnt := 0, int(objc.Send(items, selPBCount)); items != 0 && i < cnt; i++ {
		item := objc.Send1(items, selPBObjectAt, uintptr(i))
		s := objc.Send1(item, selStringForType, uintptr(pbFileURL))
		if s == 0 {
			continue
		}
		url := objc.Send1(clsPBNSURL, selPBURLWithString, uintptr(s))
		if url == 0 {
			continue
		}
		if fu := objc.Send(url, selPBFilePathURL); fu != 0 { // also resolves file reference URLs (file:///.file/id=)
			url = fu
		}
		before := len(dst)
		dst = objc.AppendUTF8(dst, objc.Send(url, selPBPath))
		if len(dst) == before {
			continue
		}
		dst = append(dst, 0)
		n++
		if len(dst)-start > MaxPaste {
			return dst[:start], 0, pbTooBig
		}
	}
	if n == 0 { // the older list
		list := objc.Send1(pb, selPBPropListForType, uintptr(pbFilenames))
		for i, cnt := 0, int(objc.Send(list, selPBCount)); list != 0 && i < cnt; i++ {
			before := len(dst)
			dst = objc.AppendUTF8(dst, objc.Send1(list, selPBObjectAt, uintptr(i)))
			if len(dst) == before {
				continue
			}
			dst = append(dst, 0)
			n++
			if len(dst)-start > MaxPaste {
				return dst[:start], 0, pbTooBig
			}
		}
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
