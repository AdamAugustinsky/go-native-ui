package pilot

import (
	"errors"
	"fmt"
	"time"

	"gonativeui/app"
	"gonativeui/cmd/rexlook/rex/rexrig"
	"gonativeui/fs"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The offscreen scene: two folders with fixed names (some not ASCII, with an
// emoji), sizes and modification times in UTC; the left pane has the keyboard,
// shows hidden files and a selection of three rows, the right pane has a filter.

type item struct {
	name  string
	kind  fs.Kind
	flags uint8
	size  int64
	when  string // 2006-01-02 15:04 UTC
}

var sceneLeft = []item{
	{"cmd", fs.KindDir, 0, 0, "2026-10-17 09:12"},
	{"docs", fs.KindDir, 0, 0, "2026-10-16 18:40"},
	{"fs", fs.KindDir, 0, 0, "2026-10-17 11:03"},
	{"app", fs.KindDir, 0, 0, "2026-10-15 22:31"},
	{"ui", fs.KindDir, 0, 0, "2026-10-17 10:55"},
	{".git", fs.KindDir, fs.FlagHidden, 0, "2026-10-17 11:40"},
	{".gitignore", fs.KindFile, fs.FlagHidden, 214, "2026-09-02 08:01"},
	{"LICENSE", fs.KindFile, 0, 11357, "2026-09-02 08:01"},
	{"Makefile", fs.KindFile, 0, 1894, "2026-10-01 14:22"},
	{"README.md", fs.KindFile, 0, 6482, "2026-10-17 07:48"},
	{"build.sh", fs.KindFile, fs.FlagExecutable, 733, "2026-10-12 16:09"},
	{"file1.txt", fs.KindFile, 0, 120, "2026-10-03 12:00"},
	{"file2.txt", fs.KindFile, 0, 480, "2026-10-03 12:05"},
	{"file10.txt", fs.KindFile, 0, 1_950, "2026-10-03 12:10"},
	{"go.mod", fs.KindFile, 0, 312, "2026-10-17 09:30"},
	{"go.sum", fs.KindFile, 0, 18_442, "2026-10-17 09:30"},
	{"latest", fs.KindSymlink, fs.FlagSymlinkDir, 18, "2026-10-14 20:15"},
	{"main.go", fs.KindFile, 0, 4_127, "2026-10-17 10:02"},
	{"Résumé.pdf", fs.KindFile, 0, 248_117, "2026-08-21 17:44"},
	{"日本語のメモ.txt", fs.KindFile, 0, 2_048, "2026-10-09 13:37"},
	{"😀 emoji party.png", fs.KindFile, 0, 1_523_900, "2026-10-10 21:08"},
}

var sceneRight = []item{
	{"Archive", fs.KindDir, 0, 0, "2026-07-30 10:00"},
	{"Reports", fs.KindDir, 0, 0, "2026-10-11 15:20"},
	{"Screenshots", fs.KindDir, 0, 0, "2026-10-16 08:15"},
	{"core-dump", fs.KindOther, 0, 0, "2026-10-13 03:12"},
	{"installer.pkg", fs.KindFile, 0, 85_330_000, "2026-10-08 19:59"},
	{"recipe-notes.txt", fs.KindFile, 0, 3_204, "2026-09-27 12:44"},
	{"release-1.4.2.zip", fs.KindFile, 0, 12_882_113, "2026-10-14 09:05"},
	{"report-2026-10.xlsx", fs.KindFile, 0, 96_781, "2026-10-12 14:18"},
	{"report-2026-09.xlsx", fs.KindFile, 0, 91_220, "2026-09-12 14:18"},
	{"resume-draft.docx", fs.KindFile, 0, 37_642, "2026-10-01 11:29"},
	{"tickets.pdf", fs.KindFile, 0, 402_513, "2026-10-05 18:03"},
	{"trace.log", fs.KindFile, 0, 7_811_456, "2026-10-17 11:21"},
	{"travel.mov", fs.KindFile, 0, 1_203_445_800, "2026-09-20 16:47"},
}

// sceneListing builds a sorted listing from fixed items.
func sceneListing(items []item) *fs.Listing {
	l := new(fs.Listing)
	for _, it := range items {
		t, err := time.Parse("2006-01-02 15:04", it.when)
		if err != nil {
			panic(err)
		}
		off := len(l.Names)
		l.Names = append(l.Names, it.name...)
		l.Entries = append(l.Entries, fs.Entry{
			Size: it.size, MTime: t.UnixNano(), NameOff: uint32(off), NameLen: uint16(len(it.name)),
			Kind: it.kind, Flags: it.flags,
		})
	}
	fs.Sort(l, fs.SortName, false)
	return l
}

func ready(dir string, l *fs.Listing) <-chan Ready {
	ch := make(chan Ready, 1)
	ch <- Ready{Dir: dir, L: l, At: platform.Now()}
	return ch
}

// SceneOptions are the Options of the offscreen scene.
func SceneOptions() Options {
	return Options{
		Home: nativePath("/Users/rex"), Traffic: true, Loc: time.UTC,
		Initial: [2]<-chan Ready{
			ready(nativePath("/Users/rex/Projects/pilot"), sceneListing(sceneLeft)),
			ready(nativePath("/Users/rex/Downloads"), sceneListing(sceneRight)),
		},
	}
}

// SceneSetup puts the offscreen scene in its state: the left pane shows hidden
// files with three rows selected, the right pane filters for "re". Call it
// before the first frame; ScenePost finishes it once the rows exist.
func (pl *Pilot) SceneSetup() {
	l, r := pl.panes[0], pl.panes[1]
	l.hideHidden = false
	l.startRows()
	r.filterOpen = true
	r.filter.SetString("re")
	r.setQuery()
	pl.active = 0
}

// ScenePost selects rows 5..7 of the left pane (the cursor on the last).
func (pl *Pilot) ScenePost() {
	v := &pl.panes[0].v
	if v.nRows() > 8 {
		v.selectOnly(v.entryAt(5), 5)
		v.selectRange(7)
		pl.panes[0].reveal = 7
	}
	if rv := &pl.panes[1].v; rv.nRows() > 0 {
		rv.selectOnly(rv.entryAt(0), 0)
	}
}

// RenderDirsShot renders the first frame Pilot shows for two real directories
// (sorted by name, hidden files hidden, local time), without traffic lights
// since a window draws its own. BounceRec uses it as the expected complete
// content of a launch (tools/bouncerec).
func RenderDirsShot(path string, w, h int, scale float32, home string, dirs [2]string) (quads, drawCalls int, err error) {
	opt := Options{Home: home}
	for i, d := range dirs {
		l := new(fs.Listing)
		if err := fs.ListDir(d, l); err == nil {
			fs.Sort(l, fs.SortName, false)
		}
		opt.Initial[i] = ready(d, l)
	}
	return shoot(New(opt), path, w, h, scale, true, nil, nil)
}

// RenderShot renders the scene offscreen at w×h pixels and the given scale
// (the window is 1200×760 pt) into a PNG at path (with the window's
// superellipse corners as transparency when corners is set), and returns the
// quads and draw calls of the frame. It runs on every OS the renderer does.
func RenderShot(path string, w, h int, scale float32, corners bool) (quads, drawCalls int, err error) {
	pl := New(SceneOptions())
	return shoot(pl, path, w, h, scale, corners, pl.SceneSetup, pl.ScenePost)
}

// shoot renders pl offscreen until it settles (setup before the first frame,
// post once the rows exist; either may be nil) and writes the PNG.
func shoot(pl *Pilot, path string, w, h int, scale float32, corners bool, setup, post func()) (quads, drawCalls int, err error) {
	o, err := app.NewOffscreen(Config(), w, h, scale, pl)
	if err != nil {
		return 0, 0, err
	}
	defer o.Close()
	settle := func() error {
		for n := 0; n < 600; n++ {
			o.Frame()
			if !o.UI.Animating() {
				return nil
			}
		}
		return errors.New("pilot: the scene did not settle")
	}
	if setup != nil {
		setup()
	}
	if err := settle(); err != nil {
		return 0, 0, err
	}
	if post != nil {
		post()
	}
	if err := settle(); err != nil {
		return 0, 0, err
	}
	img, _ := o.Frame()
	meta := map[string]any{
		"cellAdvancePt": pl.CellAdvancePt(), "rowPitchPt": pl.RowPitchPt(), "monoFace": pl.MonoFace(),
		"quads": len(o.Quads()), "drawCalls": o.R.DrawCalls(),
	}
	if err := rexrig.WritePNG(path, img.Pix, w, h, corners, meta); err != nil {
		return 0, 0, fmt.Errorf("pilot: write %s: %w", path, err)
	}
	return len(o.Quads()), o.R.DrawCalls(), nil
}

var _ = ui.KeyEnter
