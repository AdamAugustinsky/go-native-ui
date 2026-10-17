package pilot

import "gonativeui/cmd/internal/rexkit"

type iconID = rexkit.IconID

const (
	icBack iconID = iota
	icForward
	icUp
	icFolder
	icFile
	icLink
	icExec
	icOther
	numIcons
)

// SF Symbols on macOS, text glyphs elsewhere (rexkit rasterises the atlas).
var iconSpecs = [numIcons]rexkit.IconSpec{
	icBack:    {Name: "chevron.left", Pt: 12.5, Weight: 0.3, FB: "‹"},
	icForward: {Name: "chevron.right", Pt: 12.5, Weight: 0.3, FB: "›"},
	icUp:      {Name: "chevron.up", Pt: 12.5, Weight: 0.3, FB: "︿"},
	icFolder:  {Name: "folder.fill", Pt: 11.5, Weight: 0, FB: "▸"},
	icFile:    {Name: "doc", Pt: 11, Weight: 0, FB: "▫"},
	icLink:    {Name: "link", Pt: 11, Weight: 0.23, FB: "↪"},
	icExec:    {Name: "terminal", Pt: 11, Weight: 0.23, FB: "›_"},
	icOther:   {Name: "square.dashed", Pt: 11, Weight: 0, FB: "◇"},
}
