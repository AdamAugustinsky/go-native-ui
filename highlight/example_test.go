package highlight_test

import (
	"fmt"

	"gonativeui/highlight"
)

// Example lexes a code block (on a worker, in an app) and prints its
// keywords.
func Example() {
	src := []byte("package main\n\nfunc main() {}\n")
	lang := highlight.LangOf("go")
	for _, t := range highlight.Tokens(lang, src, nil) {
		if t.Role == highlight.Keyword {
			fmt.Printf("%s ", src[t.Off:t.Off+t.Len])
		}
	}
	fmt.Println(lang)
	// Output: package func go
}
