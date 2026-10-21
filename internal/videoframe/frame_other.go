//go:build !darwin

// Package videoframe extracts a local video's first frame without a player.
package videoframe

import "errors"

// Read is unsupported outside macOS.
func Read(path string, size func(int, int) (int, error)) ([]byte, int, int, error) {
	return nil, 0, 0, errors.ErrUnsupported
}
