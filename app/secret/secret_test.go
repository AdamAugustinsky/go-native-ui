package secret

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
)

// testService returns a service name no real application uses. The tests of
// the real store touch only items under it, and delete them.
func testService(t *testing.T) string {
	t.Helper()
	var b [8]byte
	rand.Read(b[:])
	return "gonativeui-secret-test-" + hex.EncodeToString(b[:])
}

// openReal opens the real credential store, or skips the test when this
// machine has none that works unattended (a CI runner without a keychain, a
// Linux box without a Secret Service).
func openReal(t *testing.T) *Store {
	t.Helper()
	s := Open(testService(t))
	probe := "probe"
	t.Cleanup(func() { s.Delete(probe) })
	err := s.Set(probe, []byte("x"))
	if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrLocked) || errors.Is(err, ErrDenied) {
		t.Skipf("no usable credential store here: %v", err)
	}
	if err != nil {
		t.Fatalf("probe Set: %v", err)
	}
	return s
}

func TestRealStoreRoundTrip(t *testing.T) { roundTrip(t, openReal(t)) }

func TestRealStoreConcurrent(t *testing.T) { concurrent(t, openReal(t)) }

// roundTrip is the contract: set, get, replace, delete, not found, and that
// accounts and services are separate.
func roundTrip(t *testing.T, s *Store) {
	t.Helper()
	acct := "alice@example.com"
	t.Cleanup(func() { s.Delete(acct) })

	if _, err := s.Get(acct); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get before Set = %v, want ErrNotFound", err)
	}
	if err := s.Delete(acct); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete before Set = %v, want ErrNotFound", err)
	}
	if err := s.Set(acct, []byte("first")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(acct)
	if err != nil || string(got) != "first" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	// Replace.
	if err := s.Set(acct, []byte("second \u00e9\x00 binary")); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(acct)
	if err != nil || string(got) != "second \u00e9\x00 binary" {
		t.Fatalf("Get after replace = %q, %v", got, err)
	}
	// The caller owns the bytes.
	got[0] = 'X'
	if again, _ := s.Get(acct); string(again) != "second \u00e9\x00 binary" {
		t.Fatalf("modifying the result changed the store: %q", again)
	}
	// Accounts are separate, and so are services.
	if _, err := s.Get("bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other account: %v", err)
	}
	other := &Store{service: s.Service() + "-other", b: s.b}
	if _, err := other.Get(acct); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other service: %v", err)
	}
	// Names that differ only in case are different items (Windows compares
	// target names without regard to case, and the package must not let it).
	for _, pair := range [][2]string{{"Alice", "alice"}, {"\u00c9mile", "\u00e9mile"}} {
		up, low := pair[0], pair[1]
		t.Cleanup(func() { s.Delete(up); s.Delete(low) })
		if err := s.Set(up, []byte("upper")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(low); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get(%q) after Set(%q) = %v, want ErrNotFound", low, up, err)
		}
		if err := s.Set(low, []byte("lower")); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Get(up); err != nil || string(got) != "upper" {
			t.Fatalf("Get(%q) = %q, %v: setting %q overwrote it", up, got, err, low)
		}
		if got, err := s.Get(low); err != nil || string(got) != "lower" {
			t.Fatalf("Get(%q) = %q, %v", low, got, err)
		}
		if err := s.Delete(up); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Get(low); err != nil || string(got) != "lower" {
			t.Fatalf("deleting %q removed %q: %q, %v", up, low, got, err)
		}
		if err := s.Delete(low); err != nil {
			t.Fatal(err)
		}
	}
	upperService := &Store{service: strings.ToUpper(s.Service()), b: s.b}
	if _, err := upperService.Get(acct); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a service name in another case: %v", err)
	}
	// A secret of a few hundred bytes, all byte values.
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	if err := s.Set("bin", all); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Delete("bin") })
	if got, err := s.Get("bin"); err != nil || !bytes.Equal(got, all) {
		t.Fatalf("binary round trip: %v, equal=%v", err, bytes.Equal(got, all))
	}
	if err := s.Delete(acct); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(acct); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v", err)
	}
}

