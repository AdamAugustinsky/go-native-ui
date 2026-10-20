//go:build darwin

package platform

import (
	"runtime"
	"testing"

	"gonativeui/internal/objc"
)

// The menus of the tests: an application menu, File (a command with a key
// equivalent and a symbol, a submenu), Edit (an edit command, a standard
// edit item without one), Window and Help.
const (
	tcSave     = 1
	tcCopy     = 4
	tcSettings = 9
	tcRecent   = 11
	tcRow      = 21
)

func testMenuSpec() *MenuSpec {
	sep := MenuItem{Kind: MenuItemSeparator}
	return &MenuSpec{
		Menus: []Menu{
			0: {Title: "App", Role: MenuRoleApp, Items: []MenuItem{{Action: MenuAbout}, sep,
				{ID: tcSettings, Title: "Settings…", Key: ',', Mods: ModSuper}, sep,
				{Kind: MenuItemSubmenu, Submenu: 5}, sep, {Action: MenuHide}, {Action: MenuQuit}}},
			1: {Title: "File", Items: []MenuItem{{ID: tcSave, Title: "Save", Key: 's', Mods: ModSuper, Symbol: "square.and.arrow.down"},
				{Kind: MenuItemSubmenu, Submenu: 6, Title: "Open Recent"}}},
			2: {Title: "Edit", Items: []MenuItem{{ID: tcCopy, Action: MenuCopy, Key: 'c', Mods: ModSuper}, {Action: MenuSelectAll}}},
			3: {Title: "Window", Role: MenuRoleWindow, Items: []MenuItem{{Action: MenuMinimize}, {Action: MenuZoom},
				{Action: MenuBringAllToFront, Key: 'b', Mods: ModSuper | ModAlt}}},
			4: {Title: "Help", Role: MenuRoleHelp},
			5: {Title: "Services", Role: MenuRoleServices},
			6: {Title: "Recent", Items: []MenuItem{{ID: tcRecent, Title: "notes.txt"}}},
			7: {Title: "Row", Items: []MenuItem{{ID: tcRow, Title: "Open"}, sep, {ID: tcRow + 1, Title: "Show Info", Symbol: "info.circle"},
				{Kind: MenuItemSubmenu, Submenu: 6}}},
		},
		Bar: []int{0, 1, 2, 3, 4},
	}
}

// menuRig is a real window with the test menus as the menu bar and a
// MenuState that records what it was asked.
type menuRig struct {
	w        *Window
	off      map[uint16]bool // disabled commands
	checked  map[uint16]bool
	asked    []uint16
	stdField bool // whether a standard edit item (id 0) is enabled
	modal    bool // a standard system item (id 0) is disabled
	record   bool
	field    func(e *Event) bool // FieldKeys, if set
}

func (r *menuRig) state(id uint16, act MenuAction) (bool, bool) {
	if r.record {
		r.asked = append(r.asked, id)
	}
	if id == 0 {
		if act.IsEdit() {
			return r.stdField, false
		}
		return !r.modal, false
	}
	return !r.off[id], r.checked[id]
}

// openMenuRig opens a window with the test menu bar, on the main thread; why
// is non-empty when the machine cannot.
func openMenuRig(spec *MenuSpec, hidden bool) (r *menuRig, why string) {
	w, err := Open(Options{Title: "menu test", Width: 400, Height: 300, Hidden: hidden})
	if err != nil {
		return nil, err.Error()
	}
	r = &menuRig{w: w, off: map[uint16]bool{}, checked: map[uint16]bool{}}
	w.SetMenuBar(spec, r.state)
	return r, ""
}

func str(id objc.ID) string { return string(objc.AppendUTF8(nil, id)) }

func itemAt(menu objc.ID, i int) objc.ID { return objc.Send1(menu, selItemAtIndex, uintptr(i)) }

func titleOf(item objc.ID) string { return str(objc.Send(item, objc.Sel("title"))) }

