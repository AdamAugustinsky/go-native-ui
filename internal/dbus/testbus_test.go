package dbus

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Two buses for the same tests: the real dbus-daemon when the binary exists
// (CI installs it), and a small broker written here that implements just
// what the tests use (Hello, RequestName, AddMatch, routing by name, signal
// matching). The broker lets the suite run where no daemon is installed; only
// the daemon checks the client against an independent implementation.

type testBus struct {
	name    string
	address string
	stop    func()
}

// buses starts every bus available and runs fn against each as a subtest.
func eachBus(t *testing.T, fn func(t *testing.T, b *testBus)) {
	t.Helper()
	t.Run("broker", func(t *testing.T) {
		b := startBroker(t)
		fn(t, b)
	})
	t.Run("dbus-daemon", func(t *testing.T) {
		b := startDaemon(t)
		if b == nil {
			t.Skip("dbus-daemon is not installed: the client was only checked against the in-process broker")
		}
		fn(t, b)
	})
}

func startDaemon(t *testing.T) *testBus {
	t.Helper()
	path, err := exec.LookPath("dbus-daemon")
	if err != nil {
		return nil
	}
	dir, err := os.MkdirTemp("/tmp", "gnuidbus")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	conf := dir + "/bus.conf"
	// A private bus that does not read the system's session configuration.
	err = os.WriteFile(conf, []byte(`<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:path=`+dir+`/bus</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*" eavesdrop="true"/>
    <allow receive_sender="*" eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "--config-file="+conf, "--nofork", "--print-address")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("dbus-daemon: %v", err)
	}
	stop := func() { cmd.Process.Kill(); cmd.Wait() }
	t.Cleanup(stop)
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(out).ReadString('\n')
		line <- strings.TrimSpace(s)
	}()
	select {
	case a := <-line:
		if a == "" {
			t.Fatal("dbus-daemon printed no address")
		}
		t.Logf("dbus-daemon at %s", a)
		return &testBus{"dbus-daemon", a, stop}
	case <-time.After(10 * time.Second):
		t.Fatal("dbus-daemon did not print its address")
	}
	return nil
}

// ---- the in-process broker ------------------------------------------------------

type broker struct {
	ln      net.Listener
	mu      sync.Mutex
	clients map[string]*brokerClient
	names   map[string]string // well-known name -> unique name
	next    int
}

type brokerClient struct {
	b     *broker
	nc    net.Conn
	name  string
	wmu   sync.Mutex
	rules []map[string]string
}

func startBroker(t *testing.T) *testBus {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "gnuibroker")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := net.Listen("unix", dir+"/bus")
	if err != nil {
		t.Fatal(err)
	}
	b := &broker{ln: ln, clients: map[string]*brokerClient{}, names: map[string]string{}}
	go b.accept()
	stop := func() {
		ln.Close()
		b.mu.Lock()
		for _, c := range b.clients {
			c.nc.Close()
		}
		b.mu.Unlock()
	}
	t.Cleanup(stop)
	return &testBus{"broker", "unix:path=" + dir + "/bus,guid=0123456789abcdef0123456789abcdef", stop}
}

func (b *broker) accept() {
	for {
		nc, err := b.ln.Accept()
		if err != nil {
			return
		}
		go b.serve(nc)
	}
}

func (b *broker) serve(nc net.Conn) {
	defer nc.Close()
	br := bufio.NewReader(nc)
	if z, err := br.ReadByte(); err != nil || z != 0 {
		return
	}
	line, err := br.ReadString('\n')
	if want := "AUTH EXTERNAL " + hex.EncodeToString([]byte(strconv.Itoa(os.Getuid()))) + "\r\n"; err != nil || line != want {
		nc.Write([]byte("REJECTED EXTERNAL\r\n"))
		return
	}
	nc.Write([]byte("OK 0123456789abcdef0123456789abcdef\r\n"))
	if line, err := br.ReadString('\n'); err != nil || line != "BEGIN\r\n" {
		return
	}
	b.mu.Lock()
	b.next++
	c := &brokerClient{b: b, nc: nc, name: fmt.Sprintf(":1.%d", b.next)}
	b.clients[c.name] = c
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.clients, c.name)
		for n, o := range b.names {
			if o == c.name {
				delete(b.names, n)
			}
		}
		b.mu.Unlock()
	}()
	for {
		m, err := readMessage(br)
		if err != nil {
			return
		}
		b.route(c, m)
	}
}

func (c *brokerClient) write(m *Message) {
	buf, err := m.marshal()
	if err != nil {
		return
	}
	c.wmu.Lock()
	c.nc.Write(buf)
	c.wmu.Unlock()
}

func (b *broker) reply(c *brokerClient, to *Message, sig string, body ...any) {
	c.write(&Message{Type: TypeMethodReturn, Serial: 1000000 + to.Serial, ReplySerial: to.Serial, Destination: c.name, Sender: busName, Signature: sig, Body: body})
}

func (b *broker) fail(c *brokerClient, to *Message, name, text string) {
	if to.Flags&FlagNoReplyExpected != 0 {
		return
	}
	c.write(&Message{Type: TypeError, Serial: 1000000 + to.Serial, ReplySerial: to.Serial, ErrorName: name, Destination: c.name, Sender: busName, Signature: "s", Body: []any{text}})
}

func parseRule(s string) map[string]string {
	r := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		k, v, _ := strings.Cut(kv, "=")
		r[k] = strings.Trim(v, "'")
	}
	return r
}

func (b *broker) route(c *brokerClient, m *Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m.Sender = c.name
	if m.Type == TypeSignal {
		for _, o := range b.clients {
			for _, r := range o.rules {
				if b.ruleMatches(r, m) {
					o.write(m)
					break
				}
			}
		}
		return
	}
	if m.Destination == busName {
		switch m.Member {
		case "Hello":
			b.reply(c, m, "s", c.name)
		case "RequestName":
			name := m.Body[0].(string)
			if o, taken := b.names[name]; taken && o != c.name {
				b.reply(c, m, "u", uint32(3))
			} else {
				b.names[name] = c.name
				b.reply(c, m, "u", uint32(1))
			}
		case "AddMatch":
			c.rules = append(c.rules, parseRule(m.Body[0].(string)))
			b.reply(c, m, "")
		case "RemoveMatch":
			want := m.Body[0].(string)
			for i, r := range c.rules {
				if fmt.Sprint(r) == fmt.Sprint(parseRule(want)) {
					c.rules = append(c.rules[:i:i], c.rules[i+1:]...)
					break
				}
			}
			b.reply(c, m, "")
		default:
			b.fail(c, m, "org.freedesktop.DBus.Error.UnknownMethod", m.Member)
		}
		return
	}
	dest := m.Destination
	if u, ok := b.names[dest]; ok {
		dest = u
	}
	o := b.clients[dest]
	if o == nil {
		b.fail(c, m, "org.freedesktop.DBus.Error.ServiceUnknown", "The name "+m.Destination+" is not known")
		return
	}
	o.write(m)
}

func (b *broker) ruleMatches(r map[string]string, m *Message) bool {
	if t := r["type"]; t != "" && t != "signal" {
		return false
	}
	if s := r["sender"]; s != "" && s != m.Sender && b.names[s] != m.Sender {
		return false
	}
	if v := r["interface"]; v != "" && v != m.Interface {
		return false
	}
	if v := r["member"]; v != "" && v != m.Member {
		return false
	}
	if v := r["path"]; v != "" && v != string(m.Path) {
		return false
	}
	if v := r["arg0"]; v != "" {
		if a, ok := m.firstString(); !ok || a != v {
			return false
		}
	}
	return true
}
