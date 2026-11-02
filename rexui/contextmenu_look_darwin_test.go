//go:build darwin

package rexui_test

import (
	"image"
	"testing"

	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

// contextMenuScene is a chat list row with ContextMenu on it; an offscreen
// window has no native menus, so the menu is the drawn one (Windows' and
// Linux's).
type contextMenuScene struct {
	k    *widget.Kit
	mode theme.Mode
}

func (s *contextMenuScene) Init(a *app.App) error {
	s.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	s.k.SetMode(s.mode)
	s.k.ReduceMotion = true
	return nil
}

func (s *contextMenuScene) Build(c *ui.Context) {
	k := s.k
	k.Begin(c)
	k.BeginWindow()
	k.Column("list")
	row := rexui.ChatListRow(k.Kit, "r1", "Ada Lovelace", "Lunch tomorrow?", "10:42", rexui.ChatRowOpts{})
	rexui.ContextMenu(k.Kit, row.ID, func(m *[]ui.MenuItem) {
		*m = append(*m,
			ui.MenuItem{ID: 1, Title: "Mark as Read", Key: 'u', Mods: ui.ModCtrl | ui.ModShift},
			ui.MenuItem{ID: 2, Title: "Pin", Mark: ui.MarkOn},
			ui.MenuItem{ID: 3, Title: "Mute", Disabled: true},
			ui.MenuSeparator,
			ui.MenuItem{Title: "Labels", Sub: []ui.MenuItem{{ID: 10, Title: "Family", Mark: ui.MarkOn}, {ID: 11, Title: "Work"}, {ID: 12, Title: "Some", Mark: ui.MarkMixed}}},
			ui.MenuSeparator,
			ui.MenuItem{ID: 4, Title: "Delete Chat", Destructive: true})
	})
	k.EndColumn()
	k.EndWindow()
	k.End()
}

// The drawn context menu, offscreen, in light and dark, with its submenu
// open under the pointer: the raised panel, check marks, a key equivalent,
// a disabled and a destructive item, separators and the submenu's chevron
// (C5).
func TestContextMenuLook(t *testing.T) {
	for _, m := range []struct {
		name string
		mode theme.Mode
	}{{"light", theme.Light}, {"dark", theme.Dark}} {
		t.Run(m.name, func(t *testing.T) {
			s := &contextMenuScene{mode: m.mode}
			r := widgettest.Open(t, app.Config{Title: "context menu"}, 960, 560, 2, s)
			r.Frames(3)
			r.Play(widgettest.RClick(ui.RoleListItem, "Ada Lovelace"), widgettest.WaitFor(ui.RoleMenuItem, "Labels"),
				widgettest.Move(ui.RoleMenuItem, "Labels"), widgettest.WaitFor(ui.RoleMenuItem, "Work"), widgettest.Settle())
			r.AssertImageRect("contextmenu-"+m.name, image.Rect(0, 0, 960, 560), widgettest.Tolerance{})
		})
	}
}

// dropdownScene is a "+" button with a DropdownMenu of Lucide icons under
// section headers, as T3 Code's composer menus are.
type dropdownScene struct {
	k    *widget.Kit
	mode theme.Mode
}

func (s *dropdownScene) Init(a *app.App) error {
	s.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	s.k.SetMode(s.mode)
	s.k.ReduceMotion = true
	return nil
}

func (s *dropdownScene) Build(c *ui.Context) {
	k := s.k
	k.Begin(c)
	k.BeginWindow()
	k.Column("col")
	b := rexui.IconButton(k.Kit, icon.Plus, "Add", rexui.IconButtonOpts{})
	rexui.DropdownMenu(k.Kit, b.ID, func(m *[]ui.MenuItem) {
		*m = append(*m,
			ui.MenuItem{Header: true, Title: "Attach"},
			ui.MenuItem{ID: 1, Title: "Files", Icon: "paperclip", Key: 'u', Mods: ui.ModSuper},
			ui.MenuItem{ID: 2, Title: "Image", Icon: "image"},
			ui.MenuItem{ID: 3, Title: "Folder", Icon: "folder-open", Disabled: true},
			ui.MenuSeparator,
			ui.MenuItem{Header: true, Title: "Mode"},
			ui.MenuItem{ID: 4, Title: "Plan", Icon: "list-todo"},
			ui.MenuItem{ID: 5, Title: "Clear Chat", Icon: "trash-2", Destructive: true})
	})
	k.EndColumn()
	k.EndWindow()
	k.End()
}

// The dropdown menu of a button, offscreen, in light and dark, with the
// pointer on its first item: section headers, Lucide icons in the item's
// colour, a key equivalent, a disabled and a destructive item, and no mark
// column.
func TestDropdownMenuLook(t *testing.T) {
	for _, m := range []struct {
		name string
		mode theme.Mode
	}{{"light", theme.Light}, {"dark", theme.Dark}} {
		t.Run(m.name, func(t *testing.T) {
			s := &dropdownScene{mode: m.mode}
			r := widgettest.Open(t, app.Config{Title: "dropdown"}, 480, 520, 2, s)
			r.Frames(3)
			r.Play(widgettest.Click(ui.RoleButton, "Add"), widgettest.WaitFor(ui.RoleMenuItem, "Files"),
				widgettest.Move(ui.RoleMenuItem, "Files"), widgettest.Settle())
			r.AssertImageRect("dropdown-"+m.name, image.Rect(0, 0, 480, 520), widgettest.Tolerance{})
		})
	}
}
