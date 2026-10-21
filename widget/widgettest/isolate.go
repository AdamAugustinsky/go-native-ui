package widgettest

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

const isolatedEnv = "GNUI_ISOLATED_TEST"

// RunMainIsolated is RunMain with each top-level test in a child process of
// its own: the test binary run again with -test.run=^Name$. A test whose app
// does not end (a LiveApp is one per process) fails alone; the next test
// starts in a fresh process instead of finding the platform taken. A child
// still running at the test timeout (-test.timeout, else 10 minutes) is
// killed with SIGKILL. Use it in TestMain instead of RunMain:
//
//	func TestMain(m *testing.M) { widgettest.RunMainIsolated(m) }
func RunMainIsolated(m *testing.M) {
	if os.Getenv(isolatedEnv) != "" || os.Getenv(scenarioEnv) != "" {
		RunMain(m)
		return
	}
	flag.Parse()
	os.Exit(runIsolated())
}

func runIsolated() int {
	str := func(name string) string {
		if f := flag.Lookup(name); f != nil {
			return f.Value.String()
		}
		return ""
	}
	runTop, runRest := splitPattern(str("test.run"))
	skipTop, skipRest := splitPattern(str("test.skip"))
	timeout := 10 * time.Minute
	if d, err := time.ParseDuration(str("test.timeout")); err == nil && d > 0 {
		timeout = d
	}
	failfast := str("test.failfast") == "true"

	// The flags the children get: every test flag that was set, except the
	// ones the parent decides (run, list) or that one file per run holds.
	var pass []string
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "test.run", "test.list", "test.testlogfile", "test.coverprofile", "test.gocoverdir":
			return
		case "test.skip":
			if skipRest == "" {
				return // applied here, to the names
			}
		}
		if strings.HasPrefix(f.Name, "test.") {
			pass = append(pass, "-"+f.Name+"="+f.Value.String())
		}
	})

	list := runTop
	if list == "" {
		list = "."
	}
	lc, cancel := child(timeout, "-test.list="+list)
	out, err := lc.Output()
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "widgettest: listing the tests: %v\n", err)
		return 1
	}
	var skip *regexp.Regexp
	if skipTop != "" && skipRest == "" {
		if skip, err = regexp.Compile(skipTop); err != nil {
			fmt.Fprintf(os.Stderr, "widgettest: -test.skip: %v\n", err)
			return 1
		}
	}
	var names []string
	for sc := bufio.NewScanner(bytes.NewReader(out)); sc.Scan(); {
		n := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(n, "Test") && (skip == nil || !skip.MatchString(n)) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		fmt.Println("testing: warning: no tests to run")
		fmt.Println("PASS")
		return 0
	}
	failed := false
	for _, n := range names {
		run := "^" + regexp.QuoteMeta(n) + "$"
		if runRest != "" {
			run += "/" + runRest
		}
		cmd, cancel := child(timeout, append([]string{"-test.run=" + run}, pass...)...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		err := cmd.Run()
		cancel()
		if err != nil {
			failed = true
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				fmt.Printf("--- FAIL: %s: its process (pid %d) did not end within %v and was killed: %v\n", n, pid(cmd), timeout, err)
			}
			if failfast {
				break
			}
		}
	}
	if failed {
		fmt.Println("FAIL")
		return 1
	}
	fmt.Println("PASS")
	return 0
}

// child is the test binary run again as an isolated child with args; its
// context kills it (SIGKILL) at the timeout. Call cancel once it has run.
func child(timeout time.Duration, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), isolatedEnv+"=1")
	cmd.WaitDelay = 5 * time.Second
	return cmd, cancel
}

func pid(cmd *exec.Cmd) int {
	if cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

// splitPattern splits a -test.run or -test.skip pattern into its top-level
// part and the rest (the subtest levels), at the first slash.
func splitPattern(p string) (top, rest string) {
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}
