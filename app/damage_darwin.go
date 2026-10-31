package app

import (
	"fmt"
	"os"

	"gonativeui/gpu"
)

// damage is a window's partial frames (D92): each frame is drawn into a back
// buffer of the window's own, only where it changed since the previous one,
// and copied to the drawable; a frame that changed nothing is not presented.
type damage struct {
	tr     gpu.DamageTracker
	cur    gpu.Damage // the frame's (a field: EncodeDamaged keeps a pointer to it while it encodes)
	back   uintptr    // the back buffer, 0 while there is none
	w, h   int        // its size
	epoch  uint32     // the renderer's epoch it was made in
	serial uint64     // the texture writes seen so far (Renderer.TextureWrites)
	writes []gpu.TextureWrite

	read     bool // the environment is read
	off      bool // GNUI_DAMAGE=0: whole frames, as before D92
	check    bool // GNUI_DAMAGE_CHECK=1: compare each partial frame with a whole one
	ref, got []byte

	// counts, for tests and the debug log
	Frames, Partial, Skipped, Mismatches int
}

// encodeFrame encodes the frame's draw list into a.fr. It returns false when
// the frame changed nothing on screen and was ended without a present
// (gpu.Surface.SkipFrame): EndFrame must not follow then.
func (a *App) encodeFrame() bool {
	d, f := &a.dmg, &a.fr
	if !d.read {
		d.read = true
		d.off = os.Getenv("GNUI_DAMAGE") == "0"
		d.check = os.Getenv("GNUI_DAMAGE_CHECK") == "1"
	}
	d.Frames++
	// A live resize presents one frame at each size: a back buffer would be
	// made and drawn whole for each, then copied, for nothing.
	// So does a drawable of another size than the surface's (one taken just
	// before a resize): the copy needs the back buffer's size. The back
	// buffer keeps the previous frame, which the tracker compares with next.
	if tw, th := gpu.TextureSize(f.Target); tw != f.W || th != f.H {
		a.R.Encode(f.CmdBuf, f.Target, f.W, f.H, a.cfg.Clear, &a.list)
		return true
	}
	if d.off || a.Surf.Live() {
		a.releaseDamage()
		a.R.Encode(f.CmdBuf, f.Target, f.W, f.H, a.cfg.Clear, &a.list)
		return true
	}
	epoch := a.R.Epoch()
	if d.back != 0 && (d.w != f.W || d.h != f.H || d.epoch != epoch) {
		a.releaseDamage() // a new size, or a new device (whose textures these are not)
	}
	if d.back == 0 {
		if d.back = a.R.NewBackBuffer(f.W, f.H); d.back == 0 {
			a.R.Encode(f.CmdBuf, f.Target, f.W, f.H, a.cfg.Clear, &a.list)
			return true
		}
		d.w, d.h, d.epoch = f.W, f.H, epoch
		d.tr.Reset()
	}
	var complete bool
	d.writes, d.serial, complete = a.R.TextureWrites(d.serial, d.writes[:0])
	if !complete {
		d.tr.Reset()
	}
	d.cur = d.tr.Frame(&a.list, f.W, f.H, a.cfg.Clear, epoch, d.writes)
	if !d.cur.Full {
		d.Partial++
	}
	if d.cur.Empty() && a.Surf.SkipFrame(f) {
		d.Skipped++
		return false
	}
	a.R.EncodeDamaged(f.CmdBuf, f.Target, d.back, f.W, f.H, a.cfg.Clear, &a.list, &d.cur)
	return true
}

// checkDamage, in the GNUI_DAMAGE_CHECK mode, compares the back buffer of a
// partial frame, once drawn, with the frame drawn whole. A difference is
// logged and heals: the next frame is drawn whole.
func (a *App) checkDamage() {
	d := &a.dmg
	if !d.check || d.back == 0 || d.cur.Full {
		return
	}
	if n := d.w * d.h * 4; len(d.ref) != n {
		d.ref, d.got = make([]byte, n), make([]byte, n)
	}
	if x, y, ok := a.R.CheckBackBuffer(d.back, d.w, d.h, a.cfg.Clear, &a.list, d.ref, d.got); !ok {
		d.Mismatches++
		d.tr.Reset()
		i := (y*d.w + x) * 4
		fmt.Fprintf(os.Stderr, "gnui damage: frame %d differs from a whole frame at %d,%d (whole %v, partial %v), damage %+v\n",
			d.Frames, x, y, d.ref[i:i+4], d.got[i:i+4], d.cur)
	}
}

// releaseDamage frees the back buffer (Metal keeps it for the frames in
// flight); the next partial frame makes a new one and is drawn whole.
func (a *App) releaseDamage() {
	d := &a.dmg
	if d.back != 0 {
		a.R.ReleaseBackBuffer(d.back)
		d.back = 0
	}
	d.ref, d.got = nil, nil
}
