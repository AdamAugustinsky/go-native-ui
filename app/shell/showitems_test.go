//go:build !darwin && !windows

package shell

import (
	"context"
	"testing"

	"gonativeui/internal/dbus"
	"gonativeui/internal/dbus/dbustest"
)

func TestShowItems(t *testing.T) {
	dbustest.Each(t, func(t *testing.T, b *dbustest.Bus) {
		dial := func(ctx context.Context) (*dbus.Conn, error) { return dbus.Dial(ctx, b.Address) }

		// No file manager on the bus: the call fails, so that reveal falls back.
		if err := showItems(dial, []string{"/tmp/a"}); err == nil {
			t.Fatal("ShowItems succeeded with no file manager")
		}

		fm, err := dbus.Dial(context.Background(), b.Address)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { fm.Close() })
		got := make(chan []any, 1)
		fm.Export("/org/freedesktop/FileManager1", "org.freedesktop.FileManager1", func(m *dbus.Message) (string, []any, error) {
			if m.Member != "ShowItems" || m.Signature != "ass" {
				return "", nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownMethod"}
			}
			got <- m.Body
			return "", nil, nil
		})
		if r, err := fm.RequestName(context.Background(), "org.freedesktop.FileManager1", 0); err != nil || r != dbus.NameReplyPrimary {
			t.Fatalf("RequestName: %d %v", r, err)
		}
		if err := showItems(dial, []string{"/tmp/my file.txt", "/tmp/caf\u00e9/b"}); err != nil {
			t.Fatal(err)
		}
		body := <-got
		uris, _ := body[0].([]any)
		if len(uris) != 2 || uris[0] != "file:///tmp/my%20file.txt" || uris[1] != "file:///tmp/caf%C3%A9/b" {
			t.Errorf("URIs %q", uris)
		}
	})
}
