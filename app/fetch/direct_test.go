package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// nowhere is a name no resolver knows (RFC 2606): only a proxy answers for it.
const nowhere = "http://origin.invalid"

// A ProxyNone transfer connects to the server itself: the origin answers and
// a proxy sees nothing, and a name only a proxy could resolve fails. It
// takes no host, port or credentials.
func TestProxyNone(t *testing.T) {
	var s seen
	proxy := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		fmt.Fprint(w, "via proxy")
	}))
	origin := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "direct") }))
	direct := Options{Proxy: &Proxy{Kind: ProxyNone}}
	got, res, err := Bytes(context.Background(), origin+"/x", 1<<10, direct)
	if err != nil || string(got) != "direct" || res.Status != 200 {
		t.Errorf("direct to the origin: %q, %+v, %v", got, res, err)
	}
	if _, _, err := Bytes(context.Background(), nowhere+"/", 1<<10, direct); err == nil {
		t.Error("a name only a proxy could resolve was fetched without one")
	}
	if reqs, _ := s.snapshot(); len(reqs) != 0 {
		t.Errorf("the proxy saw %q", reqs)
	}
	host, port := hostPort(t, proxy)
	for _, p := range []Proxy{{Kind: ProxyNone, Host: host}, {Kind: ProxyNone, Port: port}, {Kind: ProxyNone, User: "u"}} {
		if _, err := Get(context.Background(), origin, io.Discard, Options{Proxy: &p}); err == nil {
			t.Errorf("%+v accepted", p)
		}
	}
}

// ProxyNone overrides the environment's proxy (Windows and Linux; macOS
// takes the system's settings, which a test cannot set): with HTTP_PROXY
// naming a proxy, a transfer to a name only it can resolve goes through it,
// and the same with ProxyNone never reaches it. The transfers run in a child
// process, where net/http reads the environment afresh.
func TestProxyNoneOverridesTheEnvironment(t *testing.T) {
	if os.Getenv("FETCH_PROXY_CHILD") != "" {
		_, _, viaEnv := Bytes(context.Background(), nowhere+"/env", 1<<10, Options{})
		_, _, direct := Bytes(context.Background(), nowhere+"/direct", 1<<10, Options{Proxy: &Proxy{Kind: ProxyNone}})
		fmt.Printf("CHILD env=%v direct=%v\n", viaEnv == nil, direct == nil)
		return
	}
	if runtime.GOOS == "darwin" {
		t.Skip("macOS takes the system's proxy settings, not the environment's")
	}
	var s seen
	proxy := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		fmt.Fprint(w, "via proxy")
	}))
	cmd := exec.Command(os.Args[0], "-test.run=^TestProxyNoneOverridesTheEnvironment$", "-test.count=1")
	cmd.Env = append(os.Environ(), "FETCH_PROXY_CHILD=1", "HTTP_PROXY="+proxy, "http_proxy="+proxy, "NO_PROXY=", "no_proxy=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "CHILD env=true direct=false") {
		t.Errorf("child: %s; want the environment's transfer to succeed through the proxy and the direct one to fail", out)
	}
	if reqs, _ := s.snapshot(); len(reqs) != 1 || reqs[0] != "GET "+nowhere+"/env" {
		t.Errorf("the proxy saw %q, want only the environment's transfer", reqs)
	}
}
