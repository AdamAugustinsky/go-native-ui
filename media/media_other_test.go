//go:build !darwin

package media

import (
	"errors"
	"testing"
	"time"
)

func runLoopOnce() {}

func TestUnsupported(t *testing.T) {
	if err := LoadVideoPoster("clip.mp4", 80, func([]byte, int, int, error) {
		t.Error("unsupported poster called done")
	}); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("LoadVideoPoster = %v; want ErrUnsupported", err)
	}
	f := Format{48000, 1, Int16}
	if p, err := NewPlayer(sine(f, 10*time.Millisecond, 440), f); !errors.Is(err, errors.ErrUnsupported) || p != nil {
		t.Errorf("NewPlayer = %v, %v; want ErrUnsupported", p, err)
	}
	if p, err := OpenPlayer("/System/Library/Sounds/Tink.aiff"); !errors.Is(err, errors.ErrUnsupported) || p != nil {
		t.Errorf("OpenPlayer = %v, %v; want ErrUnsupported", p, err)
	}
	r := NewRecorder(RecorderOptions{})
	if err := r.Start(); !errors.Is(err, errors.ErrUnsupported) || r.State() != Idle {
		t.Errorf("Start = %v (%v); want ErrUnsupported", err, r.State())
	}
	r.Close()
}
