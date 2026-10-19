//go:build darwin

package main

import (
	"os"
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// AppKit and the window live on the main thread: the tests run on another
// goroutine and hand their AppKit work to onMain.

var mainQ = make(chan func())

func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	done := make(chan int, 1)
	go func() { done <- m.Run() }()
	for {
		select {
		case f := <-mainQ:
			f()
		case code := <-done:
			os.Exit(code)
		}
	}
}

func onMain(f func()) {
	d := make(chan struct{})
	mainQ <- func() { defer close(d); f() }
	<-d
}

// A frame whose material regions changed is presented in step with them: the GPU surface presents it with the transaction that applies the
// staged changes, with implicit animations off; frames that change nothing
// present as usual.
func TestShellPresentsInStep(t *testing.T) {
	type result struct {
		err                    error
		noFrame                bool
		synced                 [3]int
		counts                 [4]platform.ShellCounts
		pendingAfter, layerOff bool
	}
	var r result
	onMain(func() {
		w, err := platform.Open(platform.Options{Title: "in step", Width: 400, Height: 300, Hidden: true, Materials: true})
		if err != nil {
			r.err = err
			return
		}
		defer w.Close()
		pw, ph, s := w.Size()
		surf, err := gpu.NewSurface(w.Native(), pw, ph, s)
		if err != nil {
			r.err = err
			return
		}
		defer surf.Destroy()
		present := func() bool { return surf.Present(0, 0, 0, 0) }
		if !present() { // the first frame
			r.noFrame = true
			return
		}
		r.counts[0] = platform.ShellObjects()
		w.SetMaterials([]platform.MaterialRegion{{Area: platform.AreaSidebar, Rect: [4]float32{0, 0, 200 * s, 300 * s}}})
		present()
		r.synced[0], r.counts[1] = surf.SyncedPresents, platform.ShellObjects()
		r.pendingAfter = platform.ShellPending()
		present() // nothing changed
		r.synced[1], r.counts[2] = surf.SyncedPresents, platform.ShellObjects()
		w.SetMaterials([]platform.MaterialRegion{{Area: platform.AreaSidebar, Rect: [4]float32{0, 0, 240 * s, 300 * s}}})
		present()
		r.synced[2], r.counts[3] = surf.SyncedPresents, platform.ShellObjects()
		r.layerOff = !layerPresentsWithTransaction(surf)
	})
	switch {
	case r.err != nil:
		t.Skipf("cannot open a window and a Metal surface here: %v", r.err)
	case r.noFrame:
		t.Skip("no drawable for a hidden window here")
	}
	if r.synced[0] != 1 || r.counts[1].Applies-r.counts[0].Applies != 1 || r.counts[1].Synced-r.counts[0].Synced != 1 || r.pendingAfter {
		t.Errorf("frame with new materials: %d synced presents, %d applies (%d in step), still pending %v: want 1, 1, 1, false",
			r.synced[0], r.counts[1].Applies-r.counts[0].Applies, r.counts[1].Synced-r.counts[0].Synced, r.pendingAfter)
	}
	if r.synced[1] != 1 || r.counts[2].Applies != r.counts[1].Applies {
		t.Errorf("unchanged frame: %d synced presents, applies %d → %d: want no synced present and no apply", r.synced[1], r.counts[1].Applies, r.counts[2].Applies)
	}
	if r.synced[2] != 2 || r.counts[3].Synced-r.counts[2].Synced != 1 {
		t.Errorf("frame with a wider sidebar: %d synced presents, %d applies in step: want 2, 1", r.synced[2], r.counts[3].Synced-r.counts[2].Synced)
	}
	if !r.layerOff {
		t.Error("the layer still presents with the transaction after a synced frame")
	}
}

func layerPresentsWithTransaction(s *gpu.Surface) bool {
	return objc.SendBool(objc.ID(s.FrameSource()), objc.Sel("presentsWithTransaction"))
}
