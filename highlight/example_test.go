package highlight_test

import (
	"bytes"
	"fmt"

	"gonativeui/highlight"
)

// Example highlights a code block line by line, carrying the state across
// lines.
func Example() {
	src := []byte("package main\n\nfunc main() {}\n")
	lang := highlight.LangOf("go")
	var st highlight.State
	var toks []highlight.Token
	n := 0
	for _, line := range bytes.Split(bytes.TrimSuffix(src, []byte("\n")), []byte("\n")) {
		toks, st = highlight.Line(lang, st, line, toks[:0])
		n += len(toks)
	}
	fmt.Println(lang, n > 0)
	// Output: go true
}
