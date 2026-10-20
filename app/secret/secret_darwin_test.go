//go:build darwin

package secret

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"gonativeui/internal/cf"
)

// tempKeychain creates a keychain file of its own in a temporary directory and
// returns a store confined to it (keychain.ref), so the test never reads or
// writes an item of the user's keychains, whatever their service names. It
// skips when this session cannot create a keychain (SecKeychainCreate is
// deprecated but present).
func tempKeychain(t *testing.T) *Store {
	t.Helper()
	sec.once.Do(sec.load)
	if sec.err != nil {
		t.Skipf("Security framework: %v", sec.err)
	}
	lib, err := cf.Framework("Security")
	if err != nil {
		t.Skip(err)
	}
	create, del := cf.Sym(lib, "SecKeychainCreate"), cf.Sym(lib, "SecKeychainDelete")
	if create == 0 || del == 0 {
		t.Skip("SecKeychainCreate is missing")
	}
	path := append([]byte(filepath.Join(t.TempDir(), "gonativeui-test.keychain-db")), 0)
	pw := []byte("gonativeui-test-password")
	fr := cf.Acquire()
	defer cf.Release(fr)
	out := cf.Out(fr)
	st := int32(fr.Reset().ArgPtr(unsafe.Pointer(&path[0])).Arg32(uint32(len(pw))).ArgPtr(unsafe.Pointer(&pw[0])).
		ArgBool(false).Arg(0).ArgPtr(out).Call(create).R0())
	runtime.KeepAlive(path)
	runtime.KeepAlive(pw)
	kc := cf.Word(fr)
	if st != 0 || kc == 0 {
		t.Skipf("cannot create a temporary keychain here: OSStatus %d", st)
	}
	t.Cleanup(func() {
		f := cf.Acquire()
		defer cf.Release(f)
		f.Call1(del, kc)
		cf.CFRelease(f, kc)
	})
	return &Store{service: testService(t), b: keychain{ref: kc}}
}

func TestTempKeychainRoundTrip(t *testing.T) { roundTrip(t, tempKeychain(t)) }

func TestTempKeychainConcurrent(t *testing.T) { concurrent(t, tempKeychain(t)) }

func TestStatusErrors(t *testing.T) {
	for _, c := range []struct {
		st   int32
		want error
	}{
		{errSecItemNotFound, ErrNotFound},
		{errSecInteractionNotAllowed, ErrLocked},
		{errSecAuthFailed, ErrDenied},
		{errSecUserCanceled, ErrDenied},
		{errSecNoDefaultKeychain, ErrUnavailable},
	} {
		if err := statusErr(c.st); !errors.Is(err, c.want) {
			t.Errorf("statusErr(%d) = %v, want %v", c.st, err, c.want)
		}
	}
	if err := statusErr(-1); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown status = %v", err)
	}
}
