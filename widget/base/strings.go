package base

// Strings are the accessible names the primitives give the boxes the app does
// not name: the scroll bars and a splitter's dividers. English is the
// default; rexui.SetStrings sets these with the rest of rexui's words, and an
// app on package widget alone calls SetStrings.
type Strings struct {
	// HorizontalScrollBar and VerticalScrollBar name the scroll bars.
	HorizontalScrollBar, VerticalScrollBar string
	// Divider names a splitter's divider.
	Divider string
}

// StringsEnglish are the default Strings.
var StringsEnglish = Strings{
	HorizontalScrollBar: "Horizontal scroll bar",
	VerticalScrollBar:   "Vertical scroll bar",
	Divider:             "Divider",
}

// words are the Strings in use; the primitives read them every frame.
var words = StringsEnglish

// SetStrings makes s the Strings every kit uses; a field left empty keeps
// its English. Call it before the first frame, or between frames on the UI
// goroutine.
func SetStrings(s Strings) {
	orDefault(&s.HorizontalScrollBar, StringsEnglish.HorizontalScrollBar)
	orDefault(&s.VerticalScrollBar, StringsEnglish.VerticalScrollBar)
	orDefault(&s.Divider, StringsEnglish.Divider)
	words = s
}

// CurrentStrings returns the Strings in use.
func CurrentStrings() Strings { return words }

func orDefault(s *string, def string) {
	if *s == "" {
		*s = def
	}
}
