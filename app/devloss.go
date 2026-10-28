package app

import (
	"fmt"
	"os"

	"gonativeui/gpu"
)

// GPU device loss (gpu/devloss.go): a Direct3D 11 timeout detection and
// recovery or a driver update, a lost OpenGL context, a removed Metal device.
// A frame that finds the device lost is not shown. Between frames the app
// makes the surfaces and the renderer work on a new device, uploads what the
// textures lost, and owes every window a repaint. The windows do not notice.
//
// What the renderer's textures held comes back from the owners' memory:
//
//   - the text atlas: ui.Context.AtlasRestore, here;
//   - the icon atlas and image pages: when the renderer's Epoch moved, at the
//     next Prepare (icon.Atlas) or Poll (img.Cache);
//   - widget masks (gpu.PathMask): at the next Mask.
//
// Textures an owner made from pixels it did not keep (a paused video's
// poster) are blank until it makes them again.

// retryLostNs is how long after a failed recovery the next one is tried.
const retryLostNs = int64(1e9)

// deviceLost reports whether the GPU device of this window, or of the app's
// renderer, was lost and not yet recovered.
func (a *App) deviceLost() bool {
	return (a.Surf != nil && a.Surf.DeviceLost()) || (a.R != nil && a.R.DeviceLost())
}

// recoverFromLoss recovers from a lost device now, if the last attempt that
// failed was long enough ago, and owes the windows a repaint either way.
func (a *App) recoverFromLoss(now int64) {
	s := a.appObject
	if s.lostAt == 0 || now-s.lostAt >= retryLostNs {
		if err := a.recoverDevice(); err != nil {
			s.lostAt = now
			fmt.Fprintf(os.Stderr, "app: GPU device lost; recovery failed: %v\n", err)
		} else {
			s.lostAt = 0
			fmt.Fprintln(os.Stderr, "app: GPU device lost; recovered")
		}
	}
	for _, w := range a.drawing() {
		w.p.failed(now)
		w.req.RequestFrame()
		w.p.requested(now)
	}
}

// drawing returns the windows that draw with the app's renderer: its open
// windows, and a if it is still opening (or has no window: an offscreen
// render, which is the only one then).
func (a *App) drawing() []*App {
	ws := make([]*App, 0, len(a.wins)+1)
	found := false
	for _, w := range a.wins {
		found = found || w == a
		if w.Win != nil {
			ws = append(ws, w)
		}
	}
	if !found && a.Win != nil {
		ws = append(ws, a)
	}
	return ws
}

// recoverDevice moves the windows' surfaces and the renderer to a new device
// and uploads the text atlas again. On an error whatever was not moved is
// still lost, and a later call tries again.
func (a *App) recoverDevice() error {
	s := a.appObject
	if a.R == nil {
		return nil
	}
	ws := a.drawing()
	if len(ws) == 0 { // an offscreen render: the renderer owns its device
		if err := a.R.Rebuild(0, 0); err != nil {
			return err
		}
		a.UI.AtlasRestore()
		return nil
	}
	var nd *gpu.Device
	if s.dev != nil { // macOS: one device for the windows and the renderer
		d, err := gpu.NewDevice()
		if err != nil {
			return err
		}
		nd = d
	}
	for _, w := range ws {
		if err := w.Surf.Recover(nd); err != nil {
			nd.Release()
			return fmt.Errorf("surface: %w", err)
		}
		if nd == nil { // the frame source belongs to the swap chain
			w.Win.SetFrameSource(w.Surf.FrameSource())
		}
	}
	surf := ws[0].Surf
	if err := a.R.Rebuild(surf.Device(), surf.Queue()); err != nil {
		nd.Release()
		return fmt.Errorf("renderer: %w", err)
	}
	if nd != nil {
		s.dev.Release()
		s.dev = nd
	}
	// The windows share the atlas's textures (D30), so one restores them.
	ws[0].UI.AtlasRestore()
	for _, w := range ws[1:] {
		if s.atlas == nil { // a context with textures of its own
			w.UI.AtlasRestore()
		}
	}
	return nil
}
