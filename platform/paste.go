package platform

// The clipboard contract (phase 5, D28). Copying hands the platform bytes it
// copies; pasting is a request whose answer arrives later as an event, because
// on Linux the text comes from the app that owns the clipboard, through the
// display server, and the UI thread must not wait for it.
//
// Each OS implements, in paste_<os>.go:
//
//	SetClipboardText(b []byte)
//	(*Window).RequestPaste(tag uint64)
//
// SetClipboardText replaces the system clipboard with a copy of the plain
// text b (UTF-8; invalid sequences become U+FFFD); b may be reused after the
// call. RequestPaste asks for the plain text on the clipboard: the answer
// arrives as one EvPaste event on w's queue with Tag set to tag, carrying its
// Text or FlagFailed. It may come during the same Wait or Poll or a later one
// (Linux: once the owning app has sent it). A newer request replaces a
// pending one, whose answer then never arrives. The Text of an EvPaste stays
// valid like that of EvText: until the next Wait or Poll. Both run on the main
// thread.

// MaxPaste is the most text a paste delivers, in bytes; more fails
// (FlagFailed).
const MaxPaste = 16 << 20
