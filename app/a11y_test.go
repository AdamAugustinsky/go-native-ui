package app

import (
	"runtime"
	"testing"
	"time"

	"gonativeui/platform"
	"gonativeui/ui"
)

// The bridge's roles, states, actions and text kinds have ui's values, so
// ExportAX converts them by value.
func TestAXEnumsMatchUI(t *testing.T) {
	roles := []struct {
		u ui.Role
		p platform.AXRole
	}{
		{ui.RoleNone, platform.AXRoleNone}, {ui.RoleWindow, platform.AXRoleWindow}, {ui.RoleGroup, platform.AXRoleGroup},
		{ui.RoleLabel, platform.AXRoleLabel}, {ui.RoleImage, platform.AXRoleImage}, {ui.RoleButton, platform.AXRoleButton},
		{ui.RoleCheckbox, platform.AXRoleCheckbox}, {ui.RoleSwitch, platform.AXRoleSwitch}, {ui.RoleRadio, platform.AXRoleRadio},
		{ui.RoleRadioGroup, platform.AXRoleRadioGroup}, {ui.RoleSlider, platform.AXRoleSlider},
		{ui.RoleTextField, platform.AXRoleTextField}, {ui.RoleSearchField, platform.AXRoleSearchField},
		{ui.RoleComboBox, platform.AXRoleComboBox}, {ui.RoleList, platform.AXRoleList}, {ui.RoleListItem, platform.AXRoleListItem},
		{ui.RoleTable, platform.AXRoleTable}, {ui.RoleRow, platform.AXRoleRow}, {ui.RoleCell, platform.AXRoleCell},
		{ui.RoleColumnHeader, platform.AXRoleColumnHeader}, {ui.RoleScrollArea, platform.AXRoleScrollArea},
		{ui.RoleScrollBar, platform.AXRoleScrollBar}, {ui.RoleSplitter, platform.AXRoleSplitter},
		{ui.RoleTabList, platform.AXRoleTabList}, {ui.RoleTab, platform.AXRoleTab}, {ui.RoleTabPanel, platform.AXRoleTabPanel},
		{ui.RoleMenuBar, platform.AXRoleMenuBar}, {ui.RoleMenu, platform.AXRoleMenu}, {ui.RoleMenuItem, platform.AXRoleMenuItem},
		{ui.RoleMenuItemCheckbox, platform.AXRoleMenuItemCheckbox}, {ui.RoleDialog, platform.AXRoleDialog},
		{ui.RoleTooltip, platform.AXRoleTooltip}, {ui.RoleProgressBar, platform.AXRoleProgressBar}, {ui.RoleLink, platform.AXRoleLink},
		{ui.RoleHeading, platform.AXRoleHeading}, {ui.RoleToolbar, platform.AXRoleToolbar}, {ui.RoleSheet, platform.AXRoleSheet},
		{ui.RoleHost, platform.AXRoleHost},
	}
	for _, r := range roles {
		if uint8(r.u) != uint8(r.p) {
			t.Errorf("ui role %d is platform role %d", r.u, r.p)
		}
	}
	if len(roles) != int(platform.NumAXRoles) {
		t.Errorf("%d roles checked, platform has %d", len(roles), platform.NumAXRoles)
	}
	states := [][2]uint16{
		{uint16(ui.SemChecked), uint16(platform.AXChecked)}, {uint16(ui.SemMixed), uint16(platform.AXMixed)},
		{uint16(ui.SemSelected), uint16(platform.AXSelected)}, {uint16(ui.SemExpanded), uint16(platform.AXExpanded)},
		{uint16(ui.SemDisabled), uint16(platform.AXDisabled)}, {uint16(ui.SemFocused), uint16(platform.AXFocused)},
		{uint16(ui.SemEditable), uint16(platform.AXEditable)}, {uint16(ui.SemModal), uint16(platform.AXModal)},
		{uint16(ui.SemSortAscending), uint16(platform.AXSortAscending)}, {uint16(ui.SemSortDescending), uint16(platform.AXSortDescending)},
		{uint16(ui.SemLive), uint16(platform.AXLive)},
		{uint16(ui.ActPress), uint16(platform.AXPress)}, {uint16(ui.ActFocus), uint16(platform.AXFocus)},
		{uint16(ui.ActIncrement), uint16(platform.AXIncrement)}, {uint16(ui.ActDecrement), uint16(platform.AXDecrement)},
		{uint16(ui.ActExpand), uint16(platform.AXExpand)}, {uint16(ui.ActCollapse), uint16(platform.AXCollapse)},
		{uint16(ui.ActScrollIntoView), uint16(platform.AXScrollIntoView)}, {uint16(ui.ActSetValue), uint16(platform.AXSetValue)},
		{uint16(ui.ActShowMenu), uint16(platform.AXShowMenu)},
		{uint16(ui.SemTextField), uint16(platform.AXTextField)}, {uint16(ui.SemTextRich), uint16(platform.AXTextRich)},
	}
	for i, s := range states {
		if s[0] != s[1] {
			t.Errorf("pair %d: ui %#x, platform %#x", i, s[0], s[1])
		}
	}
}

