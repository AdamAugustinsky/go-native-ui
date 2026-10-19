package platform

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// Native alerts and file panels (phase 6, N2, D41).
//
// On macOS an alert is an NSAlert sheet and a file panel an NSOpenPanel or
// NSSavePanel sheet, attached to the window and started with
// beginSheetModalForWindow:completionHandler: (never runModal, which would
// starve the event loop). Everywhere else these calls return ErrUnsupported
// and the app falls back to its drawn dialog (widget.Dialog).
//
// The three calls return a DialogTicket at once. The answer arrives later, as
// one EvDialog event on the window's queue with Tag set to the ticket;
// Window.DialogResult decodes it. The calls run on the main thread at a safe
// point: between frames, from the app's own code, never from inside an AppKit
// callback. One dialog can be open per window at a time (ErrDialogBusy).
//
// Exactly one result per ticket. The sheet ends in one of four ways:
//
//   - the user answers: DialogAnswered, with the button or the paths;
//   - the user cancels a panel: DialogCancelled;
//   - the window closes under it (Window.Close): the sheet is first ended with
//     NSModalResponseAbort, because AppKit calls the completion 0 times when
//     a window closes under a sheet, and the app gets DialogCancelled once;
//   - the app withdraws it (Window.CancelDialog): no result at all.
//
// While a dialog is open on a window, the window's key, pointer and scroll
// events belong to the sheet: they are not queued for the app.
//
// Hand-over to F3 (the application object): EvDialog is the stand-in for the
// plan's EvService alert and panel results. F3 keeps the same shape: a
// ticket, an outcome, the button index and suppression flag, and the paths
// as owned strings, delivered on the app's own queue with the window's id.
// The three calls and DialogResult keep their meaning; only the receiver
// (the application, with a window argument) and the event class change. A
// result for a window that closed arrives through the application, not the
// closed window's queue.

// ErrUnsupported is returned by the native dialog calls on systems that do
// not have them yet; errors.Is(err, errors.ErrUnsupported) also holds. The
// app then shows its drawn dialog.
var ErrUnsupported = fmt.Errorf("platform: native dialogs on %s: %w", runtime.GOOS, errors.ErrUnsupported)

// ErrDialogBusy is returned when the window already has a dialog open, or
// too many dialogs are open at once.
var ErrDialogBusy = errors.New("platform: a dialog is already open")

// ErrDialogSpec is returned for a spec the call cannot honour (no buttons, too many
// buttons, no known content type, a window that is closed).
var ErrDialogSpec = errors.New("platform: invalid dialog spec")

// DialogTicket identifies one open dialog; it is the Tag of its EvDialog.
// Tickets are never 0 and never repeat in a process.
type DialogTicket uint64

// DialogKind says which call a result answers.
type DialogKind uint8

const (
	// DialogAlert is the answer to Alert.
	DialogAlert DialogKind = iota
	// DialogOpen is the answer to OpenPanel.
	DialogOpen
	// DialogSave is the answer to SavePanel.
	DialogSave
)

// DialogOutcome says how a dialog ended.
type DialogOutcome uint8

const (
	// DialogAnswered: the user chose a button (alert) or confirmed the panel.
	DialogAnswered DialogOutcome = iota
	// DialogCancelled: the panel was cancelled, or the sheet was aborted
	// because its window closed.
	DialogCancelled
)

// AlertStyle is the icon and tone of an alert.
type AlertStyle uint8

const (
	// AlertInformational is the plain alert with the application icon.
	AlertInformational AlertStyle = iota
	// AlertWarning is an alert with a caution badge.
	AlertWarning
	// AlertCritical is an alert with a stop badge; use it for destructive
	// consequences the user cannot undo.
	AlertCritical
)

// MaxAlertButtons is the most buttons an alert takes.
const MaxAlertButtons = 8

// AlertSpec describes an alert sheet.
type AlertSpec struct {
	// Style is the icon: informational, warning or critical.
	Style AlertStyle
	// Message is the bold first line; Info the smaller text under it.
	Message, Info string
	// Buttons are the button titles in AppKit's order: the first is the
	// default button (Return) and sits rightmost, the next ones to its left. At
	// least one. AppKit makes Escape press a button titled "Cancel".
	Buttons []string
	// Destructive lists the indexes of buttons that destroy something
	// (hasDestructiveAction: the system tints them).
	Destructive []int
	// Suppression is the title of a checkbox such as "Do not show this
	// message again"; empty shows none. Its state comes back in the result.
	Suppression string
}

