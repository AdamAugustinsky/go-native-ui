//go:build darwin

package fs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opening a path that does not exist fails without launching anything, from
// a goroutine that is not the main thread, and is reported through Poll.
func TestOpenMissingDarwin(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if err := Open(missing); !os.IsNotExist(err) {
		t.Fatalf("Open(missing) = %v", err)
	}
	woke := make(chan struct{}, 1)
	s := NewService(func() {
		select {
		case woke <- struct{}{}:
		default:
		}
	})
	defer s.Close()
	s.Open(4, missing)
	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("no wake")
	}
	r := s.Poll(nil)
	if len(r) != 1 || r[0].Kind != ResultOpenError || r[0].ID != 4 || !os.IsNotExist(r[0].Err) {
		t.Fatalf("%+v", r)
	}
}
