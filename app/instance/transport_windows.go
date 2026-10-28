//go:build windows

package instance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// The Windows transport: a named pipe that only the current user can open.
//
//   - The name is derived from the application id, the user's SID and the
//     session, so users and sessions never share one.
//   - The first instance is created with FILE_FLAG_FIRST_PIPE_INSTANCE, so a
//     process that made a pipe of that name earlier makes Acquire fail instead of
//     receiving the requests. The listener always keeps an idle instance until it
//     is closed, so the name never goes free in between.
//   - It is created with PIPE_REJECT_REMOTE_CLIENTS and with a DACL that grants
//     access to the user's SID only (and so to no other user, administrators
//     included).
//   - The server finds the user of every client by impersonating it at the
//     identification level, and the client finds the owner of the pipe from its
//     own handle (the pipe is created with the user as its owner). No process id
//     is involved on either side, so one that is reused cannot give another
//     answer. Both compare the SID with the user's own.
//   - Every handle is overlapped. Each call has its own event and OVERLAPPED, is
//     waited for up to its deadline, and when the deadline passes (or the
//     connection or listener is closed) it is cancelled with CancelIoEx and
//     waited for until it has completed, so nothing touches a buffer, an
//     OVERLAPPED or a handle after the call has returned. The buffers the
//     kernel reads and writes are never the caller's: each connection owns two,
//     in memory from VirtualAlloc (outside the Go heap and the goroutine stacks,
//     so it neither moves nor is collected), and data is copied between them and
//     the caller's slices while the call is still registered with the
//     connection, which frees the memory only after every call has been joined.

var (
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")

	getNamedPipeServerPID = kernel32.NewProc("GetNamedPipeServerProcessId")
	allowSetForeground    = user32.NewProc("AllowSetForegroundWindow")

	createNamedPipeW    = kernel32.NewProc("CreateNamedPipeW")
	connectNamedPipe    = kernel32.NewProc("ConnectNamedPipe")
	waitNamedPipeW      = kernel32.NewProc("WaitNamedPipeW")
	cancelIoEx          = kernel32.NewProc("CancelIoEx")
	getOverlappedResult = kernel32.NewProc("GetOverlappedResult")
	createEventW        = kernel32.NewProc("CreateEventW")
	setEvent            = kernel32.NewProc("SetEvent")
	waitForMultiple     = kernel32.NewProc("WaitForMultipleObjects")
	virtualAlloc        = kernel32.NewProc("VirtualAlloc")
	virtualFree         = kernel32.NewProc("VirtualFree")
	processIDToSession  = kernel32.NewProc("ProcessIdToSessionId")
	localFree           = kernel32.NewProc("LocalFree")

	convertStringSD       = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	impersonatePipeClient = advapi32.NewProc("ImpersonateNamedPipeClient")
	revertToSelf          = advapi32.NewProc("RevertToSelf")
	openThreadToken       = advapi32.NewProc("OpenThreadToken")
	getSecurityInfo       = advapi32.NewProc("GetSecurityInfo")
	convertSidToStringSid = advapi32.NewProc("ConvertSidToStringSidW")
)

const (
	pipeAccessDuplex        = 0x00000003
	fileFlagFirstInstance   = 0x00080000
	fileFlagOverlapped      = 0x40000000
	pipeTypeByte            = 0x0
	pipeWait                = 0x0
	pipeRejectRemoteClients = 0x8
	pipeUnlimitedInstances  = 255
	securitySQOSPresent     = 0x00100000
	securityIdentification  = 0x00010000
	tokenQuery              = 0x8
	seKernelObject          = 6
	ownerSecurityInfo       = 1

	memCommitReserve = 0x3000 // MEM_COMMIT | MEM_RESERVE
	memRelease       = 0x8000
	pageReadWrite    = 0x04
	ioBufSize        = 4096 // each of a connection's two buffers

	waitObject0 = 0
	waitTimeout = 0x102
	infinite    = 0xFFFFFFFF

	errIOPending     = syscall.Errno(997)
	errPipeBusy      = syscall.Errno(231)
	errPipeConnected = syscall.Errno(535)
	errNoData        = syscall.Errno(232)
	errNotConnected  = syscall.Errno(233)
	errBrokenPipe    = syscall.Errno(109)
	errAborted       = syscall.Errno(995)
	errNoSysRes      = syscall.Errno(1450)
)

