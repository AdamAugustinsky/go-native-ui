# Quarantined tests and gates (speed mode)

Adam approved speed mode on 2026-10-20: a known flaky test is skipped on CI,
with an issue link, instead of being rerun until it passes; and the benches'
display-link stall failures warn instead of failing the job. Everything below
is tracked in [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34).

How it works, in `.github/workflows/ci.yml`:

- A job with tests quarantined has a `QUARANTINE` regexp; its `go test` steps
  pass it as `-skip "$QUARANTINE"`. A test is skipped only on the jobs that list
  it. Only the macOS test job has one now (`TestGreenButtonWaitsForNativeReadiness` and `TestClientClose`); the Windows and Linux jobs run
  everything (an empty regexp would skip every test, so they have none).
- The bench steps named below run through `stallgate.sh`: when the bench fails
  with a display-link stall message (`bench stalled`, `bench timed out`,
  `no frame callbacks`, a spike timeout or a wrong count of measured frames),
  the step prints `::warning::quarantined ...` and succeeds. Every other
  failure (allocations on macOS, CPU budgets) still fails the step.

| Job | Test or gate | Symptom | Issue |
|---|---|---|---|
| macOS arm64 (test job) | `TestGreenButtonWaitsForNativeReadiness` (app) | `stalled in phase 1; ~6250 ms of frame time, ~7170 ms of clock`: AppKit sent willEnterFullScreen but never didEnterFullScreen, while the loop kept drawing 60 frames/ticks a second (runs [39735370966](https://github.com/AdamAugustinsky/go-native-ui/actions/runs/39735370966/job/119679698082), [39846637201](https://github.com/AdamAugustinsky/go-native-ui/actions/runs/39846637201/job/120061540012)); not reproduced in a 12/12 controlled hosted experiment (39738443438). Possibly the hosted runner's transition, possibly the pump: needs a real-hardware check. Still runs locally | [#38](https://github.com/AdamAugustinsky/go-native-ui/issues/38), [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 (test job) | `TestClientClose` (app/ws) | `no pending read: the server saw close 0` / 1006: NSURLSession drops the close frame of a task cancelled just after it opens; Close now waits for a pong first (ae4a96f), which took it from ~1 % to 3 in 1500 under heavy load, but it still failed on the loaded runner (p8-land 40619444296). network.yml still runs the ws suite on macOS on its own | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Windows (amd64, arm64) benches | spike gate: stall patterns | display-link stall; warns instead of failing (the 16-byte animated-frame allocation is no longer excused: p6-flakes 141b484, 542f3e8) | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Linux (all 4 jobs) | spike gate: vsync pacing (`no frame callbacks`) and stalls | warns instead of failing | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 benches | benches: spike, listbench, pilot (both), gallery (both), notes, imgbench | `bench stalled` / `bench timed out` / `no frame callbacks` (display-link stall, `drawable=0`); warns instead of failing. Allocation and CPU gates stay blocking | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |

Not quarantined: `cmd/vlistbench -bench -gate` runs in the macOS benches job
(step "vlistbench", `-allow-unpaced -budget-scale 3`), outside the stall-warning
wrapper, so all its gates block, including the list and edit budgets at 3x and
the check that the session exercised the list (`history loads 0` failed it on
p7-perf-fsmem's CI 39656311756).

To quarantine a test, add its name to the job's `QUARANTINE` regexp in
`.github/workflows/ci.yml` (create the `env:` entry and the `-skip` flag if the
job has none) and a row here with the failing run. To un-quarantine one, delete
its name there and its row here.

## Un-quarantined on p9-ci-flakes

Each was skipped on some job and now runs on all of them. "Passes" counts CI
runs of this branch (the macOS and Windows test jobs, the four Linux jobs); a
test that fails again on main goes back in.

Runs: R1 [40633691026](https://github.com/AdamAugustinsky/go-native-ui/actions/runs/40633691026) (all 11 jobs green), R2 [40635284542](https://github.com/AdamAugustinsky/go-native-ui/actions/runs/40635284542) (all green but macOS, which failed `TestPackageAppMustMatchItsConfig`, a test that was not quarantined and a flake of its own: see the last item), R3 [40638991672](https://github.com/AdamAugustinsky/go-native-ui/actions/runs/40638991672) (all 11 jobs green, with the fix to that test).

**The cause is found and fixed** (a test that fails without the fix is named):

- `TestColdThreadAttach`, `TestZeroAlloc` (internal/ffi, Linux): not ffi at all. Profiling every allocation (`MemProfileRate = 1`, a Linux container) put the one 16 or 32 byte object in `runtime.(*scavengerState).sleep` → `(*timers).addHeap`: the scavenger sleeps on a timer in the heap of whichever P it last ran on, and the first time a P's heap grows is an append. The tests give every P's timer heap room before they count (564153d); 3 of 300 runs counted it before and 0 of 1100 after on arm64, 3 of 300 and 0 of 300 on amd64 (emulated). A rarer window of 7 or 8 objects is a new thread's M, which the tests' three windows take.
- `TestEvictionSkipsReplacedNotes` (app/notify, Windows): the oldest note was chosen by its post time in ns, and Windows' clock ticks about every 0.5 ms or more, so two posts could tie and the lower slot won (`evicted n0,x`). Ties go to the smaller gen (0d96655); `TestEvictionSkipsReplacedNotesOnACoarseClock` freezes the clock and fails without it with the same message.
- `TestLastDrainEndsParking` (app/notify, Linux): the retry goroutine ends its list before it answers what it stored, so the last drain, seeing it not running, returned before the answer. It waits for the goroutine whenever one was started (0d96655); `TestLastDrainWaitsForTheAnswerOfTheRetry` fails without it. The CI log of the original failure is gone; this is the race the code has, not a reproduction of that failure.
- `TestCloseDoesNotWaitForTheIconStyleRead`, `TestOffscreenRunsLeaveNoWorkersOrHandles` (cmd/pilot/pilot, Windows): clock races (a read released by a 5 s timer, a Close over 1 s that includes the icon cache's second, workers given 2 s to end). Close runs under a 10 s deadline, workers get 30 s (e9fada7).
- `TestPackagedBinaryHasNoDebugChordOrInspector` (cmd/gnui, Windows, Linux): the darwin build fetched purego from the proxy and got a stream error. It builds with `GOPROXY=off`; the jobs run `go mod download`; off CI it skips when the modules are not cached (b7a6d50).
- `TestDragTypesDoNotReachHosts` (platform, macOS): registered a host for `arrText` before `dragInit` had made it; alone, it failed every time (reproduced locally). It calls `dragInit` first.
- `TestOtherWindows` (widget/widgettest/live, macOS): waits up to 240 frames for the key instead of three, and a cleanup closes its window whatever way it ends, so a failure no longer breaks the next test. The key's not moving was not reproduced.
- `TestPackageAppMustMatchItsConfig` (cmd/gnui, macOS; never quarantined): `checkBuiltApp` ranged over a map, so it named the version before the identifier one time in eight (6 of 80 runs locally, R2 on CI). Fixed order.

**Un-quarantined without a cause found**: the test passed in CI and the part that could be checked locally was hardened or read, but nothing reproduced the original failure, so these are the ones to put back first if they fail on main.

- `TestMixFollowsAndDoesNotAllocate` (theme, macOS and Linux): `AllocsPerRun(100)` divides as integers, so "allocates 1 times" meant 100 or more allocations in the window, which the code cannot make (0 of 2000 runs in Linux containers). It takes the quietest of three windows and reports all three.
- `TestSystemIconsWindowsConcurrent` (img, Windows): the failing step is traced and logged by the test (`failed decode steps`). It passed on both Windows jobs in each run. No COM race was found by reading `sysicon_windows.go` either.
- `TestPackageWithoutAppleIdentity` (cmd/gnui, macOS): the hdiutil race is retried in `gnui` since a5a206e.
- `TestEditorNestedLoopDraws`, `TestEditorUndoRedo`, `TestEditorMarkedTextSubmission`, `TestStallOfAHiddenWindowSkipsAndSaysWhy`, `TestRunKeepsDesignatedRequirement`, `TestScrollEventsThroughTheQueue`, `TestVListEditFrameBudget` (macOS): they failed "under contention" or about one run in five before the benches moved to their own job; they passed on the macOS test job in R2 and R3 and passed repeatedly where they could be run here (`TestScrollEventsThroughTheQueue` 300 of 300, `TestRunKeepsDesignatedRequirement` 10 of 10, `TestVListEditFrameBudget` 5 of 5). Two CI runs of a test that failed one in five is weak evidence.
