package platform

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"gonativeui/internal/uitypes"
)

// Drag and drop (phase 6, N5).
//
// Drag in. A window that has called SetDropKinds receives drags that offer
// files, an image or text, and reports them as events, with the pointer
// position in physical pixels from the content's top-left:
//
//	EvDragEnter  a drag came over the window: Code the kinds offered (DragKinds), SelStart the
//	             operations the source allows (DragOp), SelLen the number of items, Mods the
//	             modifier keys, Tag the drag's number (the same for all events of one drag)
//	EvDragMove   the same, as the pointer moves (also repeated while it is still)
//	EvDragLeave  the drag left the window, or was cancelled
//	EvDrop       the drag was dropped: Code the one kind delivered (a single DragKinds bit),
//	             Text its data, SelStart the number of items, X, Y where, Tag the Target of the
//	             answer that accepted it (0 when the handler named none).
//	             FlagFailed: the data could not be read, is over MaxPaste, or is an image over
//	             the limits (MaxImagePixels, MaxConvertBytes)
//
// The data of an EvDrop is in the window's text buffer and valid like that of
// an EvPaste (until the next Wait or Poll), bounded like a paste (MaxPaste):
// for DragFiles the paths, each ended by a NUL byte (a list that does not fit
// whole fails); for DragImage a PNG (a TIFF, JPEG or HEIC image is converted);
// for DragText UTF-8. Of the kinds a drag offers, the one delivered is the
// first of files, image, text that the target accepts (DragAnswer.Accept).
//
// The data is read on a worker goroutine while performDragOperation waits for it
// on the main thread, for up to dropSyncBudget (250 ms). A source that answers
// in that time (all but a lazy provider in another process) gives its EvDrop
// before the drop returns, as always; a slower one no longer holds the main
// thread, performDragOperation returns, and the EvDrop arrives in a later Wait
// when the data does. The pasteboard's items go stale when it changes owner
// (another drag can start first), so the worker compares the pasteboard's change
// count before and after its read: a read across a change is thrown away and the
// EvDrop is FlagFailed, never another owner's data. File URLs and text are read
// within their caps. An image is taken as the file its item names (Finder and
// Photos offer one) or as a copy of its encoded bytes (over MaxPaste is
// refused), and decoded to PNG by the same worker: its EvDrop arrives in a later
// Wait. An image is checked from its header before it is decoded: image 0 only,
// over MaxImagePixels pixels, or more decoded bytes than MaxConvertBytes, fails;
// the PNG it is encoded to is bounded by MaxPaste, the encoder failing past it.
// A drag from our own session (StartDrag) is answered from the spec's data and
// not through the pasteboard.
//
// A drag whose items are only file promises (NSFilePromiseReceiver: Mail
// attachments, Safari images, Photos exports) offers no kind unless
// EnableFilePromises is set: the promisers are then asked to write the files
// into a folder of ours (receivePromisedFilesAtDestination:) and the EvDrop
// (DragFiles) comes when they have, or after two minutes with the ones that did.
// A link (public.url), RTF and HTML are read as text.
//
// The system asks synchronously, at every enter and move, which operation the
// window answers, so that it can show the right cursor: the handler of
// SetDragHandler answers on the main thread, from the application's last
// finished frame, without waiting for the event loop. The events above are
// queued as well, for the application to follow the drag.
//
// Drag out. StartDrag starts a drag session from the press that is being held
// (the window keeps the last left-button press that reached the application)
// with file paths, text, a PNG image, or files that do not exist yet (a
// FilePromise, NSFilePromiseProvider on macOS). The system takes over the mouse
// then: no release ever reaches the application, so the caller cancels its own
// press (ui does). EvDragEnd reports the end: Tag the ticket StartDrag returned,
// Code the operation performed (DragOp, DragOpNone when cancelled).
//
// A FilePromise is written on a worker goroutine, never on the main thread, when
// the destination asks for the file; the completion is reported to the system
// exactly once, on success, on failure, when the application never answers
// (FilePromise.Timeout), and when the window closes.
//
// Per OS. macOS is AppKit's dragging protocols (drag_darwin.go; the files a
// drag promises are received by droppromise_darwin.go when EnableFilePromises
// is set, into a folder of the temporary directory, and delivered as DragFiles
// once written; a web link and rich text (RTF, HTML) are offered and delivered
// as DragText, droptext_darwin.go). Windows is OLE
// (drag_windows.go): drops through IDropTarget, from CF_HDROP, the "PNG" format
// or CF_DIBV5 and CF_DIB (converted on a worker, so its EvDrop comes in a later
// Wait), and CF_UNICODETEXT; drags out through DoDragDrop, which is modal, so
// StartDrag returns when the drag is over, with EvDragEnd already queued. Linux
// is XDND on X11 (dnd_x11_linux.go) and the wl_data_device protocol on Wayland
// (dnd_wl_linux.go, version 3 of the manager for a drag out; drag_linux.go has
// what the two share), with the gtk-dnd-drag-threshold setting for DragThreshold: files
// are text/uri-list, an image image/png, text UTF8_STRING and its relatives,
// read like a paste (INCR included), and a drag out proposes the action the
// modifier keys name. File promises (ErrPromiseUnsupported) and the drag
// picture (Preview) are macOS's only so far (receiving a promise is macOS's
// only too). Where there is no native
// implementation (NativeDrag false) the calls return ErrDragUnsupported and no
// events arrive.

