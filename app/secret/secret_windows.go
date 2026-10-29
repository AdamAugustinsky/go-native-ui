//go:build windows

package secret

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"gonativeui/app/paths"
	"gonativeui/internal/win32"
)

// Credential Manager (advapi32): one generic credential per item, persisted for
// this machine (not roamed). Credential Manager compares target names without
// regard to case, so the target is a case-safe encoding of the service and the
// account (credentialTarget); the account is kept readable in the user name field
// and the service in the comment, for someone browsing Credential Manager.
//
// A secret over the blob's 2,560 bytes (the phase 8 plan's S2) is sealed with
// CryptProtectData for the current user, with the target name as its extra
// entropy (a sealed file renamed to another item's name does not open), and
// written to <paths.Data(service)>\Secrets\<name>.sealed, the name a hash
// of the target. Its credential has an empty blob and one attribute,
// sealedKeyword, whose value is the file's name: the credential still says
// whether the item exists, and the file is only read through it. The file is
// written (to a temporary name, then renamed over) before the credential, so
// a crash between the two leaves either the old item or the new one; a
// smaller secret written later removes the file after its credential.

const (
	credTypeGeneric       = 1
	credPersistLocal      = 2 // CRED_PERSIST_LOCAL_MACHINE
	credMaxBlob           = 5 * 512
	errNotFound           = syscall.Errno(1168) // ERROR_NOT_FOUND
	errNoSuchLogonSession = syscall.Errno(1312) // ERROR_NO_SUCH_LOGON_SESSION
	errAccessDenied       = syscall.Errno(5)
	errBadUsername        = syscall.Errno(2202)
	errInvalidParameter   = syscall.Errno(87)

	sealedKeyword    = "gonativeui:sealed"
	sealedExt        = ".sealed"
	cryptUIForbidden = 0x1 // CRYPTPROTECT_UI_FORBIDDEN
)

// credentialAttribute mirrors CREDENTIAL_ATTRIBUTEW (wincred.h) on 64-bit
// Windows.
type credentialAttribute struct {
	Keyword   *uint16
	Flags     uint32
	ValueSize uint32
	Value     *byte
}

// dataBlob mirrors DATA_BLOB (dpapi.h).
type dataBlob struct {
	Size uint32
	Data *byte
}

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
	_ [unsafe.Sizeof(credentialAttribute{}) - 24]struct{}
	_ [24 - unsafe.Sizeof(credentialAttribute{})]struct{}
)

var wincred struct {
	once                   sync.Once
	write, read, del, free uintptr
	protect, unprotect     uintptr // crypt32, loaded with the first sealed secret
	localFree              uintptr
	cryptOnce              sync.Once
}

func loadWincred() {
	adv := win32.LoadSystem("advapi32.dll")
	wincred.write, wincred.read = win32.Proc(adv, "CredWriteW"), win32.Proc(adv, "CredReadW")
	wincred.del, wincred.free = win32.Proc(adv, "CredDeleteW"), win32.Proc(adv, "CredFree")
}

func loadCrypt() error {
	wincred.cryptOnce.Do(func() {
		c := win32.LoadSystem("crypt32.dll")
		wincred.protect, wincred.unprotect = win32.Proc(c, "CryptProtectData"), win32.Proc(c, "CryptUnprotectData")
		wincred.localFree = win32.Proc(win32.LoadSystem("kernel32.dll"), "LocalFree")
	})
	if wincred.protect == 0 || wincred.unprotect == 0 || wincred.localFree == 0 {
		return fmt.Errorf("%w: crypt32 has no CryptProtectData", ErrUnavailable)
	}
	return nil
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
	if file, ok := sealedName(cred); ok {
		return openSealed(service, account, file)
	}
	n := int(cred.CredentialBlobSize)
	out := make([]byte, n)
	if n > 0 {
		copy(out, unsafe.Slice(cred.CredentialBlob, n))
	}
	return out, nil
}

// sealedName is the sealed file's name a credential carries, if it has one.
func sealedName(cred *credential) (string, bool) {
	if cred.AttributeCount == 0 || cred.Attributes == 0 {
		return "", false
	}
	attrs := unsafe.Slice((*credentialAttribute)(win32.Ptr(cred.Attributes)), cred.AttributeCount)
	for i := range attrs {
		a := &attrs[i]
		if a.Keyword == nil || windowsString(a.Keyword) != sealedKeyword || a.Value == nil || a.ValueSize == 0 {
			continue
		}
		name := string(unsafe.Slice(a.Value, a.ValueSize))
		if strings.ContainsAny(name, `\/:`) || !strings.HasSuffix(name, sealedExt) {
			return "", false // not a name this package wrote
		}
		return name, true
	}
	return "", false
}

func windowsString(p *uint16) string {
	n := 0
	for s := unsafe.Pointer(p); *(*uint16)(unsafe.Add(s, 2*n)) != 0; n++ {
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n))
}

// sealedDir is the folder of the service's sealed secrets.
func sealedDir(service string) (string, error) {
	d, err := paths.Data(service)
	if err != nil {
		return "", fmt.Errorf("%w: a secret over %d bytes is kept in the app's data folder, and %q cannot name one: %v",
			ErrInvalid, credMaxBlob, service, err)
	}
	return filepath.Join(d, "Secrets"), nil
}

