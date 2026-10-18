package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every Go block of docs/GUIDE.md is an excerpt of the example: its fence names
// the file (```go examples/notes/notes/notes.go), a line holding only "// …"
// leaves lines out, and the lines between two of those must appear in that
// file, in that order, without a line between them (blank lines and
// indentation do not count). A block of the guide that is not code of the
// example, or code the example has since changed, fails here.
func TestGuideExcerptsAreTheExample(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	guide, err := os.ReadFile(filepath.Join(root, "docs", "GUIDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := 0
	lines := strings.Split(strings.ReplaceAll(string(guide), "\r\n", "\n"), "\n") // a Windows checkout may have CRLF
	for i := 0; i < len(lines); i++ {
		info, ok := strings.CutPrefix(lines[i], "```go")
		if !ok {
			continue
		}
		start := i + 1
		for i++; i < len(lines) && lines[i] != "```"; i++ {
		}
		path := strings.TrimSpace(info)
		if path == "" {
			t.Errorf("GUIDE.md:%d: a Go block must name the example file after ```go", start)
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Errorf("GUIDE.md:%d: %v", start, err)
			continue
		}
		blocks++
		have := codeLines(string(src))
		var frag []string
		flush := func(endLine int) {
			if len(frag) > 0 && !contains(have, frag) {
				t.Errorf("GUIDE.md:%d: these lines are not in %s, in this order:\n%s", endLine-len(frag), path, strings.Join(frag, "\n"))
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
	if blocks == 0 {
		t.Error("GUIDE.md has no Go blocks")
	}
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
