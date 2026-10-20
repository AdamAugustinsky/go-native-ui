//go:build !windows

package secret

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"gonativeui/internal/dbus"
	"gonativeui/internal/dbus/dbustest"
)

// fakeService is a Secret Service on a private bus: the default collection,
// items with attributes, and the calls the backend makes. locked makes the
// collection refuse like a locked keyring (CreateItem answers with a prompt,
// SearchItems lists the items as locked).
type fakeService struct {
	t    *testing.T
	c    *dbus.Conn
	mu   sync.Mutex
	next int
	item map[dbus.ObjectPath]*fakeItem
	lock bool

	sessions, closed, dismissed int
}

type fakeItem struct {
	attrs  map[string]string
	secret []byte
}

const fakePrefix = "/org/freedesktop/secrets/collection/default/"

func startFake(t *testing.T, b *dbustest.Bus) *fakeService {
	t.Helper()
	ctx := context.Background()
	c, err := dbus.Dial(ctx, b.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	f := &fakeService{t: t, c: c, item: map[dbus.ObjectPath]*fakeItem{}}
	c.Export(ssPath, ssService, f.service)
	c.Export(ssDefault, ssCollection, f.collection)
	if r, err := c.RequestName(ctx, ssName, 0); err != nil || r != dbus.NameReplyPrimary {
		t.Fatalf("RequestName: %d, %v", r, err)
	}
	return f
}

func (f *fakeService) service(m *dbus.Message) (string, []any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch m.Member {
	case "OpenSession":
		if m.Body[0] != "plain" {
			return "", nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.NotSupported"}
		}
		f.sessions++
		path := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/secrets/session/s%d", f.sessions))
		f.c.Export(path, "org.freedesktop.Secret.Session", func(*dbus.Message) (string, []any, error) {
			f.mu.Lock()
			f.closed++
			f.mu.Unlock()
			return "", nil, nil
		})
		return "vo", []any{dbus.Variant{Sig: "s", Val: ""}, path}, nil
	case "SearchItems":
		want := dictOf(m.Body[0])
		var found []any
		for p, it := range f.item {
			ok := true
			for k, v := range want {
				ok = ok && it.attrs[k] == v
			}
			if ok {
				found = append(found, p)
			}
		}
		if found == nil {
			found = []any{}
		}
		if f.lock {
			return "aoao", []any{[]any{}, found}, nil
		}
		return "aoao", []any{found, []any{}}, nil
	}
	return "", nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownMethod"}
}

func dictOf(v any) map[string]string {
	out := map[string]string{}
	for _, e := range v.([]any) {
		de := e.(dbus.DictEntry)
		switch x := de.Val.(type) {
		case string:
			out[de.Key.(string)] = x
		case dbus.Variant:
			if s, ok := x.Val.(string); ok {
				out[de.Key.(string)] = s
			}
		}
	}
	return out
}

func (f *fakeService) collection(m *dbus.Message) (string, []any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m.Member != "CreateItem" {
		return "", nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownMethod"}
	}
	if f.lock {
		prompt := dbus.ObjectPath("/org/freedesktop/secrets/prompt/p1")
		f.c.Export(prompt, ssPrompt, func(*dbus.Message) (string, []any, error) {
			f.mu.Lock()
			f.dismissed++
			f.mu.Unlock()
			return "", nil, nil
		})
		return "oo", []any{dbus.ObjectPath("/"), prompt}, nil
	}
	var attrsV []any
	for _, e := range m.Body[0].([]any) {
		de := e.(dbus.DictEntry)
		if de.Key == ssItem+".Attributes" {
			attrsV = de.Val.(dbus.Variant).Val.([]any)
		}
	}
	at := dictOf(attrsV)
	st := m.Body[1].(dbus.Struct)
	secret := append([]byte{}, st[2].([]byte)...)
	if m.Body[2] == true { // replace the item with the same attributes
		for p, it := range f.item {
			if it.attrs["service"] == at["service"] && it.attrs["account"] == at["account"] {
				it.secret = secret
				return "oo", []any{p, dbus.ObjectPath("/")}, nil
			}
		}
	}
	f.next++
	path := dbus.ObjectPath(fmt.Sprintf("%s%d", fakePrefix, f.next))
	f.item[path] = &fakeItem{attrs: at, secret: secret}
	f.c.Export(path, ssItem, f.itemMethod)
	return "oo", []any{path, dbus.ObjectPath("/")}, nil
}

func (f *fakeService) itemMethod(m *dbus.Message) (string, []any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	it := f.item[m.Path]
	if it == nil {
		return "", nil, &dbus.Error{Name: "org.freedesktop.Secret.Error.NoSuchObject"}
	}
	switch m.Member {
	case "GetSecret":
		return "(oayays)", []any{dbus.Struct{m.Body[0], []byte{}, it.secret, "application/octet-stream"}}, nil
	case "Delete":
		delete(f.item, m.Path)
		return "o", []any{dbus.ObjectPath("/")}, nil
	}
	return "", nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownMethod"}
}

func (f *fakeService) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.item)
}

