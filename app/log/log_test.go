package log

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
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
	if fs := activeFiles(t, dir); len(fs) != 0 {
		t.Errorf("a clean exit left %v behind", fs)
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
	case "hold": // stay alive, with an empty crash file, until stdin closes; then crash
		fmt.Println("READY")
		io.Copy(io.Discard, os.Stdin)
		panic("boom after being held")
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
			if fs := activeFiles(t, dir); len(fs) != 1 || fileSize(t, fs[0]) == 0 {
				t.Fatalf("crash files after the crash: %v", fs)
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
			if fs := activeFiles(t, dir); len(fs) != 1 || fileSize(t, fs[0]) != 0 {
				t.Errorf("want this run's own empty crash file, have %v", fs)
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
	os.WriteFile(deadCrashFile(t, dir, "aa"), []byte("panic: newest\n\ngoroutine 1"), 0o600)
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
	os.WriteFile(deadCrashFile(t, dir, "bb"), bytes.Repeat([]byte("g"), maxCrashText+500), 0o600)
	Setup(Options{Dir: dir, NoStderr: true})
	defer Close()
	c, ok := PreviousCrash()
	if !ok || !c.Truncated || len(c.Text) != maxCrashText {
		t.Errorf("crash: ok %v truncated %v len %d", ok, c.Truncated, len(c.Text))
	}
}

// If the current file cannot be moved aside (its backup name is taken by a
// directory with something in it), the rotator must still make progress: the
// old code reopened the same full file and looped forever.
func TestRotationMakesProgressWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "app.1.log", "keep"), 0o700) // rename and remove both fail
	r := &rotator{dir: dir, name: "app", max: 300, files: 2}
	done := make(chan error, 1)
	go func() {
		var err error
		for i := 0; i < 50 && err == nil; i++ {
			_, err = r.Write([]byte(fmt.Sprintf("line %03d %s\n", i, strings.Repeat("z", 40))))
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Write never returned: rotation looped on a file it could not move")
	}
	r.Close()
	fi, _ := os.Stat(filepath.Join(dir, "app.log"))
	if fi == nil || fi.Size() == 0 || fi.Size() > 300 {
		t.Errorf("current file %v, want 1..300 bytes", fi)
	}
	if b := read(t, filepath.Join(dir, "app.log")); !strings.Contains(b, "line 049") {
		t.Errorf("the newest line is not in the file:\n%s", b)
	}
}

// Through the whole pipeline: Flush and Close still return.
func TestFlushReturnsWhenRotationCannotMoveTheFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "app.1.log", "keep"), 0o700)
	var errOut syncBuf
	l, _ := Setup(Options{Dir: dir, Stderr: &errOut, MaxSize: 400, MaxFiles: 2, NoCrashCapture: true})
	for i := 0; i < 40; i++ {
		l.Info("x", "pad", strings.Repeat("q", 60))
	}
	done := make(chan struct{})
	go func() { Flush(); Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Flush/Close hung")
	}
	if strings.Count(errOut.String(), "msg=x") != 40 {
		t.Errorf("stderr got %d of 40 lines", strings.Count(errOut.String(), "msg=x"))
	}
}

// deadPID returns the pid of a process that has exited.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// deadCrashFile is the path of a crash file of an instance that is gone.
func deadCrashFile(t *testing.T, dir, nonce string) string {
	t.Helper()
	return filepath.Join(dir, fmt.Sprintf("crash.%d-%s.log", deadPID(t), nonce))
}

func activeFiles(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "crash.*.log"))
	return m
}

