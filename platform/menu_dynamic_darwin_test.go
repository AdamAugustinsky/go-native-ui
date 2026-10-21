//go:build darwin

package platform

import (
	"testing"

	"gonativeui/internal/objc"
)

// dynamicSpec is a context menu made up for one click: an action with a key
// equivalent, a checked and a mixed one, a disabled one, a separator, a
// destructive one and a submenu of labels with one checked.
func dynamicSpec() *MenuSpec {
	return &MenuSpec{Menus: []Menu{
		0: {Dynamic: true, Items: []MenuItem{
			{Title: "Mark as unread", Choice: 10, Key: 'u', Mods: ModSuper | ModShift, Symbol: "envelope.badge"},
			{Title: "Pinned", Choice: 11, Checked: true},
			{Title: "Some muted", Choice: 12, Mixed: true},
			{Title: "Archive", Choice: 13, Disabled: true},
			{Kind: MenuItemSeparator},
			{Kind: MenuItemSubmenu, Submenu: 1, Title: "Labels"},
			{Title: "Delete chat", Choice: 99, Destructive: true},
		}},
		1: {Dynamic: true, Items: []MenuItem{
			{Title: "Family", Choice: 20, Checked: true},
			{Title: "Work", Choice: 21},
		}},
	}}
}

// A menu described at the call site is a real NSMenu: titles, the separator,
// the submenu, enabled, the check and mixed marks, the key equivalent and the
// red title read back through AppKit while it is open; a chosen item (also in
// a submenu) reports its choice; dismissing reports none; a disabled item
// cannot be chosen.
func TestDynamicPopUpMenu(t *testing.T) {
	type info struct {
		title                         string
		enabled, sep, sub, attributed bool
		state                         int
		key                           string
	}
	var r struct {
		why                  string
		tracked              bool
		items, subItems      []info
		autoenables          bool
		chosen, subChosen    Popup
		dismissed, disabled  Popup
		disabledCmds, events int
	}
	onMain(func() {
		spec := dynamicSpec()
		rig, why := openMenuRig(testMenuSpec(), false)
		if why != "" {
			r.why = why
			return
		}
		defer rig.w.Close()
		w := rig.w
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		read := func(m objc.ID) []info {
			var out []info
			for i := 0; i < int(objc.Send(m, selNumberOfItems)); i++ {
				it := itemAt(m, i)
				out = append(out, info{
					title:      titleOf(it),
					enabled:    objc.SendBool(it, objc.Sel("isEnabled")),
					sep:        objc.SendBool(it, objc.Sel("isSeparatorItem")),
					sub:        objc.Send(it, objc.Sel("submenu")) != 0,
					attributed: objc.Send(it, objc.Sel("attributedTitle")) != 0,
					state:      int(objc.Send(it, selState)),
					key:        str(objc.Send(it, objc.Sel("keyEquivalent"))),
				})
			}
			return out
		}
		action, subAction, inspect := -1, -1, false
		menuOpenHook = func(m objc.ID) {
			r.tracked = true
			if inspect {
				r.items = read(m)
				r.autoenables = objc.SendBool(m, objc.Sel("autoenablesItems"))
				r.subItems = read(objc.Send(itemAt(m, 5), objc.Sel("submenu")))
			}
			if action >= 0 {
				objc.Send1(m, objc.Sel("performActionForItemAtIndex:"), uintptr(action))
			}
			if subAction >= 0 {
				sub := objc.Send(itemAt(m, 5), objc.Sel("submenu"))
				objc.Send1(sub, objc.Sel("performActionForItemAtIndex:"), uintptr(subAction))
			}
			objc.Send(m, objc.Sel("cancelTracking"))
		}
		defer func() { menuOpenHook = nil }()
		pop := func() Popup {
			p := Popup{Spec: spec, Menu: 0, Kind: PopupContext, X0: 40, Y0: 40, X1: 40, Y1: 40}
			w.q.reset()
			w.PopUpMenu(&p)
			r.events += len(w.q.ev)
			return p
		}

		inspect = true
		r.dismissed = pop()
		inspect = false

		action = 6 // Delete chat
		r.chosen = pop()
		action, subAction = -1, 1 // Labels ▸ Work
		r.subChosen = pop()
		subAction, action = -1, 3 // the disabled Archive
		r.disabled = pop()
	})
	if r.why != "" {
		t.Skipf("cannot open a window here: %s", r.why)
	}
	if !r.tracked {
		t.Fatal("PopUpMenu did not open the menu")
	}
	want := []info{
		{title: "Mark as unread", enabled: true, key: "U"},
		{title: "Pinned", enabled: true, state: 1},
		{title: "Some muted", enabled: true, state: -1},
		{title: "Archive", enabled: false},
		{sep: true},
		{title: "Labels", enabled: true, sub: true},
		{title: "Delete chat", enabled: true, attributed: true},
	}
	for i, w := range want {
		if i >= len(r.items) || r.items[i] != w {
			var got info
			if i < len(r.items) {
				got = r.items[i]
			}
			t.Errorf("item %d: %+v, want %+v", i, got, w)
		}
	}
	if len(r.items) != len(want) {
		t.Errorf("%d items, want %d", len(r.items), len(want))
	}
	if r.autoenables {
		t.Error("the menu auto-enables its items: MenuState/validation would override Disabled")
	}
	if len(r.subItems) != 2 || r.subItems[0].title != "Family" || r.subItems[0].state != 1 || r.subItems[1].state != 0 {
		t.Errorf("submenu items %+v, want Family checked and Work not", r.subItems)
	}
	if r.dismissed.Picked {
		t.Errorf("dismissed menu reports a choice: %+v", r.dismissed)
	}
	if !r.chosen.Picked || r.chosen.Choice != 99 {
		t.Errorf("Delete chat: picked %v, choice %d; want 99", r.chosen.Picked, r.chosen.Choice)
	}
	if !r.subChosen.Picked || r.subChosen.Choice != 21 {
		t.Errorf("Labels ▸ Work: picked %v, choice %d; want 21", r.subChosen.Picked, r.subChosen.Choice)
	}
	if r.disabled.Picked {
		t.Errorf("the disabled item was chosen: %+v", r.disabled)
	}
	if r.events != 0 {
		t.Errorf("a dynamic menu delivered %d events, want none (the app gets its choice from the return)", r.events)
	}
}
