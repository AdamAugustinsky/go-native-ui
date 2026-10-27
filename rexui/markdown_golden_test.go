package rexui_test

import (
	"image"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/highlight"
	"gonativeui/markdown"
	"gonativeui/rexui"
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
	view     rexui.MarkdownView
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
	rexui.Markdown(k, ui.HashString(0, "msg"), &s.doc, rexui.MarkdownOpts{View: &s.view})
	key := ui.HashString(0, "diff")
	toks := s.view.Tokens(c, key, highlight.Go, s.diff)
	sig := rexui.CodeBlock(k, key, s.diff, rexui.CodeOpts{
		Lang: highlight.Go, Tokens: toks, Label: "layout.go", Gutter: true, FirstLine: 40, Hit: 43, Diff: true, View: &s.view,
	})
	s.codeRect = c.Rect(sig.ID)
	c.Pop()
	k.End()
}

var _ = widgettest.DefineUpdateFlag()

// The offscreen goldens of Markdown and CodeBlock, light and dark: the whole
// scene, and the diff block on its own (testdata/markdown-*.png,
// codeblock-diff-*.png; -update rewrites them).
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
