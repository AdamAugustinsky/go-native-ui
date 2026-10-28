// Command probe-p1 is phase 8's probe P1 (docs/PHASE8-PLAN.md, "How Windows
// is verified"): what does OleInitialize on the main thread cost at startup,
// and does the hidden app window of D68 (a top-level WS_POPUP with
// WS_EX_TOOLWINDOW, never shown) get WM_QUERYENDSESSION, answer it both ways
// and survive WM_ENDSESSION? It decides W1 and D68: whether the main thread
// can be an OLE STA from the start, and whether the hidden window (not a
// message-only one) can own the session-end messages.
//
// What it does, on the main thread (locked):
//
//   - times LoadLibrary of ole32.dll, the cold OleInitialize and a second
//     one (S_FALSE), with QueryPerformanceCounter, and the time since the
//     process was created (GetProcessTimes) at the cold call's start and end;
//   - creates the hidden app window and reads back that it is not visible;
//   - registers "TaskbarCreated" and reports its message id;
//   - calls ShutdownBlockReasonCreate on the window and reads the reason
//     back with ShutdownBlockReasonQuery;
//   - from another OS thread, sends the window (SendMessageTimeoutW, 5 s,
//     SMTO_ABORTIFHUNG) WM_QUERYENDSESSION with ENDSESSION_LOGOFF three
//     times, answered TRUE, FALSE, and FALSE with a block reason set; then
//     WM_ENDSESSION with wParam FALSE and with TRUE (the window handles it
//     and does not exit), pinging the window after each to show it is alive;
//   - posts a registered message of its own to HWND_BROADCAST to show that
//     the window receives broadcasts (a message-only window would not).
//
// Run it on a CI runner (windows-2025, windows-11-arm) or the VM:
//
//	go run ./cmd/probe-p1 [-timeout 30s]
//
// Output: one JSON object on stdout (the result type in main_windows.go:
// ole32LoadUs, oleInitHR, oleInitColdUs, oleInitSecondHR, oleInitSecondUs,
// msSinceProcessStartAtOleInit, msSinceProcessStartAfterOleInit,
// hiddenWindow, taskbarCreatedMsg, shutdownBlockReason, queryEndSession,
// endSession, broadcastReceived, timedOut, errors). Exit status 0 when the
// probe answered (a "no" is still an answer), 1 when it could not run (no
// window, or the timeout fired; the JSON then has what was found so far),
// 2 on a system other than Windows.
package main
