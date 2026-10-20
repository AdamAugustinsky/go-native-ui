package catalog

import (
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The D7b samples: the chat composer in each of its states (empty, typing,
// with attachments, sending, disabled, with the completion list), which has no
// AppKit counterpart to measure against and so joins the D7a page (d7aPage)
// for the eye, the layout audit and the allocation test. The composer is drawn
// as an offscreen window draws it: the single-line field; a window on macOS
// hosts an NSTextView in its place.

// composerSample is one composer on the page.
type composerSample struct {
	text    string
	files   []rexui.ComposerAttachment
	sending bool
	list    bool
	look    base.Look
	off     bool
}

var composerFiles = []rexui.ComposerAttachment{{Name: "notes.pdf"}, {Name: "Screenshot 2026-10-20 at 10.41.03.png"}}

var composerSamples = [...]composerSample{
	{},
	{text: "Lunch at noon? I booked the place by the river", look: base.LookFocused},
	{text: "Here are the files", files: composerFiles},
	{text: "Here are the files", files: composerFiles[:1], sending: true},
	{text: "Can't send right now", off: true},
	{text: "Ask @a", look: base.LookFocused, list: true},
	{text: "Inactive window", look: base.LookInactive | base.LookFocused},
}

// d7bState is what the composer samples edit.
type d7bState struct {
	made  bool
	state [len(composerSamples)]*rexui.ComposerState
	comp  rexui.CompletionState
}

func (d *d7bState) make() {
	if d.made {
		return
	}
	d.made = true
	for i := range d.state {
		st, _ := rexui.NewComposer(nil, rexui.ComposerSetup{Placeholder: "Message"})
		st.SetText([]byte(composerSamples[i].text))
		d.state[i] = st
	}
	d.comp.Reset(len(d7aItems), true)
}

func composer(i int) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		s.d7b.make()
		d, cs := &s.d7b, &composerSamples[i]
		k := s.k
		k.BeginDisabled(cs.off)
		k.Force(cs.look)
		o := rexui.ComposerOpts{Attachments: cs.files, Sending: cs.sending, Still: true}
		if cs.list {
			o.Completion, o.Items = &d.comp, d7aItems
		}
		k.NextWidth(k.Fixed(380))
		id := rexui.Composer(k, "Message", d.state[i], o).ID
		k.EndDisabled()
		return id
	}
}

var d7bSamples = []Sample{
	{"composer", 380, 36, composer(0), skip("designed: no AppKit control")},
	{"composer-typing", 380, 36, composer(1), skip("designed: no AppKit control")},
	{"composer-attachments", 380, 68, composer(2), skip("designed: no AppKit control")},
	{"composer-sending", 380, 68, composer(3), skip("designed: no AppKit control")},
	{"composer-disabled", 380, 36, composer(4), skip("designed: no AppKit control")},
	{"composer-completion", 380, 150, sized(380, 150, func(s *State) { s.k.Spacer(); composer(5)(s) }), skip("designed: no AppKit control")},
	{"composer-inactive", 380, 36, composer(6), skip("designed: no AppKit control")},
}

func init() {
	Samples = append(Samples, d7bSamples...)
}
