package theme

// Two passes over a theme's roles when it is resolved (D107), once per
// Resolve and never per frame: the roles that follow the labels, then the
// contrast guard. Both work on the theme's own values (Theme.Roles), before
// the system's colours and the accent are applied.

// labelFollower is a role whose value goes with a label role: when a theme
// replaces the label and leaves the follower at the value its basis gave it
// (Theme.RoleBasis), the follower is derived from the theme's label, so an
// app that supplies its own palette gets icons, hovers and rims in its own
// colours without naming every role.
type labelFollower struct {
	role, label Role
	// alpha: keep the follower's own alpha and take only the label's RGB (a
	// tint: the label's colour at so many per cent); otherwise take the
	// label's colour whole. opaque: the label's RGB at full opacity.
	alpha, opaque bool
}

// labelFollowers are the roles derived from the labels: the borderless icon
// button's symbol from Label2 and its hover and chip from Label; the rows'
// hover, press and inactive selection, the controls' hover fill and the off
// rim are the label's colour at their own strength.
var labelFollowers = [...]labelFollower{
	{role: PaneButton, label: Label2},
	{role: PaneButtonHover, label: Label},
	{role: PaneButtonChip, label: Label, opaque: true},
	{role: RowHover, label: Label, alpha: true},
	{role: RowPressed, label: Label, alpha: true},
	{role: RowSelectedInactive, label: Label, alpha: true},
	{role: FillHover, label: Label, alpha: true},
	{role: ControlRim, label: Label, alpha: true},
}

// followLabels derives the label followers of c (the theme's roles in mode
// m) from its labels where the theme changed the label and kept the
// follower at basis's value.
func followLabels(c *Colors, basis *Colors) {
	for _, f := range labelFollowers {
		if c[f.label] == basis[f.label] || c[f.role] != basis[f.role] {
			continue
		}
		l := c[f.label]
		switch {
		case f.alpha:
			c[f.role] = l&^0xFF | c[f.role]&0xFF
		case f.opaque:
			c[f.role] = l | 0xFF
		default:
			c[f.role] = l
		}
	}
}

// The contrast floors the guard holds: WCAG 2's 4.5:1 for text that must be
// read (secondary text, placeholders) and 3:1 for tertiary text and for
// non-text indicators (the off rim).
const (
	FloorText      = 4.5
	FloorIndicator = 3.0
)

// guardedRoles are the roles the guard holds, with their floors.
var guardedRoles = [...]struct {
	role  Role
	floor float64
}{
	{Label2, FloorText}, {Placeholder, FloorText},
	{Label3, FloorIndicator}, {ControlRim, FloorIndicator},
}

// guardSurfaces are the surfaces the guarded roles are used on, each with
// what lies under it when it is translucent.
var guardSurfaces = [...][2]Role{
	{Window, Window}, {Pane, Pane}, {ControlBackground, ControlBackground}, {TextBackground, TextBackground},
	{InputFill, Window}, {Raised, Raised}, {SidebarFill, SidebarFill}, {GroupBox, GroupBox},
}

// worstContrast is the lowest contrast of fg over the guard's surfaces in c.
func worstContrast(fg uint32, c *Colors) float64 {
	w := 99.0
	for _, s := range guardSurfaces {
		w = min(w, ContrastOn(fg, c[s[0]], c[s[1]]|0xFF))
	}
	return w
}

// guardContrast moves each guarded role of c that reads under its floor on
// one of the surfaces toward the primary label, in steps of 5 %, until it
// passes (or reaches the label). A System role is left alone when it is the
// system's (the theme follows the system's colours, followSys, and does not
// own it) or AppKit's own value (measured, Measured[m]): AppKit's contrast is
// AppKit's to keep, the guard is for the colours an app supplies. A role is
// also left as the app gave it when the label itself reads under the role's
// floor: moving toward a label that cannot be read mends nothing, and would
// only replace the app's colour with the label's.
func guardContrast(c *Colors, measured *Colors, followSys bool, own *[NumRoles]bool) {
	label := worstContrast(c[Label], c)
	for _, g := range guardedRoles {
		if label < g.floor {
			continue
		}
		if g.role.Source() == SourceSystem && (followSys && !own[g.role] || c[g.role] == measured[g.role]) {
			continue
		}
		v := c[g.role]
		if v&0xFF == 0 || worstContrast(v, c) >= g.floor {
			continue
		}
		from := v
		for i := 1; i <= 20; i++ {
			v = MixRGBA(from, c[Label], float32(i)/20)
			if worstContrast(v, c) >= g.floor {
				break
			}
		}
		c[g.role] = v
	}
}
