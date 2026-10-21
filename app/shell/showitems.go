//go:build !darwin && !windows

package shell

import (
	"context"
	"net/url"
	"time"

	"gonativeui/internal/dbus"
)

// showItems calls ShowItems on the session bus with file URIs.
func showItems(dial func(context.Context) (*dbus.Conn, error), paths []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	uris := make([]any, len(paths))
	for i, p := range paths {
		uris[i] = (&url.URL{Scheme: "file", Path: p}).String()
	}
	_, err = c.CallContext(ctx, "org.freedesktop.FileManager1", "/org/freedesktop/FileManager1",
		"org.freedesktop.FileManager1", "ShowItems", "ass", uris, "")
	return err
}
