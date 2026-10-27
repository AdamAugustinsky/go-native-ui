package fetch_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// noNetHTTP are the phase 8 packages that must not link net/http or
// crypto/tls on darwin or windows (D73's size rule): the system's clients
// carry HTTP, TLS and WebSockets there.
var noNetHTTP = []string{
	"gonativeui/app/fetch",
	"gonativeui/app/ws",
	"gonativeui/app/hotkey",
	"gonativeui/app/install",
}

// knownNetHTTP are the packages that still link net/http on a target, until
// the item named moves them off it. A listed package that no longer links
// it fails the test, so the exception is removed when the item lands.
var knownNetHTTP = map[string]string{
	"windows gonativeui/app/fetch": "N1 (WinHTTP)",
}

// TestNoNetHTTP is the CI-style check `go list -deps` of the plan's N1: no
// net/http or crypto/tls in the dependencies on darwin/arm64, windows/amd64
// or windows/arm64.
func TestNoNetHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list per target")
	}
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goTool); err != nil {
		if goTool, err = exec.LookPath("go"); err != nil {
			t.Skip("no go tool")
		}
	}
	for _, target := range []string{"darwin/arm64", "windows/amd64", "windows/arm64"} {
		goos, goarch, _ := strings.Cut(target, "/")
		cmd := exec.Command(goTool, append([]string{"list", "-deps", "-f", "{{.ImportPath}} {{join .Deps \" \"}}"}, noNetHTTP...)...)
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: go list: %v", target, err)
		}
		deps := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			pkg, rest, _ := strings.Cut(line, " ")
			deps[pkg] = " " + rest + " "
		}
		for _, pkg := range noNetHTTP {
			d, ok := deps[pkg]
			if !ok {
				t.Fatalf("%s: %s not listed", target, pkg)
			}
			links := strings.Contains(d, " net/http ") || strings.Contains(d, " crypto/tls ")
			why, known := knownNetHTTP[goos+" "+pkg]
			switch {
			case links && !known:
				t.Errorf("%s: %s links net/http or crypto/tls", target, pkg)
			case !links && known:
				t.Errorf("%s: %s no longer links net/http: remove its exception (%s)", target, pkg, why)
			case links:
				t.Logf("%s: %s links net/http until %s", target, pkg, why)
			}
		}
	}
}
