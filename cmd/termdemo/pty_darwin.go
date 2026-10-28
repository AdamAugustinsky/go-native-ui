package main

import (
	"bytes"
	"syscall"
	"unsafe"
)

const (
	tiocptygrant = 0x20007454
	tiocptyunlk  = 0x20007452
	tiocptygname = 0x40807453
	tiocswinsz   = 0x80087467
)

// openPTY opens a pseudo-terminal pair's controlling side and returns it
// with the path of the program's side (posix_openpt, grantpt, unlockpt,
// ptsname).
func openPTY() (fd int, tty string, err error) {
	fd, err = syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", err
	}
	var name [128]byte
	for _, op := range [...]struct {
		req uintptr
		arg uintptr
	}{{tiocptygrant, 0}, {tiocptyunlk, 0}, {tiocptygname, uintptr(unsafe.Pointer(&name))}} {
		if err := ioctl(fd, op.req, op.arg); err != nil {
			syscall.Close(fd)
			return -1, "", err
		}
	}
	return fd, string(name[:bytes.IndexByte(name[:], 0)]), nil
}
