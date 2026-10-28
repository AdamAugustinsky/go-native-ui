package mdview_test

import (
	"bytes"
	"fmt"
	"image"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/highlight"
	"gonativeui/markdown"
	"gonativeui/rexui"
	"gonativeui/rexui/mdview"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"
)

const goldenReply = "## Fixing the layout cache\n\n" +
	"I changed `ui/richtext.go` so the cache is **per window**; see [the PR](https://example.com/pr/1) and ~~the old plan~~.\n\n" +
	"1. Read the failing test\n2. Patch `layout()`\n   - keep the cache\n   - drop the global map\n\n" +
	"- [x] parse with goldmark\n- [ ] render the spans\n\n" +
	"> The cache is per window, so two windows never share a layout.\n\n" +
	"| File | Lines | Status |\n|:--|--:|:-:|\n| `richtext.go` | 1,204 | *changed* |\n| `layout_test.go` | 88 | new |\n\n" +
	"```go\n// Layout lays the paragraph out once per width.\nfunc (c *Cache) Layout(p *Para, w float32) (Ref, error) {\n\tif r, ok := c.m[key{p.ID, w}]; ok {\n\t\treturn r, nil\n\t}\n\treturn c.lay(p, w), nil\n}\n```\n\n" +
	"---\n\nDone: all tests pass."

const goldenDiff = "@@ -40,7 +40,8 @@ func (c *Cache) Layout(p *Para, w float32) (Ref, error) {\n" +
	" \tif r, ok := c.m[key{p.ID, w}]; ok {\n" +
	" \t\treturn r, nil\n" +
	" \t}\n" +
	"-\tr := layoutGlobal(p, w)\n" +
	"+\tr := c.lay(p, w)\n" +
	"+\tc.m[key{p.ID, w}] = r\n" +
	" \treturn r, nil\n" +
	" }"

// goldenScene is a window with a Markdown message and a diff in a code
// block with a gutter and a highlighted line, in the Rex look.
type goldenScene struct {
	k        *base.Kit
	mode     theme.Mode
	view     mdview.MarkdownView
	doc      markdown.Doc
	diff     []byte
	codeRect [4]float32
}

func (s *goldenScene) Init(a *app.App) error {
	s.k = base.NewKit(a.UI, a.Text, rex.Theme())
	s.k.SetMode(s.mode)
	s.k.ReduceMotion = true
	a.UI.Reserve(4096)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	var p markdown.Parser
	p.Parse([]byte(goldenReply)).CopyTo(&s.doc)
	s.diff = []byte(goldenDiff)
	return nil
}

func (s *goldenScene) Build(c *ui.Context) {
	k := s.k
	k.Begin(c)
	r := k.Theme()
	w, h := c.Size()
	c.NextSize(ui.Px(float32(w)), ui.Px(float32(h)))
	c.NextBG(r.Colors[theme.Window])
	c.NextAxis(ui.AxisY)
	c.NextGap(k.Pt(16))
	c.NextPad(k.Pt(20))
	c.Push(c.Box(ui.DrawBackground, "window"))
	mdview.Markdown(k, ui.HashString(0, "msg"), &s.doc, mdview.MarkdownOpts{View: &s.view})
	key := ui.HashString(0, "diff")
	toks := s.view.Tokens(c, key, highlight.Go, s.diff)
	sig := mdview.CodeBlock(k, key, s.diff, mdview.CodeOpts{
		Lang: highlight.Go, Tokens: toks, Label: "layout.go", Gutter: true, FirstLine: 40, Hit: 43, Diff: true, View: &s.view,
	})
	s.codeRect = c.Rect(sig.ID)
	c.Pop()
	k.End()
}

var _ = widgettest.DefineUpdateFlag()

