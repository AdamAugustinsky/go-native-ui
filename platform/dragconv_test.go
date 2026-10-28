package platform

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestBuildDropFilesRoundTrips(t *testing.T) {
	paths := []string{`C:\Users\a b\é.txt`, `D:\𝄞 music\x.png`, "/tmp/a#b%c"}
	blk := buildDropFiles(paths)
	got, n, ok := appendDropFiles(nil, blk, MaxPaste, nil)
	if !ok || n != len(paths) {
		t.Fatalf("read %d paths (ok %v), want %d", n, ok, len(paths))
	}
	if want := strings.Join(paths, "\x00") + "\x00"; string(got) != want {
		t.Fatalf("paths = %q, want %q", got, want)
	}
	if _, n, _ := appendDropFiles(nil, buildDropFiles(nil), MaxPaste, nil); n != 0 {
		t.Fatalf("an empty list read as %d paths", n)
	}
	// A path with a NUL inside would split in two: skipped, and the rest kept.
	got, n, _ = appendDropFiles(nil, buildDropFiles([]string{"a\x00b", "c"}), MaxPaste, nil)
	if n != 1 || string(got) != "c\x00" {
		t.Fatalf("got %q (%d paths)", got, n)
	}
}

func TestURIListRoundTrips(t *testing.T) {
	paths := []string{"/home/a/Downloads/notes.txt", "/tmp/with space/é #1 %20.png", "/tmp/a?b=c"}
	list := appendURIList(nil, paths)
	if !bytes.HasSuffix(list, []byte("\r\n")) || bytes.Count(list, []byte("\r\n")) != len(paths) {
		t.Fatalf("list = %q", list)
	}
	if strings.ContainsAny(string(list), " é#") {
		t.Fatalf("list is not escaped: %q", list)
	}
	got, n := appendURIListPaths(nil, list)
	if n != len(paths) || string(got) != strings.Join(paths, "\x00")+"\x00" {
		t.Fatalf("read back %q (%d paths)", got, n)
	}
}

func TestEffectsAndOps(t *testing.T) {
	for _, op := range []DragOp{DragOpCopy, DragOpLink, DragOpMove, DragOpAll, DragOpCopy | DragOpMove} {
		if got := opsOfEffects(effectsOfOps(op)); got != op {
			t.Errorf("ops %d came back as %d", op, got)
		}
	}
	if effectsOfOps(DragOpNone) != effectNone || opsOfEffects(0x80000000) != DragOpNone {
		t.Error("none and DROPEFFECT_SCROLL are no operation")
	}
	if got := effectsOfOps(DragOpMove); got != effectMove {
		t.Errorf("move = %d, want DROPEFFECT_MOVE", got)
	}
}

func TestAnswerDragHoldsTheAnswerToTheOffer(t *testing.T) {
	if a := answerDrag(nil, 1, 2, DragFiles, DragOpMove|DragOpLink); a.Op != DragOpLink || a.Accept != DragAny {
		t.Errorf("no handler: %+v, want link and everything", a)
	}
	var seen [2]float32
	h := func(x, y float32, offered DragKinds, allowed DragOp) DragAnswer {
		seen = [2]float32{x, y}
		return DragAnswer{Op: DragOpCopy | DragOpMove, Accept: DragText, Target: 9}
	}
	if a := answerDrag(h, 3, 4, DragFiles, DragOpAll); a != (DragAnswer{}) || seen != [2]float32{3, 4} {
		t.Errorf("a handler that takes another kind: %+v", a)
	}
	// Two operations, or one the source does not allow, become the first allowed one.
	if a := answerDrag(h, 0, 0, DragText, DragOpMove); a.Op != DragOpMove || a.Target != 9 {
		t.Errorf("copy|move with only move allowed: %+v", a)
	}
}

