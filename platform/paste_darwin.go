package platform

import (
	"errors"
	"runtime"
	"sync"
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
// On macOS it is on the queue when RequestPaste returns, so the next Wait or
// Poll delivers it. A newer request replaces a pending one, whose answer then
// never arrives. The Text of an EvPaste stays valid like that of EvText: until
// the next Wait or Poll. Main thread. Text over MaxPaste bytes is measured,
// not copied.
func (w *Window) RequestPaste(tag uint64) { w.RequestPasteKinds(tag, WantText) }

// RequestPasteKinds asks for the first of files, image and text, of the
// requested kinds, that the clipboard holds (see paste.go). Files, PNG and
// text are answered at once, as RequestPaste does; an image that is only on
// the pasteboard as a TIFF is converted to PNG with ImageIO on a worker, and
// its answer arrives in a later Wait (a failed conversion is answered as
// FlagFailed, without trying the other kinds). Main thread.
//
// Files are the file URLs of the pasteboard's items (public.file-url, or the
// NSFilenamesPboardType list older programs write). Nothing of a pasteboard
// over MaxPaste is copied: it is measured first.
func (w *Window) RequestPasteKinds(tag uint64, kinds PasteKinds) {
	pbImageInit()
	q := &w.q
	from := 0
	if w.returned {
		from = w.nOut // the events before it are already with the app
	}
	q.dropQueuedPaste(from)
	pasteJob.cancel()
	off := len(q.text)
	flags := Flags(FlagFailed)
	var kind PasteKind
	found, big := false, false
	var nfiles uint32

	pool := objc.F.Call0(objc.PoolPush)
	if pb := objc.Send(clsPasteboard, selGeneralPB); pb != 0 {
		if kinds&WantFiles != 0 && !found {
			var n int
			var res pbResult
			q.text, n, res = pbAppendFiles(pb, q.text)
			found, big = found || res == pbFound, big || res == pbTooBig
			kind, nfiles = PasteFiles, uint32(n)
		}
		if kinds&WantImage != 0 && !found {
			var res pbResult
			q.text, res = pbAppendPNG(pb, q.text)
			if res == pbFound {
				found, kind = true, PasteImage
			}
			big = big || res == pbTooBig
			if !found && res == pbAbsent {
				if data, n := pbTIFFData(pb); data != 0 && n > 0 {
					if n > maxClipSource {
						big = true
					} else {
						pasteJob.start(w, tag, objc.Retain(data))
						objc.F.Call1(objc.PoolPop, pool)
						return
					}
				}
			}
		}
		if kinds&WantText != 0 && !found {
			// The size is asked for before any byte is copied, so an oversized
			// clipboard costs the scan and nothing on the Go heap.
			if str := objc.Send1(pb, selStringForType, uintptr(pbType)); str != 0 {
				n := int(objc.Send1(str, selLenBytes, 4 /* NSUTF8StringEncoding */))
				switch {
				case n > MaxPaste:
					big = true
				case n > 0:
					q.text = objc.AppendUTF8(q.text, str)
					found, kind = true, PasteText
				}
			}
		}
	}
	objc.F.Call1(objc.PoolPop, pool)
	n := len(q.text) - off
	if !found || n == 0 {
		q.text = q.text[:off]
		if big {
			flags |= FlagTooBig
		}
		q.push(Event{Kind: EvPaste, Flags: flags, Tag: tag, Time: Now()})
		return
	}
	q.push(Event{Kind: EvPaste, Paste: kind, Code: nfiles, Text: TextRef{uint32(off), uint32(n)}, Tag: tag, Time: Now()})
}

// pasteConv is the TIFF conversion in flight, if any: one at a time, the
// newest request's. A worker goroutine converts and leaves the PNG here and
// wakes the loop; the next Wait (reapPaste) turns it into the EvPaste of the
// window that asked.
type pasteConv struct {
	mu      sync.Mutex
	seq     uint64 // the job that counts; a worker finishing another drops its result
	pending bool   // a conversion for seq is running, or its result waits
	w       *Window
	tag     uint64
	done    bool
	png     []byte
	err     error
}

var pasteJob pasteConv

// cancel forgets the job in flight: its result will be dropped.
func (j *pasteConv) cancel() {
	j.mu.Lock()
	j.seq++
	j.pending, j.done, j.png, j.err = false, false, nil, nil
	j.mu.Unlock()
}

// start begins converting data (retained; the worker releases it) for w's
// request tag.
func (j *pasteConv) start(w *Window, tag uint64, data objc.ID) {
	j.mu.Lock()
	j.seq++
	seq := j.seq
	j.pending, j.done, j.w, j.tag = true, false, w, tag
	j.mu.Unlock()
	go func() {
		png, err := convertToPNG(uintptr(data), MaxPaste)
		j.mu.Lock()
		if j.seq == seq {
			j.done, j.png, j.err = true, png, err
		}
		j.mu.Unlock()
		Wake()
	}()
}

// reapPaste turns a finished conversion into the EvPaste of its request. Wait
// calls it after the AppKit events; main thread.
func (w *Window) reapPaste() {
	j := &pasteJob
	j.mu.Lock()
	if !j.done || j.w != w {
		j.mu.Unlock()
		return
	}
	png, err, tag := j.png, j.err, j.tag
	j.pending, j.done, j.png, j.err = false, false, nil, nil
	j.mu.Unlock()
	q := &w.q
	switch {
	case err != nil:
		fl := Flags(FlagFailed)
		if errors.Is(err, errPNGTooBig) {
			fl |= FlagTooBig
		}
		q.push(Event{Kind: EvPaste, Flags: fl, Tag: tag, Time: Now()})
	default:
		off := len(q.text)
		q.text = append(q.text, png...)
		q.push(Event{Kind: EvPaste, Paste: PasteImage, Text: TextRef{uint32(off), uint32(len(png))}, Tag: tag, Time: Now()})
	}
}

// pastePending reports whether a conversion is running or waits to be taken.
func pastePending() bool {
	pasteJob.mu.Lock()
	defer pasteJob.mu.Unlock()
	return pasteJob.pending
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
