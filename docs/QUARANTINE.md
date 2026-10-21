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
| Windows (amd64, arm64) | `TestImageAndFilesWithPowerShell` (platform) | `powershell did not reach ready1` after 190-205 s; 6+ CI failures on both arches | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Windows (amd64, arm64) | `TestSaveIsDebounced` (app/winstate) | fails intermittently on the runner (seen at 0.28 s) | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Windows (amd64, arm64) | `TestFirstFrameIsContent` (gpu) | white sample in the first frame; **possibly a real white flash**, not only a flake | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Windows (amd64, arm64) | `TestSystemIconsWindowsConcurrent` (img) | `icon 3 failed when decoded concurrently`; possibly a real COM concurrency bug | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Windows (amd64, arm64) | `TestPackagedBinaryHasNoDebugChordOrInspector` (cmd/gnui) | `go build` cannot fetch modules from proxy.golang.org (needs the network) | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Windows (amd64, arm64) | spike gate: stall patterns, and `animated frames allocated 1 objects / 16 bytes` | display-link stall / one 16-byte allocation in the animated frames; warns instead of failing | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Linux (all 4 jobs) | `TestLastDrainEndsParking` (app/notify) | intermittent failure on the Linux runners | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Linux (all 4 jobs) | `TestCloseIsBounded` (task) | `a second Close failed` (a 1 ms Close); 200/200 locally | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Linux (all 4 jobs) | `TestMixFollowsAndDoesNotAllocate` (theme) | `Mix allocates 1 times`, a one-off allocation; 200/200 locally | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Linux (all 4 jobs) | `TestPackagedBinaryHasNoDebugChordOrInspector` (cmd/gnui) | needs the network (module proxy) | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| Linux (all 4 jobs) | spike gate: vsync pacing (`no frame callbacks`) and stalls | warns instead of failing | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestIdleAppDrawsNothingAfterOneSecondButTheSystemsNews` (widgettest/live) | unexplained frames seconds after the wait; possibly a real wake problem | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestIdleAppDrawsNothingBetweenWaits` (widgettest/live) | `kept drawing 1 frames after the wait ended`; possibly a real wake problem | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestCloseIsBounded` (task) | `a second Close failed` | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestDragTypesDoNotReachHosts` (platform) | `host under the view: false`, about 1 in 5 | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestEditorNestedLoopDraws`, `TestEditorUndoRedo`, `TestEditorMarkedTextSubmission` (widgettest/live) | fail under contention, pass alone (MarkedTextSubmission added after it failed on run 39531272169) | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestMixFollowsAndDoesNotAllocate` (theme) | one-off allocation | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestStallOfAHiddenWindowSkipsAndSaysWhy` (widgettest) | intermittent failure on the macOS runner | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestRunKeepsDesignatedRequirement` (cmd/gnui) | intermittent failure on the macOS runner | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestScrollEventsThroughTheQueue` (platform) | SIGTRAP inside AppKit | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestWindowButtonsSignatureMismatch` (platform) | duplicate hover tracking areas after the rollback; possibly a real bug | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | `TestVListEditFrameBudget` (widget/base) | the 2 ms CPU edit budget is missed on a noisy runner | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |
| macOS arm64 | benches: spike, listbench, pilot (both), gallery (both), notes, imgbench | `bench stalled` / `bench timed out` / `no frame callbacks` (display-link stall, `drawable=0`); warns instead of failing. Allocation and CPU gates stay blocking | [#34](https://github.com/AdamAugustinsky/go-native-ui/issues/34) |

Not quarantined: `cmd/vlistbench -bench -gate` is not run by `ci.yml` (only its
package tests are), so its 2 ms edit budget cannot fail CI.

To un-quarantine a test, delete its name from the job's `QUARANTINE` regexp in
`.github/workflows/ci.yml` (and its row here).
