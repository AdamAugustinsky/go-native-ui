package platform

// The synchronous starting point (see paste.go): answered at once from
// Clipboard.

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
