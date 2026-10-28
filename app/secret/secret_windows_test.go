//go:build windows

package secret

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"

	"gonativeui/app/paths"
)

// rawCredential reads the item's credential as Credential Manager has it: its
// blob size and the sealed file it names ("" for none).
func rawCredential(t *testing.T, s *Store, account string) (blob int, file string, found bool) {
	t.Helper()
	name, _ := syscall.UTF16PtrFromString(credentialTarget(s.Service(), account))
	var cred *credential
	r, _, e := syscall.SyscallN(wincred.read, uintptr(unsafe.Pointer(name)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&cred)))
	if uint32(r) == 0 {
		if e == errNotFound {
			return 0, "", false
		}
		t.Fatalf("CredReadW: %v", e)
	}
	defer syscall.SyscallN(wincred.free, uintptr(unsafe.Pointer(cred)))
	file, _ = sealedName(cred)
	return int(cred.CredentialBlobSize), file, true
}

// Secrets over Credential Manager's 2,560 bytes (S2): sealed into a file
// under the app's data folder, the credential naming it; replaced by a
// small one, the file goes; deleted, both go.
func TestRealStoreLargeSecrets(t *testing.T) {
	s := openReal(t)
	dir, err := paths.Data(s.Service())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	const acct = "large@example.com"
	t.Cleanup(func() { s.Delete(acct) })

	// At the limit: inline, no file.
	edge := bytes.Repeat([]byte{'e'}, credMaxBlob)
	if err := s.Set(acct, edge); err != nil {
		t.Fatal(err)
	}
	if blob, file, _ := rawCredential(t, s, acct); blob != credMaxBlob || file != "" {
		t.Fatalf("a %d-byte secret: blob %d, file %q; want inline", credMaxBlob, blob, file)
	}

	for _, n := range []int{credMaxBlob + 1, 64 << 10} {
		big := make([]byte, n)
		rand.Read(big)
		if err := s.Set(acct, big); err != nil {
			t.Fatalf("Set(%d bytes): %v", n, err)
		}
		blob, file, ok := rawCredential(t, s, acct)
		if !ok || blob != 0 || file != sealedFile(s.Service(), acct) {
			t.Fatalf("%d bytes: credential found %v, blob %d, file %q", n, ok, blob, file)
		}
		got, err := s.Get(acct)
		if err != nil || !bytes.Equal(got, big) {
			t.Fatalf("Get after a %d-byte Set: %d bytes, %v", n, len(got), err)
		}
		sealed, err := os.ReadFile(filepath.Join(dir, "Secrets", file))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(sealed, big[100:132]) {
			t.Fatal("the file holds the secret in the clear")
		}
	}
	file := filepath.Join(dir, "Secrets", sealedFile(s.Service(), acct))

	// A file moved to another item's name does not open there.
	const other = "other@example.com"
	t.Cleanup(func() { s.Delete(other) })
	if err := s.Set(other, bytes.Repeat([]byte{'o'}, credMaxBlob+10)); err != nil {
		t.Fatal(err)
	}
	stolen, _ := os.ReadFile(file)
	os.WriteFile(filepath.Join(dir, "Secrets", sealedFile(s.Service(), other)), stolen, 0o600)
	if _, err := s.Get(other); !errors.Is(err, ErrDenied) {
		t.Fatalf("Get with another item's file = %v, want ErrDenied", err)
	}

	// Replaced by a small secret: inline again, the file removed.
	if err := s.Set(acct, []byte("small")); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(acct); err != nil || string(got) != "small" {
		t.Fatalf("Get after the small Set = %q, %v", got, err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the sealed file is still there after a small Set: %v", err)
	}

	// Large again, then deleted: credential and file gone.
	if err := s.Set(acct, bytes.Repeat([]byte{'x'}, 5000)); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(acct); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the sealed file is still there after Delete: %v", err)
	}
	if _, _, ok := rawCredential(t, s, acct); ok {
		t.Fatal("the credential is still there after Delete")
	}
	if _, err := s.Get(acct); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "Secrets", "*.tmp")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

// A service that cannot name a folder keeps small secrets and refuses large
// ones before anything is written.
func TestLargeSecretNeedsAFolderName(t *testing.T) {
	s := &Store{service: "bad:service", b: credMan{}}
	if err := s.Set("a", make([]byte, credMaxBlob+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Set = %v, want ErrInvalid", err)
	}
}
