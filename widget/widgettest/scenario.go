package widgettest

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Scenarios: app-level flows that end the process. An allowed quit makes AppKit
// terminate the process, so a flow that quits cannot run inside the test
// binary. A scenario is a function registered by name (RegisterScenario, in
// an init or a test file); Scenario starts the test binary again as a child
// with GNUI_SCENARIO set to the name, RunMain runs the function on the child's
// main thread instead of the tests, and the child reports what it saw as
// lines "R key=value ..." (Say) that the parent reads, together with the exit
// status. A scenario that hangs is killed after its limit and fails the test.

// scenarioEnv names the environment variable that selects the scenario a
// child runs.
const scenarioEnv = "GNUI_SCENARIO"

var scenarios = map[string]func(){}

// RegisterScenario registers the function a child process runs for name.
// Register in an init function, before RunMain.
func RegisterScenario(name string, f func()) {
	if scenarios[name] != nil {
		panic("widgettest: scenario " + name + " registered twice")
	}
	scenarios[name] = f
}

// runScenario runs the child's scenario, if this process is one, on the
// calling (main) thread and exits. RunMain calls it first.
func runScenario() {
	name := os.Getenv(scenarioEnv)
	if name == "" {
		return
	}
	f := scenarios[name]
	if f == nil {
		fmt.Printf("FAIL no scenario %q\n", name)
		os.Exit(2)
	}
	time.AfterFunc(90*time.Second, func() { fmt.Println("FAIL watchdog: the scenario ran 90 s"); os.Exit(3) })
	f()
	os.Exit(0)
}

// Say reports a result line from a scenario: "key=value" pairs, no spaces in
// the values.
func Say(format string, args ...any) { fmt.Printf("R "+format+"\n", args...) }

// Fail ends a scenario with a failure message.
func Fail(format string, args ...any) {
	fmt.Printf("FAIL "+format+"\n", args...)
	os.Exit(2)
}

// ScenarioResult is what a scenario's child process printed and how it ended.
type ScenarioResult struct {
	// KV are the key=value pairs of its "R" lines (later lines win).
	KV map[string]string
	// Out is everything it printed.
	Out string
	// Code is its exit status.
	Code int
	// Took is how long it ran.
	Took time.Duration
}

// Get is the value of key ("" if the scenario did not report it).
func (r ScenarioResult) Get(key string) string { return r.KV[key] }

// Int is the value of key as a number (0 if missing or not a number).
func (r ScenarioResult) Int(key string) int { n, _ := strconv.Atoi(r.KV[key]); return n }

// Scenario runs the registered scenario name in a child process with env
// ("KEY=value") added, and waits for it at most limit; a child that does not
// end fails the test. A child that reports skip=why is skipped, but not in
// CI. The test binary must call RunMain in its TestMain.
func Scenario(t testing.TB, name string, limit time.Duration, env ...string) ScenarioResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(append(os.Environ(), scenarioEnv+"="+name, "GNUI_NOANIM=1"), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	t0 := time.Now()
	err := cmd.Run()
	r := ScenarioResult{KV: map[string]string{}, Out: out.String(), Took: time.Since(t0)}
	if ctx.Err() != nil {
		t.Fatalf("scenario %s %v: did not end within %v (deadlock?)\n%s", name, env, limit, r.Out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		r.Code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("scenario %s: %v", name, err)
	}
	sc := bufio.NewScanner(strings.NewReader(r.Out))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "R ") {
			continue
		}
		for _, f := range strings.Fields(line[2:]) {
			if k, v, ok := strings.Cut(f, "="); ok {
				r.KV[k] = v
			}
		}
	}
	if why := r.KV["skip"]; why != "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("scenario %s on CI: %s", name, why)
		}
		t.Skipf("scenario %s: %s", name, why)
	}
	return r
}
