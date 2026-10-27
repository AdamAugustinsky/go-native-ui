//go:build darwin || linux

package main

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

// pty is a program running on a pseudo-terminal.
type pty struct {
	f   *os.File // the controlling side: the program's output, our input to it
	cmd *exec.Cmd
}

// startPTY runs argv on a new pseudo-terminal of cols × rows cells, in a
// session of its own with the terminal as its controlling terminal.
func startPTY(argv []string, cols, rows int) (*pty, error) {
	fd, name, err := openPTY()
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "/dev/ptmx")
	p := &pty{f: f}
	p.resize(cols, rows, 0, 0)
	tty, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		f.Close()
		return nil, err
	}
	defer tty.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=termdemo")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, err
	}
	p.cmd = cmd
	return p, nil
}

// resize tells the program the terminal's size (TIOCSWINSZ; it gets
// SIGWINCH).
func (p *pty) resize(cols, rows, w, h int) {
	ws := [4]uint16{uint16(rows), uint16(cols), uint16(w), uint16(h)}
	ioctl(int(p.f.Fd()), tiocswinsz, uintptr(unsafe.Pointer(&ws)))
}

func (p *pty) close() {
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Signal(syscall.SIGHUP)
	}
	p.f.Close()
	if p.cmd != nil {
		p.cmd.Wait()
	}
}
