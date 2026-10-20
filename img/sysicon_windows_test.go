//go:build windows

package img

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestSystemIconsWindows decodes the shell's icons on a worker at 32 and 64
// pixels: a folder and a .txt, from a path (IShellItemImageFactory) and from
// the type alone (the system image lists).
func TestSystemIconsWindows(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcs := map[string]Source{
		"folder path": FileIcon(dir),
		"folder type": FolderIcon(),
		"folder uti":  TypeIcon("public.folder"),
		"txt path":    FileIcon(file),
		"txt type":    ExtensionIcon(".txt"),
		"txt bare":    ExtensionIcon("txt"),
	}
	for name, src := range srcs {
		for _, side := range []int32{32, 64} {
			d := iconNow(t, c, src, side, side, 0)
			validIcon(t, name, d, int(side))
			if d.r.buf >= 0 {
				t.Logf("%s at %d: %d×%d, %d distinct pixels", name, side, d.w, d.h, distinct(d))
			}
		}
	}
	if d := iconNow(t, c, FileIcon(filepath.Join(dir, "missing.txt")), 32, 32, 0); d.r.err != errRead {
		t.Errorf("a missing file: error %d, want read", d.r.err)
	}
	if d := iconNow(t, c, TypeIcon("com.adobe.pdf"), 32, 32, 0); d.r.err != errUnsupported {
		t.Errorf("a UTI: error %d, want unsupported", d.r.err)
	}
}

// TestSystemIconsWindowsConcurrent: workers decode at once, each initialising
// COM on its own thread; every icon equals the one decoded alone (the shell
// may settle an icon by a level or two, so within a small tolerance).
func TestSystemIconsWindowsConcurrent(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	srcs := []Source{FolderIcon(), ExtensionIcon("txt"), ExtensionIcon("png"), FileIcon(os.TempDir())}
	want := make([]decoded, len(srcs))
	for i, s := range srcs {
		want[i] = iconNow(t, c, s, 48, 48, 0)
		again := iconNow(t, c, s, 48, 48, 0) // on this thread, one after the other
		mean, worst := iconDiff(want[i], again)
		t.Logf("icon %d decoded twice in a row: mean difference %.2f, worst %d", i, mean, worst)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 10; k++ {
				for i, s := range srcs {
					d := iconNow(t, c, s, 48, 48, 0)
					if mean, worst := iconDiff(want[i], d); mean > 1 || worst > 16 {
						t.Errorf("icon %d differs when decoded concurrently: mean %.2f, worst %d (%d and %d distinct pixels)", i, mean, worst, distinct(want[i]), distinct(d))
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
