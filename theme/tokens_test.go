package theme

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"

	"gonativeui/platform"
)

// The tokens against the committed macOS 27.2 reference
// (testdata/appkit-ref.json: tools/appkitref's metrics and tools/appkitscenes'
// window measurements; Apple's numbers, not its pixels). Lengths are held to
// ±0.5 pt (1 px at 2×), colours to ±1 per channel (±3 for the flat stand-ins
// of a live material, whose pixels the glass refracts by about 2 %).

type frame struct{ X, Y, W, H float64 }

type appkitRef struct {
	System struct {
		Build string `json:"osBuild"`
	} `json:"system"`
	Colors     map[string]map[string]string `json:"colors"`
	TextStyles map[string]struct {
		Size, Line, Weight float64
	} `json:"textStyles"`
	Fonts struct {
		TitleBar, Menu, ToolTips float64
		ControlSizes             map[string]float64 `json:"controlSizes"`
	} `json:"fonts"`
	Controls struct {
		Push      map[string]struct{ H, Radius, Font, W float64 } `json:"push"`
		Field     map[string]struct{ H, Radius float64 }          `json:"field"`
		Search    struct{ H, Radius float64 }                     `json:"search"`
		Switch    map[string]struct{ H, W float64 }               `json:"switch"`
		FocusRing struct {
			Outset float64 `json:"outsetFromBody"`
			Width  float64 `json:"width"`
		} `json:"focusRing"`
		PushFill string `json:"pushFill"`
	} `json:"controls"`
	Windows map[string]struct {
		Titlebar, CornerRadius float64
		Close                  struct {
			X, YFromTop, CenterYFromTop, W float64
		}
	} `json:"windows"`
	Scrollers map[string]any                                                 `json:"scrollers"`
	Menus     struct{ ItemHeight, SeparatorHeight, VerticalPadding float64 } `json:"menus"`
	Behaviour struct {
		DefaultAnimationDuration float64
	} `json:"behaviour"`
	Scenes struct {
		Styles map[string]struct {
			Titlebar, CornerRadius float64
			Close                  frame
			Title                  struct{ Size, Weight, Y, H float64 }
			Capsule                struct{ H, Y, Radius, FirstX float64 }
		} `json:"styles"`
		Sidebar struct {
			RowHeight, HeaderH, Indent, HeaderTextX, HeaderFont, IconX, Icon, TextX, GroupGap float64
		} `json:"sidebar"`
		Selections map[string]struct {
			Fill          string
			Inset, Radius float64
		} `json:"selections"`
		SidebarFill map[string]string `json:"sidebarFill"`
		Titles      map[string]any    `json:"titles"`
	} `json:"scenes"`
	Chrome struct {
		Light, Dark    map[string]string
		GroupBoxRadius float64 `json:"groupBoxRadius"`
	} `json:"chrome"`
}

