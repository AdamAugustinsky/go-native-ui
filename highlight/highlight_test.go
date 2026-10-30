package highlight_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gonativeui/highlight"
)

var update = flag.Bool("update", false, "rewrite the golden token files in testdata")

func TestLangOf(t *testing.T) {
	for name, want := range map[string]highlight.Lang{
		"go": highlight.Go, "TS": highlight.TypeScript, "python {linenos}": highlight.Python,
		"main.rs": highlight.Rust, "src/App.tsx": highlight.TypeScript, ".zshrc": highlight.Shell, "Makefile": highlight.Plain,
		"c#": highlight.CSharp, "a-very-long-directory/some-file-with-a-long-name.TsX": highlight.TypeScript, "C#": highlight.CSharp, "brainfuck": highlight.Plain, "": highlight.Plain,
	} {
		if n := testing.AllocsPerRun(10, func() { highlight.LangOf(name) }); n != 0 {
			t.Errorf("LangOf(%q) allocated %v times", name, n)
		}
		if got := highlight.LangOf(name); got != want {
			t.Errorf("LangOf(%q) = %v, want %v", name, got, want)
		}
	}
	if highlight.Go.String() != "go" || highlight.NumLangs.String() != "plain" {
		t.Error("String")
	}
}

// Every language but Plain has a lexer in the curated set.
func TestSupported(t *testing.T) {
	for l := highlight.Plain + 1; l < highlight.NumLangs; l++ {
		if !highlight.Supported(l) {
			t.Errorf("%v has no lexer", l)
		}
	}
	if highlight.Supported(highlight.Plain) {
		t.Error("Plain is supported")
	}
}

// checkCover fails unless toks are in order, inside src, never hold a line
// break, and leave out nothing but line breaks.
func checkCover(t *testing.T, src []byte, toks []highlight.Token) {
	t.Helper()
	at := uint32(0)
	for i, tk := range toks {
		if tk.Off < at || tk.Len == 0 || int(tk.Off+tk.Len) > len(src) {
			t.Fatalf("token %d %+v out of order or range (at %d, len %d)", i, tk, at, len(src))
		}
		for _, b := range src[at:tk.Off] {
			if b != '\n' {
				t.Fatalf("token %d %+v leaves out %q", i, tk, src[at:tk.Off])
			}
		}
		if bytes.IndexByte(src[tk.Off:tk.Off+tk.Len], '\n') >= 0 {
			t.Fatalf("token %d %+v holds a line break", i, tk)
		}
		at = tk.Off + tk.Len
	}
	for _, b := range src[at:] {
		if b != '\n' {
			t.Fatalf("the tokens leave out the tail %q", src[at:])
		}
	}
}

var roleNames = [highlight.NumRoles]string{"text", "keyword", "type", "function", "string", "number", "comment", "operator", "punct", "constant", "builtin", "tag", "attr", "property", "inserted", "deleted", "meta"}

// dump writes one token per line: its line number, role and text.
func dump(src []byte, toks []highlight.Token) string {
	var b strings.Builder
	for _, tk := range toks {
		line := 1 + bytes.Count(src[:tk.Off], []byte("\n"))
		fmt.Fprintf(&b, "%d %s %q\n", line, roleNames[tk.Role], src[tk.Off:tk.Off+tk.Len])
	}
	return b.String()
}

// The golden token runs of a few languages: testdata/<lang>.src lexed is
// testdata/<lang>.tokens (-update rewrites them).
func TestGoldenTokens(t *testing.T) {
	for _, name := range []string{"go", "typescript", "python", "rust", "shell", "json", "diff", "html"} {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("testdata", name+".src"))
			if err != nil {
				t.Fatal(err)
			}
			lang := highlight.LangOf(name)
			toks := highlight.Tokens(lang, src, nil)
			checkCover(t, src, toks)
			got := dump(src, toks)
			path := filepath.Join("testdata", name+".tokens")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("%s: tokens differ from %s (run with -update to see):\n%s", name, path, firstDiff(got, string(want)))
			}
		})
	}
}

