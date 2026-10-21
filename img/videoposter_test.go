package img

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVideoPosterErrors(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	text := filepath.Join(t.TempDir(), "text.mp4")
	if err := os.WriteFile(text, []byte("not a video"), 0600); err != nil {
		t.Fatal(err)
	}
	for i, path := range []string{text, text + "-missing"} {
		h, _ := c.Request(Key{Hash: uint64(i + 1), W: 80}, VideoPoster(path))
		settle(t, c, nil)
		if c.State(h) != Failed || c.Err(h) == nil {
			t.Fatalf("state %v: %v", c.State(h), c.Err(h))
		}
		if runtime.GOOS != "darwin" && !errors.Is(c.Err(h), errors.ErrUnsupported) {
			t.Fatalf("unsupported: %v", c.Err(h))
		}
	}
}
