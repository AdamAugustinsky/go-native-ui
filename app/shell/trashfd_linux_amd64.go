//go:build linux && amd64

package shell

// sysRenameat2 is the system call number of renameat2 (package syscall does not
// define it for this architecture).
const sysRenameat2 = 316
