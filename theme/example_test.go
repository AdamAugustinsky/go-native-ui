package theme_test

import (
	"fmt"

	"gonativeui/internal/fakeui"
	"gonativeui/theme"
	"gonativeui/theme/rex"
)

// Colours are 0xRRGGBBAA values. Contrast is the WCAG ratio, for checking a
// pair of colours before an app uses it.
func Example() {
	black, white := theme.Col(0x000000), theme.Col(0xFFFFFF)
	fmt.Printf("%.0f:1\n", theme.Contrast(black, white))
	fmt.Printf("%08X\n", theme.MixRGB(0x000000, 0xFFFFFF, 0.5)<<8|0xFF)
	// Output:
	// 21:1
	// 808080FF
}

// Resolving a theme for a window: the tokens in points become pixels at the
// window's scale, and the fonts are created (so it runs on the thread that owns
// the text system, once per window and again when the scale changes). An app
// that uses widget.Kit never calls it: the Kit does.
func ExampleTheme_Resolve() {
	t := rex.Theme() // a copy: change a token before resolving to derive a look
	t.Name = "Brand"
	t.Light = t.Light.WithAccent(theme.Col(0x7C3AED)) // a brand accent
	t.Dark = t.Dark.WithAccent(theme.Col(0xA78BFA))
	r := t.Resolve(fakeui.New(), 2, theme.Light)
	h := r.Sizes[theme.SizeRegular].Height
	fmt.Println(r.Name, r.Scale, h/r.Scale, "pt buttons")
	// Output: Brand 2 24 pt buttons
}
