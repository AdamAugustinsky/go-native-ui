// Package highlight splits source code into tokens with roles (keyword,
// string, comment …) for code blocks in chat and the Files and Changes
// panes (the phase 8 plan's C3 and D75). The lexers are chroma v2's
// (github.com/alecthomas/chroma/v2, MIT): a curated set for the languages
// below, registered on their own so the binary does not carry chroma's
// other 260 lexers (see Lexers and the gnui_chroma_full build tag).
//
//	lang := highlight.LangOf("ts") // a fence's info string or a file name
//	toks = highlight.Tokens(lang, src, toks[:0])
//	for _, t := range toks {
//		// src[t.Off : t.Off+t.Len] in the colour of t.Role (rexui.CodeBlock maps roles to Rex code colours)
//	}
//
// Tokens works on a whole block or file: chroma's lexers keep a stack of
// states across lines (a block comment, a heredoc, a template string), so a
// line cannot be lexed on its own. It allocates (chroma's regexp engine works
// on runes and makes a string per token), so run it on a worker, never in a
// frame: rexui.CodeHighlighter does that and hands the tokens to the frame.
// Each rule is anchored and chroma bounds every match to 250 ms, so a
// pathological input (an unterminated string, a 1 MB line) stays about linear
// in its length.
//
// Languages: Go, TypeScript, JavaScript, Python, Rust, Swift, C#, shell,
// JSON, YAML, TOML, SQL, HTML, CSS, diff and Markdown. Unknown languages are
// Plain: one Text token per line.
//
// Tokens are pointer-free and never cross a line break, so a renderer that
// draws line by line cuts them without looking at the text again.
package highlight

import (
	"bytes"
	"path"
	"strings"

	"github.com/alecthomas/chroma/v2"
)

// Lang is a language.
type Lang uint8

// The languages.
const (
	// Plain is no highlighting; the others are the languages of their names.
	Plain Lang = iota
	Go
	TypeScript
	JavaScript
	Python
	Rust
	Swift
	CSharp
	Shell
	JSON
	YAML
	TOML
	SQL
	HTML
	CSS
	Diff
	Markdown
	// NumLangs is the number of languages.
	NumLangs
)

var langNames = [NumLangs]string{"plain", "go", "typescript", "javascript", "python", "rust", "swift", "csharp", "shell", "json", "yaml", "toml", "sql", "html", "css", "diff", "markdown"}

// chromaNames are the chroma lexer names of the languages.
var chromaNames = [NumLangs]string{"", "go", "typescript", "javascript", "python", "rust", "swift", "c#", "bash", "json", "yaml", "toml", "sql", "html", "css", "diff", "markdown"}

// String is the language's name ("typescript").
func (l Lang) String() string {
	if l < NumLangs {
		return langNames[l]
	}
	return "plain"
}

// aliases maps fence names and file extensions to languages.
var aliases = map[string]Lang{
	"go": Go, "golang": Go,
	"ts": TypeScript, "typescript": TypeScript, "tsx": TypeScript, "mts": TypeScript, "cts": TypeScript,
	"js": JavaScript, "javascript": JavaScript, "jsx": JavaScript, "mjs": JavaScript, "cjs": JavaScript,
	"py": Python, "python": Python, "python3": Python, "pyi": Python,
	"rs": Rust, "rust": Rust,
	"swift": Swift,
	"cs":    CSharp, "csharp": CSharp, "c#": CSharp,
	"sh": Shell, "bash": Shell, "zsh": Shell, "shell": Shell, "console": Shell, "shellsession": Shell, "fish": Shell,
	"json": JSON, "jsonc": JSON, "json5": JSON,
	"yaml": YAML, "yml": YAML,
	"toml": TOML,
	"sql":  SQL,
	"html": HTML, "htm": HTML, "xml": HTML, "svg": HTML, "svelte": HTML, "vue": HTML,
	"css": CSS, "scss": CSS, "less": CSS,
	"diff": Diff, "patch": Diff,
	"md": Markdown, "markdown": Markdown,
	"plain": Plain, "text": Plain, "txt": Plain,
}

// LangOf names the language of a fence's info string ("ts", "golang",
// "python {linenos}": the first word) or a file name ("src/App.tsx",
// "Makefile"), case-insensitively; Plain when it is not one of ours.
func LangOf(name string) Lang {
	name = strings.TrimSpace(name)
	if i := strings.IndexAny(name, " \t{"); i >= 0 {
		name = name[:i]
	}
	name = strings.ToLower(name)
	if l, ok := aliases[name]; ok {
		return l
	}
	if ext := path.Ext(name); ext != "" {
		if l, ok := aliases[ext[1:]]; ok {
			return l
		}
	}
	switch path.Base(name) {
	case ".bashrc", ".zshrc", ".profile", ".bash_profile":
		return Shell
	}
	return Plain
}

// Role is what a token is; rexui maps roles to colours.
type Role uint8

