package prefs

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fuzzPreferences struct {
	Text    string
	Enabled bool
	Count   int64
	Ratio   float64
	Bytes   []byte
	List    []string
	Map     map[string]int64
	Nested  *settings
}

// Files <=64 KiB; values use at most 4 KiB of arbitrary text/bytes. Open
// agrees with JSON-on-defaults, including partial decode failures, and an
// actual atomic save/reopen preserves every JSON-representable field.
// Invalid UTF-8 uses encoding/json's documented U+FFFD replacement.
// Per case: 5 s and 16 MiB + 128*(file+value bytes) allocated.
func FuzzPrefsFile(f *testing.F) {
	for _, raw := range []string{"", `{}`, `null`, `{"Theme":"dark","Zoom":1.5,"Recent":["a"],"Nested":{"A":2}}`, `{"Text":"saved","Count":"wrong"}`, `{"List":["\ud800","\u0000"],"Map":{"x":-1}}`, "{\xff}"} {
		f.Add([]byte(raw), "hello\r\nworld", int64(3), uint64(0x3ff0000000000000), true)
	}
	// Reuse a worker-private directory; each call replaces the input and closes
	// its stores before the next call. No timers or files escape the iteration.
	p := filepath.Join(f.TempDir(), "prefs.json")
	f.Fuzz(func(t *testing.T, raw []byte, value string, count int64, bits uint64, keep bool) {
		if len(raw) > 64<<10 || len(value) > 4096 {
			t.Skip()
		}
		start := time.Now()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		defer func() {
			runtime.ReadMemStats(&after)
			if time.Since(start) > 5*time.Second || after.TotalAlloc-before.TotalAlloc > uint64(16<<20+128*(len(raw)+len(value))) {
				t.Fatal("prefs resource bound exceeded")
			}
		}()
		def := fuzzPreferences{Text: "default", Enabled: true, Count: 42, Ratio: 1.25, List: []string{"recent"}, Map: map[string]int64{"x": 7}, Nested: &settings{Theme: "light", Zoom: 1}}
		want := clone(def)
		decodeErr := json.Unmarshal(raw, &want)
		if err := os.WriteFile(p, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Open(p, def, Options{KeepCorrupt: keep, Debounce: time.Hour})
		if decodeErr != nil {
			if !errors.Is(err, ErrCorrupt) || !reflect.DeepEqual(s.Get(), def) {
				t.Fatal("corrupt input did not restore defaults")
			}
			s.Set(want)
			if err := s.Close(); (keep && !errors.Is(err, ErrNotLoaded)) || (!keep && err != nil) {
				t.Fatalf("corrupt save policy: %v", err)
			}
			if keep {
				got, err := os.ReadFile(p)
				if err != nil || !bytes.Equal(got, raw) {
					t.Fatal("kept corrupt file overwritten")
				}
			} else {
				got, err := os.ReadFile(p + ".corrupt")
				if err != nil || !bytes.Equal(got, raw) {
					t.Fatal("corrupt backup differs")
				}
			}
		} else {
			if err != nil || !reflect.DeepEqual(s.Get(), want) {
				t.Fatalf("load differs: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if err := os.Remove(p + ".corrupt"); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		// A JSON encode/decode supplies the string normalization reference. It
		// deliberately preserves distinct invalid bytes as separate U+FFFDs.
		v := fuzzPreferences{Text: value, Enabled: keep, Count: count, Ratio: math.Float64frombits(bits), Bytes: []byte(value), List: strings.Split(value, "\n"), Map: map[string]int64{"value": count}, Nested: &settings{Theme: value, Zoom: 2}}
		encoded, encErr := json.Marshal(v)
		s, err = Open(p, def, Options{Debounce: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		s.Set(v)
		closeErr := s.Close()
		if encErr != nil {
			if closeErr == nil {
				t.Fatal("non-finite float silently saved")
			}
			if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unencodable value created a file")
			}
			return
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		var normalized fuzzPreferences
		if err := json.Unmarshal(encoded, &normalized); err != nil {
			t.Fatal(err)
		}
		s, err = Open(p, fuzzPreferences{}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		got := s.Get()
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, normalized) {
			t.Fatal("save/load round trip differs")
		}
	})
}
