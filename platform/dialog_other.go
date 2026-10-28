//go:build !darwin && !windows

package platform

// Native alerts and file panels exist on macOS (dialog_darwin.go), and native
// file panels on Windows (dialog_windows.go). Here the calls return
// ErrUnsupported and the app shows its drawn dialog (widget.Dialog).

// NativeDialogs reports whether the OS has native alerts: false here.
const NativeDialogs = false

// NativeFilePanels reports whether OpenPanel and SavePanel are native: false
// here.
const NativeFilePanels = false

// Alert is not available here: it returns ErrUnsupported.
func (w *Window) Alert(AlertSpec) (DialogTicket, error) { return 0, ErrUnsupported }

// OpenPanel is not available here: it returns ErrUnsupported.
func (w *Window) OpenPanel(OpenSpec) (DialogTicket, error) { return 0, ErrUnsupported }

// SavePanel is not available here: it returns ErrUnsupported.
func (w *Window) SavePanel(SaveSpec) (DialogTicket, error) { return 0, ErrUnsupported }

// CancelDialog does nothing here: no dialog is ever open.
func (w *Window) CancelDialog(DialogTicket) bool { return false }
