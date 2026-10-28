// Package lexers is the curated set of chroma v2 lexers package highlight
// uses: a registry of its own holding only the languages highlight.Lang
// names, instead of chroma's lexers package, which embeds and registers
// about 280 lexers at init (about 5 MB of binary).
//
// The XML lexers in embedded/ and the Go and Markdown rules below are copied
// from github.com/alecthomas/chroma/v2 v2.27.0 (lexers/embedded, lexers/go.go,
// lexers/markdown.go), MIT, Copyright (C) 2017 Alec Thomas; LICENSE is
// alongside. Changes: the Go lexer's raw strings are plain strings (chroma
// lexes their contents with its Go text-template lexer, which is not in
// this set), and the Markdown lexer's front matter uses this registry.
package lexers

import (
	"embed"
	"io/fs"
	"strings"
	"sync"

	. "github.com/alecthomas/chroma/v2" //nolint:revive // the rules read as chroma's do
)

//go:embed embedded
var embedded embed.FS

// Registry holds the curated lexers, made on first use (about 0.3 ms to
// parse the XML configs: not before an app's first frame). Lexers compile
// their rules on their own first use.
var Registry = sync.OnceValue(func() *LexerRegistry {
	reg := NewLexerRegistry()
	paths, err := fs.Glob(embedded, "embedded/*.xml")
	if err != nil {
		panic(err)
	}
	for _, path := range paths {
		reg.Register(MustNewXMLLexer(embedded, path))
	}
	reg.Register(MustNewLexer(&Config{
		Name: "Go", Aliases: []string{"go", "golang"}, Filenames: []string{"*.go"}, MimeTypes: []string{"text/x-gosrc"},
	}, goRules))
	reg.Register(&markdownLexer{Lexer: MustNewLexer(&Config{
		Name: "markdown", Aliases: []string{"md", "mkd"}, Filenames: []string{"*.md", "*.mkd", "*.markdown"}, MimeTypes: []string{"text/x-markdown"},
	}, markdownRules)})
	return reg
})

// Get is the lexer of a name or alias in Registry, nil if there is none.
func Get(name string) Lexer { return Registry().Get(name) }

// rule is a rule with no mutator.
func rule(pattern string, emit Emitter) Rule { return Rule{Pattern: pattern, Type: emit} }

func goRules() Rules {
	return Rules{
		"root": {
			rule(`\n`, TextWhitespace),
			rule(`\s+`, TextWhitespace),
			rule(`//[^\s\n\r][^\n\r]*`, CommentPreproc),
			rule(`//[^\n\r]*`, CommentSingle),
			rule(`/(\\\n)?[*](.|\n)*?[*](\\\n)?/`, CommentMultiline),
			rule(`(import|package)\b`, KeywordNamespace),
			rule(`(var|func|struct|map|chan|type|interface|const)\b`, KeywordDeclaration),
			rule(Words(``, `\b`, `break`, `default`, `select`, `case`, `defer`, `go`, `else`, `goto`, `switch`, `fallthrough`, `if`, `range`, `continue`, `for`, `return`), Keyword),
			rule(`(true|false|iota|nil)\b`, KeywordConstant),
			rule(Words(``, `\b(\()`, `uint`, `uint8`, `uint16`, `uint32`, `uint64`, `int`, `int8`, `int16`, `int32`, `int64`, `float`, `float32`, `float64`, `complex64`, `complex128`, `byte`, `rune`, `string`, `bool`, `error`, `uintptr`, `print`, `println`, `panic`, `recover`, `close`, `complex`, `real`, `imag`, `len`, `cap`, `append`, `copy`, `delete`, `new`, `make`, `clear`, `min`, `max`), ByGroups(NameBuiltin, Punctuation)),
			rule(Words(``, `\b`, `uint`, `uint8`, `uint16`, `uint32`, `uint64`, `int`, `int8`, `int16`, `int32`, `int64`, `float`, `float32`, `float64`, `complex64`, `complex128`, `byte`, `rune`, `string`, `bool`, `error`, `uintptr`, `any`), KeywordType),
			rule(`\d+i`, LiteralNumber),
			rule(`\d+\.\d*([Ee][-+]\d+)?i`, LiteralNumber),
			rule(`\.\d+([Ee][-+]\d+)?i`, LiteralNumber),
			rule(`\d+[Ee][-+]\d+i`, LiteralNumber),
			rule(`\d+(\.\d+[eE][+\-]?\d+|\.\d*|[eE][+\-]?\d+)`, LiteralNumberFloat),
			rule(`\.\d+([eE][+\-]?\d+)?`, LiteralNumberFloat),
			rule(`0[0-7]+`, LiteralNumberOct),
			rule(`0[xX][0-9a-fA-F_]+`, LiteralNumberHex),
			rule(`0b[01_]+`, LiteralNumberBin),
			rule(`(0|[1-9][0-9_]*)`, LiteralNumberInteger),
			rule(`'(\\['"\\abfnrtv]|\\x[0-9a-fA-F]{2}|\\[0-7]{1,3}|\\u[0-9a-fA-F]{4}|\\U[0-9a-fA-F]{8}|[^\\])'`, LiteralStringChar),
			rule("`[^`]*`", LiteralString),
			rule(`"(\\\\|\\"|[^"])*"`, LiteralString),
			rule(`(<<=|>>=|<<|>>|<=|>=|&\^=|&\^|\+=|-=|\*=|/=|%=|&=|\|=|&&|\|\||<-|\+\+|--|==|!=|:=|\.\.\.|[+\-*/%&])`, Operator),
			rule(`([a-zA-Z_]\w*)(\s*)(\()`, ByGroups(NameFunction, UsingSelf("root"), Punctuation)),
			rule(`[|^<>=!()\[\]{}.,;:~]`, Punctuation),
			rule(`[^\W\d]\w*`, NameOther),
		},
	}
}

