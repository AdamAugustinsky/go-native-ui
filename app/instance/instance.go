// Package instance keeps an application to one running copy per user, and lets
// a second launch hand its command line to the first.
//
//	primary, secondary, err := instance.Acquire(appID)
//	if err != nil { /* cannot tell: run anyway, or report */ }
//	if secondary != nil {
//		// Another copy is running: ask it to open our arguments, and leave.
//		if err := secondary.Send(os.Args[1:]); err != nil {
//			log.Printf("the running copy did not take the request: %v", err)
//		}
//		return
//	}
//	defer primary.Close()
//	// in the loop (a wake arrives when a request has):
//	for _, r := range primary.Poll(buf[:0]) { openFiles(r.Dir, r.Args) }
//
// # How it works
//
// The first copy takes a lock that only one process can hold and that the OS
// releases when the process ends, however it ends: flock on a file in the
// application's data directory (paths.Data) on Unix, a named mutex on Windows.
// It also listens on a socket in the same directory, which only the user can
// open (directory 0700, socket 0600). A second copy fails to take the lock, so
// it is the secondary, and Send connects to the socket.
//
// # What a request is
//
// A request is the secondary's arguments and working directory. The primary
// holds up to Options.Queue (16) accepted requests that it has not collected
// yet, in a ring it reserved when it started. A request is accepted only when
// it fits: the primary stores it, and then answers "accepted". When the ring is
// full it answers "busy" and stores nothing. The secondary retries a busy (or
// unanswered) request for Options.Timeout (2 s) and then returns ErrBusy (or
// ErrNoAnswer). So the primary sees exactly the requests whose senders were told
// "accepted", and every sender knows whether its request will be seen.
//
// # Delivery
//
// Until the application object (F3) exists, the primary collects requests with
// Poll, and each accepted request calls platform.Wake so that an idle loop
// notices. Serial counts accepted requests. With F3 the ring is the "outside
// requests" class (D37 class 3), accepted into the same reserved ring and
// delivered as an event; Poll stays as the poll form.
//
// # Lost answers
//
// Every Send gives its request an ID and repeats the same ID in its retries.
// The primary remembers the IDs of the last 256 requests it accepted and answers a
// repeat "accepted" without storing it again, so an answer lost on the way (the
// connection broke after the request was stored) does not deliver the request
// twice. The memory is bounded: a retry more than 256 accepted requests later is
// taken for a new request. A Send that returns ErrNoAnswer is uncertain: the
// primary may have stored the request and lost only the answer for the whole
// timeout. ErrBusy and ErrRejected are certain: nothing was stored.
//
// # Who may connect
//
// The directory for the lock and the socket must be the user's own and private:
// a real directory (not a link) owned by this user, mode 0700 (tightened when
// wider, and an error when that fails), with no parent that other users can
// write unless it has the sticky bit, like /tmp. Otherwise Acquire returns
// ErrUntrusted and changes nothing. The lock file is opened without following
// links and must be a regular file of this user with no other names; it is only
// then locked, re-moded or written. The socket is made in that directory and given
// mode 0600. The primary also asks the system who is on the other end of every
// connection (SO_PEERCRED on Linux, getpeereid on macOS) and serves only this
// user's processes; if it cannot find out, it refuses.
//
// # Crashes
//
// A primary that dies leaves its socket file behind, but not the lock: the next
// copy to start takes the lock, removes the stale socket and becomes the primary.
// A secondary that finds the lock held but nothing answering the socket (a
// primary that is starting, hung, or stuck) retries for the timeout and then
// returns ErrNoAnswer; it does not take over, because the lock says the
// primary is alive.
//
// # Windows
//
// The lock is a named mutex in the session's namespace. The request channel is
// a Unix-domain socket in the application's data directory (Windows 10 version
// 1803 and later), which that directory's access control list limits to the user,
// instead of a named pipe: it is the same code and the same tests as on the other
// systems. Two limits follow. The directory is checked for its owner (this user,
// the Administrators group or SYSTEM) but its access control list is not read; the
// directories the framework chooses are under the profile, whose list excludes
// other users, and a directory given in Options.Dir is the caller's to protect.
// And the primary cannot check who connects (the system call for it,
// SIO_AF_UNIX_GETPEERPID, exists only from Windows 11 and Server 2022), so the
// directory is what keeps other users out.
package instance

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"gonativeui/app/paths"
	"gonativeui/platform"
)

var (
	// ErrBusy is Send's error when the primary answered "busy" (its queue was
	// full) until the timeout.
	ErrBusy = errors.New("instance: the running copy is busy")
	// ErrNoAnswer is Send's error when the primary did not answer: nothing
	// listened on its socket, or it accepted the connection and said nothing,
	// until the timeout.
	ErrNoAnswer = errors.New("instance: the running copy did not answer")
	// ErrRejected is Send's error when the primary refused the request as
	// malformed or too large. Retrying the same request cannot help.
	ErrRejected = errors.New("instance: the running copy rejected the request")
	// ErrUntrusted is Acquire's error when the directory for the lock and the
	// socket, or the lock file in it, is not private to this user: it is a link
	// or belongs to someone else, its mode cannot be made 0700, or a parent of
	// it can be written by other users. Nothing is changed in what is refused.
	ErrUntrusted = errors.New("instance: not a private directory or file of this user")
)

