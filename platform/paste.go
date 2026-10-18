package platform

// The clipboard contract (phase 5, D28). Copying hands the platform bytes it
// copies; pasting is a request whose answer arrives later as an event, because
// on Linux the text comes from the app that owns the clipboard, through the
// display server, and the UI thread must not wait for it.
//
// This file is the synchronous version every OS starts from: it answers a
// request at once from Clipboard. A backend that needs to (Linux) or that can
// avoid the string copies (macOS, Windows) replaces RequestPaste and
// SetClipboardText in its own files.

// MaxPaste is the most text a paste delivers, in bytes; more fails
// (FlagFailed).
const MaxPaste = 16 << 20

// SetClipboardText replaces the system clipboard with a copy of the plain
// text b (UTF-8; invalid sequences become U+FFFD). b may be reused after the
// call. Main thread.
func SetClipboardText(b []byte) { SetClipboard(string(b)) }

// RequestPaste asks for the plain text on the clipboard. The answer arrives as
// one EvPaste event on w's queue with Tag set to tag: its Text, or FlagFailed.
// It may come during the same Wait or Poll or a later one (Linux: once the
// owning app has sent it). A newer request replaces a pending one, whose
// answer then never arrives. The Text of an EvPaste stays valid like that of
// EvText: until the next Wait or Poll. Main thread.
func (w *Window) RequestPaste(tag uint64) {
	s := Clipboard()
	if s == "" {
		w.q.pushPaste(nil, tag, Now())
		return
	}
	w.q.pushPaste([]byte(s), tag, Now())
}
