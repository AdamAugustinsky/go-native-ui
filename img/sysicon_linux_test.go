//go:build linux

package img

import (
	"os"
	"path/filepath"
	"testing"
)

// sysIconEnv gives a test the icons of a theme it makes (xdgTree).
func sysIconEnv(t testing.TB) { xdgTree(t) }

// TestSystemIconsLinux decodes icons from the theme of xdgTree on a worker
// at 32 and 64 pixels.
func TestSystemIconsLinux(t *testing.T) {
	root := xdgTree(t)
	c, _ := newTestCache(t, Options{})
	file := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcs := map[string]Source{
		"folder":      FileIcon(root),
		"folder type": FolderIcon(),
		"txt file":    FileIcon(file),
		"txt":         ExtensionIcon("txt"),
		"unknown":     ExtensionIcon("zzz"),
	}
	for name, src := range srcs {
		for _, side := range []int32{32, 64} {
			validIcon(t, name, iconNow(t, c, src, side, side, 0), int(side))
		}
	}
	if d := iconNow(t, c, TypeIcon("com.adobe.pdf"), 32, 32, 0); d.r.err != errUnsupported {
		t.Errorf("a UTI: error %d, want unsupported", d.r.err)
	}
	if d := iconNow(t, c, FileIcon(filepath.Join(root, "missing")), 32, 32, 0); d.r.err != errRead {
		t.Errorf("a missing file: error %d, want read", d.r.err)
	}
	if d := iconNow(t, c, ExtensionIcon("txt"), 11000, 11000, 0); d.r.err != errTooLarge {
		t.Errorf("121 megapixels: error %d, want too large", d.r.err)
	}
}

// TestSystemIconsLinuxThroughCache: the icons are requested like any image,
// and a system without a theme says ErrUnsupported.
func TestSystemIconsLinuxThroughCache(t *testing.T) {
	root := xdgTree(t)
	c, _ := newTestCache(t, Options{})
	key := Key{Hash: HashTypeIcon("txt"), W: 64, H: 64}
	var h Handle
	settle(t, c, func() { h, _ = c.Request(key, ExtensionIcon("txt")) })
	if c.State(h) != Ready {
		t.Fatalf("state %v (%v)", c.State(h), c.Err(h))
	}
	if info, _ := c.Info(h); info.W != 64 || info.H != 64 {
		t.Errorf("icon is %d×%d, want 64×64", info.W, info.H)
	}
	t.Setenv("XDG_DATA_DIRS", filepath.Join(root, "nothing"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "nothing"))
	key = Key{Hash: HashTypeIcon("txt"), W: 48, H: 48}
	settle(t, c, func() { h, _ = c.Request(key, ExtensionIcon("txt")) })
	if c.State(h) != Failed || c.Err(h) != ErrUnsupported {
		t.Errorf("without a theme: state %v, error %v", c.State(h), c.Err(h))
	}
}