// OpenSpec describes an open panel sheet.
type OpenSpec struct {
	// Files and Dirs say what can be chosen; with neither set, files.
	Files, Dirs bool
	// Multiple allows more than one choice.
	Multiple bool
	// Types limits the files to content types. Each entry is a UTType
	// identifier ("public.png", "com.adobe.pdf") or, with a leading dot, a
	// filename extension (".png"). Empty allows every file.
	Types []string
	// Start is the directory the panel opens in; empty is the system's choice.
	Start string
	// Title, Message and Prompt are the optional texts of the panel: the
	// title, the line above the browser, the confirm button's title.
	Title, Message, Prompt string
}

// SaveSpec describes a save panel sheet.
type SaveSpec struct {
	// Name is the suggested file name.
	Name string
	// Types limits the formats, like OpenSpec.Types; the first is the
	// default extension.
	Types []string
	// Start is the directory the panel opens in; empty is the system's choice.
	Start string
	// Title, Message and Prompt are the optional texts of the panel.
	Title, Message, Prompt string
}

// DialogResult is the decoded answer of one dialog.
type DialogResult struct {
	// Ticket is the ticket the call returned.
	Ticket DialogTicket
	// Kind says which call it answers.
	Kind DialogKind
	// Outcome says how the dialog ended.
	Outcome DialogOutcome
	// Button is the index in AlertSpec.Buttons of the button pressed, -1 when
	// the dialog was cancelled or is not an alert.
	Button int
	// Suppressed is the state of the alert's suppression checkbox.
	Suppressed bool
	// Paths are the chosen files or folders, as absolute paths; empty unless
	// a panel was answered. They are owned by the caller.
	Paths []string
}

// The layout of an EvDialog: Tag is the ticket; Code the button index of an
// alert or the number of paths of a panel; SelStart holds the kind (bits 0-7),
// the outcome (bits 8-15) and the suppression checkbox (bit 16); Text the
// paths, each ended by a NUL byte. The fields are the ones EvPaste and EvText
// leave free, so the event stays pointer-free and fixed-size.

const dialogSuppressed = 1 << 16

// dialogEvent builds an EvDialog; the paths (already NUL-ended) are in the
// queue's text buffer at off, n long.
func dialogEvent(t DialogTicket, kind DialogKind, out DialogOutcome, code uint32, suppressed bool, off, n int, at int64) Event {
	w := int32(kind) | int32(out)<<8
	if suppressed {
		w |= dialogSuppressed
	}
	return Event{Kind: EvDialog, Tag: uint64(t), Code: code, SelStart: w, Text: TextRef{uint32(off), uint32(n)}, Time: at}
}

// DialogResult decodes an EvDialog event of this window. It allocates the
// path strings; a dialog result is rare.
func (w *Window) DialogResult(e Event) DialogResult {
	r := DialogResult{
		Ticket:     DialogTicket(e.Tag),
		Kind:       DialogKind(e.SelStart & 0xff),
		Outcome:    DialogOutcome(e.SelStart >> 8 & 0xff),
		Suppressed: e.SelStart&dialogSuppressed != 0,
		Button:     -1,
	}
	if r.Kind == DialogAlert {
		if r.Outcome == DialogAnswered {
			r.Button = int(e.Code)
		}
		return r
	}
	if e.Text.Len > 0 {
		r.Paths = strings.Split(strings.TrimSuffix(string(w.Text(e.Text)), "\x00"), "\x00")
	}
	return r
}

// check reports whether an alert spec can be shown.
func (s *AlertSpec) check() error {
	if len(s.Buttons) == 0 || len(s.Buttons) > MaxAlertButtons {
		return fmt.Errorf("%w: %d buttons (want 1 to %d)", ErrDialogSpec, len(s.Buttons), MaxAlertButtons)
	}
	for _, d := range s.Destructive {
		if d < 0 || d >= len(s.Buttons) {
			return fmt.Errorf("%w: destructive button %d of %d", ErrDialogSpec, d, len(s.Buttons))
		}
	}
	return nil
}
