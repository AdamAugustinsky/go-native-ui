package media

import (
	"errors"
	"fmt"
	"runtime"
)

// VideoState is how far a Video's file has loaded.
type VideoState uint8

const (
	// VideoLoading: the file is being read; Size and Duration are 0 and no
	// frame shows yet.
	VideoLoading VideoState = iota
	// VideoReady: the size, duration and poster frame are known.
	VideoReady
	// VideoFailed: the file cannot be played (Err says why).
	VideoFailed
)

// VideoOptions configures OpenVideo. The zero value plays once, with sound
// at full volume.
type VideoOptions struct {
	// Loop starts the video again from the start when it ends.
	Loop bool
	// Muted silences its sound.
	Muted bool
	// Volume is the sound's volume, 0 to 1 (0 is 1: full volume; mute with
	// Muted).
	Volume float32
	// PosterSize is the longest side, in pixels, of the poster frame shown
	// until the first frame plays (default 1280).
	PosterSize int
}

// ErrVideoUnsupported is returned by OpenVideo and LoadVideoPoster where video is not
// implemented yet (Windows and Linux).
var ErrVideoUnsupported = fmt.Errorf("media: video on %s: %w", runtime.GOOS, errors.ErrUnsupported)
