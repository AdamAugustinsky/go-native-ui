// Command probe-p7 is phase 8's probe P7 (docs/PHASE8-PLAN.md, "How Windows
// is verified"): in the CI runner's session, does SendInput reach our own
// windows, and do Shell_NotifyIconW and RegisterHotKey work there? It
// decides how W4 (the RichEdit matrix), W5 (the tray) and W7 (hotkeys) are
// tested: with real input in CI, or with messages sent to the window and the
// VM for the rest.
//
// What it does, on the main thread (locked):
//
//   - reports the session id (ProcessIdToSessionId), whether the input
//     desktop opens (OpenInputDesktop), and whether a taskbar exists
//     (FindWindowW "Shell_TrayWnd");
//   - opens a visible window and a hidden app window (WS_POPUP,
//     WS_EX_TOOLWINDOW, as D68's), brings the visible one to the front
//     (SetForegroundWindow, and if that is refused again after an injected
//     Alt tap, which lifts the foreground lock) and reports
//     GetForegroundWindow before and after, with the foreground window's
//     class;
//   - SendInput: an "A" key press and a left click at the window's centre
//     (SetCursorPos first), counting WM_KEYDOWN and WM_LBUTTONDOWN received
//     within -wait;
//   - RegisterHotKey of Ctrl+Alt+Shift+F12 with MOD_NOREPEAT on the hidden
//     window (F11 when F12 is refused, since the SDK reserves F12 for
//     debuggers), a second registration of the same chord (expected to fail
//     with ERROR_HOTKEY_ALREADY_REGISTERED, 1409), and the chord sent with
//     SendInput, counting WM_HOTKEY within -wait;
//   - Shell_NotifyIconW: NIM_ADD with a GUID (NIF_GUID) and
//     NIM_SETVERSION 4, then NIM_DELETE; if the GUID add fails (Windows
//     binds a GUID to the executable's path, and go run builds to a new
//     temporary path each time), again with a uID only.
//
// Run it on a CI runner (windows-2025, windows-11-arm) or the VM:
//
//	go run ./cmd/probe-p7 [-timeout 60s] [-wait 2s] [-input=false] [-hotkey=false] [-tray=false]
//
// Output: one JSON object on stdout (the result type in main_windows.go:
// sessionId, inputDesktop, taskbarPresent, foreground, sendInput, hotkey,
// tray, timedOut, errors). Exit status 0 when the probe answered (a "no" is
// still an answer), 1 when it could not run (no window, or the timeout
// fired; the JSON then has what was found so far), 2 on a system other
// than Windows.
package main