// axApp is a window with a button and a slider.
type axApp struct {
	pressed, stepped int
	key              ui.Key
}

func (b *axApp) Build(c *ui.Context) {
	c.NextSize(ui.Px(80), ui.Px(30))
	c.NextRole(ui.RoleButton)
	c.NextSemName("Send")
	id := c.Box(ui.Clickable|ui.Focusable, "send")
	b.key = c.KeyOf(id)
	if c.Signal(id).Activated {
		b.pressed++
	}
	c.NextSize(ui.Px(80), ui.Px(10))
	c.NextRole(ui.RoleSlider)
	c.NextSemName("Volume")
	c.NextSemRange(0, 10, 5, 1)
	s := c.Box(ui.Focusable, "vol")
	if c.Signal(s).Action&ui.ActIncrement != 0 {
		b.stepped++
	}
}

// An assistive technology's action, arriving as an event, reaches the box in
// the next frame as ui's action request.
func TestAXActionReachesTheBox(t *testing.T) {
	b := &axApp{}
	o, err := NewOffscreen(Config{}, 200, 100, 1, b)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	o.Frame()
	o.onEvent(&platform.Event{Kind: platform.EvAccessibility, Tag: uint64(b.key), Code: uint32(platform.AXPress)})
	o.Frame()
	if b.pressed != 1 {
		t.Fatalf("pressed %d times, want 1", b.pressed)
	}
	var tree ui.SemTree
	o.UI.Semantics(&tree)
	var sl ui.Key
	for _, n := range tree.Nodes {
		if n.Role == ui.RoleSlider {
			sl = n.Key
		}
	}
	o.onEvent(&platform.Event{Kind: platform.EvAccessibility, Tag: uint64(sl), Code: uint32(platform.AXIncrement)})
	o.Frame()
	if b.stepped != 1 || b.pressed != 1 {
		t.Fatalf("stepped %d, pressed %d: want 1, 1", b.stepped, b.pressed)
	}
}

// With the bridge active, every frame exports and converts the tree (what
// SyncAX asks the source for): it allocates nothing once warm. The source
// refuses during a build.
func TestAXExportAllocatesNothing(t *testing.T) {
	b := &tiny{}
	o, err := NewOffscreen(Config{}, 400, 300, 2, b)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	o.ax.a = o.App
	var tree platform.AXTree
	step := func() {
		o.Frame()
		if !o.ax.AXExport(&tree) {
			t.Fatal("no export after a frame")
		}
	}
	for range 50 {
		step()
	}
	if len(tree.Nodes) == 0 {
		t.Fatal("empty tree")
	}
	o.ax.building = true
	if o.ax.AXExport(&tree) {
		t.Error("exported in the middle of a build")
	}
	o.ax.building = false
	var mallocs uint64
	for a := 0; a < 5; a++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for range 300 {
			step()
		}
		runtime.ReadMemStats(&m1)
		if mallocs = m1.Mallocs - m0.Mallocs; mallocs == 0 {
			return
		}
	}
	t.Errorf("frames with the export allocated (last: %d mallocs over 300 frames)", mallocs)
}
