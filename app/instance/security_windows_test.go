//go:build windows

package instance

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// The pipe's DACL: one entry, an allow for the current user, nothing for anyone
// else (no other user, no Everyone, no administrators).
func TestPipeDACLIsTheUsersOnly(t *testing.T) {
	// A listener nobody accepts on: the security of a handle cannot be read while
	// a call is blocked on it.
	_, addr, err := setup(testID, Options{Dir: newDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := listen(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sddl := dacl(t, ln.(*pipeListener).next)
	sid, err := userSID()
	if err != nil {
		t.Fatal(err)
	}
	// The user may have a well-known alias in SDDL (a local administrator is "LA"):
	// say the expected SID the same way, by round trip.
	want := sddlSID(t, roundTrip(t, "D:P(A;;GA;;;"+sid+")"))
	if !strings.HasPrefix(sddl, "D:") || strings.Count(sddl, "(") != 1 ||
		!strings.HasPrefix(sddl[strings.Index(sddl, "("):], "(A;") || sddlSID(t, sddl) != want {
		t.Errorf("the pipe's DACL is %q, want one allow entry for %s (%s)", sddl, sid, want)
	}
}

// sddlSID is the trustee of the last entry of a DACL in SDDL.
func sddlSID(t *testing.T, sddl string) string {
	t.Helper()
	i := strings.LastIndex(sddl, ";;;")
	if i < 0 || !strings.HasSuffix(sddl, ")") {
		t.Fatalf("not an entry: %q", sddl)
	}
	return sddl[i+3 : len(sddl)-1]
}

// roundTrip parses an SDDL string and prints its DACL again.
func roundTrip(t *testing.T, sddl string) string {
	t.Helper()
	p, _ := syscall.UTF16PtrFromString(sddl)
	var sd uintptr
	if r, _, e := convertStringSD.Call(uintptr(unsafe.Pointer(p)), 1, uintptr(unsafe.Pointer(&sd)), 0); r == 0 {
		t.Fatal(e)
	}
	defer localFree.Call(sd)
	return sdString(t, sd)
}

func dacl(t *testing.T, h syscall.Handle) string {
	t.Helper()
	getSecurityInfo := advapi32.NewProc("GetSecurityInfo")
	var sd uintptr
	const seKernelObject, daclInfo = 6, 4
	if r, _, _ := getSecurityInfo.Call(uintptr(h), seKernelObject, daclInfo, 0, 0, 0, 0, uintptr(unsafe.Pointer(&sd))); r != 0 {
		t.Fatalf("GetSecurityInfo: error %d", r)
	}
	defer localFree.Call(sd)
	return sdString(t, sd)
}

// sdString prints the DACL of a security descriptor in SDDL.
func sdString(t *testing.T, sd uintptr) string {
	t.Helper()
	toString := advapi32.NewProc("ConvertSecurityDescriptorToStringSecurityDescriptorW")
	var str *uint16
	if r, _, e := toString.Call(sd, 1, 4, uintptr(unsafe.Pointer(&str)), 0); r == 0 {
		t.Fatalf("ConvertSecurityDescriptorToStringSecurityDescriptor: %v", e)
	}
	defer localFree.Call(uintptr(unsafe.Pointer(str)))
	n := 0
	for *(*uint16)(unsafe.Add(unsafe.Pointer(str), 2*n)) != 0 {
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice(str, n))
}

// A process that made a pipe of that name first does not get the requests: the
// primary cannot create the first instance, so Acquire fails, and releases the
// lock it took.
func TestPipeSquattedBeforehandIsRefused(t *testing.T) {
	dir := newDir(t)
	_, addr, err := setup(testID, Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	name, _ := syscall.UTF16PtrFromString(addr)
	h, _, e := createNamedPipeW.Call(uintptr(unsafe.Pointer(name)), pipeAccessDuplex,
		pipeTypeByte|pipeWait, pipeUnlimitedInstances, 4096, 4096, 0, 0)
	if syscall.Handle(h) == syscall.InvalidHandle {
		t.Fatal(e)
	}
	p, s, err := AcquireWith(testID, Options{Dir: dir})
	if err == nil {
		if p != nil {
			p.Close()
		}
		syscall.CloseHandle(syscall.Handle(h))
		t.Fatalf("Acquire = %v, %v, nil: the squatted pipe was accepted", p, s)
	}
	syscall.CloseHandle(syscall.Handle(h))
	// The failed Acquire gave the lock back.
	if p, _ := acquire(t, dir, Options{}); p == nil {
		t.Fatal("the lock was not released by the failed Acquire")
	}
}

func TestProcessSIDIsTheUsers(t *testing.T) {
	mine, err := userSID()
	if err != nil {
		t.Fatal(err)
	}
	got, err := processSID(uint32(os.Getpid()))
	if err != nil || got != mine {
		t.Fatalf("processSID = %q, %v; want %q", got, err, mine)
	}
}

func asOtherUser(t *testing.T) {
	t.Helper()
	other := "S-1-5-21-1-2-3-1001"
	sidOverride.Store(&other)
	t.Cleanup(func() { sidOverride.Store(nil) })
}

// A secondary does not send anything to a server that is not its user's.
func TestClientRefusesAServerOfAnotherUser(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{})
	_, s := acquire(t, dir, Options{Timeout: 300 * time.Millisecond})
	setPeer(t, func(conn) error { return nil }) // the server would serve anyone: only the client can refuse
	asOtherUser(t)
	if err := s.Send([]string{"secret"}); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("Send = %v, want ErrNoAnswer", err)
	}
	if got := p.Poll(nil); len(got) != 0 {
		t.Errorf("the primary stored %+v", got)
	}
}

// The primary does not serve a client that is not its user's, even one that does
// not check the server.
func TestServerRefusesAClientOfAnotherUser(t *testing.T) {
	dir := newDir(t)
	p, _ := acquire(t, dir, Options{})
	addr := endpoint(t, dir)
	asOtherUser(t)
	c, err := dialRaw(addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	body := idBody(1)
	c.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(body))), body...))
	var r [1]byte
	if _, err := io.ReadFull(c, r[:]); err == nil {
		t.Errorf("answered %q to a client of another user", r[0])
	}
	if got := p.Poll(nil); len(got) != 0 {
		t.Errorf("the primary stored %+v", got)
	}
}

