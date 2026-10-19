package ui

// Material regions (N3a, D41, D52): parts of the window that show a live
// system material instead of the app's pixels. A box is marked with
// Context.Material while it is built; after End, MaterialRegions reports
// every marked box of the frame with its rectangle (clipped by its ancestors'
// clips and the window), its corner radii and its material, in draw order.
// The app hands the list to the window (app.Run does), which places the
// native effect or glass views below its GPU surface; the app paints nothing
// in those boxes (the theme's MaterialSpec.Native says when), so the material
// shows through its transparent pixels.

// MaterialArea is the part of the window a material region is. The values
// mirror platform.MaterialArea, which the app package converts to.
type MaterialArea uint8

const (
	// AreaWindow is a backdrop behind the whole window.
	AreaWindow MaterialArea = iota
	// AreaSidebar is a source-list sidebar (flush, full height).
	AreaSidebar
	// AreaInspector is an inspector pane.
	AreaInspector
	// AreaTitlebar is the toolbar band's scroll-edge bar, under the toolbar
	// capsules (an opaque fill of Solid).
	AreaTitlebar
	// AreaToolbarCapsule is a toolbar item's glass capsule (radius half its
	// height).
	AreaToolbarCapsule
	// AreaComposer is a floating glass control such as a chat composer.
	AreaComposer
	// AreaMenu is a menu's material.
	AreaMenu
	// AreaPopover is a popover's material.
	AreaPopover
	// AreaHUD is a heads-up panel's dark material.
	AreaHUD
)

// MaterialRegion is one material region of the finished frame.
type MaterialRegion struct {
	// Area is the part of the window it is.
	Area MaterialArea
	// Token is the theme's material (theme.Material) the box shows.
	Token uint8
	// Rect is x0, y0, x1, y1 in pixels: the box clipped by its ancestors'
	// clip rectangles and the window.
	Rect [4]float32
	// Radii are the box's corner radii in pixels (top left, top right,
	// bottom right, bottom left).
	Radii [4]float32
	// Solid is the opaque colour (0xRRGGBBAA) shown under Reduce
	// Transparency, and always for AreaTitlebar; 0 takes the window
	// background.
	Solid uint32
}

// maxMaterials is the most material regions a frame reports
// (platform.MaxMaterials).
const maxMaterials = 32

// shellRegs are the material and host boxes marked in a frame.
type shellRegs struct {
	frame  uint32 // the frame the records belong to
	mats   [maxMaterials]matRec
	nMats  int
	hosts  [maxHosts]hostRec
	nHosts int
}

type matRec struct {
	id    ID
	area  MaterialArea
	token uint8
	solid uint32
}

// shellFrame drops the previous frame's records.
func (c *Context) shellFrame() {
	if c.shell.frame != c.frame {
		c.shell.frame, c.shell.nMats, c.shell.nHosts = c.frame, 0, 0
	}
}

// Material marks box id, built this frame, as a material region of the
// given area, showing the theme material token (theme.Material) and, under
// Reduce Transparency, the opaque colour solid (0xRRGGBBAA; 0: the window
// background). The region takes the box's rectangle and corner radii after
// layout. The app paints nothing in the box itself. At most 32 regions per
// frame; more are ignored.
func (c *Context) Material(id ID, area MaterialArea, token uint8, solid uint32) {
	c.shellFrame()
	if id == 0 || c.shell.nMats >= maxMaterials {
		return
	}
	c.shell.mats[c.shell.nMats] = matRec{id: id, area: area, token: token, solid: solid}
	c.shell.nMats++
}

// MaterialRegions appends to dst the material regions of the last finished
// frame (after End), in draw order: the main tree first, then each overlay
// above the one before, each in build order. Regions clipped away entirely
// are reported with an empty Rect, so a region's slot stays stable while it
// scrolls out of view. It allocates nothing when dst has room.
func (c *Context) MaterialRegions(dst []MaterialRegion) []MaterialRegion {
	dst = dst[:0]
	if c.shell.frame != c.frame {
		return dst
	}
	for ovl := 0; ovl <= c.nOvs; ovl++ { // draw order: the main tree, then the overlays
		for i := 0; i < c.shell.nMats; i++ {
			m := &c.shell.mats[i]
			b := &c.boxes[m.id]
			if b.lastFrame != c.frame || int(b.ovl) != ovl {
				continue
			}
			r := c.clippedRect(m.id)
			if empty(r) {
				r = [4]float32{}
			}
			dst = append(dst, MaterialRegion{Area: m.area, Token: m.token, Rect: r, Radii: b.radii, Solid: m.solid})
		}
	}
	return dst
}

// clippedRect is box id's rectangle clipped by the clip rectangles of its
// ancestors and by the window.
func (c *Context) clippedRect(id ID) [4]float32 {
	b := &c.boxes[id]
	r := intersect(b.rect, [4]float32{0, 0, float32(c.w), float32(c.h)})
	for p := b.parent; p != 0; p = c.boxes[p].parent {
		if pb := &c.boxes[p]; pb.flags&Clip != 0 {
			r = intersect(r, pb.rect)
		}
	}
	return r
}
