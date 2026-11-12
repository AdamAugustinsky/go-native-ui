package main

// The D7a components: overlays, feedback and the chat pieces. Registered here,
// beside the table in main.go, so that the two lists grow independently.
func init() {
	for name, files := range map[string][]string{
		"popover":    {"popover.go", "overlay.go"},
		"banner":     {"banner.go", "iconbutton.go", "button.go", "overlay.go"},
		"badge":      {"badge.go"},
		"avatar":     {"avatar.go"},
		"skeleton":   {"skeleton.go"},
		"emptystate": {"emptystate.go", "button.go"},
		"tokenfield": {"tokenfield.go", "completion.go", "textfield.go", "searchfield.go", "overlay.go"},
		"combobox":   {"combobox.go", "completion.go", "textfield.go", "searchfield.go", "overlay.go"},
		"colorwell":  {"colorwell.go", "popover.go", "textfield.go", "searchfield.go", "overlay.go"},
		"bubble":     {"bubble.go", "badge.go"},
		"chatrow":    {"chatrow.go", "avatar.go", "badge.go", "tooltip.go", "overlay.go"},
		"typing":     {"typing.go"},
		"completion": {"completion.go", "overlay.go"},
	} {
		components[name] = files
	}
}
