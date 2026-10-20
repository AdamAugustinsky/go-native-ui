package locale

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNormalizeTag(t *testing.T) {
	for in, want := range map[string]string{
		"en_US.UTF-8": "en-US", "pt_BR@euro": "pt-BR", "de_DE.utf8@euro": "de-DE", "fr": "fr", "zh-Hans-CN": "zh-Hans-CN",
		"  es_419 ": "es-419", "C": "", "POSIX": "", "": "", "C.UTF-8": "",
	} {
		if got := normalizeTag(in); got != want {
			t.Errorf("normalizeTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegionOf(t *testing.T) {
	for in, want := range map[string]string{
		"en-US": "US", "pt-BR": "BR", "zh-Hans-CN": "CN", "es-419": "419", "fr": "", "zh-Hans": "", "": "", "sr-Latn-RS": "RS",
	} {
		if got := regionOf(in); got != want {
			t.Errorf("regionOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHour24FromPattern(t *testing.T) {
	for _, c := range []struct {
		p          string
		h24, known bool
	}{
		{"HH", true, true}, {"H", true, true}, {"HH:mm", true, true}, {"kk", true, true},
		{"h a", false, true}, {"h:mm a", false, true}, {"hh", false, true}, {"K a", false, true},
		{"'at' HH", true, true},   // the quoted word has no hour field
		{"'hour' HH", true, true}, // ... and its 'h' does not count
		{"'H'", false, false},     // only quoted text
		{"", false, false}, {"mm:ss", false, false},
		{"%T", true, true}, {"%H:%M:%S", true, true}, {"%R", true, true}, {"%k:%M", true, true},
		{"%r", false, true}, {"%I:%M:%S %p", false, true}, {"%l:%M %P", false, true},
		{"%-H:%M", true, true}, {"%_I", false, true}, {"%d/%m", false, false}, {"%", false, false},
	} {
		h24, known := hour24FromPattern(c.p)
		if h24 != c.h24 || known != c.known {
			t.Errorf("hour24FromPattern(%q) = %v, %v; want %v, %v", c.p, h24, known, c.h24, c.known)
		}
	}
}

// A reported change bumps the serial, wakes the loop and makes the next Current
// read afresh; without one Current reads once.
func TestChangedInvalidatesTheCache(t *testing.T) {
	if volatile {
		t.Skip("this OS reads on every call")
	}
	var reads, wakes atomic.Int32
	oldRead, oldWake := readFn, wake
	readFn = func() Info { reads.Add(1); return Info{Region: "ZZ"} }
	wake = func() { wakes.Add(1) }
	t.Cleanup(func() { readFn, wake = oldRead, oldWake; stale.Store(true) })
	stale.Store(true)

	if got := Current(); got.Region != "ZZ" {
		t.Fatalf("Current = %+v", got)
	}
	for i := 0; i < 5; i++ {
		Current()
	}
	if reads.Load() != 1 {
		t.Errorf("%d reads for six calls, want 1", reads.Load())
	}
	before := Serial()
	changed()
	if Serial() != before+1 || wakes.Load() != 1 {
		t.Errorf("serial %d -> %d, wakes %d", before, Serial(), wakes.Load())
	}
	Current()
	Current()
	if reads.Load() != 2 {
		t.Errorf("%d reads after a change, want 2", reads.Load())
	}
}

func TestCurrentConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				Current()
				Serial()
			}
		}()
	}
	wg.Wait()
}

func TestCurrentIsPlausible(t *testing.T) {
	info := Current()
	t.Logf("%+v", info)
	for _, l := range info.Languages {
		if l == "" || l != normalizeTag(l) {
			t.Errorf("language %q is not a normalised tag", l)
		}
	}
	if r := info.Region; r != "" && !(len(r) == 2 && isUpper(r)) && !(len(r) == 3 && isDigits(r)) {
		t.Errorf("region %q", r)
	}
	if !reflect.DeepEqual(info, Current()) {
		t.Error("two reads differ")
	}
}