func loadRef(t *testing.T) *appkitRef {
	t.Helper()
	raw, err := os.ReadFile("testdata/appkit-ref.json")
	if err != nil {
		t.Fatal(err)
	}
	var r appkitRef
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

// hex parses #RRGGBB or #RRGGBBAA into 0xRRGGBBAA.
func hex(t *testing.T, s string) uint32 {
	t.Helper()
	var v uint32
	switch len(s) {
	case 7:
		if _, err := fmt.Sscanf(s, "#%06X", &v); err == nil {
			return v<<8 | 0xFF
		}
	case 9:
		if _, err := fmt.Sscanf(s, "#%08X", &v); err == nil {
			return v
		}
	}
	t.Fatalf("bad colour %q", s)
	return 0
}

func within(a, b uint32, tol int) bool {
	for sh := 0; sh < 32; sh += 8 {
		if d := int(a>>sh&255) - int(b>>sh&255); d < -tol || d > tol {
			return false
		}
	}
	return true
}

func near(a float32, b float64, tol float64) bool { return math.Abs(float64(a)-b) <= tol }

var modeNames = [2]string{"light", "dark"}

func TestMeasuredSystemRolesAreAppKits(t *testing.T) {
	ref := loadRef(t)
	for m, mode := range modeNames {
		for i := range NumSystemRoles {
			name := platform.SystemColorNames[i]
			want := hex(t, ref.Colors[mode][name])
			if got := Measured[m][i]; !within(got, want, 1) {
				t.Errorf("%s %v (%s): %08X, AppKit %08X", mode, Role(i), name, got, want)
			}
			if Role(i).Source() != SourceSystem {
				t.Errorf("%v is not a System role", Role(i))
			}
		}
	}
	if Field.Source() != SourceTheme || GroupBox.Source() != SourceTheme {
		t.Error("theme roles report the system as their source")
	}
	for r := range NumRoles {
		if r.String() == "" || r.String() == "Role(?)" {
			t.Errorf("role %d has no name", r)
		}
	}
}

// The window-chrome roles are the pixels of live 27.2 windows; "OnWindow"
// references are a translucent role seen over the window, and the derived
// roles are checked as a resolved theme derives them.
func TestMeasuredChromeRoles(t *testing.T) {
	ref := loadRef(t)
	r := (&Theme{}).Resolve(&fakeText{}, 2, Light)
	byName := map[string]Role{}
	for i := Field; i < NumRoles; i++ {
		byName[i.String()] = i
	}
	for m, want := range [2]map[string]string{ref.Chrome.Light, ref.Chrome.Dark} {
		r.Mix(float32(m))
		for name, hexv := range want {
			w := hex(t, hexv)
			onWindow := false
			if n := len(name) - len("OnWindow"); n > 0 && name[n:] == "OnWindow" {
				name, onWindow = name[:n], true
			}
			role, ok := byName[name]
			if !ok {
				t.Fatalf("no role %s", name)
			}
			got := r.Colors[role]
			if onWindow {
				got = Over(got, r.Colors[Window])
			}
			if !within(got, w, 2) {
				t.Errorf("%s %v: %08X, measured %08X", modeNames[m], role, got, w)
			}
		}
		for _, k := range []string{"active-top", "inactive-top"} {
			role := SidebarFill
			if k == "inactive-top" {
				role = SidebarFillInactive
			}
			if w := hex(t, ref.Scenes.SidebarFill[modeNames[m]+"-"+k]); !within(r.Colors[role], w, 3) {
				t.Errorf("%s %v: %08X, the glass measures %08X", modeNames[m], role, r.Colors[role], w)
			}
		}
		for key, role := range map[string]Role{"sidebarEmph": SidebarSel, "sidebarUnemph": SidebarSelUnemph, "sidebarInactive": SidebarSelInactive, "table": Selection, "tableInactive": SelectionUnemph} {
			s := ref.Scenes.Selections[key+"."+modeNames[m]]
			if w := hex(t, s.Fill); !within(r.Colors[role], w, 1) {
				t.Errorf("%s %v: %08X, measured %08X", modeNames[m], role, r.Colors[role], w)
			}
		}
	}
	if fill := hex(t, ref.Controls.PushFill); fill != Measured[Light][Fill2] {
		t.Errorf("a push button's body is Fill2: %08X, measured %08X", Measured[Light][Fill2], fill)
	}
}

var weightOf = map[float64]int{400: WeightRegular, 510: WeightMedium, 590: WeightSemibold, 700: WeightBold}

func TestTextStylesAreAppKits(t *testing.T) {
	ref := loadRef(t)
	for name, style := range map[string]TextStyle{
		"largeTitle": StyleLargeTitle, "title1": StyleTitle1, "title2": StyleTitle2, "title3": StyleTitle3,
		"headline": StyleHeadline, "body": StyleBody, "callout": StyleCallout, "subheadline": StyleSubheadline,
		"footnote": StyleFootnote, "caption1": StyleCaption1, "caption2": StyleCaption2,
	} {
		w := ref.TextStyles[name]
		got := MacText[style]
		if !near(got.Size, w.Size, 0) || !near(got.Line, w.Line, 0) || got.Weight != weightOf[w.Weight] {
			t.Errorf("%s: %+v, AppKit size %v line %v weight %v", name, got, w.Size, w.Line, w.Weight)
		}
	}
	for _, c := range []struct {
		style TextStyle
		size  float64
	}{
		{StyleWindowTitle, ref.Fonts.TitleBar}, {StyleMenu, ref.Fonts.Menu}, {StyleTooltip, ref.Fonts.ToolTips},
		{StyleToolbarTitle, ref.Scenes.Styles["unified"].Title.Size}, {StyleSidebarHeader, ref.Scenes.Sidebar.HeaderFont},
	} {
		if !near(MacText[c.style].Size, c.size, 0) {
			t.Errorf("text style %d: %v pt, AppKit %v", c.style, MacText[c.style].Size, c.size)
		}
	}
	// The toolbar titles are bold (weight 0.4) and as tall as AppKit's fields.
	for style, key := range map[TextStyle]string{StyleToolbarTitle: "unified", StyleWindowTitle: "compact"} {
		s := ref.Scenes.Styles[key]
		if MacText[style].Weight != WeightBold || !near(MacText[style].Line, s.Title.H, 0) || !near(MacWindow.Toolbars[map[string]ToolbarStyle{"unified": ToolbarUnified, "compact": ToolbarCompact}[key]].TitleY, s.Title.Y, 0.5) {
			t.Errorf("%s title: %+v, AppKit %+v", key, MacText[style], s.Title)
		}
	}
	if !MacText[StyleMono].Mono {
		t.Error("StyleMono is not monospaced")
	}
}

var sizeNames = [NumSizes]string{SizeRegular: "regular", SizeMini: "mini", SizeSmall: "small", SizeLarge: "large", SizeXLarge: "xlarge"}

func TestSizesAreAppKits(t *testing.T) {
	ref := loadRef(t)
	r := (&Theme{}).Resolve(&fakeText{}, 2, Light)
	regular := ref.Controls.Push["regular"]
	label13 := regular.W - 2*float64(MacSizes[SizeRegular].PadX) // the label "Button" at 13 pt
	for s := range NumSizes {
		name := sizeNames[s]
		spec := MacSizes[s]
		p := ref.Controls.Push[name]
		if !near(spec.Height, p.H, 0) || !near(spec.FontSize, p.Font, 0) || !near(spec.FontSize, ref.Fonts.ControlSizes[name], 0) {
			t.Errorf("%s: height %v font %v, AppKit %v and %v", name, spec.Height, spec.FontSize, p.H, p.Font)
		}
		// The resolved radius is what is drawn: a capsule is half the height.
		if rad := r.Sizes[s].Radius / 2; !near(rad, p.Radius, 0.5) {
			t.Errorf("%s: radius %v, AppKit %v", name, rad, p.Radius)
		}
		if spec.Capsule != (s == SizeLarge || s == SizeXLarge) {
			t.Errorf("%s: capsule %v", name, spec.Capsule)
		}
		// The padding: the label's width scales with the font.
		label := label13 * p.Font / 13
		if pad := (p.W - label) / 2; !near(spec.PadX, pad, 1) {
			t.Errorf("%s: padding %v, AppKit about %.1f", name, spec.PadX, pad)
		}
		if f, ok := ref.Controls.Field[name]; ok && (!near(spec.FieldHeight, f.H, 0) || !near(spec.FieldRadius, f.Radius, 0.5)) {
			t.Errorf("%s field: %v r %v, AppKit %v r %v", name, spec.FieldHeight, spec.FieldRadius, f.H, f.Radius)
		}
		if sw, ok := ref.Controls.Switch[name]; ok && (!near(spec.SwitchW, sw.W, 0) || !near(spec.SwitchH, sw.H, 0)) {
			t.Errorf("%s switch: %v × %v, AppKit %v × %v", name, spec.SwitchW, spec.SwitchH, sw.W, sw.H)
		}
		if f := r.Sizes[s].Font; f.Font == 0 || f.Line != 2*spec.FontLine {
			t.Errorf("%s: control font %+v", name, f)
		}
	}
	if s := ref.Controls.Search; !near(MacSizes[SizeRegular].FieldHeight/2, s.Radius, 0.5) {
		t.Errorf("a search field is a capsule: r %v, AppKit %v", MacSizes[SizeRegular].FieldHeight/2, s.Radius)
	}
	fr := ref.Controls.FocusRing
	if !near(MacLines.FocusOutset, fr.Outset, 0.5) || !near(MacLines.FocusWidth, fr.Width, 0.5) {
		t.Errorf("focus ring %v / %v, AppKit %v / %v", MacLines.FocusOutset, MacLines.FocusWidth, fr.Outset, fr.Width)
	}
	for k, v := range map[string]float32{"regular-overlay": MacLines.Scroller, "regular-legacy": MacLines.Scroller, "small-overlay": MacLines.ScrollerSmall, "small-legacy": MacLines.ScrollerSmallLegacy} {
		if w, _ := ref.Scrollers[k].(float64); !near(v, w, 0) {
			t.Errorf("scroller %s: %v, AppKit %v", k, v, w)
		}
	}
	if m := ref.Menus; !near(MacLines.MenuItemH, m.ItemHeight, 0) || !near(MacLines.MenuSeparatorH, m.SeparatorHeight, 0) || !near(MacLines.MenuPadY, m.VerticalPadding, 0) {
		t.Errorf("menus %+v, AppKit %+v", MacLines, m)
	}
	if !near(MacRadii.Group, ref.Chrome.GroupBoxRadius, 0.5) || MacWindow.GroupRadius != MacRadii.Group {
		t.Errorf("group box radius %v, measured %v", MacRadii.Group, ref.Chrome.GroupBoxRadius)
	}
}

// The traffic lights are stored as the close button's centre: AppKitRef's
// frame origin + 7 (the plan's "Traffic-light units").
func TestWindowIsAppKits(t *testing.T) {
	ref := loadRef(t)
	for style, key := range map[ToolbarStyle]string{ToolbarPlain: "titled", ToolbarCompact: "toolbarUnifiedCompact", ToolbarUnified: "toolbarUnified"} {
		w := ref.Windows[key]
		tb := MacWindow.Toolbars[style]
		if c := TrafficCentre(style); !near(c[0], w.Close.X+w.Close.W/2, 0.5) || !near(c[1], w.Close.CenterYFromTop, 0.5) {
			t.Errorf("%s: close centre %v, AppKit (%v, %v)", key, c, w.Close.X+7, w.Close.CenterYFromTop)
		}
		if !near(tb.Height, w.Titlebar, 0) || !near(MacWindow.Radius, w.CornerRadius, 0) {
			t.Errorf("%s: height %v radius %v, AppKit %v %v", key, tb.Height, MacWindow.Radius, w.Titlebar, w.CornerRadius)
		}
	}
	if TrafficCentre(ToolbarRex) != TrafficCentrePt || MacWindow.Toolbars[ToolbarRex].Height != 46 {
		t.Errorf("Rex: %v, TrafficCentrePt %v", TrafficCentre(ToolbarRex), TrafficCentrePt)
	}
	if MacWindow.TrafficSpacing != 23 || MacWindow.TrafficD != 14 {
		t.Errorf("traffic spacing %v diameter %v", MacWindow.TrafficSpacing, MacWindow.TrafficD)
	}
	for style, key := range map[ToolbarStyle]string{ToolbarCompact: "compact", ToolbarUnified: "unified"} {
		s := ref.Scenes.Styles[key]
		tb := MacWindow.Toolbars[style]
		zoomRight := float64(tb.Traffic[0]+2*MacWindow.TrafficSpacing) + 7
		if !near(tb.Height, s.Titlebar, 0) || !near(tb.CapsuleH, s.Capsule.H, 0) || !near(tb.CapsuleY, s.Capsule.Y, 0) ||
			!near(tb.CapsuleH/2, s.Capsule.Radius, 0) || !near(tb.FirstGap, s.Capsule.FirstX-zoomRight, 0.5) {
			t.Errorf("%s: %+v, scene %+v", key, tb, s)
		}
		if c := TrafficCentre(style); !near(c[0], s.Close.X+7, 0.5) || !near(c[1], s.Close.Y+7, 0.5) {
			t.Errorf("%s scene: close centre %v, live (%v, %v)", key, c, s.Close.X+7, s.Close.Y+7)
		}
	}
	sb, w := ref.Scenes.Sidebar, MacWindow.Sidebar
	for _, c := range [][2]float64{
		{float64(w.RowH), sb.RowHeight}, {float64(w.HeaderH), sb.HeaderH}, {float64(w.Indent), sb.Indent},
		{float64(w.HeaderX), sb.HeaderTextX}, {float64(w.IconX), sb.IconX}, {float64(w.Icon), sb.Icon},
		{float64(w.TextX), sb.TextX}, {float64(w.GroupGap), sb.GroupGap},
	} {
		if math.Abs(c[0]-c[1]) > 0.5 {
			t.Errorf("sidebar %+v, live %+v", w, sb)
			break
		}
	}
	for k, s := range ref.Scenes.Selections {
		if !near(MacWindow.Selection.Inset, s.Inset, 0.5) || !near(MacWindow.Selection.Radius, s.Radius, 0.5) {
			t.Errorf("selection %s: inset %v r %v, live %v r %v", k, MacWindow.Selection.Inset, MacWindow.Selection.Radius, s.Inset, s.Radius)
		}
	}
	if !ScrollEdgeVisible(true, true) || !ScrollEdgeVisible(false, false) || ScrollEdgeVisible(false, true) {
		t.Error("the scroll edge shows with content under the toolbar, or in an inactive window, and only then")
	}
}

func TestMotionAndReduceMotion(t *testing.T) {
	ref := loadRef(t)
	th := &Theme{Motion: Motion{Hot: 50, Active: 50, Focus: 50, Scroll: 40, Dim: 20, Fade: 18, Mode: 10, Standard: 0.25, Dismiss: 0.2, Sheet: 0.3}}
	if !near(th.Motion.Standard, ref.Behaviour.DefaultAnimationDuration, 0) {
		t.Errorf("Standard %v, NSAnimationContext %v", th.Motion.Standard, ref.Behaviour.DefaultAnimationDuration)
	}
	r := th.Resolve(&fakeText{}, 2, Light)
	if r.Motion != th.Motion {
		t.Errorf("motion without the system: %+v", r.Motion)
	}
	r.SetSystem(&SystemValues{Known: true, Settings: Settings{ReduceMotion: true}})
	m := r.Motion
	if m.Mode != 10 || m.Scroll != 40 || m.Hot != Instant || m.Dim != Instant || m.Fade != Instant || m.Standard != 0 || m.Sheet != 0 {
		t.Errorf("reduced: %+v", m)
	}
	if rate := 1 - math.Exp2(-float64(Instant)/240); rate != 1 {
		t.Errorf("Instant covers %v of the distance in a 240 Hz frame", rate)
	}
	r.SetSystem(&SystemValues{})
	if r.Motion != th.Motion {
		t.Errorf("motion after the system forgets: %+v", r.Motion)
	}
}

func TestContrastOn(t *testing.T) {
	// secondaryLabelColor on white: about 3.95:1 (the plan's number).
	if c := ContrastOn(0x0000007F, 0xFFFFFFFF, 0xFFFFFFFF); c < 3.9 || c > 4.0 {
		t.Errorf("Label2 on white: %.3f", c)
	}
	// Contrast ignores alpha: the same pair reads 21:1.
	if c := Contrast(0x0000007F, 0xFFFFFFFF); c < 20.9 {
		t.Errorf("Contrast does not ignore alpha: %.2f", c)
	}
	// A translucent background over what is under it: black 50 % over white
	// is grey, and white text on it has the grey's contrast.
	grey := Over(0x00000080, 0xFFFFFFFF)
	if a, b := ContrastOn(0xFFFFFFFF, 0x00000080, 0xFFFFFFFF), Contrast(0xFFFFFFFF, grey); math.Abs(a-b) > 1e-9 {
		t.Errorf("translucent background: %.3f, want %.3f", a, b)
	}
	// The under colour's alpha is ignored.
	if a, b := ContrastOn(0x000000D8, 0xFFFFFF00, 0x12345600), ContrastOn(0x000000D8, 0xFFFFFF00, 0x123456FF); a != b {
		t.Errorf("under's alpha matters: %v vs %v", a, b)
	}
}

func TestResolveTokens(t *testing.T) {
	r := (&Theme{}).Resolve(&fakeText{}, 2, Light)
	if r.Colors != Measured[Light] {
		t.Error("a theme without roles does not get the measured ones")
	}
	reg := r.Sizes[SizeRegular]
	if reg.Height != 48 || reg.Radius != 12 || reg.PadX != 24 || r.Sizes[SizeLarge].Radius != 28 || r.Sizes[SizeXLarge].Radius != 36 {
		t.Errorf("sizes in pixels: %+v", reg)
	}
	if r.Space.S8 != 16 || r.Radii.Window != 32 || r.Radii.Selection != 16 || r.Lines.Hairline != 1 || r.Lines.RowH != 48 {
		t.Errorf("space %+v radii %+v lines %+v", r.Space, r.Radii, r.Lines)
	}
	if u := r.Window.Toolbars[ToolbarUnified]; u.Height != 104 || u.Traffic != [2]float32{52, 52} || u.CapsuleH != 72 {
		t.Errorf("unified toolbar in pixels: %+v", u)
	}
	if r.Window.ScrollEdge.Line != 1 || r.Elevations[ElevRaised].Shadows[0].Blur != 24 {
		t.Errorf("scroll edge %+v, raised %+v", r.Window.ScrollEdge, r.Elevations[ElevRaised])
	}
	for s := range NumTextStyles {
		f := r.Styles[s]
		if f.Font == 0 || f.Line < 2*MacText[s].Line || f.Baseline <= 0 || f.Baseline > f.Line {
			t.Errorf("style %d: %+v", s, f)
		}
	}
	if b := r.Styles[StyleBody]; b.Line != 32 {
		t.Errorf("body line %v px at 2×, want 32", b.Line)
	}
	if Concentric(16, 4) != 12 || Concentric(8, 10) != 0 {
		t.Error("Concentric")
	}
	if m := r.Material(MatSidebar); m.Native != nativeMaterials || m.Fill != SidebarFill || m.FillInactive != SidebarFillInactive {
		t.Errorf("sidebar material %+v", m)
	}
	r.SetSystem(&SystemValues{Known: true, Settings: Settings{ReduceTransparency: true}})
	if r.Material(MatSidebar).Native {
		t.Error("Reduce Transparency keeps a live material")
	}
}

// SetSystem puts the system's colours in the System roles, only those, and
// only for a theme that follows them; the settings apply either way.
func TestSetSystem(t *testing.T) {
	sys := SystemValues{Known: true, Settings: Settings{IncreaseContrast: true, Scroller: ScrollerLegacy}}
	for m := range 2 {
		for i := range sys.Colors[m] {
			sys.Colors[m][i] = uint32(0x01000000*(m+1)) | uint32(i)<<8 | 0x80
		}
	}
	sys.Colors[Light][Label5] = 0 // a colour this system does not have

	th := &Theme{SystemColors: true, SystemAccent: true}
	r := th.Resolve(&fakeText{}, 1, Light)
	r.SetSystem(&sys)
	for m := range 2 {
		r.Mix(float32(m))
		for i := range NumSystemRoles {
			want := sys.Colors[m][i]
			if want == 0 {
				want = Measured[m][i]
			}
			if got := r.Colors[i]; got != want {
				t.Errorf("mode %d %v: %08X, want %08X", m, i, got, want)
			}
		}
		if r.Colors[GroupBox] != Measured[m][GroupBox] || r.Colors[Pane] != Measured[m][Pane] {
			t.Errorf("mode %d: theme roles changed", m)
		}
		if r.Colors[SidebarSel] != r.Colors[Accent]|0xFF || r.Colors[SelectionDivider] != Over(0xFFFFFF1A, r.Colors[Selection]|0xFF) {
			t.Errorf("mode %d: derived roles do not follow the accent and selection", m)
		}
	}
	if r.Settings != sys.Settings || *r.SystemValues() != sys {
		t.Errorf("settings %+v", r.Settings)
	}

	// The accent the system's colours were read with leaves them exact.
	r.SetAccent(sys.Colors[Dark][Accent])
	if r.Colors[FocusRing] != sys.Colors[Dark][FocusRing] || r.Colors[Selection] != sys.Colors[Dark][Selection] {
		t.Error("the system's own accent recoloured the system's colours")
	}
	// An accent of the app's recolours the accent roles.
	r.SetAccent(0xFF8800FF)
	if r.Colors[Accent] != 0xFF8800FF || r.Colors[FocusRing] != 0xFF8800FF&^0xFF|sys.Colors[Dark][FocusRing]&0xFF ||
		r.Colors[Selection] != 0xFF8800FF || r.Colors[SidebarSel] != 0xFF8800FF || r.Colors[OnAccent] != 0xFFFFFFFF {
		t.Errorf("app accent: accent %08X ring %08X selection %08X", r.Colors[Accent], r.Colors[FocusRing], r.Colors[Selection])
	}
	r.SetAccent(0)

	// A theme that keeps its own colours gets the settings only.
	th.SystemColors = false
	r = th.Resolve(&fakeText{}, 1, Light)
	r.SetSystem(&sys)
	if r.Colors != Measured[Light] || r.Settings != sys.Settings {
		t.Error("a theme without SystemColors took the system's colours, or not its settings")
	}
	// Forgetting the system returns to the theme's values.
	r.SetSystem(&SystemValues{})
	if r.Settings != (Settings{}) {
		t.Error("settings stay after the system forgets")
	}
}

func TestDerivedRolesKeepTheMeasuredBlue(t *testing.T) {
	c := Measured
	for m := range 2 {
		derive(&c[m], m, Measured[m][SidebarSel])
		if !within(c[m][AccentPressed], Measured[m][AccentPressed], 1) || !within(c[m][SelectionDivider], Measured[m][SelectionDivider], 1) || c[m][SidebarSel] != Measured[m][SidebarSel] {
			t.Errorf("mode %d: pressed %08X divider %08X sidebar %08X", m, c[m][AccentPressed], c[m][SelectionDivider], c[m][SidebarSel])
		}
	}
}

// Mixing and reading the roles allocates nothing, and the crossfade mixes
// every role.
func TestMixColors(t *testing.T) {
	r := (&Theme{}).Resolve(&fakeText{}, 1, Light)
	r.Mix(0.5)
	for i := range NumRoles {
		if Measured[0][i] != Measured[1][i] && (r.Colors[i] == Measured[0][i] || r.Colors[i] == Measured[1][i]) {
			t.Errorf("%v is not mixed", i)
		}
	}
	var sink uint32
	tt := float32(0)
	if n := testing.AllocsPerRun(100, func() {
		tt += 0.01
		r.Mix(tt)
		sink += r.Colors[Label2] + r.Role(Fill2) + uint32(r.Sizes[SizeSmall].Height) + uint32(r.Window.Toolbars[ToolbarUnified].Height)
	}); n != 0 {
		t.Errorf("mixing and reading tokens allocates %v times", n)
	}
	sys := SystemValues{Known: true}
	if n := testing.AllocsPerRun(20, func() { r.SetSystem(&sys); r.SetAccent(0x123456FF) }); n != 0 {
		t.Errorf("applying the system allocates %v times", n)
	}
	_ = sink
}

// The title follows the capsule before it by TitleGap, or the content
// pane's edge by TitleInset when no capsule precedes it in the pane: finder
// and settings (after a capsule), chat (unified, an empty pane: 18), notes
// (compact: 10).
func TestToolbarTitleX(t *testing.T) {
	ref := loadRef(t)
	r := (&Theme{}).Resolve(&fakeText{}, 2, Light)
	n := 0
	for scene, v := range ref.Scenes.Titles {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		style := map[string]ToolbarStyle{"unified": ToolbarUnified, "compact": ToolbarCompact}[m["style"].(string)]
		x, prev, pane := m["titleX"].(float64), m["prevCapsuleRight"].(float64), m["paneX"].(float64)
		tb := MacWindow.Toolbars[style]
		if got := tb.TitleX(float32(pane), float32(prev)); !near(got, x, 0.5) {
			t.Errorf("%s: title at %v, AppKit %v", scene, got, x)
		}
		px := r.Window.Toolbars[style]
		if got := px.TitleX(float32(2*pane), float32(2*prev)); !near(got, 2*x, 1) {
			t.Errorf("%s at 2×: title at %v px, AppKit %v", scene, got, 2*x)
		}
		n++
	}
	if n != 4 {
		t.Errorf("%d title layouts in the reference, want 4", n)
	}
	u := MacWindow.Toolbars[ToolbarUnified]
	if u.TitleX(280, 0) != 298 || u.TitleX(180, 274) != 284 {
		t.Errorf("unified: empty pane %v, after a capsule %v", u.TitleX(280, 0), u.TitleX(180, 274))
	}
}
