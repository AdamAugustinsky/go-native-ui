//go:build darwin || linux

package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// stream is where a session's output goes for one view. The session's pump
// goroutine calls replay once, then output any number of times, then exit if
// the program ended; cancel comes from elsewhere, when the view is taken
// over or ends, and unblocks output if it can.
type stream interface {
	replay(p []byte) error
	output(p []byte) error
	exit(code int)
	cancel()
}

// manager owns the sessions of one service, local or daemon.
type manager struct {
	cfg      Config
	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
}

func newManager(cfg Config) *manager {
	return &manager{cfg: cfg.withDefaults(), sessions: map[string]*session{}}
}

// session is one program on a pseudo-terminal. Its reader goroutine moves
// output into the ring and the view's queue in one step under mu, and an
// attach snapshots the ring under mu too, so the replay and the live output
// meet with no gap and no overlap.
type session struct {
	m       *manager
	id      string
	o       Options
	created time.Time
	pty     *os.File
	cmd     *exec.Cmd
	read    chan struct{} // closed when the reader has seen the terminal's end
	waited  chan struct{} // closed when the program has been reaped
	kill1   sync.Once

	mu        sync.Mutex
	cond      sync.Cond
	ring      ring
	sub       *subscriber
	size      Size
	finished  bool // read and waited
	closed    bool // pty closed
	code      int
	maxQueued int // the most bytes ever queued for a view, for tests
}

// subscriber is the session's current view.
type subscriber struct {
	out            stream
	pending, spare []byte // pending: queued for out; spare: the buffer out is writing
	stopped        bool   // detached, taken over or broken
	taken          bool   // taken over by another view
	exited         bool
	code           int
	done           chan struct{} // closed when the pump returns
}

func newID() string {
	var b [6]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (m *manager) create(o Options) (*session, error) {
	if len(o.Argv) == 0 {
		return nil, errors.New("session: no program")
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	f, cmd, err := startPTY(o)
	if err != nil {
		return nil, err
	}
	s := &session{m: m, id: newID(), o: o, created: time.Now(), pty: f, cmd: cmd,
		read: make(chan struct{}), waited: make(chan struct{}), ring: newRing(m.cfg.RingSize), size: o.Size}
	s.cond.L = &s.mu
	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()
	go s.readLoop()
	go s.waitLoop()
	go s.reap()
	return s, nil
}

func (m *manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return nil, ErrNotFound
	}
	return s, nil
}

func (m *manager) list() []Info {
	m.mu.Lock()
	ss := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		ss = append(ss, s)
	}
	m.mu.Unlock()
	l := make([]Info, len(ss))
	for i, s := range ss {
		l[i] = s.info()
	}
	return l
}

func (m *manager) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *manager) remove(s *session) {
	m.mu.Lock()
	if m.sessions[s.id] == s {
		delete(m.sessions, s.id)
	}
	m.mu.Unlock()
}

func (m *manager) kill(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	m.remove(s)
	s.kill()
	return nil
}

// shutdown kills every session, all at once, and refuses new ones.
func (m *manager) shutdown() {
	m.mu.Lock()
	m.closed = true
	ss := m.sessions
	m.sessions = map[string]*session{}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range ss {
		wg.Go(s.kill)
	}
	wg.Wait()
}

// closeView is what a view's close or disconnection does: a persistent
// session detaches, a non-persistent or finished one is killed. A view
// another has taken over does nothing.
func (m *manager) closeView(s *session, sub *subscriber) {
	s.mu.Lock()
	taken := sub.taken
	keep := s.o.Persist && !s.finished
	s.mu.Unlock()
	switch {
	case taken:
	case keep:
		s.detach(sub)
	default:
		m.remove(s)
		s.kill()
	}
}

func (s *session) info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Info{ID: s.id, Argv: s.o.Argv, Pid: s.cmd.Process.Pid, Persist: s.o.Persist, Created: s.created,
		Size: s.size, Attached: s.sub != nil, Exited: s.finished, ExitCode: s.code}
}

func (s *session) readLoop() {
	defer close(s.read)
	b := make([]byte, 32<<10)
	for {
		n, err := s.pty.Read(b)
		if n > 0 {
			s.deliver(b[:n])
		}
		if err != nil {
			return
		}
	}
}

