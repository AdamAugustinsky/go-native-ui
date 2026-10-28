//go:build linux

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadDragThreshold(t *testing.T) {
	user, system := t.TempDir(), t.TempDir()
	write := func(dir, ver, body string) {
		if err := os.MkdirAll(filepath.Join(dir, ver), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ver, "settings.ini"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dirs := []string{user, system}
	if got := readDragThreshold(dirs); got != dragThresholdLinux {
		t.Fatalf("no settings: %v, want GTK's default %d", got, dragThresholdLinux)
	}
	write(system, "gtk-3.0", "[Settings]\ngtk-dnd-drag-threshold=12\n")
	if got := readDragThreshold(dirs); got != 12 {
		t.Fatalf("the system's gtk-3.0: %v, want 12", got)
	}
	write(user, "gtk-4.0", "[Settings]\ngtk-dnd-drag-threshold=20\n")
	if got := readDragThreshold(dirs); got != 20 {
		t.Fatalf("the user's gtk-4.0 beats the system's: %v, want 20", got)
	}
	write(user, "gtk-3.0", "[Settings]\ngtk-dnd-drag-threshold=0\n") // out of range: skipped
	write(user, "gtk-4.0", "[Settings]\ngtk-dnd-drag-threshold=9999\n")
	if got := readDragThreshold(dirs); got != 12 {
		t.Fatalf("values out of range are ignored: %v, want the system's 12", got)
	}
}