// EnableFilePromises turns on the receiving of file promises: a drag that offers
// only promises is then offered as DragFiles, and its files are written into a
// folder of ours. Off by default, because AppKit throws (and the process ends) if
// receivePromisedFilesAtDestination: is called outside a real drag's
// performDragOperation:, and nothing in the tests can start a real drag from
// another application: the reception's bookkeeping is tested with the reader
// block called by hand, and the call into AppKit has been read, not run. Set it
// before the first window opens, then try a drag from Mail, Safari and Photos.
var EnableFilePromises bool

// DragKinds is a set of kinds of dragged content.
type DragKinds = uitypes.DragKinds

const (
	// DragFiles are file URLs (local files and folders).
	DragFiles DragKinds = 1 << iota
	// DragImage is image data.
	DragImage
	// DragText is plain text.
	DragText

	// DragAny is every kind.
	DragAny = DragFiles | DragImage | DragText
)

// DragOp is a drag operation, or a set of them where a set is meant (the
// operations a source allows, the ones a target can do). The zero value is
// no operation.
type DragOp = uitypes.DragOp

const (
	// DragOpNone is no operation: the drop is refused.
	DragOpNone DragOp = 0
	// DragOpCopy copies the dragged items.
	DragOpCopy DragOp = 1
	// DragOpLink makes a link or alias to them.
	DragOpLink DragOp = 2
	// DragOpMove moves them.
	DragOpMove DragOp = 4

	// DragOpAll is every operation.
	DragOpAll = DragOpCopy | DragOpLink | DragOpMove
)

// Pick returns the operation to answer when a target can do the operations of
// can and the source allows allowed: the first of copy, link and move in both,
// DragOpNone when there is none. A target that names none can copy.
func Pick(can, allowed DragOp) DragOp {
	if can == DragOpNone {
		can = DragOpCopy
	}
	both := can & allowed
	switch {
	case both&DragOpCopy != 0:
		return DragOpCopy
	case both&DragOpLink != 0:
		return DragOpLink
	case both&DragOpMove != 0:
		return DragOpMove
	}
	return DragOpNone
}

// DragAnswer is what the window answers to the system about a drag at a
// position.
type DragAnswer struct {
	// Op is the one operation: DragOpNone refuses the drop here.
	Op DragOp
	// Accept is the set of kinds the target at the position takes. The data
	// of a drop is the first of files, image and text in it that the drag
	// offered.
	Accept DragKinds
	// Target identifies the target that answered (ui: its box key); an
	// EvDrop carries it in Tag, so that data that arrives later goes to the
	// target that took the drop and to no other. 0 names none.
	Target uint64
}

// DragHandler answers a drag over the window at (x, y) (physical pixels from
// the content's top-left) that offers the kinds in offered and whose source
// allows the operations in allowed. It runs on the main thread, inside the
// system's call, so it must be quick and must not call into the window.
type DragHandler func(x, y float32, offered DragKinds, allowed DragOp) DragAnswer

// MaxImagePixels is the largest image, in pixels (width times height), that
// a dropped image or a decoder takes: a small compressed file can decode to
// gigabytes, so it is refused from its header. package img uses the same limit.
const MaxImagePixels = 100 << 20

// MaxConvertBytes is the most decoded bytes (four a pixel) a dropped image of
// a format other than PNG may need to be converted to PNG.
const MaxConvertBytes = 256 << 20

// MaxPromiseName is the longest file name of a FilePromise, in bytes.
const MaxPromiseName = 255

// MaxDragItems is the most items one StartDrag takes.
const MaxDragItems = 256

// DefaultPromiseTimeout is how long a FilePromise's writer may take when
// FilePromise.Timeout is zero.
const DefaultPromiseTimeout = 10 * time.Minute

// DragSpec describes a drag to start.
type DragSpec struct {
	// Items are what is dragged: at least one, at most MaxDragItems.
	Items []DragItem
	// Ops are the operations the source allows; zero means copy.
	Ops DragOp
	// Preview is the picture dragged along with the pointer; nil shows the
	// icon of a first item that is a file, and none otherwise.
	Preview *DragPreview
}