// Options configures Acquire. The zero value is usable.
type Options struct {
	// Dir is the directory for the lock file and the socket. "" means
	// paths.Data(id). It is created with mode 0700.
	Dir string
	// Queue is how many accepted requests the primary holds until they are
	// polled. 0 means 16.
	Queue int
	// Timeout is how long Send keeps trying when the primary is busy or silent.
	// 0 means 2 s.
	Timeout time.Duration
}

// Request is one accepted request from a secondary instance.
type Request struct {
	// Args are the secondary's command-line arguments, without the program name.
	Args []string
	// Dir is the secondary's working directory, to resolve relative paths in Args.
	Dir string
}

// Limits of one request on the wire.
const (
	maxFrame = 64 << 10
	// handlers is how many connections the primary serves at once; the others
	// are answered "busy" without being read.
	handlers = 8

	replyAccepted = 'A'
	replyBusy     = 'B'
	replyRejected = 'R'

	ioTimeout = time.Second
)

// wire is the request as sent: a length-prefixed JSON object.
type wire struct {
	// ID names the request. A secondary sends the same ID with every retry of
	// one Send, so that the primary can tell a retry of a request it has
	// already accepted (the answer was lost) from a new one.
	ID   string   `json:"id"`
	Args []string `json:"args"`
	Dir  string   `json:"dir"`
}

// dedupSize is how many accepted request IDs the primary remembers.
const dedupSize = 256

// Acquire becomes the primary instance of the application id, or finds that
// another process is: exactly one of primary and secondary is non-nil when err
// is nil. It does file system I/O, and for the primary it starts listening. Call
// it once, early, before opening a window.
func Acquire(id string) (*Primary, *Secondary, error) { return AcquireWith(id, Options{}) }

// AcquireWith is Acquire with options.
func AcquireWith(id string, o Options) (*Primary, *Secondary, error) {
	if o.Queue <= 0 {
		o.Queue = 16
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Second
	}
	dir := o.Dir
	if dir == "" {
		var err error
		if dir, err = paths.Data(id); err != nil {
			return nil, nil, err
		}
	}
	if _, err := paths.Ensure(dir); err != nil {
		return nil, nil, err
	}
	if err := checkPrivate(dir); err != nil {
		return nil, nil, err
	}
	sock, err := socketPath(dir)
	if err != nil {
		return nil, nil, err
	}
	lk, got, err := acquireLock(filepath.Join(dir, "instance.lock"))
	if err != nil {
		return nil, nil, err
	}
	if !got {
		return nil, &Secondary{sock: sock, timeout: o.Timeout}, nil
	}
	// The lock is ours: whatever socket file is there belongs to a copy that is gone.
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		lk.release()
		return nil, nil, fmt.Errorf("instance: listening on %s: %w", sock, err)
	}
	// The directory is private (checked above), so no one else could reach the
	// socket while it had the mode the umask gave it.
	if err := os.Chmod(sock, 0o600); err != nil {
		ln.Close()
		lk.release()
		return nil, nil, fmt.Errorf("instance: setting the mode of %s: %w", sock, err)
	}
	p := &Primary{ln: ln, lk: lk, sock: sock, ring: make([]Request, 0, o.Queue), limit: o.Queue,
		seen: make(map[string]struct{}, dedupSize), seenOrder: make([]string, 0, dedupSize)}
	p.wg.Add(1)
	go p.serve()
	return p, nil, nil
}

// Primary is the instance that holds the lock and takes requests.
type Primary struct {
	ln   net.Listener
	lk   lock
	sock string

	mu     sync.Mutex
	ring   []Request // accepted, not yet polled; never grows past limit
	limit  int
	closed bool
	// The IDs of the last dedupSize accepted requests, oldest first in
	// seenOrder: a retry of one of them is answered "accepted" and stored once.
	seen      map[string]struct{}
	seenOrder []string

	serial atomic.Uint32
	once   sync.Once
	wg     sync.WaitGroup
}

// wake is platform.Wake; peerHook, which replaces the question about the socket peer's user, and dropAcks, which
// makes the primary close a connection after accepting without answering, are
// replaced or set by tests.
var (
	euid     = os.Geteuid // the user whose files and peers are trusted
	wake     = platform.Wake
	peerHook atomic.Pointer[func(net.Conn) (uint32, bool, error)]
	dropAcks atomic.Int32
)

func (p *Primary) serve() {
	defer p.wg.Done()
	sem := make(chan struct{}, handlers)
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return // closed
		}
		select {
		case sem <- struct{}{}:
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				defer func() { <-sem }()
				p.handle(c)
			}()
		default: // too many at once: tell the sender to come back
			c.SetWriteDeadline(time.Now().Add(ioTimeout))
			c.Write([]byte{replyBusy})
			c.Close()
		}
	}
}

