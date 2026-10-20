package secret

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gonativeui/internal/dbus"
)

// The freedesktop.org Secret Service (GNOME Keyring, KWallet, KeePassXC) over
// the session bus, with the "plain" transfer algorithm: the bus is a private
// local socket, which is what libsecret itself relies on when no key exchange
// is available. Each call dials the bus, opens a session, does its work in the
// default collection and hangs up (which ends the session), so nothing is kept
// between calls and a service that restarts needs no recovery code.
//
// Items carry the attributes service and account (and the generic schema), so
// libsecret's secret-tool finds them: secret-tool lookup service S account A.
// A locked collection is reported as ErrLocked; the unlock prompt is not run
// (it needs a window the service owns, and this package does not own a UI).
//
// The file has no build tag so that its tests run everywhere there is a Unix
// socket; only Linux and the other Unix systems use it as the store (see
// secret_other.go).

const (
	ssName       = "org.freedesktop.secrets"
	ssPath       = dbus.ObjectPath("/org/freedesktop/secrets")
	ssService    = "org.freedesktop.Secret.Service"
	ssCollection = "org.freedesktop.Secret.Collection"
	ssItem       = "org.freedesktop.Secret.Item"
	ssPrompt     = "org.freedesktop.Secret.Prompt"
	ssDefault    = dbus.ObjectPath("/org/freedesktop/secrets/aliases/default")
	ssSchema     = "org.freedesktop.Secret.Generic"
	ssTimeout    = 15 * time.Second
)

// secretService is the backend. dial connects to the session bus; tests give
// it the address of a private bus.
type secretService struct {
	dial func(context.Context) (*dbus.Conn, error)
}

// session is one connection with an open Secret Service session.
type session struct {
	ctx  context.Context
	c    *dbus.Conn
	path dbus.ObjectPath
}

func (s secretService) open(ctx context.Context) (*session, error) {
	c, err := s.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	r, err := c.CallContext(ctx, ssName, ssPath, ssService, "OpenSession", "sv", "plain", dbus.Variant{Sig: "s", Val: ""})
	if err != nil {
		c.Close()
		return nil, mapDBus(err)
	}
	if len(r.Body) != 2 {
		c.Close()
		return nil, fmt.Errorf("%w: unexpected OpenSession reply", ErrUnavailable)
	}
	path, ok := r.Body[1].(dbus.ObjectPath)
	if !ok {
		c.Close()
		return nil, fmt.Errorf("%w: unexpected OpenSession reply", ErrUnavailable)
	}
	return &session{ctx: ctx, c: c, path: path}, nil
}

func (s *session) close() {
	s.c.CallContext(s.ctx, ssName, s.path, "org.freedesktop.Secret.Session", "Close", "")
	s.c.Close()
}

func attrs(service, account string) []any {
	return []any{
		dbus.DictEntry{Key: "service", Val: service},
		dbus.DictEntry{Key: "account", Val: account},
	}
}

// find returns the unlocked items for the pair and whether any were locked.
func (s *session) find(service, account string) (items []dbus.ObjectPath, locked bool, err error) {
	r, err := s.c.CallContext(s.ctx, ssName, ssPath, ssService, "SearchItems", "a{ss}", attrs(service, account))
	if err != nil {
		return nil, false, mapDBus(err)
	}
	if len(r.Body) != 2 {
		return nil, false, fmt.Errorf("%w: unexpected SearchItems reply", ErrUnavailable)
	}
	un, _ := r.Body[0].([]any)
	lk, _ := r.Body[1].([]any)
	for _, v := range un {
		if p, ok := v.(dbus.ObjectPath); ok {
			items = append(items, p)
		}
	}
	return items, len(lk) > 0, nil
}

func (secretService) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), ssTimeout)
}

