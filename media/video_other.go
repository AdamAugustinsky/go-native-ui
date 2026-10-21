//go:build !darwin

package media

import (
	"time"

	"gonativeui/gpu"
)

// Video is a video file playing into the window's frames. Not implemented
// here yet: OpenVideo reports ErrVideoUnsupported, and the app shows the
// file's thumbnail instead.
type Video struct{}

// OpenVideo reports ErrVideoUnsupported here.
func OpenVideo(path string, o VideoOptions) (*Video, error) { return nil, ErrVideoUnsupported }

// ID is 0 here.
func (v *Video) ID() uint64 { return 0 }

// State is VideoFailed here.
func (v *Video) State() VideoState { return VideoFailed }

// Err is ErrVideoUnsupported here.
func (v *Video) Err() error { return ErrVideoUnsupported }

// Size is 0, 0 here.
func (v *Video) Size() (w, h int) { return 0, 0 }

// Duration is 0 here.
func (v *Video) Duration() time.Duration { return 0 }

// Position is 0 here.
func (v *Video) Position() time.Duration { return 0 }

// Play does nothing here.
func (v *Video) Play() {}

// Pause does nothing here.
func (v *Video) Pause() {}

// Playing is false here.
func (v *Video) Playing() bool { return false }

// Ended is false here.
func (v *Video) Ended() bool { return false }

// Seek does nothing here.
func (v *Video) Seek(d time.Duration) {}

// SetMuted does nothing here.
func (v *Video) SetMuted(m bool) {}

// Muted is false here.
func (v *Video) Muted() bool { return false }

// SetVolume does nothing here.
func (v *Video) SetVolume(x float32) {}

// Volume is 0 here.
func (v *Video) Volume() float32 { return 0 }

// SetLoop does nothing here.
func (v *Video) SetLoop(l bool) {}

// Loop is false here.
func (v *Video) Loop() bool { return false }

// SetRate does nothing here.
func (v *Video) SetRate(r float32) {}

// Rate is 1 here.
func (v *Video) Rate() float32 { return 1 }

// Frame is nothing here.
func (v *Video) Frame(r *gpu.Renderer, now int64) (tex gpu.TextureID, w, h int, next int64) {
	return 0, 0, 0, 0
}

// Close does nothing here.
func (v *Video) Close() {}

// VideoCounts is 0, 0 here.
func VideoCounts() (videos, pixelBuffers int) { return 0, 0 }
