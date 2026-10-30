package rexui

import "gonativeui/theme"

// appKitToggleOn is ToggleOnLook as AppKit draws it (an NSButton of type
// NSButtonTypePushOnPushOff on macOS 27.2, tools/appkitref toggle-on): the
// accent with a white label, as a default button is; disabled the accent at
// 70 % under a label that is white at 12 % (UseAppKitControls).
var appKitToggleOn = ButtonRecipe{
	Fill: Of(theme.Accent), Label: Of(theme.OnAccent),
	FillInactive: OfAlpha(theme.Label, 0.125, 0.124), LabelInactive: Of(theme.Label),
	FillHover: None, FillPressed: Of(theme.AccentPressed), LabelPressed: Of(theme.OnAccent),
	FillPressedInactive: Of(theme.FillPressed), LabelPressedInactive: Of(theme.Label),
	FillDisabled: OfAlpha(theme.Accent, 0.7, 0.7), LabelDisabled: OfAlpha(theme.OnAccent, 0.12, 0.12),
	Rim: None, RimInactive: None, Highlight: None,
}

// UseAppKitControls restyles the push, icon and toggle buttons and segmented
// controls as macOS 27.2 draws them (the measurements of tools/appkitref):
// rounded rectangles of the size's radius, a toggle that is on or a chosen
// segment in the accent under a white label, systemRed destructive labels and
// borderless icon buttons without a chip. Rex's capsules and quiet
// tints are the default; an app that wants AppKit's controls instead calls
// this once, before its first frame. AppKit's rounded corners are continuous
// (a squircle, not an arc): the theme says so with Corners =
// theme.CornersContinuous (the default on macOS for a theme that sets none;
// Rex's is circular).
func UseAppKitControls() {
	for _, v := range [...]ButtonVariant{ButtonDefault, ButtonPrimary, ButtonDestructive, ButtonPlain} {
		ButtonRecipes[v].Capsule = false
	}
	for _, v := range [...]IconButtonVariant{IconBordered, IconPrimary, IconDestructive} {
		IconButtonRecipes[v].Capsule = false
	}
	for _, rc := range [...]*ButtonRecipe{&ButtonRecipes[ButtonDestructive], &IconButtonRecipes[IconDestructive]} {
		rc.Label, rc.LabelInactive, rc.LabelPressed = Of(theme.Red), Of(theme.Red), Of(theme.Red)
	}
	// a bare symbol in the secondary label colour, darkening under the pointer
	// with no chip behind it
	rc := &IconButtonRecipes[IconBorderless]
	rc.Label, rc.LabelInactive, rc.LabelPressed = Of(theme.Label2), Of(theme.Label3), Of(theme.Label)
	rc.LabelDisabled = Of(theme.Label4)
	IconButtonHoverLabel = Of(theme.Label)
	IconButtonChip = IconChip{}
	ToggleOnLook = appKitToggleOn
	SegmentedLook.Capsule = false
	SegmentedLook.Selected, SegmentedLook.SelectedLabel = Of(theme.Accent), Of(theme.OnAccent)
}
