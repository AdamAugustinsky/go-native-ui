// Package winreg reads and writes the current user's registry for the
// Windows parts of app/install, app/login and app/selfupdate: string, DWORD
// and binary values, key creation, and whole-tree deletion. It wraps
// advapi32 with syscall only (the standard library's registry package is
// internal to it), so it needs no dependency (D2).
//
// Every path is relative to HKEY_CURRENT_USER: the framework never writes
// the machine's keys (D76, everything per user).
package winreg

import (
	"errors"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// Registry value types (winnt.h).
const (
	typeSZ     = 1 // REG_SZ
	typeBinary = 3 // REG_BINARY
	typeDWORD  = 4 // REG_DWORD

	keyRead  = 0x20019 // KEY_READ
	keyWrite = 0x20006 // KEY_WRITE

	errFileNotFound = syscall.Errno(2)   // ERROR_FILE_NOT_FOUND
	errMoreData     = syscall.Errno(234) // ERROR_MORE_DATA
)

// ErrNotExist is returned when the key or value is missing.
var ErrNotExist = errors.New("winreg: no such key or value")

var (
	advapi32         = win32.LoadSystem("advapi32.dll")
	pRegCreateKeyExW = win32.Proc(advapi32, "RegCreateKeyExW")
	pRegSetValueExW  = win32.Proc(advapi32, "RegSetValueExW")
	pRegDeleteValueW = win32.Proc(advapi32, "RegDeleteValueW")
	pRegDeleteTreeW  = win32.Proc(advapi32, "RegDeleteTreeW")
)

func errOf(rc uintptr) error {
	switch e := syscall.Errno(rc); e {
	case 0:
		return nil
	case errFileNotFound:
		return ErrNotExist
	default:
		return e
	}
}

// Key is an open key under HKEY_CURRENT_USER.
type Key struct{ h syscall.Handle }

// Create opens path for writing, creating it and its parents when missing.
func Create(path string) (Key, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return Key{}, err
	}
	var h syscall.Handle
	rc, _, _ := syscall.SyscallN(pRegCreateKeyExW, uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(p)), 0, 0, 0,
		keyWrite|keyRead, 0, uintptr(unsafe.Pointer(&h)), 0)
	if err := errOf(rc); err != nil {
		return Key{}, err
	}
	return Key{h}, nil
}

// Open opens path for reading; ErrNotExist when it is missing.
func Open(path string) (Key, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return Key{}, err
	}
	var h syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, p, 0, keyRead, &h); err != nil {
		if errors.Is(err, errFileNotFound) {
			return Key{}, ErrNotExist
		}
		return Key{}, err
	}
	return Key{h}, nil
}

// Close closes the key.
func (k Key) Close() error { return syscall.RegCloseKey(k.h) }

func (k Key) set(name string, typ uint32, data []byte) error {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var d *byte
	if len(data) > 0 {
		d = &data[0]
	}
	rc, _, _ := syscall.SyscallN(pRegSetValueExW, uintptr(k.h), uintptr(unsafe.Pointer(n)), 0, uintptr(typ),
		uintptr(unsafe.Pointer(d)), uintptr(len(data)))
	return errOf(rc)
}

// SetString writes a REG_SZ value ("" names the key's default value).
func (k Key) SetString(name, value string) error {
	u, err := syscall.UTF16FromString(value)
	if err != nil {
		return err
	}
	return k.set(name, typeSZ, unsafe.Slice((*byte)(unsafe.Pointer(&u[0])), 2*len(u)))
}