// sealedFile is the name of the item's sealed file: a hash of its target, so
// that every item has its own and a name says nothing of the account.
func sealedFile(service, account string) string {
	h := sha256.Sum256([]byte(credentialTarget(service, account)))
	return hex.EncodeToString(h[:16]) + sealedExt
}

// openSealed reads and unseals the item's file.
func openSealed(service, account, file string) ([]byte, error) {
	if err := loadCrypt(); err != nil {
		return nil, err
	}
	dir, err := sealedDir(service)
	if err != nil {
		return nil, err
	}
	sealed, err := os.ReadFile(filepath.Join(dir, file))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: the sealed file of the secret is missing", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("secret: reading the sealed secret: %w", err)
	}
	return dpapi(wincred.unprotect, sealed, credentialTarget(service, account))
}

// dpapi seals (CryptProtectData) or unseals (CryptUnprotectData) in for the
// current user, with entropy as the extra secret, and returns a copy of the
// result.
func dpapi(fn uintptr, in []byte, entropy string) ([]byte, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("%w: an empty sealed secret", ErrInvalid)
	}
	ent := []byte(entropy)
	blobIn := &dataBlob{Size: uint32(len(in)), Data: &in[0]}
	blobEnt := &dataBlob{Size: uint32(len(ent)), Data: &ent[0]}
	out := &dataBlob{}
	r, _, e := syscall.SyscallN(fn, uintptr(unsafe.Pointer(blobIn)), 0, uintptr(unsafe.Pointer(blobEnt)), 0, 0,
		cryptUIForbidden, uintptr(unsafe.Pointer(out)))
	runtime.KeepAlive(in)
	runtime.KeepAlive(ent)
	if !win32.Bool(r) {
		if fn == wincred.unprotect {
			return nil, fmt.Errorf("%w: the sealed secret does not open for this user (Windows error %d)", ErrDenied, uint32(e))
		}
		return nil, fmt.Errorf("secret: sealing the secret: %w", e)
	}
	defer syscall.SyscallN(wincred.localFree, uintptr(unsafe.Pointer(out.Data)))
	res := make([]byte, out.Size)
	copy(res, unsafe.Slice(out.Data, out.Size))
	return res, nil
}

// writeSealed seals secret into the item's file, written to a temporary name
// and renamed over the old one.
func writeSealed(service, account string, secret []byte) (string, error) {
	if err := loadCrypt(); err != nil {
		return "", err
	}
	dir, err := sealedDir(service)
	if err != nil {
		return "", err
	}
	if _, err := paths.Ensure(dir); err != nil {
		return "", fmt.Errorf("secret: the sealed secrets' folder: %w", err)
	}
	sealed, err := dpapi(wincred.protect, secret, credentialTarget(service, account))
	if err != nil {
		return "", err
	}
	file := sealedFile(service, account)
	tmp, err := os.CreateTemp(dir, file+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("secret: writing the sealed secret: %w", err)
	}
	_, err = tmp.Write(sealed)
	if err2 := tmp.Close(); err == nil {
		err = err2
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, file))
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("secret: writing the sealed secret: %w", err)
	}
	return file, nil
}

// removeSealed removes the item's sealed file, if there is one.
func removeSealed(service, account string) {
	if dir, err := sealedDir(service); err == nil {
		os.Remove(filepath.Join(dir, sealedFile(service, account)))
	}
}

func (c credMan) set(service, account string, secret []byte) error {
	if err := c.ready(); err != nil {
		return err
	}
	var file string
	if len(secret) > credMaxBlob {
		f, err := writeSealed(service, account, secret)
		if err != nil {
			return err
		}
		file = f
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
	inline := secret
	if file != "" {
		inline = nil
	}
	blob := make([]byte, len(inline)+1) // never nil
	copy(blob, inline)
	cred := &credential{
		Type:               credTypeGeneric,
		TargetName:         name,
		Comment:            comment,
		CredentialBlobSize: uint32(len(inline)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocal,
		UserName:           user,
	}
	var attr *credentialAttribute
	if file != "" {
		kw, _ := syscall.UTF16PtrFromString(sealedKeyword)
		v := []byte(file)
		attr = &credentialAttribute{Keyword: kw, ValueSize: uint32(len(v)), Value: &v[0]}
		cred.AttributeCount, cred.Attributes = 1, uintptr(unsafe.Pointer(attr))
	}
	callMu.Lock()
	r, _, e := syscall.SyscallN(wincred.write, uintptr(unsafe.Pointer(cred)), 0)
	callMu.Unlock()
	runtime.KeepAlive(cred)
	runtime.KeepAlive(blob)
	runtime.KeepAlive(attr)
	if !win32.Bool(r) { // BOOL: only the low 32 bits are defined
		return mapErr(e) // a file just written stays; the next Set or Delete replaces or removes it
	}
	if file == "" {
		removeSealed(service, account) // a larger secret it replaced
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
	r, _, e := syscall.SyscallN(wincred.del, uintptr(unsafe.Pointer(name)), credTypeGeneric, 0)
	callMu.Unlock()
	if !win32.Bool(r) { // BOOL: only the low 32 bits are defined
		return mapErr(e)
	}
	removeSealed(service, account)
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
