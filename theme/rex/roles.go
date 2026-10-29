package rex

import "gonativeui/theme"

// roles are Rex's colour roles (tokens v2, D39): the macOS 27.2 values of
// theme.Measured, which the system's own replace on macOS, with Rex's
// identity on top. The field, the pane glass, the hairlines and the shadows
// are Rex's; the surfaces are neutral in dark (#1E1E1E, not the violet ink
// of the phase-5 Palette), and white goes on the accent.
//
// The Palette (light, dark) keeps its phase-5 values: the apps' look moves to
// these roles with the components (D3, D4, D6).
var roles = func() [2]theme.Colors {
	r := theme.Measured
	l, d := &r[theme.Light], &r[theme.Dark]
	l[theme.Field], d[theme.Field] = light.Background, dark.Background
	l[theme.Pane], d[theme.Pane] = 0xFFFFFFFF, 0x1E1E1EFF
	l[theme.Raised], d[theme.Raised] = 0xFFFFFFFF, 0x2A2A2AFF
	l[theme.OnAccent], d[theme.OnAccent] = 0xFFFFFFFF, 0xFFFFFFFF
	// The chrome's hairline round a pane (chrome.go, EndSurface), its inner
	// rim while unfocused, and the unfocused glass (field.go).
	l[theme.PaneHairline], d[theme.PaneHairline] = theme.ColA(0x5C5E60, 0.25), theme.ColA(0xFFFFFF, 0.11)
	l[theme.PaneHighlight], d[theme.PaneHighlight] = theme.ColA(0xFFFFFF, 0.55), theme.ColA(0xFFFFFF, 0.06)
	l[theme.GlassTint], d[theme.GlassTint] = theme.ColA(0xFFFFFF, fillAlpha), theme.ColA(darkGlass, darkAlpha)
	l[theme.Shadow], d[theme.Shadow] = light.Shadow, dark.Shadow
	l[theme.Scrim], d[theme.Scrim] = light.Scrim, dark.Scrim
	// The borderless icon button of Rex's pane headers (rexui.IconBorderless):
	// a quiet grey that darkens under the pointer, and the chip behind it.
	l[theme.PaneButton], d[theme.PaneButton] = theme.Col(0x96999B), theme.Col(0x8A8EA1)
	l[theme.PaneButtonHover], d[theme.PaneButtonHover] = theme.Col(0x3F4246), theme.Col(0xECEDF3)
	l[theme.PaneButtonChip], d[theme.PaneButtonChip] = theme.Col(0x2A2030), theme.Col(0xFFFFFF)
	return r
}()
