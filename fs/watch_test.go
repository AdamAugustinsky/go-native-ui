package fs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Watch tests run on the real file system with the OS backend. Their
// timing limits are generous (the exit criterion is 250 ms; the measured
// latencies are logged), because CI runners are noisy.
const watchLimit = time.Second

type watchRig struct {
	t    *testing.T
	w    watcher
	wake chan struct{}
	buf  []Change
}

func newWatchRig(t *testing.T) *watchRig {
	r := &watchRig{t: t, wake: make(chan struct{}, 1)}
	w, err := newWatcher(func() {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	r.w = w
	t.Cleanup(w.close)
	return r
}

// quiet waits for the file system to settle and discards pending changes.
func (r *watchRig) quiet() {
	time.Sleep(400 * time.Millisecond)
	r.buf = r.w.drain(r.buf[:0])
	select {
	case <-r.wake:
	default:
	}
}

// wait returns how long after start a change (id, kind) arrived.
func (r *watchRig) wait(start time.Time, id uint32, kinds ...ChangeKind) time.Duration {
	r.t.Helper()
	deadline := time.After(watchLimit)
	for {
		r.buf = r.w.drain(r.buf[:0])
		for _, c := range r.buf {
			if c.ID != id {
				continue
			}
			for _, k := range kinds {
				if c.Kind == k {
					return time.Since(start)
				}
			}
		}
		select {
		case <-r.wake:
		case <-deadline:
			r.t.Fatalf("no change for id %d (kinds %v) within %v", id, kinds, watchLimit)
		}
	}
}

func TestWatcherChanges(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "existing"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "victim"), []byte("x"), 0o644)
	r := newWatchRig(t)
	if err := r.w.add(7, dir); err != nil {
		t.Fatal(err)
	}
	r.quiet()

	steps := []struct {
		name string
		do   func() error
	}{
		{"create", func() error { return os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hi"), 0o644) }},
		{"modify", func() error { return os.WriteFile(filepath.Join(dir, "existing"), []byte("changed contents"), 0o644) }},
		{"rename", func() error { return os.Rename(filepath.Join(dir, "new.txt"), filepath.Join(dir, "renamed.txt")) }},
		{"mkdir", func() error { return os.Mkdir(filepath.Join(dir, "sub"), 0o755) }},
		{"delete", func() error { return os.Remove(filepath.Join(dir, "victim")) }},
	}
	for _, s := range steps {
		start := time.Now()
		if err := s.do(); err != nil {
			t.Fatal(err)
		}
		d := r.wait(start, 7, ChangeDir, ChangeOverflow)
		t.Logf("%-6s → change after %v", s.name, d.Round(time.Millisecond))
		r.quiet()
	}
}

// A change made right after add returns must not be lost.
func TestWatcherImmediate(t *testing.T) {
	for i := 0; i < 5; i++ {
		dir := t.TempDir()
		r := newWatchRig(t)
		if err := r.w.add(1, dir); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		os.WriteFile(filepath.Join(dir, "now"), nil, 0o644)
		t.Logf("immediate change seen after %v", r.wait(start, 1, ChangeDir, ChangeOverflow).Round(time.Millisecond))
	}
}

func TestWatcherGone(t *testing.T) {
	for _, how := range []string{"remove", "rename"} {
		t.Run(how, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "watched")
			os.Mkdir(dir, 0o755)
			os.WriteFile(filepath.Join(dir, "f"), nil, 0o644)
			r := newWatchRig(t)
			if err := r.w.add(3, dir); err != nil {
				t.Fatal(err)
			}
			r.quiet()
			var err error
			start := time.Now()
			if how == "remove" {
				err = os.RemoveAll(dir)
			} else {
				err = os.Rename(dir, filepath.Join(parent, "moved"))
			}
			if err != nil {
				t.Fatal(err)
			}
			d := r.wait(start, 3, ChangeGone)
			t.Logf("%s → gone after %v", how, d.Round(time.Millisecond))
		})
	}
}

