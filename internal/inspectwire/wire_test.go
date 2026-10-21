package inspectwire_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gonativeui/internal/inspectwire"
)

// shortDir is a directory of the user's with a short path (a socket path is
// limited to about 100 bytes, which t.TempDir's names exceed on macOS).
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "gi")
	if err != nil {
		t.Fatal(err)
	}
	if d, err = filepath.EvalSymlinks(d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

type echo struct{ got []inspectwire.Request }

func (e *echo) Handle(r *inspectwire.Request) *inspectwire.Response {
	e.got = append(e.got, *r)
	switch r.Op {
	case "boom":
		panic("kaboom")
	case "nil":
		return nil
	}
	return &inspectwire.Response{OK: true, Steps: len(r.Steps)}
}

func TestDispatchDecodesRequestsAndRefusesTheRest(t *testing.T) {
	h := &echo{}
	for _, tc := range []struct{ line, code string }{
		{`{"id":"7","op":"windows"}`, ""},
		{`{"id":"8","op":"do","action":"click","role":"Button","name":"Add","nth":1}`, ""},
		{`{"op":"do","steps":[{"action":"type","text":"x"},{"action":"key","key":"enter"}]}`, ""},
		{`not json`, inspectwire.ErrBadRequest},
		{``, inspectwire.ErrBadRequest},
		{`{"op":"windows","colour":"red"}`, inspectwire.ErrBadRequest},   // an unknown field is a typo, not ignored
		{`{"op":"windows"} {"op":"windows"}`, inspectwire.ErrBadRequest}, // two requests on a line
		{`{"op":"windows","window":-1}`, inspectwire.ErrBadRequest},      // a wrong type
		{`{"op":"boom","id":"9"}`, inspectwire.ErrInternal},              // a handler that panics
		{`{"op":"nil","id":"10"}`, inspectwire.ErrInternal},              // a handler that answers nothing
	} {
		r := inspectwire.Dispatch(h, []byte(tc.line))
		if tc.code == "" {
			if !r.OK || r.Error != nil {
				t.Errorf("%s: %+v", tc.line, r)
			}
			continue
		}
		if r.OK || r.Error == nil || r.Error.Code != tc.code {
			t.Errorf("%s: want error %s, got %+v", tc.line, tc.code, r)
		}
		if r.Error != nil && r.Error.Message == "" {
			t.Errorf("%s: an error with no message", tc.line)
		}
	}
	r := inspectwire.Dispatch(h, []byte(`{"id":"abc","op":"windows"}`))
	if r.ID != "abc" {
		t.Errorf("the id %q is not echoed", r.ID)
	}
	if len(h.got) < 2 || h.got[1].Name != "Add" || h.got[1].Nth != 1 || h.got[1].Action != "click" {
		t.Errorf("the inline step was decoded as %+v", h.got[1])
	}
	if r := inspectwire.Dispatch(h, []byte(`{"id":"9","op":"boom"}`)); r.ID != "9" {
		t.Errorf("a panic loses the id: %+v", r)
	}
}

func TestResponsesAreOneLine(t *testing.T) {
	r := &inspectwire.Response{OK: true, Trees: []inspectwire.Tree{{Nodes: []inspectwire.Node{{Name: "two\nlines\u2028"}}}}}
	b := r.Marshal()
	if bytes.Count(b, []byte("\n")) != 1 || b[len(b)-1] != '\n' {
		t.Fatalf("a response must be exactly one line: %q", b)
	}
	var back inspectwire.Response
	if err := json.Unmarshal(b, &back); err != nil || back.Trees[0].Nodes[0].Name != "two\nlines\u2028" {
		t.Fatalf("round trip: %v %+v", err, back)
	}
}

func TestReadLineHonoursTheLimit(t *testing.T) {
	read := func(in string, max int) (string, error) {
		b, err := inspectwire.ReadLine(bufio.NewReaderSize(strings.NewReader(in), 16), max)
		return string(b), err
	}
	if s, err := read("hello\nrest", 64); s != "hello" || err != nil {
		t.Errorf("%q %v", s, err)
	}
	if s, err := read("hello\r\n", 64); s != "hello" || err != nil {
		t.Errorf("a CRLF: %q %v", s, err)
	}
	if s, err := read(strings.Repeat("x", 100)+"\n", 101); len(s) != 100 || err != nil { // longer than the reader's buffer
		t.Errorf("a line over the buffer but within the limit: %d %v", len(s), err)
	}
	if s, err := read(strings.Repeat("x", 100)+"\n", 100); err != inspectwire.ErrLineTooLong {
		t.Errorf("101 bytes with the newline under a limit of 100: %q %v", s, err)
	}
	if _, err := read(strings.Repeat("x", 100000), 64); err != inspectwire.ErrLineTooLong { // no newline at all
		t.Errorf("an endless line: %v", err)
	}
	if s, err := read("last", 64); s != "last" || err != nil {
		t.Errorf("a last line with no newline: %q %v", s, err)
	}
	if _, err := read("", 64); !errors.Is(err, os.ErrClosed) && err == nil {
		t.Errorf("nothing to read: %v", err)
	}
}

// serve starts a listener in a fresh directory and returns its path.
func serve(t *testing.T, h inspectwire.Handler) string {
	t.Helper()
	sock := filepath.Join(shortDir(t), "s")
	ln, err := inspectwire.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { ln.Serve(h); close(done) }()
	t.Cleanup(func() {
		ln.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after Close")
		}
	})
	return sock
}

