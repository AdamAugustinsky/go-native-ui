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
	r.p = b.p
	g := b.Gallery

	var seen struct {
		tooltip, typed, cut, undone, select_, scrolled, end, selectedMany, desc, dialog, deleted, context, dark, activity, menu, divider bool
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
		r.frame()
		r.c.Semantics(&sem)
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
		seen.undone = seen.undone || (seen.cut && notes != "")
		seen.select_ = seen.select_ || g.sel == 1
		seen.selectedMany = seen.selectedMany || g.table.Sel.Count() > 3
		seen.desc = seen.desc || g.table.Order != nil
		seen.deleted = seen.deleted || string(g.status) == "Deleted"
		seen.context = seen.context || string(g.status) == "Chose “Copy Path”"
		seen.dark = seen.dark || g.Mode() == theme.Dark
		seen.activity = seen.activity || g.working
		seen.divider = seen.divider || g.split.Ratio[0] > 0.45
	}
	if err := b.Err(); err != nil {
		t.Fatal(err)
	}
	if b.Cycles() < 1 {
		t.Fatalf("the session did not finish in %d frames", frames)
	}
	for name, ok := range map[string]bool{
		"a tooltip": seen.tooltip, "typed text": seen.typed, "a cut": seen.cut, "an undo": seen.undone,
		"a choice in the Select": seen.select_, "scrolling the table": seen.scrolled, "the table's last row": seen.end,
		"a selection of many rows": seen.selectedMany, "a descending sort": seen.desc, "the modal dialog": seen.dialog,
		"the dialog's action": seen.deleted, "the context menu's command": seen.context, "the dark mode": seen.dark,
		"the activity bar": seen.activity, "a dragged divider": seen.divider, "the Edit menu": seen.menu,
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
	if d := g.split.Ratio[0] - 0.42; d < -0.003 || d > 0.003 {
		t.Errorf("the divider ends at %v, began at 0.42", g.split.Ratio[0])
	}
}

// The scenes' scripts run to their end on the fake text system: every target
// they name exists and is in view.
func TestSceneScriptsRun(t *testing.T) {
	for _, sc := range scenes {
		t.Run(sc.name, func(t *testing.T) {
			r := newRig(t, Options{Still: true, Synthetic: true})
			r.frames(5)
			r.run(sc.steps()...)
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
	small.Mode = theme.Dark
	if dark := probe("dark", small); dark >= light-0.2 {
		t.Errorf("the dark picture's mean brightness %.2f is not well below the light's %.2f", dark, light)
	}
}
