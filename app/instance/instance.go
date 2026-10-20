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
// It also listens on a channel only the user can open: a Unix-domain socket in
// the same directory (directory 0700, socket 0600), or a named pipe on Windows.
// A second copy fails to take the lock, so it is the secondary, and Send
// connects to the channel.
//
// # What a request is
//
// A request is the secondary's arguments and working directory. The primary
// holds up to Options.Queue (16) accepted requests that it has not collected
// yet, in a ring it reserved when it started. A request is accepted only when
// it fits: the primary stores it, and then answers "accepted". When the ring is
// full it answers "busy" and stores nothing. The secondary retries a busy (or
// unanswered) request for Options.Timeout (2 s; at most 15 s) and then returns
// ErrBusy (or ErrNoAnswer, or ErrUncertain). So the primary sees exactly the
// requests whose senders were told "accepted", and every sender knows whether
// its request will be seen, except after ErrNoAnswer and ErrUncertain (below).
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
// The primary remembers the ID of every request it accepts, for at least 30
// seconds (longer than any Send keeps trying) and for as long as the request has
// not been polled, and answers a repeat "accepted" without storing it again. So
// an answer lost on the way (the connection broke after the request was stored)
// does not deliver the request twice. The memory is bounded (1024 IDs): when it is
// full of IDs that must still be kept, the primary answers "busy" rather than
// forget one. What Send returns says what is known:
//
//   - nil: the primary stored the request.
//   - ErrBusy, ErrRejected: nothing was stored.
//   - ErrUncertain: an earlier attempt of this Send reached the primary and its
//     answer never came, and a later one was told "busy"; the request may or may
//     not have been stored.
//   - ErrNoAnswer: no answer came before the timeout; the request may have been
//     stored and only the answer lost.
//
// # Who may connect
//
// On Unix the directory for the lock and the socket must be the user's own and
// private: a real directory (not a link) owned by this user, mode 0700
// (tightened when wider, and an error when that fails), and none of its ancestors
// may be used to swap it for something else: every ancestor, up to the root, must
// belong to this user or to root and must not be writable by group or others
// unless it has the sticky bit (like /tmp). Whatever its group: on macOS every user
// is in "staff". Symbolic links are resolved once, and the lock, the socket and
// every later operation use only the resolved path; a link retargeted afterwards
// changes nothing. If any check fails Acquire returns ErrUntrusted and changes
// nothing. The lock file is opened without following links and must be a regular
// file of this user with no other names; it is only then locked, re-moded or
// written. The socket is given mode 0600. The primary also asks the system who is
// on the other end of every connection (SO_PEERCRED on Linux, getpeereid on macOS)
// and serves only this user's processes; if it cannot find out, it refuses.
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
// The lock is a named mutex in the session's namespace, and the channel is a
// named pipe, \\.\pipe\gnui-instance-<hash of the id and the user's SID>. The pipe
// is created with FILE_FLAG_FIRST_PIPE_INSTANCE, so a process that made a pipe of
// that name first makes Acquire fail instead of receiving the requests, with
// PIPE_REJECT_REMOTE_CLIENTS, and with a security descriptor that grants access to
// the current user's SID only. The listener always keeps an idle instance, so the
// name is never free while it runs. The primary finds the user of every client by
// impersonating it at the identification level (no process id, so none that can
// be reused) and serves only the same SID; a secondary finds the server's user
// from the server's process before it sends anything. Every call on the pipe is
// overlapped and cancelled, and waited for, when its deadline passes. No
// directory is used on Windows: Options.Dir is not read as a path and only
// separates one instance from another (tests use it), so its permissions do not
// matter.
package instance

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"gonativeui/app/paths"
	"gonativeui/platform"
)

var (
	// ErrBusy is Send's error when the primary answered "busy" (its queue was
	// full) until the timeout. It means the request was not stored.
	ErrBusy = errors.New("instance: the running copy is busy")
	// ErrNoAnswer is Send's error when the primary did not answer: nothing
	// listened on its socket, or it accepted the connection and said nothing,
	// until the timeout.
	ErrNoAnswer = errors.New("instance: the running copy did not answer")
	// ErrRejected is Send's error when the primary refused the request as
	// malformed or too large. Retrying the same request cannot help.
	ErrRejected = errors.New("instance: the running copy rejected the request")
	// ErrUncertain is Send's error when an earlier attempt of the same Send
	// reached the primary without getting an answer, and a later one was told
	// that the primary is busy: the request may or may not have been stored.
	ErrUncertain = errors.New("instance: it is not known whether the running copy took the request")
	// ErrUntrusted is Acquire's error when the directory for the lock and the
	// socket, or the lock file in it, is not private to this user: it is a link
	// or belongs to someone else, its mode cannot be made 0700, or a parent of
	// it can be written by other users. Nothing is changed in what is refused.
	ErrUntrusted = errors.New("instance: not a private directory or file of this user")
)

