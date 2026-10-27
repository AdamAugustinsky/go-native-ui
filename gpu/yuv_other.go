//go:build !darwin

package gpu

import (
	"errors"
	"fmt"
	"runtime"
)

var errYUV = fmt.Errorf("gpu: YUV textures on %s: %w", runtime.GOOS, errors.ErrUnsupported)

// NewYUVTexture reports ErrUnsupported here (YUV textures are macOS-only for
// now): the caller converts its frames to RGBA and uploads those.
func (r *Renderer) NewYUVTexture(f YUVFormat, w, h int, sp YUVSpace) (TextureID, error) {
	return 0, errYUV
}

// StageYUV does nothing here: no YUV texture exists (NewYUVTexture).
func (r *Renderer) StageYUV(t TextureID, im *YUVImage) {}

// ExternalCounts is 0, 0 here: nothing is imported.
func (r *Renderer) ExternalCounts() (pending, leased int) { return 0, 0 }

// Abandon does nothing here: nothing is imported.
func (r *Renderer) Abandon(cmdBuf uintptr) {}

// CollectLeases does nothing here: nothing is imported.
func (r *Renderer) CollectLeases() {}

// Collect does nothing here: nothing is imported, and freed textures go
// with the frames that follow.
func (r *Renderer) Collect() {}
