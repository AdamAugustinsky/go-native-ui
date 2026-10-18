package gallery

import "gonativeui/ui"

// The sessions. Every one ends where it began (the same mode, the dialog and
// the menus closed, the table back at its top), so that the benchmark can run
// it over and over and measure whole sessions.

const (
	roleButton = ui.RoleButton
	rowRole    = ui.RoleRow
)

// benchSteps is the benchmark's session: hover and press on buttons and a
// tooltip, typing then copy, cut, select all and undo (paste needs a clipboard
// round trip and is not in it), choices, a Select, the table (scrolling a
// million rows, Page Down, Shift selection, a sort, a context menu), a Dialog,
// and the light/dark crossfade.
func benchSteps() []step {
	prim, shift := modPrim, modShift
	s := []step{idle(6)}
	add := func(x ...step) { s = append(s, x...) }

	// buttons: hover, press, a tooltip
	add(move(roleButton, "Save"), idle(3),
		down(roleButton, "Save"), idle(3), up(), idle(3),
		move(roleButton, "Open…"), waitFor(ui.RoleTooltip, ""), idle(4),
		move(roleButton, "Remove"), waitGone(ui.RoleTooltip, ""),
		move(roleButton, "Add item"), waitFor(ui.RoleTooltip, ""),
		move(roleButton, "More"), waitGone(ui.RoleTooltip, ""), idle(3))

	// typing, select all, copy, cut, undo
	add(click(ui.RoleTextField, "Notes"), idle(2), key(ui.KeyEnd, 0),
		typeText("abc"), idle(2), typeText("de"), idle(2),
		key('A', prim), idle(2), key('C', prim), idle(2), key(ui.KeyRight, 0), idle(2),
		key('A', prim), key('X', prim), idle(3),
		key('Z', prim), idle(2), key('Z', prim), idle(2), key('Z', prim), idle(2),
		keys(ui.KeyLeft, shift, 3), idle(2), key(ui.KeyEnd, 0), idle(2))

	// choices
	add(click(ui.RoleCheckbox, "Enable feature"), idle(4), click(ui.RoleCheckbox, "Enable feature"), idle(4),
		click(ui.RoleCheckbox, "All options"), idle(3), click(ui.RoleCheckbox, "All options"), idle(3),
		click(ui.RoleCheckbox, "Enable feature"), idle(3),
		click(ui.RoleSwitch, "Notifications"), idle(4), click(ui.RoleSwitch, "Notifications"), idle(4),
		click(ui.RoleRadio, "Two"), idle(3), click(ui.RoleRadio, "One"), idle(3),
		click(ui.RoleSlider, "Volume"), idle(2), keys(ui.KeyRight, 0, 4), keys(ui.KeyLeft, 0, 4), idle(2),
		click(ui.RoleTab, "Details"), idle(3), click(ui.RoleTab, "Overview"), idle(3))

	// a Select, choosing with the pointer and with the keyboard
	add(click(ui.RoleComboBox, "Format"), waitFor(ui.RoleListItem, "Markdown"), idle(3),
		click(ui.RoleListItem, "Markdown"), waitGone(ui.RoleListItem, "HTML"), idle(2),
		click(ui.RoleComboBox, "Format"), waitFor(ui.RoleListItem, "HTML"), idle(2),
		key(ui.KeyUp, 0), key(ui.KeyEnter, 0), waitGone(ui.RoleListItem, "HTML"), idle(2))

	// a menu from the bar
	add(click(ui.RoleMenuItem, "Edit"), waitFor(ui.RoleMenuItem, "Undo"), idle(3),
		move(ui.RoleMenuItem, "Cut"), idle(3), key(ui.KeyEscape, 0), waitGone(ui.RoleMenuItem, "Undo"), idle(2))

	// the table: a million rows
	add(sweep(ui.RoleTable, "", 24), idle(2),
		click(rowRole, "").Nth(3), idle(2),
		click(rowRole, "").Nth(8).With(shift), idle(2),
		keys(ui.KeyPageDown, 0, 3), keys(ui.KeyDown, shift, 6), keys(ui.KeyPageUp, shift, 2), idle(2),
		key('A', prim), idle(3),
		wheel(ui.RoleTable, "", -40, 80), idle(2), wheel(ui.RoleTable, "", 55, 60), idle(2),
		key(ui.KeyEnd, 0), idle(60), key(ui.KeyHome, 0), idle(60),
		click(ui.RoleColumnHeader, "ID"), idle(4), click(ui.RoleColumnHeader, "ID"), idle(4),
		click(rowRole, "").Nth(2), idle(2),
		rclick(ui.RoleRow, "").Nth(2), waitFor(ui.RoleMenuItem, "Copy Path"), idle(2),
		click(ui.RoleMenuItem, "Copy Path"), waitGone(ui.RoleMenuItem, "Copy Path"), idle(2))

	// the divider between the first two panes, there and back
	add(drag(ui.RoleSplitter, "Divider", 3, 20), idle(2), drag(ui.RoleSplitter, "Divider", -3, 20), idle(2))

	// the dialog: open it, close it with Escape; then once more and take the action
	add(click(roleButton, "Delete…"), waitFor(ui.RoleDialog, ""), settle(), idle(3),
		key(ui.KeyEscape, 0), waitGone(ui.RoleDialog, ""), settle(),
		click(roleButton, "Delete…"), waitFor(ui.RoleDialog, ""), settle(),
		click(roleButton, "Delete"), waitGone(ui.RoleDialog, ""), settle())

	// the indeterminate progress bar, which asks for frames for as long as it is shown
	add(click(ui.RoleSwitch, "Show activity"), pause(120), click(ui.RoleSwitch, "Show activity"), settle())

	// light and dark
	add(key('D', prim|shift), settle(), idle(3), key('D', prim|shift), settle(), idle(3), key('D', prim|shift), settle(), idle(3))
	return s
}

// scene is the steps that put the gallery in the state of a screenshot.
type scene struct {
	name  string
	steps func() []step
}

// Scenes are the states Shot renders.
var scenes = []scene{
	{"main", func() []step {
		return []step{idle(4), click(rowRole, "").Nth(3), click(rowRole, "").Nth(6).With(modShift),
			click(ui.RoleTextField, "Notes"), idle(2), key('A', modPrim), idle(2), move(roleButton, "Save"), settle(), idle(2)}
	}},
	{"menu", func() []step {
		return []step{idle(4), click(ui.RoleMenuItem, "File"), waitFor(ui.RoleMenuItem, "Open Recent"),
			click(ui.RoleMenuItem, "Open Recent"), settle(), idle(2)}
	}},
	{"select", func() []step {
		return []step{idle(4), click(ui.RoleComboBox, "Format"), waitFor(ui.RoleListItem, "HTML"), move(ui.RoleListItem, "Markdown"), settle(), idle(2)}
	}},
	{"context", func() []step {
		return []step{idle(4), click(rowRole, "").Nth(2), rclick(rowRole, "").Nth(2),
			waitFor(ui.RoleMenuItem, "Copy Path"), move(ui.RoleMenuItem, "Copy Path"), settle(), idle(2)}
	}},
	{"dialog", func() []step {
		return []step{idle(4), click(rowRole, "").Nth(3), click(rowRole, "").Nth(6).With(modShift),
			click(roleButton, "Delete…"), waitFor(ui.RoleDialog, ""), settle(), idle(2)}
	}},
	{"tooltip", func() []step {
		return []step{idle(4), move(roleButton, "Open…"), waitFor(ui.RoleTooltip, ""), settle(), idle(2)}
	}},
}
