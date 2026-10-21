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
		{"नमस्ते दुनिया", "नदु"}, // (the vowel sign is part of the grapheme)
		{"ნინო ბერიძე", "ნბ"},    // Georgian has no title case of its own: unchanged
		{"", ""},
		{"   ", ""},
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