// Options configures Acquire. The zero value is usable.
type Options struct {
	// Dir is the directory for the lock file and the socket. "" means
	// paths.Data(id). It is created with mode 0700. It is not used on Windows
	// except to tell instances apart (see the package comment).
	Dir string
	// Queue is how many accepted requests the primary holds until they are
	// polled. 0 means 16.
	Queue int
	// Timeout is how long Send keeps trying when the primary is busy or silent.
	// 0 means 2 s; more than 15 s is taken as 15 s (the primary remembers a
	// request's ID for 30).
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

// The primary's memory of accepted request IDs. Variables so that tests can
// shrink them.
var (
	// dedupSize is how many IDs it holds.
	dedupSize = 1024
	// dedupHorizon is the least time an ID is kept: longer than the longest
	// time Send keeps retrying (maxTimeout) with a margin.
	dedupHorizon = 30 * time.Second
	// nowFn is the clock for it.
	nowFn = time.Now
)

// maxTimeout caps Options.Timeout.
const maxTimeout = 15 * time.Second

// A conn is one connection, in either direction. A listener accepts them.
// Unix-domain sockets and Windows named pipes both fit.
type (
	conn interface {
		io.ReadWriteCloser
		SetDeadline(time.Time) error
	}
	listener interface {
		Accept() (conn, error)
		Close() error
	}
)

// Acquire becomes the primary instance of the application id, or finds that
// another process is: exactly one of primary and secondary is non-nil when err
// is nil. It does file system I/O, and for the primary it starts listening. Call
// it once, early, before opening a window.
func Acquire(id string) (*Primary, *Secondary, error) { return AcquireWith(id, Options{}) }

// AcquireWith is Acquire with options.
func AcquireWith(id string, o Options) (*Primary, *Secondary, error) {
	if !paths.ValidID(id) {
		return nil, nil, fmt.Errorf("%w: %q", paths.ErrInvalidID, id)
	}
	if o.Queue <= 0 {
		o.Queue = 16
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Second
	}
	o.Timeout = min(o.Timeout, maxTimeout)
	lockKey, addr, err := setup(id, o)
	if err != nil {
		return nil, nil, err
	}
	lk, got, err := acquireLock(lockKey)
	if err != nil {
		return nil, nil, err
	}
	if !got {
		return nil, &Secondary{addr: addr, timeout: o.Timeout}, nil
	}
	// The lock is ours: whatever is left at the address belongs to a copy that is gone.
	ln, err := listen(addr)
	if err != nil {
		lk.release()
		return nil, nil, fmt.Errorf("instance: listening on %s: %w", addr, err)
	}
	p := &Primary{ln: ln, lk: lk, addr: addr, ring: make([]Request, 0, o.Queue), limit: o.Queue,
		seen: make(map[string]*seenEntry, dedupSize)}
	p.wg.Add(1)
	go p.serve()
	return p, nil, nil
}

// Primary is the instance that holds the lock and takes requests.
type Primary struct {
	ln   listener
	lk   lock
	addr string

	mu     sync.Mutex
	ring   []Request    // accepted, not yet polled; never grows past limit
	ringE  []*seenEntry // the IDs of the requests in ring
	limit  int
	closed bool
	// The IDs of accepted requests, oldest first in order: a repeat of one of
	// them is answered "accepted" and stored once.
	seen  map[string]*seenEntry
	order []*seenEntry

	serial atomic.Uint32
	once   sync.Once
	wg     sync.WaitGroup
}

// A seenEntry is the memory of one accepted request.
type seenEntry struct {
	id     string
	at     time.Time
	polled bool // no longer in the ring
}

// euid is the user whose files and peers are trusted.
func euid() int { return os.Geteuid() + int(euidShift.Load()) }

// wake is platform.Wake; peerHook replaces the question about who is on the other
// end of a connection, and dropAcks makes the primary close a connection after
// accepting without answering: tests set them.
var (
	euidShift atomic.Int32 // tests add to the user id, to be taken for another user
	wake      = platform.Wake
	peerHook  atomic.Pointer[func(conn) error]
	dropAcks  atomic.Int32
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
			c.SetDeadline(time.Now().Add(ioTimeout))
			c.Write([]byte{replyBusy})
			c.Close()
		}
	}
}

