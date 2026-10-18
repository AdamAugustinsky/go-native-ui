package dbus

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func dial(t *testing.T, b *testBus) *Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, b.address)
	if err != nil {
		t.Fatalf("Dial %s: %v", b.address, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

const (
	svcName  = "org.example.Fake"
	svcPath  = ObjectPath("/org/example/Fake")
	svcIface = "org.example.Fake"
)

// A service that owns svcName and answers a few methods.
func startService(t *testing.T, b *testBus) *Conn {
	t.Helper()
	s := dial(t, b)
	s.Export(svcPath, svcIface, func(m *Message) (string, []any, error) {
		switch m.Member {
		case "Swap": // (s, u) -> (u, s)
			return "us", []any{m.Body[1], m.Body[0]}, nil
		case "Fail":
			return "", nil, &Error{Name: "org.example.Error.Nope", Body: []any{"no thanks"}}
		case "FailPlain":
			return "", nil, errors.New("plain")
		case "Block":
			time.Sleep(300 * time.Millisecond)
			return "", nil, nil
		case "Huge":
			return "s", []any{strings.Repeat("x", MaxMessage+1)}, nil
		case "Signal": // emits before it replies, like a portal answering at once
			s.Emit(svcPath, svcIface, "Done", "s", m.Body[0])
			return "", nil, nil
		}
		return "", nil, &Error{Name: "org.freedesktop.DBus.Error.UnknownMethod"}
	})
	r, err := s.RequestName(context.Background(), svcName, 0)
	if err != nil || r != NameReplyPrimary {
		t.Fatalf("RequestName: %d %v", r, err)
	}
	return s
}

func TestCallAndReply(t *testing.T) {
	eachBus(t, func(t *testing.T, b *testBus) {
		startService(t, b)
		c := dial(t, b)
		if !strings.HasPrefix(c.Name(), ":") {
			t.Errorf("unique name %q", c.Name())
		}
		r, err := c.Call(svcName, svcPath, svcIface, "Swap", "su", "left", uint32(7))
		if err != nil {
			t.Fatal(err)
		}
		if r.Signature != "us" || len(r.Body) != 2 || r.Body[0] != uint32(7) || r.Body[1] != "left" {
			t.Errorf("reply %+v", r)
		}
		for _, tc := range []struct{ member, name string }{
			{"Fail", "org.example.Error.Nope"},
			{"FailPlain", "org.freedesktop.DBus.Error.Failed"},
			{"NoSuchMethod", "org.freedesktop.DBus.Error.UnknownMethod"},
			{"Huge", "org.freedesktop.DBus.Error.Failed"}, // the reply cannot be sent: an error, not silence
		} {
			_, err := c.Call(svcName, svcPath, svcIface, tc.member, "")
			var e *Error
			if !errors.As(err, &e) || e.Name != tc.name {
				t.Errorf("%s: error %v, want %s", tc.member, err, tc.name)
			}
		}
		if _, err := c.Call("org.example.Nobody", svcPath, svcIface, "X", ""); err == nil || !strings.Contains(err.Error(), "ServiceUnknown") {
			t.Errorf("call to a missing service: %v", err)
		}
		// Arguments that do not fit their signature fail before anything is sent.
		if _, err := c.Call(svcName, svcPath, svcIface, "Swap", "su", 1, 2); err == nil {
			t.Error("bad arguments accepted")
		}
	})
}

func TestCallContext(t *testing.T) {
	eachBus(t, func(t *testing.T, b *testBus) {
		startService(t, b)
		c := dial(t, b)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		t0 := time.Now()
		_, err := c.CallContext(ctx, svcName, svcPath, svcIface, "Block", "")
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(t0) > 250*time.Millisecond {
			t.Errorf("err %v after %v", err, time.Since(t0))
		}
		// The late reply is dropped; the connection still works.
		if _, err := c.Call(svcName, svcPath, svcIface, "Swap", "su", "a", uint32(1)); err != nil {
			t.Error(err)
		}
		c.mu.Lock()
		n := len(c.pending)
		c.mu.Unlock()
		if n != 0 {
			t.Errorf("%d calls still pending", n)
		}
	})
}

func TestSignals(t *testing.T) {
	eachBus(t, func(t *testing.T, b *testBus) {
		s := startService(t, b)
		c := dial(t, b)
		got := make(chan *Message, 8)
		sub, err := c.Subscribe(context.Background(), Match{Sender: svcName, Path: svcPath, Interface: svcIface, Member: "Changed"},
			func(m *Message) { got <- m })
		if err != nil {
			t.Fatal(err)
		}
		other := make(chan *Message, 8)
		argSub, err := c.Subscribe(context.Background(), Match{Sender: svcName, Interface: svcIface, Member: "Changed", Arg0: "wanted"},
			func(m *Message) { other <- m })
		if err != nil {
			t.Fatal(err)
		}
		s.Emit(svcPath, svcIface, "Changed", "sv", "unwanted", V(uint32(1)))
		s.Emit(svcPath, svcIface, "Changed", "sv", "wanted", Variant{"(ddd)", Struct{0.25, 0.5, 0.75}})
		s.Emit(svcPath, svcIface, "Other", "s", "not subscribed")
		for i, want := range []string{"unwanted", "wanted"} {
			select {
			case m := <-got:
				if m.Body[0] != want || m.Sender == "" || m.Path != svcPath {
					t.Errorf("signal %d: %+v", i, m)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("signal %d never arrived", i)
			}
		}
		select {
		case m := <-other:
			if m.Body[0] != "wanted" {
				t.Errorf("Arg0 subscription got %+v", m)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Arg0 signal never arrived")
		}
		select {
		case m := <-got:
			t.Errorf("a signal that was not subscribed arrived: %+v", m)
		case m := <-other:
			t.Errorf("the Arg0 subscription got the other signal: %+v", m)
		case <-time.After(50 * time.Millisecond):
		}
		// After Close the callback is not called again. A later signal on a
		// second subscription proves the first had its chance.
		sub.Close()
		argSub.Close()
		marker := make(chan struct{}, 1)
		if _, err := c.Subscribe(context.Background(), Match{Interface: svcIface, Member: "Marker"}, func(*Message) { marker <- struct{}{} }); err != nil {
			t.Fatal(err)
		}
		s.Emit(svcPath, svcIface, "Changed", "sv", "wanted", V(uint32(2)))
		s.Emit(svcPath, svcIface, "Marker", "")
		<-marker
		select {
		case m := <-got:
			t.Errorf("signal after Close: %+v", m)
		case m := <-other:
			t.Errorf("signal after Close: %+v", m)
		default:
		}
	})
}

// A signal sent while the call is being answered reaches a subscription made
// before the call, before the reply: what the portal's Response needs.
func TestSubscribeBeforeCall(t *testing.T) {
	eachBus(t, func(t *testing.T, b *testBus) {
		startService(t, b)
		c := dial(t, b)
		var seen atomic.Bool
		if _, err := c.Subscribe(context.Background(), Match{Interface: svcIface, Member: "Done", Arg0: "req1"}, func(*Message) { seen.Store(true) }); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Call(svcName, svcPath, svcIface, "Signal", "s", "req1"); err != nil {
			t.Fatal(err)
		}
		if !seen.Load() {
			t.Error("the signal sent before the reply was lost")
		}
	})
}

func TestConnectionLost(t *testing.T) {
	eachBus(t, func(t *testing.T, b *testBus) {
		startService(t, b)
		c := dial(t, b)
		var wg sync.WaitGroup
		wg.Add(1)
		var callErr error
		go func() {
			defer wg.Done()
			_, callErr = c.Call(svcName, svcPath, svcIface, "Block", "")
		}()
		time.Sleep(50 * time.Millisecond)
		b.stop()
		wg.Wait()
		if callErr == nil {
			t.Error("a call in flight survived the bus")
		}
		select {
		case <-c.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("Done never closed")
		}
		if c.Err() == nil {
			t.Error("Err is nil after Done")
		}
		if _, err := c.Call(svcName, svcPath, svcIface, "Swap", "su", "a", uint32(1)); err == nil {
			t.Error("call on a dead connection succeeded")
		}
	})
}

func TestNoBus(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if _, err := DialSession(context.Background()); !errors.Is(err, ErrNoBus) {
		t.Errorf("no environment: %v", err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent/gnui/bus")
	t0 := time.Now()
	if _, err := DialSession(context.Background()); err == nil || time.Since(t0) > time.Second {
		t.Errorf("missing socket: %v after %v", err, time.Since(t0))
	}
}

func TestSessionAddressFallback(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "gnuixdg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ln, err := net.Listen("unix", dir+"/bus")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", dir)
	if a, err := SessionAddress(); err != nil || a != "unix:path="+dir+"/bus" {
		t.Errorf("fallback: %q %v", a, err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/elsewhere")
	if a, _ := SessionAddress(); a != "unix:path=/elsewhere" {
		t.Errorf("the variable should win: %q", a)
	}
}

// Peers that misbehave: the dial fails or the connection ends, quickly, with
// no panic and no unbounded allocation.
func TestMisbehavingBus(t *testing.T) {
	serveOnce := func(t *testing.T, script func(nc net.Conn)) string {
		dir, err := os.MkdirTemp("/tmp", "gnuibad")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		ln, err := net.Listen("unix", dir+"/bus")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			for {
				nc, err := ln.Accept()
				if err != nil {
					return
				}
				go func() { defer nc.Close(); script(nc) }()
			}
		}()
		return "unix:path=" + dir + "/bus"
	}
	drain := func(nc net.Conn) { nc.Read(make([]byte, 512)) }

	t.Run("rejects the authentication", func(t *testing.T) {
		a := serveOnce(t, func(nc net.Conn) { drain(nc); nc.Write([]byte("REJECTED EXTERNAL\r\n")) })
		if _, err := Dial(context.Background(), a); err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("err %v", err)
		}
	})
	t.Run("never answers", func(t *testing.T) {
		a := serveOnce(t, func(nc net.Conn) { drain(nc); time.Sleep(2 * time.Second) })
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		t0 := time.Now()
		if _, err := Dial(ctx, a); err == nil || time.Since(t0) > time.Second {
			t.Errorf("err %v after %v", err, time.Since(t0))
		}
	})
	t.Run("answers Hello with garbage", func(t *testing.T) {
		a := serveOnce(t, func(nc net.Conn) {
			drain(nc)
			nc.Write([]byte("OK 00\r\n"))
			nc.Write([]byte{'l', 2, 0, 1, 0xff, 0xff, 0xff, 0x7f, 1, 0, 0, 0, 0, 0, 0, 0}) // a 2 GiB body
			time.Sleep(time.Second)
		})
		if _, err := Dial(context.Background(), a); err == nil {
			t.Error("accepted")
		}
	})
}
