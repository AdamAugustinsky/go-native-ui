package gallery

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
)

// The benchmark session does what it says: every part of it takes effect, and
// it ends where it began.
func TestBenchSessionDrivesTheGallery(t *testing.T) {
	b, err := NewBench(Options{Synthetic: true})
	if err != nil {
		t.Fatal(err)
	}
	r := newRigFor(t, b.Gallery)
	r.Use(b.p)
	g := b.Gallery

	var seen struct {
		tooltip, typed, cut, undone, pasted, select_, scrolled, end, selectedMany, desc, dialog, deleted, context, dark, activity, menu, search, back bool
		scenes                                                                                                                                        [numScenes]bool
	}
	notes0 := ""
	var first struct {
		sel, tab, radio int
		check, sw       bool
	}
	snapshot := func() {
		notes0 = string(g.notes.Bytes())
		first.sel, first.tab, first.radio, first.check, first.sw = g.sel, g.tab, g.radio, g.check, g.sw
	}
	snapshot()
	var sem ui.SemTree
	sem.Reserve(4096, 1<<16)
	frames := 0
	for ; frames < 60000 && b.Cycles() < 1; frames++ {
		r.Frame()
		r.Context().Semantics(&sem)
		for _, n := range sem.Nodes {
			switch n.Role {
			case ui.RoleTooltip:
				seen.tooltip = true
			case ui.RoleDialog:
				seen.dialog = seen.dialog || n.State&ui.SemModal != 0
			case ui.RoleRow:
				seen.scrolled = seen.scrolled || n.Pos[0] > 1000
				seen.end = seen.end || n.Pos[0] == 999_999
			case ui.RoleMenuItem:
				seen.menu = seen.menu || string(sem.Text(n.Name)) == "Undo"
			}
		}
		notes := string(g.notes.Bytes())
		seen.typed = seen.typed || notes == "Hello, gallery!abc" || notes == "Hello, galleryabcde"
		seen.cut = seen.cut || notes == ""
		// the paste brings back what the copy took, after the original text
		seen.pasted = seen.pasted || (len(r.Clipboard()) > 0 && notes == notes0+string(r.Clipboard()))
		seen.undone = seen.undone || (seen.cut && notes != "")
		seen.select_ = seen.select_ || g.sel == 1
		seen.selectedMany = seen.selectedMany || g.table.Sel.Count() > 3
		seen.desc = seen.desc || g.table.Order != nil
		seen.deleted = seen.deleted || string(g.status) == "Deleted"
		seen.context = seen.context || string(g.status) == "Chose “Copy Path”"
		seen.dark = seen.dark || g.Mode() == theme.Dark
		seen.activity = seen.activity || g.working
		seen.scenes[g.scene()] = true
		seen.search = seen.search || g.query.Len() > 0
		seen.back = seen.back || g.histAt+1 < g.histN
	}
	if err := b.Err(); err != nil {
		t.Fatal(err)
	}
	if b.Cycles() < 1 {
		t.Fatalf("the session did not finish in %d frames", frames)
	}
	for name, ok := range map[string]bool{
		"a tooltip": seen.tooltip, "typed text": seen.typed, "a cut": seen.cut, "an undo": seen.undone, "a paste": seen.pasted,
		"a choice in the Select": seen.select_, "scrolling the table": seen.scrolled, "the table's last row": seen.end,
		"a selection of many rows": seen.selectedMany, "a descending sort": seen.desc, "the modal dialog": seen.dialog,
		"the dialog's action": seen.deleted, "the context menu's command": seen.context, "the dark mode": seen.dark,
		"the activity bar": seen.activity, "the sidebar search": seen.search, "going back": seen.back, "the Edit menu": seen.menu,
	} {
		if !ok {
			t.Errorf("the session never showed %s", name)
		}
	}
	// it ends where it began
	if g.Mode() != theme.Light || g.askDelete || g.working {
		t.Errorf("the session ends in mode %v, dialog %v, activity %v", g.Mode(), g.askDelete, g.working)
	}
	if got := string(g.notes.Bytes()); got != notes0 {
		t.Errorf("Notes ends as %q, began as %q", got, notes0)
	}
	if g.sel != first.sel || g.tab != first.tab || g.radio != first.radio || g.check != first.check || g.sw != first.sw {
		t.Errorf("a choice ends differently from where it began: sel %d/%d tab %d/%d radio %d/%d check %v/%v switch %v/%v (now/began)",
			g.sel, first.sel, g.tab, first.tab, g.radio, first.radio, g.check, first.check, g.sw, first.sw)
	}
	if g.table.Order != nil {
		t.Error("the table ends sorted descending")
	}
	for sc, ok := range seen.scenes {
		if !ok {
			t.Errorf("the session never showed the %s scene", defs[sc].title)
		}
	}
	if g.scene() != scPlayground || g.query.Len() != 0 {
		t.Errorf("the session ends in the %s scene with the search %q, began in the Playground with none", defs[g.scene()].title, g.query.Bytes())
	}
}

