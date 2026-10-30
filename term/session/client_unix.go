//go:build darwin || linux

package session

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Dial connects to the daemon serving dir.
func Dial(dir string) (Service, error) {
	c, f, pid, err := dial(dir)
	if err != nil {
		return nil, err
	}
	return &client{dir: dir, c: c, f: f, pid: pid, atts: map[*remoteAttachment]struct{}{}}, nil
}

// DialOrLaunch connects to dir's daemon, starting it with argv first if
// none is running: argv runs in a session of its own with its output in
// dir's log, and DialOrLaunch waits up to timeout for its socket. An app
// usually launches itself in a daemon mode that calls RunDaemon.
func DialOrLaunch(dir string, argv []string, timeout time.Duration) (Service, error) {
	if s, err := Dial(dir); err == nil {
		return s, nil
	}
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(filepath.Join(dir, logName), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	log.Close()
	if err != nil {
		return nil, err
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	deadline := time.Now().Add(timeout)
	for {
		s, err := Dial(dir)
		if err == nil {
			return s, nil
		}
		select {
		case <-exited:
			// Another launcher's daemon may have won the lock.
			if s, err := Dial(dir); err == nil {
				return s, nil
			}
			return nil, fmt.Errorf("session: the daemon exited (see %s): %v", filepath.Join(dir, logName), err)
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("session: no daemon at %s after %v: %w", dir, timeout, err)
		}
	}
}

func dial(dir string) (net.Conn, *framer, int, error) {
	c, err := net.DialTimeout("unix", filepath.Join(dir, sockName), 2*time.Second)
	if err != nil {
		return nil, nil, 0, err
	}
	f := newFramer(c)
	c.SetDeadline(time.Now().Add(5 * time.Second))
	var h hello
	err = f.writeJSON(fHello, hello{Version: Version})
	if err == nil {
		var t frameType
		var b []byte
		if t, b, err = f.read(); err == nil {
			switch t {
			case fHello:
				err = json.Unmarshal(b, &h)
			case fReply:
				var rp reply
				if err = json.Unmarshal(b, &rp); err == nil {
					err = rp.err()
				}
			default:
				err = fmt.Errorf("session: frame %d for a hello", t)
			}
		}
	}
	if err != nil {
		c.Close()
		return nil, nil, 0, err
	}
	c.SetDeadline(time.Time{})
	return c, f, h.Pid, nil
}

// client is a Service over a daemon's control connection; each attachment
// has a connection of its own, so a view that is slow to take its output
// holds back only its session.
type client struct {
	dir string
	pid int

	mu sync.Mutex // one request at a time
	c  net.Conn
	f  *framer

	amu    sync.Mutex
	atts   map[*remoteAttachment]struct{}
	closed bool
}

func (c *client) request(rq request) (reply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var rp reply
	if err := c.f.writeJSON(fRequest, rq); err != nil {
		return rp, err
	}
	if err := c.f.readJSON(fReply, &rp); err != nil {
		return rp, err
	}
	return rp, rp.err()
}

func (c *client) Create(o Options) (Info, error) {
	rp, err := c.request(request{Op: "create", Options: &o})
	if err != nil {
		return Info{}, err
	}
	return *rp.Info, nil
}

func (c *client) List() ([]Info, error) {
	rp, err := c.request(request{Op: "list"})
	return rp.List, err
}

func (c *client) Kill(id string) error {
	_, err := c.request(request{Op: "kill", ID: id})
	return err
}

// Shutdown stops the daemon and returns once it has killed its sessions
// and removed its files.
func (c *client) Shutdown() error {
	if _, err := c.request(request{Op: "shutdown"}); err != nil {
		return err
	}
	c.c.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		if _, _, err := c.f.read(); err != nil {
			break
		}
	}
	return c.Close()
}

// Close drops the client's connections, as a crash would: persistent
// sessions detach and the rest are hung up.
func (c *client) Close() error {
	c.amu.Lock()
	c.closed = true
	for a := range c.atts {
		a.c.Close()
	}
	c.atts = nil
	c.amu.Unlock()
	return c.c.Close()
}

func (c *client) Attach(id string, sink io.Writer, sz Size) (Attachment, error) {
	conn, f, _, err := dial(c.dir)
	if err != nil {
		return nil, err
	}
	var rp reply
	err = f.writeJSON(fRequest, request{Op: "attach", ID: id, Size: sz})
	if err == nil {
		err = f.readJSON(fReply, &rp)
	}
	if err == nil {
		err = rp.err()
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	a := &remoteAttachment{cl: c, id: id, c: conn, f: f, done: make(chan struct{})}
	c.amu.Lock()
	if c.closed {
		c.amu.Unlock()
		conn.Close()
		return nil, ErrClosed
	}
	c.atts[a] = struct{}{}
	c.amu.Unlock()
	go a.readLoop(sink)
	return a, nil
}

type remoteAttachment struct {
	cl   *client
	id   string
	c    net.Conn
	f    *framer
	done chan struct{} // no more output
	once sync.Once

	mu     sync.Mutex
	exited bool
	code   int
}

// readLoop moves the session's output to sink. It is the only reader of
// the connection, so a sink that blocks stops it reading, the daemon's
// writes back up, and its pump holds the session's terminal.
func (a *remoteAttachment) readLoop(sink io.Writer) {
	defer a.finish()
	for {
		t, b, err := a.f.read()
		if err != nil {
			return
		}
		switch t {
		case fReset:
			_, err = io.WriteString(sink, ResetSequence)
		case fOutput:
			_, err = sink.Write(b)
		case fExit:
			if len(b) == 4 {
				a.mu.Lock()
				a.exited, a.code = true, int(int32(binary.BigEndian.Uint32(b)))
				a.mu.Unlock()
			}
			return
		}
		if err != nil { // the view is gone
			a.c.Close()
			return
		}
	}
}

func (a *remoteAttachment) finish() { a.once.Do(func() { close(a.done) }) }

func (a *remoteAttachment) ID() string { return a.id }

func (a *remoteAttachment) Write(p []byte) (int, error) {
	if err := a.f.writeData(fInput, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (a *remoteAttachment) Resize(sz Size) error { return a.f.write(fResize, encodeSize(sz)) }

func (a *remoteAttachment) Detach() error { return a.end(fDetach) }
func (a *remoteAttachment) Close() error  { return a.end(fClose) }

// end sends t and hangs up; the daemon reads t before the hang-up, so it
// acts on it (after end returns), not on the disconnection.
func (a *remoteAttachment) end(t frameType) error {
	err := a.f.write(t, nil)
	a.c.Close()
	a.finish()
	a.cl.amu.Lock()
	delete(a.cl.atts, a)
	a.cl.amu.Unlock()
	if errors.Is(err, net.ErrClosed) {
		err = ErrClosed
	}
	return err
}

func (a *remoteAttachment) Done() <-chan struct{} { return a.done }

func (a *remoteAttachment) Exit() (int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.code, a.exited
}