// The bar built from a spec, read back through AppKit: the menus in order,
// the system's application, Services, Window and Help menus, and each item's
// title, action, target, tag, key equivalent and symbol.
func TestMenuBarStructure(t *testing.T) {
	type itemInfo struct {
		title, key, action string
		mask               uintptr
		tag                uintptr
		ourTarget, nilTgt  bool
		image              bool
	}
	var r struct {
		why                         string
		bar                         []string
		save, copyItem, about, quit itemInfo
		selectAll, settings, recent itemInfo
		windows, help, services     bool
		editTitles                  []string
		emoji                       bool
		appTitles                   []string
		windowTitles                []string
	}
	onMain(func() {
		rig, why := openMenuRig(testMenuSpec(), true)
		if why != "" {
			r.why = why
			return
		}
		defer rig.w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		main := objc.Send(rig.w.app, objc.Sel("mainMenu"))
		n := int(objc.Send(main, selNumberOfItems))
		for i := 0; i < n; i++ {
			r.bar = append(r.bar, titleOf(itemAt(main, i)))
		}
		sub := func(i int) objc.ID { return objc.Send(itemAt(main, i), objc.Sel("submenu")) }
		info := func(item objc.ID) itemInfo {
			in := itemInfo{title: titleOf(item), key: str(objc.Send(item, objc.Sel("keyEquivalent"))),
				mask: uintptr(objc.Send(item, objc.Sel("keyEquivalentModifierMask"))), tag: uintptr(objc.Send(item, selTag)),
				image: objc.Send(item, objc.Sel("image")) != 0}
			if a := objc.SEL(objc.Send(item, selAction)); a != 0 {
				in.action = objc.SelName(a)
			}
			tg := objc.Send(item, objc.Sel("target"))
			in.ourTarget, in.nilTgt = tg == nativeMenus.target, tg == 0
			return in
		}
		app, file, edit := sub(0), sub(1), sub(2)
		r.about, r.settings, r.quit = info(itemAt(app, 0)), info(itemAt(app, 2)), info(itemAt(app, 7))
		for i := 0; i < int(objc.Send(app, selNumberOfItems)); i++ {
			r.appTitles = append(r.appTitles, titleOf(itemAt(app, i)))
		}
		r.save = info(itemAt(file, 0))
		r.recent = info(itemAt(objc.Send(itemAt(file, 1), objc.Sel("submenu")), 0))
		r.copyItem, r.selectAll = info(itemAt(edit, 0)), info(itemAt(edit, 1))
		// AppKit adds its own items to the menu titled Edit (Writing Tools,
		// AutoFill, Start Dictation, Emoji & Symbols, the last two with hidden
		// alternates for their other key equivalents).
		for i := 0; i < int(objc.Send(edit, selNumberOfItems)); i++ {
			it := itemAt(edit, i)
			if objc.SendBool(it, objc.Sel("isHidden")) {
				continue
			}
			r.editTitles = append(r.editTitles, titleOf(it))
			if a := objc.SEL(objc.Send(it, selAction)); a == objc.Sel("orderFrontCharacterPalette:") {
				r.emoji = true
			}
		}
		for i := 0; i < int(objc.Send(sub(3), selNumberOfItems)); i++ {
			if it := itemAt(sub(3), i); !objc.SendBool(it, objc.Sel("isHidden")) {
				r.windowTitles = append(r.windowTitles, titleOf(it))
			}
		}
		r.windows = objc.Send(rig.w.app, objc.Sel("windowsMenu")) == sub(3)
		r.help = objc.Send(rig.w.app, objc.Sel("helpMenu")) == sub(4)
		r.services = objc.Send(rig.w.app, objc.Sel("servicesMenu")) == objc.Send(itemAt(app, 4), objc.Sel("submenu"))
	})
	if r.why != "" {
		t.Skipf("cannot open a window here: %s", r.why)
	}
	if want := []string{"App", "File", "Edit", "Window", "Help"}; !equalStrings(r.bar, want) {
		t.Errorf("bar %q, want %q", r.bar, want)
	}
	cmdMask := uintptr(1 << 20)
	if s := r.save; s.title != "Save" || s.key != "s" || s.mask != cmdMask || s.action != "gnuiCommand:" || !s.ourTarget || s.tag != tcSave || !s.image {
		t.Errorf("Save: %+v", s)
	}
	if s := r.recent; s.title != "notes.txt" || s.action != "gnuiCommand:" || s.tag != tcRecent {
		t.Errorf("a submenu's item: %+v", s)
	}
	if s := r.copyItem; s.action != "copy:" || !s.nilTgt || s.tag != tcCopy || s.title != "Copy" || s.key != "c" {
		t.Errorf("Copy (an edit command through the responder chain): %+v", s)
	}
	if s := r.selectAll; s.action != "selectAll:" || !s.nilTgt || s.tag != 0 || s.title != "Select All" || s.key != "a" || s.mask != cmdMask {
		t.Errorf("Select All (standard, no command): %+v", s)
	}
	if s := r.about; s.action != "orderFrontStandardAboutPanel:" || !s.ourTarget || s.title != "About "+nativeMenus.appName {
		t.Errorf("About: %+v", s)
	}
	if s := r.settings; s.title != "Settings…" || s.key != "," || s.action != "gnuiCommand:" || s.tag != tcSettings {
		t.Errorf("Settings: %+v", s)
	}
	if s := r.quit; s.action != "gnuiQuit:" || !s.ourTarget || s.key != "q" || s.mask != cmdMask || s.title != "Quit "+nativeMenus.appName {
		t.Errorf("Quit: %+v", s)
	}
	if !r.windows || !r.help || !r.services {
		t.Errorf("system menus: Window %v, Help %v, Services %v", r.windows, r.help, r.services)
	}
	if !r.emoji {
		t.Error("AppKit added no Emoji & Symbols item to the Edit menu")
	}
	t.Logf("application menu %q; Edit menu with AppKit's items %q; Window menu %q", r.appTitles, r.editTitles, r.windowTitles)
}