// DragItem is one dragged item: exactly one of its fields is set.
type DragItem struct {
	// Path is a file or folder that exists.
	Path string
	// Text is plain text.
	Text string
	// PNG is an image.
	PNG []byte
	// Promise is a file that is written when the destination asks for it.
	Promise *FilePromise
}

// FilePromise is a file that does not exist yet: media still to download, a
// document still to render. The destination (Finder) asks for it when the
// drop happens.
type FilePromise = uitypes.FilePromise

// DragPreview is the picture of a drag.
type DragPreview struct {
	// PNG is the image.
	PNG []byte
	// Rect is where the picture starts, in window pixels (x0, y0, x1, y1;
	// origin top left), usually the dragged box: it appears there and
	// follows the pointer from that offset.
	Rect [4]float32
}

// DragTicket identifies one drag out; it is the Tag of its EvDragEnd. It is
// never 0 and never repeats in a process.
type DragTicket uint64

var (
	// ErrDragUnsupported is returned by the drag calls where the OS has no
	// implementation (a build for another OS than macOS, Windows and Linux; on
	// Wayland, a compositor without version 3 of wl_data_device_manager);
	// errors.Is(err, errors.ErrUnsupported) also holds.
	ErrDragUnsupported = fmt.Errorf("platform: drag and drop on %s: %w", runtime.GOOS, errors.ErrUnsupported)
	// ErrPromiseUnsupported is returned by StartDrag for a spec with a
	// FilePromise where the OS has no file promises (everywhere but macOS so
	// far); errors.Is(err, errors.ErrUnsupported) also holds.
	ErrPromiseUnsupported = fmt.Errorf("platform: file promises on %s: %w", runtime.GOOS, errors.ErrUnsupported)
	// ErrNoPress is returned by StartDrag when no left-button press is being
	// held: a drag starts from a press that reached the application.
	ErrNoPress = errors.New("platform: no mouse press to start a drag from")
	// ErrDragBusy is returned by StartDrag while another drag out is running.
	ErrDragBusy = errors.New("platform: a drag is already in progress")
	// ErrDragRefused is returned by StartDrag when the system did not start the
	// session.
	ErrDragRefused = errors.New("platform: the system did not start the drag")
	// ErrDragSpec is returned by StartDrag for a spec it cannot honour (no
	// items, too many, an item that sets none or several of its fields, a
	// promise without a name or a writer, a window that is closed).
	ErrDragSpec = errors.New("platform: invalid drag spec")
)

// Validate reports whether the spec can be started; the error wraps
// ErrDragSpec.
func (s *DragSpec) Validate() error {
	if len(s.Items) == 0 || len(s.Items) > MaxDragItems {
		return errors.Join(ErrDragSpec, errors.New("1 to MaxDragItems items"))
	}
	for i := range s.Items {
		it := &s.Items[i]
		n := 0
		if it.Path != "" {
			n++
		}
		if it.Text != "" {
			n++
		}
		if len(it.PNG) > 0 {
			n++
		}
		if it.Promise != nil {
			n++
			if it.Promise.Write == nil {
				return errors.Join(ErrDragSpec, errors.New("a promise needs a writer"))
			}
			if err := validPromiseName(it.Promise.Name); err != nil {
				return errors.Join(ErrDragSpec, err)
			}
		}
		if n != 1 {
			return errors.Join(ErrDragSpec, errors.New("an item sets exactly one of Path, Text, PNG, Promise"))
		}
	}
	return nil
}

// The distance, in logical units, the pointer moves with the button held
// before a press becomes a drag (DragThreshold), where the program does not
// read the system's setting: macOS has no public query (4 points is the
// conventional value), Windows' default SM_CXDRAG is 4 pixels, and GTK's
// gtk-dnd-drag-threshold is 8.
const (
	dragThresholdMac     = 4
	dragThresholdWindows = 4
	dragThresholdLinux   = 8
)

// validPromiseName reports why name cannot be the file name of a promise: the
// destination creates it inside its own folder, so it must be a base name (not
// empty, ".", "..", or anything with a path separator), without NUL or ":"
// (the old path separator, which Finder shows as "/"), valid UTF-8, and short
// enough for a file system.
func validPromiseName(name string) error {
	switch {
	case name == "" || name == "." || name == "..":
		return errors.New("a promise needs a file name")
	case len(name) > MaxPromiseName:
		return errors.New("the promise's file name is too long")
	case !utf8.ValidString(name):
		return errors.New("the promise's file name is not valid UTF-8")
	case strings.ContainsAny(name, "/:\x00"):
		return errors.New("the promise's file name must be a base name without / : or NUL")
	}
	return nil
}