// The offscreen goldens of Markdown and CodeBlock, light and dark: the whole
// scene, and the diff block on its own (testdata/markdown-*.png,
// codeblock-diff-*.png; -update rewrites them), with a clean layout audit.
func TestMarkdownGolden(t *testing.T) {
	for _, m := range []struct {
		name string
		mode theme.Mode
	}{{"light", theme.Light}, {"dark", theme.Dark}} {
		t.Run(m.name, func(t *testing.T) {
			s := &goldenScene{mode: m.mode}
			cfg := app.Config{Title: "Markdown", Width: 640, Height: 740}
			r := widgettest.Open(t, cfg, 640*2, 740*2, 2, s)
			for deadline := time.Now().Add(20 * time.Second); ; {
				r.Frames(3)
				if s.view.Pending() == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("highlighting never finished")
				}
				time.Sleep(5 * time.Millisecond)
			}
			r.Frames(4)
			r.AssertImage("markdown-"+m.name, widgettest.Tolerance{})
			cr := s.codeRect
			r.AssertImageRect("codeblock-diff-"+m.name, image.Rect(int(cr[0]), int(cr[1]), int(cr[2]), int(cr[3])), widgettest.Tolerance{})
		})
	}
}

// codeListScene is a file's diff as a VList of CodeLine rows, its lines
// longer than the window: ContentW (CodeWidth of the longest line) makes the
// list scroll sideways, every row by one offset.
type codeListScene struct {
	k     *base.Kit
	mode  theme.Mode
	view  mdview.MarkdownView
	st    base.VListState
	lines [][]byte
	toks  [][]highlight.Token
	cw    float32
	sideX float64 // a sideways offset to set once (0: none)
	// headers makes every twelfth line a file's header (ItemFixedX), its
	// name and counts staying in view while the lines scroll sideways
	headers bool
	heads   [][4]float32
	rows    [][4]float32
	outer   [4]float32
	scroll  float64
}

func (s *codeListScene) Init(a *app.App) error {
	s.k = base.NewKit(a.UI, a.Text, rex.Theme())
	s.k.SetMode(s.mode)
	s.k.ReduceMotion = true
	a.UI.Reserve(4096)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	var src []byte
	for i := range 40 {
		switch i % 5 {
		case 1:
			src = fmt.Appendf(src, "-\tr := layoutGlobal(p, w) // line %d: the old global cache, shared by every window of the app\n", i)
		case 2:
			src = fmt.Appendf(src, "+\tr := c.lay(p, w) // line %d: the cache is per window now, so two windows never share a layout\n", i)
		default:
			src = fmt.Appendf(src, " \treturn r, nil // %d\n", i)
		}
	}
	src = bytes.TrimSuffix(src, []byte("\n"))
	toks := highlight.Tokens(highlight.Diff, src, nil)
	for at := 0; at <= len(src); {
		n := bytes.IndexByte(src[at:], '\n')
		if n < 0 {
			n = len(src) - at
		}
		var lt []highlight.Token
		for _, t := range toks {
			if int(t.Off) >= at && int(t.Off+t.Len) <= at+n {
				t.Off -= uint32(at)
				lt = append(lt, t)
			}
		}
		s.lines, s.toks = append(s.lines, src[at:at+n]), append(s.toks, lt)
		at += n + 1
	}
	ids := make([]uint64, len(s.lines))
	for i := range ids {
		ids[i] = uint64(i)
	}
	s.st.Append(ids)
	s.st.ScrollToID(6, base.AlignStart)
	return nil
}

func (s *codeListScene) Build(c *ui.Context) {
	k := s.k
	k.Begin(c)
	r := k.Theme()
	w, h := c.Size()
	c.NextSize(ui.Px(float32(w)), ui.Px(float32(h)))
	c.NextBG(r.Colors[theme.Pane])
	c.Push(c.Box(ui.DrawBackground, "window"))
	o := mdview.CodeLineOpts{GutterDigits: 2, View: &s.view}
	if s.cw == 0 {
		cols := 0
		for _, l := range s.lines {
			cols = max(cols, mdview.CodeColumns(l))
		}
		s.cw = mdview.CodeWidth(k, cols, o)
	}
	v := base.BeginVList(c, "file", &s.st, base.VListOpts{Estimate: 18, ContentW: s.cw})
	if s.sideX > 0 {
		_, y := c.Scroll(v.Body)
		c.SetScroll(v.Body, s.sideX, y)
		s.sideX = 0
	}
	s.rows, s.heads = s.rows[:0], s.heads[:0]
	for i := v.First; i < v.Last; i++ {
		if s.headers && i%12 == 0 {
			v.ItemFixedX(i)
			fileHeader(k, i)
			s.heads = append(s.heads, c.Rect(c.Parent()))
			continue
		}
		n := v.Item(i)
		o.Number, o.Kind, o.Item, o.Part = int(n)+1, mdview.DiffKind(s.lines[n]), n, 0
		mdview.CodeLine(k, ui.KeyN(c.ParentKey(), 1), s.lines[n], s.toks[n], o)
		s.rows = append(s.rows, c.Rect(c.Parent()))
	}
	base.EndVList(&v)
	s.outer = c.Rect(v.Outer)
	s.scroll, _ = c.Scroll(v.Body)
	c.Pop()
	k.End()
}

