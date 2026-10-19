//go:build windows

package instance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
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
//     receiving the requests.
//   - It is created with PIPE_REJECT_REMOTE_CLIENTS and with a DACL that grants
//     access to the user's SID only (and so to no other user, administrators
//     included).
//   - The server checks the user of every client process, and the client the user
//     of the server process, against the user's own SID.

var (
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	createNamedPipeW   = kernel32.NewProc("CreateNamedPipeW")
	connectNamedPipe   = kernel32.NewProc("ConnectNamedPipe")
	waitNamedPipeW     = kernel32.NewProc("WaitNamedPipeW")
	cancelIoEx         = kernel32.NewProc("CancelIoEx")
	flushFileBuffers   = kernel32.NewProc("FlushFileBuffers")
	pipeClientPID      = kernel32.NewProc("GetNamedPipeClientProcessId")
	pipeServerPID      = kernel32.NewProc("GetNamedPipeServerProcessId")
	processIDToSession = kernel32.NewProc("ProcessIdToSessionId")
	localFree          = kernel32.NewProc("LocalFree")

	convertStringSD = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
)

const (
	pipeAccessDuplex        = 0x00000003
	fileFlagFirstInstance   = 0x00080000
	pipeTypeByte            = 0x0
	pipeWait                = 0x0
	pipeRejectRemoteClients = 0x8
	pipeUnlimitedInstances  = 255
	securitySQOSPresent     = 0x00100000
	securityIdentification  = 0x00010000
	processQueryLimited     = 0x1000
	tokenQuery              = 0x8

	errPipeBusy      = syscall.Errno(231)
	errPipeConnected = syscall.Errno(535)
	errNoData        = syscall.Errno(232)
	errBrokenPipe    = syscall.Errno(109)
	errAborted       = syscall.Errno(995)
	errFileNotFound  = syscall.Errno(2)
)

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

// processSID is the SID of the user of process pid.
func processSID(pid uint32) (string, error) {
	h, err := syscall.OpenProcess(processQueryLimited, false, pid)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)
	var tok syscall.Token
	if err := syscall.OpenProcessToken(h, tokenQuery, &tok); err != nil {
		return "", err
	}
	defer tok.Close()
	return tokenSID(tok)
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

// ---- the server ------------------------------------------------------------------

type pipeListener struct {
	addr string
	name *uint16
	sd   uintptr // the security descriptor, from LocalAlloc
	sa   syscall.SecurityAttributes

	mu        sync.Mutex
	next      syscall.Handle // the instance that waits for the next client
	accepting bool
	closed    bool
}

// listen creates the first instance, which fails if the name is taken, with the
// user-only DACL.
func listen(addr string) (listener, error) {
	sid, err := userSID()
	if err != nil {
		return nil, err
	}
	name, err := syscall.UTF16PtrFromString(addr)
	if err != nil {
		return nil, err
	}
	sddl, err := syscall.UTF16PtrFromString("D:P(A;;GA;;;" + sid + ")")
	if err != nil {
		return nil, err
	}
	l := &pipeListener{addr: addr, name: name}
	if r, _, e := convertStringSD.Call(uintptr(unsafe.Pointer(sddl)), 1, uintptr(unsafe.Pointer(&l.sd)), 0); r == 0 {
		return nil, fmt.Errorf("building the security descriptor: %w", e)
	}
	l.sa = syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), SecurityDescriptor: l.sd}
	h, err := l.create(true)
	if err != nil {
		localFree.Call(l.sd)
		return nil, fmt.Errorf("creating the pipe (is the name taken?): %w", err)
	}
	l.next = h
	return l, nil
}

func (l *pipeListener) create(first bool) (syscall.Handle, error) {
	mode := uintptr(pipeAccessDuplex)
	if first {
		mode |= fileFlagFirstInstance
	}
	h, _, e := createNamedPipeW.Call(uintptr(unsafe.Pointer(l.name)), mode,
		pipeTypeByte|pipeWait|pipeRejectRemoteClients, pipeUnlimitedInstances, 4096, 4096, 0,
		uintptr(unsafe.Pointer(&l.sa)))
	if syscall.Handle(h) == syscall.InvalidHandle {
		return 0, e
	}
	return syscall.Handle(h), nil
}

func (l *pipeListener) Accept() (conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, net_ErrClosed
	}
	h := l.next
	l.next = 0
	if h == 0 {
		var err error
		if h, err = l.create(false); err != nil {
			l.mu.Unlock()
			return nil, err
		}
	}
	l.accepting = true
	l.mu.Unlock()

	r, _, e := connectNamedPipe.Call(uintptr(h), 0)
	l.mu.Lock()
	l.accepting = false
	closed := l.closed
	l.mu.Unlock()
	if closed {
		syscall.CloseHandle(h)
		return nil, net_ErrClosed
	}
	if r == 0 && e != errPipeConnected && e != errNoData {
		syscall.CloseHandle(h)
		return nil, e
	}
	// Another instance waits for the next client before this one is served.
	nh, err := l.create(false)
	l.mu.Lock()
	switch {
	case l.closed && err == nil:
		syscall.CloseHandle(nh)
	case err == nil:
		l.next = nh
	}
	l.mu.Unlock()
	return &pipeConn{h: h, server: true}, nil
}

