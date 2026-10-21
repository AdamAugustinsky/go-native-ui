//go:build darwin

package media

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/img"
)

type posterResult struct {
	pix  []byte
	w, h int
	err  error
}

func requestPoster(t *testing.T, path string, maxSide int) posterResult {
	t.Helper()
	result := make(chan posterResult)
	var err error
	var elapsed time.Duration
	onMain(func() {
		start := time.Now()
		err = LoadVideoPoster(path, maxSide, func(pix []byte, w, h int, err error) {
			// An unbuffered handoff cannot finish until the call has returned.
			result <- posterResult{pix, w, h, err}
		})
		elapsed = time.Since(start)
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed >= 5*time.Millisecond {
		t.Errorf("LoadVideoPoster blocked for %v", elapsed)
	}
	t.Logf("LoadVideoPoster returned in %v", elapsed)
	select {
	case r := <-result:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("poster result did not arrive")
		return posterResult{}
	}
}

func TestLoadVideoPoster(t *testing.T) {
	plain, turned := testClips(t, onMain)
	for _, tc := range []struct {
		name, path string
		max, w, h  int
		boxW, boxH int32
		flags      img.Flags
	}{
		{"default", plain, 0, 160, 96, 0, 0, 0},
		{"scaled", plain, 80, 80, 48, 80, 80, 0},
		{"turned", turned, 80, 48, 80, 80, 80, 0},
		{"width", plain, 80, 80, 48, 80, 0, 0},
		{"turned-width", turned, 80, 48, 80, 48, 0, 0},
		{"cover", plain, 100, 100, 60, 60, 60, img.Cover},
		{"turned-cover", turned, 100, 60, 100, 60, 60, img.Cover},
		{"no-upscale", plain, 640, 160, 96, 640, 0, img.Animate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := requestPoster(t, tc.path, tc.max)
			t.Logf("poster sha256: %x", sha256.Sum256(r.pix))
			if r.err != nil || r.w != tc.w || r.h != tc.h || len(r.pix) != r.w*r.h*4 {
				t.Fatalf("poster: %d×%d, %d bytes, %v", r.w, r.h, len(r.pix), r.err)
			}
			var v *Video
			var err error
			onMain(func() { v, err = OpenVideo(tc.path, VideoOptions{Muted: true, PosterSize: tc.max}) })
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { onMain(v.Close) })
			for start := time.Now(); v.State() == VideoLoading && time.Since(start) < 10*time.Second; {
				onMain(func() { pump(time.Millisecond) })
			}
			if v.State() != VideoReady {
				t.Fatalf("Video state %v: %v", v.State(), v.Err())
			}
			// Compare owned CPU bytes before Frame uploads and discards Video's
			// poster. Both paths must be byte-for-byte identical (no tolerance).
			if r.w != v.posW || r.h != v.posH || !bytes.Equal(r.pix, v.poster) {
				t.Fatal("poster differs from Video's poster")
			}
			g := new(posterGPU)
			c := img.NewCache(img.Options{Renderer: g, Wake: func() {}})
			t.Cleanup(func() { c.Close(time.Second) })
			key := img.Key{Hash: 1, W: tc.boxW, H: tc.boxH, Flags: tc.flags}
			h, _ := c.Request(key, img.VideoPoster(tc.path))
			for end := time.Now().Add(10 * time.Second); c.State(h) == img.Pending && time.Now().Before(end); {
				c.Poll()
				c.Request(key, img.VideoPoster(tc.path))
				time.Sleep(time.Millisecond)
			}
			info, _ := c.Info(h)
			if c.State(h) != img.Ready || info.W != r.w || info.H != r.h {
				t.Fatalf("cache: %+v, %v", info, c.Err(h))
			}
			_, uv, _ := c.Use(h)
			for y := 0; y < r.h; y++ {
				o := ((int(uv[1])+y)*g.w + int(uv[0])) * 4
				if !bytes.Equal(g.pix[o:o+r.w*4], r.pix[y*r.w*4:(y+1)*r.w*4]) {
					t.Fatal("cache pixels differ from both media posters")
				}
			}
			if n := testing.AllocsPerRun(100, func() { c.Poll(); c.Request(key, img.VideoPoster(tc.path)); c.Use(h) }); n != 0 {
				t.Fatalf("ready allocations: %g", n)
			}
		})
	}
}

// Capture actual uploads, including atlas padding, without opening a window.
type posterGPU struct {
	pix  []byte
	w, h int
}

func (g *posterGPU) NewTexture(_ gpu.TextureKind, w, h int) gpu.TextureID {
	g.w, g.h = w, h
	g.pix = make([]byte, w*h*4)
	return 1
}
func (g *posterGPU) Upload(_ gpu.TextureID, x, y, w, h int, p []byte, stride int) {
	for row := 0; row < h; row++ {
		copy(g.pix[((y+row)*g.w+x)*4:((y+row)*g.w+x+w)*4], p[row*stride:row*stride+w*4])
	}
}
func (g *posterGPU) Stage(t gpu.TextureID, x, y, w, h int, p []byte, stride int) {
	g.Upload(t, x, y, w, h, p, stride)
}
func (*posterGPU) FreeTexture(gpu.TextureID) {}

func TestLoadVideoPosterErrors(t *testing.T) {
	var err error
	onMain(func() { err = LoadVideoPoster("", 80, nil) })
	if err != ErrInvalid {
		t.Fatalf("nil callback: %v, want ErrInvalid", err)
	}
	dir := t.TempDir()
	text := filepath.Join(dir, "not-video.txt")
	if err := os.WriteFile(text, []byte("not a video"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "missing.mp4"), text} {
		r := requestPoster(t, path, 80)
		if r.err == nil || r.pix != nil || r.w != 0 || r.h != 0 {
			t.Errorf("%s: got %d×%d, %d bytes, %v; want an error only", path, r.w, r.h, len(r.pix), r.err)
		}
	}
}
