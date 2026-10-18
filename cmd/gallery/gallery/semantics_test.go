package gallery

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

var update = flag.Bool("update", false, "rewrite the golden files of testdata")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run the test with -update to write it)", err)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")) // a Windows checkout may have converted the line endings
	if !bytes.Equal(want, []byte(got)) {
		wl, gl := strings.Split(string(want), "\n"), strings.Split(got, "\n")
		for i := 0; i < len(wl) || i < len(gl); i++ {
			var w, g string
			if i < len(wl) {
				w = wl[i]
			}
			if i < len(gl) {
				g = gl[i]
			}
			if w != g {
				t.Fatalf("%s differs from the semantic tree at line %d:\n  want %s\n  got  %s\n(run the test with -update if the change is intended)", path, i+1, w, g)
			}
		}
	}
}

// The semantic tree of the window as it opens, and with the dialog open over
// four selected rows. The rows are named by their first cell, so that a person
// using assistive technology, and a test, find "row 3" by its text.
func TestSemanticsGolden(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.Frames(5)
	golden(t, "semantics-main.txt", widgettest.Dump(r.Tree()))
	if n, ok := r.Find(ui.RoleRow, "3"); !ok || n.Pos != [2]int32{3, 1_000_000} {
		t.Errorf("the table row named 3: found %v, at %v", ok, n.Pos)
	}

	r.Play(click(rowRole, "").Nth(3), click(rowRole, "").Nth(6).With(modShift),
		click(roleButton, "Delete…"), waitFor(ui.RoleDialog, ""), settle(), idle(2))
	golden(t, "semantics-dialog.txt", widgettest.Dump(r.Tree()))
}
