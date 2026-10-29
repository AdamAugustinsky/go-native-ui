//go:build darwin

package selfupdate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gonativeui/internal/testlock"
)

// hfsVolume mounts a small HFS+ disk image (no root: hdiutil attaches an image
// the user owns) and returns its mount point. The test is skipped when the
// image cannot be made or attached, as it can be where disk images are
// restricted; the volume is detached when the test ends.
func hfsVolume(t *testing.T) string {
	t.Helper()
	// The disk image service is the system's: cmd/gnui's tests (another
	// process) make and verify images; held until the volume is detached.
	testlock.Hold(t, "diskimage")
	dir := realTemp(t)
	img, mnt := filepath.Join(dir, "hfs.dmg"), filepath.Join(dir, "mnt")
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	try := func(name string, args ...string) (out []byte, err error) {
		for i := 0; i < 4; i++ { // a new image is briefly busy (cmd/gnui's hdiutil retry)
			if out, err = exec.Command(name, args...).CombinedOutput(); err == nil {
				return out, nil
			}
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
		}
		return out, err
	}
	if out, err := try("/usr/bin/hdiutil", "create", "-size", "20m", "-fs", "HFS+", "-volname", "FoldKey", "-layout", "NONE", img); err != nil {
		t.Skipf("no HFS+ disk image here: %v\n%s", err, out)
	}
	if out, err := try("/usr/bin/hdiutil", "attach", "-nobrowse", "-noverify", "-mountpoint", mnt, img); err != nil {
		t.Skipf("cannot attach an HFS+ disk image here: %v\n%s", err, out)
	}
	t.Cleanup(func() { // runs before TempDir's removal
		if out, err := try("/usr/bin/hdiutil", "detach", "-force", mnt); err != nil {
			t.Errorf("cannot detach %s: %v\n%s", mnt, err, out)
		}
	})
	if out, err := exec.Command("/sbin/mount").Output(); err != nil || !bytes.Contains(out, []byte(mnt+" (hfs,")) {
		t.Skipf("%s is not an HFS+ volume:\n%s", mnt, out)
	}
	return mnt
}

// The ground truth for hfsIgnorable: on a real HFS+ volume, every code point
// that is skipped when names are compared (a file named "probe_a" is found
// under a name with it at the end, in the middle or at the start) is dropped
// by foldKey, so the key matches whatever the volume matches.
func TestFoldKeyIgnoresWhatHFSPlusIgnores(t *testing.T) {
	mnt := hfsVolume(t)
	if err := os.WriteFile(filepath.Join(mnt, "probe_a"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	found := map[rune]bool{}
	for r := rune(1); r <= 0x10FFFF; r++ {
		if r == '/' || r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		c := string(r)
		names := []string{"probe_a" + c, "pro" + c + "be_a", c + "probe_a"}
		if r > 0xFFFF {
			names = names[:1] // the comparison folds 16-bit units; one position keeps this quick
		}
		for _, name := range names {
			if _, err := os.Lstat(filepath.Join(mnt, name)); err != nil {
				continue
			}
			found[r] = true
			if foldKey(name) != foldKey("probe_a") {
				t.Errorf("HFS+ finds %q as probe_a, but foldKey gives %q", name, foldKey(name))
			}
		}
	}
	// The volume and the list agree both ways, so a code point Apple adds
	// shows up here as a failure rather than as a hole.
	for _, r := range hfsIgnored {
		if !found[r] {
			t.Errorf("U+%04X is in the list, but this HFS+ volume does not skip it", r)
		}
	}
	for r := range found {
		if !hfsIgnorable(r) {
			t.Errorf("this HFS+ volume skips U+%04X, which hfsIgnorable does not list", r)
		}
	}
	t.Logf("HFS+ skips %d code points; all are in hfsIgnorable", len(found))
}

// Two crafted archives, on the file system the aliasing needs. Whatever ditto
// does with them is Apple's behaviour and is only logged (measured on macOS
// 27: on HFS+ a folder reached through the link fails with ENOTDIR, and a file
// named like the link replaces it, so an archive's two entries become one
// directory entry; on APFS both exist, and nothing is written through a link).
// What is ours, and asserted, is that checkArchive refuses both before ditto
// runs.
func TestHFSPlusAliasesAreRefusedBeforeTheyAreExtracted(t *testing.T) {
	mnt := hfsVolume(t)
	base := []entry{dir("S.app/"), dir("S.app/Resources/"), file("S.app/Info", "ORIGINAL"), link("S.app/a", "Info"), link("S.app/d", "Resources")}
	for _, c := range []struct {
		name  string
		extra entry
		want  string
	}{
		{"a file named like a link", file("S.app/a\u200c", "OVER-THE-LINK"), "shares its name"},
		{"a file in a folder named like a link", file("S.app/d\u200c/x", "THROUGH-THE-LINK"), "through the link"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := mkzip(t, append(append([]entry(nil), base...), c.extra)...)
			if _, err := checkArchive(r, r.Size()); !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("checkArchive = %v, want ErrRejected mentioning %q", err, c.want)
			}
			zip := filepath.Join(realTemp(t), "alias.zip")
			b := make([]byte, r.Size())
			r.ReadAt(b, 0)
			if err := os.WriteFile(zip, b, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, vol := range []struct{ name, dir string }{{"HFS+", mnt}, {"APFS", realTemp(t)}} {
				where := filepath.Join(vol.dir, "x")
				os.MkdirAll(where, 0o755)
				out, err := exec.Command("/usr/bin/ditto", "-x", "-k", "--noqtn", "--noacl", zip, where).CombinedOutput()
				var names []string
				ents, _ := os.ReadDir(filepath.Join(where, "S.app"))
				for _, e := range ents {
					names = append(names, fmt.Sprintf("%q", e.Name()))
				}
				info, _ := os.ReadFile(filepath.Join(where, "S.app", "Info"))
				t.Logf("%s: ditto error %v; S.app holds %s; Info is %q; %s", vol.name, err, strings.Join(names, " "), info, bytes.TrimSpace(out))
			}
		})
	}
}
