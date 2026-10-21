//go:build linux

package inspectwire

import (
	"errors"
	"syscall"
)

// hasACL is false on Linux: a socket made in a private directory with no
// default ACL has none, and an access ACL shows in the group bits (the mask).
func hasACL(string) (bool, error) { return false, nil }

// stripACL does nothing on Linux.
func stripACL(string) error { return nil }

// dirHasACL reports whether dir has a default POSIX ACL, which what is created
// in it would inherit.
func dirHasACL(dir string) (bool, error) {
	n, err := syscall.Getxattr(dir, "system.posix_acl_default", nil)
	switch {
	case err == nil:
		return n > 0, nil
	case errors.Is(err, syscall.ENODATA), errors.Is(err, syscall.ENOTSUP):
		return false, nil
	}
	return false, err
}
