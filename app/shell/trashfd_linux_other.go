//go:build linux && !amd64 && !arm64

package shell

// 0 where the numbers are not known here. Moving a file into the trash then
// fails (it never falls back to something that could replace a file).
const (
	sysRenameat2 = 0
	sysLinkat    = 0
)
