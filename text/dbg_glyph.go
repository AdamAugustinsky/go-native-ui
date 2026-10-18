//go:build linux || textengine

package text

import (
	"fmt"
	"os"
	"time"
)

var dbgGlyph = os.Getenv("GNUI_XDBG") == "glyph"
var dbgSeen = map[[2]uint32]uint8{}
var dbgT0 = time.Now()

func dbgGlyphMiss(face FaceID, gid uint16, sub uint8) {
	k := [2]uint32{uint32(face), uint32(gid)}
	kind := "NEWGLYPH"
	if dbgSeen[k] != 0 {
		kind = "NEWSUB"
	}
	dbgSeen[k] |= 1 << sub
	if time.Since(dbgT0) > 8*time.Second {
		fmt.Fprintf(os.Stderr, "GLYPHMISS %6.1fs %s face=%d gid=%d sub=%d seenmask=%04b\n", time.Since(dbgT0).Seconds(), kind, face, gid, sub, dbgSeen[k])
	}
}