func TestWatcherTwoIDsAndRemove(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	r := newWatchRig(t)
	for id, d := range map[uint32]string{1: a, 2: a, 3: b} {
		if err := r.w.add(id, d); err != nil {
			t.Fatal(err)
		}
	}
	r.quiet()
	os.WriteFile(filepath.Join(a, "x"), nil, 0o644)
	r.wait(time.Now(), 1, ChangeDir, ChangeOverflow)
	// Both ids on the same directory hear about it (1 was drained above; 2 is still pending or arrives together).
	r.buf = r.w.drain(r.buf[:0])
	r.quiet()

	r.w.remove(1)
	r.w.remove(2)
	r.quiet()
	os.WriteFile(filepath.Join(a, "y"), nil, 0o644)
	time.Sleep(300 * time.Millisecond)
	r.buf = r.w.drain(r.buf[:0])
	for _, c := range r.buf {
		if c.ID == 1 || c.ID == 2 {
			t.Errorf("change %+v for a removed id", c)
		}
	}
	os.WriteFile(filepath.Join(b, "z"), nil, 0o644)
	r.wait(time.Now(), 3, ChangeDir, ChangeOverflow)

	if err := r.w.add(9, filepath.Join(a, "does-not-exist")); err == nil {
		t.Error("watching a missing directory succeeded")
	}
}

// Adding and removing watches while events fly must neither deadlock nor
// lose the watches that stay (run with -race).
func TestWatcherChurn(t *testing.T) {
	keep, churn := t.TempDir(), t.TempDir()
	r := newWatchRig(t)
	if err := r.w.add(1, keep); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			os.WriteFile(filepath.Join(churn, "f"), []byte{byte(i)}, 0o644)
			time.Sleep(2 * time.Millisecond)
		}
	}()
	t0 := time.Now()
	for i := 0; i < 20; i++ {
		if err := r.w.add(2, churn); err != nil {
			t.Fatal(err)
		}
		r.buf = r.w.drain(r.buf[:0])
		r.w.remove(2)
	}
	t.Logf("add+remove: %v each", (time.Since(t0) / 20).Round(10*time.Microsecond))
	close(stop)
	<-done
	r.quiet()
	os.WriteFile(filepath.Join(keep, "still"), nil, 0o644)
	r.wait(time.Now(), 1, ChangeDir, ChangeOverflow)
}

// The whole pipeline on the real file system: list, watch, change, and the
// refreshed listing arrives (this is exit criterion 4 for the UI side).
func TestServiceRealWatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "one"), []byte("1"), 0o644)
	wake := make(chan struct{}, 1)
	s := NewService(func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	defer s.Close()
	var buf []Result
	await := func(match func(Result) bool) Result {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			buf = s.Poll(buf[:0])
			for _, r := range buf {
				if match(r) {
					return r
				}
				s.Release(r.Listing)
			}
			select {
			case <-wake:
			case <-deadline:
				t.Fatal("timed out")
			}
		}
	}
	has := func(l *Listing, name string) bool {
		for i := range l.Entries {
			if string(l.Name(i)) == name {
				return true
			}
		}
		return false
	}

	s.List(1, dir, SortName, false)
	s.Watch(1, dir)
	r := await(func(r Result) bool { return r.Kind == ResultListing })
	if !has(r.Listing, "one") || len(r.Listing.Entries) != 1 {
		t.Fatalf("first listing: %d entries", len(r.Listing.Entries))
	}
	s.Release(r.Listing)
	time.Sleep(500 * time.Millisecond) // let the OS watch settle

	for _, step := range []struct {
		name  string
		do    func()
		check func(*Listing) bool
	}{
		{"create", func() { os.WriteFile(filepath.Join(dir, "two"), []byte("22"), 0o644) }, func(l *Listing) bool { return has(l, "two") }},
		{"rename", func() { os.Rename(filepath.Join(dir, "two"), filepath.Join(dir, "three")) }, func(l *Listing) bool { return has(l, "three") && !has(l, "two") }},
		{"delete", func() { os.Remove(filepath.Join(dir, "one")) }, func(l *Listing) bool { return !has(l, "one") }},
	} {
		start := time.Now()
		step.do()
		for {
			r := await(func(r Result) bool { return r.Kind == ResultListing })
			ok := r.Listing.Err == nil && step.check(r.Listing)
			refresh := r.Refresh
			s.Release(r.Listing)
			if ok {
				if !refresh {
					t.Error("watch relist not marked Refresh")
				}
				d := time.Since(start)
				t.Logf("%-6s → refreshed listing after %v", step.name, d.Round(time.Millisecond))
				if d > watchLimit {
					t.Errorf("%s took %v", step.name, d)
				}
				break
			}
		}
	}

	// The directory itself disappears.
	os.RemoveAll(dir)
	start := time.Now()
	for {
		r := await(func(r Result) bool { return r.Kind == ResultGone || r.Kind == ResultListing })
		if r.Kind == ResultListing {
			s.Release(r.Listing)
			continue
		}
		t.Logf("remove dir → gone after %v", time.Since(start).Round(time.Millisecond))
		break
	}
}
