//go:build windows

package instance

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
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