// Hooks for the tests, which force interleavings that are otherwise a matter of
// luck. All are nil or false in production.
var (
	// ioBeforeSubmit runs after a call is registered with its connection and
	// before it is submitted; ioBeforeWait after it is submitted and before it
	// is waited for.
	ioBeforeSubmit atomic.Pointer[func()]
	ioBeforeWait   atomic.Pointer[func()]
	// beforeCreateNext runs in Accept before it creates the replacement
	// instance.
	beforeCreateNext atomic.Pointer[func()]
	// failCreate makes the creation of every instance but the first fail.
	failCreate atomic.Bool
	// ownerHook replaces the reading of the owner of a pipe by its client.
	ownerHook atomic.Pointer[func(syscall.Handle) (string, error)]
)

func runHook(h *atomic.Pointer[func()]) {
	if f := h.Load(); f != nil {
		(*f)()
	}
}

// sidOverride replaces the user's SID for the checks: tests set it, to be taken
// for another user.
var sidOverride atomic.Pointer[string]

// userSID is the string form of the current user's SID.
func userSID() (string, error) {
	if s := sidOverride.Load(); s != nil {
		return *s, nil
	}
	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer tok.Close()
	return tokenSID(tok)
}

func tokenSID(tok syscall.Token) (string, error) {
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String()
}

func sessionID() uint32 {
	var s uint32
	processIDToSession.Call(uintptr(os.Getpid()), uintptr(unsafe.Pointer(&s)))
	return s
}

// setup returns the key of the lock and the name of the pipe. Options.Dir is not
// a path here; it only tells instances apart.
func setup(id string, o Options) (lockKey, addr string, err error) {
	sid, err := userSID()
	if err != nil {
		return "", "", fmt.Errorf("instance: finding the user: %w", err)
	}
	sum := sha256.Sum256([]byte(id + "\x00" + sid + "\x00" + strconv.Itoa(int(sessionID())) + "\x00" + o.Dir))
	h := hex.EncodeToString(sum[:12])
	return "pipe:" + h, `\\.\pipe\gnui-instance-` + h, nil
}

func removeEndpoint(string) {}

// ---- overlapped calls ------------------------------------------------------------

var (
	errClosed         = errors.New("instance: use of a closed connection")
	errListenerClosed = errors.New("instance: listener closed")
)

func newEvent() (syscall.Handle, error) {
	h, _, e := createEventW.Call(0, 1, 0, 0) // manual reset, not signalled
	if h == 0 {
		return 0, e
	}
	return syscall.Handle(h), nil
}

// cancel asks for the call ov on h to be cancelled; it may already be done.
func cancel(h syscall.Handle, ov *syscall.Overlapped) {
	cancelIoEx.Call(uintptr(h), uintptr(unsafe.Pointer(ov)))
}

// await waits for the overlapped call ov, which has been submitted on h, until
// the deadline (none if zero) or until the event abort (if not 0) is signalled.
// In either case the call is cancelled, and await returns only when it has
// completed, so the caller may reuse its buffer and ov: os.ErrDeadlineExceeded
// for the deadline, errClosed for abort. If the call completed regardless, its
// result is returned.
func await(h syscall.Handle, ov *syscall.Overlapped, deadline time.Time, abort syscall.Handle) (uint32, error) {
	runHook(&ioBeforeWait)
	handles := [2]syscall.Handle{ov.HEvent, abort}
	count := uintptr(1)
	if abort != 0 {
		count = 2
	}
	ms := uintptr(infinite)
	if !deadline.IsZero() {
		ms = uintptr(max(0, min(time.Until(deadline)/time.Millisecond, time.Duration(infinite-1))))
	}
	r, _, _ := waitForMultiple.Call(count, uintptr(unsafe.Pointer(&handles[0])), 0, ms)
	timedOut, aborted := false, false
	switch r {
	case waitObject0:
	case waitObject0 + 1:
		aborted = true
	case waitTimeout:
		timedOut = true
	default: // WAIT_FAILED: give up on the call
		aborted = true
	}
	if timedOut || aborted {
		cancel(h, ov)
	}
	var n uint32
	if r, _, e := getOverlappedResult.Call(uintptr(h), uintptr(unsafe.Pointer(ov)), uintptr(unsafe.Pointer(&n)), 1); r == 0 {
		switch {
		case e == errAborted && timedOut:
			return n, os.ErrDeadlineExceeded
		case e == errAborted && aborted:
			return n, errClosed
		}
		return n, e
	}
	return n, nil
}