// A rebuilt menu bar (a language or model change) keeps what AppKit attaches
// to particular menus: the Services menu (AppKit fills only the first one it
// was given), the Window menu with the window list (the window opened before
// the bar, as an app's does) and the Help menu, which gets the search field.
// Whatever bars earlier tests installed, every one is checked.
func TestMenuBarRebuildKeepsSystemMenus(t *testing.T) {
	type check struct {
		services, servicesInBar bool
		servicesMenu            objc.ID
		windows, help           bool
		windowItems             int
		appTitle                string
	}
	var r struct {
		why    string
		rounds []check
	}
	onMain(func() {
		spec := testMenuSpec()
		rig, why := openMenuRig(spec, false)
		if why != "" {
			r.why = why
			return
		}
		defer rig.w.Close()
		w := rig.w
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		for round := 0; round < 3; round++ {
			if round > 0 { // the same bar in another language
				spec = testMenuSpec()
				spec.Menus[1].Title, spec.Menus[5].Title, spec.Menus[3].Title = "Fichier", "Services FR", "Fenêtre"
				w.SetMenuBar(spec, rig.state)
			}
			main := objc.Send(w.app, objc.Sel("mainMenu"))
			sub := func(i int) objc.ID { return objc.Send(itemAt(main, i), objc.Sel("submenu")) }
			appMenu, windows := sub(0), sub(3)
			svc := objc.Send(itemAt(appMenu, 4), objc.Sel("submenu"))
			var c check
			c.services = svc != 0 && objc.Send(w.app, objc.Sel("servicesMenu")) == svc
			c.servicesInBar = objc.Send(svc, objc.Sel("supermenu")) == appMenu
			c.servicesMenu = svc
			c.windows = objc.Send(w.app, objc.Sel("windowsMenu")) == windows
			c.help = objc.Send(w.app, objc.Sel("helpMenu")) == sub(4)
			for i := 0; i < int(objc.Send(windows, selNumberOfItems)); i++ {
				it := itemAt(windows, i)
				if objc.SEL(objc.Send(it, selAction)) == objc.Sel("makeKeyAndOrderFront:") && objc.Send(it, objc.Sel("target")) == w.win {
					c.windowItems++
				}
			}
			c.appTitle = titleOf(itemAt(appMenu, 0))
			r.rounds = append(r.rounds, c)
		}
	})
	if r.why != "" {
		t.Skipf("cannot open a window here: %s", r.why)
	}
	for i, c := range r.rounds {
		if !c.services || !c.servicesInBar || !c.windows || !c.help {
			t.Errorf("bar %d: Services registered %v and in the application menu %v; Window menu %v; Help menu %v", i, c.services, c.servicesInBar, c.windows, c.help)
		}
		if c.windowItems != 1 {
			t.Errorf("bar %d: the Window menu lists the window %d times, want once", i, c.windowItems)
		}
		if c.servicesMenu != r.rounds[0].servicesMenu {
			t.Errorf("bar %d: a Services menu of its own (%#x, the first was %#x)", i, c.servicesMenu, r.rounds[0].servicesMenu)
		}
		if c.appTitle != "About "+nativeMenus.appName {
			t.Errorf("bar %d: application menu starts with %q", i, c.appTitle)
		}
	}
}

