//go:build unix

package img

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/internal/dbus"
	"gonativeui/internal/dbus/dbustest"
)

// fakeSettingsPortal serves the settings of the desktop portal on b: ReadOne
// (or only the version 1 Read, which wraps the value in a second variant).
func fakeSettingsPortal(t *testing.T, b *dbustest.Bus, vals map[string]string, v1 bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dbus.Dial(ctx, b.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.Export(portalPath, portalSettings, func(m *dbus.Message) (string, []any, error) {
		if m.Member == "ReadOne" && v1 || m.Member != "ReadOne" && m.Member != "Read" {
			return "", nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownMethod"}
		}
		ns, _ := m.Body[0].(string)
		key, _ := m.Body[1].(string)
		v, ok := vals[ns+" "+key]
		if !ok {
			return "", nil, &dbus.Error{Name: "org.freedesktop.portal.Error.NotFound", Body: []any{"Requested setting not found"}}
		}
		val := dbus.Variant{Sig: "s", Val: v}
		if m.Member == "Read" {
			return "v", []any{dbus.Variant{Sig: "v", Val: val}}, nil
		}
		return "v", []any{val}, nil
	})
	if r, err := c.RequestName(ctx, portalName, dbus.NameDoNotQueue); err != nil || r != dbus.NameReplyPrimary {
		t.Fatalf("RequestName: %d, %v", r, err)
	}
}

func TestDesktopIconTheme(t *testing.T) {
	dbustest.Each(t, func(t *testing.T, b *dbustest.Bus) {
		fakeSettingsPortal(t, b, map[string]string{themeNS + " " + themeKey: "Other"}, false)
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", b.Address)
		if got := desktopIconTheme(); got != "Other" {
			t.Errorf("theme %q, want Other", got)
		}
	})
	t.Run("v1", func(t *testing.T) {
		b := dbustest.Broker(t)
		fakeSettingsPortal(t, b, map[string]string{themeNS + " " + themeKey: "Other"}, true)
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", b.Address)
		if got := desktopIconTheme(); got != "Other" {
			t.Errorf("a version 1 portal: theme %q, want Other", got)
		}
	})
	t.Run("no setting", func(t *testing.T) {
		b := dbustest.Broker(t)
		fakeSettingsPortal(t, b, map[string]string{}, false)
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", b.Address)
		if got := desktopIconTheme(); got != "" {
			t.Errorf("a portal without the key: theme %q, want none", got)
		}
	})
	t.Run("no portal", func(t *testing.T) {
		b := dbustest.Broker(t)
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", b.Address)
		if got := desktopIconTheme(); got != "" {
			t.Errorf("a bus without the portal: theme %q, want none", got)
		}
	})
	t.Run("no bus", func(t *testing.T) {
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
		t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
		if got := desktopIconTheme(); got != "" {
			t.Errorf("no bus: theme %q, want none", got)
		}
	})
}

// TestThemeChainTakesTheDesktopsTheme: the desktop's setting leads the chain,
// settings.ini only when the desktop has none.
func TestThemeChainTakesTheDesktopsTheme(t *testing.T) {
	root := xdgTree(t) // settings.ini names Fancy
	other := filepath.Join(root, "share", "icons", "Other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "index.theme"), []byte("[Icon Theme]\nName=Other\nInherits=hicolor\nDirectories=48x48/mimetypes\n\n[48x48/mimetypes]\nSize=48\nType=Fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := xdgSystem().themeChain(); len(got) == 0 || got[0] != "Fancy" {
		t.Errorf("without a desktop setting: chain %v, want Fancy first", got)
	}
	b := dbustest.Broker(t)
	fakeSettingsPortal(t, b, map[string]string{themeNS + " " + themeKey: "Other"}, false)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", b.Address)
	got := xdgSystem().themeChain()
	if len(got) < 2 || got[0] != "Other" || got[1] != "hicolor" {
		t.Errorf("with the desktop's setting: chain %v, want Other, hicolor", got)
	}
}
