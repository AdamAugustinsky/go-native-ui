# Phase 8 measurements — Windows

The answers of the Windows probes P1–P8 (`docs/PHASE8-PLAN.md`, "How Windows is verified"), from GitHub's hosted runners. These are **runner** numbers: shared hosts with a software adapter ("Microsoft Basic Render Driver") and no GPU or hardware encoder. They answer yes/no questions and give orders of magnitude; they are never budgets (D78). The VM and hardware columns stay empty until those machines exist.

| Runner | Image | Windows | Arch |
|---|---|---|---|
| `windows-2025` | Windows Server 2025 | 10.0.26100 | amd64 |
| `windows-11-arm` | Windows 11 | 10.0.26200 | arm64 |

Toolchain: Go 1.27.1 (`go-version-file: go.mod`), `CGO_ENABLED=0`. Workflow: `.github/workflows/probes.yml`. Date: 2026-10-27.

| Run | Commit | What changed before it |
|---|---|---|
| [40544964714](https://github.com/AdamAugustinsky/go-native-ui/actions/runs/40544964714) | b148170 | first run |
| [40547464988](https://github.com/AdamAugustinsky/go-native-ui/actions/runs/40547464988) | 417d9c6 | P3's runtime path and options object, P5's pixel read and IME section, P8's stream reader and WebSocket diagnostics, P2's covering-window report |
| [40548088487](https://github.com/AdamAugustinsky/go-native-ui/actions/runs/40548088487) | e067f06 | the workflow stops `WWAHost.exe` (the arm64 "Microsoft account" prompt); P8 makes 10 large WebSocket trips |
| [40548591015](https://github.com/AdamAugustinsky/go-native-ui/actions/runs/40548591015) | c6b2bca | the workflow also stops Start and Search and presses Escape (**final**) |

Every probe exited 0 (answered) on both runners in every run. Ranges below are over the runs where the probe measured the thing in question.

## Answers

| Probe | Question | amd64 (`windows-2025`) | arm64 (`windows-11-arm`) | Key numbers | Decides |
|---|---|---|---|---|---|
| P1 | `OleInitialize` cost on the main thread; session-end messages to a hidden app window | yes: both behave | yes: both behave | cold `OleInitialize` 1.37–1.96 ms (amd64), 2.00–2.61 ms (arm64), including ole32.dll's load of 0.82–1.14 / 1.30–1.69 ms; second call `S_FALSE` in 2–5 µs | D68, W1: unchanged |
| P2 | Child window over a flip-model swap chain: z-order, clipping, frames of trailing | above the swap chain, clipped, **0 frames** | the same, **0 frames**, in the final run only (earlier runs: covered by shell windows, not a measurement) | 113 of 113 sampled compositions at 0 frames; composition period 15.625 ms | D66: no fallback needed |
| P3 | WebView2 runtime present; does `CreateWebViewEnvironmentWithOptionsInternal` create an environment and navigate? | **yes** (runtime 155.0.4283.62) | **yes** (runtime 156.0.4314.42) | see P3 below | **D65 option 1** |
| P4 | Media Foundation and WGC on each runner; hardware or software encoder | MF present, MP4 written and read back, WGC captures; software encoders only | the same | MP4 3.008 s AAC + H.264; WGC 63–76 frames in 2 s | M2: both runners are reference runners |
| P5 | Hosted RichEdit: colour emoji, IME, TOM undo, `EN_REQUESTRESIZE` | **no colour emoji**; IME composition commits; undo groups; resize, caret, spell check, OLE paste work | the same | emoji 0 yellow px vs control 127 red px; 45 px per line at 24 pt | D67: colour needs the windowless refinement |
| P6 | Toast with the registry AUMID alone, and with the shortcut; read back from history | both post and read back | both post and read back | history count 1, then 2; `Setting` 0x80070490 without the shortcut | D71: registry key suffices on these builds |
| P7 | `SendInput`, `RegisterHotKey`, `Shell_NotifyIconW` in the runner's session | all yes | all yes **after the workflow clears the shell windows**; before that, `SendInput` missed and the tray failed with E_FAIL | hotkey: second registration → 1409; tray: GUID add, version 4 | W4, W5, W7: tested on both runners |
| P8 | WinHTTP proxy, streamed uploads, streamed responses, cancel, WebSocket | all yes | all yes | first streamed line 0.4–0.6 ms; 32 MiB WebSocket 24 of 24 trips per arch since the fix | D73, N1, N2: synchronous WinHTTP |

## P1 — OLE on the main thread, session end (`cmd/probe-p1`)

| | amd64 | arm64 |
|---|---:|---:|
| ole32.dll load | 0.82–1.14 ms | 1.30–1.69 ms |
| `OleInitialize`, cold (includes the load) | 1.37–1.96 ms | 2.00–2.61 ms |
| `OleInitialize`, second call (`S_FALSE`) | 1.8–2.1 µs | 3.4–4.9 µs |
| process start → after `OleInitialize` | 9.9–13.6 ms | 20.9–30.2 ms |

The hidden `WS_POPUP`/`WS_EX_TOOLWINDOW` window received `WM_QUERYENDSESSION` (`lParam` 0x80000000): allowed (result 1), refused (0), and refused with a `ShutdownBlockReasonCreate` reason that reads back ("Saving the recording"). It received `WM_ENDSESSION` with `wParam` FALSE and TRUE, was alive after each, and received the registered `TaskbarCreated` broadcast. Same on both runners in all four runs.

## P2 — child windows over the swap chain (`cmd/probe-p2`)

400×300 topmost popup, `WS_CLIPCHILDREN`, D3D11 on the "Microsoft Basic Render Driver" (driver type hardware, feature level 11.1), `FLIP_DISCARD`, composition period 15.625 ms (64 Hz). 120 frames at 4 px per frame; frames near the wrap are not sampled.

| | amd64 (4 runs) | arm64, final run | arm64, runs 1–3 |
|---|---|---|---|
| `WindowFromPoint` at the child | the child | the child | not ours |
| at rest, screen read: child visible, clipped, swap chain beside | yes, yes, yes | yes, yes, yes | nothing of ours visible |
| at rest, `PrintWindow(PW_RENDERFULLCONTENT)` | yes, yes, yes | yes, yes, yes | yes, yes, yes |
| sampled compositions | 113 | 113 | 0 (`trailing_valid` false) |
| child trailing the band | 0 frames in 113 | 0 frames in 113 | — |

On arm64, the covering window at the sample point was a `Windows.UI.Core.CoreWindow`. In run 40547464988 it was "Microsoft account" (`WWAHost.exe`, 1024×768). In run 40548088487, after `WWAHost` was stopped, it was "Start" (`StartMenuExperienceHost.exe`), with "Search" (`SearchHost.exe`) in the foreground. The final run's workflow step stops all three and presses Escape.

## P3 — WebView2 without `WebView2Loader.dll` (`cmd/probe-p3`)

The first run found the runtime in the registry but reported `found=false`. It looked for `<EBWebView>\<arch>\EmbeddedBrowserWebView.dll`, but the `EBWebView` value is the version folder itself (`C:\Program Files (x86)\Microsoft\EdgeWebView\Application\<version>`), and the DLL is in `<version>\EBWebView\<x64|arm64>\`. Fixed in 417d9c6, with an `ICoreWebView2EnvironmentOptions` implemented in Go, which is passed as the options argument.

| | amd64 | arm64 |
|---|---|---|
| registry | `HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\ClientState\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}` only (no native-view or `HKCU` key) | the same |
| runtime version (`pv`, and the folder name) | 155.0.4283.62 | 156.0.4314.42 |
| client DLLs shipped (`EBWebView\`) | x64, x86 | arm64, x64, x86 |
| `CreateWebViewEnvironmentWithOptionsInternal(TRUE, 0, folder, options, handler)` | S_OK, environment S_OK | S_OK, environment S_OK |
| options getters the runtime called | AdditionalBrowserArguments, Language, AllowSingleSignOnUsingOSPrimaryAccount once; TargetCompatibleBrowserVersion twice | the same |
| controller, `put_Bounds` (RECT by value), `put_IsVisible`, `Navigate` to `http://127.0.0.1:<port>/` | all S_OK | all S_OK |
| `NavigationCompleted`: `IsSuccess`, `WebErrorStatus` | TRUE, 0 | TRUE, 0 |
| title read back | "gnui-p3" | "gnui-p3" |
| load DLL (cold) | 304–571 ms | 2.2–2.8 ms |
| environment completed | 162–245 ms | 5.0–6.6 ms |
| controller completed | 2.02–3.94 s | 0.43–0.47 s |
| navigation completed | 0.44–0.90 s | 1.05–3.21 s |

Runs 40547464988, 40548088487 and 40548591015. The NULL-options attempt runs only when the first fails, so it never ran. The vtable slots used (`ICoreWebView2Environment` 3 and 5; `ICoreWebView2Controller` 4, 6, 24 and 25; `ICoreWebView2` 5, 15 and 48; `ICoreWebView2NavigationCompletedEventArgs` 3 and 4; the handlers' `Invoke` 3; `ICoreWebView2EnvironmentOptions` 3–10) and the four IIDs were checked against `WebView2.h` from the Microsoft.Web.WebView2 1.0.4314.35 NuGet package.

## P4 — Media Foundation and Windows.Graphics.Capture (`cmd/probe-p4`)

| | amd64 (Server 2025) | arm64 (Windows 11) |
|---|---|---|
| mfplat / mfreadwrite load, `MFStartup` | yes, S_OK | yes, S_OK |
| H.264 encoders (`MFTEnumEx`) | software 1 ("H264 Encoder MFT"), hardware none | the same |
| AAC encoders | software 1 ("Microsoft AAC Audio Encoder MFT"), hardware none | the same |
| sink writer: NV12 + PCM in, 90 frames + 90 audio blocks | every step S_OK; 156–381 ms | every step S_OK; 302–334 ms |
| MP4 | 139,488 bytes; read back 3,007.98 ms, streams audio/AAC and video/H264 | the same |
| WGC `CreateForWindow` on our window, free-threaded pool | every step S_OK | every step S_OK |
| frames in 2 s; mean interval; first frame | 63–76; 26.0–31.5 ms; 28.6–55.6 ms | 64–73; 27.3–31.2 ms; 23.1–156.3 ms |

## P5 — hosted RichEdit (`cmd/probe-p5`)

`RICHEDIT50W` from System32's msftedit.dll, in a top-level window, on an OLE STA.

**Colour emoji.** The first run's `coloured_pixels` was -1, because `PrintWindow` on the child read nothing. From 417d9c6 on, the probe reads the host window with `PW_CLIENTONLY | PW_RENDERFULLCONTENT` (it worked in every run). The text "A😀B☺C" is set at 24 pt, with B red as a control and ☺ (U+263A) as a one-colour baseline. Pixel counts in each glyph's cell, the same on both runners and in all three runs:

| setup | face read back | emoji yellow / dark px | red B: red px | ☺ yellow px | colour |
|---|---|---:|---:|---:|---|
| default font binding | Segoe UI Symbol | 0 / 112 | 127 | 0 | no |
| emoji's face set to Segoe UI Emoji | Segoe UI Emoji | 0 / 331 | 127 | 0 | no |
| + `EM_SETTYPOGRAPHYOPTIONS(TO_ADVANCEDTYPOGRAPHY \| TO_DEFAULTCOLOREMOJI \| TO_DISPLAYFONTCOLOR)` (accepted, reads back 0x3001) | Segoe UI Emoji | 0 / 331 | 127 | 0 | no |
| `EM_SWITCHTOD2D` on a fresh control (child process) | — | returns 0 | — | — | not supported |

**IME.** Without an input method composing, the bare `WM_IME_STARTCOMPOSITION`, `WM_IME_COMPOSITION(GCS_RESULTSTR)` and `WM_IME_ENDCOMPOSITION` return 0 and insert nothing; this is what the first run's zeros were. `WM_IME_CHAR(U+3042)` inserts "あ". With Microsoft's Japanese IME (layout 0x04110411, `ImmIsIME` TRUE) opened on the control's context, these worked on both runners: `ImmSetCompositionStringW(SCS_SETSTR, "あ")` showed "あ" in the control while composing, and `ImmNotifyIME(NI_COMPOSITIONSTR, CPS_COMPLETE)` committed it. On arm64 this held even when our window was not in the foreground. (The en-US layout also reports `ImmIsIME` TRUE there.)

**The rest.** These held in all four runs:
- Undo: one `EM_UNDO` after two `EM_REPLACESEL`s left "baseone" without grouping, and "base" inside `ITextDocument::BeginEditCollection`/`EndEditCollection` (S_OK). With `Undo(tomSuspend)`/`tomResume` (both S_FALSE) around the two replacements, one `EM_UNDO` removed nothing ("baseone two").
- `EN_REQUESTRESIZE` asked for 45, 135 and 450 px for 1, 3 and 10 lines at 24 pt, one notification each.
- `EM_POSFROMCHAR` gave a 45 px line height.
- `IMF_SPELLCHECKING` was accepted (lang options 0x182 → 0x982).
- `IRichEditOleCallback`, set with `EM_SETOLECALLBACK`: a pasted `CF_UNICODETEXT` called `QueryAcceptData` once, with `fReally` TRUE, and arrived.

## P6 — toasts (`cmd/probe-p6`)

AUMID `GonativeUI.ProbeP6`, registered under `HKCU\Software\Classes\AppUserModelId`, with `SetCurrentProcessExplicitAppUserModelID`. The same on both runners in all four runs:

| variant | `CreateToastNotifierWithId`, `Show` | `Setting` | in history | first seen in history | `Dismissed` |
|---|---|---|---|---|---|
| registry key only | S_OK, S_OK | 0x80070490 (element not found) | yes (count 1) | 5.1–13.2 ms amd64, 7.7–173 ms arm64 | `TimedOut` |
| registry key + Start-menu shortcut with `System.AppUserModel.ID` | S_OK, S_OK | `Enabled` | yes (count 2) | 3.9–6.8 ms amd64, 8.2–9.2 ms arm64 | `TimedOut` |

The Go-implemented delegates (`Activated`, `Dismissed`, `Failed`): 6 made, 6 freed, 0 live, 0 stale.

## P7 — input, hotkeys and the tray in the runner's session (`cmd/probe-p7`)

Session 2, input desktop, taskbar present on both.

| | amd64 (4 runs) | arm64, runs 1–2 (account prompt up) | arm64, run 3 (prompt stopped) | arm64, final run (prompt, Start, Search stopped) |
|---|---|---|---|---|
| foreground before | our window | `Windows.UI.Core.CoreWindow` | `Windows.UI.Core.CoreWindow` | our window |
| `SetForegroundWindow` | yes | no | no | yes |
| `SendInput` key → `WM_KEYDOWN`, click → `WM_LBUTTONDOWN` | 1, 1 | 0, 0 | 0, 0 | 1, 1 |
| `RegisterHotKey(Ctrl+Alt+Shift+F12)`; second registration | yes; 1409 | yes; 1409 | yes; 1409 | yes; 1409 |
| chord through `SendInput` → `WM_HOTKEY` | 1 | 1 | 1 | 1 |
| `Shell_NotifyIconW` add with a GUID, `NOTIFYICON_VERSION_4`, delete | yes, yes, yes | E_FAIL (0x80004005) | yes, yes, yes | yes, yes, yes |

## P8 — WinHTTP (`cmd/probe-p8`)

Loopback servers inside the probe; session with `WINHTTP_ACCESS_TYPE_AUTOMATIC_PROXY`. Throughputs are loopback on shared hosts and vary 4× between runs; they only show that nothing is slow by design.

| | amd64 | arm64 |
|---|---|---|
| IE proxy config / default proxy | read; auto-detect on, no proxy | the same |
| 64 MiB upload, known length | ok; 537–2,179 MB/s | ok; 525–2,200 MB/s |
| 64 MiB upload, chunked (framed by the probe) | ok; 600–1,959 MB/s | ok; 507–1,245 MB/s |
| streamed response, `QueryDataAvailable` → `ReadData(n)` | first line 0.43–0.58 ms, longest wait between reads 5.7–5.9 ms, 200 lines in 202 reads | first line 0.53–0.54 ms, 5.6–5.8 ms, the same |
| the same with a 4 KiB `ReadData` loop only | first line 656–671 ms, 2 reads (`ReadData` waits for a full buffer) | 658–681 ms, 2 reads |
| cancel: `WinHttpCloseHandle` on a blocked `WinHttpWriteData` | returns at once (≤ 0.4 ms) with 12017 | the same |
| WebSocket upgrade | 101; 7.5–8.1 ms | 101; 8.8–120 ms |
| text and binary echo; ping answered; close 1000 "server-bye" | yes | yes |
| 32 MiB message round trips (one send or 1 MiB fragments) | 24 of 24 since 417d9c6; 63–118 ms each; received as 1,024 `BinaryFragment` pieces of ≤ 32 KiB plus a `BinaryMessage` | 24 of 24 since 417d9c6; 134–173 ms each |

The first run's two failures:
- `stream_lines` failed on both arches. The probe read with a plain 4 KiB `WinHttpReadData`, which in synchronous mode returns only once its buffer is full or the response has ended. Kept as `stream_lines_readdata_only` to record this.
- `websocket.big_ok` failed on arm64 alone: the message arrived as 32,800,758 of 33,554,432 bytes in 1,001 fragments, ending in a `BinaryMessage`, with no error. This did not recur in 24 later trips per arch, which recorded what the server received (always the full length) and the first differing byte (none). Its cause is unknown.

## VM and hardware

Not measured: there is no Windows VM or PC yet (plan, "What needs you").
