//go:build darwin

package fetch

import (
	"context"
	"errors"
	"io"
	"testing"
)

// macOS's proxy settings for NSURLSession have no TLS-secured proxy.
func TestHTTPSProxyUnsupported(t *testing.T) {
	p := Proxy{Kind: ProxyHTTPS, Host: "127.0.0.1", Port: 8443}
	if _, err := Get(context.Background(), "https://example.com/", io.Discard, Options{Proxy: &p}); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("error %v, want errors.ErrUnsupported", err)
	}
}
