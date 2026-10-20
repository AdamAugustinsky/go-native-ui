package gallery

import (
	"gonativeui/rexui"
	"gonativeui/rexui/catalog"
)

// The gallery's scenes, the rows of its sidebar. The live scenes come first;
// the pages of rexui's AppKit catalogue (catalog.Pages) follow, page p being
// scene scCatalogue+p.
const (
	scPlayground = iota
	scStates
	scTable
	scAppearance
	scDialogs
	scCatalogue
)

// numScenes is the number of scenes.
const numScenes = scCatalogue + len(catalog.Pages)

// The sidebar's groups.
const (
	grpGallery = iota
	grpWindow
	grpCatalogue
	numGroups
)

var groupTitles = [numGroups]string{"Gallery", "Window", "AppKit catalogue"}

// The tiles' colours, the macOS 27.2 system colours as System Settings'
// sidebar shows them (the same measured values as chromedemo's Settings).
const (
	tileBlue = iota
	tileRed
	tileIndigo
	tileGrey
	tileLabel
	tileTeal
	tileGreen
	tileOrange
	numTiles
)

var tileColours = [numTiles]rexui.Pair{
	tileBlue: rexui.Solid(0x0070F4, 0x148EFE), tileRed: rexui.Solid(0xF53125, 0xFF4F43),
	tileIndigo: rexui.Solid(0x5452CB, 0x6361F1), tileGrey: rexui.Solid(0x848488, 0xA2A2A6),
	tileLabel: rexui.PairA(0x000000, 0xFFFFFF, 0.7, 0.9), tileTeal: rexui.Solid(0x2EA7BC, 0x44D4EC),
	tileGreen: rexui.Solid(0x34C759, 0x30D158), tileOrange: rexui.Solid(0xFF9500, 0xFF9F0A),
}

// sceneDef describes a scene.
type sceneDef struct {
	title  string // the sidebar's row and the window's title
	symbol string // the tile's SF Symbol
	tile   int
	group  int
}

// catalogueDefs are the catalogue pages' rows (AppKitPages names the same
// pages for the command line); a page without an entry here is shown under
// its page name.
var catalogueDefs = [...]sceneDef{
	{"Controls", "switch.2", tileGrey, grpCatalogue},
	{"Text styles", "textformat", tileLabel, grpCatalogue},
	{"Lists", "list.bullet", tileTeal, grpCatalogue},
	{"Forms", "checklist", tileOrange, grpCatalogue},
	{"Components", "square.stack.3d.up.fill", tileIndigo, grpCatalogue},
}

// defs is every scene's definition, by scene.
var defs [numScenes]sceneDef

func init() {
	defs[scPlayground] = sceneDef{"Playground", "slider.horizontal.3", tileBlue, grpGallery}
	defs[scStates] = sceneDef{"States", "square.grid.3x3.fill", tileIndigo, grpGallery}
	defs[scTable] = sceneDef{"Table", "tablecells.fill", tileGreen, grpGallery}
	defs[scAppearance] = sceneDef{"Appearance", "circle.lefthalf.filled", tileLabel, grpWindow}
	defs[scDialogs] = sceneDef{"Dialogs", "bubble.left.fill", tileRed, grpWindow}
	for p := range catalog.Pages {
		d := sceneDef{AppKitPages[p], "square.grid.2x2.fill", tileGrey, grpCatalogue}
		if p < len(catalogueDefs) {
			d = catalogueDefs[p]
		}
		defs[scCatalogue+p] = d
	}
}
