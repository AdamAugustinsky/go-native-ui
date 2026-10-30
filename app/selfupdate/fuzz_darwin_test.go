//go:build darwin

package selfupdate

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The publisher's signed manifest lives in the app, not this package. This
// exercises the package's real PrepareSHA256 entry point: every selected
// byte mutation and every selected digest-bit mutation must be rejected
// before staging. A correct digest reaches ErrNotBundled in a test binary.
// Payload <=64 KiB; 5 s and 16 MiB + 256*payload bytes, no extraction.
func FuzzUpdateDigest(f *testing.F) {
	f.Add([]byte("signed archive payload"), uint32(0), byte(1))
	f.Add(fuzzArchive(f, bundle()), uint32(100), byte(128))
	f.Add([]byte{0}, uint32(0), byte(255))
	p := filepath.Join(f.TempDir(), "update.zip")
	f.Fuzz(func(t *testing.T, b []byte, at uint32, mask byte) {
		if len(b) == 0 || len(b) > 64<<10 {
			t.Skip()
		}
		defer fuzzUpdateBound(t, len(b))()
		if mask == 0 {
			mask = 1
		}
		sum := sha256.Sum256(b)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if s, err := PrepareSHA256(p, sum); s != nil || !errors.Is(err, ErrNotBundled) {
			t.Fatalf("valid digest did not reach bundle gate: %v", err)
		}
		bad := sum
		bad[at%32] ^= mask
		if s, err := PrepareSHA256(p, bad); s != nil || !errors.Is(err, ErrRejected) {
			t.Fatalf("changed digest accepted: %v", err)
		}
		copyB := append([]byte(nil), b...)
		copyB[uint64(at)%uint64(len(b))] ^= mask
		if err := os.WriteFile(p, copyB, 0o600); err != nil {
			t.Fatal(err)
		}
		if s, err := PrepareSHA256(p, sum); s != nil || !errors.Is(err, ErrRejected) {
			t.Fatalf("changed archive accepted: %v", err)
		}
	})
}
