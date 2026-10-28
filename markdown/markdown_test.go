package markdown_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gonativeui/markdown"
	"gonativeui/ui"
)

var update = flag.Bool("update", false, "rewrite the golden block files in testdata")

var kindNames = [...]string{"?", "para", "heading", "code", "item", "cell", "rule"}

// dump writes a document one block per line, then its spans, one per line:
// what the conversion made of goldmark's tree.
func dump(d *markdown.Doc) string {
	var b strings.Builder
	for i := range d.Blocks {
		bl := &d.Blocks[i]
		fmt.Fprintf(&b, "%s", kindNames[bl.Kind])
		if bl.Level > 0 {
			fmt.Fprintf(&b, " h%d", bl.Level)
		}
		if bl.Depth > 0 {
			fmt.Fprintf(&b, " depth=%d", bl.Depth)
		}
		if bl.Quote > 0 {
			fmt.Fprintf(&b, " quote=%d", bl.Quote)
		}
		if bl.Flags&markdown.Ordered != 0 {
			fmt.Fprintf(&b, " #%d", bl.Number)
		}
		for _, f := range []struct {
			bit  markdown.BlockFlags
			name string
		}{{markdown.Task, "task"}, {markdown.Checked, "checked"}, {markdown.Loose, "loose"}, {markdown.Open, "open"}} {
			if bl.Flags&f.bit != 0 {
				fmt.Fprintf(&b, " %s", f.name)
			}
		}
		if bl.Kind == markdown.TableCell {
			fmt.Fprintf(&b, " r%dc%d align=%d", bl.Row, bl.Col, d.AlignOf(bl))
		}
		if bl.Kind == markdown.Code {
			fmt.Fprintf(&b, " info=%q", d.Info(bl))
		}
		body := d.Body(bl)
		fmt.Fprintf(&b, " %q\n", body)
		for _, s := range d.SpansOf(bl) {
			fmt.Fprintf(&b, "\t%q style=%d", body[s.Off:s.Off+s.Len], s.Style)
			if s.Flags&ui.SpanLink != 0 {
				fmt.Fprintf(&b, " link=%q", d.LinkDest(s.Tag))
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// The fixtures, agent-transcript-like messages, convert to the blocks and
// spans of testdata/<name>.blocks (-update rewrites them; read the diff).
func TestFixtures(t *testing.T) {
	for _, name := range []string{"plan", "tasks", "table", "code", "html"} {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("testdata", name+".md"))
			if err != nil {
				t.Fatal(err)
			}
			var p markdown.Parser
			got := dump(p.Parse(src))
			path := filepath.Join("testdata", name+".blocks")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("%s converts to\n%s\nwant (%s)\n%s", name, got, path, want)
			}
		})
	}
}

// find is the first block of kind k whose body holds s.
func find(t *testing.T, d *markdown.Doc, k markdown.BlockKind, s string) *markdown.Block {
	t.Helper()
	for i := range d.Blocks {
		if b := &d.Blocks[i]; b.Kind == k && bytes.Contains(d.Body(b), []byte(s)) {
			return b
		}
	}
	t.Fatalf("no %s block holding %q in\n%s", kindNames[k], s, dump(d))
	return nil
}

// span is the span of b covering the text s, and whether there is one.
func span(d *markdown.Doc, b *markdown.Block, s string) (ui.Span, bool) {
	body := d.Body(b)
	i := bytes.Index(body, []byte(s))
	for _, sp := range d.SpansOf(b) {
		if int(sp.Off) == i && int(sp.Len) == len(s) {
			return sp, true
		}
	}
	return ui.Span{}, false
}