// ---- the server ------------------------------------------------------------------

type pipeListener struct {
	addr string
	name *uint16

	mu      sync.Mutex
	sd      uintptr // the security descriptor, from LocalAlloc; freed by Close
	sa      syscall.SecurityAttributes
	next    syscall.Handle // an idle instance: it keeps the name until Close
	closed  bool
	wg      sync.WaitGroup // the Accept calls, which create instances with sd
	closeEv syscall.Handle // signalled by Close
}

// listen creates the first instance, which fails if the name is taken, owned by
// the user (not by the administrators group, which an elevated process would
// otherwise own it as) and with the user-only DACL.
func listen(addr string) (listener, error) {
	sid, err := userSID()
	if err != nil {
		return nil, err
	}
	name, err := syscall.UTF16PtrFromString(addr)
	if err != nil {
		return nil, err
	}
	sddl, err := syscall.UTF16PtrFromString("O:" + sid + "D:P(A;;GA;;;" + sid + ")")
	if err != nil {
		return nil, err
	}
	l := &pipeListener{addr: addr, name: name}
	if r, _, e := convertStringSD.Call(uintptr(unsafe.Pointer(sddl)), 1, uintptr(unsafe.Pointer(&l.sd)), 0); r == 0 {
		return nil, fmt.Errorf("building the security descriptor: %w", e)
	}
	l.sa = syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), SecurityDescriptor: l.sd}
	if l.closeEv, err = newEvent(); err != nil {
		localFree.Call(l.sd)
		return nil, err
	}
	h, err := l.create(true)
	if err != nil {
		syscall.CloseHandle(l.closeEv)
		localFree.Call(l.sd)
		return nil, fmt.Errorf("creating the pipe (is the name taken?): %w", err)
	}
	l.next = h
	return l, nil
}

// create makes an instance. Only the first has FILE_FLAG_FIRST_PIPE_INSTANCE: the
// others are made while an instance still holds the name (see Accept). The caller
// holds l.mu, or is listen, and l.sd is still allocated.
func (l *pipeListener) create(first bool) (syscall.Handle, error) {
	mode := uintptr(pipeAccessDuplex | fileFlagOverlapped)
	if first {
		mode |= fileFlagFirstInstance
	} else if failCreate.Load() {
		return 0, errNoSysRes
	}
	h, _, e := createNamedPipeW.Call(uintptr(unsafe.Pointer(l.name)), mode,
		pipeTypeByte|pipeWait|pipeRejectRemoteClients, pipeUnlimitedInstances, 4096, 4096, 0,
		uintptr(unsafe.Pointer(&l.sa)))
	if syscall.Handle(h) == syscall.InvalidHandle {
		return 0, e
	}
	return syscall.Handle(h), nil
}

