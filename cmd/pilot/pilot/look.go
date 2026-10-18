package pilot

import (
	"runtime"

	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
)

// menloPt is the size at which Menlo's cell is 7.5 pt wide (its advance is
// 0.602 em): the columns of the file list are laid out in such cells.
const menloPt = 12.46

// look is the Rex theme with the monospace face Pilot's lists are set in. On
// macOS that is Menlo, as the terminal-style content of the Rex reference is;
// elsewhere the theme's own system monospace font.
func look() *theme.Theme {
	t := rex.Theme()
	if face := monoFace(); face != "" {
		t.Fonts.Mono = text.FontDesc{Family: face, SizePt: menloPt, Weight: 400}
		t.Fonts.MonoBold = text.FontDesc{Family: face, SizePt: menloPt, Weight: 700}
	}
	return t
}

// monoFace is the family the lists are set in, empty for the theme's system
// monospace font.
func monoFace() string {
	if runtime.GOOS == "darwin" {
		return "Menlo"
	}
	return ""
}
