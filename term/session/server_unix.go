//go:build darwin || linux

package session

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ErrRunning is returned by Serve when the directory's daemon is running.
var ErrRunning = errors.New("session: a daemon is already running there")

// serveHook, for tests, sees each daemon Serve makes.
var serveHook func(*daemon)

// ServeOptions tunes a daemon.
type ServeOptions struct {
	Config
	// IdleExit, if positive, shuts the daemon down once it has had no
	// sessions and no clients for that long.
	IdleExit time.Duration
	// Stop, if not nil, shuts the daemon down when it is closed.
	Stop <-chan struct{}
}

// RunDaemon is Serve for a daemon's main: SIGTERM and SIGINT shut it down
// and SIGHUP is ignored. ErrRunning is not an error for it.
func RunDaemon(dir string, o ServeOptions) error {
	signal.Ignore(syscall.SIGHUP)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sig)
	stop := make(chan struct{})
	prev := o.Stop
	go func() {
		select {
		case <-sig:
		case <-prev:
		}
		close(stop)
	}()
	o.Stop = stop
	err := Serve(dir, o)
	if errors.Is(err, ErrRunning) {
		return nil
	}
	return err
}

// Serve runs a session daemon on dir's socket until a client asks it to
// shut down, o.Stop is closed or it has idled for o.IdleExit. On the way out
// it kills every session (SIGHUP, then SIGKILL after the grace period) and
// removes its socket and files.
func Serve(dir string, o ServeOptions) error {
	if err := ensureDir(dir); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return ErrRunning
	}
	sock := filepath.Join(dir, sockName)
	os.Remove(sock) // a dead daemon's; the lock says none is running
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	os.Chmod(sock, 0o600)
	pid := filepath.Join(dir, pidName)
	os.WriteFile(pid, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)

	d := &daemon{m: newManager(o.Config), ln: ln, conns: map[net.Conn]struct{}{},
		stop: make(chan struct{}), finished: make(chan struct{}), cleaned: make(chan struct{})}
	if serveHook != nil {
		serveHook(d)
	}
	go func() {
		select {
		case <-o.Stop:
			d.shutdown()
		case <-d.stop:
		}
	}()
	if o.IdleExit > 0 {
		go d.idle(o.IdleExit)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			break
		}
		if !d.track(c, true) {
			c.Close()
			break
		}
		go d.serve(c)
	}
	d.shutdown() // the listener failed, or shutdown closed it
	for _, f := range []string{sock, pid, filepath.Join(dir, logName), filepath.Join(dir, lockName)} {
		os.Remove(f)
	}
	os.Remove(dir) // if empty
	close(d.cleaned)
	return nil
}

type daemon struct {
	m  *manager
	ln net.Listener

	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	closing bool

	once     sync.Once
	stop     chan struct{} // closed when shutting down starts
	finished chan struct{} // closed when sessions are killed and connections closed
	cleaned  chan struct{} // closed when the socket and files are gone
}

// shutdown kills the sessions, then closes the listener and every
// connection. It runs once; later calls wait for the first.
func (d *daemon) shutdown() {
	d.once.Do(func() {
		close(d.stop)
		d.mu.Lock()
		d.closing = true
		d.mu.Unlock()
		d.m.shutdown()
		d.ln.Close()
		d.mu.Lock()
		for c := range d.conns {
			c.Close()
		}
		d.mu.Unlock()
		close(d.finished)
	})
	<-d.finished
}

func (d *daemon) track(c net.Conn, add bool) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !add {
		delete(d.conns, c)
		return true
	}
	if d.closing {
		return false
	}
	d.conns[c] = struct{}{}
	return true
}

func (d *daemon) idle(after time.Duration) {
	t := time.NewTicker(min(after/4, time.Second))
	defer t.Stop()
	since := time.Now()
	for {
		select {
		case <-d.stop:
			return
		case now := <-t.C:
			d.mu.Lock()
			busy := len(d.conns) > 0
			d.mu.Unlock()
			if busy || d.m.count() > 0 {
				since = now
			} else if now.Sub(since) >= after {
				go d.shutdown()
				return
			}
		}
	}
}

// serve answers one connection's requests. An attach request turns the
// connection into that session's data connection for the rest of its life.
func (d *daemon) serve(c net.Conn) {
	defer func() {
		c.Close()
		d.track(c, false)
	}()
	f := newFramer(c)
	var h hello
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if err := f.readJSON(fHello, &h); err != nil {
		return
	}
	if h.Version != Version {
		f.writeJSON(fReply, reply{Error: fmt.Sprintf("session: the client speaks protocol %d, the daemon %d", h.Version, Version)})
		return
	}
	if err := f.writeJSON(fHello, hello{Version: Version, Pid: os.Getpid()}); err != nil {
		return
	}
	c.SetDeadline(time.Time{})
	for {
		var rq request
		if err := f.readJSON(fRequest, &rq); err != nil {
			return
		}
		var rp reply
		switch rq.Op {
		case "create":
			if rq.Options == nil {
				rp.Error = "session: create without options"
				break
			}
			s, err := d.m.create(*rq.Options)
			if err != nil {
				rp.Error = err.Error()
				break
			}
			in := s.info()
			rp.Info = &in
		case "list":
			rp.List = d.m.list()
		case "kill":
			if err := d.m.kill(rq.ID); err != nil {
				rp.Error = err.Error()
			}
		case "shutdown":
			// Reply once the sessions are gone, and close this connection,
			// which the client waits for, once the files are gone too.
			d.track(c, false)
			d.mu.Lock()
			d.closing = true
			d.mu.Unlock()
			d.m.shutdown()
			f.writeJSON(fReply, rp)
			d.shutdown()
			<-d.cleaned
			return
		case "attach":
			s, err := d.m.get(rq.ID)
			if err != nil {
				rp.Error = err.Error()
				break
			}
			in := s.info()
			rp.Info = &in
			if err := f.writeJSON(fReply, rp); err != nil {
				return
			}
			d.attached(c, f, s, rq.Size)
			return
		default:
			rp.Error = "session: unknown request " + strconv.Quote(rq.Op)
		}
		if err := f.writeJSON(fReply, rp); err != nil {
			return
		}
	}
}

// attached serves a data connection: the session's output goes out on it
// (with the connection's own flow control: a client that does not read
// makes the pump's writes block), and input, resizes, detach and close come
// in. A connection that breaks is a closed view.
func (d *daemon) attached(c net.Conn, f *framer, s *session, sz Size) {
	sub := s.attach(&connStream{f: f, c: c}, sz)
	for {
		t, b, err := f.read()
		if err != nil {
			d.m.closeView(s, sub)
			return
		}
		switch t {
		case fInput:
			s.write(b)
		case fResize:
			if sz, ok := decodeSize(b); ok {
				s.resize(sz)
			}
		case fDetach:
			s.detach(sub)
			return
		case fClose:
			d.m.closeView(s, sub)
			return
		}
	}
}

// connStream frames a session's output onto a data connection.
type connStream struct {
	f *framer
	c net.Conn
}

func (w *connStream) replay(p []byte) error {
	if err := w.f.write(fReset, nil); err != nil {
		return err
	}
	if err := w.f.writeData(fOutput, p); err != nil {
		return err
	}
	return w.f.write(fLive, nil)
}

func (w *connStream) output(p []byte) error { return w.f.writeData(fOutput, p) }

func (w *connStream) exit(code int) {
	w.f.write(fExit, binary.BigEndian.AppendUint32(nil, uint32(int32(code))))
}

func (w *connStream) cancel() { w.c.Close() }
