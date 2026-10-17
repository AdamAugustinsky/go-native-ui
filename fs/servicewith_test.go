package fs

import (
	"errors"
	"testing"
	"time"
)

// NewServiceWith serves caller-made listings through the normal pool and ring
// and has no OS watcher.
func TestNewServiceWith(t *testing.T) {
	woke := make(chan struct{}, 8)
	s := NewServiceWith(func() {
		select {
		case woke <- struct{}{}:
		default:
		}
	}, func(path string, l *Listing) error {
		for _, n := range []string{"b", "a", path} {
			l.Entries = append(l.Entries, Entry{NameOff: uint32(len(l.Names)), NameLen: uint16(len(n))})
			l.Names = append(l.Names, n...)
		}
		return nil
	})
	defer s.Close()

	req := s.List(7, "zed", SortName, false)
	var res []Result
	deadline := time.After(5 * time.Second)
	for len(res) == 0 {
		select {
		case <-woke:
			res = s.Poll(res[:0])
		case <-deadline:
			t.Fatal("no result")
		}
	}
	r := res[0]
	if r.Kind != ResultListing || r.ID != 7 || r.Req != req || r.Listing == nil || len(r.Listing.Entries) != 3 {
		t.Fatalf("result %+v", r)
	}
	if got := string(r.Listing.Name(int(r.Listing.Order[0]))); got != "a" {
		t.Errorf("sorted first = %q, want a", got)
	}
	s.Release(r.Listing, r.Lease)

	s.Watch(7, "zed")
	res = res[:0]
	for len(res) == 0 {
		select {
		case <-woke:
			res = s.Poll(res[:0])
		case <-deadline:
			t.Fatal("no watch error")
		}
	}
	if res[0].Kind != ResultWatchError || !errors.Is(res[0].Err, ErrUnsupported) {
		t.Errorf("watch: %+v", res[0])
	}
}
