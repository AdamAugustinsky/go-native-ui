//go:build darwin

package secret

import (
	"fmt"
	"sync"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
)

// The Keychain through the SecItem C API. It is plain C on Core Foundation
// objects that each call creates and releases, so it runs on whatever worker
// goroutine calls it, with a call frame of its own (internal/cf). The
// data-protection keychain (kSecUseDataProtectionKeychain) is left off, so the
// item goes to the user's default file-based keychain and needs no
// provisioning profile.

// OSStatus values (Security/SecBase.h).
const (
	errSecSuccess               = 0
	errSecUserCanceled          = -128
	errSecServiceNotAvailable   = -67585
	errSecNotAvailable          = -25291
	errSecReadOnly              = -25292
	errSecAuthFailed            = -25293
	errSecNoSuchKeychain        = -25294
	errSecInvalidKeychain       = -25295
	errSecDuplicateItem         = -25299
	errSecItemNotFound          = -25300
	errSecNoDefaultKeychain     = -25307
	errSecInteractionNotAllowed = -25308
	errSecMissingEntitlement    = -34018
)

type secSyms struct {
	once sync.Once
	err  error

	add, copyMatching, update, del uintptr

	class, classGeneric, service, account, valueData, returnData, matchLimit, matchLimitOne, boolTrue uintptr
	searchList, useKeychain                                                                           uintptr
}

var sec secSyms

func (s *secSyms) load() {
	if s.err = cf.Init(); s.err != nil {
		return
	}
	lib, err := cf.Framework("Security")
	if err != nil {
		s.err = err
		return
	}
	s.add, s.copyMatching = cf.Sym(lib, "SecItemAdd"), cf.Sym(lib, "SecItemCopyMatching")
	s.update, s.del = cf.Sym(lib, "SecItemUpdate"), cf.Sym(lib, "SecItemDelete")
	s.class, s.classGeneric = cf.Const(lib, "kSecClass"), cf.Const(lib, "kSecClassGenericPassword")
	s.service, s.account = cf.Const(lib, "kSecAttrService"), cf.Const(lib, "kSecAttrAccount")
	s.valueData, s.returnData = cf.Const(lib, "kSecValueData"), cf.Const(lib, "kSecReturnData")
	s.matchLimit, s.matchLimitOne = cf.Const(lib, "kSecMatchLimit"), cf.Const(lib, "kSecMatchLimitOne")
	s.searchList, s.useKeychain = cf.Const(lib, "kSecMatchSearchList"), cf.Const(lib, "kSecUseKeychain")
	s.boolTrue = cf.Const(cf.CoreFoundation(), "kCFBooleanTrue")
	for _, p := range []uintptr{s.add, s.copyMatching, s.update, s.del, s.class, s.classGeneric, s.service,
		s.account, s.valueData, s.returnData, s.matchLimit, s.matchLimitOne, s.boolTrue, s.searchList, s.useKeychain} {
		if p == 0 {
			s.err = cf.ErrUnavailable
		}
	}
}

func osBackend() backend { return keychain{} }

// keychain is the Keychain backend. The zero value uses the user's default
// keychain and search list. ref, when not 0, is a SecKeychainRef that confines
// every query and every new item to that one keychain: the tests give it a
// temporary keychain, so they never touch the user's items.
type keychain struct{ ref uintptr }

// query builds the dictionary that names one item. The caller releases it. Every
// Core Foundation object made on the way is checked: a null string or dictionary
// (out of memory, bytes that are not text) is an error, never a half-built query.
func (k keychain) query(fr *ffi.Frame, service, account string) (uintptr, error) {
	d := cf.Dict(fr)
	if d == 0 {
		return 0, fmt.Errorf("%w: cannot make a query", ErrUnavailable)
	}
	cf.DictSet(fr, d, sec.class, sec.classGeneric)
	for _, kv := range [...]struct {
		k uintptr
		v string
	}{{sec.service, service}, {sec.account, account}} {
		s := cf.String(fr, kv.v)
		if s == 0 {
			cf.CFRelease(fr, d)
			return 0, fmt.Errorf("%w: cannot make a Core Foundation string of the name", ErrInvalid)
		}
		cf.DictSet(fr, d, kv.k, s)
		cf.CFRelease(fr, s) // the dictionary holds its own reference
	}
	if k.ref != 0 {
		list := cf.Array(fr, k.ref)
		if list == 0 {
			cf.CFRelease(fr, d)
			return 0, fmt.Errorf("%w: cannot make a query", ErrUnavailable)
		}
		cf.DictSet(fr, d, sec.searchList, list)
		cf.CFRelease(fr, list)
	}
	return d, nil
}

