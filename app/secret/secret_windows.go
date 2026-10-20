//go:build windows

package secret

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// Credential Manager (advapi32): one generic credential per item, persisted for
// this machine (not roamed). Credential Manager compares target names without
// regard to case, so the target is a case-safe encoding of the service and the
// account (credentialTarget); the account is kept readable in the user name field
// and the service in the comment, for someone browsing Credential Manager.

const (
	credTypeGeneric       = 1
	credPersistLocal      = 2 // CRED_PERSIST_LOCAL_MACHINE
	credMaxBlob           = 5 * 512
	errNotFound           = syscall.Errno(1168) // ERROR_NOT_FOUND
	errNoSuchLogonSession = syscall.Errno(1312) // ERROR_NO_SUCH_LOGON_SESSION
	errAccessDenied       = syscall.Errno(5)
	errBadUsername        = syscall.Errno(2202)
	errInvalidParameter   = syscall.Errno(87)
)

// credential mirrors CREDENTIALW (wincred.h) on 64-bit Windows.
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        [2]uint32 // FILETIME
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	_ [unsafe.Sizeof(credential{}) - 80]struct{}
	_ [80 - unsafe.Sizeof(credential{})]struct{}
)

var wincred struct {
	once                   sync.Once
	write, read, del, free uintptr
}

func loadWincred() {
	adv := win32.LoadSystem("advapi32.dll")
	wincred.write, wincred.read = win32.Proc(adv, "CredWriteW"), win32.Proc(adv, "CredReadW")
	wincred.del, wincred.free = win32.Proc(adv, "CredDeleteW"), win32.Proc(adv, "CredFree")
}

func osBackend() backend { return credMan{} }

type credMan struct{}

// callMu serialises the calls into Credential Manager: they go through the
// local security authority, and calls from several threads at once have been
// seen to fail where one at a time do not (see the concurrent test).
var callMu sync.Mutex

func (credMan) ready() error {
	wincred.once.Do(loadWincred)
	if wincred.write == 0 || wincred.read == 0 || wincred.del == 0 || wincred.free == 0 {
		return fmt.Errorf("%w: advapi32 has no Credential Manager", ErrUnavailable)
	}
	return nil
}

func (c credMan) get(service, account string) ([]byte, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	name, err := syscall.UTF16PtrFromString(credentialTarget(service, account))
	if err != nil {
		return nil, ErrInvalid
	}
	var cred *credential
	callMu.Lock()
	defer callMu.Unlock()
	r, _, e := syscall.SyscallN(wincred.read, uintptr(unsafe.Pointer(name)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&cred)))
	if !win32.Bool(r) { // BOOL: only the low 32 bits are defined
		return nil, mapErr(e)
	}
	defer syscall.SyscallN(wincred.free, uintptr(unsafe.Pointer(cred)))
	n := int(cred.CredentialBlobSize)
	out := make([]byte, n)
	if n > 0 {
		copy(out, unsafe.Slice(cred.CredentialBlob, n))
	}
	return out, nil
}

func (c credMan) set(service, account string, secret []byte) error {
	if err := c.ready(); err != nil {
		return err
	}
	if len(secret) > credMaxBlob {
		return fmt.Errorf("%w: Credential Manager keeps at most %d bytes", ErrInvalid, credMaxBlob)
	}
	name, err := syscall.UTF16PtrFromString(credentialTarget(service, account))
	if err != nil {
		return ErrInvalid
	}
	user, err := syscall.UTF16PtrFromString(shortText(account, 256)) // CRED_MAX_USERNAME_LENGTH is 513
	if err != nil {
		return ErrInvalid
	}
	comment, err := syscall.UTF16PtrFromString(shortText(service, 200)) // CRED_MAX_STRING_LENGTH is 256
	if err != nil {
		return ErrInvalid
	}
	blob := make([]byte, len(secret)+1) // never nil
	copy(blob, secret)
	cred := &credential{
		Type:               credTypeGeneric,
		TargetName:         name,
		Comment:            comment,
		CredentialBlobSize: uint32(len(secret)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocal,
		UserName:           user,
	}
	callMu.Lock()
	defer callMu.Unlock()
	r, _, e := syscall.SyscallN(wincred.write, uintptr(unsafe.Pointer(cred)), 0)
	runtime.KeepAlive(cred)
	runtime.KeepAlive(blob)
	if !win32.Bool(r) { // BOOL: only the low 32 bits are defined
		return mapErr(e)
	}
	return nil
}

func (c credMan) del(service, account string) error {
	if err := c.ready(); err != nil {
		return err
	}
	name, err := syscall.UTF16PtrFromString(credentialTarget(service, account))
	if err != nil {
		return ErrInvalid
	}
	callMu.Lock()
	defer callMu.Unlock()
	r, _, e := syscall.SyscallN(wincred.del, uintptr(unsafe.Pointer(name)), credTypeGeneric, 0)
	if !win32.Bool(r) { // BOOL: only the low 32 bits are defined
		return mapErr(e)
	}
	return nil
}

func mapErr(e error) error {
	en, _ := e.(syscall.Errno)
	var base error
	switch en {
	case errNotFound:
		base = ErrNotFound
	case errAccessDenied:
		base = ErrDenied
	case errNoSuchLogonSession:
		base = ErrUnavailable
	case errInvalidParameter, errBadUsername:
		base = ErrInvalid
	default:
		return fmt.Errorf("secret: Credential Manager: %w", e)
	}
	return fmt.Errorf("%w (Windows error %d)", base, uint32(en))
}
