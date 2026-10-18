package pilot

import "gonativeui/icon"

// The icons of the directory entries beyond the standard Folder and File: a
// symlink, an executable and anything else. They are added to the kit's atlas
// before its first frame (SF Symbols on macOS, Segoe codepoints on Windows,
// glyphs of the system font elsewhere).
var (
	specLink  = icon.Spec{Symbol: "link", Segoe: 0xE71B, Fallback: '↪', Pt: 11, Weight: 0.23}
	specExec  = icon.Spec{Symbol: "terminal", Segoe: 0xE756, Fallback: '›', Pt: 11, Weight: 0.23}
	specOther = icon.Spec{Symbol: "square.dashed", Fallback: '◇', Pt: 11, Weight: 0}
)

// entryIcons are the icon IDs of the kinds of entries, made in Init.
type entryIcons struct{ link, exec, other icon.ID }
