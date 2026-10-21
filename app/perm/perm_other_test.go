//go:build !darwin

package perm

import (
	"errors"
	"testing"
)

func TestUnsupported(t *testing.T) {
	for _, k := range []Kind{Microphone, Camera} {
		if _, err := Status(k); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("Status(%v) = %v, want ErrUnsupported", k, err)
		}
		if _, err := Request(k); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("Request(%v) = %v, want ErrUnsupported", k, err)
		}
		if err := OpenSettings(k); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("OpenSettings(%v) = %v, want ErrUnsupported", k, err)
		}
	}
}
