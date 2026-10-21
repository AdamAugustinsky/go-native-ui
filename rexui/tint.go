package rexui

import (
	"math"

	"gonativeui/theme"
)

// NoRole is the Role of a Tint that draws nothing.
const NoRole theme.Role = 255

// LitRole is the Role of a Tint that is a colour of the app's own (OfRGBA).
const LitRole theme.Role = 254

var _ [LitRole - theme.NumRoles]struct{} // the roles stay below LitRole

// Tint is a colour a recipe names: a colour role, optionally at an opacity of
// its own in light and dark. Recipes hold Tints, not colours, so a light/dark
// crossfade and the system's live colours cost nothing extra.
//
// Alpha replaces the role's alpha (both 0 keep it). It is for the few colours
// macOS 27.2 draws that no role names (the accent at 70 % of a disabled
// switch, red at 25 % of a destructive button); each such use is measured and
// documented in its recipe. A colour several recipes share gets a role of its
// own (theme.Knob, theme.FieldOutline).
type Tint struct {
	// Role is the colour role (NoRole: nothing; LitRole: Lit).
	Role theme.Role
	// Alpha is the opacity in light (0) and dark (1), 0 to 1; both zero keep
	// the role's own, one zero is transparent in that mode.
	Alpha [2]float32
	// Lit is the colour (0xRRGGBBAA) of a Tint whose Role is LitRole.
	Lit uint32
}

// Of is the Tint of role as it is.
func Of(role theme.Role) Tint { return Tint{Role: role} }

// OfAlpha is role at the opacity light in light and dark in dark.
func OfAlpha(role theme.Role, light, dark float32) Tint {
	return Tint{Role: role, Alpha: [2]float32{light, dark}}
}

// OfRGBA is a colour of the app's own (0xRRGGBBAA), the same in light and
// dark: a contact's colour, a brand's. Recipes keep roles; an option that
// takes a Tint (AvatarOpts.Color) takes one of these too.
func OfRGBA(rgba uint32) Tint { return Tint{Role: LitRole, Lit: rgba} }

// None draws nothing.
var None = Tint{Role: NoRole}

// RGBA is the tint's colour in r, 0xRRGGBBAA at r's light/dark mix; 0 for
// None.
func (t Tint) RGBA(r *theme.Resolved) uint32 {
	var c uint32
	switch {
	case t.Role == LitRole:
		c = t.Lit
	case t.Role >= theme.NumRoles:
		return 0
	default:
		c = r.Colors[t.Role]
	}
	if t.Alpha == ([2]float32{}) {
		return c
	}
	a := theme.Clamp01(theme.Lerp(t.Alpha[0], t.Alpha[1], r.T))
	return c&^0xFF | uint32(a*255+0.5)
}

// settled is the share of the way an exponential approach covers in a
// duration given as its length: 95 %, which reads as arrived.
const settled = 0.95

// RateFor is the rate (theme.Motion's unit, for ui.Context.AnimateValue) of an
// exponential approach that covers 95 % of the way in d seconds: a duration
// token (Motion.Standard) as a rate. d ≤ 0, which Reduce Motion gives, is
// theme.Instant.
func RateFor(d float32) float32 {
	if d <= 0 {
		return theme.Instant
	}
	return float32(-math.Log2(1-settled)) / d
}

// over composites top over fill, at strength t: a press or hover tint laid
// over a fill. A fill that draws nothing gives top itself.
func over(fill, top uint32, t float32) uint32 {
	if t <= 0 || top&0xFF == 0 {
		return fill
	}
	if fill&0xFF == 0 {
		return theme.WithAlpha(top, t)
	}
	if fill&0xFF == 0xFF {
		return theme.MixRGBA(fill, theme.Over(top, fill), t)
	}
	return theme.MixRGBA(fill, top, t)
}

// mix blends a toward b by t, keeping a when t is 0.
func mix(a, b uint32, t float32) uint32 {
	if t <= 0 {
		return a
	}
	return theme.MixRGBA(a, b, t)
}

// pt is v points in whole pixels at the kit's scale.
func pt(scale, v float32) float32 { return theme.Snap(v * scale) }

// b2f is 1 for true and 0 for false (shared: tint.go comes with every component).
func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