// deliver adds p to the ring and to the view's queue. If the queue is
// full it waits, and so the terminal is not read and the program blocks on
// its writes: the flow control that keeps a slow view from costing memory.
func (s *session) deliver(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.sub != nil && len(s.sub.pending) > 0 && len(s.sub.pending)+len(p) > s.m.cfg.LiveLimit {
		s.cond.Wait()
	}
	s.ring.write(p)
	if sub := s.sub; sub != nil {
		sub.pending = append(sub.pending, p...)
		s.maxQueued = max(s.maxQueued, len(sub.pending))
		s.cond.Broadcast()
	}
}

func (s *session) waitLoop() {
	s.cmd.Wait()
	s.mu.Lock()
	s.code = s.cmd.ProcessState.ExitCode()
	s.mu.Unlock()
	close(s.waited)
}

// reap closes the terminal once its program and output are done, and
// forgets a non-persistent session nobody is viewing.
func (s *session) reap() {
	<-s.read
	<-s.waited
	s.mu.Lock()
	s.finished, s.closed = true, true
	s.pty.Close()
	forget := !s.o.Persist && s.sub == nil
	s.cond.Broadcast()
	s.mu.Unlock()
	if forget {
		s.m.remove(s)
	}
}

// kill sends SIGHUP to the program's process group (Setsid made it the
// group's leader) and SIGKILL after the grace period if the program, or
// another process of the group holding the terminal, is still there.
func (s *session) kill() {
	s.kill1.Do(func() {
		pid := s.cmd.Process.Pid
		grace := s.m.cfg.KillGrace
		syscall.Kill(-pid, syscall.SIGHUP)
		t := time.NewTimer(grace)
		defer t.Stop()
		select {
		case <-s.waited:
			select {
			case <-s.read:
				return
			case <-t.C:
			}
		case <-t.C:
		}
		syscall.Kill(-pid, syscall.SIGKILL)
		<-s.waited
		select {
		case <-s.read:
		case <-time.After(grace):
			// A process outside the group holds the terminal open; the
			// reader goes when it does.
		}
	})
}

func (s *session) write(p []byte) (int, error) {
	return s.pty.Write(p)
}

func (s *session) resize(sz Size) {
	if sz.Cols <= 0 || sz.Rows <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.size = sz
	if !s.closed {
		resizePTY(s.pty, sz)
	}
}

// attach makes out the session's view, taking it over from any other.
func (s *session) attach(out stream, sz Size) *subscriber {
	sub := &subscriber{out: out, done: make(chan struct{})}
	s.mu.Lock()
	old := s.sub
	if old != nil {
		old.stopped, old.taken = true, true
	}
	replay, _ := s.ring.snapshot(nil)
	s.sub = sub
	s.cond.Broadcast()
	s.mu.Unlock()
	if old != nil {
		old.out.cancel()
	}
	s.resize(sz)
	go s.pump(sub, replay)
	return sub
}

// detach ends sub's view; the session goes on.
func (s *session) detach(sub *subscriber) {
	s.mu.Lock()
	if s.sub == sub {
		s.sub = nil
	}
	sub.stopped = true
	s.cond.Broadcast()
	s.mu.Unlock()
	sub.out.cancel()
}

// pump moves the replay, then the queued output, to sub's stream.
func (s *session) pump(sub *subscriber, replay []byte) {
	defer close(sub.done)
	if err := sub.out.replay(replay); err != nil {
		s.detach(sub)
		return
	}
	for {
		s.mu.Lock()
		for len(sub.pending) == 0 && !sub.stopped && !s.finished {
			s.cond.Wait()
		}
		if sub.stopped {
			s.mu.Unlock()
			return
		}
		if len(sub.pending) == 0 { // finished, and everything is out
			sub.exited, sub.code = true, s.code
			s.mu.Unlock()
			sub.out.exit(sub.code)
			return
		}
		chunk := sub.pending
		sub.pending, sub.spare = sub.spare[:0], nil
		s.cond.Broadcast()
		s.mu.Unlock()
		err := sub.out.output(chunk)
		s.mu.Lock()
		sub.spare = chunk[:0]
		s.mu.Unlock()
		if err != nil {
			s.detach(sub)
			return
		}
	}
}

func (s *session) exitOf(sub *subscriber) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sub.code, sub.exited
}
