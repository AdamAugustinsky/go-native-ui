package platform

import (
	"runtime"
	"unicode/utf8"
	"unsafe"

	"gonativeui/internal/objc"
)

// The asynchronous clipboard (see paste.go) on NSPasteboard. Neither call
// allocates on the Go heap once warm: the text goes to AppKit from the
// caller's bytes and comes back into the window's own text buffer, through
// the main thread's ffi frame. The paste is answered at once, so its event is
// on the queue for the same Wait.

// clipScratch holds a copy of text with invalid UTF-8 replaced, the only case
// in which SetClipboardText cannot hand the caller's bytes to AppKit.
var clipScratch []byte

var clipEmpty [1]byte // a non-nil pointer for empty text

// SetClipboardText replaces the system clipboard with a copy of the plain
// text b (UTF-8; invalid sequences become U+FFFD). b may be reused after the
// call. Main thread.
func SetClipboardText(b []byte) { setClipboardText(b, false) }

func setClipboardText(b []byte, concealed bool) {
	pbInit()
	if !utf8.Valid(b) {
		clipScratch = appendValidUTF8(clipScratch[:0], b)
		b = clipScratch
	}
	p := &clipEmpty[0]
	if len(b) > 0 {
		p = &b[0]
	}
	pool := objc.F.Call0(objc.PoolPush)
	if pb := objc.Send(clsPasteboard, selGeneralPB); pb != 0 {
		alloc := objc.Send(clsNSString, selAllocStr) // before the init call is assembled: both use the frame
		str := objc.ID(objc.F.Reset().Arg(uintptr(alloc)).Arg(uintptr(selInitBytes)).
			ArgPtr(unsafe.Pointer(p)).Arg(uintptr(len(b))).Arg(4 /* NSUTF8StringEncoding */).Call(objc.MsgSend).R0())
		if str != 0 {
			objc.Send(pb, selClearContents)
			objc.Send2(pb, selSetStrFT, uintptr(str), uintptr(pbType))
			objc.Release(str)
			if concealed {
				pbImageInit()
				pbMarkConcealed(pb)
			}
		}
	}
	objc.F.Call1(objc.PoolPop, pool)
	runtime.KeepAlive(b)
}

// RequestPaste asks for the plain text on the clipboard. The answer arrives as
// one EvPaste event on w's queue with Tag set to tag: its Text, or FlagFailed.
// The pasteboard is read on a worker (pasteboard_worker_darwin.go), so the
// answer comes with a later Wait or Poll, never during the call. A newer
// request replaces a pending one, whose answer then never arrives. The Text of
// an EvPaste stays valid like that of EvText: until the next Wait or Poll. Main
// thread. Text over MaxPaste bytes is measured, not copied.
func (w *Window) RequestPaste(tag uint64) { w.RequestPasteKinds(tag, WantText) }

// RequestPasteKinds asks for the first of files, image and text, of the
// requested kinds, that the clipboard holds (see paste.go). The general
// pasteboard is read on the pasteboard worker, which waits for the owner of the
// data if it is slow (a slow owner no longer freezes the window), and converts
// an image that is only there as a TIFF to PNG with ImageIO; the answer arrives
// in a later Wait. A kind that cannot be delivered (over MaxPaste, or a TIFF
// that does not convert) is passed over for the next requested kind.
//
// Files are the file URLs of the pasteboard's items (public.file-url, on this
// machine, or the NSFilenamesPboardType list older programs write). Nothing of a
// pasteboard over MaxPaste is copied: it is measured first. Main thread.
func (w *Window) RequestPasteKinds(tag uint64, kinds PasteKinds) {
	pbImageInit()
	q := &w.q
	from := 0
	if w.returned {
		from = w.nOut // the events before it are already with the app
	}
	q.dropQueuedPaste(from)
	rd := pasteboardReader()
	var pb objc.ID
	if kinds&(WantText|WantImage|WantFiles) != 0 {
		pool := objc.F.Call0(objc.PoolPush)
		if pb = objc.Send(clsPasteboard, selGeneralPB); pb != 0 {
			rd.submit(w, pb, tag, kinds) // retains it
		}
		objc.F.Call1(objc.PoolPop, pool)
	}
	if pb == 0 {
		rd.cancel()
		q.push(Event{Kind: EvPaste, Flags: FlagFailed, Tag: tag, Time: Now()})
	}
}

// reapPaste turns a finished read into the EvPaste of its request. Wait calls
// it after the AppKit events; main thread.
func (w *Window) reapPaste() { pasteboardReader().take(w) }

// pastePending reports whether a read is running or waits to be taken.
func pastePending() bool { return pasteboardReader().pending() }

// dropQueuedPaste removes the EvPaste events at index from on: the answer to
// an older request that has not been delivered.
func (q *eventQueue) dropQueuedPaste(from int) {
	from = min(from, len(q.ev))
	kept := q.ev[:from]
	for _, e := range q.ev[from:] {
		if e.Kind != EvPaste {
			kept = append(kept, e)
		}
	}
	q.ev = kept
}
