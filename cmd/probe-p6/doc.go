// Command probe-p6 is phase 8's probe P6 (docs/PHASE8-PLAN.md, "How Windows
// is verified"): toasts from an unpackaged executable, before track W6.
//
// Question: can a plain executable post a WinRT toast with an AUMID that
// is registered only in the registry (HKCU\Software\Classes\AppUserModelId\<id>,
// DisplayName), after SetCurrentProcessExplicitAppUserModelID, as a3-kb's
// C# client does today, or does it also need a Start-menu shortcut that
// carries the AUMID (System.AppUserModel.ID)? For each variant it reports
// IToastNotifier's Setting, Show's HRESULT, whether the toast is in the
// action centre's history (IToastNotificationHistory2::GetHistoryWithId)
// and which of the Activated, Dismissed and Failed events (TypedEventHandler
// delegates implemented with internal/com) fired, with Failed's error code.
// The toast has two actions with protocol activation (probe-p6:action?x=1),
// D71's route for a stopped app; clicking them needs a person (the VM).
//
// It decides D71's registration: whether install.Install must write the
// shortcut for toasts to work (windows-11-arm on CI; the VM for Windows 10).
//
// Run on a runner (or the VM), from the repository root:
//
//	go run ./cmd/probe-p6                     # registry variant, then registry + shortcut
//	go run ./cmd/probe-p6 -shortcut=false     # the registry variant only
//	go run ./cmd/probe-p6 -keep -wait 30s     # leave everything, wait for a click
//
// Flags: -id (the AUMID, default GonativeUI.ProbeP6), -shortcut (default
// true), -keep (keep the registry key, the shortcut and the toasts),
// -wait (per variant, default 4 s), -timeout (default 60 s; on expiry the
// report so far is printed with timedOut and the exit status is 1).
//
// Output: one JSON object on stdout: probe, arch, windows (major, minor,
// build), aumid, steps (HRESULT of each process-wide step: RoInitialize,
// SetCurrentProcessExplicitAppUserModelID, the registry, the clean-up),
// variants [{name, steps, setting, showHR, historyCount, historyFirstMs,
// activated, dismissed, dismissReason, failed, failedError, posted}],
// shortcutHR, shortcutSteps, comObjects (the delegate table's counts),
// elapsedMs. Exit status 0 when the probe answered (a "no" included), 1 on
// a timeout, 2 off Windows.
package main
