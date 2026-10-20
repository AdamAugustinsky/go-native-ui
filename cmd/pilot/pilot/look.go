package pilot

import (
	"runtime"

	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
)

// menloPt is the size at which Menlo's cell is 7.5 pt wide (its advance is
// 0.602 em).
const menloPt = 12.46

// look is the Rex theme. The file list is set in the system font like
// Finder's (13 pt body, sizes right-aligned, dates in the secondary colour);
// with mono (Options.Mono, pilot -mono) the list and the path field are set in
// Menlo on macOS, the theme's own monospace font elsewhere, in columns of
// 7.5 pt cells like a terminal's. The gutter between the panes is one point:
// the dividing line (the panes' grab area is wider, see drawDivider).
func look(mono bool) *theme.Theme {
	t := rex.Theme()
	t.Metrics.Gutter = 1
	if mono {
		if face := monoFace(); face != "" {
			t.Fonts.Mono = text.FontDesc{Family: face, SizePt: menloPt, Weight: 400}
			t.Fonts.MonoBold = text.FontDesc{Family: face, SizePt: menloPt, Weight: 700}
		}
	}
	return t
}

// monoFace is the family the list is set in with Options.Mono, empty for the
// theme's system monospace font.
func monoFace() string {
	if runtime.GOOS == "darwin" {
		return "Menlo"
	}
	return ""
}
