//go:build darwin || linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/ghosttyvt"
	"gonativeui/term"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// TestAuditLayout runs the layout audit on the demo's screen in light and
// dark, at its narrowest window and a roomy one, with a program whose title
// is longer than the header.
func TestAuditLayout(t *testing.T) {
	if os.Getenv(term.EnvLibrary) == "" {
		os.Setenv(term.EnvLibrary, filepath.Join("..", "..", ".cache", "libghostty-vt", ghosttyvt.Short, runtime.GOOS+"-"+runtime.GOARCH))
	}
	if err := term.Available(); err != nil {
		if os.Getenv("GNUI_REQUIRE_LIBGHOSTTY") != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	argv := []string{"/bin/sh", "-c", `printf '\033]0;a window title longer than the narrowest header has room for\007'; seq 1 200; sleep 5`}
	for _, m := range []theme.Preference{theme.ForceLight, theme.ForceDark} {
		for _, sz := range [][2]int{{640, 500}, {1440, 900}} {
			d := &demo{argv: argv, mode: m, shot: true, transparency: true}
			o, err := app.NewOffscreen(app.Config{}, sz[0], sz[1], 2, d)
			if err != nil {
				if os.Getenv("CI") != "" {
					t.Fatalf("offscreen rendering on CI: %v", err)
				}
				t.Skipf("offscreen rendering: %v", err)
			}
			for range 40 {
				time.Sleep(10 * time.Millisecond)
				o.Frame()
			}
			if d.s.T.Title() == "" {
				t.Error("the program's title did not arrive")
			}
			widgettest.AuditLayout(t, fmt.Sprintf("termdemo %v %dx%d", m, sz[0], sz[1]), o.UI, fieldAllow)
			d.close()
			o.Close()
		}
	}
}

// fieldAllow accepts Rex's window field reaching 10 px past the left edge of
// a window 320 pt wide: an empty Rex window (k.BeginWindow alone) reports the
// same findings there and none at 500 pt, so it is the theme's, not this
// screen's.
var fieldAllow = widgettest.Allow{Check: ui.AuditOverflow, Where: "{floating background} in Window",
	Within: [4]float32{-20, 0, 1, 10000},
	Reason: "theme/rex's field overflows a 320 pt window on its left edge (an empty Rex window does too)"}
