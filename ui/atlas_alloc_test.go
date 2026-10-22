package ui

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	"gonativeui/gpu"
)

func TestAtlasSteadyFramesAllocateNothing(t *testing.T) {
	// A fresh process prevents another test from warming this assertion site.
	if os.Getenv("GNUI_ATLAS_ALLOC_CHILD") == "" {
		// Repeat independently: binding must not get lucky and warm the old
		// per-frame assertion's cache before we start counting.
		for range 3 {
			cmd := exec.Command(os.Args[0], "-test.run=^TestAtlasSteadyFramesAllocateNothing$")
			cmd.Env = append(os.Environ(), "GNUI_ATLAS_ALLOC_CHILD=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
		}
		return
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	c := New(newFake())
	c.AtlasSync(&stagingUploader{texUploader: texUploader{uploads: map[gpu.TextureID]int{}}})
	var in Input
	var list gpu.DrawList
	frame := func() { c.Begin(&in, 100, 100, 1, .016); c.End(&list) }
	for range 8 {
		frame()
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 50000 { // far beyond the runtime's ~1/1024 cache-build sampling
		frame()
	}
	runtime.ReadMemStats(&after)
	if n := after.Mallocs - before.Mallocs; n != 0 {
		t.Fatalf("%d allocations / %d bytes in steady frames", n, after.TotalAlloc-before.TotalAlloc)
	}
}

func TestAtlasUploaderCapabilityChanges(t *testing.T) {
	for _, shared := range []bool{false, true} {
		sh := &pagedShaper{fakeShaper: newFake(), pages: 1}
		c := New(sh)
		st := &stagingUploader{texUploader: texUploader{uploads: map[gpu.TextureID]int{}}}
		plain := &texUploader{uploads: map[gpu.TextureID]int{}}
		for _, up := range []Uploader{st, plain, st} {
			sh.dirty[0] = true
			if shared {
				c.ShareAtlas(NewSharedAtlas(up))
			} else {
				c.AtlasSync(up)
			}
			stages, uploads := st.stages, plain.total()
			sh.dirty[0] = true
			c.syncAtlas()
			if up == st && st.stages != stages+1 || up == plain && plain.total() != uploads+1 || up == plain && st.stages != stages {
				t.Fatalf("shared=%v: wrong upload path after binding %T", shared, up)
			}
		}
	}
}
