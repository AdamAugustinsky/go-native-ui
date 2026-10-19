package log

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is a stderr stand-in that is safe to read while the writer goroutine
// writes.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLogsToFileAndStderr(t *testing.T) {
	dir := t.TempDir()
	var errOut syncBuf
	l, err := Setup(Options{Dir: dir, Stderr: &errOut, Level: slog.LevelInfo})
	if err != nil {
		t.Fatal(err)
	}
	defer Close()
	l.Debug("hidden")
	l.Info("hello", "n", 1)
	l.Error("bad", "err", "boom")
	Flush()
	f := read(t, filepath.Join(dir, "app.log"))
	for _, want := range []string{"msg=hello", "n=1", "level=ERROR", "msg=bad", "err=boom"} {
		if !strings.Contains(f, want) {
			t.Errorf("file lacks %q:\n%s", want, f)
		}
	}
	if strings.Contains(f, "hidden") {
		t.Error("a debug record passed an info level")
	}
	if errOut.String() != f {
		t.Errorf("stderr and file differ:\n%q\n%q", errOut.String(), f)
	}
}

func TestJSONAndLevelVar(t *testing.T) {
	dir := t.TempDir()
	var lv slog.LevelVar
	l, _ := Setup(Options{Dir: dir, NoStderr: true, JSON: true, Level: &lv, Name: "j"})
	defer Close()
	l.Debug("no")
	lv.Set(slog.LevelDebug)
	l.Debug("yes")
	Flush()
	f := read(t, filepath.Join(dir, "j.log"))
	if strings.Contains(f, `"no"`) || !strings.Contains(f, `"msg":"yes"`) {
		t.Errorf("file:\n%s", f)
	}
}

func TestSetupUsesPathsLogsForID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "R"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "L"))
	t.Setenv("XDG_STATE_HOME", "")
	l, err := Setup(Options{ID: "dev.test.logid", NoStderr: true})
	if err != nil {
		t.Fatal(err)
	}
	defer Close()
	l.Info("x")
	Flush()
	var found string
	filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == "app.log" {
			found = p
		}
		return nil
	})
	if found == "" || !strings.Contains(found, "dev.test.logid") {
		t.Errorf("app.log at %q", found)
	}
	if _, err := Setup(Options{}); err == nil {
		t.Error("Setup without ID or Dir succeeded")
	}
	if _, err := Setup(Options{Dir: t.TempDir(), Name: "a/b"}); err == nil {
		t.Error("Setup accepted a name with a separator")
	}
}

func TestRotationKeepsLinesWholeAndBoundsFiles(t *testing.T) {
	dir := t.TempDir()
	l, err := Setup(Options{Dir: dir, NoStderr: true, MaxSize: 2000, MaxFiles: 3, NoCrashCapture: true})
	if err != nil {
		t.Fatal(err)
	}
	defer Close()
	const n = 200
	for i := 0; i < n; i++ {
		l.Info(fmt.Sprintf("line-%04d", i), "pad", strings.Repeat("x", 40))
		if i%10 == 0 {
			Flush() // bursts of different sizes reach the rotator
		}
	}
	Flush()
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if want := []string{"app.1.log", "app.2.log", "app.log"}; fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("files %v, want %v", names, want)
	}
	line := regexp.MustCompile(`^time=\S+ level=INFO msg=line-(\d{4}) pad=x{40}$`)
	prev := n
	for _, name := range []string{"app.log", "app.1.log", "app.2.log"} {
		p := filepath.Join(dir, name)
		fi, _ := os.Stat(p)
		if fi.Size() > 2000 {
			t.Errorf("%s is %d bytes, over the limit", name, fi.Size())
		}
		lines := strings.Split(strings.TrimSuffix(read(t, p), "\n"), "\n")
		for j := len(lines) - 1; j >= 0; j-- { // newest file first, each read backwards
			m := line.FindStringSubmatch(lines[j])
			if m == nil {
				t.Fatalf("%s holds a broken line: %q", name, lines[j])
			}
			var k int
			fmt.Sscanf(m[1], "%d", &k)
			if k != prev-1 {
				t.Fatalf("%s: line %d after %d, want %d (a gap or reordering)", name, k, prev, prev-1)
			}
			prev = k
		}
	}
	if prev == 0 {
		t.Error("nothing was rotated away: the test did not exercise rotation")
	}
}

