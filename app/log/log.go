// Package log sets up the application's logging: a [log/slog] logger that writes
// to standard error and to a size-rotated file in the log directory, and a
// record of crashes.
//
//	logger, err := log.Setup(log.Options{ID: "dev.example.app"})
//	if err != nil { /* the logger still works, on standard error */ }
//	slog.SetDefault(logger) // or Options.SetDefault
//	defer log.Close()       // flushes; call at quit
//	if c, ok := log.PreviousCrash(); ok { /* offer to report c.Text */ }
//
// # Where it may be called
//
// Logging is for events, not for frames: formatting a record allocates, so
// nothing on the frame path (Build, layout, Encode) logs. What it does not do is
// block: a record is copied into a bounded buffer and a goroutine writes it, so
// a slow disk or a stalled terminal never stalls the caller. If the buffer
// fills (1 MiB by default) records are dropped and a warning with the count is
// written when the writer catches up. Records still in the buffer when the
// process dies are lost; Flush before a deliberate exit, and Close at quit.
//
// # Crashes
//
// Setup points the Go runtime's crash output (runtime/debug.SetCrashOutput) at
// this instance's own file in the log directory, crash.<pid>-<nonce>.log.
// Instances that share the directory each have one and never touch another
// running instance's. An unrecovered panic or a fatal runtime error writes its
// goroutine dump there (as well as to standard error), after the process can no
// longer do anything else. For a fatal runtime error (concurrent map writes,
// out of memory) the Go runtime puts the "fatal error:" line on standard error
// only; the file has the stacks.
//
// The next Setup looks at the files of instances that are no longer running (by
// pid; a pid that has been reused by another process keeps its file until that
// process exits). An empty one is removed. A non-empty one is read and moved to
// crash-<time>-<pid>-<nonce>.log (the newest 5 are kept) and the newest is
// reported through PreviousCrash. If the move fails, the file stays where it is,
// is still reported, and Setup returns the error; nothing is overwritten. A
// clean run leaves its file empty and Close removes it. Crashes that never reach
// the Go runtime (a signal in native code, being killed) leave nothing.
package log

import (
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gonativeui/app/paths"
)

// Options configures Setup. The zero value is not usable: set ID or Dir.
type Options struct {
	// ID is the application ID; the log directory is paths.Logs(ID) unless Dir
	// is set.
	ID string
	// Dir overrides the log directory (created if missing).
	Dir string
	// Name is the base name of the log file: <Name>.log, with backups
	// <Name>.1.log, <Name>.2.log, and so on. Default "app".
	Name string
	// Level is the minimum level that is logged. nil means slog.LevelInfo. Use a
	// *slog.LevelVar to change it while the program runs.
	Level slog.Leveler
	// MaxSize is the size in bytes at which the file is rotated. Default 5 MiB.
	// A line is never split between files.
	MaxSize int64
	// MaxFiles is how many files are kept, the current one included. Default 5.
	MaxFiles int
	// JSON selects slog's JSON format instead of its key=value text.
	JSON bool
	// AddSource adds the file and line of the call to every record.
	AddSource bool
	// Stderr is where the records are also written; nil means os.Stderr.
	Stderr io.Writer
	// NoStderr turns the standard error output off (a GUI app started from the
	// Dock has none worth writing to).
	NoStderr bool
	// Buffer is the most bytes of records waiting to be written; further
	// records are dropped and counted. Default 1 MiB.
	Buffer int
	// SetDefault also installs the logger as slog's default, so the package
	// level functions (slog.Info) and the standard log package use it.
	SetDefault bool
	// NoCrashCapture leaves the runtime's crash output alone.
	NoCrashCapture bool
}

// Crash describes the crash of the previous run.
type Crash struct {
	// Time is when the crash file was last written (the crash).
	Time time.Time
	// Path is the file the crash was moved to; it stays until it is among the
	// five oldest.
	Path string
	// Text is the beginning of the crash output: the panic message and the
	// goroutine stacks. At most 64 KiB.
	Text string
	// Truncated is true when Text is shorter than the file.
	Truncated bool
}

