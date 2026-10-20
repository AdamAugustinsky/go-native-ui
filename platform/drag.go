package platform

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"
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
//	             Text its data, SelStart the number of items, X, Y where, Tag the drag's number.
//	             FlagFailed: the data could not be read or is over MaxPaste
//
// The data of an EvDrop is in the window's text buffer and valid like that of
// an EvPaste (until the next Wait or Poll), bounded like a paste (MaxPaste):
// for DragFiles the paths, each ended by a NUL byte; for DragImage a PNG
// (a TIFF, JPEG or HEIC image is converted on a worker, so that event may
// arrive in a later Wait); for DragText UTF-8. Of the kinds a drag offers, the
// one delivered is the first of files, image, text that the target accepts
// (DragAnswer.Accept).
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
// Where there is no native implementation (Windows and Linux so far,
// NativeDrag) the calls return ErrUnsupported and no events arrive.

// DragKinds is a set of kinds of dragged content.
type DragKinds uint8

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
type DragOp uint8

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
}

// DragHandler answers a drag over the window at (x, y) (physical pixels from
// the content's top-left) that offers the kinds in offered and whose source
// allows the operations in allowed. It runs on the main thread, inside the
// system's call, so it must be quick and must not call into the window.
type DragHandler func(x, y float32, offered DragKinds, allowed DragOp) DragAnswer

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
type FilePromise struct {
	// Name is the file name (with its extension) the destination creates.
	Name string
	// Type is the uniform type identifier of the file ("public.png",
	// "com.adobe.pdf"); empty is "public.data".
	Type string
	// Write creates the file at dest (a path in the destination folder;
	// write it fully, then return). It runs on a worker goroutine of its own.
	// ctx is cancelled when the window closes or after Timeout; a writer that
	// outlives it is answered for already, and its result is ignored. A nil
	// error completes the promise, any other error fails it.
	Write func(ctx context.Context, dest string) error
	// Timeout bounds Write; zero means DefaultPromiseTimeout.
	Timeout time.Duration
}

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
	// implementation yet (Windows, Linux); errors.Is(err, errors.ErrUnsupported)
	// also holds.
	ErrDragUnsupported = fmt.Errorf("platform: drag and drop on %s: %w", runtime.GOOS, errors.ErrUnsupported)
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
			if it.Promise.Name == "" || it.Promise.Write == nil {
				return errors.Join(ErrDragSpec, errors.New("a promise needs a name and a writer"))
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
