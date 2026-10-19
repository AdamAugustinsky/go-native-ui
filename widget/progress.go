package widget

import (
	"gonativeui/rexui"
	"gonativeui/ui"
)

// ProgressBar builds a progress bar showing frac, from 0 to 1 (rexui.ProgressBar).
// A negative frac is an indeterminate bar: a segment sweeps along the track for
// as long as the bar is visible and the window shown, and only then does it ask
// for frames; a window whose bar is scrolled, covered or hidden goes back to
// idle. The width fills the parent unless NextWidth says otherwise. With
// Kit.ReduceMotion the segment stands still. The label is the accessible name.
func (k *Kit) ProgressBar(label string, frac float32) ui.ID {
	return rexui.ProgressBar(k.Kit, label, frac, rexui.ProgressOpts{})
}