func equalStrings(a, b []string) bool {
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

// validateMenuItem: asks the app's MenuState, sets the check mark, and
// allocates nothing.
func TestMenuValidation(t *testing.T) {
	var r struct {
		why                          string
		saveOn, saveOff, copyOn      bool
		stdOff, stdOn                bool
		checkedState, uncheckedState uintptr
		allocs                       float64
		asked                        []uint16
	}
	onMain(func() {
		rig, why := openMenuRig(testMenuSpec(), true)
		if why != "" {
			r.why = why
			return
		}
		defer rig.w.Close()
		main := objc.Send(rig.w.app, objc.Sel("mainMenu"))
		file := objc.Send(itemAt(main, 1), objc.Sel("submenu"))
		edit := objc.Send(itemAt(main, 2), objc.Sel("submenu"))
		save, copyItem, selAll := itemAt(file, 0), itemAt(edit, 0), itemAt(edit, 1)
		target := nativeMenus.target
		rig.record = true
		r.saveOn = objc.SendBool1(target, selValidateItem, uintptr(save))
		rig.off[tcSave], rig.checked[tcSave] = true, true
		r.saveOff = !objc.SendBool1(target, selValidateItem, uintptr(save))
		r.checkedState = uintptr(objc.Send(save, selState))
		rig.checked[tcSave] = false
		objc.SendBool1(target, selValidateItem, uintptr(save))
		r.uncheckedState = uintptr(objc.Send(save, selState))
		r.copyOn = objc.SendBool1(rig.w.view, selValidateItem, uintptr(copyItem))
		r.stdOff = !objc.SendBool1(rig.w.view, selValidateItem, uintptr(selAll))
		rig.stdField = true
		r.stdOn = objc.SendBool1(rig.w.view, selValidateItem, uintptr(selAll))
		r.asked = rig.asked
		rig.record = false
		r.allocs = testing.AllocsPerRun(200, func() {
			objc.SendBool1(target, selValidateItem, uintptr(save))
			objc.SendBool1(rig.w.view, selValidateItem, uintptr(copyItem))
		})
	})
	if r.why != "" {
		t.Skipf("cannot open a window here: %s", r.why)
	}
	if !r.saveOn || !r.saveOff {
		t.Errorf("Save enabled %v, then disabled %v", r.saveOn, r.saveOff)
	}
	if r.checkedState != 1 || r.uncheckedState != 0 {
		t.Errorf("check mark %d then %d, want 1 then 0", r.checkedState, r.uncheckedState)
	}
	if !r.copyOn || !r.stdOff || !r.stdOn {
		t.Errorf("Copy on %v; standard Select All off without a field %v, on with one %v", r.copyOn, r.stdOff, r.stdOn)
	}
	if want := []uint16{tcSave, tcSave, tcSave, tcCopy, 0, 0}; len(r.asked) != len(want) {
		t.Errorf("asked about %v, want %v", r.asked, want)
	}
	if r.allocs != 0 {
		t.Errorf("validation allocates %.1f times", r.allocs)
	}
}

// keyDown makes a key-down NSEvent for the window: chars typed with
// modifier flags (NSEventModifierFlags) on the key with virtual code code.
func keyDown(w *Window, chars string, flags uintptr, code uint16) objc.ID {
	// everything that sends a message first: the call is assembled in the same frame
	s := objc.Str(chars)
	num := objc.Send(w.win, objc.Sel("windowNumber"))
	cls := objc.Cls("NSEvent")
	sel := objc.Sel("keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:charactersIgnoringModifiers:isARepeat:keyCode:")
	f := objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).Arg(10).ArgPoint(10, 10).Arg(flags).ArgF64(1).Arg(uintptr(num)).Arg(0).Arg(uintptr(s)).Arg(uintptr(s))
	if runtime.GOARCH == "arm64" {
		f.Arg32(uint32(code) << 16) // isARepeat (NO) at byte 0, the keyCode at byte 2 of the packed stack
	} else {
		f.Arg(0).Arg(uintptr(code))
	}
	return objc.ID(f.Call(objc.MsgSend).R0())
}

// count tallies the key-downs and commands queued.
func countEvents(q []Event) (keys int, cmds []uint64, closes int) {
	for _, e := range q {
		switch {
		case e.Kind == EvKey && e.Flags&FlagDown != 0:
			keys++
		case e.Kind == EvCommand:
			cmds = append(cmds, e.Tag)
		case e.Kind == EvClose:
			closes++
		}
	}
	return
}

