package icon_test

import (
	"fmt"

	"gonativeui/icon"
	"gonativeui/internal/fakeui"
)

// An icon is a Spec: a Lucide icon by name, the same on every OS. An app
// adds its own after the standard ones. (In an app, widget.Kit owns the
// atlas: Kit.AddIcon adds to it and Kit.Icons returns it.)
func Example() {
	atlas := icon.New(fakeui.New()) // a.Text in an app: the text system
	folder := atlas.Add(icon.Lucide("folder-open"), icon.Lucide("x").Small())
	fmt.Println(folder == icon.NumStandard, atlas.Len() == int(icon.NumStandard)+2)
	// Output: true true
}

// An app that wants a platform's own symbol names it: the SF Symbol on
// macOS, a Segoe codepoint on Windows and a glyph of the system font where
// neither exists.
func ExampleSpec_system() {
	s := icon.Spec{Symbol: "star.fill", Segoe: 0xE735, Fallback: '★', Pt: 16}
	fmt.Println(s.Lucide == "", s.Symbol)
	// Output: true star.fill
}

// An icon beside text takes the size, weight and scale of the text's style,
// as NSImage's symbol configuration does.
func ExampleSpec_In() {
	s := icon.Lucide("send").In(icon.StyleBody, icon.ScaleMedium)
	fmt.Println(s.Pt)
	sym := icon.Spec{Symbol: "paperplane", Fallback: '➤'}.In(icon.StyleBody, icon.ScaleLarge)
	fmt.Println(sym.Pt > 0, sym.Scale == icon.ScaleLarge)
	// Output:
	// 16
	// true true
}