// SetDWORD writes a REG_DWORD value.
func (k Key) SetDWORD(name string, v uint32) error {
	return k.set(name, typeDWORD, []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
}

// SetBinary writes a REG_BINARY value.
func (k Key) SetBinary(name string, b []byte) error { return k.set(name, typeBinary, b) }

// value reads a value's type and bytes.
func (k Key) value(name string) (uint32, []byte, error) {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, nil, err
	}
	buf := make([]byte, 256)
	for {
		var typ uint32
		size := uint32(len(buf))
		err := syscall.RegQueryValueEx(k.h, n, nil, &typ, &buf[0], &size)
		switch {
		case err == nil:
			return typ, buf[:size], nil
		case errors.Is(err, errMoreData) && size > uint32(len(buf)) && size < 1<<20:
			buf = make([]byte, size)
		case errors.Is(err, errFileNotFound):
			return 0, nil, ErrNotExist
		default:
			return 0, nil, err
		}
	}
}

// String reads a REG_SZ (or REG_EXPAND_SZ, unexpanded) value.
func (k Key) String(name string) (string, error) {
	typ, b, err := k.value(name)
	if err != nil {
		return "", err
	}
	if typ != typeSZ && typ != 2 { // REG_EXPAND_SZ
		return "", errors.New("winreg: " + name + " is not a string")
	}
	u := unsafe.Slice((*uint16)(unsafe.Pointer(unsafe.SliceData(b))), len(b)/2)
	return syscall.UTF16ToString(u), nil
}

// DWORD reads a REG_DWORD value.
func (k Key) DWORD(name string) (uint32, error) {
	typ, b, err := k.value(name)
	if err != nil {
		return 0, err
	}
	if typ != typeDWORD || len(b) != 4 {
		return 0, errors.New("winreg: " + name + " is not a DWORD")
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, nil
}

// Binary reads a value's bytes, whatever its type.
func (k Key) Binary(name string) ([]byte, error) {
	_, b, err := k.value(name)
	return b, err
}

// SubKeys lists the key's direct subkeys.
func (k Key) SubKeys() ([]string, error) {
	var out []string
	buf := make([]uint16, 256)
	for i := uint32(0); ; i++ {
		n := uint32(len(buf))
		err := syscall.RegEnumKeyEx(k.h, i, &buf[0], &n, nil, nil, nil, nil)
		if errors.Is(err, syscall.Errno(259)) { // ERROR_NO_MORE_ITEMS
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, syscall.UTF16ToString(buf[:n]))
	}
}

// GetString opens path and reads one string value.
func GetString(path, name string) (string, error) {
	k, err := Open(path)
	if err != nil {
		return "", err
	}
	defer k.Close()
	return k.String(name)
}

// Exists reports whether the key at path exists.
func Exists(path string) bool {
	k, err := Open(path)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

// DeleteValue removes one value; a missing key or value is not an error.
func DeleteValue(path, name string) error {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var h syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, p, 0, keyWrite, &h); err != nil {
		if errors.Is(err, errFileNotFound) {
			return nil
		}
		return err
	}
	defer syscall.RegCloseKey(h)
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	rc, _, _ := syscall.SyscallN(pRegDeleteValueW, uintptr(h), uintptr(unsafe.Pointer(n)))
	if err := errOf(rc); err != nil && err != ErrNotExist {
		return err
	}
	return nil
}

// DeleteTree removes the key at path with its subkeys and values; a missing
// key is not an error. path must name a key below a fixed root such as
// Software\Classes\<scheme>, never "" or "Software".
func DeleteTree(path string) error {
	if path == "" {
		return errors.New("winreg: DeleteTree needs a key")
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	rc, _, _ := syscall.SyscallN(pRegDeleteTreeW, uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(p)))
	if err := errOf(rc); err != nil && err != ErrNotExist {
		return err
	}
	// RegDeleteTreeW leaves the key itself on some versions when it had no
	// subkeys; RegDeleteKeyW removes it.
	return deleteKey(path)
}

var pRegDeleteKeyW = win32.Proc(advapi32, "RegDeleteKeyW")

func deleteKey(path string) error {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	rc, _, _ := syscall.SyscallN(pRegDeleteKeyW, uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(p)))
	if err := errOf(rc); err != nil && err != ErrNotExist {
		return err
	}
	return nil
}
