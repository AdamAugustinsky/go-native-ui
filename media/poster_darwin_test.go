//go:build darwin

package media

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	}{
		{"default", plain, 0, 160, 96},
		{"scaled", plain, 80, 80, 48},
		{"turned", turned, 80, 48, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := requestPoster(t, tc.path, tc.max)
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
		})
	}
}

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
