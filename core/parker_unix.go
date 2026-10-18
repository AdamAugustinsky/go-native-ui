//go:build !windows

package core

import (
	"os"
	"sync/atomic"
)

// parkerSys is a pipe: Unpark writes a byte, Park reads it. The reader blocks
// in the network poller (os.Pipe files are non-blocking and registered with
// it): internal/poll.(*FD).Read calls runtime_pollWait, which is
// runtime.poll_runtime_pollWait → netpollblock → gopark on the pollDesc's rg
// word (runtime/netpoll.go), and parking there takes no sudog.
type parkerSys struct {
	r, w *os.File
	rb   [1]byte // the parked goroutine's read buffer
	wb   [1]byte
}

func (s *parkerSys) open() bool {
	r, w, err := os.Pipe()
	if err != nil {
		return false
	}
	s.r, s.w = r, w
	return true
}

func (s *parkerSys) wait(closed *atomic.Bool) bool {
	_, err := s.r.Read(s.rb[:])
	return err == nil
}

func (s *parkerSys) wake() { s.w.Write(s.wb[:]) }

// wakeForClose wakes a reader parked in the pipe and closes the write end.
func (s *parkerSys) wakeForClose() {
	s.w.Write(s.wb[:])
	s.w.Close()
}

func (s *parkerSys) close() { s.r.Close() }
