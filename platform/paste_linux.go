package platform

// The Linux clipboard (see clipboard_linux.go for how it works).

// SetClipboardText replaces the system clipboard with a copy of the plain
// text b (UTF-8; invalid sequences become U+FFFD). b may be reused after the
// call. Without an open window the text only stays in the process. Main
// thread.
func SetClipboardText(b []byte) { SetClipboardTextWith(b, ClipOptions{}) }

// SetClipboardTextWith is SetClipboardText with options (see ClipOptions): with
// Concealed the text is offered with x-kde-passwordManagerHint = secret too.
// Without an open window only the text is kept (and the hint is not). Main
// thread.
func SetClipboardTextWith(b []byte, o ClipOptions) {
	if w := &theWindow; w.open {
		w.clipSetData(b, false, o.Concealed)
		return
	}
	SetClipboard(string(b))
}

// SetClipboardImage replaces the system clipboard with the PNG file png (see
// paste.go): it is offered to other applications as image/png, through the
// display server, until another application takes the clipboard, with
// ClipOptions.Concealed also as x-kde-passwordManagerHint. It needs an open
// window (ErrNoClipboard without one). Main thread.
func SetClipboardImage(png []byte, opts ...ClipOptions) error {
	n, err := checkPNG(png)
	if err != nil {
		return err
	}
	w := &theWindow
	if !w.open {
		return ErrNoClipboard
	}
	w.clipSetData(png[:n], true, clipOpts(opts).Concealed)
	return nil
}

// RequestPaste asks for the plain text on the clipboard. The answer arrives as
// one EvPaste event on w's queue with Tag set to tag: its Text, or FlagFailed.
// It may come during the same Wait or Poll or a later one (Linux: once the
// owning app has sent it). A newer request replaces a pending one, whose
// answer then never arrives. The Text of an EvPaste stays valid like that of
// EvText: until the next Wait or Poll. Main thread.
func (w *Window) RequestPaste(tag uint64) { w.clipRequest(tag) }

// RequestPasteKinds asks for the first of files (a text/uri-list of file: URIs),
// image (image/png) and text, of the requested kinds, that the clipboard's
// owner offers (see paste.go). Like RequestPaste's, the answer arrives once
// the owner has sent the data; an owner that offers none of the requested
// kinds fails at once, one that stops sending fails after 2 s, and data over
// MaxPaste fails with FlagTooBig, without being kept. Only PNG images are
// understood. Main thread.
func (w *Window) RequestPasteKinds(tag uint64, kinds PasteKinds) { w.clipRequestKinds(tag, kinds) }