// Accept waits for a client. The idle instance l.next is what the client will
// find; before it is used, another is created to take its place, so that an
// instance (and with it the name) exists at every moment. If that cannot be
// created, the idle one stays where it is, nothing is accepted, and creation is
// tried again until it works or the listener is closed.
func (l *pipeListener) Accept() (conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, errListenerClosed
	}
	l.wg.Add(1) // Close waits for this call before it frees the descriptor
	l.mu.Unlock()
	defer l.wg.Done()

	var h syscall.Handle
	for {
		runHook(&beforeCreateNext)
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return nil, errListenerClosed
		}
		nh, err := l.create(false)
		if err == nil {
			h, l.next = l.next, nh
		}
		l.mu.Unlock()
		if err == nil {
			break
		}
		if r, _, _ := waitForMultiple.Call(1, uintptr(unsafe.Pointer(&l.closeEv)), 0, 20); r == waitObject0 {
			return nil, errListenerClosed
		}
	}

	ev, err := newEvent()
	if err != nil {
		syscall.CloseHandle(h)
		return nil, err
	}
	defer syscall.CloseHandle(ev)
	ov := &syscall.Overlapped{HEvent: ev}
	defer runtime.KeepAlive(ov) // until the call has been joined, whichever way Accept returns
	r, _, e := connectNamedPipe.Call(uintptr(h), uintptr(unsafe.Pointer(ov)))
	switch {
	case r != 0, e == errPipeConnected, e == errNoData:
		// Connected (a client that has gone again is seen as an end of file).
	case e == errIOPending:
		if _, err := await(h, ov, time.Time{}, l.closeEv); err != nil && err != errNoData {
			syscall.CloseHandle(h)
			if err == errClosed {
				return nil, errListenerClosed
			}
			return nil, err
		}
	default:
		syscall.CloseHandle(h)
		return nil, e
	}
	pc, err := newPipeConn(h, true)
	if err != nil {
		return nil, err
	}
	return pc, nil
}

// Close stops the listener. It returns when no Accept is still using it; only
// then are the idle instance and the security descriptor released.
func (l *pipeListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()
	setEvent.Call(uintptr(l.closeEv))
	l.wg.Wait()
	l.mu.Lock()
	h, sd := l.next, l.sd
	l.next, l.sd = 0, 0
	l.mu.Unlock()
	if h != 0 {
		syscall.CloseHandle(h)
	}
	if sd != 0 {
		localFree.Call(sd)
	}
	syscall.CloseHandle(l.closeEv)
	return nil
}

// ---- a connection ----------------------------------------------------------------

// An ioOp is one overlapped call in progress: the OVERLAPPED and the buffer the
// kernel is using, together, from the submission until the completion has been
// joined (the record is on the heap and referenced from the connection until
// then, and kept alive after the join). buf is a slice of the connection's
// VirtualAlloc'd memory, never of the caller's slice.
type ioOp struct {
	ov  syscall.Overlapped
	buf []byte
}

type pipeConn struct {
	h      syscall.Handle
	server bool
	wrote  bool   // a server that has answered waits for the client to go before it closes
	pre    []byte // read ahead by peerCheck, returned first by Read

	// The memory the kernel reads and writes: rbuf for reads, wbuf for writes. It
	// is freed by Close, after every call has been joined.
	mem        uintptr
	rbuf, wbuf []byte
	rmu, wmu   sync.Mutex // one read and one write at a time, each with its own buffer

	mu       sync.Mutex
	deadline time.Time
	closed   bool
	pending  map[*ioOp]struct{} // the calls in progress, for Close to cancel
	ops      sync.WaitGroup     // the calls in progress, for Close to wait for
}

// newPipeConn takes over the handle h (closing it if it fails).
func newPipeConn(h syscall.Handle, server bool) (*pipeConn, error) {
	r, _, e := virtualAlloc.Call(0, 2*ioBufSize, memCommitReserve, pageReadWrite)
	if r == 0 {
		syscall.CloseHandle(h)
		return nil, fmt.Errorf("instance: allocating the pipe's buffers: %w", e)
	}
	// An address outside the Go heap, not a pointer the garbage collector knows.
	p := *(*unsafe.Pointer)(unsafe.Pointer(&r))
	mem := unsafe.Slice((*byte)(p), 2*ioBufSize)
	return &pipeConn{h: h, server: server, mem: r, rbuf: mem[:ioBufSize:ioBufSize], wbuf: mem[ioBufSize:],
		pending: map[*ioOp]struct{}{}}, nil
}

func (c *pipeConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}