// A key equivalent goes through AppKit's menu: the command arrives once as
// EvCommand and the key-down is not queued as well; a disabled item leaves
// the key to the app; ⌘Q asks to close; an item whose action is AppKit's
// takes its key all the same; an edit key equivalent reaches our
// view through the responder chain when the window is key.
func TestKeyEquivalentFiresOnce(t *testing.T) {
	var r struct {
		why                     string
		keys, offKeys, quitKeys int
		frontKeys               int
		cmds, offCmds           []uint64
		closes                  int
		key                     bool
		copyKeys                int
		copyCmds                []uint64
	}
	onMain(func() {
		rig, why := openMenuRig(testMenuSpec(), false)
		if why != "" {
			r.why = why
			return
		}
		defer rig.w.Close()
		w := rig.w
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		cmd := uintptr(1 << 20)
		w.q.reset()
		w.handle(keyDown(w, "s", cmd, 0x01))
		r.keys, r.cmds, _ = countEvents(w.q.ev)
		w.q.reset()
		rig.off[tcSave] = true
		w.handle(keyDown(w, "s", cmd, 0x01))
		r.offKeys, r.offCmds, _ = countEvents(w.q.ev)
		w.q.reset()
		w.handle(keyDown(w, "q", cmd, 0x0C))
		r.quitKeys, _, r.closes = countEvents(w.q.ev)
		w.q.reset()
		w.handle(keyDown(w, "b", cmd|1<<19, 0x0B)) // ⌥⌘B: Bring All to Front, an action of AppKit's own
		r.frontKeys, _, _ = countEvents(w.q.ev)
		r.key = objc.SendBool(w.win, objc.Sel("isKeyWindow"))
		if r.key {
			w.q.reset()
			w.handle(keyDown(w, "c", cmd, 0x08))
			r.copyKeys, r.copyCmds, _ = countEvents(w.q.ev)
		}
		w.q.reset()
	})
	if r.why != "" {
		t.Skipf("cannot open a window here: %s", r.why)
	}
	if r.keys != 0 || len(r.cmds) != 1 || r.cmds[0] != tcSave {
		t.Errorf("⌘S: %d key-downs and commands %v, want none and [%d]", r.keys, r.cmds, tcSave)
	}
	if r.offKeys != 1 || len(r.offCmds) != 0 {
		t.Errorf("⌘S with Save disabled: %d key-downs and commands %v, want the key and no command", r.offKeys, r.offCmds)
	}
	if r.quitKeys != 0 || r.closes != 1 {
		t.Errorf("⌘Q: %d key-downs and %d close requests, want none and one", r.quitKeys, r.closes)
	}
	if r.frontKeys != 0 {
		t.Errorf("⌥⌘B (AppKit's arrangeInFront:): %d key-downs, want none", r.frontKeys)
	}
	if !r.key {
		t.Log("the window is not key here: the responder-chain case is not checked")
	} else if r.copyKeys != 0 || len(r.copyCmds) != 1 || r.copyCmds[0] != tcCopy {
		t.Errorf("⌘C: %d key-downs and commands %v, want none and [%d]", r.copyKeys, r.copyCmds, tcCopy)
	}
}

// A key-down the focused field takes (FieldKeys) is not a key equivalent: the
// item bound to it does not act, and the key is the app's.
func TestFieldKeyIsNoKeyEquivalent(t *testing.T) {
	var r struct {
		why        string
		keys       int
		cmds       []uint64
		freeKeys   int
		freeCmds   []uint64
		afterField bool
	}
	onMain(func() {
		rig, why := openMenuRig(testMenuSpec(), false)
		if why != "" {
			r.why = why
			return
		}
		defer rig.w.Close()
		w := rig.w
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		w.SetFieldKeys(func(e *Event) bool { return e.Key == KeyA+'S'-'A' && e.Mods == ModSuper })
		defer w.SetFieldKeys(nil)
		w.q.reset()
		w.handle(keyDown(w, "s", 1<<20, 0x01))
		r.keys, r.cmds, _ = countEvents(w.q.ev)
		r.afterField = w.fieldKey
		w.SetFieldKeys(func(*Event) bool { return false })
		w.q.reset()
		w.handle(keyDown(w, "s", 1<<20, 0x01))
		r.freeKeys, r.freeCmds, _ = countEvents(w.q.ev)
		w.q.reset()
	})
	if r.why != "" {
		t.Skipf("cannot open a window here: %s", r.why)
	}
	if r.keys != 1 || len(r.cmds) != 0 {
		t.Errorf("⌘S the field takes: %d key-downs and commands %v, want the key and no command", r.keys, r.cmds)
	}
	if r.afterField {
		t.Error("the field's claim outlived its key")
	}
	if r.freeKeys != 0 || len(r.freeCmds) != 1 || r.freeCmds[0] != tcSave {
		t.Errorf("⌘S the field leaves: %d key-downs and commands %v, want none and [%d]", r.freeKeys, r.freeCmds, tcSave)
	}
}

