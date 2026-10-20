//go:build !darwin

package platform

// editorNative is empty where there is no native editor: NewEditor refuses.
type editorNative struct{}

// NewEditor creates a native multi-line editor as a host of the window. Only
// macOS has one (editor_darwin.go); here it returns ErrNoHosts.
func (w *Window) NewEditor(EditorOptions) (*Editor, error) { return nil, ErrNoHosts }

// Host is the editor's host, for ui.Context.Host.
func (e *Editor) Host() HostID { return 0 }

// View is the editor's NSTextView* (0 here).
func (e *Editor) View() uintptr { return 0 }

// Release does nothing here.
func (e *Editor) Release() {}

// Focused reports whether the editor has the keyboard focus.
func (e *Editor) Focused() bool { return false }

// Focus gives the editor the keyboard focus.
func (e *Editor) Focus() {}

// SetText replaces the whole text.
func (e *Editor) SetText([]byte) error { return ErrNoHosts }

// SetSelection sets the selection.
func (e *Editor) SetSelection(TextSelection) error { return ErrNoHosts }

// Replace replaces a range as a user edit.
func (e *Editor) Replace(TextRange, []byte, TextRange) error { return ErrNoHosts }

// SetSpans styles ranges of the text.
func (e *Editor) SetSpans([]TextSpan) error { return ErrNoHosts }

// SetAutocorrect turns automatic spelling correction on or off.
func (e *Editor) SetAutocorrect(bool) {}

// CaretRect is where the caret is.
func (e *Editor) CaretRect() ([4]float32, bool) { return [4]float32{}, false }

func (e *Editor) sync() {}
