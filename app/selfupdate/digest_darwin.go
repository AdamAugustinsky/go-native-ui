//go:build darwin

package selfupdate

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type winStaged struct{}

// PrepareSHA256 is Prepare for an archive whose SHA-256 the app took from a
// manifest it trusts: the archive is copied to a private file while it is
// hashed, and that copy, once its digest matches sum, is what Prepare
// unpacks. On macOS the signature rules apply as well; on Windows it is how
// an unsigned app (Options.Unsigned) updates.
func PrepareSHA256(archive string, sum [32]byte) (*Staged, error) {
	in, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	dir, err := os.MkdirTemp("", "selfupdate-digest-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	cp := filepath.Join(dir, filepath.Base(archive))
	out, err := os.OpenFile(cp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	var got [32]byte
	copy(got[:], h.Sum(nil))
	if got != sum {
		return nil, fmt.Errorf("%w: the archive's SHA-256 is %x, not the one the app expects", ErrRejected, got)
	}
	return Prepare(cp)
}
