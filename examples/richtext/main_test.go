package main

import (
	"runtime"
	"strings"
	"testing"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/script"
	"gonativeui/widget/widgettest"
)

var cfg = app.Config{Title: "Rich text", Width: 680, Height: 300, Clear: [4]float32{1, 1, 1, 1}}

func open(t *testing.T) (*doc, *widgettest.Rig) {
	t.Helper()
	d := newDoc()
	r := widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, d)
	r.Frames(4)
	return d, r
}

// The link and the mention are hit regions and links of the semantic tree, and
// a click on one reports its tag; nothing is opened.
func TestLinkAndMentionReportClicks(t *testing.T) {
	d, r := open(t)
	for _, name := range []string{"@ana", "example.com/pings"} {
		if _, ok := r.Find(ui.RoleLink, name); !ok {
			t.Fatalf("no link named %q in the tree", name)
		}
	}
	r.Click(ui.RoleLink, "example.com/pings")
	r.Frames(2)
	if !strings.HasPrefix(d.line, "clicked the link") {
		t.Errorf("after a click on the link the line is %q", d.line)
	}
	r.Click(ui.RoleLink, "@ana")
	r.Frames(2)
	if !strings.HasPrefix(d.line, "clicked the mention") {
		t.Errorf("after a click on the mention the line is %q", d.line)
	}
}

// Dragging from the first paragraph to the second selects across them, and
// copying puts both parts on the clipboard as one text.
func TestSelectionAcrossParagraphsCopies(t *testing.T) {
	d, r := open(t)
	var clip []byte
	r.Context().ClipboardSet = func(b []byte) { clip = append(clip[:0], b...) }
	r.Click(ui.RoleLabel, "Press and drag from here") // a press in a text focuses the scope
	r.Play(script.Edit(ui.EditSelectAll), script.Edit(ui.EditCopy))
	r.Frames(2)
	got := string(clip)
	if !strings.Contains(got, "Press and drag from here") || !strings.Contains(got, "to here, and the text between copies as one.") {
		t.Errorf("the clipboard holds %q (the line is %q)", got, d.line)
	}
}

func TestLayoutAndNames(t *testing.T) {
	_, r := open(t)
	r.AuditLayout("richtext")
	widgettest.LintNames(t, "richtext", r.Tree())
}

func TestSteadyFramesAllocateNothing(t *testing.T) {
	o := widgettest.NewOffscreen(t, cfg, int(cfg.Width), int(cfg.Height), 1, newDoc())
	for range 300 {
		o.Frame()
	}
	runtime.GC()
	for range 300 {
		o.Frame()
	}
	var best uint64
	for w := 0; w < 12; w++ {
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for range 200 {
			o.Frame()
		}
		runtime.ReadMemStats(&m1)
		if m1.Mallocs == m0.Mallocs && m1.TotalAlloc == m0.TotalAlloc {
			return
		}
		if w == 0 || m1.Mallocs-m0.Mallocs < best {
			best = m1.Mallocs - m0.Mallocs
		}
	}
	t.Errorf("every one of 12 runs of 200 frames allocated (fewest: %d mallocs)", best)
}
