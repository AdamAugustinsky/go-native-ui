package uisim_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/cmd/gallery/gallery"
	"gonativeui/examples/notes/notes"
	"gonativeui/platform"
	"gonativeui/text"
)

func TestSimUICachePressureWhileHidden(t *testing.T) {
	b := &scene{ids: []uint64{1, 2, 3}}
	s, err := app.NewSim(app.Config{}, 900, 700, 1, b, app.SimOptions{Seed: 1, Render: true})
	must(t, err)
	defer s.Close()
	must(t, s.Advance(time.Second))
	b.k.Focus(b.fieldKey)
	b.field.SetSelection(0, 5)
	wake(t, s, 12*time.Second)
	b.burst = 6000
	wake(t, s, 100*time.Millisecond)
	peak, err := s.Memory()
	must(t, err)
	if peak.Boxes < 6000 || peak.Instances < 6000 {
		t.Fatalf("rendered stress did not grow capacities: %+v", peak)
	}
	b.burst = 0
	wake(t, s, 100*time.Millisecond)
	pixels := slices.Clone(s.FramePixels(0).Pix)
	// Every slot of the three-buffer GPU ring needs sixty calm uses.
	for range 210 {
		wake(t, s, 20*time.Millisecond)
	}
	floor, err := s.CheckMemory(app.SimMemory{Boxes: 2048, Instances: 8192})
	must(t, err)
	if floor.Boxes >= peak.Boxes || floor.Instances >= peak.Instances || !slices.Equal(pixels, s.FramePixels(0).Pix) {
		t.Fatalf("trimming changed pixels or retained peak storage: peak=%+v floor=%+v", peak, floor)
	}
	before := tree(b.a.UI)
	ts := b.a.Text
	runGen, atlasGen := ts.RunGeneration(), ts.AtlasGeneration()
	hide(t, s)
	quiet(t, s, time.Hour)
	font := ts.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: 1})
	// Pressure the real shared caches as another active window could do.
	line := []byte(strings.Repeat("abcdefghijklmno ", 1024))
	for i := 0; ts.RunGeneration() == runGen && i < 100; i++ {
		key := strconv.AppendInt(line[:len(line):len(line)], int64(i), 10)
		ts.Shape(font, key)
	}
	if ts.RunGeneration() == runGen {
		t.Fatal("run cache was not evicted")
	}
	for size := float32(128); ts.AtlasGeneration() == atlasGen && size < 1024; size += 16 {
		f := ts.Font(text.FontDesc{SizePt: size, Weight: 400, Scale: 1})
		run := ts.Run(ts.Shape(f, []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789")))
		for i, gid := range run.Glyphs {
			ts.Glyph(run.Faces[i], gid, 0)
		}
	}
	if ts.AtlasGeneration() == atlasGen {
		t.Fatal("glyph atlas was not evicted")
	}
	quiet(t, s, 7*time.Hour)
	show(t, s)
	equalTrees(t, tree(b.a.UI), before)
	if !slices.Equal(pixels, s.FramePixels(0).Pix) {
		t.Fatal("run/atlas eviction changed returned pixels")
	}
	if !slices.Equal(b.field.Selected(), []byte("Draft")) {
		t.Fatal("cache eviction lost selection")
	}
	_, err = s.CheckMemory(app.SimMemory{RunGlyphs: text.MaxCachedGlyphs, RunText: text.MaxCachedTextBytes})
	must(t, err)
	// Atlas UVs can change during eviction; once rebuilt, an unchanged
	// return must once again have exactly the same draw list.
	draw, _ := s.FrameHash(0)
	s.AddCheck(app.SimReturnFrame(0, draw))
	hide(t, s)
	quiet(t, s, time.Hour)
	show(t, s)
	t.Logf("run generations %d -> %d; atlas generations %d -> %d", runGen, ts.RunGeneration(), atlasGen, ts.AtlasGeneration())
	t.Logf("pixel-identical trims: boxes %d -> %d, instances %d -> %d", peak.Boxes, floor.Boxes, peak.Instances, floor.Instances)
}

func TestSimUIExampleBuilders(t *testing.T) {
	for _, name := range []string{"notes", "gallery"} {
		t.Run(name, func(t *testing.T) {
			var b app.Builder
			var cfg app.Config
			var err error
			if name == "notes" {
				b, err = notes.New()
				cfg = notes.Config()
			} else {
				b, err = gallery.New(gallery.Options{Still: true, Start: "Table"})
				cfg = gallery.Config()
			}
			must(t, err)
			s, err := app.NewSim(cfg, 1100, 720, 1, b, app.SimOptions{Seed: 1})
			must(t, err)
			defer s.Close()
			must(t, s.Advance(time.Second))
			for _, scale := range []float32{2, 1.5, 1} {
				hide(t, s)
				quiet(t, s, 8*time.Hour)
				must(t, s.Appearance(scale != 1))
				event(t, s, platform.Event{Kind: platform.EvResize, W: int32(1100 * scale), H: int32(720 * scale), Scale: scale})
				show(t, s)
			}
			before := tree(s.Window(0).UI)
			draw, _ := s.FrameHash(0)
			s.AddCheck(app.SimReturnFrame(0, draw))
			hide(t, s)
			quiet(t, s, 24*time.Hour)
			show(t, s)
			equalTrees(t, tree(s.Window(0).UI), before)
		})
	}
}