const (
	maxCrashText = 64 << 10
	keepCrashes  = 5
)

var state struct {
	sync.Mutex
	w     *asyncWriter
	crash *Crash
	owns  string // path of the crash file whose descriptor this Setup gave the runtime ("" if none)
}

// Setup starts logging and returns the logger. It does file I/O (the directory, the log file, the crash file):
// call it once at startup, before the first frame. If the directory or the crash
// file cannot be made, Setup returns the error together with a logger that still
// works (standard error only, or the file part that could be set up). A second
// call replaces the first: it flushes and closes it.
func Setup(opt Options) (*slog.Logger, error) {
	Close()

	var errs []string
	dir := opt.Dir
	if dir == "" {
		if opt.ID == "" {
			return nil, fmt.Errorf("log: Options needs an ID or a Dir")
		}
		d, err := paths.Logs(opt.ID)
		if err != nil {
			errs = append(errs, err.Error())
		}
		dir = d
	}
	name := opt.Name
	if name == "" {
		name = "app"
	}
	if strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("log: Name %q must not contain a path separator", name)
	}
	if opt.MaxSize <= 0 {
		opt.MaxSize = 5 << 20
	}
	if opt.MaxFiles <= 0 {
		opt.MaxFiles = 5
	}
	if opt.Buffer <= 0 {
		opt.Buffer = 1 << 20
	}
	level := opt.Level
	if level == nil {
		level = slog.LevelInfo
	}

	var sinks []io.Writer
	var closer io.Closer
	var crash *Crash
	owns := ""
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			errs = append(errs, err.Error())
		} else {
			rot := &rotator{dir: dir, name: name, max: opt.MaxSize, files: opt.MaxFiles}
			if err := rot.open(); err != nil {
				errs = append(errs, err.Error())
			} else {
				sinks, closer = append(sinks, rot), rot
			}
			if !opt.NoCrashCapture {
				var cerrs []string
				crash, owns, cerrs = captureCrashes(dir)
				errs = append(errs, cerrs...)
			}
		}
	}
	if !opt.NoStderr {
		w := opt.Stderr
		if w == nil {
			w = os.Stderr
		}
		sinks = append(sinks, w)
	}
	aw := newAsyncWriter(opt.Buffer, opt.JSON, closer, sinks...)
	ho := &slog.HandlerOptions{Level: level, AddSource: opt.AddSource}
	var h slog.Handler = slog.NewTextHandler(aw, ho)
	if opt.JSON {
		h = slog.NewJSONHandler(aw, ho)
	}
	logger := slog.New(h)

	state.Lock()
	state.w, state.crash, state.owns = aw, crash, owns
	state.Unlock()
	if opt.SetDefault {
		slog.SetDefault(logger)
	}
	if crash != nil {
		logger.Warn("the previous run crashed", "file", crash.Path, "time", crash.Time.Format(time.RFC3339))
	}
	if len(errs) > 0 {
		return logger, fmt.Errorf("log: %s", strings.Join(errs, "; "))
	}
	return logger, nil
}

// renameFile moves a crash file into the archive; a variable so a test can make
// it fail.
var renameFile = os.Rename

// activeCrash names the crash file of one running instance: crash.<pid>-<nonce>.log.
// Each instance has its own, so instances sharing a log directory neither
// truncate nor remove each other's. The nonce keeps a reused pid apart.
func activeCrash(dir string) string {
	var n [6]byte
	rand.Read(n[:])
	return filepath.Join(dir, fmt.Sprintf("crash.%d-%x.log", os.Getpid(), n))
}

// activePID extracts the pid from a crash.<pid>-<nonce>.log name.
func activePID(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, "crash.")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, ".log")
	if !ok {
		return 0, false
	}
	pidStr, _, ok := strings.Cut(rest, "-")
	pid, err := strconv.Atoi(pidStr)
	return pid, ok && err == nil && pid > 0
}

