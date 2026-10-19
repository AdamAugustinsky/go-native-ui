//go:build windows

package instance

import (
	"fmt"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

var (
	advapi32         = syscall.NewLazyDLL("advapi32.dll")
	kernel32w        = syscall.NewLazyDLL("kernel32.dll")
	getNamedSecInfoW = advapi32.NewProc("GetNamedSecurityInfoW")
	localFree        = kernel32w.NewProc("LocalFree")
)

// checkPrivate verifies that dir belongs to this user (or to the Administrators
// group, which owns what an elevated process makes, or to SYSTEM). A directory
// someone else made first is theirs to change the access of, so it is refused.
// The directory's access control list itself is not evaluated: the directories
// the framework picks (under the profile) inherit one that excludes other users,
// and a directory given in Options.Dir is the caller's responsibility beyond its
// owner. The socket's peer is not checked on Windows (see peerUID).
func checkPrivate(dir string) error {
	name, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	var owner, sd uintptr
	// SE_FILE_OBJECT = 1, OWNER_SECURITY_INFORMATION = 1.
	r, _, _ := getNamedSecInfoW.Call(uintptr(unsafe.Pointer(name)), 1, 1, uintptr(unsafe.Pointer(&owner)), 0, 0, 0, uintptr(unsafe.Pointer(&sd)))
	if r != 0 {
		return fmt.Errorf("instance: reading the owner of %s: %w", dir, syscall.Errno(r))
	}
	defer localFree.Call(sd)
	ownerSID, err := (*syscall.SID)(win32.Ptr(owner)).String()
	if err != nil {
		return err
	}

	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return err
	}
	me, err := u.User.Sid.String()
	if err != nil {
		return err
	}
	switch ownerSID {
	case me, "S-1-5-32-544", "S-1-5-18": // this user, Administrators, SYSTEM
		return nil
	}
	return fmt.Errorf("%w: %s belongs to %s, not to this user", ErrUntrusted, dir, ownerSID)
}
