// Package session runs programs on pseudo-terminals whose lifetime is not
// tied to a view: a view attaches to a session, detaches from it, and
// attaches again later, and the session goes on running in between.
//
// There are two implementations of one API, Service:
//
//   - NewLocal, in-process: the sessions belong to this process and die
//     with it. It is the default, and needs no daemon, socket or files.
//   - Dial or DialOrLaunch, a client of a session daemon (Serve, RunDaemon,
//     cmd/gnui-sessiond) over a Unix socket in a per-user 0700 directory.
//     Sessions marked Persist outlive the client.
//
// Each session keeps a bounded ring of its recent output. Attach resets the
// view's terminal (ResetSequence) and replays the ring from a safe boundary
// before live output, so the terminal's state is consistent but anything
// older than the ring, and the modes it set, is lost. The live stream after
// the replay is lossless and flow-controlled: a sink that blocks stops the
// session reading its pseudo-terminal, after a bounded queue.
// docs/design/terminal-sessions.md has the protocol and the semantics.
//
// The implementation is for macOS and Linux; elsewhere every call returns
// ErrUnsupported.
package session

import (
	"errors"
	"io"
	"time"
)

// ResetSequence is written to an attachment's sink before the replay: CAN
// ends any escape sequence the terminal is in the middle of, and RIS (ESC c)
// resets it fully, so the replay lands on a clean terminal.
const ResetSequence = "\x18\x1bc"

var (
	// ErrUnsupported is returned on systems without the service.
	ErrUnsupported = errors.New("session: not supported on this system")
	// ErrNotFound is returned for an unknown session ID.
	ErrNotFound = errors.New("session: no such session")
	// ErrClosed is returned by a closed service or attachment.
	ErrClosed = errors.New("session: closed")
)

// Size is a terminal's size in cells and, optionally, pixels.
type Size struct {
	Cols, Rows    int
	Width, Height int
}

// Options describes a new session.
type Options struct {
	Argv    []string // the program and its arguments
	Env     []string // added to the service's environment (later entries win)
	Dir     string   // the working directory; empty is the service's
	Size    Size
	Persist bool // outlive the view: closing or losing it detaches instead of hanging up
}

// Info describes a session.
type Info struct {
	ID       string
	Argv     []string
	Pid      int
	Persist  bool
	Created  time.Time
	Size     Size
	Attached bool
	Exited   bool // the program has exited and its output has been read
	ExitCode int  // with Exited; -1 if a signal ended it
}

// Service creates sessions and attaches views to them.
type Service interface {
	// Create starts a session. It runs, and fills its ring, until it is
	// attached, killed, or the program exits.
	Create(Options) (Info, error)
	List() ([]Info, error)
	// Attach makes sink the session's view: ResetSequence and the replay
	// go to it first, then the live output. A session has one view; a
	// second Attach takes it over from the first, whose attachment ends.
	// Writes to sink come from one goroutine at a time; a Write that blocks
	// holds the session's output back (bounded) and loses none of it.
	Attach(id string, sink io.Writer, size Size) (Attachment, error)
	// Kill hangs the session up (SIGHUP to its process group, SIGKILL after
	// a grace period) and forgets it.
	Kill(id string) error
	// Shutdown kills every session. For a daemon client it stops the daemon,
	// which also removes its socket and files.
	Shutdown() error
	// Close releases the service. A local service shuts down; a client
	// disconnects, which detaches persistent sessions and hangs up the rest.
	Close() error
}

// Attachment is a view's hold on a session.
type Attachment interface {
	ID() string
	// Write sends p to the program as typed input.
	io.Writer
	Resize(Size) error
	// Detach ends the view; the session goes on, persistent or not.
	Detach() error
	// Close ends the view the way closing a window does: a persistent
	// session detaches and a non-persistent one is killed. A session that
	// has exited is forgotten.
	Close() error
	// Done is closed when no more output will reach the sink: the program
	// exited (after its last output), or the attachment ended. A Write in
	// flight when Detach or Close was called may still complete after.
	Done() <-chan struct{}
	// Exit reports the program's exit code once Done is closed by its exit.
	Exit() (code int, exited bool)
}

// Config tunes a service; zero fields take the defaults.
type Config struct {
	RingSize  int           // bytes of output kept for replay (default 1 MiB)
	LiveLimit int           // bytes queued for a view before the pseudo-terminal waits (default 256 KiB)
	KillGrace time.Duration // SIGHUP to SIGKILL (default 2 s)
}

func (c Config) withDefaults() Config {
	if c.RingSize <= 0 {
		c.RingSize = 1 << 20
	}
	if c.LiveLimit <= 0 {
		c.LiveLimit = 256 << 10
	}
	if c.KillGrace <= 0 {
		c.KillGrace = 2 * time.Second
	}
	return c
}