// handle reads one request and answers it.
func (p *Primary) handle(c net.Conn) {
	defer c.Close()
	// Only this user's processes are served. Where the system cannot say who is
	// on the other end, the private directory is what keeps others out; where it
	// can say and fails to, the connection is refused.
	if uid, ok, err := peerOf(c); ok && (err != nil || int(uid) != euid()) {
		return
	}
	c.SetDeadline(time.Now().Add(ioTimeout))
	reply := byte(replyRejected)
	if id, req, ok := readRequest(c); ok {
		reply = p.offer(id, req)
	}
	if reply == replyAccepted && dropAcks.Add(-1) >= 0 {
		return // a test: the request is stored, the answer is lost
	}
	c.Write([]byte{reply})
}

func readRequest(r io.Reader) (id string, req Request, ok bool) {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return
	}
	size := binary.BigEndian.Uint32(n[:])
	if size == 0 || size > maxFrame {
		return
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return
	}
	var w wire
	if err := json.Unmarshal(buf, &w); err != nil || w.ID == "" || len(w.ID) > 64 {
		return
	}
	return w.ID, Request{Args: w.Args, Dir: w.Dir}, true
}

// offer stores the request if it fits. The answer is given only after the
// request is in the ring. A request whose ID was accepted before is a retry of
// one whose answer was lost: it is answered "accepted" and not stored again.
func (p *Primary) offer(id string, r Request) byte {
	p.mu.Lock()
	if _, dup := p.seen[id]; dup {
		p.mu.Unlock()
		return replyAccepted
	}
	if p.closed || len(p.ring) >= p.limit {
		p.mu.Unlock()
		return replyBusy
	}
	p.ring = append(p.ring, r)
	if len(p.seenOrder) == dedupSize {
		delete(p.seen, p.seenOrder[0])
		p.seenOrder = append(p.seenOrder[:0], p.seenOrder[1:]...)
	}
	p.seen[id] = struct{}{}
	p.seenOrder = append(p.seenOrder, id)
	p.mu.Unlock()
	p.serial.Add(1)
	wake()
	return replyAccepted
}

// Poll appends the accepted requests that have not been collected yet to dst,
// oldest first, and returns it. It never blocks, and any goroutine may call it.
func (p *Primary) Poll(dst []Request) []Request {
	p.mu.Lock()
	dst = append(dst, p.ring...)
	clear(p.ring)
	p.ring = p.ring[:0]
	p.mu.Unlock()
	return dst
}

// Serial counts the requests accepted so far.
func (p *Primary) Serial() uint32 { return p.serial.Load() }

// Close stops listening, removes the socket and releases the lock, so that the
// next copy to start becomes the primary. Requests accepted and not polled are
// dropped; poll first if they matter. It is safe to call twice.
func (p *Primary) Close() error {
	p.once.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		p.ln.Close() // removes the socket file as well, on Unix
		p.wg.Wait()
		os.Remove(p.sock)
		p.lk.release()
	})
	return nil
}

// Secondary is a copy that found another one running.
type Secondary struct {
	sock    string
	timeout time.Duration
}

// Send hands args (and the working directory) to the primary. It returns nil
// once the primary has accepted the request, and ErrBusy, ErrNoAnswer or
// ErrRejected otherwise (see the package comment). It blocks for up to the
// timeout: call it from main before any UI, or from a worker.
func (s *Secondary) Send(args []string) error {
	dir, _ := os.Getwd()
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	body, err := json.Marshal(wire{ID: hex.EncodeToString(raw[:]), Args: args, Dir: dir})
	if err != nil {
		return err
	}
	if len(body) > maxFrame {
		return fmt.Errorf("%w: %d bytes of arguments", ErrRejected, len(body))
	}
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	frame = append(frame, body...)

	deadline := time.Now().Add(s.timeout)
	last := ErrNoAnswer
	for wait := 25 * time.Millisecond; ; wait = min(wait*2, 200*time.Millisecond) {
		if time.Until(deadline) <= 0 {
			return last // the sleep used up the time: report the last answer, not a new one
		}
		reply, err := s.try(frame, deadline)
		switch {
		case err == nil && reply == replyAccepted:
			return nil
		case err == nil && reply == replyRejected:
			return ErrRejected
		case err == nil:
			last = ErrBusy
		default:
			last = ErrNoAnswer
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return last
		}
		time.Sleep(min(wait, remaining))
	}
}

// try makes one attempt: connect, send, read the one-byte answer.
func (s *Secondary) try(frame []byte, deadline time.Time) (byte, error) {
	limit := min(time.Until(deadline), ioTimeout)
	if limit <= 0 {
		return 0, ErrNoAnswer
	}
	c, err := net.DialTimeout("unix", s.sock, limit)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(limit))
	if _, err := c.Write(frame); err != nil {
		return 0, err
	}
	var b [1]byte
	if _, err := io.ReadFull(c, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

// peerOf asks who is on the other end of c: peerUID, or the test hook.
func peerOf(c net.Conn) (uint32, bool, error) {
	if h := peerHook.Load(); h != nil {
		return (*h)(c)
	}
	return peerUID(c)
}
