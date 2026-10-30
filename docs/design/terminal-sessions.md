# Terminal sessions: term/session (2026-10-29)

`term/session` runs programs on pseudo-terminals whose lifetime does not
depend on any view. A view attaches to a session, detaches, and attaches
again later. An app that wants sessions to outlive its windows, or the app
itself, runs them in a small daemon over a Unix socket. Every other app gets
the same API in-process, with no daemon, socket or files. Rex's mux daemon
gave the idea; nothing is taken from its code. It is optional and sits above
the UI framework: the package imports neither `term` nor `rexui`, and an
attachment writes its output to whatever `io.Writer` the view gives it.

macOS and Linux only, like the pseudo-terminal itself (syscalls and ioctl,
`CGO_ENABLED=0`). On Windows the package compiles, and every call returns
`ErrUnsupported`.

## API

```go
svc := session.NewLocal(session.Config{})              // in-process: the default
svc, err := session.DialOrLaunch(dir, argv, 5*time.Second) // a daemon, started if missing

in, _ := svc.Create(session.Options{Argv: argv, Size: sz, Persist: true})
att, _ := svc.Attach(in.ID, sink, sz) // sink: the view's io.Writer
att.Write(keys); att.Resize(sz)
att.Detach() // the session goes on
att.Close()  // as closing a window: detach if persistent, else hang up
svc.Kill(in.ID); svc.List(); svc.Shutdown(); svc.Close()
```

A session ID is 12 random hex digits. It stays the same for the session's
life in its service, across any number of attaches and reconnections; it
does not survive the daemon. A session has at most one view. A second
`Attach` takes the session over (like `tmux attach -d`), and the first
attachment's `Done` closes. That is also how a client that crashed is
replaced, before the daemon has even noticed its sockets closing.

## Lifecycle

| Event | Persistent session | Non-persistent session |
|---|---|---|
| `Detach` | runs on, output into the ring | runs on, output into the ring |
| `Close`, or the client's connection breaks | detaches | SIGHUP, then SIGKILL after the grace period; forgotten |
| the program exits | listed as exited with its code until closed or killed | forgotten once no view holds it |
| `Kill` | SIGHUP, SIGKILL after the grace period; forgotten | the same |
| `Shutdown`, or the daemon's SIGTERM/SIGINT | every session killed, all at once | the same |

A non-persistent session behaves as termdemo's PTY did before: closing its
view hangs it up. A local service's `Close` is `Shutdown`, and the sessions
are this process's children. If the process dies without closing them, the
kernel closes their terminals and hangs up their foreground process groups.

The signals go to the program's process group, which `Setsid` makes the
program's own. The grace period (`Config.KillGrace`, 2 s by default) ends
early once the program has been reaped and its terminal has reached EOF.
SIGKILL goes to the group if either is still pending, so a program that
ignores SIGHUP, or a child of it still holding the terminal, does not
outlive the kill.

Shutdown takes the daemon's lock (flock on `lock`), so a second daemon in
the same directory exits at once. Shutdown then kills the sessions, closes
the listener and every connection, and removes `sock`, `pid`, `log`, `lock`
and the directory. The client's `Shutdown` returns only once the files are
gone. `ServeOptions.IdleExit` shuts down a daemon that has had no sessions
and no clients for that long; termdemo's daemon uses a minute.

## Transport

The socket is `<dir>/sock` (mode 0600) in a directory of its own, mode 0700
and owned by the user, which `ensureDir` checks. `DefaultDir(app)` is
`$XDG_RUNTIME_DIR/gnui-session-<app>-<uid>`, or the same name under the
temporary directory, which macOS makes per-user. The directory's
permissions are the only access control: there is no peer-credential check.

Every frame is a 4-byte big-endian length n, then n bytes: a type byte and
the payload (1 MiB at most; data is sent 64 KiB at a time). Control payloads
are JSON. Input and output are raw bytes.

| Type | Direction | Payload |
|---|---|---|
| 1 hello | both | `{"Version":1,"Pid":…}`; the daemon answers a different version with an error reply and hangs up |
| 2 request | c→d | `{"Op":"create"\|"list"\|"attach"\|"kill"\|"shutdown","ID":…,"Options":…,"Size":…}` |
| 3 reply | d→c | `{"Error":…,"Info":…,"List":…}` |
| 4 input | c→d | bytes for the program |
| 5 resize | c→d | cols, rows, width, height as 4 × uint32 |
| 6 detach / 7 close | c→d | empty |
| 8 reset | d→c | empty: the client writes `ResetSequence` to the sink, and the replay follows |
| 9 output | d→c | the program's output |
| 10 live | d→c | empty: the replay is over |
| 11 exit | d→c | int32 exit code (−1 for a signal); nothing follows |

A client keeps one control connection for requests. Each attachment opens a
connection of its own, which an `attach` request turns into that session's
data connection for the rest of its life. So a view that is slow to take
its output holds back only its own session, and control requests never wait
behind output.

## Restoration on attach

Replay is based on a ring of raw bytes. It is not a snapshot of the
terminal's state. No server-side libghostty-vt instance re-serialises the
screen.