// The system's items (Zoom, Close, Minimize, Quit) are the app's to decide,
// command identity and all, and AppKit's action follows only when the app
// enables them: a disabled Zoom does not zoom and leaves its key; a modal
// holds back the standard Minimize; a disabled Quit asks nothing.
func TestSystemItemsFollowTheApp(t *testing.T) {
	const tcQuit, tcZoom, tcClose = 33, 31, 32
	spec := &MenuSpec{
		Menus: []Menu{
			{Title: "App", Role: MenuRoleApp, Items: []MenuItem{{Action: MenuQuit, ID: tcQuit}}},
			{Title: "Window", Role: MenuRoleWindow, Items: []MenuItem{{Action: MenuZoom, ID: tcZoom, Key: 'z', Mods: ModCtrl | ModSuper},
				{Action: MenuClose, ID: tcClose}, {Action: MenuMinimize}}},
		},
		Bar: []int{0, 1},
	}
	type step struct {
		keys, closes int
		frame        [4]float64
		mini         bool
		visible      bool
	}
	var r struct {
		why                        string
		before, off, on, back      step
		quit, minimize, closeOff   step
		asked                      []uint16
		zoomOn, zoomOff, zoomValid bool
		target                     bool
	}
	onMain(func() {
		rig, why := openMenuRig(spec, false)
		if why != "" {
			r.why = why
			return
		}
		defer rig.w.Close()
		w := rig.w
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		cmd, ctrl := uintptr(1<<20), uintptr(1<<18)
		snap := func() step {
			var s step
			s.keys, _, s.closes = countEvents(w.q.ev)
			f := objc.SendRect(w.win, selFrame)
			s.frame = [4]float64{f.X, f.Y, f.W, f.H}
			s.mini = objc.SendBool(w.win, objc.Sel("isMiniaturized"))
			s.visible = objc.SendBool(w.win, objc.Sel("isVisible"))
			w.q.reset()
			return s
		}
		w.q.reset()
		r.before = snap()
		zoom := itemAt(objc.Send(itemAt(objc.Send(w.app, objc.Sel("mainMenu")), 1), objc.Sel("submenu")), 0)
		r.target = objc.Send(zoom, objc.Sel("target")) == nativeMenus.target
		rig.record = true
		rig.off[tcZoom] = true
		r.zoomOff = objc.SendBool1(nativeMenus.target, selValidateItem, uintptr(zoom))
		w.handle(keyDown(w, "z", cmd|ctrl, 0x06))
		r.off = snap()
		rig.off[tcZoom] = false
		r.zoomOn = objc.SendBool1(nativeMenus.target, selValidateItem, uintptr(zoom))
		r.zoomValid = systemValid(zoom, actionSel[MenuZoom])
		w.handle(keyDown(w, "z", cmd|ctrl, 0x06))
		r.on = snap()
		w.handle(keyDown(w, "z", cmd|ctrl, 0x06)) // zoom back
		r.back = snap()
		rig.off[tcQuit] = true
		w.handle(keyDown(w, "q", cmd, 0x0C))
		r.quit = snap()
		rig.modal = true
		w.handle(keyDown(w, "m", cmd, 0x2E))
		r.minimize = snap()
		rig.modal = false
		rig.off[tcClose] = true
		w.handle(keyDown(w, "w", cmd, 0x0D))
		r.closeOff = snap()
		r.asked = rig.asked
		rig.record = false
	})
	if r.why != "" {
		t.Skipf("cannot open a window here: %s", r.why)
	}
	if !r.target {
		t.Error("Zoom does not target GNUIMenuTarget")
	}
	// AppKit's own target for performZoom: is the key (or main) window: where
	// the window cannot become key (a locked screen) AppKit disables Zoom
	// whatever the app says.
	if r.zoomOff || r.zoomOn != r.zoomValid {
		t.Errorf("Zoom validates %v disabled, %v enabled; want false, and AppKit's own verdict %v", r.zoomOff, r.zoomOn, r.zoomValid)
	}
	if r.off.keys != 1 || r.off.frame != r.before.frame {
		t.Errorf("⌃⌘Z with Zoom disabled: %d key-downs, frame %v → %v; want the key and no zoom", r.off.keys, r.before.frame, r.off.frame)
	}
	if !r.zoomValid {
		t.Log("the window is not key here: AppKit has no target for Zoom, so the zoom itself is not checked")
	} else if r.on.keys != 0 || r.on.frame == r.before.frame || r.back.frame != r.before.frame {
		t.Errorf("⌃⌘Z with Zoom enabled: %d key-downs, frame %v → %v → %v; want no key, a zoom and back", r.on.keys, r.before.frame, r.on.frame, r.back.frame)
	}
	if r.quit.keys != 1 || r.quit.closes != 0 {
		t.Errorf("⌘Q with Quit disabled: %d key-downs, %d close requests; want the key and none", r.quit.keys, r.quit.closes)
	}
	if r.minimize.keys != 1 || r.minimize.mini {
		t.Errorf("⌘M in a modal: %d key-downs, minimised %v; want the key and no minimise", r.minimize.keys, r.minimize.mini)
	}
	if r.closeOff.keys != 1 || r.closeOff.closes != 0 || !r.closeOff.visible {
		t.Errorf("⌘W with Close disabled: %+v; want the key, no close and the window shown", r.closeOff)
	}
	var sawZoom, sawQuit, sawClose bool
	for _, id := range r.asked {
		sawZoom, sawQuit, sawClose = sawZoom || id == tcZoom, sawQuit || id == tcQuit, sawClose || id == tcClose
	}
	if !sawZoom || !sawQuit || !sawClose {
		t.Errorf("the app was asked about %v, want Zoom, Quit and Close by their commands", r.asked)
	}
}