func TestRequestsAndResponsesGoLineByLine(t *testing.T) {
	sock := serve(t, &echo{})
	c, err := inspectwire.Dial(sock, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for i, id := range []string{"a", "b", "c"} {
		r, err := c.Call(&inspectwire.Request{ID: id, Op: "do", Steps: make([]inspectwire.Step, i)}, 2*time.Second)
		if err != nil || !r.OK || r.ID != id || r.Steps != i {
			t.Fatalf("%s: %+v %v", id, r, err)
		}
	}
}

// A request over 64 KiB is refused with a structured error and the connection
// is closed, since the rest of its line cannot be told from the next request.
func TestARequestOverTheSizeLimitIsRefused(t *testing.T) {
	h := &echo{}
	sock := serve(t, h)
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	big := `{"op":"do","action":"type","text":"` + strings.Repeat("x", inspectwire.MaxLine) + `"}` + "\n"
	go conn.Write([]byte(big))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var r inspectwire.Response
	if err := json.Unmarshal(line, &r); err != nil || r.OK || r.Error == nil || r.Error.Code != inspectwire.ErrTooLarge {
		t.Fatalf("%s %v", line, err)
	}
	if len(h.got) != 0 {
		t.Error("the handler saw an oversize request")
	}
	// exactly at the limit is fine
	conn2, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	conn2.SetDeadline(time.Now().Add(5 * time.Second))
	pre := `{"op":"windows","path":"`
	req := pre + strings.Repeat("y", inspectwire.MaxLine-len(pre)-len(`"}`)-1) + `"}` + "\n"
	if len(req) != inspectwire.MaxLine {
		t.Fatalf("test request is %d bytes", len(req))
	}
	conn2.Write([]byte(req))
	line, err = bufio.NewReader(conn2).ReadBytes('\n')
	if err != nil || !bytes.Contains(line, []byte(`"ok":true`)) {
		t.Fatalf("a request of exactly %d bytes: %s %v", inspectwire.MaxLine, line, err)
	}
}

func TestConnectionsAreCapped(t *testing.T) {
	sock := serve(t, &echo{})
	var conns []net.Conn
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for i := 0; i < 8; i++ {
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	// make sure the eight are accepted before the ninth arrives
	for _, c := range conns {
		c.Write([]byte(`{"op":"windows"}` + "\n"))
		if _, err := bufio.NewReader(c).ReadBytes('\n'); err != nil {
			t.Fatal(err)
		}
	}
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	line, _ := bufio.NewReader(c).ReadBytes('\n')
	if !bytes.Contains(line, []byte(inspectwire.ErrBusy)) {
		t.Fatalf("the ninth connection got %q", line)
	}
}

func TestShotPath(t *testing.T) {
	d := shortDir(t)
	sockDir := filepath.Join(d, "run")
	os.Mkdir(sockDir, 0o700)
	sock := filepath.Join(sockDir, "s")
	out := filepath.Join(d, "out")
	os.Mkdir(out, 0o755)
	os.Mkdir(filepath.Join(sockDir, "sub"), 0o755)
	link := filepath.Join(d, "link")
	if err := os.Symlink(sockDir, link); err != nil {
		t.Logf("no symbolic links here: %v", err)
		link = ""
	}
	good, err := inspectwire.ShotPath(sock, filepath.Join(out, "a.png"))
	if err != nil || good != filepath.Join(out, "a.png") {
		t.Errorf("a good path: %q %v", good, err)
	}
	bad := map[string]string{
		"relative":          "a.png",
		"dot-relative":      "./a.png",
		"in the socket dir": filepath.Join(sockDir, "a.png"),
		"below it":          filepath.Join(sockDir, "sub", "a.png"),
		"by dot-dot":        filepath.Join(out, "..", "run", "a.png"),
		"missing directory": filepath.Join(out, "nope", "a.png"),
		"a directory":       out,
	}
	if link != "" {
		bad["through a link"] = filepath.Join(link, "a.png")
	}
	for name, p := range bad {
		if got, err := inspectwire.ShotPath(sock, p); err == nil {
			t.Errorf("%s: %q was accepted as %q", name, p, got)
		}
	}
}

func TestCheckDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		d := shortDir(t)
		if _, err := inspectwire.CheckDir(d); err != nil {
			t.Errorf("a directory in the profile: %v", err)
		}
		if _, err := inspectwire.CheckDir(filepath.Join(d, "missing")); err == nil {
			t.Error("a missing directory was accepted")
		}
		return
	}
	d := shortDir(t)
	mk := func(name string, mode os.FileMode) string {
		p := filepath.Join(d, name)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, mode := range []os.FileMode{0o700, 0o755, 0o750, 0o500} {
		if _, err := inspectwire.CheckDir(mk(mode.String(), mode)); err != nil {
			t.Errorf("mode %v refused: %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o770, 0o707, 0o777, 0o720, 0o702} {
		_, err := inspectwire.CheckDir(mk("bad"+mode.String(), mode))
		if err == nil || !strings.Contains(err.Error(), "writable") {
			t.Errorf("mode %v: %v", mode, err)
		}
	}
	f := filepath.Join(d, "file")
	os.WriteFile(f, nil, 0o600)
	if _, err := inspectwire.CheckDir(f); err == nil {
		t.Error("a file was accepted as a directory")
	}
	if _, err := inspectwire.CheckDir(filepath.Join(d, "missing")); err == nil {
		t.Error("a missing directory was accepted")
	}
	if os.Geteuid() != 0 {
		// Somebody else's directory, whatever its mode: the system's own.
		for _, other := range []string{"/", "/usr", "/etc"} {
			if _, err := inspectwire.CheckDir(other); err == nil || !strings.Contains(err.Error(), "not owned") {
				t.Errorf("%s: %v", other, err)
			}
		}
	}
}

func TestListenRefusesAnUnsafeDirectoryAndARelativePath(t *testing.T) {
	d := shortDir(t)
	if _, err := inspectwire.Listen("s"); err == nil {
		t.Error("a relative socket path was accepted")
	}
	if runtime.GOOS == "windows" {
		return
	}
	os.Chmod(d, 0o777)
	if _, err := inspectwire.Listen(filepath.Join(d, "s")); err == nil {
		t.Error("a world-writable directory was accepted")
	}
	if _, err := os.Lstat(filepath.Join(d, "s")); err == nil {
		t.Error("a socket was created anyway")
	}
	os.Chmod(d, 0o770)
	if _, err := inspectwire.Listen(filepath.Join(d, "s")); err == nil {
		t.Error("a group-writable directory was accepted")
	}
}

func TestSocketIsPrivateAndRemovedOnClose(t *testing.T) {
	d := shortDir(t)
	sock := filepath.Join(d, "s")
	ln, err := inspectwire.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(sock)
	if err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("no socket: %v %v", fi, err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("the socket's mode is %v, want 0600", fi.Mode().Perm())
	}
	go ln.Serve(&echo{})
	ln.Close()
	if _, err := os.Lstat(sock); !os.IsNotExist(err) {
		t.Errorf("the socket file remains after Close: %v", err)
	}
}

func TestStaleSocketsAreReplacedAndOtherFilesAreNot(t *testing.T) {
	d := shortDir(t)
	sock := filepath.Join(d, "s")

	// a stale socket: bound, then abandoned without removing the file
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	l.Close()
	if fi, err := os.Lstat(sock); err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Skipf("this system does not report a leftover socket file as a socket (%v, %v): stale sockets are not recognised here", fi, err)
	}
	ln, err := inspectwire.Listen(sock)
	if err != nil {
		t.Fatalf("a stale socket was not replaced: %v", err)
	}

	// a live one is not
	if _, err := inspectwire.Listen(sock); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Errorf("a socket in use was taken over: %v", err)
	}
	if c, err := net.Dial("unix", sock); err != nil {
		t.Errorf("the first listener was disturbed: %v", err)
	} else {
		c.Close()
	}
	go ln.Serve(&echo{})
	ln.Close()

	// a regular file, a directory and a symbolic link are never removed
	for name, mk := range map[string]func(string) error{
		"file":      func(p string) error { return os.WriteFile(p, []byte("precious"), 0o600) },
		"directory": func(p string) error { return os.Mkdir(p, 0o700) },
		"link": func(p string) error {
			os.WriteFile(p+".target", []byte("precious"), 0o600)
			return os.Symlink(p+".target", p)
		},
	} {
		p := filepath.Join(d, name)
		if err := mk(p); err != nil {
			t.Logf("%s: %v", name, err)
			continue
		}
		if l, err := inspectwire.Listen(p); err == nil {
			l.Close()
			t.Errorf("a %s at the socket path was replaced", name)
		}
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("the %s was removed: %v", name, err)
		}
	}
}
