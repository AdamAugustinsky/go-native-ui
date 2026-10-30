# Terminal output pacing

How program output reaches `rexui.Terminal` views: a bounded producer side
with backpressure (`TerminalInbox`), one parse budget per window shared by
its terminals (`TerminalScheduler`), and the diagnostics of both. Code:
`rexui/terminal_feed.go`; tests: `rexui/terminal_feed_test.go`.

## The problem

`TerminalState.Feed` appended to an unbounded buffer on the UI thread, and
each view parsed up to `FeedBudget` (4 ms) of it per frame. A reader
goroutine that copies a pty into another unbounded slice can outrun the
parser for ever (`yes`, `cat` of a large file), so memory grows without
limit while the program never feels backpressure, and N busy panes cost
N × 4 ms per frame, with the focused pane waiting behind the others.

## Producer side: `TerminalInbox`

`s.OpenInbox(TerminalInboxOpts{Wake, HighWater, LowWater})` makes the
state's inbox (once; later calls return it). It is safe from any goroutine.

- **Write** queues bytes. While `HighWater` bytes (default 1 MiB, about two
  frames of parsing) are queued but not parsed, it blocks, and resumes once
  the view has parsed down to `LowWater` (default 256 KiB, at most half of
  `HighWater`). A write larger than the room left is queued in parts, so the
  queue never exceeds `HighWater`. Queued bytes are counted until parsed,
  including those the UI has taken and not yet finished.
- **ReadFrom** (`io.ReaderFrom`, so `io.Copy(in, r)` uses it) waits for room
  before each read, so the backlog stays in the pty's or socket's own
  buffer: the kernel blocks the child when it fills, as a real terminal
  would. It returns nil at EOF and does not close the inbox. It cannot
  interrupt a `Read` already blocked in `r`; closing `r` is its owner's job.
- **Wake** is called from the writer when bytes arrive and the UI has taken
  everything before (one call per batch the UI takes), and from Close. Pass
  `app.Win.Wake` (or `platform.Wake`); both are coalesced again below.
- **Close** may come from either side. Blocked and later writes return
  `ErrTerminalInboxClosed` with the count queued before it. Bytes queued
  before Close are still parsed. `TerminalState.Close` closes the inbox, so
  a reader stuck on a full queue lets go. `Closed()` with
  `TerminalState.Pending() == 0` means everything the program wrote has been
  parsed.

The UI swaps the inbox's buffer with its own when it has parsed what it had,
so in steady state nothing is copied twice and nothing allocates. Memory per
terminal is bounded by about two `HighWater` buffers.

## Legacy `Feed`

`Feed` is unchanged: it runs on the UI thread, cannot block, keeps every
byte and has no bound. It stays for small UI-side writes and for existing
producers, which keep their behaviour; `TerminalStats.Fed` counts what came
this way, so an app can see a producer that should move to the inbox. Fed
bytes and inbox bytes may be mixed; fed bytes are parsed after whatever the
UI had already taken from the inbox.

No output is ever discarded. There is no overflow policy: dropping bytes in
the middle of an escape sequence or a UTF-8 character would corrupt the
screen in ways a later redraw may not fix. If one is ever needed it must be
opt-in and resynchronise (a full reset plus a snapshot or replay from the
session owner), not truncate.

## Shared budget: `TerminalScheduler`

`NewTerminalScheduler(TerminalSchedulerOpts{Budget, FocusShare})`, one per
window, passed as `TerminalOpts.Scheduler`. Terminals join at their first
view or with `Add` (do this for panes that start hidden), and leave with
`Remove` or `Close`.

The parse runs once per frame, at the first view built with the scheduler,
so every view drawn later in the frame shows it. In a frame that builds no
view (all panes hidden) the app calls `Run(c)`. Each frame:

1. The terminal with the keyboard focus, or `TerminalOpts.Focus ≥ 0.5`,
   drawn in the last frame, parses first, for up to `FocusShare` (0.5) of
   `Budget` (4 ms) while others have output queued, or all of it alone.
2. The others share what is left in proportion: views drawn in the last
   frame weigh 2, hidden ones 1. Each group starts from a terminal one
   further on each frame (round-robin), so a terminal the budget did not
   reach is early the next frame.
3. Time still left goes round again in the same order.

The scheduler asks for another frame while any of its terminals has output
queued, and for none when all are drained: zero idle CPU is kept, and a
reader's Wake starts the next frame.

Chunks given to the library are sized from a running estimate of its parse
rate (512 B to 64 KiB), so a slice ends near its deadline instead of up to a
64 KiB chunk past it.

`TerminalOpts.FeedBudget` keeps its meaning without a scheduler (the view's
own budget, default 4 ms). With one it caps that view's part of the shared
budget (0: no cap of its own).

## Diagnostics

`TerminalState.Stats()` (UI thread, no allocation): `Queued` and
`QueuedPeak` bytes, `Fed`, `Written` and `Parsed` totals, `ParsedFrame` and
`ParseTime` of the last frame that parsed it, `HighWaterHits`, `Blocked`
(time writers waited), and `FramesBehind` (frames in a row that ended with
output queued). `TerminalScheduler.Stats()`: the last frame's `Budget`,
`Spent`, `Parsed` bytes, `Terminals` and `Behind`. The debug overlay
(`ui/debug.go`) records frame causes only and has no app metrics hook, so
these are not shown there; an app can draw them itself.

## Wiring a pty

```go
// Init
s, _ := rexui.NewTerminal(term.Options{Scrollback: 5000})
in := s.OpenInbox(rexui.TerminalInboxOpts{Wake: win.Wake})
sched := rexui.NewTerminalScheduler(rexui.TerminalSchedulerOpts{})
go func() {
	in.ReadFrom(ptyFile) // blocks while the view is behind
	in.Close()           // the program is done
}()

// Build
sig := rexui.Terminal(k, "terminal", s, rexui.TerminalOpts{Scheduler: sched})
…
if in.Closed() && s.Pending() == 0 { /* everything parsed: quit or show exited */ }
```

## Measured

`GNUI_MEASURE=1 go test ./rexui -run TerminalSchedulerPanes -v`, Mac mini,
macOS 27.2 (Mac16,10, load average about 3.4 from other agents' work). 8 panes: two 16 MiB
floods of coloured lines (one drawn, one hidden) and six recorded sessions
from `term/testdata/sessions`, read in 1 B to 16 KiB pieces by goroutines
through inboxes, and a focused pane fed 300 B a frame. 4 ms budget.

| High water | Bytes | Frames | Parse per frame p50 / p99 / max | Focused pane max | Max queue | High-water hits |
|---|---|---|---|---|---|---|
| 64 KiB | 33.7 MB | 130 | 0.46 / 0.50 / 0.98 ms | 69 µs | 64 KiB | 512 |
| 1 MiB (default) | 33.6 MB | 27 | 1.60 / 4.01 / 4.01 ms | 26 µs | 1 MiB | 32 |

In both runs the focused pane parsed its output in the frame it came (0
frames late), no queued pane went a frame without parsing, every byte was
parsed, and every pane's final screen (`AppendScreen(VT)`) equals the old
path's: the same bytes fed and parsed whole at the same size. The 12 µs
over budget in the second run is the last chunk's estimate.
