package session

import (
	"strconv"
	"syscall"
	"unsafe"
)

const (
	tiocgptn   = 0x80045430
	tiocsptlck = 0x40045431
	tiocswinsz = 0x5414
)

// openPTY opens a pseudo-terminal pair's controlling side and returns it
// with the path of the program's side.
func openPTY() (fd int, tty string, err error) {
	fd, err = syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", err
	}
	var unlock int32
	var n uint32
	if err := ioctl(fd, tiocsptlck, uintptr(unsafe.Pointer(&unlock))); err != nil {
		syscall.Close(fd)
		return -1, "", err
	}
	if err := ioctl(fd, tiocgptn, uintptr(unsafe.Pointer(&n))); err != nil {
		syscall.Close(fd)
		return -1, "", err
	}
	return fd, "/dev/pts/" + strconv.Itoa(int(n)), nil
}
