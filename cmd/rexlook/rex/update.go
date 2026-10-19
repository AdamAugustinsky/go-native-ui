package rex

import (
	"gonativeui/theme"
	"gonativeui/ui"
)

// The reference's three tabs. The first holds the three panes of the
// screenshot; the others have their own panes so that switching tabs shows
// something.
var tabStyles = [...]tabStyle{
	{front: 0xF6F6F4FF, border: theme.ColA(0x000000, 0.18), backs: [2]uint32{0x52B35B, 0x2F8F4E}, step: 11, iconX: 23, textX: 112, icon: icTabAgent, symbol: 0x23262B},
	{front: 0xF6EFD6FF, border: theme.ColA(0x000000, 0.18), backs: [2]uint32{0x3C7DBE, 0}, step: 12, iconX: 21, textX: 99, icon: icTabFace, symbol: 0x3A3A3C},
	{front: 0x2B2D30FF, border: theme.ColA(0x000000, 0.35), backs: [2]uint32{0xF0963C, 0xA266DA}, step: 8, iconX: 21, textX: 97, icon: icTabTerm, symbol: 0x6FE08A},
}

func (r *Rex) initTabs() {
	r.nTabs = 3
	t := &r.tabs[0]
	*t = tab{used: true, uid: r.uid(), name: "Stress-test Snake demo ", path: "| rex-snake", label: "Stress-test Snake demo | rex-snake", style: tabStyles[0]}
	// (agent over git) beside the game, with the measured ratios.
	t.setRoot(r.uid(), kindAgent, r.uid())
	t.splitLeaf(0, ui.AxisX, 0.54638, r.uid(), kindSnake, r.uid(), r.uid())
	t.splitLeaf(0, ui.AxisY, 0.43302, r.uid(), kindGit, r.uid(), r.uid())
	t.focus = t.panes[0].uid

	t = &r.tabs[1]
	*t = tab{used: true, uid: r.uid(), name: "Bun ", path: "~/sites/announcement-draft", label: "Bun ~/sites/announcement-draft", style: tabStyles[1]}
	t.setRoot(r.uid(), kindBun, r.uid())
	t.splitLeaf(0, ui.AxisX, 0.58, r.uid(), kindShell, r.uid(), r.uid())
	t.focus = t.panes[0].uid

	t = &r.tabs[2]
	*t = tab{used: true, uid: r.uid(), name: "fish ", path: "~/sites/macos-client-update", label: "fish ~/sites/macos-client-update", style: tabStyles[2]}
	t.setRoot(r.uid(), kindShell, r.uid())
	r.active = 0
}

func (r *Rex) cur() *tab { return &r.tabs[r.active] }

// selectTab shows tab i. The switch is instant, as AppKit's tabs are: the
// pill glides over, the panes are there in the same frame.
func (r *Rex) selectTab(i int) {
	if i == r.active || i < 0 || i >= r.nTabs {
		return
	}
	r.active = i
	r.glide = false
}

func (r *Rex) newTab() {
	if r.nTabs >= maxTabs {
		return
	}
	t := &r.tabs[r.nTabs]
	*t = tab{used: true, uid: r.uid(), name: "fish ", path: "~/Sites", label: "fish ~/Sites", style: tabStyles[2]}
	t.setRoot(r.uid(), kindShell, r.uid())
	r.nTabs++
	r.selectTab(r.nTabs - 1)
}

func (r *Rex) closeTab(i int) {
	if r.nTabs <= 1 {
		return
	}
	copy(r.tabs[i:r.nTabs], r.tabs[i+1:r.nTabs])
	r.nTabs--
	r.tabs[r.nTabs] = tab{}
	if i < r.active {
		r.active--
	} else if r.active >= r.nTabs {
		r.active = r.nTabs - 1
	}
	r.switchN++
	r.glide = false
}

// canClose reports whether the focused pane's close button does anything: the
// last pane of the last tab stays.
func (r *Rex) canClose() bool { return r.tabs[r.active].numPanes() > 1 || r.nTabs > 1 }