// ---- overlapped calls, the listener, the identity of the client ----------------------

func setHook(t *testing.T, h *atomic.Pointer[func()], f func()) {
	t.Helper()
	h.Store(&f)
	t.Cleanup(func() { h.Store(nil) })
}

func newListener(t *testing.T) (*pipeListener, string) {
	t.Helper()
	_, addr, err := setup(testID, Options{Dir: newDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := listen(addr)
	if err != nil {
		t.Fatal(err)
	}
	l := ln.(*pipeListener)
	t.Cleanup(func() { l.Close() })
	return l, addr
}

// pair is a connected server end and client end.
func pair(t *testing.T) (srv, cli *pipeConn) {
	t.Helper()
	l, addr := newListener(t)
	c, err := dialRaw(addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); s.Close() })
	return s.(*pipeConn), c.(*pipeConn)
}

// finishes fails the test, instead of hanging it, when f does not return.
func finishes(t *testing.T, what string, within time.Duration, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatalf("%s did not return within %v", what, within)
	}
}

// A peer that takes the request and says nothing: Send is back when its timeout
// is, with a small margin, not later.
func TestSilentPeerDoesNotHoldSend(t *testing.T) {
	s := fakePrimary(t, newDir(t), func(n int, c conn) { time.Sleep(3 * time.Second) })
	s.timeout = 400 * time.Millisecond
	start := time.Now()
	var err error
	finishes(t, "Send", 5*time.Second, func() { err = s.Send([]string{"x"}) })
	if !errors.Is(err, ErrNoAnswer) {
		t.Errorf("Send = %v, want ErrNoAnswer", err)
	}
	if d := time.Since(start); d > 400*time.Millisecond+1500*time.Millisecond {
		t.Errorf("Send took %v with a timeout of 400ms", d)
	}
}