// markdownLexer highlights a leading YAML front matter block, then the rest
// as Markdown.
type markdownLexer struct {
	Lexer
}

func (m *markdownLexer) Tokenise(options *TokeniseOptions, text string) (Iterator, error) {
	frontmatter, rest, ok := splitFrontmatter(text)
	if !ok {
		return m.Lexer.Tokenise(options, text)
	}
	yaml := Registry().Get("YAML")
	if yaml == nil {
		return m.Lexer.Tokenise(options, text)
	}
	yamlTokens, err := yaml.Tokenise(options, frontmatter)
	if err != nil {
		return nil, err
	}
	markdownTokens, err := m.Lexer.Tokenise(options, rest)
	if err != nil {
		return nil, err
	}
	return Concaterator(yamlTokens, markdownTokens), nil
}

// splitFrontmatter extracts a leading YAML front matter block.
func splitFrontmatter(text string) (frontmatter string, rest string, ok bool) {
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return "", text, false
	}
	lineEnd := strings.IndexByte(text, '\n')
	if lineEnd < 0 {
		return "", text, false
	}
	if strings.TrimSuffix(text[:lineEnd], "\r") != "---" {
		return "", text, false
	}
	for pos := lineEnd + 1; pos < len(text); {
		next := strings.IndexByte(text[pos:], '\n')
		if next < 0 {
			break
		}
		lineEnd = pos + next
		if strings.TrimSuffix(text[pos:lineEnd], "\r") == "---" {
			return text[:lineEnd+1], text[lineEnd+1:], true
		}
		pos = lineEnd + 1
	}
	return "", text, false
}

func markdownRules() Rules {
	return Rules{
		"root": {
			rule(`<!--[\w\W]*?-->`, CommentMultiline),
			rule(`^(#[^#].+\n)`, ByGroups(GenericHeading)),
			rule(`^(#{2,6}.+\n)`, ByGroups(GenericSubheading)),
			rule(`^(\s*)([*-] )(\[[ xX]\])( .+\n)`, ByGroups(Text, Keyword, Keyword, UsingSelf("inline"))),
			rule(`^(\s*)([*-])(\s)(.+\n)`, ByGroups(Text, Keyword, Text, UsingSelf("inline"))),
			rule(`^(\s*)([0-9]+\.)( .+\n)`, ByGroups(Text, Keyword, UsingSelf("inline"))),
			rule(`^(\s*>\s)(.+\n)`, ByGroups(Keyword, GenericEmph)),
			rule("^(```\\n)([\\w\\W]*?)(^```$)", ByGroups(String, Text, String)),
			rule("^(```)(\\w+)(\\n)([\\w\\W]*?)(^```$)", UsingByGroup(2, 4, String, String, String, Text, String)),
			Include("inline"),
		},
		"inline": {
			rule(`<!--[\w\W]*?-->`, CommentMultiline),
			rule(`\\.`, Text),
			rule(`(\s)(\*|_)((?:(?!\2).)*)(\2)((?=\W|\n))`, ByGroups(Text, GenericEmph, GenericEmph, GenericEmph, Text)),
			rule(`(\s)((\*\*|__).*?)\3((?=\W|\n))`, ByGroups(Text, GenericStrong, GenericStrong, Text)),
			rule(`(\s)(~~[^~]+~~)((?=\W|\n))`, ByGroups(Text, GenericDeleted, Text)),
			rule("`[^`]+`", LiteralStringBacktick),
			rule(`[@#][\w/:]+`, NameEntity),
			rule(`(!?\[)([^]]+)(\])(\()([^)]+)(\))`, ByGroups(Text, NameTag, Text, Text, NameAttribute, Text)),
			rule(`.|\n`, Text),
		},
	}
}