func storeOn(t *testing.T, b *dbustest.Bus) *Store {
	t.Helper()
	dial := func(ctx context.Context) (*dbus.Conn, error) { return dbus.Dial(ctx, b.Address) }
	return &Store{service: testService(t), b: secretService{dial: dial}}
}

func TestSecretServiceRoundTrip(t *testing.T) {
	dbustest.Each(t, func(t *testing.T, b *dbustest.Bus) {
		f := startFake(t, b)
		s := storeOn(t, b)
		t.Cleanup(func() { // runs after roundTrip's own cleanups
			if n := f.count(); n != 0 {
				t.Errorf("%d items left after the round trip", n)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.closed != f.sessions {
				t.Errorf("%d sessions opened, %d closed", f.sessions, f.closed)
			}
		})
		roundTrip(t, s)
	})
}

func TestSecretServiceConcurrent(t *testing.T) {
	dbustest.Each(t, func(t *testing.T, b *dbustest.Bus) {
		startFake(t, b)
		concurrent(t, storeOn(t, b))
	})
}

func TestSecretServiceReplacesInPlace(t *testing.T) {
	dbustest.Each(t, func(t *testing.T, b *dbustest.Bus) {
		f := startFake(t, b)
		s := storeOn(t, b)
		for _, v := range []string{"one", "two", "three"} {
			if err := s.Set("acct", []byte(v)); err != nil {
				t.Fatal(err)
			}
		}
		if n := f.count(); n != 1 {
			t.Errorf("%d items after three Sets of one account, want 1", n)
		}
	})
}

func TestSecretServiceLocked(t *testing.T) {
	dbustest.Each(t, func(t *testing.T, b *dbustest.Bus) {
		f := startFake(t, b)
		s := storeOn(t, b)
		if err := s.Set("acct", []byte("kept")); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		f.lock = true
		f.mu.Unlock()
		if _, err := s.Get("acct"); !errors.Is(err, ErrLocked) {
			t.Errorf("Get on a locked collection = %v, want ErrLocked", err)
		}
		if err := s.Delete("acct"); !errors.Is(err, ErrLocked) {
			t.Errorf("Delete on a locked collection = %v, want ErrLocked", err)
		}
		if err := s.Set("other", []byte("x")); !errors.Is(err, ErrLocked) {
			t.Errorf("Set on a locked collection = %v, want ErrLocked", err)
		}
		f.mu.Lock()
		dismissed := f.dismissed
		f.lock = false
		f.mu.Unlock()
		if dismissed != 1 {
			t.Errorf("the unlock prompt was dismissed %d times, want 1 (never run)", dismissed)
		}
		if got, err := s.Get("acct"); err != nil || string(got) != "kept" {
			t.Errorf("after unlocking: %q, %v", got, err)
		}
	})
}

func TestSecretServiceUnavailable(t *testing.T) {
	dbustest.Each(t, func(t *testing.T, b *dbustest.Bus) {
		// A bus without the service.
		s := storeOn(t, b)
		if _, err := s.Get("a"); !errors.Is(err, ErrUnavailable) {
			t.Errorf("no service: Get = %v, want ErrUnavailable", err)
		}
		if err := s.Set("a", []byte("x")); !errors.Is(err, ErrUnavailable) {
			t.Errorf("no service: Set = %v, want ErrUnavailable", err)
		}
	})
	// No bus at all.
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent/gnui-no-bus")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	s := &Store{service: "svc", b: secretService{dial: dbus.DialSession}}
	if _, err := s.Get("a"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("no bus: Get = %v, want ErrUnavailable", err)
	}
}
