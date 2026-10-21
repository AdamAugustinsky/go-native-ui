// Package childapp runs a live scenario of an app service in a child
// process of the test binary, so that the scenario owns the main thread, the
// application object and the process's lifetime (an allowed quit ends it).
// The test starts the child with Run; the child prints "R key=value" lines
// (Say), which Run returns, and ends with its status. It is for tests only.
//
//	func init() { runtime.LockOSThread() } // the main goroutine keeps the main thread
//
//	func TestMain(m *testing.M) {
//		childapp.Register("badge", func() { app.Main(app.AppConfig{}, h); … })
//		if childapp.Serve() { return }
//		os.Exit(m.Run())
//	}
//
//	r := childapp.Run(t, "badge", 30*time.Second)
//	r.KV["badge"] // what the scenario said
package childapp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const envName = "GNUI_CHILDAPP"

var scenarios = map[string]func(){}

// Register adds scenario name, run by Serve in the child.
func Register(name string, f func()) { scenarios[name] = f }

// Serve runs the scenario named by the environment, if this process is a
// child, and ends the process (status 0, or 2 after Fail). It returns false
// in the test process itself.
func Serve() bool {
	name := os.Getenv(envName)
	if name == "" {
		return false
	}
	f := scenarios[name]
	if f == nil {
		Fail("no scenario %q", name)
	}
	time.AfterFunc(90*time.Second, func() { fmt.Println("FAIL watchdog"); os.Exit(3) })
	f()
	os.Exit(0)
	return true
}

// Say prints one result line, "R key=value …".
func Say(format string, args ...any) { fmt.Printf("R "+format+"\n", args...) }

// Fail ends the child with a message.
func Fail(format string, args ...any) {
	fmt.Printf("FAIL "+format+"\n", args...)
	os.Exit(2)
}

// Skip ends the child and has the test skip with why (a failure on CI).
func Skip(why string) {
	Say("skip=%s", strings.ReplaceAll(why, " ", "_"))
	os.Exit(0)
}

// Result is what a child printed and how it ended.
type Result struct {
	KV   map[string]string
	Out  string
	Code int
}

// Run starts scenario name in a child of the test binary and waits for it at
// most limit. A child that did not end in time, or ended with a failure
// (status other than 0), fails the test with its output.
func Run(t *testing.T, name string, limit time.Duration, env ...string) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(append(os.Environ(), envName+"="+name, "GNUI_NOANIM=1"), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	r := Result{KV: map[string]string{}, Out: out.String()}
	if ctx.Err() != nil {
		t.Fatalf("%s %v: did not end within %v\n%s", name, env, limit, r.Out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		r.Code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	sc := bufio.NewScanner(strings.NewReader(r.Out))
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "R ")
		if !ok {
			continue
		}
		for _, f := range strings.Fields(line) {
			if k, v, ok := strings.Cut(f, "="); ok {
				r.KV[k] = v
			}
		}
	}
	if why := r.KV["skip"]; why != "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s on CI: %s", name, why)
		}
		t.Skipf("%s: %s", name, why)
	}
	if r.Code != 0 {
		t.Fatalf("%s ended with status %d\n%s", name, r.Code, r.Out)
	}
	return r
}
