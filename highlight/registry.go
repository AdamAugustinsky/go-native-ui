//go:build !gnui_chroma_full

package highlight

import (
	"github.com/alecthomas/chroma/v2"

	"gonativeui/highlight/internal/lexers"
)

// Lexers says which lexers the binary carries: "curated" (the languages of
// Lang, highlight/internal/lexers) or, built with the gnui_chroma_full tag,
// "full" (chroma's lexers package, every lexer chroma has, still reached
// only through the languages of Lang). Measured on the notes example
// (stripped, 2026-10-27): curated adds 1.0 MB on darwin/arm64 and 1.07 MB on
// windows/amd64, full 3.7 and 3.8 MB; full also parses about 280 XML configs
// at init (3-4 ms), curated parses its 14 on first use.
const Lexers = "curated"

// lexerOf is the lexer of lang, nil for none.
func lexerOf(lang Lang) chroma.Lexer {
	if n := chromaNames[lang]; n != "" {
		return lexers.Get(n)
	}
	return nil
}