func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var a, b string
		if i < len(g) {
			a = g[i]
		}
		if i < len(w) {
			b = w[i]
		}
		if a != b {
			return fmt.Sprintf("line %d: got %s, want %s", i+1, a, b)
		}
	}
	return ""
}

// A few roles a reader expects, beyond the goldens: Go's keywords, strings
// and comments, a diff's added and removed lines.
func TestRoles(t *testing.T) {
	src := []byte("// hi\nfunc main() { x := \"s\" + 1 }\n")
	toks := highlight.Tokens(highlight.Go, src, nil)
	role := func(s string) highlight.Role {
		i := bytes.Index(src, []byte(s))
		for _, tk := range toks {
			if int(tk.Off) <= i && i < int(tk.Off+tk.Len) {
				return tk.Role
			}
		}
		return 255
	}
	for s, want := range map[string]highlight.Role{"// hi": highlight.Comment, "func": highlight.Keyword, "main": highlight.Function, `"s"`: highlight.String, "1": highlight.Number, ":=": highlight.Operator} {
		if got := role(s); got != want {
			t.Errorf("%q is %v, want %v", s, got, want)
		}
	}
	d := []byte("@@ -1 +1 @@\n-old\n+new\n same\n")
	dt := highlight.Tokens(highlight.Diff, d, nil)
	checkCover(t, d, dt)
	want := []highlight.Role{highlight.Meta, highlight.Deleted, highlight.Inserted}
	for i, w := range want {
		if i >= len(dt) || dt[i].Role != w {
			t.Fatalf("diff tokens %s", dump(d, dt))
		}
	}
}

// Plain and an empty source: one Text token per non-empty line, nothing for
// nothing, and no allocation once dst has room.
func TestPlain(t *testing.T) {
	src := []byte("a\n\nbc\n")
	toks := highlight.Tokens(highlight.Plain, src, make([]highlight.Token, 0, 8))
	if len(toks) != 2 || toks[0] != (highlight.Token{Off: 0, Len: 1}) || toks[1] != (highlight.Token{Off: 3, Len: 2}) {
		t.Fatalf("Plain: %+v", toks)
	}
	if got := highlight.Tokens(highlight.Go, nil, nil); len(got) != 0 {
		t.Fatal("tokens of nothing")
	}
	dst := make([]highlight.Token, 0, 8)
	if n := testing.AllocsPerRun(100, func() { highlight.Tokens(highlight.Plain, src, dst[:0]) }); n != 0 {
		t.Fatalf("Plain allocates %v", n)
	}
}

// Pathological input stays about linear: an unterminated string and a
// 1 MB line lex in bounded time (generous: the Mac mini may be loaded), and
// doubling the input does not much more than double the time.
func TestPathological(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	line := func(n int) []byte {
		var b bytes.Buffer
		for b.Len() < n {
			b.WriteString(`x := foo(1, "bar") + y; `)
		}
		return b.Bytes()
	}
	lex := func(lang highlight.Lang, src []byte) time.Duration {
		t0 := time.Now()
		toks := highlight.Tokens(lang, src, nil)
		d := time.Since(t0)
		checkCover(t, src, toks)
		return d
	}
	half := lex(highlight.Go, line(512<<10))
	full := lex(highlight.Go, line(1<<20))
	t.Logf("Go, one line: 512 KB in %v, 1 MB in %v (Mac mini, loaded)", half, full)
	if full > 20*time.Second {
		t.Errorf("a 1 MB line took %v", full)
	}
	if full > 6*half+500*time.Millisecond { // loose: a loaded machine
		t.Errorf("not linear: 512 KB %v, 1 MB %v", half, full)
	}
	open := append([]byte(`s := "`), bytes.Repeat([]byte("ab "), 100<<10)...) // never closed
	if d := lex(highlight.Go, open); d > 10*time.Second {
		t.Errorf("an unterminated 300 KB string took %v", d)
	}
	ts := append([]byte("const s = `"), bytes.Repeat([]byte("${a} b "), 40<<10)...)
	if d := lex(highlight.TypeScript, ts); d > 10*time.Second {
		t.Errorf("an unterminated template string took %v", d)
	}
}
