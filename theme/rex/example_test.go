package rex_test

import (
	"fmt"

	"gonativeui/theme"
	"gonativeui/theme/rex"
)

// Rex is the default look. Theme returns a copy, so an app derives its own by
// changing tokens before the kit is made (widget.NewKit takes the theme), and
// every component follows. The palette keeps body text at a contrast of at
// least 7:1 against its surfaces.
func Example() {
	t := rex.Theme()
	fmt.Println(t.Name)
	fmt.Println(theme.Contrast(t.Light.Text, t.Light.Surface) >= 7)

	brand := rex.Theme() // widget.NewKit(a.UI, a.Text, brand)
	brand.Light = brand.Light.WithAccent(theme.Col(0x0A7D5A))
	brand.Dark = brand.Dark.WithAccent(theme.Col(0x34D399))
	fmt.Println(brand.Light.Accent != t.Light.Accent)
	// Output:
	// Rex
	// true
	// true
}

// The terminal-style colours of the reference, for a pane whose focus
// animation stands at focus (0 dimmed, 1 focused), in the light/dark mix t.
func ExampleTerminalAt() {
	focused := rex.TerminalAt(0, 1)
	dimmed := rex.TerminalAt(0, 0)
	fmt.Println(focused.FG != dimmed.FG)
	// Output: true
}