func concurrent(t *testing.T, s *Store) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 4; i++ {
		acct := "worker-" + string(rune('a'+i))
		t.Cleanup(func() { s.Delete(acct) })
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := []byte(strings.Repeat(acct, 10))
			for j := 0; j < 5; j++ {
				if err := s.Set(acct, want); err != nil {
					errs <- err
					return
				}
				got, err := s.Get(acct)
				if err != nil || !bytes.Equal(got, want) {
					errs <- errors.Join(err, errors.New("wrong secret for "+acct))
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// fake is an in-memory backend, to check what Store does before the OS sees
// the call.
type fake struct{ calls int }

func (f *fake) get(service, account string) ([]byte, error) { f.calls++; return nil, ErrNotFound }
func (f *fake) set(service, account string, secret []byte) error {
	f.calls++
	return nil
}
func (f *fake) del(service, account string) error { f.calls++; return nil }

func TestInvalidArguments(t *testing.T) {
	long := strings.Repeat("x", maxName+1)
	for _, c := range []struct{ service, account string }{
		{"", "a"}, {"s", ""}, {"s\x00t", "a"}, {"s", "a\x00b"}, {long, "a"}, {"s", long},
		{"bad\xff", "a"}, {"s", "bad\xc3"}, {"s", "\xed\xa0\x80"}, {"\xf0\x28\x8c\x28", "a"}, // not UTF-8, a surrogate
	} {
		f := &fake{}
		s := &Store{service: c.service, b: f}
		if _, err := s.Get(c.account); !errors.Is(err, ErrInvalid) {
			t.Errorf("Get(%q, %q) = %v, want ErrInvalid", c.service, c.account, err)
		}
		if err := s.Set(c.account, []byte("x")); !errors.Is(err, ErrInvalid) {
			t.Errorf("Set(%q, %q) = %v, want ErrInvalid", c.service, c.account, err)
		}
		if err := s.Delete(c.account); !errors.Is(err, ErrInvalid) {
			t.Errorf("Delete(%q, %q) = %v, want ErrInvalid", c.service, c.account, err)
		}
		if f.calls != 0 {
			t.Errorf("(%q, %q): the backend was called %d times", c.service, c.account, f.calls)
		}
	}
	f := &fake{}
	s := &Store{service: "s", b: f}
	if err := s.Set("a", nil); err != nil || f.calls != 1 {
		t.Errorf("an empty secret is allowed: %v, calls %d", err, f.calls)
	}
}

func TestCredentialTargetIsCaseSafeAndUnambiguous(t *testing.T) {
	pairs := [][2]string{{"svc", "Alice"}, {"svc", "alice"}, {"Svc", "alice"}, {"svc", "\u00c9"}, {"svc", "\u00e9"},
		{"a:b", "c"}, {"a", "b:c"}, {"a", "bc"}, {"ab", "c"}, {"a/b", "c"}, {"a", "b/c"}, {"a", "A"}, {"\u1e9e", "ss"}, {"\u1e9e", "SS"}}
	seen := map[string][2]string{}
	for _, p := range pairs {
		tg := credentialTarget(p[0], p[1])
		if tg != strings.ToLower(tg) {
			t.Errorf("%q has capital letters: case folding could merge it with another", tg)
		}
		if prev, dup := seen[strings.ToLower(tg)]; dup {
			t.Errorf("%q and %q both name %q (ignoring case)", prev, p, tg)
		}
		seen[strings.ToLower(tg)] = p
	}
	if got := shortText("\u00e9\u00e9\u00e9", 5); got != "\u00e9\u00e9" {
		t.Errorf("shortText cut inside a character: %q", got)
	}
}
