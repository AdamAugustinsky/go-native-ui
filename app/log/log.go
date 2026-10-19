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
// crash.log in the log directory. An unrecovered panic or a fatal runtime error
// writes its goroutine dump there (as well as to standard error), after the
// process can no longer do anything else. For a fatal runtime error (concurrent
// map writes, out of memory) the Go runtime puts the "fatal error:" line on
// standard error only; the file has the stacks. The next Setup finds the non-empty
// file, moves it to crash-<time>.log (the newest 5 are kept) and reports it
// through PreviousCrash. A clean run leaves crash.log empty. Crashes that never
// reach the Go runtime (a signal in native code, being killed) leave nothing.
package log

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
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
	crashFile    = "crash.log"
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
				var err error
				if crash, owns, err = captureCrashes(dir); err != nil {
					errs = append(errs, err.Error())
				}
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

// captureCrashes archives a crash left by the previous run and points the
// runtime's crash output at a fresh crash.log.
func captureCrashes(dir string) (prev *Crash, owns string, err error) {
	path := filepath.Join(dir, crashFile)
	if fi, serr := os.Stat(path); serr == nil && fi.Size() > 0 {
		dst := filepath.Join(dir, "crash-"+fi.ModTime().UTC().Format("20060102-150405")+".log")
		os.Remove(dst)
		if rerr := os.Rename(path, dst); rerr == nil {
			c := &Crash{Time: fi.ModTime(), Path: dst}
			if f, oerr := os.Open(dst); oerr == nil {
				b := make([]byte, maxCrashText+1)
				n, _ := io.ReadFull(f, b)
				f.Close()
				c.Text, c.Truncated = string(b[:min(n, maxCrashText)]), n > maxCrashText
			}
			prev = c
		}
		pruneCrashes(dir)
	}
	f, ferr := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if ferr != nil {
		return prev, "", ferr
	}
	defer f.Close() // SetCrashOutput duplicates the descriptor
	if serr := debug.SetCrashOutput(f, debug.CrashOptions{}); serr != nil {
		return prev, "", serr
	}
	return prev, path, nil
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
