package widget

import (
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// DefaultTooltipDelayNs is how long the pointer rests on a widget before its
// tooltip shows.
const DefaultTooltipDelayNs = base.DefaultTooltipDelayNs

// Tooltip shows text beside the widget id, which must have been built in this
// frame and take pointer input (a button, a field, a list; a plain label does
// not). The tooltip appears once the pointer has rested on the widget for
// TooltipDelayNs, fades in, and goes when the pointer leaves it or a button is
// pressed. It is an overlay: it is drawn above everything, takes no input and
// never takes the focus. Call it right after the widget.
//
// Where the window system shows tooltips itself (macOS: ui.Context.NativeTooltips)
// nothing is drawn: the text is declared for the frame, and the system owns the
// delay, the look and the placement. Disabled widgets show tooltips too,
// without becoming hot or taking input.
func (k *Kit) Tooltip(id ui.ID, text string) { rexui.Tooltip(k.Kit, id, text) }