// Popped-up menus: the context menu of a model menu delivers its command,
// a list delivers the chosen index (PopUpMenu really tracks; the item is
// chosen from inside the tracking loop), the tree is cached until its items
// change, and a menu item chosen with performActionForItemAtIndex: arrives
// once.
func TestPopUpMenus(t *testing.T) {
	var r struct {
		why               string
		ctxIdx, listIdx   int
		ctxCmds, listCmds []uint64
		subIdx            int
		subCmds           []uint64
		subTitles         []string
		sameTree, newTree bool
		listChecked       []uintptr
		performCmds       []uint64
		tracked           bool
		edit              []Event
	}
	onMain(func() {
		spec := testMenuSpec()
		rig, why := openMenuRig(spec, false)
		if why != "" {
			r.why = why
			return
		}
		defer rig.w.Close()
		w := rig.w
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		choose, chooseSub := -1, -1
		menuOpenHook = func(m objc.ID) {
			r.tracked = true
			if choose >= 0 {
				objc.Send1(m, objc.Sel("performActionForItemAtIndex:"), uintptr(choose))
			}
			if chooseSub >= 0 { // an item of the submenu of item chooseSub
				sub := objc.Send(itemAt(m, chooseSub), objc.Sel("submenu"))
				objc.Send1(sub, objc.Sel("performActionForItemAtIndex:"), 0)
			}
			objc.Send(m, objc.Sel("cancelTracking"))
		}
		defer func() { menuOpenHook = nil }()

		w.q.reset()
		choose = 2
		r.ctxIdx = w.PopUpMenu(&Popup{Spec: spec, Menu: 7, Kind: PopupContext, X0: 40, Y0: 40, X1: 40, Y1: 40})
		_, r.ctxCmds, _ = countEvents(w.q.ev)

		// an item of a submenu: its command, and no index of the menu popped up
		w.q.reset()
		choose, chooseSub = -1, 3
		r.subIdx = w.PopUpMenu(&Popup{Spec: spec, Menu: 7, Kind: PopupContext, X0: 40, Y0: 40, X1: 40, Y1: 40})
		_, r.subCmds, _ = countEvents(w.q.ev)
		chooseSub = -1
		// the submenu item shows its menu's title: a new title is a new tree
		r.subTitles = append(r.subTitles, titleOf(itemAt(popupFor(spec, 7).menu, 3)))
		spec.Menus[6].Title = "Recent Files"
		r.subTitles = append(r.subTitles, titleOf(itemAt(popupFor(spec, 7).menu, 3)))
		spec.Menus[6].Title = "Recent"

		list := &MenuSpec{Menus: []Menu{{Items: []MenuItem{{Title: "Plain text"}, {Title: "Markdown"}, {Title: "HTML"}}}}}
		w.q.reset()
		choose = 1
		r.listIdx = w.PopUpMenu(&Popup{Spec: list, Menu: 0, Kind: PopupList, X0: 20, Y0: 20, X1: 200, Y1: 44, Selected: 2})
		_, r.listCmds, _ = countEvents(w.q.ev)
		pm := popupFor(list, 0)
		for i := 0; i < 3; i++ {
			r.listChecked = append(r.listChecked, uintptr(objc.Send(itemAt(pm.menu, i), selState)))
		}
		r.sameTree = popupFor(list, 0).menu == pm.menu
		list.Menus[0].Items[1].Title = "Rich text"
		r.newTree = titleOf(itemAt(popupFor(list, 0).menu, 1)) == "Rich text"

		// performActionForItemAtIndex: on the cached context menu, outside tracking
		w.q.reset()
		row := popupFor(spec, 7).menu
		objc.Send1(row, objc.Sel("performActionForItemAtIndex:"), 0)
		_, r.performCmds, _ = countEvents(w.q.ev)

		// a standard edit item without a command, sent through the view
		w.q.reset()
		edit := objc.Send(itemAt(objc.Send(w.app, objc.Sel("mainMenu")), 2), objc.Sel("submenu"))
		objc.Send1(w.view, actionSel[MenuSelectAll], uintptr(itemAt(edit, 1)))
		r.edit = append(r.edit, w.q.ev...)
		w.q.reset()
	})
	if r.why != "" {
		t.Skipf("cannot open a window here: %s", r.why)
	}
	if !r.tracked {
		t.Fatal("PopUpMenu did not open the menu")
	}
	if r.ctxIdx != 2 || len(r.ctxCmds) != 1 || r.ctxCmds[0] != tcRow+1 {
		t.Errorf("context menu: chose %d, commands %v; want 2 and [%d]", r.ctxIdx, r.ctxCmds, tcRow+1)
	}
	if r.subIdx != -1 || len(r.subCmds) != 1 || r.subCmds[0] != tcRecent {
		t.Errorf("a submenu's item: chose %d, commands %v; want -1 and [%d]", r.subIdx, r.subCmds, tcRecent)
	}
	if !equalStrings(r.subTitles, []string{"Recent", "Recent Files"}) {
		t.Errorf("a submenu item's title %q: the cache missed its menu's new title", r.subTitles)
	}
	if r.listIdx != 1 || len(r.listCmds) != 0 {
		t.Errorf("list: chose %d, commands %v; want 1 and none", r.listIdx, r.listCmds)
	}
	if len(r.listChecked) != 3 || r.listChecked[2] != 1 || r.listChecked[0] != 0 {
		t.Errorf("list check marks %v, want the selected one", r.listChecked)
	}
	if !r.sameTree || !r.newTree {
		t.Errorf("cache: reused when unchanged %v, rebuilt when changed %v", r.sameTree, r.newTree)
	}
	if len(r.performCmds) != 1 || r.performCmds[0] != tcRow {
		t.Errorf("performActionForItemAtIndex: delivered %v, want [%d]", r.performCmds, tcRow)
	}
	if len(r.edit) != 1 || r.edit[0].Kind != EvCommand || r.edit[0].Flags&FlagStandard == 0 || MenuAction(r.edit[0].Tag) != MenuSelectAll {
		t.Errorf("standard Select All delivered %+v", r.edit)
	}
}