// The roles.
const (
	// Text is anything else; the others are the roles of their names.
	Text Role = iota
	Keyword
	Type
	Function
	String
	Number
	Comment
	Operator
	Punctuation
	Constant
	Builtin
	Tag
	Attribute
	Property
	// Inserted and Deleted are a diff's added and removed lines; Meta its
	// headers and hunk lines, a shell's prompt, a decorator and a Markdown
	// heading.
	Inserted
	Deleted
	Meta
	// NumRoles is the number of roles.
	NumRoles
)

// Token is a run of a line with one role: bytes [Off, Off+Len) of the
// source Tokens was given.
type Token struct {
	// Off and Len are the token's bytes.
	Off, Len uint32
	// Role is what it is.
	Role Role
}

// MaxSource is the largest source Tokens lexes; a longer one is Plain. It
// bounds a worker's time and memory (chroma holds the source as runes, 4
// bytes each, and a token per lexeme).
const MaxSource = 8 << 20

// Supported reports whether lang has a lexer (every language but Plain).
func Supported(lang Lang) bool { return lang != Plain && lang < NumLangs && lexerOf(lang) != nil }

// Tokens appends the tokens of src in language lang to dst and returns
// dst. The tokens are in order and cover src except its line breaks ('\n'),
// which no token contains; runs of one role are merged. Plain, an unknown
// language and a source over MaxSource give one Text token per non-empty
// line. It allocates: call it on a worker.
func Tokens(lang Lang, src []byte, dst []Token) []Token {
	if len(src) == 0 {
		return dst
	}
	var lx chroma.Lexer
	if lang != Plain && lang < NumLangs && len(src) <= MaxSource {
		lx = lexerOf(lang)
	}
	if lx == nil {
		return plain(src, dst)
	}
	it, err := lx.Tokenise(nil, string(src))
	if err != nil {
		return plain(src, dst)
	}
	start, off := len(dst), 0
	for t := it(); t != chroma.EOF; t = it() {
		v := t.Value
		if off+len(v) > len(src) { // the newline a lexer adds at the end
			v = v[:len(src)-off]
		}
		if string(src[off:off+len(v)]) != v { // never seen: a lexer that rewrites text
			return plain(src, dst[:start])
		}
		dst = appendRun(dst, start, uint32(off), v, roleOf(t.Type))
		off += len(v)
		if off == len(src) {
			break
		}
	}
	if off < len(src) { // the lexer stopped early: the rest is text
		dst = appendRun(dst, start, uint32(off), string(src[off:]), Text)
	}
	return dst
}

// appendRun appends the token(s) of text v at off with role r, split at line
// breaks and merged with the token before it (from start on) when that one
// ends where it begins with the same role.
func appendRun(dst []Token, start int, off uint32, v string, r Role) []Token {
	for len(v) > 0 {
		n := strings.IndexByte(v, '\n')
		if n < 0 {
			n = len(v)
		}
		if n > 0 {
			if l := len(dst); l > start && dst[l-1].Role == r && dst[l-1].Off+dst[l-1].Len == off {
				dst[l-1].Len += uint32(n)
			} else {
				dst = append(dst, Token{Off: off, Len: uint32(n), Role: r})
			}
		}
		if n == len(v) {
			break
		}
		off += uint32(n + 1)
		v = v[n+1:]
	}
	return dst
}

// plain appends one Text token per non-empty line of src.
func plain(src []byte, dst []Token) []Token {
	for off := 0; off < len(src); {
		n := bytes.IndexByte(src[off:], '\n')
		if n < 0 {
			n = len(src) - off
		}
		if n > 0 {
			dst = append(dst, Token{Off: uint32(off), Len: uint32(n), Role: Text})
		}
		off += n + 1
	}
	return dst
}

// roleOf maps a chroma token type to a role.
func roleOf(t chroma.TokenType) Role {
	switch t {
	case chroma.KeywordType, chroma.NameClass, chroma.NameException:
		return Type
	case chroma.KeywordConstant, chroma.NameConstant, chroma.NameEntity, chroma.LiteralStringBoolean:
		return Constant
	case chroma.NameBuiltin, chroma.NameBuiltinPseudo:
		return Builtin
	case chroma.NameFunction, chroma.NameFunctionMagic:
		return Function
	case chroma.NameTag:
		return Tag
	case chroma.NameAttribute:
		return Attribute
	case chroma.NameProperty:
		return Property
	case chroma.NameDecorator, chroma.GenericHeading, chroma.GenericSubheading, chroma.GenericPrompt:
		return Meta
	case chroma.GenericInserted:
		return Inserted
	case chroma.GenericDeleted, chroma.GenericError, chroma.GenericTraceback:
		return Deleted
	case chroma.OperatorWord:
		return Keyword
	}
	switch {
	case t.InCategory(chroma.Keyword):
		return Keyword
	case t.InSubCategory(chroma.LiteralString):
		return String
	case t.InSubCategory(chroma.LiteralNumber), t == chroma.LiteralDate:
		return Number
	case t.InCategory(chroma.Operator):
		return Operator
	case t.InCategory(chroma.Punctuation):
		return Punctuation
	case t.InCategory(chroma.Comment):
		return Comment
	}
	return Text
}