// handle reads one request and answers it.
func (p *Primary) handle(c conn) {
	defer c.Close()
	// Only this user's processes are served. Where the system cannot say who is
	// on the other end, the private directory is what keeps others out; where it
	// can say and fails to, the connection is refused.
	c.SetDeadline(time.Now().Add(ioTimeout))
	if peerOf(c) != nil {
		return
	}
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
// one whose answer was lost: it is answered "accepted" and not stored again. An
// ID is forgotten only when it is old enough and its request has been polled; if
// the table is full of IDs that must stay, the answer is "busy".
func (p *Primary) offer(id string, r Request) byte {
	now := nowFn()
	p.mu.Lock()
	if _, dup := p.seen[id]; dup {
		p.mu.Unlock()
		return replyAccepted
	}
	if p.closed || len(p.ring) >= p.limit {
		p.mu.Unlock()
		return replyBusy
	}
	for len(p.order) >= dedupSize {
		oldest := p.order[0]
		if !oldest.polled || now.Sub(oldest.at) < dedupHorizon {
			p.mu.Unlock()
			return replyBusy
		}
		delete(p.seen, oldest.id)
		p.order[0] = nil
		p.order = p.order[1:]
	}
	e := &seenEntry{id: id, at: now}
	p.seen[id] = e
	p.order = append(p.order, e)
	p.ring = append(p.ring, r)
	p.ringE = append(p.ringE, e)
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
	for _, e := range p.ringE {
		e.polled = true // from now on its ID may be forgotten, after the horizon
	}
	clear(p.ring)
	clear(p.ringE)
	p.ring, p.ringE = p.ring[:0], p.ringE[:0]
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
		p.ln.Close()
		p.wg.Wait()
		removeEndpoint(p.addr)
		p.lk.release()
	})
	return nil
}

// Secondary is a copy that found another one running.
type Secondary struct {
	addr    string
	timeout time.Duration
}

// Send hands args (and the working directory) to the primary. It returns nil
// once the primary has accepted the request, and ErrBusy, ErrNoAnswer,
// ErrUncertain or ErrRejected otherwise (see the package comment for what each
// says about whether the request was stored). It blocks for up to the
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
	maybe := false // an attempt delivered the request and got no answer
	for wait := 25 * time.Millisecond; ; wait = min(wait*2, 200*time.Millisecond) {
		if time.Until(deadline) <= 0 {
			return last // the sleep used up the time: report the last answer, not a new one
		}
		reply, sent, err := s.try(frame, deadline)
		if sent && err != nil {
			maybe = true
		}
		switch {
		case err == nil && reply == replyAccepted:
			return nil
		case err == nil && reply == replyRejected:
			return ErrRejected
		case err == nil && maybe:
			last = ErrUncertain
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

// try makes one attempt: connect, send, read the one-byte answer. sent says the
// request was written to the connection, so the primary may have it whatever
// happened next.
func (s *Secondary) try(frame []byte, deadline time.Time) (reply byte, sent bool, err error) {
	limit := min(time.Until(deadline), ioTimeout)
	if limit <= 0 {
		return 0, false, ErrNoAnswer
	}
	c, err := dial(s.addr, limit)
	if err != nil {
		return 0, false, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(limit))
	if _, err := c.Write(frame); err != nil {
		return 0, false, err
	}
	var b [1]byte
	if _, err := io.ReadFull(c, b[:]); err != nil {
		return 0, true, err
	}
	return b[0], true, nil
}

// peerOf asks whether the process on the other end of c is this user's: nil if
// it is (or if the system cannot tell and the channel's own protection is what
// keeps others out), an error to refuse. A test can replace the question.
func peerOf(c conn) error {
	if h := peerHook.Load(); h != nil {
		return (*h)(c)
	}
	return peerCheck(c)
}
