//go:build gnui_chroma_full

package highlight

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Lexers says which lexers the binary carries (see registry.go).
const Lexers = "full"

// lexerOf is the lexer of lang, nil for none.
func lexerOf(lang Lang) chroma.Lexer {
	if n := chromaNames[lang]; n != "" {
		return lexers.Get(n)
	}
	return nil
}
