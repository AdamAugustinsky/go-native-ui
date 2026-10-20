package ui

// The system colour panel, driven by a colour well (rexui.ColorWell). The
// well that has the panel calls ColorPanel every frame; the app asks, after
// End, what to do with the platform's panel (ColorPanelRequest) and reports
// what the user does there as input (Input.ColorPick, Input.ColorClosed).
// Calling ColorPanel again with another owner moves the panel to it; a frame
// without a call asks for it to be closed.

// ColorPanel says that the box with key owner shows the system colour panel
// with the colour rgba (0xRRGGBBAA, sRGB) this frame, and reports whether it
// holds the panel. A well calls it for as long as it is active: with claim on
// the frame it is activated, which takes the panel from the box that had it,
// and with claim off after that, when it holds the panel only if no other box
// does. A box that is told false is no longer active. The box that lost the
// panel learns it one frame later, as its next call.
func (c *Context) ColorPanel(owner Key, rgba uint32, claim bool) bool {
	if !claim {
		if c.cpNow.on && c.cpNow.owner != owner {
			return false
		}
		if c.cpLast.on && c.cpLast.owner != owner && !(c.cpNow.on && c.cpNow.owner == owner) {
			return false
		}
	}
	c.cpNow = colorReq{owner: owner, rgba: rgba, on: true}
	return true
}

// ColorPanelRequest reports, after End, whether a box showed the colour panel
// in the frame (ColorPanel) and with which colour. The app shows the platform's
// panel, moves it to a new colour, or closes it when ok is false.
func (c *Context) ColorPanelRequest() (owner Key, rgba uint32, ok bool) {
	return c.cpLast.owner, c.cpLast.rgba, c.cpLast.on
}

// TakeColorPick returns the colour the user chose in the panel this frame,
// if the panel belongs to owner (the box that called ColorPanel last).
func (c *Context) TakeColorPick(owner Key) (rgba uint32, ok bool) {
	if !c.in.HasColorPick || c.cpLast.owner != owner || !c.cpLast.on {
		return 0, false
	}
	return c.in.ColorPick, true
}

// ColorPanelClosed reports that the user closed the panel this frame, if it
// belongs to owner.
func (c *Context) ColorPanelClosed(owner Key) bool {
	return c.in.ColorClosed && c.cpLast.owner == owner && c.cpLast.on
}

// colorReq is a frame's colour panel request.
type colorReq struct {
	owner Key
	rgba  uint32
	on    bool
}