func (b secretService) get(service, account string) ([]byte, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	s, err := b.open(ctx)
	if err != nil {
		return nil, err
	}
	defer s.close()
	items, locked, err := s.find(service, account)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		if locked {
			return nil, fmt.Errorf("%w: the item is in a locked collection", ErrLocked)
		}
		return nil, ErrNotFound
	}
	r, err := s.c.CallContext(ctx, ssName, items[0], ssItem, "GetSecret", "o", s.path)
	if err != nil {
		return nil, mapDBus(err)
	}
	if len(r.Body) != 1 {
		return nil, fmt.Errorf("%w: unexpected GetSecret reply", ErrUnavailable)
	}
	st, _ := r.Body[0].(dbus.Struct)
	if len(st) != 4 {
		return nil, fmt.Errorf("%w: unexpected GetSecret reply", ErrUnavailable)
	}
	val, _ := st[2].([]byte)
	return append([]byte{}, val...), nil
}

func (b secretService) set(service, account string, secret []byte) error {
	ctx, cancel := b.ctx()
	defer cancel()
	s, err := b.open(ctx)
	if err != nil {
		return err
	}
	defer s.close()
	props := []any{
		dbus.DictEntry{Key: ssItem + ".Label", Val: dbus.Variant{Sig: "s", Val: service + ": " + account}},
		dbus.DictEntry{Key: ssItem + ".Attributes", Val: dbus.Variant{Sig: "a{ss}", Val: append(attrs(service, account),
			dbus.DictEntry{Key: "xdg:schema", Val: ssSchema})}},
	}
	sec := dbus.Struct{s.path, []byte{}, append([]byte{}, secret...), "application/octet-stream"}
	r, err := s.c.CallContext(ctx, ssName, ssDefault, ssCollection, "CreateItem", "a{sv}(oayays)b", props, sec, true)
	if err != nil {
		return mapDBus(err)
	}
	if len(r.Body) == 2 {
		if p, _ := r.Body[1].(dbus.ObjectPath); p != "/" && p != "" { // a prompt: the collection is locked
			s.c.CallContext(ctx, ssName, p, ssPrompt, "Dismiss", "")
			return fmt.Errorf("%w: the default collection is locked", ErrLocked)
		}
	}
	return nil
}

func (b secretService) del(service, account string) error {
	ctx, cancel := b.ctx()
	defer cancel()
	s, err := b.open(ctx)
	if err != nil {
		return err
	}
	defer s.close()
	items, locked, err := s.find(service, account)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		if locked {
			return fmt.Errorf("%w: the item is in a locked collection", ErrLocked)
		}
		return ErrNotFound
	}
	for _, it := range items {
		r, err := s.c.CallContext(ctx, ssName, it, ssItem, "Delete", "")
		if err != nil {
			return mapDBus(err)
		}
		if len(r.Body) == 1 {
			if p, _ := r.Body[0].(dbus.ObjectPath); p != "/" && p != "" {
				s.c.CallContext(ctx, ssName, p, ssPrompt, "Dismiss", "")
				return fmt.Errorf("%w: the item needs the user's confirmation to be deleted", ErrDenied)
			}
		}
	}
	return nil
}

// mapDBus turns a D-Bus failure into the package's errors.
func mapDBus(err error) error {
	var de *dbus.Error
	if !errors.As(err, &de) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err) // no bus, no reply, a timeout
	}
	var base error
	switch {
	case strings.HasSuffix(de.Name, "Secret.Error.IsLocked"):
		base = ErrLocked
	case strings.HasSuffix(de.Name, "Secret.Error.NoSuchObject"):
		base = ErrNotFound
	case strings.HasSuffix(de.Name, "DBus.Error.AccessDenied"), strings.HasSuffix(de.Name, "DBus.Error.AuthFailed"):
		base = ErrDenied
	case strings.HasSuffix(de.Name, "DBus.Error.ServiceUnknown"), strings.HasSuffix(de.Name, "DBus.Error.NameHasNoOwner"),
		strings.HasSuffix(de.Name, "DBus.Error.NoReply"), strings.HasSuffix(de.Name, "DBus.Error.UnknownObject"),
		strings.HasSuffix(de.Name, "DBus.Error.UnknownMethod"), strings.HasSuffix(de.Name, "DBus.Error.Spawn.ServiceNotFound"):
		base = ErrUnavailable
	default:
		return fmt.Errorf("secret: Secret Service: %w", de)
	}
	return fmt.Errorf("%w (%s)", base, de.Name)
}
