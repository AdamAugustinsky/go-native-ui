//go:build darwin

package live

import (
	"testing"

	"gonativeui/app"
	"gonativeui/menu"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

// The menu commands that have Shift in their key equivalent.
const (
	cmdSaveAs menu.ID = iota + 10
	cmdFindNext
	cmdFindPrev
	cmdColors
	cmdFonts
	cmdUndo
	cmdRedo
)

// shiftProbe is an application with a text field and a menu of commands whose
// shortcuts hold Shift and ⌘, and the model's own Edit menu with Undo and
// Redo (or, with stdEdit, the standard Edit menu the app adds).
type shiftProbe struct {
	k       *widget.Kit
	buf     ui.TextBuffer
	fired   map[menu.ID]int
	stdEdit bool
}

func (p *shiftProbe) Init(a *app.App) error {
	p.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	p.fired = map[menu.ID]int{}
	cmds := []menu.Command{
		{ID: cmdSaveAs, Title: "Save As…", Shortcut: menu.Shortcut{Key: 'S', Mods: ui.ModShift, Primary: true}},
		{ID: cmdFindNext, Title: "Find Next", Shortcut: menu.Shortcut{Key: 'G', Primary: true}},
		{ID: cmdFindPrev, Title: "Find Previous", Shortcut: menu.Shortcut{Key: 'G', Mods: ui.ModShift, Primary: true}},
		{ID: cmdColors, Title: "Show Colors", Shortcut: menu.Shortcut{Key: 'C', Mods: ui.ModShift, Primary: true}},
		{ID: cmdFonts, Title: "Show Fonts", Shortcut: menu.Shortcut{Key: 'T', Primary: true}},
	}
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	menus := []menu.Menu{
		{Title: "File", Items: []menu.Item{item(cmdSaveAs)}},
		{Title: "Find", Items: []menu.Item{item(cmdFindNext), item(cmdFindPrev)}},
		{Title: "Format", Items: []menu.Item{item(cmdColors), item(cmdFonts)}},
	}
	bar := []int{0, 1, 2}
	if !p.stdEdit {
		cmds = append(cmds,
			menu.Command{ID: cmdUndo, Role: menu.RoleUndo, Title: "Undo", Shortcut: menu.Shortcut{Key: 'Z', Primary: true}},
			menu.Command{ID: cmdRedo, Role: menu.RoleRedo, Title: "Redo", Shortcut: menu.Shortcut{Key: 'Z', Mods: ui.ModShift, Primary: true}})
		menus = append(menus, menu.Menu{Title: "Edit", Items: []menu.Item{item(cmdUndo), item(cmdRedo)}})
		bar = append(bar, 3)
	}
	m, err := menu.NewModel(cmds, menus, bar)
	if err != nil {
		return err
	}
	p.k.SetMenu(m)
	a.SetMenu(m)
	return nil
}

func (p *shiftProbe) Build(c *ui.Context) {
	k := p.k
	k.Begin(c)
	for id := cmdSaveAs; id <= cmdFonts; id++ {
		if k.Command(id) {
			p.fired[id]++
		}
	}
	k.BeginWindow()
	k.TextField("Name", &p.buf, "")
	k.EndWindow()
	k.End()
}

func TestShiftCommandShortcuts(t *testing.T) {
	for _, stdEdit := range []bool{false, true} {
		name := "model Edit menu"
		if stdEdit {
			name = "standard Edit menu"
		}
		t.Run(name, func(t *testing.T) {
			p := &shiftProbe{stdEdit: stdEdit}
			l := widgettest.StartLive(t, probeConfig(), p)
			l.Click(ui.RoleTextField, "Name")
			l.Type("abc")
			text := func() (s string) { l.Do(func(*app.App) { s = p.buf.String() }); return }
			fired := func(id menu.ID) (n int) { l.Do(func(*app.App) { n = p.fired[id] }); return }
			if got := text(); got != "abc" {
				t.Fatalf("typed %q", got)
			}
			for _, c := range []struct {
				name string
				key  ui.KeyCode
				mods ui.Mods
				id   menu.ID
			}{
				{"Save As ⇧⌘S", 'S', ui.ModSuper | ui.ModShift, cmdSaveAs},
				{"Find Next ⌘G", 'G', ui.ModSuper, cmdFindNext},
				{"Find Previous ⇧⌘G", 'G', ui.ModSuper | ui.ModShift, cmdFindPrev},
				{"Show Colors ⇧⌘C", 'C', ui.ModSuper | ui.ModShift, cmdColors},
				{"Show Fonts ⌘T", 'T', ui.ModSuper, cmdFonts},
			} {
				n := fired(c.id)
				l.Press(c.key, c.mods)
				if got := fired(c.id); got != n+1 {
					t.Errorf("%s fired %d times", c.name, got-n)
				}
			}
			l.Press('Z', ui.ModSuper)
			if got := text(); got == "abc" {
				t.Error("⌘Z did not undo")
			}
			undone := text()
			l.Press('Z', ui.ModSuper|ui.ModShift)
			if got := text(); got == undone {
				t.Errorf("⇧⌘Z did not redo (text %q)", got)
			}
		})
	}
}