// fileHeader is a diff's file header row: the file's name at the left and
// its counts at the right.
func fileHeader(k *base.Kit, i int) {
	c, r := k.Context(), k.Theme()
	c.NextBG(r.Colors[theme.Fill5])
	k.NextPadding(12, 4, 12, 4)
	k.NextGap(8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "head", ui.Pct(1), ui.Children(1), ui.DrawBackground)
	name := "ui/richtext.go"
	if i%24 != 0 {
		name = "ui/layout_test.go"
	}
	rexui.Label(k, name, rexui.LabelOpts{Style: rexui.TextHeadline})
	k.Spacer()
	rexui.Label(k, "+8", rexui.LabelOpts{MonoPt: 11, Color: rexui.SuccessText})
	rexui.Label(k, "−8", rexui.LabelOpts{MonoPt: 11, Color: rexui.DangerText})
	k.EndContainer()
}

// A file's lines as CodeLine rows in a VList with ContentW: scrolled 160 px
// sideways, every row starts 160 px left of the list and is as wide as the
// longest line, its band included (testdata/codelines-*.png), and the
// layout audit finds nothing clipped that it should not.
func TestCodeLinesScrollSidewaysGolden(t *testing.T) {
	for _, m := range []struct {
		name string
		mode theme.Mode
	}{{"light", theme.Light}, {"dark", theme.Dark}} {
		t.Run(m.name, func(t *testing.T) {
			s := &codeListScene{mode: m.mode}
			r := widgettest.Open(t, app.Config{Title: "Code lines", Width: 420, Height: 220}, 420*2, 220*2, 2, s)
			r.Frames(4)
			s.sideX = 160
			r.Frames(4)
			if s.scroll != 160 {
				t.Fatalf("the list is %v px sideways, want 160", s.scroll)
			}
			for i, row := range s.rows {
				if row[0] != s.outer[0]-160 || row[2]-row[0] < s.cw {
					t.Fatalf("row %d spans x %v..%v, want from %v and %v wide", i, row[0], row[2], s.outer[0]-160, s.cw)
				}
			}
			r.AuditLayout("codelines-" + m.name)
			r.AssertImage("codelines-"+m.name, widgettest.Tolerance{})
		})
	}
}

// A diff with file headers built with ItemFixedX: scrolled 160 px sideways,
// the lines start 160 px left of the list while each header stays at its
// left edge, as wide as the view, its counts in view at its right
// (testdata/codelines-headers-*.png).
func TestCodeLinesFixedHeadersGolden(t *testing.T) {
	for _, m := range []struct {
		name string
		mode theme.Mode
	}{{"light", theme.Light}, {"dark", theme.Dark}} {
		t.Run(m.name, func(t *testing.T) {
			s := &codeListScene{mode: m.mode, headers: true}
			r := widgettest.Open(t, app.Config{Title: "Code lines", Width: 420, Height: 220}, 420*2, 220*2, 2, s)
			r.Frames(4)
			s.sideX = 160
			r.Frames(4)
			if s.scroll != 160 {
				t.Fatalf("the list is %v px sideways, want 160", s.scroll)
			}
			if len(s.heads) == 0 {
				t.Fatal("no header was built")
			}
			for i, h := range s.heads {
				if h[0] != s.outer[0] || h[2] != s.outer[2] {
					t.Fatalf("header %d spans x %v..%v, want the view's %v..%v", i, h[0], h[2], s.outer[0], s.outer[2])
				}
			}
			for i, row := range s.rows {
				if row[0] != s.outer[0]-160 {
					t.Fatalf("row %d starts at x %v, want %v", i, row[0], s.outer[0]-160)
				}
			}
			r.AuditLayout("codelines-headers-" + m.name)
			r.AssertImage("codelines-headers-"+m.name, widgettest.Tolerance{})
		})
	}
}