// What a reader of the fixtures expects, stated outright so that a golden
// rewritten in error is caught: markers removed, styles as bits, links as
// link spans, nesting as depth, tables as cells.
func TestConversion(t *testing.T) {
	var p markdown.Parser
	read := func(name string) *markdown.Doc {
		src, err := os.ReadFile(filepath.Join("testdata", name+".md"))
		if err != nil {
			t.Fatal(err)
		}
		p.Reset()
		return p.Parse(src)
	}

	d := read("plan")
	if h := find(t, d, markdown.Heading, "Plan"); h.Level != 2 || string(d.Body(h)) != "Plan" {
		t.Errorf("heading %+v %q", h, d.Body(h))
	}
	para := find(t, d, markdown.Paragraph, "failing test")
	for s, want := range map[string]uint16{"failing test": markdown.Strong, "ui/richtext.go": markdown.CodeSpan, "check": markdown.Emph} {
		if sp, ok := span(d, para, s); !ok || sp.Style != want {
			t.Errorf("%q: span %+v %v, want style %d", s, sp, ok, want)
		}
	}
	if it := find(t, d, markdown.ListItem, "Read the test"); it.Depth != 1 || it.Flags&markdown.Ordered == 0 || it.Number != 1 {
		t.Errorf("first item %+v", it)
	}
	if it := find(t, d, markdown.ListItem, "keep the cache"); it.Depth != 2 || it.Flags&markdown.Ordered != 0 {
		t.Errorf("nested item %+v", it)
	}
	if sp, ok := span(d, find(t, d, markdown.ListItem, "drop"), "the old path"); !ok || sp.Style != markdown.Strike {
		t.Errorf("strike %+v", sp)
	}
	links := find(t, d, markdown.Paragraph, "See ")
	if sp, ok := span(d, links, "https://github.com/yuin/goldmark"); !ok || sp.Flags&ui.SpanLink == 0 || string(d.LinkDest(sp.Tag)) != "https://github.com/yuin/goldmark" {
		t.Errorf("linkify: %+v %v", sp, ok)
	}
	if sp, ok := span(d, links, "the guide"); !ok || sp.Style != markdown.LinkStyle || string(d.LinkDest(sp.Tag)) != "docs/guide/richtext.md" {
		t.Errorf("link: %+v %v", sp, ok)
	}

	d = read("tasks")
	if it := find(t, d, markdown.ListItem, "parse with goldmark"); it.Flags&(markdown.Task|markdown.Checked) != markdown.Task|markdown.Checked || string(d.Body(it)) != "parse with goldmark" {
		t.Errorf("checked task %+v %q", it, d.Body(it))
	}
	if it := find(t, d, markdown.ListItem, "nested task"); it.Flags&markdown.Task == 0 || it.Flags&markdown.Checked != 0 || it.Depth != 2 {
		t.Errorf("nested task %+v", it)
	}
	if q := find(t, d, markdown.Paragraph, "nested quote"); q.Quote != 2 {
		t.Errorf("nested quote %+v", q)
	}
	if q := find(t, d, markdown.Paragraph, "the quote"); q.Quote != 1 || string(d.Body(q)) != "Note: the quote spans two lines." {
		t.Errorf("quote %+v %q", q, d.Body(q))
	}

	d = read("table")
	var cells int
	for i := range d.Blocks {
		if d.Blocks[i].Kind == markdown.TableCell {
			cells++
		}
	}
	if cells != 9 {
		t.Errorf("%d cells, want 9", cells)
	}
	if c := find(t, d, markdown.TableCell, "Lines"); c.Row != 0 || c.Col != 1 || d.AlignOf(c) != markdown.AlignRight {
		t.Errorf("header cell %+v align %d", c, d.AlignOf(c))
	}
	if c := find(t, d, markdown.TableCell, "1,204"); c.Row != 2 || c.Col != 1 {
		t.Errorf("cell %+v", c)
	}
	if c := find(t, d, markdown.TableCell, "ok"); d.AlignOf(c) != markdown.AlignCenter {
		t.Errorf("centre %d", d.AlignOf(c))
	}
	find(t, d, markdown.ThematicBreak, "")

	d = read("code")
	c := find(t, d, markdown.Code, "func main")
	if string(d.Info(c)) != "go" || string(d.Body(c)) != "func main() {\n\tfmt.Println(\"hi <b>\")\n}" {
		t.Errorf("fenced code %q %q", d.Info(c), d.Body(c))
	}
	if c := find(t, d, markdown.Code, "indented"); string(d.Body(c)) != "indented code\nblock" || len(d.Info(c)) != 0 {
		t.Errorf("indented code %q", d.Body(c))
	}

	d = read("html")
	if b := find(t, d, markdown.Paragraph, "<div"); !strings.Contains(string(d.Body(b)), "raw *html* block\n</div>") {
		t.Errorf("an HTML block is text: %q", d.Body(b))
	}
	b := find(t, d, markdown.Paragraph, "Inline")
	if !strings.Contains(string(d.Body(b)), "<span>tag</span> & entity © and *escaped* text, a hard\nbreak") {
		t.Errorf("inline HTML, entities, escapes and hard break: %q", d.Body(b))
	}
	if sp, ok := span(d, b, "me@example.com"); !ok || string(d.LinkDest(sp.Tag)) != "mailto:me@example.com" {
		t.Errorf("email autolink %+v", sp)
	}
	if sp, ok := span(d, b, "www.example.com"); !ok || sp.Flags&ui.SpanLink == 0 {
		t.Errorf("bare www link %+v", sp)
	}
	img := find(t, d, markdown.Paragraph, "alt text")
	if sp, ok := span(d, img, "alt text"); !ok || string(d.LinkDest(sp.Tag)) != "img.png" {
		t.Errorf("image %+v", sp)
	}
	if h := find(t, d, markdown.Heading, "Setext"); h.Level != 1 {
		t.Errorf("setext %+v", h)
	}
	if it := find(t, d, markdown.ListItem, "eight"); it.Number != 8 {
		t.Errorf("start number %+v", it)
	}
	if it := find(t, d, markdown.ListItem, "nine"); it.Number != 9 {
		t.Errorf("second number %+v", it)
	}
}

