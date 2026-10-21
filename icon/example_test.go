package icon_test

import (
	"fmt"

	"gonativeui/icon"
	"gonativeui/internal/fakeui"
)

// An icon is a Spec: the SF Symbol on macOS, a Segoe codepoint on Windows and
// a glyph of the system font where neither exists, so every platform draws
// something in the same place. An app adds its own after the standard ones.
// (In an app, widget.Kit owns the atlas: Kit.AddIcon adds to it and
// Kit.Icons returns it.)
func Example() {
	atlas := icon.New(fakeui.New()) // a.Text in an app: the text system
	star := atlas.Add(icon.Spec{Symbol: "star.fill", Segoe: 0xE735, Fallback: '★', Pt: 16})
	fmt.Println(star == icon.NumStandard, atlas.Len() == int(icon.NumStandard)+1)
	// Output: true true
}

// An icon beside text takes the size, weight and scale of the text's style,
// as NSImage's symbol configuration does.
func ExampleSpec_In() {
	s := icon.Spec{Symbol: "paperplane", Fallback: '➤'}.In(icon.StyleBody, icon.ScaleLarge)
	fmt.Println(s.Pt > 0, s.Scale == icon.ScaleLarge)
	// Output: true true
}
