package widget

import "gonativeui/rexui"

// Tabs builds a strip of tabs, one per title, that edits *sel (the index of the
// shown tab): a segmented control in the tab-list role
// (rexui.SegmentedControl, rexui.SegmentedTabs), as macOS draws a tab view's
// tabs. The app builds the content of the selected tab below it. The selected
// tab is the strip's tab stop; the Left and Right arrow keys move the
// selection and the focus together, wrapping at the ends. Changed is set in
// the frame the selection changed.
func (k *Kit) Tabs(label string, sel *int, titles []string) Signal {
	return rexui.SegmentedControl(k.Kit, label, sel, titles, rexui.SegmentedOpts{Role: rexui.SegmentedTabs})
}
