package rexui_test

import (
	"bytes"
	"image"
	"image/png"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
)

// Initials are the first letter or digit of the first and last words,
// whatever comes before it in the word, in any script, whole graphemes.
func TestAvatarInitials(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"Ada Lovelace", "AL"},
		{"Ada", "A"},
		{"ada king lovelace", "AL"},
		{"Name (You)", "NY"},
		{"  @ada   \"Bea\"  ", "AB"},
		{"3M Company", "3C"},
		{"🎉 Party Time", "PT"},
		{"Party 🎉", "P"},
		{"🎉 🎂", "🎉"},
		{"👨‍👩‍👧 family", "F"},
		{"👨‍👩‍👧", "👨‍👩‍👧"},
		{"élodie durand", "ÉD"},
		{"e\u0301lodie", "E\u0301"},
		{"Ελένη Παπαδοπούλου", "ΕΠ"},
		{"Анна Каренина", "АК"},
		{"王小明", "王"},
		{"محمد علي", "مع"},
		{"김 민수", "김민"},
		{"नमस्ते दुनिया", "नदु"},    // (the vowel sign is part of the grapheme)
		{"ნინო ბერიძე", "ნბ"},       // Georgian has no title case of its own: unchanged
		{"Ada Lovelace-King", "AK"}, // a hyphen ends a word, as a space does
		{"Ada-Lovelace", "AL"},
		{"Mary-Jane", "MJ"},
		{"Ada – Lovelace", "AL"}, // an en dash too
		{"-Ada", "A"},
		{"Ada-", "A"},
		{"", "#"}, // a blank name shows BlankInitial
		{"   ", "#"},
		{"\t\n \u00a0", "#"},
		{"(You)", "Y"},
	} {
		if got := string(rexui.Initials(nil, tc.name)); got != tc.want {
			t.Errorf("Initials(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
	var buf [16]byte
	if n := testing.AllocsPerRun(50, func() { rexui.Initials(buf[:0], "Name (You)") }); n != 0 {
		t.Errorf("Initials allocates %v times", n)
	}
}

// A blank name draws the hash sign, not an empty disc.
func TestAvatarBlankNameShowsHash(t *testing.T) {
	var id ui.ID
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		id = rexui.Avatar(g.k, "  ", rexui.AvatarOpts{})
	}))
	r := g.c.Rect(id)
	glyphs := 0
	for _, q := range g.list.Quads {
		if q.Mode == gpu.ModeMaskR8 && q.Rect[0] >= r[0] && q.Rect[2] <= r[2] && q.Rect[1] >= r[1] && q.Rect[3] <= r[3] {
			glyphs++
		}
	}
	if glyphs != 1 {
		t.Errorf("%d glyphs in the avatar of a blank name, want the one hash sign", glyphs)
	}
	if rexui.BlankInitial != "#" {
		t.Errorf("BlankInitial is %q", rexui.BlankInitial)
	}
}

// hasFont reports whether the text system has made a font of d already (the
// fake hands out keys in order, so a font not made yet gets the next key).
func hasFont(g *rig, d text.FontDesc) bool {
	ts := g.k.TextSystem()
	fresh := ts.Font(text.FontDesc{SizePt: 91.37, Weight: 100, Scale: g.scale})
	return ts.Font(d) < fresh
}

// InitialsScale and InitialsWeight set the initials' font from the avatar's
// diameter; both zero keep the size's text style, and a frame allocates nothing.
func TestAvatarInitialsStyle(t *testing.T) {
	semi := func(pt float32) text.FontDesc {
		return text.FontDesc{SizePt: pt, Weight: theme.WeightSemibold, Scale: 2}
	}
	build := func(o rexui.AvatarOpts, stack bool) func(g *rig) {
		return func(g *rig) {
			if stack {
				rexui.AvatarStack(g.k, "Members", []string{"Ada Lovelace", "Bea"}, 4, o)
				return
			}
			rexui.Avatar(g.k, "Ada Lovelace", o)
		}
	}
	for _, stack := range []bool{false, true} {
		const size = 40
		want := semi(0.38 * size)
		g := newRig(t, theme.Light, nil, column(build(rexui.AvatarOpts{Size: size}, stack)))
		if hasFont(g, want) {
			t.Fatalf("stack %v: the default initials are already 0.38 of the diameter, semibold", stack)
		}
		def := text.FontDesc{SizePt: theme.MacText[theme.StyleHeadline].Size, Weight: theme.MacText[theme.StyleHeadline].Weight, Scale: 2}
		if !hasFont(g, def) {
			t.Errorf("stack %v: the default initials are not the headline style", stack)
		}
		g = newRig(t, theme.Light, nil, column(build(rexui.AvatarOpts{Size: size, InitialsScale: 0.38, InitialsWeight: theme.WeightSemibold}, stack)))
		if !hasFont(g, want) {
			t.Errorf("stack %v: the initials are not semibold at 0.38 of the diameter", stack)
		}
		if n := testing.AllocsPerRun(20, g.frame); n != 0 {
			t.Errorf("stack %v: a frame with the initials' style allocates %v times", stack, n)
		}
		// one of the two set: the other is the text style's
		g = newRig(t, theme.Light, nil, column(build(rexui.AvatarOpts{Size: size, InitialsWeight: theme.WeightSemibold}, stack)))
		if !hasFont(g, semi(def.SizePt)) {
			t.Errorf("stack %v: InitialsWeight alone changed the size", stack)
		}
		g = newRig(t, theme.Light, nil, column(build(rexui.AvatarOpts{Size: size, InitialsScale: 0.5}, stack)))
		if !hasFont(g, text.FontDesc{SizePt: 0.5 * size, Weight: def.Weight, Scale: 2}) {
			t.Errorf("stack %v: InitialsScale alone changed the weight", stack)
		}
	}
}