var net_ErrClosed = errors.New("instance: listener closed")

func (l *pipeListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	h, accepting := l.next, l.accepting
	l.next = 0
	l.mu.Unlock()
	if h != 0 {
		syscall.CloseHandle(h)
	}
	// An Accept blocked in ConnectNamedPipe is woken by a client of our own: the
	// instance is listening whether or not Accept has reached the call yet.
	for i := 0; accepting && i < 50; i++ {
		if c, err := dialRaw(l.addr, 100*time.Millisecond); err == nil {
			c.Close()
		}
		l.mu.Lock()
		accepting = l.accepting
		l.mu.Unlock()
		if accepting {
			time.Sleep(10 * time.Millisecond)
		}
	}
	localFree.Call(l.sd)
	return nil
}

// ---- a connection ----------------------------------------------------------------

type pipeConn struct {
	h      syscall.Handle
	server bool

	mu       sync.Mutex
	deadline time.Time
}

func (c *pipeConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}

// do runs one blocking call on the handle, cancelled when the deadline passes.
func (c *pipeConn) do(f func() error) error {
	c.mu.Lock()
	d := c.deadline
	c.mu.Unlock()
	var fired atomic.Bool
	if !d.IsZero() {
		dur := time.Until(d)
		if dur <= 0 {
			return os.ErrDeadlineExceeded
		}
		t := time.AfterFunc(dur, func() { fired.Store(true); cancelIoEx.Call(uintptr(c.h), 0) })
		defer t.Stop()
	}
	err := f()
	if err != nil && (fired.Load() || err == errAborted) {
		return os.ErrDeadlineExceeded
	}
	return err
}

func (c *pipeConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var n uint32
	err := c.do(func() error { return syscall.ReadFile(c.h, p, &n, nil) })
	switch {
	case err == errBrokenPipe:
		return int(n), io.EOF
	case err != nil:
		return int(n), err
	case n == 0:
		return 0, io.EOF
	}
	return int(n), nil
}

func (c *pipeConn) Write(p []byte) (int, error) {
	done := 0
	for done < len(p) {
		var n uint32
		if err := c.do(func() error { return syscall.WriteFile(c.h, p[done:], &n, nil) }); err != nil {
			return done, err
		}
		done += int(n)
	}
	return done, nil
}

// Close ends the connection. A server first waits (a little) until the client has
// read what was written to it.
func (c *pipeConn) Close() error {
	if c.server {
		done := make(chan struct{})
		go func() { flushFileBuffers.Call(uintptr(c.h)); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			cancelIoEx.Call(uintptr(c.h), 0)
		}
	}
	return syscall.CloseHandle(c.h)
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
			syscall.OPEN_EXISTING, securitySQOSPresent|securityIdentification, 0)
		if err == nil {
			return &pipeConn{h: h}, nil
		}
		left := time.Until(deadline)
		if err != errPipeBusy || left <= 0 {
			return nil, err
		}
		waitNamedPipeW.Call(uintptr(unsafe.Pointer(name)), uintptr(min(left, 50*time.Millisecond)/time.Millisecond))
	}
}

// dial connects and checks that the server is a process of this user.
func dial(addr string, timeout time.Duration) (conn, error) {
	c, err := dialRaw(addr, timeout)
	if err != nil {
		return nil, err
	}
	pc := c.(*pipeConn)
	if err := checkPeer(pc, pipeServerPID); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// peerCheck refuses a client that is not a process of this user.
func peerCheck(c conn) error {
	pc, ok := c.(*pipeConn)
	if !ok {
		return nil
	}
	return checkPeer(pc, pipeClientPID)
}

// checkPeer finds the process at the other end (with GetNamedPipeClientProcessId
// or GetNamedPipeServerProcessId) and compares its user with ours. Not being
// able to find out is a refusal.
func checkPeer(c *pipeConn, pidProc *syscall.LazyProc) error {
	var pid uint32
	if r, _, e := pidProc.Call(uintptr(c.h), uintptr(unsafe.Pointer(&pid))); r == 0 {
		return fmt.Errorf("instance: cannot find the process at the other end: %w", e)
	}
	theirs, err := processSID(pid)
	if err != nil {
		return fmt.Errorf("instance: cannot identify the process %d at the other end: %w", pid, err)
	}
	mine, err := userSID()
	if err != nil {
		return err
	}
	if theirs != mine {
		return fmt.Errorf("instance: the process %d at the other end belongs to %s", pid, theirs)
	}
	return nil
}
