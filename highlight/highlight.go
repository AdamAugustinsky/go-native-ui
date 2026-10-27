// Package highlight splits source code into tokens with roles (keyword,
// string, comment …) for code blocks in chat and the Files and Changes
// panes (the phase 8 plan's C3 and D75). The lexers are table-driven and
// line-oriented: a State carries what a line leaves open (a block comment,
// a multi-line string) into the next, so a streaming message or a long file
// is highlighted a line at a time.
//
//	lang := highlight.LangOf("ts") // a fence's info string or a file name
//	var st highlight.State
//	for _, line := range lines {
//		toks, st = highlight.Line(lang, st, line, toks[:0])
//		for _, t := range toks {
//			// line[t.Off : t.Off+t.Len] in the colour of t.Role (rexui.CodeBlock maps roles to Rex code colours)
//		}
//	}
//
// Languages: Go, TypeScript, JavaScript, Python, Rust, Swift, C#, shell,
// JSON, YAML, TOML, SQL, HTML, CSS, diff and Markdown. Unknown languages
// are Plain: one Text token per line.
//
// Tokens are pointer-free; Line allocates nothing beyond dst's growth and
// stays linear on pathological input (unterminated strings, 1 MB lines).
//
// Wave 0 of phase 8 fixes this API with the fallback for unknown languages
// everywhere: until C3 lands, every language is highlighted as Plain.
package highlight

import (
	"path"
	"strings"
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
// "python {linenos}": the first word) or a file name 
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
	// headers and hunk lines, and a shell's prompt.
	Inserted
	Deleted
	Meta
	// NumRoles is the number of roles.
	NumRoles
)

// Token is a run of a line with one role: bytes [Off, Off+Len).
type Token struct {
	// Off and Len are the token's bytes in the line.
	Off, Len uint32
	// Role is what it is.
	Role Role
}

// State is what a line leaves open for the next (0 at the start of a
// file).
type State uint32

// Line appends the tokens of line (without its newline) in language lang,
// starting in state st, to dst, and returns them with the state the next
// line starts in. The tokens cover the line without gaps or overlap.
func Line(lang Lang, st State, line []byte, dst []Token) ([]Token, State) {
	if len(line) == 0 {
		return dst, st
	}
	return append(dst, Token{Off: 0, Len: uint32(len(line)), Role: Text}), st // wave 0: Plain for every language
}

// Supported reports whether lang has a lexer yet (false for every language
// until C3).
func Supported(lang Lang) bool { return false }
