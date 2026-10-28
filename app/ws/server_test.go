package ws

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testServer is the loopback WebSocket server of N2's suite: our own
// framing in server mode (frame.go), on every OS. The path picks what it
// does after the handshake (handlers); a few paths act before it.
type testServer struct {
	ln  net.Listener
	url string // ws://127.0.0.1:port

	pongs     atomic.Int64
	lastPong  atomic.Value // string
	closeCode atomic.Int64 // the client's close frame's code (0 none yet)
	closeWhy  atomic.Value // string
	header    atomic.Value // http.Header of the last handshake

	handlers map[string]func(p *peer)

	mu    sync.Mutex
	conns []net.Conn
}

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func newServer(t testing.TB) *testServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &testServer{ln: ln, url: "ws://" + ln.Addr().String(), handlers: map[string]func(*peer){}}
	s.lastPong.Store("")
	s.closeWhy.Store("")
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, c)
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		s.mu.Lock()
		for _, c := range s.conns {
			c.Close()
		}
		s.mu.Unlock()
	})
	return s
}

func (s *testServer) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReaderSize(c, 64<<10)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	switch req.URL.Path {
	case "/status401":
		io.WriteString(c, "HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	case "/hang": // never answers the handshake
		io.Copy(io.Discard, br)
		return
	}
	if !strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		io.WriteString(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
		return
	}
	s.header.Store(req.Header.Clone())
	sum := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + wsGUID))
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		base64.StdEncoding.EncodeToString(sum[:]) + "\r\n"
	for _, offered := range strings.Split(req.Header.Get("Sec-WebSocket-Protocol"), ",") {
		if strings.TrimSpace(offered) == "gnui.v2" {
			resp += "Sec-WebSocket-Protocol: gnui.v2\r\n"
		}
	}
	if _, err := io.WriteString(c, resp+"\r\n"); err != nil {
		return
	}
	p := newPeer(c, br, false, 64<<20)
	p.onPong = func(b []byte) {
		s.lastPong.Store(string(b))
		s.pongs.Add(1)
	}
	h := s.handlers[req.URL.Path]
	if h == nil {
		h = echo
	}
	h(p)
}

// echo answers every message with itself until the connection ends; a
// close frame from the client is recorded by the caller's peer.
func echo(p *peer) {
	var buf []byte
	for {
		kind, msg, err := p.read(buf[:0])
		if err != nil {
			return
		}
		buf = msg
		if p.write(kind, msg) != nil {
			return
		}
	}
}

// recordClose reads until the client's close frame and records it.
func (s *testServer) recordClose(p *peer) {
	var buf []byte
	for {
		_, msg, err := p.read(buf[:0])
		if ce := p.peerClose.Load(); ce != nil {
			s.closeWhy.Store(ce.Reason)
			s.closeCode.Store(int64(ce.Code))
			return
		}
		if err != nil {
			return
		}
		buf = msg
	}
}

// waitFor polls cond for up to d.
func waitFor(d time.Duration, cond func() bool) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

// proxyServer is an HTTP proxy for the tests: CONNECT tunnels, and
// absolute-form requests forwarded to their host (a ws:// upgrade through
// a proxy may take either path). It records the first line of each
// request.
type proxyServer struct {
	ln   net.Listener
	mu   sync.Mutex
	seen []string
}

func newProxy(t testing.TB) *proxyServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ps := &proxyServer{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go ps.serve(c)
		}
	}()
	return ps
}

func (ps *proxyServer) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	ps.mu.Lock()
	ps.seen = append(ps.seen, req.Method+" "+req.RequestURI)
	ps.mu.Unlock()
	target := req.Host
	if req.Method != "CONNECT" {
		target = req.URL.Host
	}
	if h, port, err := net.SplitHostPort(target); err == nil && h == "origin" {
		target = net.JoinHostPort("127.0.0.1", port) // a name only the proxy resolves
	}
	up, err := net.Dial("tcp", target)
	if err != nil {
		io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer up.Close()
	if req.Method == "CONNECT" {
		io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
	} else {
		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authorization")
		req.URL.Scheme, req.URL.Host = "", ""
		if err := req.Write(up); err != nil {
			return
		}
	}
	go func() { io.Copy(up, br); up.Close() }()
	io.Copy(c, up)
}

func (ps *proxyServer) lines() []string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return append([]string(nil), ps.seen...)
}
