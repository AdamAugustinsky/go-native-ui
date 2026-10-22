package icon

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/platform"
	"gonativeui/ui"
)

type plainUploader struct{ uploads int }

func (*plainUploader) NewTexture(gpu.TextureKind, int, int) gpu.TextureID { return 1 }
func (u *plainUploader) Upload(gpu.TextureID, int, int, int, int, []byte, int) {
	u.uploads++
}

type capableUploader struct {
	plainUploader
	stages, frees int
}

func (u *capableUploader) Stage(gpu.TextureID, int, int, int, int, []byte, int) {
	u.stages++
}
func (u *capableUploader) FreeTexture(gpu.TextureID) { u.frees++ }

func TestAttachChangesCapabilities(t *testing.T) {
	if os.Getenv("GNUI_ICON_ALLOC_CHILD") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAttachChangesCapabilities$")
		cmd.Env = append(os.Environ(), "GNUI_ICON_ALLOC_CHILD=1", "DBUS_SESSION_BUS_ADDRESS=disabled:")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	if runtime.GOOS == "linux" { // settle the init-time portal before counting
		platform.SystemAppearance()
	}
	a := newAtlas(nil, nil)
	st, plain := &capableUploader{}, &plainUploader{}
	for _, up := range []ui.Uploader{st, plain, st} {
		a.Attach(up)
		stages, uploads, frees := st.stages, plain.uploads, st.frees
		a.dirty = [4]int{0, 0, 1, 1}
		a.upload()
		a.retire(a.tex)
		a.frame++
		if len(a.retired) > 0 {
			a.reap()
		}
		if up == st && (st.stages != stages+1 || st.frees != frees+1) || up == plain && (plain.uploads != uploads+1 || st.stages != stages || st.frees != frees) {
			t.Fatalf("wrong upload/free path after binding %T", up)
		}
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 50000 {
		a.dirty = [4]int{0, 0, 1, 1}
		a.upload()
		a.retire(a.tex)
		a.frame++
		a.reap()
	}
	runtime.ReadMemStats(&after)
	if n := after.Mallocs - before.Mallocs; n != 0 {
		t.Fatalf("%d allocations in icon upload/retirement", n)
	}
}
