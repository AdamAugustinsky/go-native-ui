package app

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/ui"
)

const (
	mcOpen menu.ID = iota + 1
	mcQuit
	mcAbout
	mcCopy
	mcPaste
	mcZoom
	mcSettings
	mcToggle
)

func testMenuModel(t *testing.T, withEdit bool) *menu.Model {
	t.Helper()
	primary := func(k ui.KeyCode) menu.Shortcut { return menu.Shortcut{Key: k, Primary: true} }
	cmds := []menu.Command{
		{ID: mcOpen, Title: "Open…", Shortcut: primary('O'), Symbol: "folder"},
		{ID: mcQuit, Title: "Exit", Role: menu.RoleQuit, Shortcut: menu.Shortcut{Key: 'X', Mods: ui.ModAlt}},
		{ID: mcAbout, Title: "About", Role: menu.RoleAbout},
		{ID: mcCopy, Role: menu.RoleCopy, Shortcut: primary('C')},
		{ID: mcPaste, Role: menu.RolePaste, Shortcut: primary('V')},
		{ID: mcZoom, Title: "Zoom", Role: menu.RoleZoom},
		{ID: mcSettings, Role: menu.RoleSettings},
		{ID: mcToggle, Title: "Toggle", Shortcut: menu.Shortcut{Key: ui.KeyBackspace, Mods: ui.ModShift, Primary: true}},
	}
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	sep := menu.Item{Kind: menu.ItemSeparator}
	file := menu.Menu{Title: "File", Items: []menu.Item{item(mcOpen), sep, item(mcSettings), sep, item(mcQuit)}}
	edit := menu.Menu{Title: "Edit", Items: []menu.Item{item(mcCopy), item(mcPaste)}}
	view := menu.Menu{Title: "View", Items: []menu.Item{item(mcToggle), item(mcZoom)}}
	help := menu.Menu{Title: "Help", Role: menu.MenuHelp, Items: []menu.Item{item(mcAbout)}}
	menus, bar := []menu.Menu{file, view, help}, []int{0, 1, 2}
	if withEdit {
		menus, bar = []menu.Menu{file, edit, view, help}, []int{0, 1, 2, 3}
	}
	m, err := menu.NewModel(cmds, menus, bar)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func barTitles(s *platform.MenuSpec) []string {
	var out []string
	for _, bi := range s.Bar {
		out = append(out, s.Menus[bi].Title)
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The macOS bar around a model: the application menu first, with the
// application roles moved into it; a standard Edit menu after the first
// model menu when the model has none; the model's Help menu kept and a
// standard Window menu added; separators left by the moved items dropped.
func TestNativeMenuSpec(t *testing.T) {
	s := nativeMenuSpec(testMenuModel(t, false))
	if got, want := barTitles(&s), []string{"Application", "File", "Edit", "View", "Window", "Help"}; !sameStrings(got, want) {
		t.Fatalf("bar %q, want %q", got, want)
	}
	app := s.Menus[s.Bar[0]]
	if app.Role != platform.MenuRoleApp {
		t.Errorf("the first menu has role %d", app.Role)
	}
	var acts []platform.MenuAction
	var settings, quit, about, hide platform.MenuItem
	for _, it := range app.Items {
		switch it.Kind {
		case platform.MenuItemAction:
			acts = append(acts, it.Action)
			switch it.Action {
			case platform.MenuNone:
				if it.ID == uint16(mcSettings) {
					settings = it
				}
			case platform.MenuQuit:
				quit = it
			case platform.MenuAbout:
				about = it
			case platform.MenuHide:
				hide = it
			}
		case platform.MenuItemSubmenu:
			if s.Menus[it.Submenu].Role != platform.MenuRoleServices {
				t.Errorf("the application menu's submenu is not Services")
			}
		}
	}
	wantActs := []platform.MenuAction{platform.MenuAbout, platform.MenuNone, platform.MenuHide, platform.MenuHideOthers, platform.MenuShowAll, platform.MenuQuit}
	if len(acts) != len(wantActs) {
		t.Fatalf("application menu actions %v, want %v", acts, wantActs)
	}
	for i := range acts {
		if acts[i] != wantActs[i] {
			t.Fatalf("application menu actions %v, want %v", acts, wantActs)
		}
	}
	if settings.Title != "Settings…" || settings.Key != ',' || settings.Mods != platform.ModSuper || settings.Action != platform.MenuNone {
		t.Errorf("Settings %+v", settings)
	}
	// The system's items keep the model's command of their role, so that the
	// model decides them; their title and key equivalent are macOS's.
	if quit.ID != uint16(mcQuit) || quit.Title != "" || quit.Key != 0 || about.ID != uint16(mcAbout) || hide.ID != 0 {
		t.Errorf("Quit %+v, About %+v, Hide (no command) %+v", quit, about, hide)
	}
	file := s.Menus[0]
	if len(file.Items) != 1 || file.Items[0].ID != uint16(mcOpen) || file.Items[0].Key != 'o' || file.Items[0].Mods != platform.ModSuper || file.Items[0].Symbol != "folder" {
		t.Errorf("File %+v: want Open… alone (Settings and Quit moved, their separators gone)", file.Items)
	}
	view := s.Menus[1]
	if len(view.Items) != 2 || view.Items[0].Key != platform.MenuKeyBackspace || view.Items[0].Mods != platform.ModSuper|platform.ModShift ||
		view.Items[1].Action != platform.MenuZoom || view.Items[1].ID != uint16(mcZoom) {
		t.Errorf("View %+v", view.Items)
	}
	if help := s.Menus[2]; help.Role != platform.MenuRoleHelp || len(help.Items) != 0 {
		t.Errorf("Help %+v: want the Help role, About moved out", help)
	}
	edit := s.Menus[s.Bar[2]]
	if w := s.Menus[s.Bar[4]]; w.Role != platform.MenuRoleWindow || len(w.Items) != 4 || w.Items[0].Action != platform.MenuMinimize {
		t.Errorf("standard Window menu %+v", w)
	}
	for _, it := range edit.Items {
		if it.Kind == platform.MenuItemAction && (!it.Action.IsEdit() || it.ID != 0) {
			t.Errorf("standard Edit item %+v", it)
		}
	}

	s = nativeMenuSpec(testMenuModel(t, true))
	if got, want := barTitles(&s), []string{"Application", "File", "Edit", "View", "Window", "Help"}; !sameStrings(got, want) {
		t.Fatalf("with an Edit menu: bar %q, want %q", got, want)
	}
	if e := s.Menus[1].Items; len(e) != 2 || e[0].Action != platform.MenuCopy || e[0].ID != uint16(mcCopy) || e[1].Action != platform.MenuPaste {
		t.Errorf("the model's Edit menu %+v: edit roles keep their ids", e)
	}

	s = nativeMenuSpec(nil)
	if got, want := barTitles(&s), []string{"Application", "Edit", "Window", "Help"}; !sameStrings(got, want) {
		t.Errorf("without a model: bar %q, want %q", got, want)
	}
}

// A standard Edit item without a command becomes the focused field's edit
// action; a command item a command.
func TestTranslateMenuCommands(t *testing.T) {
	tr := newTranslator()
	tr.event(&platform.Event{Kind: platform.EvCommand, Tag: uint64(platform.MenuSelectAll), Flags: platform.FlagStandard}, nil)
	tr.event(&platform.Event{Kind: platform.EvCommand, Tag: 7}, nil)
	in := &tr.in
	if in.NEdit != 1 || in.Edit[0] != ui.EditSelectAll {
		t.Errorf("edits %v", in.Edit[:in.NEdit])
	}
	if in.NCommands != 1 || in.Commands[0] != 7 {
		t.Errorf("commands %v", in.Commands[:in.NCommands])
	}
}

// The MenuState the platform asks: the model's state, and the standard edit
// items only with a text field focused; no allocation.
func TestMenuState(t *testing.T) {
	m := testMenuModel(t, true)
	a := &App{appObject: &appObject{}, UI: ui.New(nil), menu: m}
	m.SetEnabled(mcOpen, false)
	m.SetChecked(mcToggle, true)
	if on, _ := a.menuState(uint16(mcOpen), platform.MenuNone); on {
		t.Error("a disabled command is enabled")
	}
	if on, ch := a.menuState(uint16(mcToggle), platform.MenuNone); !on || !ch {
		t.Errorf("Toggle enabled %v checked %v", on, ch)
	}
	if on, _ := a.menuState(0, platform.MenuCopy); on {
		t.Error("a standard Copy is enabled without a focused field")
	}
	if n := testing.AllocsPerRun(100, func() { a.menuState(uint16(mcToggle), platform.MenuNone) }); n != 0 {
		t.Errorf("menuState allocates %.1f times", n)
	}
}

// The system's items are the model's to decide too: a disabled RoleZoom is
// disabled; while a drawn modal is open Quit is held back unless its command
// is InModal, and so are the standard system items without a command (Hide,
// Minimize …), like any command that is not.
func TestMenuStateOfSystemItems(t *testing.T) {
	m := testMenuModel(t, true)
	a := &App{appObject: &appObject{}, UI: ui.New(fakeui.New()), menu: m}
	var list gpu.DrawList
	frame := func(modal bool) {
		in := ui.Input{Focused: true}
		a.UI.Begin(&in, 400, 300, 1, 1.0/60)
		if modal {
			a.UI.BeginOverlay(1, ui.OverlayModal, [4]float32{0, 0, 400, 300}, ui.PlaceCenter)
			a.UI.EndOverlay()
		}
		a.UI.End(&list)
	}
	frame(false)
	m.SetEnabled(mcZoom, false)
	if on, _ := a.menuState(uint16(mcZoom), platform.MenuZoom); on {
		t.Error("a disabled RoleZoom is enabled")
	}
	for _, act := range []platform.MenuAction{platform.MenuHide, platform.MenuMinimize, platform.MenuClose, platform.MenuQuit} {
		if on, _ := a.menuState(0, act); !on {
			t.Errorf("standard item %d disabled without a modal", act)
		}
	}
	if on, _ := a.menuState(uint16(mcQuit), platform.MenuQuit); !on {
		t.Error("Quit disabled without a modal")
	}
	frame(true)
	frame(true)
	if on, _ := a.menuState(uint16(mcQuit), platform.MenuQuit); on {
		t.Error("an unmarked RoleQuit is enabled in a modal")
	}
	for _, act := range []platform.MenuAction{platform.MenuHide, platform.MenuMinimize, platform.MenuClose, platform.MenuQuit} {
		if on, _ := a.menuState(0, act); on {
			t.Errorf("standard item %d enabled in a modal", act)
		}
	}
	m2, err := menu.NewModel([]menu.Command{{ID: 1, Role: menu.RoleQuit, InModal: true}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.menu = m2
	if on, _ := a.menuState(1, platform.MenuQuit); !on {
		t.Error("a RoleQuit marked InModal is disabled in a modal")
	}
	// The field's claim is asked for every key-down: it allocates nothing.
	ev := platform.Event{Kind: platform.EvKey, Key: platform.KeyA + 'Z' - 'A', Mods: platform.ModSuper, Flags: platform.FlagDown}
	if n := testing.AllocsPerRun(100, func() { a.fieldTakesKey(&ev) }); n != 0 {
		t.Errorf("fieldTakesKey allocates %.1f times", n)
	}
}

func TestKeyEquivalent(t *testing.T) {
	cases := []struct {
		s    menu.Shortcut
		r    rune
		mods platform.Mods
	}{
		{menu.Shortcut{Key: 'S', Primary: true}, 's', platform.ModSuper},
		{menu.Shortcut{Key: 'Z', Mods: ui.ModShift, Primary: true}, 'z', platform.ModSuper | platform.ModShift},
		{menu.Shortcut{Key: ',', Primary: true}, ',', platform.ModSuper},
		{menu.Shortcut{Key: ui.KeyLeft, Mods: ui.ModAlt | ui.ModCtrl}, platform.MenuKeyLeft, platform.ModAlt | platform.ModCtrl},
		{menu.Shortcut{Key: ui.KeyEnter, Primary: true}, platform.MenuKeyEnter, platform.ModSuper},
		{menu.Shortcut{}, 0, 0},
		{menu.Shortcut{Key: 0x300, Primary: true}, 0, 0},
	}
	for _, c := range cases {
		if r, m := keyEquivalent(c.s); r != c.r || m != c.mods {
			t.Errorf("%+v: %q %v, want %q %v", c.s, r, m, c.r, c.mods)
		}
	}
}
