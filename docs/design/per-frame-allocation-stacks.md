# Per-frame allocation stacks for the `-gate` tooling (note for #37)

Status: a note, not built. The item asks for the stack of an allocation in one steady frame of a gate run; this records what exists, what a per-frame version would take, and why it is not worth building yet.

## What exists

`internal/allocsite` (D27) turns `runtime.MemProfileRate = 1` into per-window attribution: every allocation is recorded with its stack at once, the runtime publishes the profile at a collection, so two `Snapshot`s taken after a `runtime.GC()` differ by exactly the allocations between them. `Diff` groups them by stack and classifies each by its first frame past the allocator: `Runtime` (a new thread, the scavenger's timer heap, the type-assertion cache) is a one-off a gate passes over; `Ours` is not, whichever window it lands in. Profiling does not allocate on the heap, so `ReadMemStats` deltas stay exact. The benches' `-allocsites` flag reports the sites of a whole session or run.

The same method found the Linux allocation of #34/#37 in minutes: the one 16 or 32 byte object that failed `internal/ffi`'s `TestZeroAlloc` and `TestColdThreadAttach` is `runtime.(*scavengerState).sleep` → `(*timers).addHeap`, the first append to a P's timer heap (the scavenger sleeps on a timer in the heap of the P it last ran on). Not ours, and not in thread attach or callbacks.

## What per-frame would take

A session is one stretch; a frame is one call of `Build`. To say which frame allocated, a snapshot has to follow each frame, and a snapshot after the frame is only complete after a collection. A gate run in a diagnostic mode (a second run after a failure, never the timed one) could do `runtime.GC()` after each frame and `Diff` against the last snapshot, so each allocation is attributed to the frame it happened in. The attribution is exact; the run is not the run that failed:

- A collection per frame empties `sync.Pool`s (two collections drop everything, one moves it to the victim cache), so code that pools (the `fmt` printers, `encoding/json`) allocates in the frame after each collection, and the diagnostic reports its own refills.
- Finalizers, `runtime.AddCleanup` callbacks and weak pointers run on the collections and allocate on their own goroutines, which the stack shows as `Other`.
- The frame's timing changes (a collection costs more than a frame), so a failure that depends on pacing may not repeat.
- Goroutine-creation stacks stop in the runtime (`newproc`), so an allocation on a worker goroutine names the worker's function, not the frame that asked for the work. That is the "truncated `newproc` is unknown" of the item and does not change with per-frame snapshots.

None of these makes it unsound as a diagnostic, but each makes it misreport something, and the existing per-window report already says which origin allocates (ours or the runtime's), which is what a gate needs. Which frame is the one thing the per-window report does not say.

## Recommendation

Do not build it now. If a failing gate keeps needing the frame, add `-allocsites-per-frame` to `app`'s bench driver as above, off by default, and print the pool and finalizer caveats in its header. Sampling `MemProfile` at the default rate per frame (the item's first idea) stays out: its records appear at the next collection and are sampled, so a one-object allocation can be missing or land in a later frame.
