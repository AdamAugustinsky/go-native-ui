// Package secret keeps small secrets (tokens, passwords, keys) in the
// operating system's credential store, under a service name and an account:
//
//	store := secret.Open(appID)                    // the app ID from app.AppConfig
//	err := store.Set("alice@example.com", token)   // add or replace
//	tok, err := store.Get("alice@example.com")     // secret.ErrNotFound if never set
//	err = store.Delete("alice@example.com")
//
// # Blocking
//
// Every call talks to the OS and can block: the Keychain may be asking the
// user to unlock it, Credential Manager and the Secret Service go through
// other processes. Call them from a task worker (package task), never from
// the UI thread and never per frame. The calls are safe from any goroutine.
//
// # What each OS does
//
//   - macOS: the Keychain, with SecItem* on generic passwords
//     (kSecClassGenericPassword; service and account are the item's
//     kSecAttrService and kSecAttrAccount) in the user's default keychain. The
//     data-protection keychain is not used, so no provisioning profile or
//     keychain-access-groups entitlement is needed. The Security framework is
//     opened on first use. An item is readable without a prompt by the program
//     that created it, so a rebuilt, re-signed program may be asked by the user
//     (sign development builds with a stable identity: gnui run does).
//   - Windows: Credential Manager, generic credentials named "service/account"
//     (CredWriteW, CredReadW, CredDeleteW), persisted for this machine. One
//     secret is at most 2560 bytes (CRED_MAX_CREDENTIAL_BLOB_SIZE).
//   - Linux and other Unix systems: the freedesktop.org Secret Service over the
//     session D-Bus (GNOME Keyring, KWallet, KeePassXC), in the default
//     collection, with the attributes service and account. Without a session
//     bus or a service the calls return ErrUnavailable. A locked collection is
//     reported as ErrLocked; the package never opens the unlock prompt.
//
// # Errors
//
// The errors below are matched with errors.Is. The returned error also carries
// the OS's own code in its text.
package secret

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound means there is no secret for the service and account (Get,
	// Delete).
	ErrNotFound = errors.New("secret: not found")
	// ErrDenied means the OS or the user refused access: the prompt was
	// declined, the item belongs to another program, or the store is
	// read-only.
	ErrDenied = errors.New("secret: access denied")
	// ErrLocked means the store is locked and could not be unlocked without
	// the user (a locked Keychain with no interaction possible, a locked
	// Secret Service collection). Unlocking it and retrying may succeed.
	ErrLocked = errors.New("secret: store locked")
	// ErrUnavailable means there is no credential store to talk to: no
	// session bus or Secret Service on Linux, the Security framework missing,
	// a Windows session without Credential Manager.
	ErrUnavailable = errors.New("secret: no credential store available")
	// ErrInvalid means the service, the account or the secret cannot be
	// stored: an empty or too-long name, a NUL byte in a name, a secret the OS
	// would refuse for its size.
	ErrInvalid = errors.New("secret: invalid service, account or secret")
)

// maxName is the longest service or account accepted, in bytes.
const maxName = 1024

// Store reads and writes the secrets of one service. Its methods may be called
// from any goroutine and block; see the package comment.
type Store struct {
	service string
	b       backend
}

// backend is one OS's credential store. The Store has checked the arguments.
type backend interface {
	get(service, account string) ([]byte, error)
	set(service, account string, secret []byte) error
	del(service, account string) error
}

// Open returns the store for a service, which is normally the application's ID
// (app.AppConfig.ID) so that its items are its own. Open does no I/O and never
// fails: a service the OS cannot use makes the calls return ErrInvalid.
func Open(service string) *Store { return &Store{service: service, b: osBackend()} }

// Service is the service name the store was opened with.
func (s *Store) Service() string { return s.service }

// Get returns the secret kept for account. It returns ErrNotFound when there is
// none. The caller owns the returned bytes.
func (s *Store) Get(account string) ([]byte, error) {
	if err := s.check(account); err != nil {
		return nil, err
	}
	return s.b.get(s.service, account)
}

// Set stores secret for account, replacing an earlier one. The secret may be
// empty. The store keeps its own copy.
func (s *Store) Set(account string, secret []byte) error {
	if err := s.check(account); err != nil {
		return err
	}
	return s.b.set(s.service, account, secret)
}

// Delete removes the secret kept for account. It returns ErrNotFound when
// there is none.
func (s *Store) Delete(account string) error {
	if err := s.check(account); err != nil {
		return err
	}
	return s.b.del(s.service, account)
}

func (s *Store) check(account string) error {
	for _, n := range [...]struct{ what, v string }{{"service", s.service}, {"account", account}} {
		if n.v == "" || len(n.v) > maxName {
			return fmt.Errorf("%w: %s must be 1 to %d bytes", ErrInvalid, n.what, maxName)
		}
		for i := 0; i < len(n.v); i++ {
			if n.v[i] == 0 {
				return fmt.Errorf("%w: %s contains a NUL byte", ErrInvalid, n.what)
			}
		}
	}
	return nil
}
