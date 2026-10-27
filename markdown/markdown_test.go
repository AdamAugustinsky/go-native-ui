package markdown_test

import (
	"testing"

	"gonativeui/markdown"
)

// TestFallback is wave 0's documented fallback: the source as one plain
// paragraph, and storage reused across parses.
func TestFallback(t *testing.T) {
	var p markdown.Parser
	d := p.Parse([]byte("**hi** there"))
	if len(d.Blocks) != 1 || d.Blocks[0].Kind != markdown.Paragraph || string(d.Body(&d.Blocks[0])) != "**hi** there" {
		t.Fatalf("Parse: %+v %q", d.Blocks, d.Text)
	}
	if s := d.SpansOf(&d.Blocks[0]); len(s) != 1 || s[0].Style != 0 || s[0].Len != 12 {
		t.Fatalf("spans: %+v", s)
	}
	d = p.Append([]byte("\n\nmore"))
	if string(d.Body(&d.Blocks[0])) != "**hi** there\n\nmore" {
		t.Fatalf("Append: %q", d.Text)
	}
	if n := testing.AllocsPerRun(10, func() { p.Parse([]byte("**hi** there\n\nmore")) }); n != 0 {
		t.Fatalf("a reparse of the same size allocates %v", n)
	}
	p.Reset()
	if d := p.Append(nil); len(d.Blocks) != 0 {
		t.Fatal("Reset kept blocks")
	}
	if markdown.NumStyles != int(markdown.Emph|markdown.Strong|markdown.CodeSpan|markdown.Strike)+1 {
		t.Fatal("NumStyles does not cover the style bits")
	}
}
