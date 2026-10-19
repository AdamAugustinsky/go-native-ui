//go:build linux && amd64

package shell

// The system call numbers of renameat2 and linkat (package syscall defines
// neither for this architecture).
const (
	sysRenameat2 = 316
	sysLinkat    = 265
)
