package menu

import "gonativeui/platform"

// Strings are the titles the framework gives the standard commands and menus
// when the app gives none: a command with a Role and an empty Title, in the
// drawn menus, the tray's and the native ones (whose application menu,
// standard Edit, Window and Help menus and Services submenu the app layer
// adds itself). English is the default, in macOS's words;
// rexui.StringsPtBR.Menu is Brazilian Portuguese. A title the app gives always
// wins.
type Strings struct {
	// About, Hide and Quit title the commands of those roles in a drawn
	// menu or the tray's; AboutApp, HideApp and QuitApp title them in the
	// macOS application menu, where %s is the application's name.
	About, Hide, Quit          string
	AboutApp, HideApp, QuitApp string
	// HideOthers, ShowAll and Settings are the application menu's others.
	HideOthers, ShowAll, Settings string
	// Undo … SelectAll are the edit commands.
	Undo, Redo, Cut, Copy, Paste, PasteAndMatchStyle, Delete, SelectAll string
	// Minimize, Zoom, Close (the File menu's, on macOS), CloseWindow (a
	// drawn menu's), BringAllToFront and EnterFullScreen act on windows.
	Minimize, Zoom, Close, CloseWindow, BringAllToFront, EnterFullScreen string
	// Services, Edit, Window and Help title the menus of those names.
	Services, Edit, Window, Help string
}

// StringsEnglish are the default Strings.
var StringsEnglish = Strings{
	About: "About", Hide: "Hide", Quit: "Quit",
	AboutApp: "About %s", HideApp: "Hide %s", QuitApp: "Quit %s",
	HideOthers: "Hide Others", ShowAll: "Show All", Settings: "Settings…",
	Undo: "Undo", Redo: "Redo", Cut: "Cut", Copy: "Copy", Paste: "Paste",
	PasteAndMatchStyle: "Paste and Match Style", Delete: "Delete", SelectAll: "Select All",
	Minimize: "Minimize", Zoom: "Zoom", Close: "Close", CloseWindow: "Close Window",
	BringAllToFront: "Bring All to Front", EnterFullScreen: "Enter Full Screen",
	Services: "Services", Edit: "Edit", Window: "Window", Help: "Help",
}

// words are the Strings in use.
var words = StringsEnglish

// SetStrings makes s the titles of the standard commands and menus; a field
// left empty keeps its English. It sets the native menus' titles of the
// standard actions too (platform.SetMenuTitles). Call it before the first
// frame (native menus already built keep their titles until rebuilt), on the
// UI goroutine. rexui.SetStrings calls it with the rest of rexui's words.
func SetStrings(s Strings) {
	en := &StringsEnglish
	for _, f := range [...]struct {
		v  *string
		en string
	}{
		{&s.About, en.About}, {&s.Hide, en.Hide}, {&s.Quit, en.Quit},
		{&s.AboutApp, en.AboutApp}, {&s.HideApp, en.HideApp}, {&s.QuitApp, en.QuitApp},
		{&s.HideOthers, en.HideOthers}, {&s.ShowAll, en.ShowAll}, {&s.Settings, en.Settings},
		{&s.Undo, en.Undo}, {&s.Redo, en.Redo}, {&s.Cut, en.Cut}, {&s.Copy, en.Copy}, {&s.Paste, en.Paste},
		{&s.PasteAndMatchStyle, en.PasteAndMatchStyle}, {&s.Delete, en.Delete}, {&s.SelectAll, en.SelectAll},
		{&s.Minimize, en.Minimize}, {&s.Zoom, en.Zoom}, {&s.Close, en.Close}, {&s.CloseWindow, en.CloseWindow},
		{&s.BringAllToFront, en.BringAllToFront}, {&s.EnterFullScreen, en.EnterFullScreen},
		{&s.Services, en.Services}, {&s.Edit, en.Edit}, {&s.Window, en.Window}, {&s.Help, en.Help},
	} {
		if *f.v == "" {
			*f.v = f.en
		}
	}
	words = s
	var t platform.MenuTitles
	t[platform.MenuAbout], t[platform.MenuHide], t[platform.MenuQuit] = s.AboutApp, s.HideApp, s.QuitApp
	t[platform.MenuHideOthers], t[platform.MenuShowAll] = s.HideOthers, s.ShowAll
	t[platform.MenuUndo], t[platform.MenuRedo], t[platform.MenuCut], t[platform.MenuCopy] = s.Undo, s.Redo, s.Cut, s.Copy
	t[platform.MenuPaste], t[platform.MenuPasteAndMatchStyle] = s.Paste, s.PasteAndMatchStyle
	t[platform.MenuDelete], t[platform.MenuSelectAll] = s.Delete, s.SelectAll
	t[platform.MenuMinimize], t[platform.MenuZoom], t[platform.MenuClose] = s.Minimize, s.Zoom, s.Close
	t[platform.MenuBringAllToFront], t[platform.MenuFullScreen] = s.BringAllToFront, s.EnterFullScreen
	platform.SetMenuTitles(t)
}

// CurrentStrings returns the Strings in use.
func CurrentStrings() Strings { return words }

// RoleTitle is the title of a command of role r that has no title of its
// own, in the Strings in use: "Copy", "Quit" ("" for RoleNone and the roles
// whose commands always have a title).
func RoleTitle(r Role) string {
	s := &words
	switch r {
	case RoleAbout:
		return s.About
	case RoleQuit:
		return s.Quit
	case RoleHide:
		return s.Hide
	case RoleHideOthers:
		return s.HideOthers
	case RoleShowAll:
		return s.ShowAll
	case RoleUndo:
		return s.Undo
	case RoleRedo:
		return s.Redo
	case RoleCut:
		return s.Cut
	case RoleCopy:
		return s.Copy
	case RolePaste:
		return s.Paste
	case RolePasteAndMatchStyle:
		return s.PasteAndMatchStyle
	case RoleSelectAll:
		return s.SelectAll
	case RoleDelete:
		return s.Delete
	case RoleMinimize:
		return s.Minimize
	case RoleZoom:
		return s.Zoom
	case RoleCloseWindow:
		return s.CloseWindow
	case RoleFullScreen:
		return s.EnterFullScreen
	case RoleSettings:
		return s.Settings
	}
	return ""
}