func TestDropKindAndOwnData(t *testing.T) {
	if dropKind(DragText|DragImage) != DragImage || dropKind(DragAny) != DragFiles || dropKind(DragText) != DragText {
		t.Error("a drop delivers the first of files, image and text")
	}
	items := []DragItem{{Text: "hello"}, {Path: "/a"}, {PNG: []byte{1, 2}}, {Path: "/b"}}
	if p, n := ownPaths(items, 100); n != 2 || string(p) != "/a\x00/b\x00" {
		t.Errorf("paths %q (%d)", p, n)
	}
	if p, n := ownPaths(items, 5); p != nil || n != 0 {
		t.Errorf("a list that does not fit whole is refused: %q (%d)", p, n)
	}
	if string(ownText(items, 100)) != "hello" || ownText(items, 3) != nil || !bytes.Equal(ownPNG(items), []byte{1, 2}) {
		t.Error("own text or image")
	}
	if k := specKinds(items); k != DragAny {
		t.Errorf("kinds = %d, want all", k)
	}
	if !reflect.DeepEqual(ownFilePaths(items), []string{"/a", "/b"}) {
		t.Error("file paths")
	}
	if specHasPromise(&DragSpec{Items: items}) || !specHasPromise(&DragSpec{Items: []DragItem{{Promise: &FilePromise{}}}}) {
		t.Error("specHasPromise")
	}
}

func TestPushDropData(t *testing.T) {
	q := newEventQueue()
	q.pushDropData(Event{Kind: EvDrop, Code: uint32(DragText)}, []byte("abc"), 0)
	q.pushDropData(Event{Kind: EvDrop, Code: uint32(DragFiles)}, []byte("/a\x00/b\x00"), 2)
	q.pushDropData(Event{Kind: EvDrop}, nil, 0)
	q.pushDropData(Event{Kind: EvDrop}, make([]byte, MaxPaste+1), 0)
	ev := q.ev
	if len(ev) != 4 {
		t.Fatalf("%d events", len(ev))
	}
	if ev[0].Flags&FlagFailed != 0 || string(q.textOf(ev[0].Text)) != "abc" {
		t.Errorf("text drop: %+v", ev[0])
	}
	if ev[1].SelStart != 2 || string(q.textOf(ev[1].Text)) != "/a\x00/b\x00" {
		t.Errorf("file drop: %+v", ev[1])
	}
	if ev[2].Flags&FlagFailed == 0 || ev[3].Flags&FlagFailed == 0 {
		t.Error("an empty or oversized drop is failed")
	}
}

func TestDndActionFor(t *testing.T) {
	for _, c := range []struct {
		ops  DragOp
		mods Mods
		want DragOp
	}{
		{DragOpAll, 0, DragOpCopy},
		{DragOpMove | DragOpLink, 0, DragOpLink},
		{DragOpAll, ModShift, DragOpMove},
		{DragOpAll, ModCtrl, DragOpCopy},
		{DragOpAll, ModShift | ModCtrl, DragOpLink},
		{DragOpCopy, ModShift, DragOpCopy}, // a modifier for what the source does not allow
		{DragOpMove, ModCtrl, DragOpMove},
	} {
		if got := dndActionFor(c.ops, c.mods); got != c.want {
			t.Errorf("ops %d mods %d: %d, want %d", c.ops, c.mods, got, c.want)
		}
	}
}

func TestGTKSettingInt(t *testing.T) {
	ini := "# a comment\n[Other]\ngtk-dnd-drag-threshold=99\n[Settings]\ngtk-theme-name=Adwaita\n gtk-dnd-drag-threshold = 12 \n"
	if n, ok := gtkSettingInt(ini, "gtk-dnd-drag-threshold"); !ok || n != 12 {
		t.Errorf("got %d, %v; want 12 (the [Settings] group's)", n, ok)
	}
	for _, bad := range []string{"", "[Settings]\ngtk-dnd-drag-threshold=lots\n", "gtk-dnd-drag-threshold=5\n", "[Settings]\nother=5\n"} {
		if n, ok := gtkSettingInt(bad, "gtk-dnd-drag-threshold"); ok {
			t.Errorf("%q read as %d", bad, n)
		}
	}
}

func TestHTMLToText(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain", "plain"},
		{"<b>bold</b> and <i>italic</i>", "bold and italic"},
		{"<p>one</p><p>two &amp; three</p>", "one\ntwo & three"},
		{"a<br>b<br/>c", "a\nb\nc"},
		{"<html><head><title>t</title><style>p{x:y}</style></head><body><script>var a = '<b>';</script>text&nbsp;here</body></html>", "text here"},
		{"  spaced \n\t out  ", "spaced out"},
		{"<ul><li>x</li><li>y</li></ul>", "x\ny"},
		{"é &eacute; &#x263A;", "é é ☺"},
		{"unclosed <b", "unclosed"},
		{"", ""},
	} {
		if got := string(htmlToText([]byte(c.in))); got != c.want {
			t.Errorf("htmlToText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
