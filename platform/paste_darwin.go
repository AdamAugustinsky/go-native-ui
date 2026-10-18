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
func SetClipboardText(b []byte) {
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
		}
	}
	objc.F.Call1(objc.PoolPop, pool)
	runtime.KeepAlive(b)
}

// RequestPaste asks for the plain text on the clipboard. The answer arrives as
// one EvPaste event on w's queue with Tag set to tag: its Text, or FlagFailed.
// On macOS it is on the queue when RequestPaste returns, so the next Wait or
// Poll delivers it. A newer request replaces a pending one, whose answer then
// never arrives. The Text of an EvPaste stays valid like that of EvText: until
// the next Wait or Poll. Main thread.
func (w *Window) RequestPaste(tag uint64) {
	pbInit()
	q := &w.q
	from := 0
	if w.returned {
		from = w.nOut // the events before it are already with the app
	}
	q.dropQueuedPaste(from)
	off := len(q.text)
	pool := objc.F.Call0(objc.PoolPush)
	if pb := objc.Send(clsPasteboard, selGeneralPB); pb != 0 {
		q.text = objc.AppendUTF8(q.text, objc.Send1(pb, selStringForType, uintptr(pbType)))
	}
	objc.F.Call1(objc.PoolPop, pool)
	n := len(q.text) - off
	if n == 0 || n > MaxPaste {
		q.text = q.text[:off]
		q.push(Event{Kind: EvPaste, Flags: FlagFailed, Tag: tag, Time: Now()})
		return
	}
	q.push(Event{Kind: EvPaste, Text: TextRef{uint32(off), uint32(n)}, Tag: tag, Time: Now()})
}

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