func (r *Rex) canSplit() bool { return r.tabs[r.active].numPanes() < maxPanes }

// update applies this frame's input to the model, before anything is built,
// so the frame that shows a click or a drag is the frame it happens in.
func (r *Rex) update(c *ui.Context) {
	r.commands(c)
	r.tabKeys(c)
	// A tab is chosen on the press, as in AppKit's tab bars and segmented
	// controls, not on the release.
	for i := 0; i < r.nTabs; i++ {
		if c.PeekSignal(tabKey(r.tabs[i].uid)).Pressed {
			r.selectTab(i)
			break
		}
	}
	if c.PeekSignal(keyPlus).Clicked {
		r.newTab()
	}
	t := &r.tabs[r.active]

	// Dividers: the first child follows the pointer within the minimum sizes.
	if t.max == 0 {
		for i := range t.nodes {
			n := &t.nodes[i]
			if !n.used || n.leaf {
				continue
			}
			s := c.PeekSignal(divKey(n.uid))
			if s.Pressed {
				n.press = n.ratio
			}
			if s.Dragging || s.Released {
				ax := 0
				if n.axis == ui.AxisY {
					ax = 1
				}
				t.dragSplit(int8(i), s.DragDelta[ax])
				r.glide = false
			}
		}
	}

	for slot := range t.panes {
		p := &t.panes[slot]
		if !p.used {
			continue
		}
		if c.PeekSignal(paneKey(p.uid)).Pressed {
			t.focus = p.uid
		}
		if t.focus != p.uid {
			continue
		}
		for b := 0; b < numBtns; b++ {
			if c.PeekSignal(btnKey(p.uid, b)).Clicked {
				r.button(t, int8(slot), b)
				return
			}
		}
	}
}

// button applies a pane header button.
func (r *Rex) button(t *tab, slot int8, b int) {
	uid := t.panes[slot].uid
	switch b {
	case btnSplitX, btnSplitY:
		if !r.canSplit() {
			return
		}
		axis := ui.AxisX
		if b == btnSplitY {
			axis = ui.AxisY
		}
		t.max = 0
		from := r.disp[slot]
		if np := t.splitLeaf(slot, axis, 0.5, r.uid(), kindShell, r.uid(), r.uid()); np >= 0 {
			t.panes[np].born, t.panes[np].from = true, from
			t.focus = t.panes[np].uid
			r.glide = true
		}
	case btnMax:
		if t.max == uid {
			t.max = 0
		} else {
			t.max = uid
		}
		t.focus = uid
	case btnClose:
		if t.numPanes() > 1 {
			t.closeLeaf(slot)
			r.glide = true
		} else {
			r.closeTab(r.active)
		}
	}
}

// tabKeys applies the system's tab shortcuts: ⌘1…⌘8 a tab and ⌘9 the last,
// ⌃Tab and ⌘⇧] the next, ⌃⇧Tab and ⌘⇧[ the previous (both wrap), ⌘T a new
// tab.
func (r *Rex) tabKeys(c *ui.Context) {
	in := c.Input()
	for i := 0; i < in.NKeys; i++ {
		k := in.Keys[i]
		if !k.Down {
			continue
		}
		cmd, shift, ctrl := k.Mods&ui.ModSuper != 0, k.Mods&ui.ModShift != 0, k.Mods&ui.ModCtrl != 0
		next := 0
		switch {
		case cmd && !shift && k.Code >= '1' && k.Code <= '8':
			r.selectTab(int(k.Code - '1'))
		case cmd && !shift && k.Code == '9':
			r.selectTab(r.nTabs - 1)
		case cmd && !shift && k.Code == 'T':
			r.newTab()
		case ctrl && k.Code == ui.KeyTab, cmd && shift && k.Code == ui.KeyRightBracket:
			next = 1
			if ctrl && shift {
				next = -1
			}
		case cmd && shift && k.Code == ui.KeyLeftBracket:
			next = -1
		}
		if next != 0 && r.nTabs > 1 {
			r.selectTab((r.active + next + r.nTabs) % r.nTabs)
		}
	}
}