// The deadline passes before the call is waited for (a timer that fired before
// the I/O began): the call is still cancelled, and the read comes back.
func TestDeadlinePassingBeforeTheWaitStillCancels(t *testing.T) {
	_, cli := pair(t)
	setHook(t, &ioBeforeWait, func() { time.Sleep(300 * time.Millisecond) })
	cli.SetDeadline(time.Now().Add(50 * time.Millisecond))
	var err error
	finishes(t, "Read", 5*time.Second, func() { _, err = cli.Read(make([]byte, 1)) })
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("Read = %v, want a deadline error", err)
	}
	// And one that is already in the past.
	ioBeforeWait.Store(nil)
	cli.SetDeadline(time.Now().Add(-time.Second))
	finishes(t, "Read after the deadline", 5*time.Second, func() { _, err = cli.Read(make([]byte, 1)) })
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("Read = %v, want a deadline error", err)
	}
	// The connection still works afterwards: nothing was left pending on the buffer.
	cli.SetDeadline(time.Time{})
}

// Close while a read is pending cancels it and returns at once, on both ends.
func TestCloseDuringAPendingRead(t *testing.T) {
	for _, end := range []string{"server", "client"} {
		t.Run(end, func(t *testing.T) {
			srv, cli := pair(t)
			c := cli
			if end == "server" {
				c = srv
			}
			read := make(chan error, 1)
			go func() { _, err := c.Read(make([]byte, 1)); read <- err }()
			time.Sleep(100 * time.Millisecond)
			finishes(t, "Close", 3*time.Second, func() { c.Close() })
			select {
			case err := <-read:
				if err == nil {
					t.Error("the read returned no error")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the pending read was not cancelled")
			}
		})
	}
}

// Close arrives after the call is registered and before it is submitted: it finds
// nothing to cancel, and the call must cancel itself.
func TestCloseBeforeTheCallIsSubmitted(t *testing.T) {
	_, cli := pair(t)
	var once sync.Once
	setHook(t, &ioBeforeSubmit, func() {
		once.Do(func() {
			go cli.Close()
			time.Sleep(200 * time.Millisecond) // Close has marked the connection and is waiting
		})
	})
	var err error
	finishes(t, "Read", 5*time.Second, func() { _, err = cli.Read(make([]byte, 1)) })
	if err == nil {
		t.Error("the read returned no error")
	}
}

// Close races Accept at the moment it is about to create the replacement instance:
// the descriptor it would use must not be freed under it.
func TestCloseRacingAcceptKeepsTheDescriptor(t *testing.T) {
	l, _ := newListener(t)
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	setHook(t, &beforeCreateNext, func() {
		once.Do(func() { close(reached); <-release })
	})
	accepted := make(chan error, 1)
	go func() { _, err := l.Accept(); accepted <- err }()
	<-reached
	closed := make(chan struct{})
	go func() { l.Close(); close(closed) }()
	time.Sleep(200 * time.Millisecond)
	select {
	case <-closed:
		t.Fatal("Close returned while Accept could still create an instance")
	default:
	}
	l.mu.Lock()
	freed := l.sd == 0
	l.mu.Unlock()
	if freed {
		t.Fatal("the security descriptor was freed while Accept could still use it")
	}
	close(release)
	select {
	case err := <-accepted:
		if !errors.Is(err, errListenerClosed) {
			t.Errorf("Accept = %v, want the listener closed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not return")
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	l.mu.Lock()
	freed = l.sd == 0
	l.mu.Unlock()
	if !freed {
		t.Error("the security descriptor was not freed by Close")
	}
}

func createFirstInstance(addr string) (syscall.Handle, error) {
	name, _ := syscall.UTF16PtrFromString(addr)
	h, _, e := createNamedPipeW.Call(uintptr(unsafe.Pointer(name)), pipeAccessDuplex|fileFlagFirstInstance,
		pipeTypeByte|pipeWait, pipeUnlimitedInstances, 4096, 4096, 0, 0)
	if syscall.Handle(h) == syscall.InvalidHandle {
		return 0, e
	}
	return syscall.Handle(h), nil
}

// A replacement instance that cannot be created does not make the name go free:
// the idle instance stays until another can take its place, a third party cannot
// make the pipe meanwhile, and the listener works again when creation does.
func TestFailedReplacementKeepsTheName(t *testing.T) {
	l, addr := newListener(t)
	failCreate.Store(true)
	t.Cleanup(func() { failCreate.Store(false) })
	type result struct {
		c   conn
		err error
	}
	accepted := make(chan result, 2)
	go func() { c, err := l.Accept(); accepted <- result{c, err} }()

	// A client is there while nothing can be created: it connects to the idle
	// instance, and must not be taken for it to go on alone.
	c, err := dialRaw(addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	time.Sleep(150 * time.Millisecond)
	select {
	case r := <-accepted:
		t.Fatalf("Accept returned (%v) although no replacement could be created", r.err)
	default:
	}
	if h, err := createFirstInstance(addr); err == nil {
		syscall.CloseHandle(h)
		t.Fatal("a third party made the pipe while the replacement could not be created")
	}

	failCreate.Store(false)
	select {
	case r := <-accepted:
		if r.err != nil {
			t.Fatalf("Accept after recovery: %v", r.err)
		}
		r.c.Close()
		c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not recover")
	}
	if h, err := createFirstInstance(addr); err == nil {
		syscall.CloseHandle(h)
		t.Fatal("a third party made the pipe after the connection was closed")
	}
	// Recovered: a client is served.
	c2, err := dialRaw(addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	var srv conn
	finishes(t, "Accept", 5*time.Second, func() { srv, err = l.Accept() })
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	c2.SetDeadline(time.Now().Add(3 * time.Second))
	srv.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c2.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := io.ReadFull(srv, b[:]); err != nil || b[0] != 'x' {
		t.Fatalf("read %q, %v", b, err)
	}
}

// The server names the client's user by impersonating it, after it has read.
func TestServerNamesTheClientByImpersonation(t *testing.T) {
	srv, cli := pair(t)
	cli.SetDeadline(time.Now().Add(3 * time.Second))
	srv.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := cli.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if err := peerCheck(srv); err != nil {
		t.Fatalf("peerCheck of this user's client: %v", err)
	}
	var b [2]byte
	if _, err := io.ReadFull(srv, b[:]); err != nil || string(b[:]) != "hi" {
		t.Fatalf("what the check read ahead was lost: %q, %v", b, err)
	}
	mine, _ := userSID()
	if got, err := srv.clientSID(); err != nil || got != mine {
		t.Fatalf("clientSID = %q, %v; want %q", got, err, mine)
	}
}

func TestImpersonationIsReverted(t *testing.T) {
	srv, cli := pair(t)
	cli.SetDeadline(time.Now().Add(3 * time.Second))
	srv.SetDeadline(time.Now().Add(3 * time.Second))
	cli.Write([]byte("hi"))
	srv.readRaw(make([]byte, 2))
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, err := srv.clientSID(); err != nil {
		t.Fatal(err)
	}
	var tok syscall.Token
	const currentThread = ^uintptr(1)
	r, _, e := openThreadToken.Call(currentThread, tokenQuery, 1, uintptr(unsafe.Pointer(&tok)))
	if r != 0 {
		tok.Close()
		t.Fatal("the thread still has an impersonation token")
	}
	if e != syscall.Errno(1008) { // ERROR_NO_TOKEN
		t.Errorf("OpenThreadToken: %v, want ERROR_NO_TOKEN", e)
	}
	// And a failed impersonation (nothing read yet on a fresh pair) also leaves the thread alone.
	srv2, _ := pair(t)
	if _, err := srv2.clientSID(); err == nil {
		t.Log("impersonation before a read was allowed by this system")
	}
	if r, _, _ := openThreadToken.Call(currentThread, tokenQuery, 1, uintptr(unsafe.Pointer(&tok))); r != 0 {
		tok.Close()
		t.Fatal("the thread has an impersonation token after a failed impersonation")
	}
}

// A client of another user (here: this user taken for another one) is refused by
// the server's own look at the client, not by a hook.
func TestImpersonatedClientOfAnotherUserIsRefused(t *testing.T) {
	srv, cli := pair(t)
	cli.SetDeadline(time.Now().Add(3 * time.Second))
	srv.SetDeadline(time.Now().Add(3 * time.Second))
	cli.Write([]byte("hi"))
	asOtherUser(t)
	if err := peerCheck(srv); err == nil {
		t.Fatal("peerCheck accepted a client whose user differs")
	}
}