// The scenes' scripts run to their end on the fake text system: every target
// they name exists and is in view.
func TestSceneScriptsRun(t *testing.T) {
	for _, sc := range scenes {
		t.Run(sc.name, func(t *testing.T) {
			r := newRig(t, Options{Still: true, Synthetic: true})
			r.Frames(5)
			r.Play(sc.steps()...)
		})
	}
}

// The pictures render: a PNG of the requested size, the dark mode darker than
// the light, and the dialog scene different from the main one.
func TestShotsRender(t *testing.T) {
	dir := t.TempDir()
	probe := func(name string, o ShotOptions) float64 {
		t.Helper()
		path := filepath.Join(dir, name+".png")
		if _, err := Shot(path, o); err != nil {
			if os.Getenv("CI") == "" {
				t.Skipf("offscreen rendering: %v", err)
			}
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		img, err := png.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != o.Width || b.Dy() != o.Height {
			t.Fatalf("%s is %v, want %d×%d", name, b, o.Width, o.Height)
		}
		var sum float64
		n := 0
		for y := 0; y < o.Height; y += 8 {
			for x := 0; x < o.Width; x += 8 {
				r, g, b, _ := img.At(x, y).RGBA()
				sum += float64(r+g+b) / 3 / 65535
				n++
			}
		}
		return sum / float64(n)
	}
	small := ShotOptions{Width: 960, Height: 600, Scale: 0.75, Rows: 5000}
	light := probe("light", small)
	dialog := small
	dialog.Scene = "dialog"
	if d := probe("dialog", dialog); d == light {
		t.Error("the dialog scene looks like the main scene")
	}
	small.Preference = theme.ForceDark
	if dark := probe("dark", small); dark >= light-0.2 {
		t.Errorf("the dark picture's mean brightness %.2f is not well below the light's %.2f", dark, light)
	}
	// following the system: a dark system gives the dark picture, a light one the light
	small.Preference = theme.System
	small.System = ui.Appearance{Dark: true, Known: true}
	if d := probe("system-dark", small); d >= light-0.2 {
		t.Errorf("a dark system gives the mean brightness %.2f, not well below the light's %.2f", d, light)
	}
	small.System.Dark = false
	if l := probe("system-light", small); l < light-0.01 || l > light+0.01 {
		t.Errorf("a light system gives the mean brightness %.2f, want the light's %.2f", l, light)
	}
}

// A window smaller than the scene (the smallest the window may be, which is
// what a small CI display gives) still runs the whole session: the steps
// scroll their targets into view.
func TestBenchSessionInASmallWindow(t *testing.T) {
	b, err := NewBench(Options{Synthetic: true})
	if err != nil {
		t.Fatal(err)
	}
	r := newRigFor(t, b.Gallery)
	r.Resize(MinWidthPt, MinHeightPt)
	r.Use(b.p)
	frames := 0
	for ; frames < 60000 && b.Cycles() < 2 && b.Err() == nil; frames++ {
		r.Frame()
	}
	if err := b.Err(); err != nil {
		t.Fatal(err)
	}
	if b.Cycles() < 2 {
		t.Fatalf("two sessions did not finish in %d frames; cycles %d", frames, b.Cycles())
	}
	if b.Gallery.Mode() != theme.Light || b.Gallery.askDelete {
		t.Error("the session does not end where it began")
	}
}
