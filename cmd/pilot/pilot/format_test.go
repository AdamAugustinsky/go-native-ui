package pilot

import (
	"testing"
	"time"

	"gonativeui/ui"
)

func TestFormats(t *testing.T) {
	c := ui.New(nil)
	var in ui.Input
	c.Begin(&in, 100, 100, 1, 1.0/60)
	size := func(n int64) string { f := c.Fmt(); appendSize(f, n); return f.String() }
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"}, {999, "999 B"}, {1000, "1.0 KB"}, {12_345, "12.3 KB"}, {99_949, "99.9 KB"},
		{123_456, "123 KB"}, {999_499, "999 KB"}, {1_500_000, "1.5 MB"}, {85_330_000, "85.3 MB"},
		{1_203_445_800, "1.2 GB"}, {4_000_000_000_000, "4.0 TB"},
	} {
		if got := size(tc.n); got != tc.want {
			t.Errorf("appendSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
	count := func(n int) string { f := c.Fmt(); appendCount(f, n); return f.String() }
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", 1000000: "1,000,000", 100005: "100,005"} {
		if got := count(n); got != want {
			t.Errorf("appendCount(%d) = %q, want %q", n, got, want)
		}
	}
	date := func(ns int64, loc *time.Location) string { f := c.Fmt(); appendDate(f, ns, loc); return f.String() }
	ts := time.Date(2026, 10, 17, 14, 3, 59, 0, time.UTC).UnixNano()
	if got := date(ts, time.UTC); got != "2026-10-17 14:03" {
		t.Errorf("date = %q", got)
	}
	if got := date(ts, time.FixedZone("x", 2*3600)); got != "2026-10-17 16:03" {
		t.Errorf("date in +2 = %q", got)
	}
}

func TestFormatsDoNotAllocate(t *testing.T) {
	c := ui.New(nil)
	var in ui.Input
	c.Begin(&in, 100, 100, 1, 1.0/60)
	ts := time.Date(2026, 10, 17, 14, 3, 0, 0, time.UTC).UnixNano()
	c.Fmt() // grow the arena
	if n := testing.AllocsPerRun(200, func() {
		c.Begin(&in, 100, 100, 1, 1.0/60)
		f := c.Fmt()
		appendSize(f, 12_345_678)
		appendDate(f, ts, time.UTC)
		appendCount(f, 1_234_567)
	}); n != 0 {
		t.Errorf("formatting allocates %v times", n)
	}
}

func TestNameSetAndHash(t *testing.T) {
	var s nameSet
	s.reset(3)
	for _, n := range []string{"a", "bb", "ccc"} {
		s.add(nameHash([]byte(n)))
	}
	s.add(nameHash([]byte("a")))
	if s.n != 3 || !s.has(nameHash([]byte("bb"))) || s.has(nameHash([]byte("d"))) {
		t.Errorf("set: %d entries", s.n)
	}
}
