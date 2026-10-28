//go:build !darwin && !windows

package ws

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gonativeui/app/fetch"
)

// Linux and the rest: net/http's upgrade (a 101 response's Body is the
// connection) with our own framing (frame.go).

const acceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// acceptKey is the Sec-WebSocket-Accept a server answers key with.
func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + acceptGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func proxyFunc(p *fetch.Proxy) (func(*http.Request) (*url.URL, error), error) {
	if p == nil {
		return http.ProxyFromEnvironment, nil
	}
	if p.Kind == fetch.ProxyNone {
		return nil, nil
	}
	pu := &url.URL{Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port))}
	switch p.Kind {
	case 0, fetch.ProxyHTTP:
		pu.Scheme = "http"
	case fetch.ProxyHTTPS:
		pu.Scheme = "https"
	case fetch.ProxySOCKS5:
		pu.Scheme = "socks5"
	default:
		return nil, errors.New("ws: unknown proxy kind")
	}
	if p.User != "" || p.Password != "" {
		pu.User = url.UserPassword(p.User, p.Password)
	}
	return http.ProxyURL(pu), nil
}

func osDial(ctx context.Context, rawURL string, o Options) (backend, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, "", fmt.Errorf("%w: %q", ErrBadURL, rawURL)
	}
	u.Scheme = map[string]string{"ws": "http", "wss": "https"}[u.Scheme]
	proxy, err := proxyFunc(o.Proxy)
	if err != nil {
		return nil, "", err
	}
	tr := &http.Transport{
		Proxy: proxy,
		// HTTP/1.1 only: the upgrade is an HTTP/1.1 mechanism.
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	var nonce [16]byte
	rand.Read(nonce[:])
	key := base64.StdEncoding.EncodeToString(nonce[:])

	// The request's context must outlive the handshake (it is the
	// connection's), so ctx only cancels the handshake.
	reqCtx, cancelReq := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(reqCtx, "GET", u.String(), nil)
	if err != nil {
		cancelReq()
		return nil, "", err
	}
	for k, v := range o.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", key)
	req.Header.Set("Sec-WebSocket-Version", "13")
	if len(o.Subprotocols) > 0 {
		req.Header.Set("Sec-WebSocket-Protocol", strings.Join(o.Subprotocols, ", "))
	}
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	stop := context.AfterFunc(ctx, cancelReq)
	resp, err := client.Do(req)
	if !stop() { // ctx ended the handshake
		if err == nil {
			resp.Body.Close()
		}
		cancelReq()
		return nil, "", ctx.Err()
	}
	if err != nil {
		cancelReq()
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, "", fmt.Errorf("ws: %w", err)
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if resp.StatusCode != http.StatusSwitchingProtocols || !ok {
		resp.Body.Close()
		cancelReq()
		return nil, "", &HandshakeError{Status: resp.StatusCode}
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") || resp.Header.Get("Sec-WebSocket-Accept") != acceptKey(key) {
		rwc.Close()
		cancelReq()
		return nil, "", fmt.Errorf("%w: the server's handshake does not accept ours", errProtocol)
	}
	p := newPeer(rwc, nil, true, o.MaxMessage)
	p.newKey = func(k *[4]byte) { rand.Read(k[:]) }
	p.onClosed = cancelReq
	return p, resp.Header.Get("Sec-WebSocket-Protocol"), nil
}
