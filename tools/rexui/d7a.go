package main

// The D7a components: overlays, feedback and the chat pieces. Registered here,
// beside the table in main.go, so that the two lists grow independently.
func init() {
	for name, files := range map[string][]string{
		"popover":    {"popover.go"},
		"banner":     {"banner.go", "iconbutton.go", "button.go", "motion.go"},
		"badge":      {"badge.go", "motion.go"},
		"avatar":     {"avatar.go", "motion.go"},
		"skeleton":   {"skeleton.go", "motion.go"},
		"emptystate": {"emptystate.go", "button.go", "motion.go"},
		"tokenfield": {"tokenfield.go", "completion.go", "textfield.go", "searchfield.go", "motion.go"},
		"combobox":   {"combobox.go", "completion.go", "textfield.go", "searchfield.go"},
		"colorwell":  {"colorwell.go", "popover.go", "textfield.go", "searchfield.go"},
		"bubble":     {"bubble.go", "badge.go", "motion.go"},
		"chatrow":    {"chatrow.go", "avatar.go", "badge.go", "motion.go"},
		"typing":     {"typing.go", "motion.go"},
		"completion": {"completion.go"},
	} {
		components[name] = files
	}
}
