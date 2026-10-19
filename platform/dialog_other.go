//go:build !darwin

package platform

// Native alerts and file panels exist on macOS only so far (dialog_darwin.go).
// Elsewhere the calls return ErrUnsupported and the app shows its drawn dialog
// (widget.Dialog). A native Windows TaskDialogIndirect or IFileDialog is not
// built: both need COM vtable calls without cgo, which is not cheap.

// NativeDialogs reports whether the OS has native alert and file panel sheets:
// false here.
const NativeDialogs = false

// Alert is not available here: it returns ErrUnsupported.
func (w *Window) Alert(AlertSpec) (DialogTicket, error) { return 0, ErrUnsupported }

// OpenPanel is not available here: it returns ErrUnsupported.
func (w *Window) OpenPanel(OpenSpec) (DialogTicket, error) { return 0, ErrUnsupported }

// SavePanel is not available here: it returns ErrUnsupported.
func (w *Window) SavePanel(SaveSpec) (DialogTicket, error) { return 0, ErrUnsupported }

// CancelDialog does nothing here: no dialog is ever open.
func (w *Window) CancelDialog(DialogTicket) bool { return false }
