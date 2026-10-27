package highlight_test

import (
	"testing"

	"gonativeui/highlight"
)

func TestLangOf(t *testing.T) {
	for name, want := range map[string]highlight.Lang{
		"go": highlight.Go, "TS": highlight.TypeScript, "python {linenos}": highlight.Python,
		"main.rs": highlight.Rust, "src/App.tsx": highlight.TypeScript, ".zshrc": highlight.Shell, "Makefile": highlight.Plain,
		"c#": highlight.CSharp, "brainfuck": highlight.Plain, "": highlight.Plain,
	} {
		if got := highlight.LangOf(name); got != want {
			t.Errorf("LangOf(%q) = %v, want %v", name, got, want)
		}
	}
	if highlight.Go.String() != "go" || highlight.NumLangs.String() != "plain" {
		t.Error("String")
	}
}

// TestLineFallback is wave 0's fallback: one Text token per line, state
// passed through, no allocation once dst has room.
func TestLineFallback(t *testing.T) {
	dst := make([]highlight.Token, 0, 8)
	line := []byte(`func main() { println("hi") }`)
	toks, st := highlight.Line(highlight.Go, 7, line, dst)
	if len(toks) != 1 || toks[0].Len != uint32(len(line)) || toks[0].Role != highlight.Text || st != 7 {
		t.Fatalf("Line = %+v %d", toks, st)
	}
	if n := testing.AllocsPerRun(100, func() { highlight.Line(highlight.Go, 0, line, dst[:0]) }); n != 0 {
		t.Fatalf("Line allocates %v", n)
	}
}
