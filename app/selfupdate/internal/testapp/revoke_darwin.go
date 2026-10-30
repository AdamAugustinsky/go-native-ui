package main

import (
	"os"
	"syscall"
	"unsafe"
)

// revokeInherited does to the helper what macOS did to one in the field:
// syspolicyd revoked access to the staging folder's lock file while the
// helper was being launched ("(Sandbox) syspolicyd revoked access to
// .../lock"), so the helper's inherited descriptor to it stayed open but dead,
// and fstat on it failed with EBADF. Each inherited descriptor (3 and up) to a
// regular file is replaced by a terminal's, which revoke(2) then kills the
// same way: the only files revoke works on are terminals.
func revokeInherited() {
	for fd := 3; fd < 64; fd++ {
		var st syscall.Stat_t
		if syscall.Fstat(fd, &st) != nil || st.Mode&syscall.S_IFMT != syscall.S_IFREG {
			continue
		}
		if err := deadOnto(fd); err != nil {
			panic("testapp: cannot revoke fd: " + err.Error())
		}
	}
}

// deadOnto puts a revoked terminal descriptor at fd.
func deadOnto(fd int) error {
	const tiocptygrant, tiocptyunlk, tiocptygname = 0x20007454, 0x20007452, 0x40807453
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer m.Close()
	var name [128]byte
	for _, c := range []struct{ req, arg uintptr }{{tiocptygrant, 0}, {tiocptyunlk, 0}, {tiocptygname, uintptr(unsafe.Pointer(&name[0]))}} {
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), c.req, c.arg); e != 0 {
			return e
		}
	}
	n := 0
	for n < len(name) && name[n] != 0 {
		n++
	}
	slave := string(name[:n])
	s, err := syscall.Open(slave, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	err = syscall.Dup2(s, fd)
	syscall.Close(s)
	if err != nil {
		return err
	}
	return syscall.Revoke(slave)
}
