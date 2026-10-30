package rex

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/workspace"
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
	t.setRoot(r.uid(), kindAgent)
	t.splitLeaf(0, ui.AxisX, 0.54638, r.uid(), kindSnake)
	t.splitLeaf(0, ui.AxisY, 0.43302, r.uid(), kindGit)
	t.ws.SetFocus(workspace.PaneID(t.panes[0].uid))

	t = &r.tabs[1]
	*t = tab{used: true, uid: r.uid(), name: "Bun ", path: "~/sites/announcement-draft", label: "Bun ~/sites/announcement-draft", style: tabStyles[1]}
	t.setRoot(r.uid(), kindBun)
	t.splitLeaf(0, ui.AxisX, 0.58, r.uid(), kindShell)
	t.ws.SetFocus(workspace.PaneID(t.panes[0].uid))

	t = &r.tabs[2]
	*t = tab{used: true, uid: r.uid(), name: "fish ", path: "~/sites/macos-client-update", label: "fish ~/sites/macos-client-update", style: tabStyles[2]}
	t.setRoot(r.uid(), kindShell)
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
	t.setRoot(r.uid(), kindShell)
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

// moveTab moves tab from to the place to, the tabs between shifting by one;
// the chosen tab stays chosen.
func (r *Rex) moveTab(from, to int) {
	if from == to || from < 0 || to < 0 || from >= r.nTabs || to >= r.nTabs {
		return
	}
	act := r.tabs[r.active].uid
	t := r.tabs[from]
	if from < to {
		copy(r.tabs[from:to], r.tabs[from+1:to+1])
	} else {
		copy(r.tabs[to+1:from+1], r.tabs[to:from])
	}
	r.tabs[to] = t
	for i := 0; i < r.nTabs; i++ {
		if r.tabs[i].uid == act {
			r.active = i
		}
	}
	r.fillTabList()
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
	// The tab bar: a tab is chosen on the press, as in AppKit's tab bars and
	// segmented controls, not on the release; its close button or a middle
	// click closes it; a drag moves it; ⌃Tab, ⌘⇧] and ⌘1… choose.
	r.fillTabList()
	ev := r.bar.Update(r.k, r.tabList[:r.nTabs], r.active)
	if ev.Select >= 0 {
		r.selectTab(ev.Select)
	}
	if ev.MoveFrom >= 0 {
		r.moveTab(ev.MoveFrom, ev.MoveTo)
	}
	if ev.Close >= 0 {
		r.closeTab(ev.Close)
	}
	if c.PeekSignal(keyPlus).Clicked {
		r.newTab()
	}
	t := &r.tabs[r.active]

	// The workspace: a press in a pane focuses it, a divider follows the
	// pointer (or, focused, the arrow keys) within the minimum sizes, and
	// ⌘⌥ and the arrows, or F6 and ⇧F6, move the focus between panes.
	if ev := t.ws.Update(c, workspace.Keys{Dir: ui.ModSuper | ui.ModAlt, Cycle: true}); ev.Moved {
		r.glide = false
	}
	if t.ws.Focus() == 0 {
		return
	}
	uid := t.focus()
	for b := 0; b < numBtns; b++ {
		if c.PeekSignal(btnKey(t, uid, b)).Clicked {
			r.button(t, t.slotOf(uid), b)
			return
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
		t.ws.SetZoom(0)
		from := r.disp[slot]
		if np := t.splitLeaf(slot, axis, 0.5, r.uid(), kindShell); np >= 0 {
			t.panes[np].born, t.panes[np].from = true, from
			t.ws.SetFocus(workspace.PaneID(t.panes[np].uid))
			r.glide = true
		}
	case btnMax:
		if t.max() == uid {
			t.ws.SetZoom(0)
		} else {
			t.ws.SetZoom(workspace.PaneID(uid))
		}
	case btnClose:
		if t.numPanes() > 1 {
			t.closeLeaf(slot)
			r.glide = true
		} else {
			r.closeTab(r.active)
		}
	}
}

// tabKeys applies ⌘T, a new tab. The tab bar takes the system's other tab
// shortcuts: ⌘1…⌘8 a tab and ⌘9 the last, ⌃Tab and ⌘⇧] the next, ⌃⇧Tab and
// ⌘⇧[ the previous (both wrap).
func (r *Rex) tabKeys(c *ui.Context) {
	if c.TakeKey('T', ui.ModSuper) {
		r.newTab()
	}
}
