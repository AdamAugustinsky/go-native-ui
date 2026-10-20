//go:build windows

package img

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"gonativeui/internal/win32"
)

// sysIconEnv records where decodes fail, and shows it if the test does.
func sysIconEnv(t testing.TB) {
	var mu sync.Mutex
	var fails []string
	iconTrace = func(step string, code uintptr) {
		mu.Lock()
		fails = append(fails, fmt.Sprintf("%s: %#x", step, code))
		mu.Unlock()
	}
	t.Cleanup(func() {
		iconTrace = nil
		mu.Lock()
		defer mu.Unlock()
		if t.Failed() || len(fails) > 0 {
			t.Logf("failed decode steps: %v", fails)
		}
	})
}

// TestSystemIconsWindowsBurst: a burst of 200 icons of unknown extensions on
// the cache's workers, all of which must arrive.
func TestSystemIconsWindowsBurst(t *testing.T) {
	sysIconEnv(t)
	c, _ := newTestCache(t, Options{Workers: 4})
	const n = 200
	var keys [n]Key
	var srcs [n]Source
	for i := range keys {
		ext := fmt.Sprintf("burst%d", i)
		keys[i] = Key{Hash: HashTypeIcon(ext), W: 64, H: 64}
		srcs[i] = ExtensionIcon(ext)
	}
	settle(t, c, func() {
		for i := range keys {
			c.Request(keys[i], srcs[i])
		}
	})
	if s := c.Stats(); s.Ready != n {
		t.Errorf("%d of %d icons ready (%+v)", s.Ready, n, s)
	}
}

// TestSystemIconsWindows decodes the shell's icons on a worker at 32 and 64
// pixels: a folder and a .txt, from a path (IShellItemImageFactory) and from
// the type alone (the system image lists).
func TestSystemIconsWindows(t *testing.T) {
	sysIconEnv(t)
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
	sysIconEnv(t)
	c, _ := newTestCache(t, Options{})
	srcs := []Source{FolderIcon(), ExtensionIcon("txt"), ExtensionIcon("png"), FileIcon(os.TempDir())}
	want := make([]decoded, len(srcs))
	for i, s := range srcs {
		want[i] = iconNow(t, c, s, 48, 48, 0)
		if want[i].r.buf < 0 {
			t.Fatalf("icon %d failed alone: %v", i, errOf(want[i].r.err))
		}
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
					if d.r.buf < 0 {
						t.Errorf("icon %d failed when decoded concurrently: %v", i, errOf(d.r.err))
						return
					}
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

// TestSystemIconsWindowsAlphaKinds checks what shellPixels is told about the
// shell's alpha: the bitmap IShellItemImageFactory renders is premultiplied
// (no channel above its alpha), the colour bitmap of an icon is straight (an
// antialiased edge with a channel above its alpha).
func TestSystemIconsWindowsAlphaKinds(t *testing.T) {
	sysIconEnv(t)
	wk := new(worker)
	dir := t.TempDir()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !loadShell() || win32.HR(win32.Call(shell.coInit, 0, coApartment|coNoOle1DDE)) < 0 {
		t.Fatal("no shell")
	}
	defer win32.Call(shell.coUninit)
	over := func(px []byte) (n, partial int) {
		for i := 0; i < len(px); i += 4 {
			a := px[i+3]
			if a != 0 && a != 255 {
				partial++
			}
			if px[i] > a || px[i+1] > a || px[i+2] > a {
				n++
			}
		}
		return
	}
	px, _, _, code := wk.itemPixels(dir, 128)
	if px == nil {
		t.Fatalf("item bitmap: error %d", code)
	}
	n, partial := over(px)
	t.Logf("item bitmap: %d of %d pixels have a channel above their alpha, %d translucent", n, len(px)/4, partial)
	if n != 0 {
		t.Errorf("IShellItemImageFactory's bitmap is not premultiplied: %d pixels have a channel above their alpha", n)
	}
	var iconN, iconPartial int
	iconRawHook = func(px []byte) { iconN, iconPartial = over(px) }
	defer func() { iconRawHook = nil }()
	m, code := wk.typeIcon(&Source{kind: srcIconFolder}, 256)
	if m == nil {
		t.Fatalf("folder icon: error %d", code)
	}
	t.Logf("folder icon bitmap: %d pixels with a channel above their alpha, %d translucent", iconN, iconPartial)
	if iconPartial == 0 || iconN == 0 {
		t.Errorf("an icon's colour bitmap is not straight alpha: %d translucent pixels, %d of them with a channel above their alpha", iconPartial, iconN)
	}
}

// TestSystemIconsWindowsScratchIsBounded: a large icon does not leave a
// decoded-size buffer behind in the worker.
func TestSystemIconsWindowsScratchIsBounded(t *testing.T) {
	sysIconEnv(t)
	c, _ := newTestCache(t, Options{})
	wk := new(worker)
	j := job{src: FileIcon(t.TempDir()), copy: -1, w: 1024, h: 1024}
	r := wk.decodeIcon(c, &j)
	if r.buf < 0 {
		t.Fatalf("a 1024 pixel icon failed: %v", errOf(r.err))
	}
	c.bufs.put(r.buf, c.fr)
	if n := cap(wk.icon.color) + cap(wk.icon.mask); n > 2*maxIconScratch {
		t.Errorf("the worker keeps %d bytes of scratch after a 1024 pixel icon (limit %d each)", n, maxIconScratch)
	}
	// A small icon's scratch is kept for the next one.
	j.w, j.h = 32, 32
	r = wk.decodeIcon(c, &j)
	if r.buf < 0 {
		t.Fatal("a 32 pixel icon failed")
	}
	c.bufs.put(r.buf, c.fr)
	if cap(wk.icon.color) == 0 {
		t.Error("a small icon's scratch was dropped")
	}
}
