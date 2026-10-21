//go:build darwin

package fs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Finding 12: names that do not fit the 32-bit arena are an error with a
// partial listing, never wrapped offsets.
func TestListDirTooLarge(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	old := arenaLimit
	defer func() { arenaLimit = old }()
	arenaLimit = 25 // room for two 10-byte names
	l := new(Listing)
	err := ListDir(dir, l)
	if !errors.Is(err, ErrTooLarge) || !errors.Is(l.Err, ErrTooLarge) {
		t.Fatalf("err = %v, l.Err = %v", err, l.Err)
	}
	if len(l.Entries) != 2 || len(l.Names) != 20 {
		t.Fatalf("partial listing: %d entries, %d name bytes", len(l.Entries), len(l.Names))
	}
	for i := range l.Entries {
		if n := string(l.Name(i)); len(n) != 10 {
			t.Errorf("entry %d name %q", i, n)
		}
	}
	arenaLimit = 1 // more entries than indices
	if err := ListDir(dir, l); !errors.Is(err, ErrTooLarge) {
		t.Errorf("entry count: %v", err)
	}
}

// A service has the scratch of its workers ready, with path buffers that fit
// any path: listings and stats that overlap for the first time allocate no
// 264 KiB buffer, and none grows its path (D27: the Pilot bench gate met both
// in a measured window).
func TestServicePrewarmsScratch(t *testing.T) {
	scratchMu.Lock()
	scratchFree = nil
	scratchMu.Unlock()
	prewarmScratch(numWorkers + 1)
	n := numWorkers + 1
	long, name := strings.Repeat("d", scratchPath-1), strings.Repeat("n", maxName)
	got := make([]*bulkScratch, n)
	if a := testing.AllocsPerRun(10, func() {
		for i := range got {
			got[i] = getScratch()
		}
		for _, s := range got {
			s.path = append(append(s.path[:0], long...), 0)
			s.link = append(append(append(append(s.link[:0], long...), '/'), name...), 0)
			putScratch(s)
		}
	}); a != 0 {
		t.Errorf("%d scratches used at once allocate %v times after prewarmScratch", n, a)
	}
}