// An avatar's initials take a colour of the app's own.
func TestAvatarLiteralColor(t *testing.T) {
	var id ui.ID
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		id = rexui.Avatar(g.k, "Ada Lovelace", rexui.AvatarOpts{Color: rexui.OfRGBA(0x25D366FF)})
	}))
	r := g.c.Rect(id)
	found := false
	for _, q := range g.list.Quads {
		if q.Mode == gpu.ModeShape && q.Rect == r && q.Color[0] == 0x25D366FF {
			found = true
		}
	}
	if !found {
		t.Error("no disc in the app's colour")
	}
	if got := rexui.OfRGBA(0x11223344).RGBA(g.k.Theme()); got != 0x11223344 {
		t.Errorf("OfRGBA is %#x", got)
	}
	if got := rexui.OfRGBA(0x112233FF); got == (rexui.Tint{}) {
		t.Error("a literal colour is the zero Tint")
	}
}

// texs is an image cache renderer that only hands out ids.
type texs struct{ n gpu.TextureID }

func (r *texs) NewTexture(gpu.TextureKind, int, int) gpu.TextureID    { r.n++; return r.n + 100 }
func (r *texs) Upload(gpu.TextureID, int, int, int, int, []byte, int) {}
func (r *texs) Stage(gpu.TextureID, int, int, int, int, []byte, int)  {}
func (r *texs) FreeTexture(gpu.TextureID)                             {}

// A stack shows the pictures that are ready, initials for the others, and
// "+N" for the rest.
func TestAvatarStackShowsPictures(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	cache := img.NewCache(img.Options{Renderer: &texs{}, Wake: func() {}})
	defer cache.Close(time.Second)
	names := []string{"Ada Lovelace", "Bea Smith", "Cy Young", "Di Prince", "Ed Wood"}
	pics := make([]img.Handle, 2) // a picture for the first, none for the second
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		g.k.SetImages(cache)
		cache.Poll()
		pics[0], _ = cache.Request(img.Key{Hash: img.HashBytes(b.Bytes()), W: 56, H: 56, Flags: img.Cover}, img.Bytes(b.Bytes()))
		rexui.AvatarStack(g.k, "Members", names, 3, rexui.AvatarOpts{Size: 28, Images: pics})
	}))
	deadline := time.Now().Add(5 * time.Second)
	for cache.State(pics[0]) != img.Ready && time.Now().Before(deadline) {
		g.frame()
		time.Sleep(time.Millisecond)
	}
	g.frames(2)
	if cache.State(pics[0]) != img.Ready {
		t.Fatalf("image %v", cache.State(pics[0]))
	}
	images, discs := 0, 0
	for _, q := range g.list.Quads {
		switch {
		case q.Mode == gpu.ModeRGBA && q.Rect[2]-q.Rect[0] == 56:
			images++
		case q.Mode == gpu.ModeShape && q.Rect[2]-q.Rect[0] == 56 && q.Radii[0] == 28:
			discs++
		}
	}
	if images != 1 || discs != 3 { // Bea and Cy as initials, and +2
		t.Errorf("%d pictures and %d initials discs, want 1 and 3", images, discs)
	}
	if n := testing.AllocsPerRun(20, g.frame); n != 0 {
		t.Errorf("a frame of the stack allocates %v times", n)
	}
}
