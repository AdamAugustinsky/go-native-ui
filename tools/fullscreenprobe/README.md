# Finite hosted fullscreen diagnosis (not a release patch)

The experiment branch leaves `3367e56` and `11acddd` untouched. Its CI workflow
contains only this isolated GUI job, not the repository's parallel test suite.

Predeclared matrix: three fixed repetitions of four cells, all in one macOS-15
job. The Go binaries use Go 1.27.1, CGO_ENABLED=0 and gnui_publicapi, matching
the failing phase. Every attempt is a fresh process, serialized under lockf
and caffeinate with an unlocked-screen check. No attempt retries internally.

1. **control-original**: 05f2a50, original fullscreen fixture and assertions,
   with telemetry only; no delayed readiness fixture.
2. **control-delayed**: unchanged production from 05f2a50 with the identical
   delayed-readiness fixture from 11acddd, supplied through a Go overlay.
3. **candidate-delayed**: 11acddd with that same fixture and instrumentation.
4. **native-control**: plain NSWindow, NSApplication.run, delayed activation,
   green-button entry/exit, did-notifications and a native timer. No gnui pump
   or Metal. Its timer drives the same 100ms-capped frame clock and 5s phase,
   15s frame-clock and 120s wall budgets; it is not a GPU-rendering control.

The script verifies that the two revisions differ only in test files.
Cells 2 and 3 share byte-identical instrumented fullscreen source. Cell 1
separates the new fixture from the old, behavior-unchanged control.

Instrumentation is generated once and shared: app-loop turns and maximum
gap, committed-frame and GPU-command-completion counts, read-only CFRunLoop
activity observer counters (including before/after waiting), existing native
state and mode, activation and fullscreen notifications. It adds no native
timer or wake source to gnui. GPU completion does not prove on-glass display.
The observer and completion callbacks can perturb timing; absence of a
reproduction is not proof of absence or grounds for quarantine.

`results.tsv` records every outcome, including skips and setup failures.
Full logs, environment, source overlays and hashes are uploaded even on
failure. The existing test timeouts and outcome assertions are not relaxed.
An outer 170s supervisor bounds a broken event loop and cleans up only the
process group it created, including a child orphaned by a Go test timeout.
That outcome is a failure, never a retry or a passing observation.