// A key equivalent with Shift fires its command from the key-down of a real
// ⇧⌘ chord. AppKit makes such an event with characters "z" (⌘ takes Shift off
// them) and charactersIgnoringModifiers "Z" (+[NSEvent eventWithCGEvent:] of a
// keyboard CGEvent with those flags, 2026-10-20, macOS 27), and NSMenu matches an
// item against the latter, case-sensitively: an item made with the lowercase
// letter and Shift in its mask ("z") never matched, so Edit ▸ Redo did not act
// on ⇧⌘Z for a hosted text view, which does not hand keys back to the app to
// match. The item's letter is the uppercase one, as AppKit's own Redo is.
func TestShiftKeyEquivalentsMatchTheRealChord(t *testing.T) {
	const tcRedo, tcSaveAs = 31, 32
	spec := &MenuSpec{
		Menus: []Menu{
			0: {Title: "App", Role: MenuRoleApp, Items: []MenuItem{{Action: MenuQuit}}},
			1: {Title: "File", Items: []MenuItem{{ID: tcSaveAs, Title: "Save As…", Key: 's', Mods: ModSuper | ModShift}}},
			2: {Title: "Edit", Items: []MenuItem{{ID: tcRedo, Title: "Redo", Key: 'z', Mods: ModSuper | ModShift}}},
		},
		Bar: []int{0, 1, 2},
	}
	type res struct {
		keys int
		cmds []uint64
	}
	var got [2]res
	var why string
	cases := []struct {
		chars, ignoring string
		code            uint16
		want            uint64
	}{
		{"z", "Z", 0x06, tcRedo}, {"s", "S", 0x01, tcSaveAs},
	}
	onMain(func() {
		rig, y := openMenuRig(spec, false)
		if y != "" {
			why = y
			return
		}
		defer rig.w.Close()
		w := rig.w
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		for i, c := range cases {
			w.q.reset()
			w.handle(nsKey(w, 10, c.chars, c.ignoring, 1<<20|1<<17, c.code, 1))
			got[i].keys, got[i].cmds, _ = countEvents(w.q.ev)
			w.q.reset()
		}
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	for i, c := range cases {
		if r := got[i]; r.keys != 0 || len(r.cmds) != 1 || r.cmds[0] != c.want {
			t.Errorf("⇧⌘ event %q/%q: %d key-downs and commands %v, want none and [%d]", c.chars, c.ignoring, r.keys, r.cmds, c.want)
		}
	}
}
