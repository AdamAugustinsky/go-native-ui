package widgettest

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
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
	if lf := liveScenarios[name]; lf != nil {
		runLiveScenario(lf)
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

// liveScenarios are the scenarios that drive a LiveApp: they need a test
// goroutine of their own, with the main thread free to serve it.
var liveScenarios = map[string]func(testing.TB){}

// RegisterLiveScenario registers a scenario that is a test of the harness
// itself: a function that gets a testing.TB and may start a LiveApp with it,
// as a test does, in a child process. The TB records instead of failing: a
// Fatal, a Skip or an Error is reported to the parent as a line "T kind:
// message" in the output and as outcome=fatal|errors|skip|pass (a failure outranks a skip, as in testing), and the child
// exits when the function and its cleanups are done. Use it to test what
// happens when a LiveApp is used wrongly or its environment is broken, which
// cannot be done in the test binary itself.
func RegisterLiveScenario(name string, f func(t testing.TB)) {
	if scenarios[name] != nil || liveScenarios[name] != nil {
		panic("widgettest: scenario " + name + " registered twice")
	}
	liveScenarios[name] = f
}

// runLiveScenario runs f on a goroutine with a recording TB and serves the
// main thread meanwhile.
func runLiveScenario(f func(testing.TB)) {
	mainRunning.Store(true)
	time.AfterFunc(120*time.Second, func() { fmt.Println("FAIL watchdog: the scenario ran 120 s"); os.Exit(3) })
	s := &scenarioT{}
	go func() {
		done := make(chan struct{})
		go func() { defer close(done); f(s) }()
		<-done
		s.finish()
		os.Exit(0)
	}()
	serveMain()
}

// scenarioT is the testing.TB of a live scenario: it records.
type scenarioT struct {
	testing.TB // nil: the methods a LiveApp does not use panic
	mu         sync.Mutex
	cleanups   []func()
	fatals     int
	failed     bool
	skipped    bool
}

func (s *scenarioT) note(kind, format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := strings.ReplaceAll(fmt.Sprintf(format, args...), "\n", "\n  ")
	fmt.Printf("T %s: %s\n", kind, msg)
}

func (s *scenarioT) Helper()                 {}
func (s *scenarioT) Name() string            { return "scenario" }
func (s *scenarioT) Cleanup(f func())        { s.mu.Lock(); s.cleanups = append(s.cleanups, f); s.mu.Unlock() }
func (s *scenarioT) Log(args ...any)         { s.note("log", "%s", fmt.Sprint(args...)) }
func (s *scenarioT) Logf(f string, a ...any) { s.note("log", f, a...) }
func (s *scenarioT) Error(args ...any)       { s.Errorf("%s", fmt.Sprint(args...)) }
func (s *scenarioT) Errorf(f string, a ...any) {
	s.failed = true
	s.note("error", f, a...)
}
func (s *scenarioT) Fatal(args ...any) { s.Fatalf("%s", fmt.Sprint(args...)) }
func (s *scenarioT) Fatalf(f string, a ...any) {
	s.mu.Lock()
	s.failed = true
	s.fatals++
	s.mu.Unlock()
	s.note("fatal", f, a...)
	runtime.Goexit()
}
func (s *scenarioT) Skip(args ...any) { s.Skipf("%s", fmt.Sprint(args...)) }
func (s *scenarioT) Skipf(f string, a ...any) {
	s.mu.Lock()
	s.skipped = true
	s.mu.Unlock()
	s.note("skip", f, a...)
	runtime.Goexit()
}
func (s *scenarioT) Failed() bool { return s.failed }

// finish runs the cleanups last to first and reports the outcome.
func (s *scenarioT) finish() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		done := make(chan struct{})
		go func() { defer close(done); s.cleanups[i]() }() // a Fatal in a cleanup ends only its goroutine
		<-done
	}
	outcome := "pass" // as testing reports it: a failure outranks a skip
	switch {
	case s.fatals > 0:
		outcome = "fatal"
	case s.failed:
		outcome = "errors"
	case s.skipped:
		outcome = "skip"
	}
	Say("outcome=%s", outcome)
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
