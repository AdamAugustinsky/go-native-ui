//go:build unix

package instance

import (
	"fmt"
	"os"
	"syscall"
)

// checkPrivateDir verifies that dir (in a shared place such as /tmp) is a real
// directory owned by this user that no one else can enter, so that another user
// who made it first cannot be the one to receive our requests.
func checkPrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("instance: %s is not a directory of this user", dir)
	}
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}
