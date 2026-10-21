package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every Go and JSON block of the guide is an excerpt of real source: docs/GUIDE.md
// and each chapter in docs/guide/. A fence names the file
// (```go examples/notes/notes/notes.go), a line holding only "// …" leaves lines
// out, and the lines between two of those must appear in that file, in that
// order, without a line between them (blank lines and indentation do not
// count). A block of the guide that is not code of the example, or code the
// example has since changed, fails here. Blocks of other languages (sh, text)
// are not checked.
func TestGuideExcerptsAreTheExample(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	chapters, err := filepath.Glob(filepath.Join(root, "docs", "guide", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) == 0 {
		t.Fatal("docs/guide has no chapters")
	}
	guide := filepath.Join(root, "docs", "GUIDE.md")
	for _, file := range append([]string{guide}, chapters...) {
		name, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatal(err)
		}
		if blocks := checkGuide(t, root, file, filepath.ToSlash(name)); blocks == 0 {
			t.Errorf("%s has no checked blocks", name)
		}
	}
}

// checkGuide checks the blocks of one file of the guide and returns how many
// it checked.
func checkGuide(t *testing.T, root, file, name string) int {
	t.Helper()
	guide, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	blocks := 0
	lines := strings.Split(strings.ReplaceAll(string(guide), "\r\n", "\n"), "\n") // a Windows checkout may have CRLF
	for i := 0; i < len(lines); i++ {
		info, ok := strings.CutPrefix(lines[i], "```go")
		if !ok {
			if info, ok = strings.CutPrefix(lines[i], "```json"); !ok {
				continue
			}
		}
		start := i + 1
		for i++; i < len(lines) && lines[i] != "```"; i++ {
		}
		path := strings.TrimSpace(info)
		if path == "" {
			t.Errorf("%s:%d: a Go or JSON block must name the source file after the language", name, start)
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Errorf("%s:%d: %v", name, start, err)
			continue
		}
		blocks++
		have := codeLines(string(src))
		var frag []string
		flush := func(endLine int) {
			if len(frag) > 0 && !contains(have, frag) {
				t.Errorf("%s:%d: these lines are not in %s, in this order:\n%s", name, endLine-len(frag), path, strings.Join(frag, "\n"))
			}
			frag = frag[:0]
		}
		for j := start; j < i; j++ {
			if s := strings.TrimSpace(lines[j]); s == "// …" {
				flush(j)
			} else if s != "" {
				frag = append(frag, s)
			}
		}
		flush(i)
	}
	return blocks
}

// codeLines returns the non-blank lines of src, trimmed.
func codeLines(src string) []string {
	var out []string
	for _, l := range strings.Split(src, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// contains reports whether frag is a run of consecutive elements of have.
func contains(have, frag []string) bool {
	for i := 0; i+len(frag) <= len(have); i++ {
		j := 0
		for j < len(frag) && have[i+j] == frag[j] {
			j++
		}
		if j == len(frag) {
			return true
		}
	}
	return false
}
