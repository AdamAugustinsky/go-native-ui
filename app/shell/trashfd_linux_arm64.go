//go:build linux && arm64

package shell

// The system call numbers of renameat2 and linkat.
const (
	sysRenameat2 = 276
	sysLinkat    = 37
)
