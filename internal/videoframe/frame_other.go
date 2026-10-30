//go:build !darwin

// Package videoframe extracts a local video's first frame without a player.
package videoframe

import "errors"

// ReadInto is unsupported outside macOS.
func ReadInto(path string, size func(int, int) (int, error), dst func(int, int) ([]byte, int, error)) (int, int, error) {
	return 0, 0, errors.ErrUnsupported
}