func TestRotationAppendsToExistingFileAndOversizeLine(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app.log"), []byte(strings.Repeat("old line\n", 100)), 0o600) // 900 bytes
	l, _ := Setup(Options{Dir: dir, NoStderr: true, MaxSize: 1000, MaxFiles: 2, NoCrashCapture: true})
	defer Close()
	l.Info("a")
	l.Info("huge", "pad", strings.Repeat("z", 3000)) // longer than MaxSize: gets a file of its own
	Flush()
	cur := read(t, filepath.Join(dir, "app.log"))
	prev := read(t, filepath.Join(dir, "app.1.log"))
	if !strings.Contains(cur, "msg=huge") || strings.Contains(cur, "old line") {
		t.Errorf("current file:\n%.200s", cur)
	}
	if !strings.Contains(prev, "old line") || !strings.Contains(prev, "msg=a") {
		t.Errorf("previous file:\n%.200s", prev)
	}
}

// stuck is a sink that blocks until released.
type stuck struct{ release chan struct{} }

func (s *stuck) Write(p []byte) (int, error) { <-s.release; return len(p), nil }

func TestLoggingNeverWaitsForTheSink(t *testing.T) {
	dir := t.TempDir()
	sink := &stuck{release: make(chan struct{})}
	l, _ := Setup(Options{Dir: dir, Stderr: sink, Buffer: 4096, NoCrashCapture: true})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5000; i++ {
			l.Info("flood", "i", i, "pad", strings.Repeat("p", 100))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("logging blocked on a stalled stderr")
	}
	close(sink.release)
	Flush()
	Close()
	f := read(t, filepath.Join(dir, "app.log"))
	if !strings.Contains(f, "log lines dropped") {
		t.Errorf("no drop notice in:\n%.300s", f)
	}
	if !strings.Contains(f, "msg=flood") {
		t.Error("nothing was written")
	}
}

func TestCloseFlushesAndLaterRecordsAreDropped(t *testing.T) {
	dir := t.TempDir()
	l, _ := Setup(Options{Dir: dir, NoStderr: true})
	l.Info("before")
	Close()
	l.Info("after") // must not panic or block
	Close()
	f := read(t, filepath.Join(dir, "app.log"))
	if !strings.Contains(f, "msg=before") || strings.Contains(f, "msg=after") {
		t.Errorf("file:\n%s", f)
	}
	if _, err := os.Stat(filepath.Join(dir, "crash.log")); err == nil {
		t.Error("a clean exit left crash.log behind")
	}
}

func TestSetupErrorStillGivesAWorkingLogger(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, nil, 0o600)
	var errOut syncBuf
	l, err := Setup(Options{Dir: filepath.Join(blocker, "logs"), Stderr: &errOut})
	if err == nil || l == nil {
		t.Fatalf("logger %v, err %v", l, err)
	}
	defer Close()
	l.Info("still here")
	Flush()
	if !strings.Contains(errOut.String(), "still here") {
		t.Errorf("stderr: %q", errOut.String())
	}
}

// The crash tests run a child process that sets up logging and dies.

const crashEnv = "GNUI_LOG_CRASH_DIR"

func TestCrashHelper(t *testing.T) {
	dir := os.Getenv(crashEnv)
	if dir == "" {
		t.Skip("helper process for TestCrashIsCapturedAndReportedNextRun")
	}
	l, err := Setup(Options{Dir: dir, NoStderr: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	l.Info("about to crash")
	Flush()
	switch os.Getenv("GNUI_LOG_CRASH_KIND") {
	case "goroutine":
		go func() { panic("boom from a goroutine") }()
		select {}
	case "fatal":
		m := map[int]int{}
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 1e6; j++ {
					m[j] = j
				}
			}()
		}
		wg.Wait()
	default:
		panic("boom from the helper")
	}
}

