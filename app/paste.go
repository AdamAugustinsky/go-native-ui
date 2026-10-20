package app

import "gonativeui/platform"

// Pasting images and files (platform.Window.RequestPasteKinds). Text pastes go
// to the focused text field through ui.Input; an image or a list of files has
// no field to take it, so the app asks for it and reads the answer from a
// frame, like TakeDialog's:
//
//	a.RequestPaste(tag, platform.WantImage|platform.WantFiles)
//	...
//	if p, ok := a.TakePaste(); ok && !p.Failed { use(p.Kind, p.Data) }

// Paste is the answer to RequestPaste.
type Paste struct {
	// Tag is the tag RequestPaste was called with.
	Tag uint64
	// Kind says what Data is: platform.PasteImage (the bytes of a PNG file),
	// platform.PasteFiles (file paths, each ended by a NUL byte;
	// platform.PasteFilePaths splits them) or, when the request asked for text
	// too and the clipboard has nothing better, platform.PasteText.
	Kind platform.PasteKind
	// Data is kept by the App between frames (up to platform.MaxPaste) and is
	// valid until the next paste arrives: copy what must outlive it.
	Data []byte
	// Files is the number of paths in Data for platform.PasteFiles.
	Files int
	// Failed is set when the clipboard holds none of the requested kinds (or
	// the transfer failed), with no Data. TooBig says it did hold one, but over
	// platform.MaxPaste.
	Failed, TooBig bool
}

// pasteSlot is the newest answer not taken yet.
type pasteSlot struct {
	have  bool
	asked bool   // RequestPaste is waiting for its answer
	tag   uint64 // and this is its tag
	p     Paste
	buf   []byte
}

// isMedia reports whether the EvPaste e answers RequestPaste (not a text
// field's request): it carries an image or files, or has the tag of the
// pending request.
func (s *pasteSlot) isMedia(e *platform.Event) bool {
	return e.Paste != platform.PasteText || s.asked && e.Tag == s.tag
}

// RequestPaste asks the system clipboard for the first of files, image and text,
// of the kinds in the mask, that it holds. The answer comes back through
// TakePaste in a later frame. (A text field's own paste, the paste chord, is not
// this: it asks for text and gets it through ui.Input.)
// Use a tag no text field's key is. A newer request replaces a pending one.
// Ignored without a window.
func (a *App) RequestPaste(tag uint64, kinds platform.PasteKinds) {
	if a.Win == nil {
		return
	}
	a.paste.have, a.paste.asked, a.paste.tag = false, true, tag
	a.Win.RequestPasteKinds(tag, kinds)
}

// TakePaste returns the answer to RequestPaste, and false
// when there is none. It reports each answer once.
func (a *App) TakePaste() (Paste, bool) {
	if !a.paste.have {
		return Paste{}, false
	}
	a.paste.have = false
	return a.paste.p, true
}

// onMediaPaste stores the EvPaste that answers RequestPaste (data or failure) for
// TakePaste, and asks for a frame. The event's data is copied into the App's own buffer
// (platform.MaxPaste at most, sized exactly, so repeated pastes of a size allocate
// only the first time).
func (a *App) onMediaPaste(e *platform.Event, data []byte) {
	s := &a.paste
	s.asked = false
	p := Paste{Tag: e.Tag, Kind: e.Paste}
	if e.Flags&platform.FlagFailed != 0 {
		p.Failed, p.TooBig = true, e.Flags&platform.FlagTooBig != 0
	} else {
		if cap(s.buf) < len(data) {
			s.buf = make([]byte, 0, len(data))
		}
		s.buf = append(s.buf[:0], data...)
		p.Data, p.Files = s.buf, int(e.Code)
	}
	s.have, s.p = true, p
	a.p.invalidate()
}
