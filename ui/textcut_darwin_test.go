//go:build darwin

package ui

import (
	"bytes"
	"runtime"
	"testing"
	"unicode/utf8"

	"gonativeui/gpu"
	"gonativeui/text"
)

// With CoreText: a cut of mixed-direction and contextually shaped text keeps
// valid text in logical order with the font's "…" where the cut is, the kept
// line fits the box, a file name keeps its extension, and a warm frame shapes
// nothing again and allocates nothing.
func TestRealTextCut(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ts, err := text.New()
	if err != nil {
		t.Skip("no text system:", err)
	}
	defer ts.Close()
	font := ts.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: 2})
	c := New(ts)
	c.AtlasSync(&fakeUploader{})
	var list gpu.DrawList
	cases := []struct {
		s    string
		cut  TextCut
		want func(kept []byte) bool
	}{
		{"Report מסמך חשוב מאוד final draft.pdf", CutTail, func(k []byte) bool { return bytes.HasSuffix(k, ellipsisText) }},
		{"Report מסמך חשוב מאוד final draft.pdf", CutMiddle, func(k []byte) bool { return bytes.HasSuffix(k, []byte(".pdf")) && bytes.Contains(k, ellipsisText) }},
		{"مرحبا بالعالم الجميل في هذا اليوم", CutTail, func(k []byte) bool { return bytes.HasSuffix(k, ellipsisText) }},
		{"/Users/someone/Documents/projects/notes.txt", CutHead, func(k []byte) bool { return bytes.HasPrefix(k, ellipsisText) }},
	}
	ids := make([]ID, len(cases))
	frame := func() {
		c.Begin(&Input{}, 800, 400, 2, 1.0/60)
		c.PushFont(font)
		for i, tc := range cases {
			c.NextSize(Px(240), Px(40))
			c.NextTextCut(tc.cut)
			ids[i] = c.BoxKeyed(DrawText, KeyN(0, uint64(i+1)), []byte(tc.s))
		}
		c.PopFont()
		c.End(&list)
	}
	frame()
	frame()
	for i, tc := range cases {
		b := &c.boxes[ids[i]]
		if !c.Truncated(ids[i]) || b.cut == 0 {
			t.Fatalf("%q: not cut", tc.s)
		}
		cut := c.cuts[b.cut-1]
		kv := ts.Run(cut.kept)
		if !cut.ok || !utf8.Valid(kv.Text) || !tc.want(kv.Text) {
			t.Errorf("%q (%d): kept %q", tc.s, tc.cut, kv.Text)
		}
		if kv.Advance > 240.01 {
			t.Errorf("%q: kept line %v px in a 240 px box", tc.s, kv.Advance)
		}
		t.Logf("%q → %q (%.1f px)", tc.s, kv.Text, kv.Advance)
	}
	if a := testing.AllocsPerRun(20, frame); a != 0 {
		t.Errorf("%v allocations a warm frame", a)
	}
}