func crashChild(t *testing.T, dir, kind string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), crashEnv+"="+dir, "GNUI_LOG_CRASH_KIND="+kind)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the helper exited cleanly:\n%s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() == 3 {
		t.Fatalf("helper: %v\n%s", err, out)
	}
}

func TestCrashIsCapturedAndReportedNextRun(t *testing.T) {
	for _, tc := range []struct{ kind, want string }{
		{"panic", "boom from the helper"},
		{"goroutine", "boom from a goroutine"},
		{"fatal", "maps.fatal"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			dir := t.TempDir()
			crashChild(t, dir, tc.kind)
			if fi, err := os.Stat(filepath.Join(dir, "crash.log")); err != nil || fi.Size() == 0 {
				t.Fatalf("crash.log after the crash: %v %v", fi, err)
			}
			l, err := Setup(Options{Dir: dir, NoStderr: true})
			if err != nil {
				t.Fatal(err)
			}
			defer Close()
			c, ok := PreviousCrash()
			if !ok {
				t.Fatal("PreviousCrash found nothing")
			}
			if !strings.Contains(c.Text, tc.want) || !strings.Contains(c.Text, "goroutine ") {
				t.Errorf("crash text lacks %q or a goroutine dump:\n%.500s", tc.want, c.Text)
			}
			if c.Time.IsZero() || time.Since(c.Time) > time.Minute {
				t.Errorf("crash time %v", c.Time)
			}
			if _, err := os.Stat(c.Path); err != nil || !strings.HasPrefix(filepath.Base(c.Path), "crash-") {
				t.Errorf("archived at %q: %v", c.Path, err)
			}
			if fi, _ := os.Stat(filepath.Join(dir, "crash.log")); fi == nil || fi.Size() != 0 {
				t.Errorf("fresh crash.log is not empty: %v", fi)
			}
			Flush()
			f := read(t, filepath.Join(dir, "app.log"))
			if !strings.Contains(f, "about to crash") || !strings.Contains(f, "the previous run crashed") {
				t.Errorf("log:\n%s", f)
			}
			// A run after a clean one reports nothing.
			Close()
			if _, err := Setup(Options{Dir: dir, NoStderr: true}); err != nil {
				t.Fatal(err)
			}
			if _, ok := PreviousCrash(); ok {
				t.Error("a clean run reported the older crash again")
			}
			_ = l
		})
	}
}

func TestOldCrashFilesArePruned(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 8; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("crash-2026010%d-000000.log", i)), []byte("old"), 0o600)
	}
	os.WriteFile(filepath.Join(dir, "crash.log"), []byte("panic: newest\n\ngoroutine 1"), 0o600)
	if _, err := Setup(Options{Dir: dir, NoStderr: true}); err != nil {
		t.Fatal(err)
	}
	defer Close()
	m, _ := filepath.Glob(filepath.Join(dir, "crash-*.log"))
	if len(m) != keepCrashes {
		t.Errorf("%d crash files kept: %v", len(m), m)
	}
	if c, ok := PreviousCrash(); !ok || !strings.Contains(c.Text, "newest") {
		t.Errorf("previous crash %v %v", c, ok)
	}
}

func TestHugeCrashTextIsTruncated(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "crash.log"), bytes.Repeat([]byte("g"), maxCrashText+500), 0o600)
	Setup(Options{Dir: dir, NoStderr: true})
	defer Close()
	c, ok := PreviousCrash()
	if !ok || !c.Truncated || len(c.Text) != maxCrashText {
		t.Errorf("crash: ok %v truncated %v len %d", ok, c.Truncated, len(c.Text))
	}
}