// do runs one overlapped call on buf (one of the connection's buffers), with the
// connection's deadline. A Close that arrives at any point cancels it. after, if
// not nil, runs on success while the call is still registered, so the buffer
// cannot be freed under it.
func (c *pipeConn) do(buf []byte, start func(op *ioOp) error, after func(n uint32)) (uint32, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, errClosed
	}
	ev, err := newEvent()
	if err != nil {
		c.mu.Unlock()
		return 0, err
	}
	op := &ioOp{buf: buf}
	op.ov.HEvent = ev
	c.pending[op] = struct{}{}
	c.ops.Add(1)
	d := c.deadline
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, op)
		c.mu.Unlock()
		syscall.CloseHandle(ev)
		c.ops.Done()
	}()

	runHook(&ioBeforeSubmit)
	if err := start(op); err != nil && err != errIOPending {
		return 0, err
	}
	// A Close that came before the call was submitted found nothing to cancel.
	c.mu.Lock()
	if c.closed {
		cancel(c.h, &op.ov)
	}
	c.mu.Unlock()
	n, err := await(c.h, &op.ov, d, 0)
	runtime.KeepAlive(op) // the record, and with it the buffer's slice, until the call is joined
	if err == nil && after != nil {
		after(n)
	}
	return n, err
}

func (c *pipeConn) readRaw(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	buf := c.rbuf[:min(len(p), len(c.rbuf))]
	// The count of bytes is read from GetOverlappedResult: the call is given nil, so
	// the kernel has no Go variable to write to later.
	n, err := c.do(buf, func(op *ioOp) error { return syscall.ReadFile(c.h, op.buf, nil, &op.ov) },
		func(n uint32) { copy(p, buf[:n]) })
	switch {
	case err == errBrokenPipe || err == errNotConnected:
		return 0, io.EOF
	case err != nil:
		return 0, err
	case n == 0:
		return 0, io.EOF
	}
	return int(n), nil
}

func (c *pipeConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.pre) > 0 {
		n := copy(p, c.pre)
		c.pre = c.pre[n:]
		return n, nil
	}
	return c.readRaw(p)
}

func (c *pipeConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	done := 0
	for done < len(p) {
		buf := c.wbuf[:min(len(p)-done, len(c.wbuf))]
		copy(buf, p[done:]) // into the connection's memory, before the call is made
		n, err := c.do(buf, func(op *ioOp) error { return syscall.WriteFile(c.h, op.buf, nil, &op.ov) }, nil)
		if err != nil {
			return done, err
		}
		done += int(n)
	}
	c.wrote = true
	return done, nil
}

// Close ends the connection: the calls in progress are cancelled and waited for,
// and only then are the handle closed and the memory freed. A server that has
// answered first waits (for a second at most) until the client has read the
// answer and gone, which is how it knows the answer was not lost with the pipe.
func (c *pipeConn) Close() error {
	if c.server && c.wrote && c.rmu.TryLock() {
		c.rmu.Unlock()
		c.SetDeadline(time.Now().Add(time.Second))
		var b [1]byte
		c.readRaw(b[:])
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	for op := range c.pending {
		cancel(c.h, &op.ov)
	}
	c.mu.Unlock()
	c.ops.Wait()
	err := syscall.CloseHandle(c.h)
	virtualFree.Call(c.mem, 0, memRelease)
	return err
}

// ---- who is on the other end -----------------------------------------------------

// clientSID is the SID of the user of the client, found by impersonating the
// client at the identification level and reading the user of the thread's token.
// No process id is used, so a process id that is reused cannot give another
// answer. The system lets a server do this only after it has read from the pipe.
// The thread is locked to the goroutine while it impersonates, and always reverts:
// if that fails, the thread is never given back and ends with the goroutine.
func (c *pipeConn) clientSID() (string, error) {
	runtime.LockOSThread()
	if r, _, e := impersonatePipeClient.Call(uintptr(c.h)); r == 0 {
		runtime.UnlockOSThread()
		return "", fmt.Errorf("instance: cannot identify the client: %w", e)
	}
	defer func() {
		if r, _, _ := revertToSelf.Call(); r != 0 {
			runtime.UnlockOSThread()
		}
	}()
	var tok syscall.Token
	const currentThread = ^uintptr(1) // the pseudo handle -2
	if r, _, e := openThreadToken.Call(currentThread, tokenQuery, 1, uintptr(unsafe.Pointer(&tok))); r == 0 {
		return "", fmt.Errorf("instance: cannot read the client's token: %w", e)
	}
	defer tok.Close()
	return tokenSID(tok)
}

// peerCheck refuses a client that is not a process of this user. It first reads
// what the client has sent (Read returns it afterwards), because the system does
// not allow the client to be impersonated before that.
func peerCheck(c conn) error {
	pc, ok := c.(*pipeConn)
	if !ok {
		return nil
	}
	buf := make([]byte, 4096)
	n, err := pc.readRaw(buf)
	if err != nil {
		return err
	}
	pc.pre = buf[:n]
	theirs, err := pc.clientSID()
	if err != nil {
		return err
	}
	mine, err := userSID()
	if err != nil {
		return err
	}
	if theirs != mine {
		return fmt.Errorf("instance: the client belongs to %s", theirs)
	}
	return nil
}

// ---- the client ------------------------------------------------------------------

// dialRaw connects to the pipe without looking at who made it. The client allows
// the server to know who it is but not to act as it (SECURITY_IDENTIFICATION).
func dialRaw(addr string, timeout time.Duration) (conn, error) {
	name, err := syscall.UTF16PtrFromString(addr)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		h, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
			syscall.OPEN_EXISTING, securitySQOSPresent|securityIdentification|fileFlagOverlapped, 0)
		if err == nil {
			pc, err := newPipeConn(h, false)
			if err != nil {
				return nil, err
			}
			return pc, nil
		}
		left := time.Until(deadline)
		if err != errPipeBusy || left <= 0 {
			return nil, err
		}
		waitNamedPipeW.Call(uintptr(unsafe.Pointer(name)), uintptr(min(left, 50*time.Millisecond)/time.Millisecond))
	}
}

