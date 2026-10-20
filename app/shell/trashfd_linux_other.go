//go:build linux && !amd64 && !arm64

package shell

// sysRenameat2 is 0 where its number is not known here: moving a file into the
// trash is then refused (see renameInto).
const sysRenameat2 = 0
