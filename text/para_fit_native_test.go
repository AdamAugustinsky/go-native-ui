//go:build darwin || windows

package text

import "testing"

// The basic layout over the platform's shaper (CoreText on macOS, where
// Layout itself is the typesetter's, and DirectWrite).
func TestBasicParaFit(t *testing.T) {
	s := newSys(t)
	checkParaFit(t, s, uiFont(s))
}
