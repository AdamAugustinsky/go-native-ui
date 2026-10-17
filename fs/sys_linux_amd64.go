//go:build linux && amd64

package fs

// sysStatx is __NR_statx: 332 in arch/x86/entry/syscalls/syscall_64.tbl
// (linux master, fetched 2026-10-17). The frozen syscall package predates it.
const sysStatx = 332
