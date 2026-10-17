//go:build linux && arm64

package fs

// sysStatx is __NR_statx: 291 in include/uapi/asm-generic/unistd.h, which
// arm64 uses (linux master, fetched 2026-10-17). The frozen syscall package
// predates it.
const sysStatx = 291
