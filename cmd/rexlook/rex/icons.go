package rex

import "gonativeui/cmd/internal/rexkit"

// The icons are system symbols, rasterised by rexkit into an atlas (SF Symbols
// on macOS; text glyph fallbacks elsewhere). Nothing here is third-party artwork.

type iconID = rexkit.IconID

const (
	icAgent iconID = iota
	icGit
	icNode
	icSplit2
	icSplit1
	icExpand
	icXmark
	icDevice
	icCommand
	icPlus
	icTabAgent
	icTabFace
	icTabTerm
	numIcons
)

var iconSpecs = [numIcons]rexkit.IconSpec{
	icAgent:    {Name: "sparkles", Pt: 12.5, Weight: 0.23, FB: "✦"},
	icGit:      {Name: "plusminus.circle", Pt: 12, Weight: 0, FB: "±"},
	icNode:     {Name: "hexagon.fill", Pt: 12, Weight: 0, FB: "⬢"},
	icSplit2:   {Name: "rectangle.split.2x1", Pt: 11, Weight: 0, FB: "◫"},
	icSplit1:   {Name: "rectangle.split.1x2", Pt: 11, Weight: 0, FB: "⬒"},
	icExpand:   {Name: "arrow.up.left.and.arrow.down.right", Pt: 10.5, Weight: 0, FB: "⤢"},
	icXmark:    {Name: "xmark", Pt: 10.5, Weight: 0, FB: "✕"},
	icDevice:   {Name: "macstudio", Pt: 11, Weight: 0, FB: "▣"},
	icCommand:  {Name: "command", Pt: 12.5, Weight: 0.23, FB: "⌘"},
	icPlus:     {Name: "plus", Pt: 12.5, Weight: 0.23, FB: "+"},
	icTabAgent: {Name: "sparkles", Pt: 12, Weight: 0.23, FB: "✦"},
	icTabFace:  {Name: "face.smiling", Pt: 12, Weight: 0, FB: "☺"},
	icTabTerm:  {Name: "terminal", Pt: 12, Weight: 0.23, FB: ">_"},
}
