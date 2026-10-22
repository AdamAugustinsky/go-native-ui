# Quarantined tests and gates (speed mode)

Adam approved speed mode on 2026-10-20: a known flaky test is skipped on CI,
with an issue link, instead of being rerun until it passes; and the benches'
display-link stall failures warn instead of failing the job. Everything below
is tracked in [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34).

How it works, in `.github/workflows/ci.yml`:

- Each job (macOS, Windows, Linux) has a `QUARANTINE` regexp; its `go test`
  steps pass it as `-skip "$QUARANTINE"`. A test is skipped only on the jobs
  that list it.
- The bench steps named below run through `stallgate.sh`: when the bench fails
  with a display-link stall message (`bench stalled`, `bench timed out`,
  `no frame callbacks`, a spike timeout or a wrong count of measured frames),
  the step prints `::warning::quarantined ...` and succeeds. Every other
  failure (allocations on macOS, CPU budgets) still fails the step.

| Job | Test or gate | Symptom | Issue |
|---|---|---|---|
| Windows (amd64, arm64) | `TestEvictionSkipsReplacedNotes` (app/notify) | `evicted n0,x, want n0,n1`; 4 times across branches on Windows amd64 and arm64 (e.g. p6-c7 39551142037, main 39583048063) | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Windows (amd64, arm64) | `TestSystemIconsWindowsConcurrent` (img) | `icon 3 failed when decoded concurrently`; possibly a real COM concurrency bug | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Windows (amd64, arm64) | `TestCloseDoesNotWaitForTheIconStyleRead`, `TestOffscreenRunsLeaveNoWorkersOrHandles` (cmd/pilot/pilot) | timing assertions fail on the loaded arm64 runner (`a read that returned after Close published 7 (want 0)`; the package takes 270-310 s there) | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Windows (amd64, arm64) | `TestPackagedBinaryHasNoDebugChordOrInspector` (cmd/gnui) | `go build` cannot fetch modules from proxy.golang.org (needs the network) | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Windows (amd64, arm64) | spike gate: stall patterns | display-link stall; warns instead of failing (the 16-byte animated-frame allocation is no longer excused: p6-flakes 141b484, 542f3e8) | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Linux (all 4 jobs) | `TestLastDrainEndsParking` (app/notify) | intermittent failure on the Linux runners | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Linux (all 4 jobs) | `TestMixFollowsAndDoesNotAllocate` (theme) | `Mix allocates 1 times`, a one-off allocation; 200/200 locally | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Linux (all 4 jobs) | `TestPackagedBinaryHasNoDebugChordOrInspector` (cmd/gnui) | needs the network (module proxy) | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Linux (all 4 jobs) | spike gate: vsync pacing (`no frame callbacks`) and stalls | warns instead of failing | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestDragTypesDoNotReachHosts` (platform) | `host under the view: false`, about 1 in 5 | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestEditorNestedLoopDraws`, `TestEditorUndoRedo`, `TestEditorMarkedTextSubmission` (widgettest/live) | fail under contention, pass alone (MarkedTextSubmission added after it failed on run 39531272169) | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestMixFollowsAndDoesNotAllocate` (theme) | one-off allocation | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestStallOfAHiddenWindowSkipsAndSaysWhy` (widgettest) | intermittent failure on the macOS runner | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestRunKeepsDesignatedRequirement` (cmd/gnui) | intermittent failure on the macOS runner | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestPackageWithoutAppleIdentity` (cmd/gnui) | `hdiutil verify ... unable to recognize ... as a disk image. (Resource temporarily unavailable)` (runs 39559733303, 39692907334): the image is verified while still busy, likely a runner race in hdiutil/diskimages-helper | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestVListEditFrameBudget` (widget/base) | the 2 ms CPU edit budget missed again after p6-flakes' rebuild-clock fix: `move a run of 100000: a frame spent 2.282 ms of CPU on the edit, budget 2 ms` (run 39625101050); re-quarantined as agreed when it was un-quarantined | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestOtherWindows` (widgettest/live) | `the other window did not take the key from the active app` (CI 39531083904 on main, 39542658745 on p6-editorfix); the failure **leaves its other window open**, which broke the next test (`TestEditorEditMenuValidatesAndActs`: `typed: not true within 120 frames`) | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestScrollEventsThroughTheQueue` (platform) | SIGTRAP inside AppKit | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | benches: spike, listbench, pilot (both), gallery (both), notes, imgbench | `bench stalled` / `bench timed out` / `no frame callbacks` (display-link stall, `drawable=0`); warns instead of failing. Allocation and CPU gates stay blocking | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |

Not quarantined: `cmd/vlistbench -bench -gate` runs in the macOS job (step
"vlistbench", `-allow-unpaced -budget-scale 3`), outside the stall-warning
wrapper, so all its gates block, including the list and edit budgets at 3x and
the check that the session exercised the list (`history loads 0` failed it on
p7-perf-fsmem's CI 39656311756).

To un-quarantine a test, delete its name from the job's `QUARANTINE` regexp in
`.github/workflows/ci.yml` (and its row here).
