package markdown_test

import (
	"fmt"

	"gonativeui/markdown"
)

// Example parses a message and walks its blocks: each has a body and spans
// for ui.RichText.
func Example() {
	var p markdown.Parser
	doc := p.Parse([]byte("# Hello\n\nSome **bold** text.\n\n- one\n- two"))
	for i := range doc.Blocks {
		b := &doc.Blocks[i]
		fmt.Printf("%d depth %d %q %d span(s)\n", b.Kind, b.Depth, doc.Body(b), len(doc.SpansOf(b)))
	}
	// Output:
	// 2 depth 0 "Hello" 0 span(s)
	// 1 depth 0 "Some bold text." 1 span(s)
	// 4 depth 1 "one" 0 span(s)
	// 4 depth 1 "two" 0 span(s)
}
