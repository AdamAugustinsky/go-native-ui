//go:build darwin || linux

package session

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

func ioctl(fd int, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, arg); e != 0 {
		return e
	}
	return nil
}

// startPTY runs o.Argv on a new pseudo-terminal, in a session (and process
// group) of its own with the terminal as its controlling terminal, and
// returns the terminal's controlling side.
func startPTY(o Options) (*os.File, *exec.Cmd, error) {
	fd, name, err := openPTY()
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), "/dev/ptmx")
	resizePTY(f, o.Size)
	tty, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	defer tty.Close()
	cmd := exec.Command(o.Argv[0], o.Argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.Env = append(os.Environ(), o.Env...)
	cmd.Dir = o.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, cmd, nil
}

// resizePTY tells the program the terminal's size (TIOCSWINSZ; it gets
// SIGWINCH if the size changed).
func resizePTY(f *os.File, s Size) {
	if s.Cols <= 0 || s.Rows <= 0 {
		return
	}
	ws := [4]uint16{uint16(s.Rows), uint16(s.Cols), uint16(s.Width), uint16(s.Height)}
	ioctl(int(f.Fd()), tiocswinsz, uintptr(unsafe.Pointer(&ws)))
}
