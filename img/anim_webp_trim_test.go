//go:build !darwin

package img

import "testing"

// webpTrimSource adds x/image/webp's decoder (C6) to TestTrimThenRender's
// sources; macOS decodes WebP with ImageIO (TestImageIOTrim).
func webpTrimSource(t *testing.T, srcs map[string]frameSource) {
	w, err := openWebP(webpAnimation(32, 32, 3, 80, 0))
	if err != nil {
		t.Fatal(err)
	}
	srcs["webp"] = w
}
