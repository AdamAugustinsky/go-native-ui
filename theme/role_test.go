package theme

import (
	"testing"

	"gonativeui/platform"
)

// Roles are append-only: a role's number is API, since a theme indexes its
// colours by it. Every role main had before D3b keeps its number (a role added
// in the middle fails here), and the roles added since come after them.
func TestRoleNumbersAreStable(t *testing.T) {
	pinned := []struct {
		role Role
		n    int
	}{
		{Window, 0}, {UnderPage, 1}, {ControlBackground, 2}, {TextBackground, 3}, {Label, 4}, {Label2, 5},
		{Label3, 6}, {Label4, 7}, {Label5, 8}, {Text, 9}, {Placeholder, 10}, {TextDisabled, 11},
		{HeaderText, 12}, {Fill1, 13}, {Fill2, 14}, {Fill3, 15}, {Fill4, 16}, {Fill5, 17},
		{Separator, 18}, {Grid, 19}, {Accent, 20}, {Selection, 21}, {SelectionUnemph, 22}, {OnSelection, 23},
		{TextSelection, 24}, {TextSelectionUnemph, 25}, {SelectedText, 26}, {FocusRing, 27}, {AltRow, 28}, {Link, 29},
		{Caret, 30}, {FindHighlight, 31}, {Red, 32}, {Orange, 33}, {Yellow, 34}, {Green, 35},
		{Mint, 36}, {Teal, 37}, {Cyan, 38}, {Blue, 39}, {Indigo, 40}, {Purple, 41},
		{Pink, 42}, {Brown, 43}, {Gray, 44}, {Field, 45}, {Pane, 46}, {Raised, 47},
		{OnAccent, 48}, {AccentPressed, 49}, {FillPressed, 50}, {FillDisabled, 51}, {InputFill, 52}, {PaneHairline, 53},
		{PaneHighlight, 54}, {GlassTint, 55}, {Shadow, 56}, {Scrim, 57}, {SidebarFill, 58}, {SidebarFillInactive, 59},
		{SidebarLabel, 60}, {SidebarLabelInactive, 61}, {SidebarHeader, 62}, {SidebarHeaderInactive, 63}, {SidebarSel, 64}, {SidebarSelUnemph, 65},
		{SidebarSelInactive, 66}, {SelectionDivider, 67}, {TitleText, 68}, {TitleTextInactive, 69}, {CapsuleFill, 70}, {CapsuleFillInactive, 71},
		{CapsuleRim, 72}, {CapsuleRimInactive, 73}, {CapsuleHighlight, 74}, {ScrollEdgeBar, 75}, {ScrollEdgeBarInactive, 76}, {ScrollEdgeLine, 77},
		{AccessoryLine, 78}, {GroupBox, 79},
	}
	for _, p := range pinned {
		if int(p.role) != p.n {
			t.Errorf("%v is role %d, it was %d", p.role, int(p.role), p.n)
		}
	}
	for _, r := range [...]Role{ControlText, Knob, FieldOutline, ToolbarSymbolInactive, SelectedRowTextInactive, SidebarDetail, SidebarSearchFill, SidebarSearchFillInactive} {
		if int(r) < len(pinned) {
			t.Errorf("%v is role %d, inside the first %d", r, int(r), len(pinned))
		}
	}
	if len(pinned) != int(ControlText) {
		t.Errorf("the roles after GroupBox start at %d, want %d", int(ControlText), len(pinned))
	}
}

// Each platform.SystemColor feeds its own System role, and only those roles
// are System roles.
func TestSystemColorRoles(t *testing.T) {
	seen := map[Role]platform.SystemColor{}
	for c := range platform.NumSystemColors {
		r := SystemRole(c)
		if prev, dup := seen[r]; dup {
			t.Errorf("%s and %s both feed %v", platform.SystemColorNames[prev], platform.SystemColorNames[c], r)
		}
		seen[r] = c
		if r.Source() != SourceSystem {
			t.Errorf("%v is fed by %s but is not a System role", r, platform.SystemColorNames[c])
		}
	}
	for r := range NumRoles {
		if _, ok := seen[r]; !ok && r.Source() == SourceSystem {
			t.Errorf("%v is a System role that no system colour feeds", r)
		}
	}
	if ControlText.Source() != SourceSystem || SystemRole(platform.ColorControlText) != ControlText {
		t.Error("controlTextColor does not feed ControlText")
	}
}
