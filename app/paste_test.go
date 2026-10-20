package app

import (
	"bytes"
	"testing"

	"gonativeui/platform"
)

// An image or file answer is kept for TakePaste, once, and a failure says
// whether the data was there but too big; the data is the App's own copy.
func TestMediaPasteSlot(t *testing.T) {
	a := &App{appObject: &appObject{}}
	if _, ok := a.TakePaste(); ok {
		t.Fatal("a paste out of nothing")
	}
	png := []byte("\x89PNG\r\n\x1a\nimage bytes")
	src := append([]byte(nil), png...)
	a.onMediaPaste(&platform.Event{Kind: platform.EvPaste, Paste: platform.PasteImage, Tag: 7}, src)
	if !a.p.needDraw {
		t.Error("an answer does not ask for a frame")
	}
	src[0] = 0 // the platform reuses its buffer
	p, ok := a.TakePaste()
	if !ok || p.Tag != 7 || p.Kind != platform.PasteImage || p.Failed || !bytes.Equal(p.Data, png) {
		t.Fatalf("image: %+v %v", p, ok)
	}
	if _, ok := a.TakePaste(); ok {
		t.Error("an answer is taken twice")
	}

	a.onMediaPaste(&platform.Event{Kind: platform.EvPaste, Paste: platform.PasteFiles, Tag: 8, Code: 2}, []byte("/a\x00/b c\x00"))
	p, _ = a.TakePaste()
	if got := platform.PasteFilePaths(p.Data); p.Files != 2 || len(got) != 2 || got[1] != "/b c" {
		t.Errorf("files: %+v %q", p, got)
	}

	a.onMediaPaste(&platform.Event{Kind: platform.EvPaste, Flags: platform.FlagFailed | platform.FlagTooBig, Tag: 9}, nil)
	if p, _ = a.TakePaste(); !p.Failed || !p.TooBig || p.Data != nil || p.Tag != 9 {
		t.Errorf("too big: %+v", p)
	}
	a.onMediaPaste(&platform.Event{Kind: platform.EvPaste, Flags: platform.FlagFailed, Tag: 10}, nil)
	if p, _ = a.TakePaste(); !p.Failed || p.TooBig {
		t.Errorf("failed: %+v", p)
	}

	// Repeated pastes of one size allocate only the first time.
	big := make([]byte, 1<<20)
	ev := &platform.Event{Kind: platform.EvPaste, Paste: platform.PasteImage}
	a.onMediaPaste(ev, big)
	if n := testing.AllocsPerRun(20, func() {
		a.onMediaPaste(ev, big)
		a.TakePaste()
	}); n != 0 {
		t.Errorf("%v allocations per image paste", n)
	}
}

// Only the answers to RequestPaste are the App's; a text field's paste goes on
// to the field.
func TestIsMediaPaste(t *testing.T) {
	var s pasteSlot
	text := &platform.Event{Kind: platform.EvPaste, Tag: 5}
	if s.isMedia(text) {
		t.Error("a text field's paste is not media")
	}
	if !s.isMedia(&platform.Event{Kind: platform.EvPaste, Paste: platform.PasteFiles, Tag: 5}) {
		t.Error("files are media")
	}
	s.asked, s.tag = true, 5
	if !s.isMedia(text) {
		t.Error("the answer to RequestPaste (even text, even a failure) is the App's")
	}
	if s.isMedia(&platform.Event{Kind: platform.EvPaste, Tag: 6}) {
		t.Error("another tag is a field's")
	}
}