- Each session keeps the last `Config.RingSize` bytes of its output (1 MiB by default), whether or not a view is attached.
- On attach, in one step under the session's lock, the daemon copies the ring and makes the new view the session's subscriber. The replay and the live stream therefore meet exactly: no byte is missing between them and none is sent twice.
- The view's sink gets `ResetSequence` first: CAN (0x18) aborts any escape sequence the terminal is half-way through, and RIS (ESC c) resets it fully, including its screen, modes and scrollback. The replay follows, then the live output.
- The replay starts at the stream's first byte if the ring has not wrapped. Once it has wrapped, the replay starts at a safe offset: one where a terminal that read the whole stream would be in its ground state, outside any escape sequence or UTF-8 character. The ring finds these with a small DEC-style parser covering ESC, CSI, OSC/DCS/SOS/PM/APC strings, CAN/SUB and UTF-8 lengths. It prefers offsets just after a newline and records one roughly every RingSize/64 bytes, 256 at most. If no safe offset is left (the whole ring is one escape string), the replay is empty, and only the reset is sent.
- On attach, the session's pseudo-terminal is resized to the view's size. If the size changed, a full-screen program gets SIGWINCH and redraws.

Lost beyond the ring: everything older than the safe start, and with it any
state that older output set and the replay does not set again. That covers
the alternate screen, cursor-key and mouse modes, bracketed paste, the
title, the colour palette, scroll regions and the cursor's position.
Line-oriented output replays well. A full-screen program shows the redraws
in the ring, possibly on the primary screen, until it redraws. Same-size
reattaches get no SIGWINCH.

## Backpressure

The pseudo-terminal's reader goroutine appends each read (32 KiB at most)
to the ring and to the view's queue in one step. When the queue holds
`Config.LiveLimit` bytes (256 KiB by default) and the next read would
overflow it, the reader waits. It stops reading the pseudo-terminal, the
kernel's buffer fills, and the program blocks in `write`. The pump goroutine
swaps the queue for a second buffer and writes it to the stream. So a
session holds at most two queue buffers per view, plus the ring and one
read buffer, however slow the view. In the daemon, the stream is the data
connection. A client whose sink blocks stops reading its socket, the
daemon's writes block, and the same bound applies (plus the kernel's socket
buffers).

The live stream never loses a byte. Only the ring wraps, and only the
replay is affected. With no view attached, the reader never waits: a
detached session keeps running and its output only goes to the ring. A view
that never drains holds its session's program, and only that one, until it
detaches, closes, is taken over, or its connection breaks. A sink that
returns an error detaches its view. In the daemon, a broken connection
counts as Close.

The sink gets its Writes from one goroutine at a time, and must not keep
`p`. A `Write` already in flight when `Detach` or `Close` is called may
complete after it returns, since an in-process sink that blocks cannot be
interrupted. `Done` closes once the pump has returned.

## Packaging and launching (opt-in)

An app that does not opt in uses `NewLocal` and ships nothing extra.
Installed apps need no change.

An app that opts in has two choices:

- **Launch itself.** The app's main recognises a daemon flag before anything else and calls `session.RunDaemon(dir, …)`. Its client calls `session.DialOrLaunch(dir, []string{os.Executable(), "-sessiond", "-session-dir", dir}, …)`. This is what termdemo does, and the bundle `gnui package` makes needs nothing new: the daemon is the app's own executable. The daemon runs in a session of its own, with its output in `<dir>/log`. Two clients that launch at once start two daemons; the lock lets one win, and both clients connect to it.
- **Bundle `cmd/gnui-sessiond`** beside the executable (for example in Contents/MacOS) and launch that instead, with `-dir`. It is the same `RunDaemon`.

Either way the daemon belongs to the user, lives only as long as its
sessions (with `-idle` / `IdleExit`), and is not registered with launchd or
systemd. Nothing persists across a daemon restart: no SQLite, no state on
disk beyond the socket and lock. Remote transport is out of scope.

## termdemo

termdemo runs its program in a local service by default, so its behaviour,
`-shot` and `audit_test` are as before. `-daemon` runs it in the daemon,
`-persist` makes it outlive the window and prints its ID, `-attach ID`
reopens one, and `-sessions`, `-kill ID` and `-shutdown` manage the daemon.
Output reaches the terminal through `termSink` (cmd/termdemo/sink.go), a
small `io.Writer` that queues bytes and wakes the window. The UI thread
feeds them to `TerminalState.Feed` in `take`. That is the seam where a
bounded, blocking terminal writer replaces it.

## Tests (term/session, real pseudo-terminals)

- attach, then output and the exit code (local);
- detach: the program lives on and its output keeps reaching the ring. Reattach: the reset, then the replay, then the live output, with the ticks continuous across the boundary;
- client crash, simulated by closing its sockets with nothing said: the persistent session lives and a new client reattaches; the non-persistent session dies;
- persistent vs non-persistent close, explicit kill, and the `-1` exit code a kill reports, in both local and daemon modes;
- a view held for 0.5 s and then slow, while 1.49 MB passes through a 64 KiB ring: every live byte arrives in order, and the queue stays at or under its limit. Measured on an M-series Mac with a 32 KiB limit: at most 26 KiB queued in-process and 32 KiB in the daemon;
- a daemon launched on demand as a separate process, with a program that ignores SIGHUP and has a child: after `Shutdown`, the daemon, the program, its child and a second session's program are all gone by PID, and so is the directory;
- the ring's replay starts at a safe boundary after wrapping, with OSC strings, SGR and multi-byte UTF-8 in the stream.

## Limits

- Multiple simultaneous views of one session are not supported; the second takes over.
- Process-group signalling misses processes a job-control shell moved to other process groups. Those get the kernel's hang-up when the terminal closes, if they are in the foreground; background jobs that ignore it survive.
- The kill grace is the daemon's, not the session's.
- The replay is byte-based, as described above. A terminal-state snapshot (a libghostty-vt instance in the daemon) is the next step if replay is not good enough for full-screen programs.