func fileSize(t *testing.T, p string) int64 {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

// Two instances share a log directory. The second one's Setup and Close must
// leave the first one's crash file alone, and the first one's later crash must
// still be captured (with one shared crash.log, the second's start truncated or
// its exit unlinked the file the first was about to write).
func TestConcurrentInstancesKeepTheirOwnCrashOutput(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), crashEnv+"="+dir, "GNUI_LOG_CRASH_KIND=hold")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			stdin.Close()
			cmd.Wait()
		}
	}()
	rd := bufio.NewReader(stdout)
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatalf("helper died before it was ready: %v", err)
		}
		if strings.TrimSpace(line) == "READY" {
			break
		}
	}
	childFile := activeFiles(t, dir)
	if len(childFile) != 1 {
		t.Fatalf("the holder's crash file: %v", childFile)
	}

	// The second instance starts and exits cleanly in the same directory.
	if _, err := Setup(Options{Dir: dir, NoStderr: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := PreviousCrash(); ok {
		t.Error("the second instance reported a crash of a running instance")
	}
	if fs := activeFiles(t, dir); len(fs) != 2 {
		t.Errorf("crash files while both run: %v", fs)
	}
	Close()
	if fs := activeFiles(t, dir); len(fs) != 1 || fs[0] != childFile[0] {
		t.Fatalf("after the second exited, crash files %v, want only the holder's %v", fs, childFile)
	}

	stdin.Close() // the holder now panics
	io.Copy(io.Discard, rd)
	waited = true
	if err := cmd.Wait(); err == nil {
		t.Fatal("the holder exited cleanly")
	}
	if _, err := Setup(Options{Dir: dir, NoStderr: true}); err != nil {
		t.Fatal(err)
	}
	defer Close()
	c, ok := PreviousCrash()
	if !ok || !strings.Contains(c.Text, "boom after being held") {
		t.Fatalf("the holder's crash was lost: %v %q", ok, c.Text)
	}
}

// A failed move must not cost the crash: the file stays, is reported, and the
// next Setup archives it.
func TestFailedArchiveKeepsTheCrash(t *testing.T) {
	dir := t.TempDir()
	src := deadCrashFile(t, dir, "cc")
	os.WriteFile(src, []byte("panic: precious\n\ngoroutine 1 [running]:"), 0o600)
	renameFile = func(string, string) error { return fmt.Errorf("disk says no") }
	_, err := Setup(Options{Dir: dir, NoStderr: true})
	renameFile = os.Rename
	if err == nil || !strings.Contains(err.Error(), "disk says no") {
		t.Fatalf("Setup error %v, want the archive failure", err)
	}
	c, ok := PreviousCrash()
	if !ok || !strings.Contains(c.Text, "precious") || c.Path != src {
		t.Errorf("crash %+v ok %v, want the original reported in place", c, ok)
	}
	Close()
	if b, e := os.ReadFile(src); e != nil || !strings.Contains(string(b), "precious") {
		t.Fatalf("the crash file was destroyed: %q %v", b, e)
	}
	if _, err := Setup(Options{Dir: dir, NoStderr: true}); err != nil {
		t.Fatal(err)
	}
	defer Close()
	if _, e := os.Stat(src); e == nil {
		t.Error("the retry did not archive it")
	}
	m, _ := filepath.Glob(filepath.Join(dir, "crash-*.log"))
	if len(m) != 1 || !strings.Contains(read(t, m[0]), "precious") {
		t.Errorf("archive %v", m)
	}
}

// Archive names never collide: a file already sitting under the name a crash
// would get is left alone.
func TestArchiveNamesDoNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := deadCrashFile(t, dir, "dd")
	os.WriteFile(src, []byte("panic: second"), 0o600)
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	os.Chtimes(src, when, when)
	taken := filepath.Join(dir, "crash-20260102-030405-"+strings.TrimSuffix(strings.TrimPrefix(filepath.Base(src), "crash."), ".log")+".log")
	os.WriteFile(taken, []byte("panic: first"), 0o600)
	// A second crash of the same second from another instance.
	other := deadCrashFile(t, dir, "ee")
	os.WriteFile(other, []byte("panic: third"), 0o600)
	os.Chtimes(other, when, when)
	if _, err := Setup(Options{Dir: dir, NoStderr: true}); err != nil {
		t.Fatal(err)
	}
	defer Close()
	if b := read(t, taken); b != "panic: first" {
		t.Errorf("an existing archive was overwritten: %q", b)
	}
	m, _ := filepath.Glob(filepath.Join(dir, "crash-*.log"))
	var all string
	for _, p := range m {
		all += read(t, p) + "|"
	}
	if len(m) != 3 || !strings.Contains(all, "second") || !strings.Contains(all, "third") || !strings.Contains(all, "first") {
		t.Errorf("archives %v: %s", m, all)
	}
}
