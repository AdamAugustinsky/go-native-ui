package platform

import (
	"errors"
	"strings"

	"gonativeui/internal/uitypes"
)

// The clipboard contract (phase 5, D28; images and files: phase 6, S6).
// Copying hands the platform bytes it copies; pasting is a request whose
// answer arrives later as an event, because on Linux the data comes from the
// app that owns the clipboard, through the display server, and on Windows from
// whichever program owns it, and the UI thread must not wait for either.
//
// Each OS implements, in paste_<os>.go:
//
//	SetClipboardText(b []byte)
//	SetClipboardTextWith(b []byte, o ClipOptions)
//	SetClipboardImage(png []byte, o ...ClipOptions) error
//	(*Window).RequestPaste(tag uint64)
//	(*Window).RequestPasteKinds(tag uint64, kinds PasteKinds)
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
//
// RequestPasteKinds is RequestPaste for a mask of kinds (PasteKinds). The one
// EvPaste it answers with says which kind it carries in Event.Paste, and puts
// the data in the same buffer text uses (read it with Window.Text, valid until
// the next Wait or Poll):
//
//   - PasteText: UTF-8 text, as RequestPaste gives it;
//   - PasteImage: the bytes of a PNG file. Where the clipboard holds another
//     image format it is converted on a worker thread, never on the UI thread
//     (macOS: a TIFF, with ImageIO; Windows: a DIB, with image/png), so the
//     answer comes in a later Wait;
//   - PasteFiles: the paths of files, as UTF-8, each ended by a NUL byte
//     (the layout of a file dialog's paths), with Event.Code their number.
//     Other URLs and non-file items are left out.
//
// When the clipboard holds several of the requested kinds the first of files,
// image, text wins: a file copied in a file manager often comes with its name
// as text and an icon as an image, and an image copied from a page with its
// alternate text. Ask for the kinds the field can use, not for all of them.
// With nothing of the requested kinds the answer is FlagFailed; with a
// requested kind that is there but over MaxPaste, the next requested kind is
// tried and, if there is none, the answer is FlagFailed|FlagTooBig and
// nothing was copied (an image is refused by its size, before any of it
// reaches the Go heap, and the conversion of an image the platform has to
// convert is bounded as well: see MaxPaste).
//
// SetClipboardImage replaces the system clipboard with the PNG file png (a
// copy is made; png may be reused after the call). It returns ErrNotPNG when
// png is not a complete PNG and ErrTooLarge when it is over MaxPaste; the
// clipboard is not touched then. Windows also offers the image as CF_DIBV5,
// made on the clipboard worker, for programs that do not read PNG. ClipOptions
// with Concealed marks what is copied as a secret that clipboard history and
// sync services should not keep (see ClipOptions).

// MaxPaste is the most data a paste delivers, in bytes, of any kind: the text,
// the PNG file or the paths (with their NULs). More fails (FlagFailed, with
// FlagTooBig), and nothing of it is kept.
//
// Data the platform has to convert to PNG is read and converted within bounds
// as well: a source over 8*MaxPaste bytes, or one that declares more than 32
// million pixels, is refused without being decoded.
const MaxPaste = uitypes.MaxPaste

// maxClipSource is the largest image in another format (a TIFF, a DIB) that is
// converted to PNG. The PNG that comes out is limited by MaxPaste.
const maxClipSource = 8 * MaxPaste

// maxClipPixels is the most pixels a conversion decodes or encodes: 128 MiB of
// RGBA.
const maxClipPixels = 32 << 20

// PasteKind says what an EvPaste carries (Event.Paste).
type PasteKind uint8

// The kinds of data a paste carries. PasteText is the zero value, so an
// EvPaste from RequestPaste (and every other event) has it.
const (
	PasteText  PasteKind = iota // UTF-8 text
	PasteImage                  // the bytes of a PNG file
	PasteFiles                  // file paths, each ended by a NUL byte; Event.Code is their number
)

// PasteKinds is a mask of the kinds of data RequestPasteKinds accepts.
type PasteKinds uint8

// The bits of a PasteKinds mask.
const (
	WantText  PasteKinds = 1 << iota // plain text
	WantImage                        // an image, delivered as PNG
	WantFiles                        // file paths
)

// Has reports whether the mask includes kind.
func (k PasteKinds) Has(kind PasteKind) bool { return k&(1<<kind) != 0 }

// ClipOptions qualify what is copied (SetClipboardTextWith, SetClipboardImage).
type ClipOptions struct {
	// Concealed marks the copy as a secret that clipboard managers and
	// history and sync services should not record or show:
	//
	//   - macOS adds the pasteboard types org.nspasteboard.ConcealedType
	//     (this is a secret; managers must not store it or show it in clear)
	//     and org.nspasteboard.TransientType (do not keep it in history at
	//     all). The first is what a password manager sets; the second makes
	//     the managers that only look at that one (several do) skip it too,
	//     and costs nothing, since a secret on the clipboard is not meant to
	//     outlive its use.
	//   - Windows registers ExcludeClipboardContentFromMonitorProcessing,
	//     CanIncludeInClipboardHistory = 0 and CanUploadToCloudClipboard = 0.
	//   - Linux (X11 and Wayland) offers x-kde-passwordManagerHint with the
	//     data "secret", which KDE's Klipper and the managers that follow
	//     it honour.
	//
	// The data itself is still pasteable by any program: the flag is a hint to
	// the ones that keep a history.
	Concealed bool
}

func clipOpts(o []ClipOptions) (c ClipOptions) {
	if len(o) > 0 {
		c = o[0]
	}
	return c
}

// Errors of SetClipboardImage.
var (
	// ErrNotPNG is returned by SetClipboardImage for data that is not a
	// complete PNG file.
	ErrNotPNG = errors.New("platform: clipboard image is not a PNG")
	// ErrTooLarge is returned by SetClipboardImage for a PNG over MaxPaste.
	ErrTooLarge = errors.New("platform: clipboard image is over MaxPaste")
	// ErrNoClipboard is returned by SetClipboardImage on a system where the
	// clipboard belongs to the window (Windows, Linux) when none is open.
	ErrNoClipboard = errors.New("platform: no open window to own the clipboard")
)

// checkPNG validates png for SetClipboardImage and returns its length without
// anything after the IEND chunk.
func checkPNG(png []byte) (int, error) {
	if len(png) > MaxPaste+(1<<12) {
		return 0, ErrTooLarge
	}
	n := pngEnd(png)
	if n == 0 {
		return 0, ErrNotPNG
	}
	if n > MaxPaste {
		return 0, ErrTooLarge
	}
	return n, nil
}

// PasteFilePaths splits the data of a PasteFiles event (see RequestPasteKinds)
// into its paths. It allocates; a file paste is rare.
func PasteFilePaths(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}
