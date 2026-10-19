package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// envFlags collects repeated -env KEY=VAL flags.
type envFlags []string

func (e *envFlags) String() string { return strings.Join(*e, ",") }
func (e *envFlags) Set(v string) error {
	if !strings.Contains(v, "=") || strings.HasPrefix(v, "=") {
		return fmt.Errorf("%q is not KEY=VALUE", v)
	}
	*e = append(*e, v)
	return nil
}

func cmdRun(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("run", stderr)
	cfgPath := fs.String("c", "gnui.json", "path of the gnui.json")
	adhoc := fs.Bool("adhoc", false, "sign ad-hoc instead of with gnui-dev (macOS asks for privacy grants again after every rebuild)")
	detach := fs.Bool("detach", false, "launch and return; output goes to the log files")
	tags := fs.String("tags", "", "go build -tags")
	ldflags := fs.String("ldflags", "", "extra go build -ldflags")
	devOut := fs.String("o", "", "directory for the development bundle (default .gnui/dev next to gnui.json)")
	var env envFlags
	fs.Var(&env, "env", "KEY=VALUE for the launched app (repeatable)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return errors.New("run builds and launches a macOS bundle")
	}
	c, err := LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	var spec signSpec
	if *adhoc {
		spec = signSpec{Identity: "-", Label: "an ad-hoc signature"}
	} else if spec, err = devSignSpec(); err != nil {
		return err
	}

	devDir := *devOut
	if devDir == "" {
		devDir = filepath.Join(c.dir, ".gnui", "dev")
	}
	app := filepath.Join(devDir, c.Name+".app")
	fmt.Fprintf(stdout, "building %s (%s) for darwin/%s\n", c.Name, c.BundleID(true), runtime.GOARCH)
	ent, cleanup, err := buildBundle(c, buildOptions{Dev: true, Archs: []string{runtime.GOARCH}, Tags: *tags, Ldflags: *ldflags, Out: app, Log: nil})
	if err != nil {
		return err
	}
	defer cleanup()
	// Development bundles: no hardened runtime (a nested dylib loads without a
	// Team ID) and no timestamp (no network needed).
	if _, err := signBundle(app, signOptions{Spec: spec, Entitlements: ent}); err != nil {
		return err
	}
	if err := verifyBundle(app); err != nil {
		return err
	}
	dr, _ := designatedRequirement(app)
	fmt.Fprintf(stdout, "signed with %s\ndesignated requirement: %s\n", spec.Label, dr)

	absApp, err := filepath.Abs(app)
	if err != nil {
		return err
	}
	outLog := filepath.Join(devDir, c.Name+".out.log")
	errLog := filepath.Join(devDir, c.Name+".err.log")
	for _, p := range []string{outLog, errLog} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			return err
		}
	}
	openArgs := []string{"-n", "--stdout", outLog, "--stderr", errLog}
	if !*detach {
		openArgs = append(openArgs, "-W")
	}
	for _, e := range env {
		openArgs = append(openArgs, "--env", e)
	}
	openArgs = append(openArgs, absApp)
	if fs.NArg() > 0 {
		openArgs = append(openArgs, "--args")
		openArgs = append(openArgs, fs.Args()...)
	}
	if *detach {
		if _, err := runOut("open", openArgs...); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "launched %s\nlogs: %s\n      %s\n", absApp, outLog, errLog)
		return nil
	}

	fmt.Fprintf(stdout, "launching %s (Ctrl-C quits it)\n", absApp)
	cmd := exec.Command("open", openArgs...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(2)
	go func() { defer wg.Done(); tailFile(outLog, stdout, stop) }()
	go func() { defer wg.Done(); tailFile(errLog, stderr, stop) }()
	if err := cmd.Start(); err != nil {
		close(stop)
		wg.Wait()
		return err
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-sig:
		exe := filepath.Join(absApp, "Contents", "MacOS", c.Executable)
		exec.Command("pkill", "-TERM", "-f", "^"+regexp.QuoteMeta(exe)).Run()
		select {
		case err = <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			err = <-done
		}
	}
	signal.Stop(sig)
	close(stop)
	wg.Wait()
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	fmt.Fprintln(stdout, "app exited")
	return nil
}

// tailFile copies what is appended to path to w until stop closes, then
// drains what is left.
func tailFile(path string, w io.Writer, stop <-chan struct{}) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	buf := make([]byte, 32<<10)
	drain := func() {
		for {
			n, _ := f.Read(buf)
			if n == 0 {
				return
			}
			w.Write(buf[:n])
		}
	}
	for {
		drain()
		select {
		case <-stop:
			drain()
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
