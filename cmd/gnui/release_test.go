package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// gnui package builds with the gnui_release tag, whatever -tags says, and a
// development build (gnui run) does not.
func TestReleaseBuildsAreTaggedAndDevelopmentBuildsAreNot(t *testing.T) {
	c := &Config{Main: "."}
	for _, tc := range []struct {
		name string
		o    buildOptions
		want []string
	}{
		{"package", buildOptions{}, []string{"build", "-trimpath", "-ldflags=-s -w", "-tags", "gnui_release", "-o", "x", "."}},
		{"package with tags", buildOptions{Tags: "a,b"}, []string{"build", "-trimpath", "-ldflags=-s -w", "-tags", "a,b,gnui_release", "-o", "x", "."}},
		{"package that has it", buildOptions{Tags: "gnui_release,a"}, []string{"build", "-trimpath", "-ldflags=-s -w", "-tags", "gnui_release,a", "-o", "x", "."}},
		{"run", buildOptions{Dev: true}, []string{"build", "-o", "x", "."}},
		{"run with tags", buildOptions{Dev: true, Tags: "a"}, []string{"build", "-tags", "a", "-o", "x", "."}},
	} {
		if got := buildArgs(c, "x", tc.o); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: go %v, want %v", tc.name, got, tc.want)
		}
	}
}

// What a packaged binary contains: nothing of the debug overlay's chord, Debug
// menu or inspector panel, while a development build has all of them. The
// probe links package app; function names stay in the binary's symbol tables
// even when it is stripped, and the menu's titles are strings in it.
func TestPackagedBinaryHasNoDebugChordOrInspector(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries")
	}
	c, err := LoadConfig("testdata/debugprobe/gnui.json")
	if err != nil {
		t.Fatal(err)
	}
	needles := []string{"Toggle Overlay", "Run Layout Audit", "inspectLines", "chordCommand", "debugCommand"}
	build := func(o buildOptions) []byte {
		t.Helper()
		out := filepath.Join(t.TempDir(), "probe")
		if err := buildExecutable(c, out, o); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	dev := build(buildOptions{Dev: true})
	for _, n := range needles {
		if !bytes.Contains(dev, []byte(n)) {
			t.Errorf("a development build lacks %q: the check below proves nothing", n)
		}
	}
	rel := build(buildOptions{})
	for _, n := range needles {
		if bytes.Contains(rel, []byte(n)) {
			t.Errorf("a packaged binary contains %q", n)
		}
	}
}
