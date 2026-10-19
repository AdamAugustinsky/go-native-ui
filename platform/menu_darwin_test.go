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
			3: {Title: "Window", Role: MenuRoleWindow, Items: []MenuItem{{Action: MenuMinimize}, {Action: MenuZoom}}},
			4: {Title: "Help", Role: MenuRoleHelp},
			5: {Title: "Services", Role: MenuRoleServices},
			6: {Title: "Recent", Items: []MenuItem{{ID: tcRecent, Title: "notes.txt"}}},
			7: {Title: "Row", Items: []MenuItem{{ID: tcRow, Title: "Open"}, sep, {ID: tcRow + 1, Title: "Show Info", Symbol: "info.circle"}}},
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
	record   bool
}

func (r *menuRig) state(id uint16, act MenuAction) (bool, bool) {
	if r.record {
		r.asked = append(r.asked, id)
	}
	if id == 0 {
		return act.IsEdit() && r.stdField, false
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
	if s := r.about; s.action != "orderFrontStandardAboutPanel:" || !s.nilTgt || s.title != "About "+nativeMenus.appName {
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
	t.Logf("application menu %q; Edit menu with AppKit's items %q", r.appTitles, r.editTitles)
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
// the key to the app; ⌘Q asks to close; an edit key equivalent reaches our
// view through the responder chain when the window is key.
func TestKeyEquivalentFiresOnce(t *testing.T) {
	var r struct {
		why                     string
		keys, offKeys, quitKeys int
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
	if !r.key {
		t.Log("the window is not key here: the responder-chain case is not checked")
	} else if r.copyKeys != 0 || len(r.copyCmds) != 1 || r.copyCmds[0] != tcCopy {
		t.Errorf("⌘C: %d key-downs and commands %v, want none and [%d]", r.copyKeys, r.copyCmds, tcCopy)
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
		choose := -1
		menuOpenHook = func(m objc.ID) {
			r.tracked = true
			if choose >= 0 {
				objc.Send1(m, objc.Sel("performActionForItemAtIndex:"), uintptr(choose))
			}
			objc.Send(m, objc.Sel("cancelTracking"))
		}
		defer func() { menuOpenHook = nil }()

		w.q.reset()
		choose = 2
		r.ctxIdx = w.PopUpMenu(&Popup{Spec: spec, Menu: 7, Kind: PopupContext, X0: 40, Y0: 40, X1: 40, Y1: 40})
		_, r.ctxCmds, _ = countEvents(w.q.ev)

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