// A copy holds the document after the parser has moved on; once warm,
// copying allocates nothing.
func TestCopyTo(t *testing.T) {
	var p markdown.Parser
	var cp markdown.Doc
	p.Parse([]byte("# One\n\n[a](b)")).CopyTo(&cp)
	want := dump(&cp)
	p.Parse([]byte("something else entirely"))
	if dump(&cp) != want {
		t.Fatal("the copy changed with the parser")
	}
	d := p.Parse([]byte("# One\n\n[a](b)"))
	if n := testing.AllocsPerRun(20, func() { d.CopyTo(&cp) }); n != 0 {
		t.Fatalf("CopyTo allocates %v", n)
	}
}

// transcript is a long agent reply: headings, paragraphs, lists, code and a
// table, repeated to about n bytes.
func transcript(n int) []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "## Step %d\n\nI read `file%d.go` and found **%d** issues; see [the log](https://example.com/%d).\n\n", i, i, i, i)
		fmt.Fprintf(&b, "- first point about step %d\n- second, with *emphasis*\n  - nested detail\n\n", i)
		fmt.Fprintf(&b, "```go\nfunc step%d() error {\n\treturn nil\n}\n```\n\n", i)
		if i%3 == 0 {
			fmt.Fprintf(&b, "| a | b |\n|---|---|\n| %d | x |\n\n", i)
		}
		fmt.Fprintf(&b, "> quoted remark %d\n\n", i)
	}
	return b.Bytes()
}

// A 20 KB message streamed in 50-byte chunks parses again only its tail:
// after every chunk the document equals a fresh parse of the source so far,
// and the bytes parsed in all stay a small multiple of the message, where
// parsing from the start each time would be about 200 times it.
func TestStreaming(t *testing.T) {
	msg := transcript(20 << 10)
	var s, whole markdown.Parser
	total, most := 0, 0
	for i := 0; i < len(msg); i += 50 {
		d := s.Append(msg[i:min(i+50, len(msg))])
		total += s.Reparsed()
		most = max(most, s.Reparsed())
		if got, want := dump(d), dump(whole.Parse(msg[:min(i+50, len(msg))])); got != want {
			t.Fatalf("after %d bytes the streamed document differs from a fresh parse:\n%s\nwant\n%s", min(i+50, len(msg)), got, want)
		}
	}
	quadratic := len(msg) * (len(msg) / 50) / 2
	t.Logf("%d bytes in %d chunks: parsed %d bytes in all (%.1f× the message; from the start each time: %d), at most %d at once",
		len(msg), (len(msg)+49)/50, total, float64(total)/float64(len(msg)), quadratic, most)
	if total > 3*len(msg) {
		t.Errorf("parsed %d bytes for a %d-byte message: not only the tail", total, len(msg))
	}
	if most > 400 {
		t.Errorf("one append parsed %d bytes; the longest block of the transcript is under 350", most)
	}
}

// A link reference definition can change a link before it, so a document
// that has one is parsed from the start each time; it stays right.
func TestStreamingReferences(t *testing.T) {
	msg := []byte("See [the docs][d].\n\nMore text here.\n\n[d]: https://example.com/docs\n\nAfter.\n")
	var s, whole markdown.Parser
	for i := 0; i < len(msg); i += 7 {
		d := s.Append(msg[i:min(i+7, len(msg))])
		if got, want := dump(d), dump(whole.Parse(msg[:min(i+7, len(msg))])); got != want {
			t.Fatalf("after %d bytes:\n%s\nwant\n%s", min(i+7, len(msg)), got, want)
		}
	}
	if s.Reparsed() != len(msg) {
		t.Errorf("the last append parsed %d of %d bytes", s.Reparsed(), len(msg))
	}
}

// Parsing a finished message: the conversion's own share allocates little
// beyond goldmark's tree, and the parser's storage is reused.
func TestReset(t *testing.T) {
	var p markdown.Parser
	p.Parse([]byte("**hi** there"))
	p.Reset()
	if d := p.Append(nil); len(d.Blocks) != 0 || len(p.Source()) != 0 {
		t.Fatal("Reset kept blocks")
	}
	if markdown.NumStyles != int(markdown.Emph|markdown.Strong|markdown.CodeSpan|markdown.Strike|markdown.LinkStyle)+1 || markdown.NumStyles > ui.MaxRichStyles {
		t.Fatal("NumStyles does not cover the style bits, or does not fit a RichStyles table")
	}
}

// BenchmarkParse is the cost of parsing a 20 KB transcript (on a worker).
func BenchmarkParse(b *testing.B) {
	msg := transcript(20 << 10)
	var p markdown.Parser
	b.SetBytes(int64(len(msg)))
	b.ReportAllocs()
	for b.Loop() {
		p.Parse(msg)
	}
}