// captureCrashes archives the crash files that earlier instances left, then
// creates this instance's own and points the runtime's crash output at it.
//
// A file belongs to a running instance if its pid is alive (and is not ours: a
// file with our pid that is not the one we are about to create is from an
// earlier run that had the same pid). Only files of dead instances are touched:
// an empty one is removed, a non-empty one is read and moved to
// crash-<time>-<pid>-<nonce>.log, a name no other file has. If the move fails
// the original stays where it is, still reported, and the next Setup tries
// again; nothing is truncated or overwritten.
func captureCrashes(dir string) (prev *Crash, owns string, errs []string) {
	files, _ := filepath.Glob(filepath.Join(dir, "crash.*.log"))
	sort.Strings(files)
	for _, path := range files {
		pid, ok := activePID(filepath.Base(path))
		if !ok || (pid != os.Getpid() && processAlive(pid)) {
			continue
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		if fi.Size() == 0 {
			os.Remove(path)
			continue
		}
		c := &Crash{Time: fi.ModTime(), Path: path}
		if f, err := os.Open(path); err == nil {
			b := make([]byte, maxCrashText+1)
			n, _ := io.ReadFull(f, b)
			f.Close()
			c.Text, c.Truncated = string(b[:min(n, maxCrashText)]), n > maxCrashText
		}
		if dst, err := archiveCrash(dir, path, fi.ModTime()); err != nil {
			errs = append(errs, fmt.Sprintf("could not archive %s: %v", path, err))
		} else {
			c.Path = dst
		}
		if prev == nil || c.Time.After(prev.Time) {
			prev = c
		}
	}
	pruneCrashes(dir)
	path := activeCrash(dir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return prev, "", append(errs, err.Error())
	}
	defer f.Close() // SetCrashOutput duplicates the descriptor
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		os.Remove(path)
		return prev, "", append(errs, err.Error())
	}
	return prev, path, errs
}

// archiveCrash moves src to a crash-<time>-<pid>-<nonce>.log that does not
// exist yet.
func archiveCrash(dir, src string, mod time.Time) (string, error) {
	id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(src), "crash."), ".log")
	stem := "crash-" + mod.UTC().Format("20060102-150405") + "-" + id
	dst := filepath.Join(dir, stem+".log")
	for n := 2; ; n++ {
		if _, err := os.Lstat(dst); err != nil {
			break
		}
		dst = filepath.Join(dir, fmt.Sprintf("%s-%d.log", stem, n))
	}
	if err := renameFile(src, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// pruneCrashes deletes all but the newest keepCrashes archived crash files.
func pruneCrashes(dir string) {
	m, _ := filepath.Glob(filepath.Join(dir, "crash-*.log"))
	if len(m) <= keepCrashes {
		return
	}
	sort.Strings(m) // the names sort by time
	for _, p := range m[:len(m)-keepCrashes] {
		os.Remove(p)
	}
}

// PreviousCrash returns the crash of the previous run, if the Setup that is in
// force found one. It does no I/O.
func PreviousCrash() (Crash, bool) {
	state.Lock()
	defer state.Unlock()
	if state.crash == nil {
		return Crash{}, false
	}
	return *state.crash, true
}

// Flush blocks until every record logged so far has been handed to the file and
// to standard error. It waits for I/O: call it before a deliberate exit or
// after an error you want on disk, not from the frame path.
func Flush() {
	state.Lock()
	w := state.w
	state.Unlock()
	if w != nil {
		w.flush()
	}
}

// Close flushes and stops logging: the file is closed, the runtime's crash output
// is released and the empty crash file removed (a clean exit leaves none). The
// logger from Setup keeps working but drops its records. Close may be called
// more than once.
func Close() {
	state.Lock()
	w, owns := state.w, state.owns
	state.w, state.owns, state.crash = nil, "", nil
	state.Unlock()
	if w != nil {
		w.close()
	}
	if owns != "" {
		debug.SetCrashOutput(nil, debug.CrashOptions{})
		if fi, err := os.Stat(owns); err == nil && fi.Size() == 0 {
			os.Remove(owns)
		}
	}
}
