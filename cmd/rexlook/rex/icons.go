package rex

import "gonativeui/icon"

// The icons are system symbols, rasterised by package icon into an atlas (SF
// Symbols on macOS; glyphs of the system fonts elsewhere). Nothing here is
// third-party artwork. They follow the standard icons in the atlas; the plus
// is the standard one.

type iconID = icon.ID

const (
	icAgent iconID = icon.NumStandard + iota
	icGit
	icNode
	icSplit2
	icSplit1
	icExpand
	icXmark
	icDevice
	icCommand
	icTabAgent
	icTabFace
	icTabTerm
	numIcons = iota
)

// icPlus is the standard plus: the same symbol, size and weight as the
// reference's.
const icPlus = icon.Plus

var iconSpecs = [numIcons]icon.Spec{
	icAgent - icon.NumStandard:    {Symbol: "sparkles", Pt: 12.5, Weight: 0.23, Fallback: '✦'},
	icGit - icon.NumStandard:      {Symbol: "plusminus.circle", Pt: 12, Fallback: '±'},
	icNode - icon.NumStandard:     {Symbol: "hexagon.fill", Pt: 12, Fallback: '⬢'},
	icSplit2 - icon.NumStandard:   {Symbol: "rectangle.split.2x1", Pt: 11, Fallback: '◫'},
	icSplit1 - icon.NumStandard:   {Symbol: "rectangle.split.1x2", Pt: 11, Fallback: '⬒'},
	icExpand - icon.NumStandard:   {Symbol: "arrow.up.left.and.arrow.down.right", Pt: 10.5, Fallback: '⤢'},
	icXmark - icon.NumStandard:    {Symbol: "xmark", Pt: 10.5, Fallback: '✕'},
	icDevice - icon.NumStandard:   {Symbol: "macstudio", Pt: 11, Fallback: '▣'},
	icCommand - icon.NumStandard:  {Symbol: "command", Pt: 12.5, Weight: 0.23, Fallback: '⌘'},
	icTabAgent - icon.NumStandard: {Symbol: "sparkles", Pt: 12, Weight: 0.23, Fallback: '✦'},
	icTabFace - icon.NumStandard:  {Symbol: "face.smiling", Pt: 12, Fallback: '☺'},
	icTabTerm - icon.NumStandard:  {Symbol: "terminal", Pt: 12, Weight: 0.23, Fallback: '>'},
}