// dial connects and checks that the server is this user's: the pipe is owned by
// the user (listen sets that), and the owner is read from this connection's own
// handle, so it is the owner of the very pipe that was connected, not of whatever
// process a process id names later.
func dial(addr string, timeout time.Duration) (conn, error) {
	c, err := dialRaw(addr, timeout)
	if err != nil {
		return nil, err
	}
	pc := c.(*pipeConn)
	if err := checkServer(pc); err != nil {
		c.Close()
		return nil, err
	}
	letServerToFront(pc.h)
	return c, nil
}

// letServerToFront lets the process at the other end of the pipe (checked to
// be this user's) take the foreground (AllowSetForegroundWindow), so the
// primary can bring its window to the front for the request (D68): Windows
// grants the foreground only to the process the user works with, which is
// this one. A process id is used here only for that right, which the
// process the user launched may give; a failure changes nothing else.
func letServerToFront(h syscall.Handle) {
	var pid uint32
	if r, _, _ := getNamedPipeServerPID.Call(uintptr(h), uintptr(unsafe.Pointer(&pid))); r == 0 || pid == 0 {
		return
	}
	foregroundGiven.Store(pid)
	allowSetForeground.Call(uintptr(pid))
}

// foregroundGiven is the last process id letServerToFront gave the
// foreground right to (tests).
var foregroundGiven atomic.Uint32

func checkServer(c *pipeConn) error {
	owner, err := pipeOwnerSID(c.h)
	if err != nil {
		return fmt.Errorf("instance: cannot find the owner of the pipe: %w", err)
	}
	mine, err := userSID()
	if err != nil {
		return err
	}
	if owner != mine {
		return fmt.Errorf("instance: the pipe belongs to %s", owner)
	}
	return nil
}

// pipeOwnerSID is the owner of the pipe object behind the handle h.
func pipeOwnerSID(h syscall.Handle) (string, error) {
	if f := ownerHook.Load(); f != nil {
		return (*f)(h)
	}
	var owner, sd uintptr
	if r, _, _ := getSecurityInfo.Call(uintptr(h), seKernelObject, ownerSecurityInfo,
		uintptr(unsafe.Pointer(&owner)), 0, 0, 0, uintptr(unsafe.Pointer(&sd))); r != 0 {
		return "", syscall.Errno(r)
	}
	defer localFree.Call(sd) // the owner SID points into it
	var str *uint16
	if r, _, e := convertSidToStringSid.Call(owner, uintptr(unsafe.Pointer(&str))); r == 0 {
		return "", e
	}
	defer localFree.Call(uintptr(unsafe.Pointer(str)))
	n := 0
	for *(*uint16)(unsafe.Add(unsafe.Pointer(str), 2*n)) != 0 {
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice(str, n)), nil
}
