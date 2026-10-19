//go:build linux && !amd64 && !arm64

package shell

// sysRenameat2 is 0 where its number is not known here: the move then checks for
// the name first and uses renameat.
const sysRenameat2 = 0