func (k keychain) begin() (*ffi.Frame, error) {
	sec.once.Do(sec.load)
	if sec.err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, sec.err)
	}
	return cf.Acquire(), nil
}

func (k keychain) get(service, account string) ([]byte, error) {
	fr, err := k.begin()
	if err != nil {
		return nil, err
	}
	defer cf.Release(fr)
	q, err := k.query(fr, service, account)
	if err != nil {
		return nil, err
	}
	defer cf.CFRelease(fr, q)
	cf.DictSet(fr, q, sec.returnData, sec.boolTrue)
	cf.DictSet(fr, q, sec.matchLimit, sec.matchLimitOne)
	out := cf.Out(fr)
	st := int32(fr.Reset().Arg(q).ArgPtr(out).Call(sec.copyMatching).R0())
	if st != errSecSuccess {
		return nil, statusErr(st)
	}
	data := cf.Word(fr)
	b := cf.DataBytes(fr, data)
	cf.CFRelease(fr, data)
	return b, nil
}

func (k keychain) set(service, account string, secret []byte) error {
	fr, err := k.begin()
	if err != nil {
		return err
	}
	defer cf.Release(fr)
	q, err := k.query(fr, service, account)
	if err != nil {
		return err
	}
	defer cf.CFRelease(fr, q)
	data := cf.Data(fr, secret)
	if data == 0 {
		return fmt.Errorf("%w: cannot make a Core Foundation object of the secret", ErrUnavailable)
	}
	defer cf.CFRelease(fr, data)

	add, err := k.query(fr, service, account)
	if err != nil {
		return err
	}
	defer cf.CFRelease(fr, add)
	cf.DictSet(fr, add, sec.valueData, data)
	if k.ref != 0 {
		cf.DictSet(fr, add, sec.useKeychain, k.ref)
	}
	st := int32(fr.Reset().Arg(add).Arg(0).Call(sec.add).R0())
	if st == errSecDuplicateItem {
		upd := cf.Dict(fr)
		if upd == 0 {
			return fmt.Errorf("%w: cannot make a query", ErrUnavailable)
		}
		defer cf.CFRelease(fr, upd)
		cf.DictSet(fr, upd, sec.valueData, data)
		st = int32(fr.Reset().Arg(q).Arg(upd).Call(sec.update).R0())
	}
	if st != errSecSuccess {
		return statusErr(st)
	}
	return nil
}

func (k keychain) del(service, account string) error {
	fr, err := k.begin()
	if err != nil {
		return err
	}
	defer cf.Release(fr)
	q, err := k.query(fr, service, account)
	if err != nil {
		return err
	}
	defer cf.CFRelease(fr, q)
	if st := int32(fr.Reset().Arg(q).Call(sec.del).R0()); st != errSecSuccess {
		return statusErr(st)
	}
	return nil
}

// statusErr maps an OSStatus to the package's errors, keeping the code.
func statusErr(st int32) error {
	var base error
	switch st {
	case errSecItemNotFound:
		base = ErrNotFound
	case errSecInteractionNotAllowed: // locked, and no user interface to unlock it
		base = ErrLocked
	case errSecAuthFailed, errSecUserCanceled, errSecReadOnly, errSecMissingEntitlement:
		base = ErrDenied
	case errSecNotAvailable, errSecNoSuchKeychain, errSecInvalidKeychain, errSecNoDefaultKeychain, errSecServiceNotAvailable:
		base = ErrUnavailable
	default:
		return fmt.Errorf("secret: Keychain error %d", st)
	}
	return fmt.Errorf("%w (Keychain OSStatus %d)", base, st)
}
