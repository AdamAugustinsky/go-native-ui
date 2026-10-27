package markdown_test

import (
	"fmt"

	"gonativeui/markdown"
)

// Example parses a message and walks its blocks: each has a body and spans
// for ui.RichText.
func Example() {
	var p markdown.Parser
	doc := p.Parse([]byte("Hello, world."))
	for i := range doc.Blocks {
		b := &doc.Blocks[i]
		fmt.Printf("%d %q %d span(s)\n", b.Kind, doc.Body(b), len(doc.SpansOf(b)))
	}
	// Output: 1 "Hello, world." 1 span(s)
}
