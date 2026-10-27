# Phase 8 plan: Windows parity, for HyperPedia and the A3 app

Drafted 2026-10-27, for your approval. It answers your decision of that day: migrate two A3 Lab desktop apps fully onto the framework with the Rex look, and "build the missing Windows pieces in the framework … don't keep the old C#/Electron builds as the Windows answer."

- **a3-kb / HyperPedia** (`a3-kb/apps/mac`, Swift; `a3-kb/apps/windows`, .NET WinForms): a menu-bar/tray app with a global-hotkey palette, a floating recording bar, a settings window, screen and microphone recording to MP4, tokens in the Keychain or Credential Manager, notifications with actions, an `a3kb:` URL scheme, self-update from a JSON manifest, and on Windows a per-user self-install.
- **a3-app** (`a3-app/desktop`, Electron over `ui/`, React): becomes a native chat client for the coding agent over HTTPS and one WebSocket per window, with an embedded browser, a terminal pane, a multi-line composer, markdown with code, notifications, downloads, file pickers, more than one window and a local port tunnel.

Each app's own plan is `docs/GNUI-MIGRATION-PLAN.md` in its repo (branch `gnui-migration-plan`), written in parallel. This plan is the framework's part: what both apps need from it, on macOS and Windows.

**Why phase 8.** Phases 1–6 built the framework; phase 7 is ZapFast (D33, D49–D51). The `p8-rex` branch already carries Rex-look work for apps (D62, `theme/rex` dark fit, `ThemeWithField`, `LabelOpts.MonoPt`), which both apps use, but no plan claims a phase 8. So this is phase 8, and its new decisions are numbered from D63. If `p8-rex` adds entries before this plan is approved, the numbers here move up.

**What phase 8 does:**
- **Ends D34 for Windows.** The services, windows and hosts phase 6 shipped for macOS get Windows implementations: more than one window and windowless running, tray, toasts, native file dialogs, drag and drop, hosted native views as child windows, the multi-line composer, login items, self-update, URL schemes, single-instance activation, install and packaging.
- **Adds what neither OS has yet,** on both: panel windows (the palette and the recording bar), global hotkeys, screen and audio capture to MP4, a hosted OS browser view, HTTP methods with streaming bodies, a WebSocket client, a terminal emulator widget, markdown and syntax highlighting.
- **Makes Windows verifiable** beyond CI: a Windows VM with a desktop, the agent bridge on Windows, and a measurement page for Windows numbers.

**Linux stays where phase 6 left it** (D63): it compiles, keeps its 4 CI jobs green and returns `ErrUnsupported` for the new APIs.

Behaviour not yet verified is marked as an assumption or tied to a named probe (P1–P10, under [How Windows is verified](#how-windows-is-verified)).

## Scope

In scope, by package (the D28/D40 layering is unchanged; new packages are imported only by apps that use them):

| Package | What changes | Status |
|---|---|---|
| `platform` | Windows: a window registry (D30), windowless running on a hidden app window, window commands (title, hide, activate, frame), panel windows, child-window hosts, the RichEdit editor, `IFileDialog`, OLE drag and drop, a Rex titlebar. macOS: panel windows | changed |
| `app` | Windows: the multi-window pump, the app object's quit routes, `EvOpenURLs` from a second instance | changed |
| `app/tray`, `app/notify`, `app/login`, `app/selfupdate`, `app/dock` | Windows implementations (`app/dock` becomes taskbar progress, overlay badge and flash on Windows) | changed |
| `app/hotkey` | global shortcuts, both OSes | new |
| `app/install` | per-user install, uninstall and registration (URL schemes, AUMID, shortcut) on Windows; reports "not needed" on macOS | new |
| `app/fetch` | any method, request bodies with upload progress, streamed responses; WinHTTP on Windows instead of `net/http` | changed |
| `app/ws` | a WebSocket client: `NSURLSessionWebSocketTask`, WinHTTP's WebSocket API, and on Linux `net/http` with our own framing | new |
| `app/webview` | a hosted OS browser view: `WKWebView`, WebView2 (D64, D65) | new |
| `media/capture` | screen or window plus microphone and system audio to MP4, both OSes | new |
| `term` | a terminal emulator: VT parser, screen and scrollback model, input encoding (pure Go) | new |
| `markdown`, `highlight` | a GFM-subset parser to rich-text spans; table-driven syntax highlighters (pure Go) | new |
| `rexui` | `Terminal`, `Markdown`, `CodeBlock`, `WebView` (a host box with Rex chrome) components; the composer gains the Windows editor | changed |
| `cmd/gnui` | `package -target windows`: icon, manifest and version resources, optional signing, a zip; the agent bridge on Windows | changed |
| `internal/com`, `internal/win32` | COM objects implemented in Go (vtables for callbacks), WinRT activation and `HSTRING`s | changed |

## Not in this phase

- **Linux parity** for any of the above (D63).
- **UI built in HTML.** D2 still excludes it; D64 allows only hosting the OS's browser view as content.
- **UI Automation** (Windows accessibility for our drawn UI). The hosted RichEdit and WebView2 bring their own providers; the drawn UI stays unannounced to Narrator. It is a phase of its own (the N7 bridge, ported); see Risks.
- **A local terminal** (ConPTY, `forkpty`). a3-app's terminal is remote: bytes come over the agent's WebSocket. `term` is the emulator only.
- **DirectComposition.** Panels on Windows are opaque with DWM corners and shadow; hosts are child windows (D66). Transparent panels and visual-hosted WebView2 come with DComp later.
- **Native glass on Windows** (Mica, Acrylic). The drawn fallbacks stay (D54).
- **The drawn multi-line editor.** Both apps' editors on Windows are hosted RichEdit (D67); the drawn one waits for Linux.
- **MSI or MSIX installers, the Microsoft Store, a fixed-version WebView2 runtime.**
- **Intel Macs** (D55). Both apps lose Intel Mac support when they move; each app's plan states it.
- The final name, the licence, API stability.

## The standing rules

As in phase 6 ("Contracts"), for every item below:
- the UI runs on the main thread; workers wake the loop;
- a steady-state frame allocates nothing, under the exact gate (D11, D27), including frames that move hosts, scroll the terminal or stream markdown;
- bulk data is pointer-free;
- idle means 0 presents and at most 1 wakeup per second, with a tray icon, hotkeys, a hidden panel, an open WebSocket and an idle hosted view;
- no cgo; the framework loads only OS libraries and OS components (D2, D64, D65);
- content-first startup within 150 ms warm (D21). **Nothing new loads before the first frame:** WebKit, WebView2, ScreenCaptureKit, Media Foundation, WinRT toasts and RichEdit load on first use. A tray app's "first frame" is its tray icon: the icon is set before any window exists, and that is measured.

**Windows rules added by this phase:**
- **The main thread is an OLE single-threaded apartment** (`OleInitialize` in `platform`'s start on Windows), because WebView2, RichEdit's OLE callback, `IFileDialog` and `RegisterDragDrop` need it. Its cost is measured (P1); today only workers call `CoInitializeEx` (`app/shell/shell_windows.go:52`, `img/sysicon_windows.go:163`, `fs/open_windows.go:75`).
- **COM objects we implement** (event handlers, `IDropTarget`, `IRichEditOleCallback`, WinRT delegates) are fixed-size records in a table, one `syscall.NewCallback` per method shape shared by all objects of a kind, dispatching on `this` (the D36 pattern: one callback per class, not per object). Reference counts are explicit; leak counters join the shell leak gate (`platform.ShellObjects`).
- **Native modal loops** (file dialogs, drag out, menu tracking, the size/move loop) keep the app drawing through the frame handler, never re-entering Build (D30's rule, now on Windows).
- **Undocumented Windows APIs** follow D53's two switches (a build tag and a runtime option), as private AppKit API does (D76).

## Gap table

What the two apps need, per OS: the status today on `main` (693d3b3), and the target API. "kb" is a3-kb, "app" is a3-app.

| Capability | Needed by | macOS today | Windows today | Target API (item) |
|---|---|---|---|---|
| Tray / menu-bar icon with a menu | kb | yes (`app/tray`, `tray_darwin.go`) | `ErrUnsupported` (`app/tray/tray_other.go:14`) | `tray.New` on `Shell_NotifyIconW` with a native popup menu (W5) |
| Running with no window | kb | yes (D37, `WaitApp`) | no pump without a window (`platform/appobj_other.go:23`, `app/pump_other.go:9`) | the app pump on a hidden app window (D68) (W1) |
| More than one window | both | yes (D30) | refused (`platform/platform_windows.go:303`) | `App.OpenWindow` on a window registry (W1) |
| Window commands: title, hide, activate, frame | both | yes | no-ops (`platform/wincmd_other.go:10,38,41,44`) | the same `Window` methods, implemented (W1) |
| Borderless floating panels (palette, recording bar) | kb | none | none | `platform.Options.Panel` / `app.Config.Panel`: borderless, non-activating, floating, hide on deactivate, all Spaces, excluded from capture (W2) |
| Global hotkeys | kb | none (phase-6 gap table) | none | `app/hotkey`: Carbon `RegisterEventHotKey`; `RegisterHotKey` (W7) |
| URL scheme registration | kb | yes, `gnui.json` `urlSchemes` (`cmd/gnui/config.go:45`) | no registration | `install.RegisterScheme` (per-user `HKCU\Software\Classes`), also done by `install.Install` (W10) |
| URL activation | kb | yes (`EvOpenURLs`) | `instance` hands args over (`app/instance/transport_windows.go`), but nothing turns them into `EvOpenURLs` | a second instance's `scheme:` arguments arrive as `EvOpenURLs` (W1) |
| Single instance, hand-off to the front | both | yes (`app/instance`) | named mutex and pipe exist; the primary cannot take the foreground | `instance.Send` calls `AllowSetForegroundWindow` for the primary (W1) |
| Notifications with actions and activation | both | yes (`app/notify`, D45) | `ErrUnsupported` (`app/notify/center_other.go:13`) | `notify` on WinRT toasts with an AUMID, protocol activation and the `Activated` event (W6) |
| Taskbar / Dock progress, badge, attention | app | badge and bounce (`app/dock`); no Dock progress | `ErrUnsupported` (`app/dock/dock_other.go:7`) | `dock.SetProgress` (both); `ITaskbarList3` progress and overlay, `FlashWindowEx` (S1) |
| Native file dialogs | both | yes (`OpenPanel`, `SavePanel`) | none (`platform/dialog_other.go:18`) | the same calls on `IFileOpenDialog` / `IFileSaveDialog` (W8) |
| Alerts | both | `NSAlert` | drawn `rexui` dialog | unchanged: drawn on Windows (D29's reasoning: no dark mode in `TaskDialog`) |
| Drag and drop into a window | app | yes (N5) | `ErrDragUnsupported` (`platform/drag_other.go:30,33`) | `SetDropKinds` on OLE `RegisterDragDrop`; drag out on `DoDragDrop` (W8) |
| Hosted native views | app | yes (`NewHost`, N3b) | `ErrNoHosts` (`platform/shell_other.go:29`) | child windows in clip containers (W3, D66) |
| Multi-line editor with IME | both (kb quick-add `Palette.cs:73`; app composer) | hosted `NSTextView` (`NewEditor`, N4) | `ErrNoHosts` (`platform/editor_other.go:10`); the composer falls back to one drawn line | `NewEditor` on a hosted RichEdit, same contract (W4, D67) |
| Embedded browser | app | none; D2 excludes webviews | none | `app/webview` on `WKWebView` and WebView2 (H1, H2; D64, D65) |
| Screen + mic + system audio to MP4 | kb | none in the framework; `media.Recorder` is audio WAV only (D57) | `ErrUnsupported` (`media/media_other.go:24`) | `media/capture` on ScreenCaptureKit; on WGC + WASAPI + Media Foundation (M1, M2; D72) |
| Exclude own windows from capture | kb | none | none | `Panel.ExcludeFromCapture`: `sharingType = none`; `WDA_EXCLUDEFROMCAPTURE` (W2) |
| HTTP methods, request bodies, upload progress, streaming | both (kb uploads recordings; app `PUT /api/upload` up to 20 GB) | GET only (`app/fetch/fetch.go:4,185`) | GET only, on `net/http` (`app/fetch/fetch_http.go:9`) | `fetch.Do(ctx, Request, w, Options)` (N1, D73) |
| WebSocket client | app | none in the repo | none | `app/ws` (N2, D73) |
| Local TCP listener for the port tunnel | app | stdlib `net` | stdlib `net` | app-side, plain Go (`net.Listen`, +0.65 MB measured); `app/ws` carries the bytes |
| Terminal emulator widget | app | none (`theme/rex/terminal.go` is a palette for rexlook's fake panes) | none | `term` + `rexui.Terminal` (C1, D74) |
| Markdown and code in messages | app | rich text spans (D42), no markdown | spans on `BasicLayouter` (no bidi) | `markdown` + `highlight` + `rexui.Markdown` (C2, C3, D75) |
| Secrets | both | Keychain | Credential Manager, 2,560-byte limit (`app/secret/secret_windows.go:24`) | unchanged API; values over the limit are refused today, so S2 adds DPAPI-sealed files for them |
| Login item | kb | yes (`app/login`) | `ErrUnsupported` (`app/login/login_other.go:6`) | `login` on the `HKCU …\Run` value, reading `StartupApproved` (S3) |
| Self-update | both (kb has one; app has none) | yes (D61) | `ErrUnsupported` (`app/selfupdate/selfupdate_other.go:7`) | `selfupdate` on Windows: helper copy, rename swap, health marker, rollback; same signer when signed (S4, D76) |
| Per-user install and uninstall | kb (Windows `Installer.cs`) | n/a (a bundle) | none | `app/install` (W10, D76) |
| Packaging: icon, manifest, version, signing | both | `gnui package` (D47) | plain `go build`; no icon, manifest or signing (`docs/guide/packaging.md:234`) | `gnui package -target windows` (T1, D77) |
| Rex window chrome | both | native shell (D52) | system caption; no custom titlebar | a drawn Rex titlebar with Windows caption buttons and snap layouts (W9) |
| Video and YUV textures | (kb preview, optional) | yes (D56, D58) | `ErrVideoUnsupported` (`media/video_other.go:17`, `gpu/yuv_other.go:15`) | not in this phase unless an app plan asks; a recording is opened with the shell |
| Agent bridge (`GNUI_INSPECT`) | verification | yes | refused (`internal/inspectwire/listen_windows.go:11`) | a named pipe on Windows (T2) |
| Accessibility | both | NSAccessibility (N7) | none | not in this phase (see Risks) |

The table compresses to one finding: every Windows row except secrets, clipboard, shell, prefs, paths, winstate, log, locale and `instance`'s transport is missing, and four capabilities (panels, hotkeys, capture, webview) are missing on macOS too.

## Proposed decisions (D63–D78)

Proposed here, not written to `DECISIONS.md`. Each lists the choice, the alternatives and the cost; the ones that need your approval under D2 say so.

### D63. Phase 8 is Windows parity for two apps; Linux waits

**Decision.** Revises D34 for Windows only. Every API phase 6 shipped for macOS, and every new one below, gets a Windows implementation in this phase, held to the same gates. Linux keeps compiling and its CI jobs stay green; new APIs return `ErrUnsupported` there, tested.

**Alternatives:** all three OSes per feature (roughly a third more native work, and Linux has no user among the two apps); keeping the C# and Electron builds on Windows (you rejected it, 2026-10-27).

**Cost:** Linux's parity phase grows by what this one adds (webview excepted, D64).

### D64. Hosting the OS's browser view is allowed; UI in HTML is not (amends D2)

**Decision.** D2's exclusion of webviews is split in two:
- **Still excluded:** building any part of an app's or the framework's interface in HTML, CSS or JavaScript (Electron, Wails, Tauri and webview-shell UIs); a JavaScript bridge from page script to app code; bundling a browser engine.
- **Allowed:** an optional package, `app/webview`, that hosts the OS's own browser view as content, the way an image or a video is content: `WKWebView` (WebKit, part of macOS) and WebView2 (the Evergreen runtime, a Windows component; D65). No framework package imports it, so an app that does not use it links none of it.

**The contract keeps it content:** navigate, reload, back and forward, stop; title, URL, loading and progress events; a navigation policy the app decides (scheme allow-list, new windows load in place or are refused); permissions denied by default (camera, microphone, location, notifications; the app may allow a named set); downloads reported and written where the app says; a non-persistent session by default, or a named persistent profile the app asks for; the engine's own context menu and inspector, which the app may turn off. There is no `ExecuteScript`, no injected script and no message channel in this phase.

**Linux:** WebKitGTK would bring GTK, which D2 excludes: `ErrUnsupported`.

**Why:** a3-app's browser pane shows the user's own dev servers and web pages; rebuilding a browser is not an option, and the system's engine brings its own security updates. The line that matters for the framework is that our UI stays ours (D17, D52), and that holds.

**Alternatives:** keep webviews excluded and open pages in the system browser (a3-app loses its pane; this is the no-webview fallback, kept for a missing runtime); bundle CEF or Chromium (a bundled C++ library of over 100 MB: rejected).

**Needs your approval:** yes, as an amendment to D2.

### D65. WebView2 without `WebView2Loader.dll`

**Decision.** WebView2 is a COM API, so it needs no cgo; the one native piece is its loader. We do what the loader does, in Go: read the Evergreen runtime's location from the registry (`HKLM`, then `HKCU`, `SOFTWARE\Microsoft\EdgeUpdate\ClientState\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`, value `EBWebView`), load `EBWebView\<arch>\EmbeddedBrowserWebView.dll` from it, and call its export `CreateWebViewEnvironmentWithOptionsInternal(checkRunningInstance, runtimeType, userDataFolder, options, handler)`.

**Evidence.** Microsoft's WebView2 Win32 reference documents that export and its signature, and says that it "may be modified or removed in future versions" and that the loader's `CreateCoreWebView2EnvironmentWithOptions` is the supported path ([webview2-idl](https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/webview2-idl?view=webview2-1.0.3967.48), read 2026-10-27). Two pure-Go projects ship this approach: glaze (`crgimenes/glaze`, `webview2_windows.go`, `findEmbeddedBrowserDLL`/`createEnvironment`) and Wails' `go-webview2/webviewloader` (a port of OpenWebView2Loader). We read them and copy nothing.

**What we give up against the real loader:** its policy registry overrides (`…\Policies\Microsoft\Edge\WebView2\…`), environment-variable overrides, and fallback to Beta/Dev/Canary channels. We implement the policy reads for `BrowserExecutableFolder` and `UserDataFolder` (managed machines use them); the rest is documented as unsupported.

**If the export changes:** `webview.New` returns `ErrRuntime` with the reason, the app shows its fallback (open in the system browser), and we either follow the new export or take option 2 below.

**Alternatives:**
1. Our loader (chosen): no bundled code, so no D2 approval needed for the loader, at the risk above.
2. Ship Microsoft's `WebView2Loader.dll` (about 160 KB per architecture, redistributable under the WebView2 SDK licence) next to the executable, and call its supported entry point. **A bundled native library: needs your approval under D2.** Kept as the fallback, decided only if probe P3 or a runtime update breaks option 1.
3. `WebView2LoaderStatic.lib`: needs a C linker, so cgo: rejected.
4. A fixed-version runtime (150 MB+ per architecture): rejected.

**The runtime itself** is treated as an OS component: preinstalled on Windows 11, and delivered to Windows 10 by Windows Update and Edge (not re-verified for every Windows 10 edition; LTSC and Server images can lack it). We never install it. A missing runtime is `ErrUnavailable`, with the bootstrapper URL in the error for the app to show if it wants.

### D66. Hosted views on Windows are child windows in clip containers

**Decision.** D41's host model on Windows: each host is a child `HWND` inside a container child window whose rectangle is the host box's clip (the macOS clip view's counterpart), so a host in a scrolled list is clipped by the list. Children sit above the swap chain's content, as native views do on macOS; a host is hidden while a drawn popup or modal covers it (the D41 rule, unchanged). Geometry is applied with one `DeferWindowPos` batch per frame, right after `Present`. The top-level window gets `WS_CLIPCHILDREN`.

**What is not atomic:** a flip-model `Present` and a child window move are not committed together, so a host can trail a scrolling list by one frame. Measured in P2; if it is visible by eye during a scroll, hosts are hidden during momentum scrolls and shown at rest (the fallback), and DirectComposition is the real fix in a later phase.

**Alternatives:** DirectComposition visual hosting (WebView2's composition controller, our swap chain in a DComp visual): atomic and allows transparency, but it changes the presentation path of every window and moves input routing for WebView2 into our code. Larger than this phase needs.

### D67. The multi-line editor on Windows is a hosted RichEdit

**Decision.** `platform.NewEditor` on Windows hosts a RichEdit control (`MSFTEDIT_CLASS`, `msftedit.dll`, a system library) as a D66 host, behind the composer's existing contract (phase-6 plan, "The composer"): UTF-8 byte ranges mapped to UTF-16 through the same checkpoint table; marked text visible and respected (`WM_IME_STARTCOMPOSITION` … `WM_IME_ENDCOMPOSITION`, and `ITextDocument`'s composition range); `Replace` as one undo step (`EM_REPLACESEL` with undo, inside a TOM undo group); the completion handoff by subclassing the control's key messages; height from `EN_REQUESTRESIZE`; `CaretRect` from `EM_POSFROMCHAR`; pasted images and files routed through `IRichEditOleCallback::QueryAcceptData`; spell check from `IMF_SPELLCHECKING` (Windows 8+). `rexui.Composer` and a3-kb's quick-add box then work unchanged on both OSes.

**Why:** it mirrors D41 on macOS: the OS editor brings the input methods through TSF, undo, spell check, its own UI Automation provider, and the touch keyboard, which a drawn editor would each have to earn. Both apps need exactly one multi-line field kind (a composer).

**Alternatives:**
- **The drawn multi-line editor** promised by the phase-6 plan for "the Windows and Linux phase": wrapped layout with carets and selection over `RichText`, IME through imm32 and later TSF, undo from `ui/textundo.go`. It is the only answer for Linux, and it would look exactly like Rex. Its cost here: L–XL, no spell check, no accessibility until UIA exists, and imm32 IME untested with a real input method (D25). Deferred to the Linux phase, where it is needed anyway.
- **Windowless RichEdit** (`ITextServices2::TxDrawD2D` into our own target): the Rex look with RichEdit's editing, but it needs Direct2D interop on our D3D11 device and an `ITextHost` implementation. A later refinement if the child-window editor's look falls short.

**Cost and risk:** RichEdit is a child window, so the Rex capsule behind it is a solid fill (`EM_SETBKGNDCOLOR`) and the placeholder is a drawn label shown while the editor is empty and unfocused (RichEdit has no cue banner). Colour emoji in windowed RichEdit is unverified (P5).

### D68. The app object and more than one window on Windows

**Decision.** D30 and D37 built for Windows:
- **A window registry** replaces `platform_windows.go`'s single window. The app-wide pump waits on every window's frame-latency waitable plus the message queue in one `MsgWaitForMultipleObjectsEx` (at most 63 handles: 60 windows, plus wake, the appearance watcher and spare), with window-tagged events; each window is paced on its own and presents only when it has something new.
- **An app window that is never shown** (a top-level `WS_POPUP` with `WS_EX_TOOLWINDOW`, so it is in neither the taskbar nor Alt+Tab) owns the wake message, tray and hotkey messages, `WM_QUERYENDSESSION`/`WM_ENDSESSION`, the `TaskbarCreated` broadcast (the tray icon is re-added after Explorer restarts) and the instance hand-off, so the app runs with zero windows; `WaitApp` becomes real.
- **Quit routes** (the tray menu's Quit, closing the last window, `a.Quit()`, logout and shutdown) arrive as `EvQuitRequested`, as on macOS. Logout: `WM_QUERYENDSESSION` answered inside the message when the app answers inside the request; otherwise `ShutdownBlockReasonCreate` with the app's reason, then `WM_ENDSESSION`. Windows gives an app about 5 s before offering to end it; that time is the system's.
- **Window commands** (`SetTitle`, `Hide`, `Activate`, `Visible`, `FrameRect`, `SetFrameRect`, `Zoom`, `ToggleFullScreen`) are implemented; `Activate` uses the foreground rights a hotkey, a tray click or a second instance's `AllowSetForegroundWindow` grants, and otherwise flashes the taskbar button, as Windows intends.

**Alternatives:** a message-only window (`HWND_MESSAGE`): lighter, but it receives no broadcast messages, so neither the session-end messages nor `TaskbarCreated` reach it.

### D69. Panel windows, on both OSes

**Decision.** A window kind, not a separate API: `app.Config.Panel` (and `platform.Options.Panel`) with fields `Floating`, `NonActivating`, `HideOnDeactivate`, `AllSpaces`, `ExcludeFromCapture`, `Shadow`, `Radius`; a panel is borderless, has no titlebar or traffic lights, never appears in the Dock, taskbar or Alt+Tab, and moves by its drag regions (D24).
- **macOS:** a `GNUIPanel` class (an `NSPanel` subclass on the D36 class registry) with `borderless | nonactivatingPanel`, `canBecomeKey` YES, level `floating` or `statusBar`, `hidesOnDeactivate`, `collectionBehavior` (`canJoinAllSpaces`, `moveToActiveSpace`, `fullScreenAuxiliary`), `sharingType = none`, a transparent window with the corner radius drawn by us (the Metal layer already supports alpha).
- **Windows:** `WS_POPUP` with `WS_EX_TOOLWINDOW` (no taskbar button), `WS_EX_TOPMOST` for `Floating`, `WS_EX_NOACTIVATE` for `NonActivating` (the recording bar); the palette activates when shown from a hotkey. `ExcludeFromCapture` is `SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE)` (Windows 10 2004+; `WDA_MONITOR` before). Corners: `DWMWA_WINDOW_CORNER_PREFERENCE` round on Windows 11, square on 10; the shadow is DWM's (`DwmExtendFrameIntoClientArea` with a 1-px margin). The swap chain stays opaque (`DXGI_ALPHA_MODE_IGNORE`, `gpu/d3d11_windows.go:210`): a panel's corners are DWM's, not ours.
- **HideOnDeactivate on Windows** is ours: `WM_ACTIVATE` to inactive hides the panel.

**Alternatives:** a separate `panel` package (duplicates the window lifecycle); per-pixel transparent panels on Windows through DirectComposition (later, D66).

### D70. Global hotkeys without hooks or permissions

**Decision.** `app/hotkey`: `Register(Shortcut, id) error`, `Unregister(id)`, presses delivered as `EvHotkey` (D37 class 1, on the main thread). macOS: Carbon's `RegisterEventHotKey` with one `InstallEventHandler` callback (HIToolbox, part of macOS; C functions through `internal/ffi`, the callback through the ffi callback table). Windows: `RegisterHotKey` on the hidden app window (D68), `MOD_NOREPEAT`. A shortcut another app owns fails with `ErrTaken`, which the app shows in its settings.

**Alternatives:** an event tap (`CGEventTap`) or `WH_KEYBOARD_LL` hook: they see every key the user types, need the Accessibility permission on macOS, and cost a wakeup per keystroke system-wide. Rejected for the idle and privacy budgets.

**Note:** Carbon hotkeys are deprecated API but undeprecated in practice (a3-kb uses them today, `apps/mac/Sources/A3KB/HotKeys.swift`); no replacement exists that needs no permission.

### D71. Notifications on Windows: toasts, an AUMID, protocol activation

**Decision.** `app/notify` on Windows posts WinRT toasts: `RoGetActivationFactory` for `Windows.UI.Notifications.ToastNotificationManager` and `ToastNotification`, toast XML built from the `Note` (title, body, up to 5 actions, an optional reply input), `CreateToastNotifierWithId(AUMID)`.
- **Identity:** the AUMID is `AppConfig.ID`. `install.Install` writes `HKCU\Software\Classes\AppUserModelId\<id>` (`DisplayName`, `IconUri`) and sets `System.AppUserModel.ID` on the Start-menu shortcut; at startup the process calls `SetCurrentProcessExplicitAppUserModelID`. a3-kb's C# client posts toasts with the registry key alone today (`apps/windows/src/Notifier.cs`); whether that is enough on Windows 10 is probe P6.
- **Activation while running:** the toast's `Activated` and `Dismissed` events (WinRT delegates implemented in Go) complete the response slot reserved at post time (D45), with reply text from `IToastActivatedEventArgs2.UserInput`.
- **Activation after the app quit:** each action is a protocol launch, `<scheme>:gnui-notify?slot=…&action=…` (the app's own URL scheme). Windows starts the executable with it; a running instance takes it through `instance`, a new one at startup, and both deliver it as the notification response, not as `EvOpenURLs`. An app without a URL scheme gets actions only while it runs (`PostedWithoutActions` otherwise). a3-kb already works this way (`Notifier.cs`: "protocol activation … with no COM activator").

**Alternatives:** a COM activator (`INotificationActivationCallback`, `LocalServer32`, `ToastActivatorCLSID` on the shortcut): reply text after a restart, at the cost of a COM server in the app. Later, if an app needs replies while not running. MSIX packaging (identity for free): needs signing and changes distribution; not this phase.

### D72. Screen and audio capture to MP4

**Decision.** `media/capture`: `Pick(win, Options) Ticket` (the system's picker) or `Start(Target, Options)`; `Pause`, `Resume`, `Stop(discard)`; `Elapsed` and audio `Level` readable per frame without allocating; `EvCaptureEnded` with the MP4 path and recorded duration. Options: microphone on/off, system audio on/off, cursor, frame rate (30 default), maximum size.
- **macOS 15+** (the framework's floor stays 14; capture reports `ErrUnsupported` on 14): `SCContentSharingPicker` (no Screen Recording permission), an `SCStream` with `capturesAudio`, `excludesCurrentProcessAudio` and `captureMicrophone`, and `SCRecordingOutput` writing H.264 MP4. The system writes the file: no Go on a per-frame path. Pause detaches the output (finishing a segment) and resume attaches a new one; `Stop` joins the segments with `AVMutableComposition` and a passthrough `AVAssetExportSession` on a worker. This is a3-kb's shipped design (`Recorder.swift`, `Segments.swift`), moved behind the objc layer. Delegates and completion blocks follow D36.
- **Windows 10 2004+:** Windows.Graphics.Capture (`GraphicsCapturePicker`, or `IGraphicsCaptureItemInterop::CreateForMonitor`/`CreateForWindow` for an app-drawn list as a3-kb has today) into a free-threaded frame pool on our D3D11 device; frames converted BGRA→NV12 by the D3D11 video processor (`ID3D11VideoProcessor`, a system component); WASAPI shared-mode capture of the default microphone and loopback of the default render endpoint, mixed to 48 kHz stereo float on our capture thread; a Media Foundation sink writer (`MFCreateSinkWriterFromURL`) with the H.264 and AAC encoders, hardware transforms allowed, fast-start MP4. Pause stops feeding samples and offsets later timestamps, so Windows writes one file and needs no join.
- **Threads:** WGC's `FrameArrived` and WASAPI's event run on threads we own or the frame pool's; their handlers copy or enqueue only, as D36 and D57 require. No Go code runs on an audio engine's real-time thread: WASAPI capture is a pull from our own locked goroutine with a 200 ms buffer, so a GC pause cannot drop audio.

**Alternatives:** on macOS, `SCStream` sample buffers into our own `AVAssetWriter` (works on macOS 14 and writes one file, but runs Go per video frame and audio buffer on SCK's queues); on Windows, `MFCreateSinkWriter` fed RGB with the sink writer's own colour converter (simpler, CPU conversion at 1080p30: rejected for CPU), or ScreenRecorderLib (C++/CLI, x64 only, a bundled library: rejected).

### D73. HTTP and WebSocket through the OS (refines D60)

**Decision.**
- **`fetch.Do(ctx, Request, w, Options)`**: any method, headers, a request body from an `io.Reader` with a known or unknown length, upload progress, the streamed response as `Get` streams it today (`Get` and `Bytes` stay as shorthands). Server-sent events are the app's: a streamed body it reads line by line.
- **Windows moves from `net/http` to WinHTTP** (`winhttp.dll`): the system's TLS (SChannel), trust store and proxy settings (`WINHTTP_ACCESS_TYPE_AUTOMATIC_PROXY`, Windows 8.1+), as D60 chose `NSURLSession` on macOS. Calls are synchronous WinHTTP on goroutines; cancelling closes the request handle, which ends a pending call. `net/http` stays on Linux only.
- **`app/ws`**: `Dial(ctx, url, Options) (*Conn, error)`, `ReadMessage(buf)` into the caller's buffer, `WriteMessage(kind, data)`, `Close(code, reason)`; pings answered by the OS; message size limit; no compression extension. macOS: `NSURLSessionWebSocketTask` on the D60 session machinery. Windows: WinHTTP's `WinHttpWebSocketCompleteUpgrade`, `Send`, `Receive`, `Close`. Linux: `net/http`'s upgrade plus our own RFC 6455 framing (about 400 lines; no dependency).

**Why in the framework, not each app:** the size budget and the trust store. Measured on this Mac (Go 1.27.2, `-trimpath -s -w`, hello world 1.35–1.48 MB): `net` +0.62–0.67 MB, `crypto/tls` +2.9–3.3 MB, `net/http` +4.1–4.6 MB, on darwin/arm64, windows/amd64 and windows/arm64. The gallery is already 6.94 MB on windows/amd64 and `notes` 5.81 MB; a chat app on `net/http` plus a WebSocket library lands at or over the 10 MB gate on Windows, and its TLS would ignore the system's proxy and enterprise roots. With the OS clients a3-app pays only `net` for its tunnel.

**Alternatives:** `golang.org/x/net/websocket` or `nhooyr.io/websocket`/`coder/websocket` (pure Go, permissive, but on `net/http` and `crypto/tls`: the 4 MB above); keeping `net/http` on Windows (the size, and no system proxy).

### D74. A terminal emulator of our own

**Decision.** Package `term`: a VT parser written from the DEC/ANSI state machine (Paul Williams' published diagram, vt100.net, as alacritty's `vte` and others do), a screen model (grid, scrollback ring, alternate screen, tab stops, scroll regions, origin and wrap modes, SGR with 16/256/24-bit colour, underline styles, wide and combining characters, OSC 0/2 titles, OSC 8 hyperlinks, bracketed paste, mouse modes 1000/1002/1006, application cursor and keypad keys, DECSCUSR cursor shapes), reflow on resize, and an input encoder (keys, modifiers, paste, mouse) to xterm sequences. OSC 52 clipboard writes go to the app, which decides. `rexui.Terminal` draws it with `theme/rex/terminal.go`'s palette and supports selection, copy, find and scroll.

**Pointer-free and allocation-free:** cells are 8 bytes (a rune or a grapheme index, a style index, flags), rows are offsets into a ring, graphemes beyond one rune and hyperlinks live in interned side tables with budgets. Parsing allocates nothing per byte.

**Why ours:** the candidates are small (MIT `danielgatis/go-vte`, `hinshun/vt10x`, `charmbracelet/x/vt`), but none is pointer-free or allocation-free per byte, and the parser is a few hundred lines of table; the screen model is where the work is, and it has to fit `ui`'s draw path. A dependency would need a D2 row and still be rewritten for D11.

**Width tables** (East Asian Width, emoji presentation) are generated from Unicode 17 data by a tool, as `tools/genbidi` does (D42).

### D75. Markdown and highlighting of our own

**Decision.** `markdown`: a parser for the GFM subset chat needs (paragraphs, headings, emphasis, strong, strikethrough, inline code, fenced and indented code, block quotes, ordered and unordered and task lists, tables, links and autolinks, thematic breaks, hard breaks; raw HTML shown as text) into pointer-free blocks and `ui.Span`s. It parses incrementally at block granularity, so a streaming message re-parses only its last open block. Tested against the CommonMark spec examples for the constructs supported. `highlight`: table-driven lexers for the languages agent transcripts show most (Go, TypeScript/JavaScript, Python, Rust, Swift, C#, shell, JSON, YAML, TOML, SQL, HTML, CSS, diff, Markdown), mapped to Rex code roles. `rexui.Markdown` and `rexui.CodeBlock` draw them, with a copy button per block.

**Alternatives:** `yuin/goldmark` (MIT, CommonMark-complete, but an AST of pointers per parse, and about 1 MB); `alecthomas/chroma` (MIT, 200+ languages, regex-based, several MB and allocating per token). Either would need a D2 row; the subset is small enough to own.

### D76. Install, update and undocumented API on Windows

**Decision.**
- **`app/install`** (Windows): `Install(Options)` copies the running executable to `%LOCALAPPDATA%\Programs\<Name>`, writes the Start-menu shortcut (`IShellLinkW` plus `IPropertyStore` for the AUMID), the uninstall entry (`HKCU\…\Uninstall\<id>` with `--uninstall`), URL schemes and the AUMID key, and relaunches from there; `Uninstall` removes all of it and the folder (a helper copy in `%TEMP%` deletes the running executable after it exits). `Installed()` and `Supported()` tell the app where it runs. Everything is per user: no administrator rights. On macOS, `Supported` reports `ErrNotNeeded`.
- **`app/selfupdate` on Windows**, following D61: `Prepare(path)` checks the staged executable; the helper (a copy of the running executable started with a private argument) waits for the parent to exit, renames the old executable aside (Windows allows renaming a running image), moves the new one in, relaunches it, and rolls back if `Started` does not arrive within 30 s. **The same-signer rule** when the running executable is Authenticode-signed: the staged one must pass `WinVerifyTrust` and carry the same publisher (subject and issuer chain), so a certificate renewal still updates. **When the running executable is unsigned** (a3-kb today), `Prepare` refuses unless the app sets `Options.Unsigned` and passes the digest it verified itself from a manifest it trusts; the risk is documented, and signing is the fix.
- **`app/login` on Windows:** the `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` value with `--start-hidden`; `Status` reads `…\Explorer\StartupApproved\Run` so a user's "Disabled" in Task Manager or Settings is reported, not overwritten.
- **Undocumented Windows API** (dark mode for the tray's popup menu through `uxtheme` ordinals 133/135, `SetPreferredAppMode`) sits behind D53's two switches: the `gnui_publicapi` tag removes it, and `PublicAPIOnly` turns it off at run time. The public fallback is a light menu.

**Alternatives:** an MSI or MSIX installer (signing required, admin for MSI, different update model); Squirrel-style updaters (a bundled .NET stub); the Startup folder for login items (no user-disabled state).

### D77. Windows packaging from any host

**Decision.** `gnui package -target windows/amd64,windows/arm64` builds release executables with `-H windowsgui` and links a resource object (`.syso`) we generate in Go: the icon (`RT_GROUP_ICON` from the 1024-px PNG, downscaled to 16–256 px), the manifest (Per-Monitor v2 DPI, `supportedOS` for 10 and 11, `activeCodePage` UTF-8, `longPathAware`, Common Controls 6, `asInvoker`), and `VERSIONINFO` from `gnui.json`. Signing runs an external tool when an identity is configured: `signtool` on Windows, `osslsigncode` or `jsign` elsewhere, with an RFC 3161 timestamp. Output: the executable (which installs itself, D76) and a zip.

**Alternatives:** `rc.exe`/`windres` (a Windows SDK or MinGW on every build machine); `tc-hib/go-winres` (MIT, pure Go; a dependency for about 500 lines of COFF and resource writing we can own).

### D78. How Windows is verified

**Decision.** Three layers: CI on the hosted Windows runners for everything that runs headless or in the runner's session (unit tests, real windows on WARP, probes, benches, allocation gates); a Windows 11 VM with a desktop for what CI cannot see (toasts, the tray, hotkeys from a real keyboard, IME, capture of a real desktop, WebView2 on a client image, timings); and your hand checklist on that VM or a real PC before each app's Windows checkpoint. Windows numbers are labelled by where they come from: runner (WARP, not a budget), VM (indicative), hardware (gated). Details under [How Windows is verified](#how-windows-is-verified).

## Work

Tracks as in phase 6. Each item gives what it builds, per OS, the dependency review (D2), its tests and its gate. Effort is S (a day or two), M (about a week), L (two weeks or more), for one agent. "Opus (high)" marks COM, objc and threading work; "Sonnet" the rest, as phase 6 assigned them.

### Wave 0: contracts and probes (me)

- **Contracts in code** with stubs on every OS, so tracks build against fixed interfaces: `Options.Panel`, `app/hotkey`, `media/capture`, `app/webview`, `fetch.Do` and `Request`, `app/ws`, `term`, `markdown`, `highlight`, `app/install`, the Windows window registry's types, the COM-object table in `internal/com`, and WinRT helpers (`RoGetActivationFactory`, `HSTRING`, `IInspectable`) in `internal/win32`.
- **The probes P1–P10** (below) as tests or `cmd/probe-*` programs, run on CI before the tracks they de-risk start.
- **The Windows VM request** to you (D78).
- *Wave 0 note (2026-10-27):*
  - `internal/win32`'s WinRT helpers and `internal/com`'s object table (`NewKind`) are real, small implementations rather than stubs, because probes P3, P5 and P6 run through them. The COM table has a Windows unit test.
  - The new event sources are `SourceHotkey`, `SourceCapture` and `SourceWebView`.
  - Probes P1-P8 are `cmd/probe-p1` … `cmd/probe-p8`. Each prints one JSON object and exits 0 when it answered. They cross-compile and vet for windows/amd64 and windows/arm64 and have not run. `.github/workflows/probes.yml` runs them on `windows-2025` and `windows-11-arm` when they or `internal/com`/`internal/win32` change, or on demand, and uploads the answers. Their vtable slots and IIDs were written from the SDK headers' layout as remembered; each is marked "verify on first CI run" where it is uncertain.
  - P9 and P10 are `cmd/probe-p9` and `cmd/probe-p10`; their results are under W7 and M1.
  - Size: the contracts add no weight to apps that do not import them. The stripped gallery went from 6,944,256 to 6,945,280 bytes on windows/amd64 (+1 KiB, one PE section alignment step), from 6,304,768 to 6,305,792 on windows/arm64, and from 7,496,786 to 7,496,898 on darwin/arm64.

### Track W: the Windows platform

- **W1. The app object and windows on Windows** (D68). The window registry; the multi-window pump; the hidden app window; windowless `WaitApp`; quit routes and session end; window commands; `EvOpenURLs` from a second instance's arguments that match the app's schemes (`instance.Send` already carries arguments: `app/instance/transport_windows.go`); `AllowSetForegroundWindow` in the secondary; the main thread as an OLE STA; the frame handler inside native modal loops; one shared D3D11 device, renderer and atlas for all windows (D30's macOS split, ported).
  - *Tests (CI, both runners):* two windows with input routed by `HWND`, paced independently, closing one keeps the other drawing; 20 open/close cycles leak no window, surface, texture or host (`platform.ShellObjects`, GDI and USER object counts from `GetGuiResources`); the app runs 10 s with zero windows at 0 presents; a second instance's `notes-dev:x` arrives as `EvOpenURLs`; `ReplyQuit` inside and after `WM_QUERYENDSESSION` (sent to the hidden window by the test).
  - *Gate:* the gallery's and notes' Windows benches pass unchanged with one window, and with two (a new `-windows 2` mode): 0 allocations, idle 0 presents. *Opus (high).* L.
  - *Wave 0 note:* `platform/registry_windows.go` holds the registry's types (`winRegistry`, `appWindow`, `waitSet`) and their bookkeeping, tested on Windows CI; nothing uses them yet. The wait budget fixes `platform.MaxWindows` at 60 on Windows (macOS keeps 64).
- **W2. Panel windows, both OSes** (D69). *macOS:* the `GNUIPanel` class and its options; *Windows:* the styles, display affinity, DWM corners and shadow, hide on deactivate. `app.Config.Panel` and a `Show`/`Hide` pair that keeps the panel's Build state (a hidden panel builds nothing).
  - *Tests:* style and level read back (`styleMask`, `level`, `collectionBehavior`, `sharingType`; `GetWindowLongPtrW`, `GetWindowDisplayAffinity`); a non-activating panel shown while another window is key leaves that window key; `HideOnDeactivate` hides on focus loss; a shown and hidden panel 100 times leaks nothing.
  - *Gate:* a panel shows within 1 frame of `Show` when warm (the palette opens on the frame after the hotkey), measured from `EvHotkey` to present. *Opus (high)* for macOS, *Sonnet* for Windows. M.
  - *Wave 0 note:* the field is a pointer, `app.Config.Panel *platform.Panel` and `platform.Options.Panel` (nil: an ordinary window), so the zero `Panel` is a usable panel. `Open` and `OpenWindow` refuse a panel with `platform.ErrNoPanels` while `platform.PanelsSupported()` is false (everywhere until W2). The Show/Hide pair is the existing `Win.Show`/`Win.Hide`, documented for panels; no new methods. D69's "level floating or statusBar" is one `Floating` bool for now; W2 adds a level only if the recording bar needs the status-bar level.
- **W3. Hosts as child windows** (D66). Clip containers, `DeferWindowPos` batches after `Present`, `WS_CLIPCHILDREN`, the overlay-hides-host rule, focus hand-off (`WM_SETFOCUS` to a child queues `EvHostFocus`; Tab, Shift+Tab, Escape and Enter come back as host commands), pointer routing (our hit regions subtract hosts), DPI changes moving hosts, the offscreen placeholder. `HostView` takes an `HWND`.
  - *Tests:* P2's trailing measurement; a host in a scrolled list is clipped by the list; a drawn popup over a host hides it and shows it again; focus traversal in and out.
  - *Gate:* 0 allocations on frames that move hosts (the shelldemo bench on Windows, `cmd/shelldemo` with a Win32 `EDIT` child as its hosted text field). *Opus (high).* M.
- **W4. The RichEdit editor** (D67), behind `platform.Editor`; `widget/base.NativeEditor` and `rexui.Composer` unchanged above it.
  - *Tests:* the N4 real-editor matrix, ported: keys through `SendInput` where the runner's session allows it (P7), and `WM_CHAR`/`WM_IME_*` sent to the control where not; marked text, undo after typing and after `Replace`, paste of text, an image and files, completion navigation, Enter policies, edge arrows, the byte/UTF-16 mapping over emoji, combining marks and Arabic.
  - *Your hand checklist* (VM or PC, about ten minutes): Microsoft Japanese IME and Pinyin (compose, choose, commit, send); the emoji panel (Win+.); voice typing (Win+H); spell check underline and correction.
  - *Gate:* the matrix passes on both runners; idle with an unfocused editor at 0 presents (a focused RichEdit blinks its own caret: measured and reported, as on macOS). *Opus (high).* L.
- **W5. The tray on Windows.** `tray.New` on `Shell_NotifyIconW` (`NOTIFYICONDATAW` with a GUID, version 4), icon from the app's image, tooltip, left click as `Clicked`, right click (`WM_CONTEXTMENU`) opening the `menu.Model` as a native popup menu (`TrackPopupMenuEx`) with dark mode behind D76's switches; re-added on `TaskbarCreated`; removed on exit and on crash recovery (the GUID re-claims a stale icon). `SetTitle` maps to the tooltip (Windows has no tray text); a3-kb's elapsed time goes in the tooltip and the menu header. When toasts are unavailable (`notify.Setup` reports `ErrNotRegistered`, or Focus Assist suppresses them), `tray.Balloon(title, body)` (`NIF_INFO`) is the fallback, as the C# app does today.
  - *Tests:* `Shell_NotifyIconW` succeeds on the runners (explorer runs there; P7 confirms), menu item ids map to commands, `TaskbarCreated` re-adds; the hand checklist checks it by eye, in light and dark.
  - *Gate:* idle with a tray icon: 0 presents, ≤ 1 wakeup/s. *Sonnet.* M.
  - *Wave 0 note:* `Balloon` is a method, `(*tray.Item).Balloon(title, body)`, because a balloon comes from one icon. On macOS it returns `tray.ErrNoBalloon` (the fallback there is `app/notify`).
- **W6. Toasts** (D71). WinRT activation, toast XML from `Note` (escaped), the `Activated`/`Dismissed`/`Failed` delegates, protocol activation for a stopped app, the D45 response slots, `ToastNotificationHistory` for `delivered` and `remove`. The AUMID registration lives in `app/install` (W10); `notify.Setup` checks it and reports `ErrNotRegistered` with what is missing.
  - *Tests:* the existing fake-centre tests run unchanged (they already run on Windows: `TestEvictionSkipsReplacedNotes` is quarantined there, `docs/QUARANTINE.md`); P6 posts a real toast on `windows-11-arm` and reads it back from the history; the VM checks a click on an action with the app running and with it quit.
  - *Gate:* a click on "Publicar" in a3-kb's toast reaches the app as a response in both states, on the VM. *Opus (high).* M.
- **W7. Global hotkeys, both OSes** (D70). *macOS:* Carbon through ffi; *Windows:* `RegisterHotKey` on the hidden window. Shortcut display strings per OS (⌥⌘K, Ctrl+Alt+K).
  - *Tests:* register, unregister, `ErrTaken` when the shortcut is held (a second registration in the test); delivery driven by a synthetic Carbon event (`SendEventToEventTarget`, no permission needed) on macOS, and by `SendInput` on Windows where the session allows (P7).
  - *Gate:* hotkey to palette on glass ≤ 50 ms warm on the MacBook and the VM. *Sonnet.* S–M.
  - *Wave 0 note:* a press is an `EvService` from the new `platform.SourceHotkey`, decoded by `hotkey.Pressed(e)`, as `tray.Clicked` decodes a click, not a new `EventKind`. The signature is `hotkey.Register(id, Shortcut)`, and `hotkey.Shortcut` is an alias of `menu.Shortcut`, so `AppendLabel` gives ⌥⌘K and Ctrl+Alt+K. `ErrInvalid` refuses a chord without a modifier other than Shift. **P10 ran on the Mac mini (2026-10-27):** a `kEventHotKeyPressed` handler installed on `GetApplicationEventTarget` through the ffi callback table was reached by 1,001 of 1,001 synthetic events sent with `SendEventToEventTarget`, with the `EventHotKeyID` read back each time. Each press took 0 Go allocations and about 136 ns. A second `RegisterEventHotKey` of the same chord in the same process returned `eventHotKeyExistsErr` (-9878), which is `ErrTaken`'s source on macOS. Whether a chord held by another process also fails is for W7's test.
- **W8. File dialogs and drag and drop on Windows.** `OpenPanel`/`SavePanel` on `IFileOpenDialog`/`IFileSaveDialog` (filters, multiple selection, folders, a default folder and name), run modally to their window with the frame handler drawing; `CancelDialog` closes them. Drop in: an `IDropTarget` per window (`RegisterDragDrop`) with `CF_HDROP` files, `CF_UNICODETEXT`, PNG and DIB images, hover positions to `ui` drop targets, the same `EvDrag*` events as macOS. Drag out: `DoDragDrop` with an `IDataObject` of file paths (downloads), modal with the frame handler.
  - *Tests:* the dialog opened and cancelled by the test (`IFileDialog::Close` from a timer) returns its ticket once; a drop simulated through our `IDropTarget` methods; a drag out to our own window. The VM checks a drop from Explorer.
  - *Gate:* 20 dialogs and 20 drops leak no COM object. *Sonnet* (dialogs), *Opus (high)* (OLE drag). M.
- **W9. Rex window chrome on Windows.** A drawn titlebar in the Rex language: `WM_NCCALCSIZE` removes the caption, `WM_NCHITTEST` reports the caption, borders and the three caption buttons (`HTMINBUTTON`, `HTMAXBUTTON`, `HTCLOSE`), so Windows 11's snap layouts flyout appears over our maximize button; the buttons drawn to Windows 11's metrics (46×32 at 100 %) in light and dark; the Rex tabs and toolbar in the band. `app.Config.Titlebar` applies on Windows too.
  - *Tests:* hit-test results per region at 100 %, 150 % and 200 %; maximize and restore keep the client area right (the maximized-window border inset); a GnuiShot-style capture against a reference of the system caption metrics.
  - *Gate:* your eye at the a3-kb Windows checkpoint. *Sonnet.* M.
- **W10. `app/install` and URL schemes** (D76). Install, uninstall, registration of schemes and the AUMID, the shortcut with its AUMID, `Installed`, refreshing the scheme's command line on each launch so it follows the executable (as a3-kb does: `Program.cs` `RegisterProtocol`).
  - *Tests:* install into a temporary `%LOCALAPPDATA%` override and uninstall, leaving no key or file (registry read back); an `a3kb-test:` link opened with `ShellExecuteExW` starts the installed copy, which hands it to the running one.
  - *Gate:* the round trip on both runners. *Sonnet.* M.
  - *Wave 0 note:* `Uninstall` takes the `Options` (it needs the ID). `install.ErrNotNeeded` (macOS) deliberately does not match `errors.ErrUnsupported`: the app has nothing to do there. `UninstallArg` is `--uninstall`.

### Track H: the hosted browser view

- **H1. `app/webview` on macOS** (D64). A `WKWebView` host (`HostView` with the view): `WKWebViewConfiguration` with `nonPersistentDataStore` (or `dataStoreForIdentifier:`, macOS 14, for a named profile); a navigation delegate (`decidePolicyForNavigationAction:decisionHandler:` with the kept block, D36; scheme allow-list; `navigationAction:didBecomeDownload:`), a UI delegate (new windows loaded in place or refused; `requestMediaCapturePermissionForOrigin:` denied unless allowed), KVO observers for `title`, `URL`, `estimatedProgress`, `loading`, `canGoBack`; `WKDownloadDelegate` writing to the app's chosen path with progress; `isInspectable` as an option. WebKit loads on the first `New`.
  - *Tests (macOS CI and the Mac mini):* a loopback page: navigate, title and URL events, back and forward, a blocked scheme, a refused popup, a download of 10 MB with progress, a camera request denied, a non-persistent store leaving no cookie after `Close` (a new view does not see it).
  - *Gate:* creating the first view does not touch the first frame (it is never on the startup path), and an idle view adds 0 presents to our process; the WebContent process's own CPU and memory are reported, not budgeted. *Opus (high).* M.
  - *Wave 0 note:* (H1 and H2) the policy is declared in `webview.Options` and applied by the view without a round trip to the app: `Schemes` (default http, https, about, data, blob), `NewWindows`, `Allow` (a `Permission` bit set, denied by default), `DownloadDir` ("" refuses), `Profile` ("" non-persistent; a plain name is a persistent profile), `NoContextMenu`, `Inspectable` and `ApplicationName`. Events are an `EvService` from the new `platform.SourceWebView`, decoded by `webview.EventOf`. The view takes a `*platform.Window`, as `NewComposer` does, and exposes its `HostID` for `ui.Context.Host`. `ErrUnavailable` and `RuntimeURL` cover a missing WebView2 runtime. There is no script API.
- **H2. `app/webview` on Windows** (D65). The loader; `ICoreWebView2Environment` with options (`AdditionalBrowserArguments` empty; `Language`); the controller as a child of a W3 container (`ICoreWebView2Controller.put_Bounds`, `put_IsVisible`, `MoveFocus`, `NotifyParentWindowPositionChanged`); InPrivate mode for non-persistent sessions (`ICoreWebView2ControllerOptions`, `CreateCoreWebView2ControllerWithOptions`) or a named profile; events: `NavigationStarting` (policy), `NavigationCompleted`, `SourceChanged`, `DocumentTitleChanged`, `HistoryChanged`, `NewWindowRequested` (load in place or refuse), `PermissionRequested` (deny by default), `DownloadStarting` (path, progress, cancel), `ProcessFailed` (report and recreate). The user data folder in the app's cache folder. `TrySuspend` when hidden.
  - *Dependency review:* WebView2 headers are read for vtable order (the Windows SDK / NuGet `WebView2.h`, cited per interface as D7 does); nothing copied.
  - *Tests:* P3 on both runners, then the same matrix as H1 where the runtime is installed (skipped with a reason where it is not).
  - *Gate:* as H1; a hidden view suspended within 1 s. *Opus (high).* L.

### Track M: capture

On both OSes, the app's own audio is excluded from the system-audio track (`excludesCurrentProcessAudio`; on Windows, process-loopback exclusion where the build has it, otherwise reported). Failures are typed (`ErrPermissionDenied`, `ErrPickerCancelled`, `ErrNoEncoder`, `ErrDiskFull`) so a3-kb can show the right pt-BR message.

- **M1. `media/capture` on macOS 15+** (D72). The picker and its observer, the stream and recording output, pause by segments, the passthrough join on a worker, `Level` from the microphone through the stream's audio output only if a3-kb shows a meter (otherwise none, to keep Go off SCK's queues); `app/perm` for the microphone first.
  - *Tests:* the join of three generated MP4 segments (made with `AVAssetWriter` in the test) gives one file of the summed duration with video and audio tracks; a live test on the Mac mini or MacBook, with a `gnui run` bundle you grant once, records 5 s of a window through `SCContentFilter` and checks the file's duration, size and tracks.
  - *Gate:* while recording, our process's CPU ≤ 3 % of one core at 1080p30 (the encoder is the system's), 0 Go allocations per second outside events. *Opus (high).* M.
  - *Wave 0 note:* (M1 and M2) `capture.Pick(win, Options)` returns a `*Recording` rather than a `Ticket`: the recording exists from the picker on, and `Started` and `Ended` (D72's `EvCaptureEnded`) are `EvService`s from the new `platform.SourceCapture` carrying its ID. `Recording.Result()` carries the path, the duration and the typed error, with `ErrBusy` and `ErrTargetGone` added to the preamble's four. The app's own audio is excluded unless `Options.IncludeOwnAudio` is set; `Recording.OwnAudioExcluded()` reports what the OS did. **P9 is written but not run** (`cmd/probe-p9`): the Screen Recording grant is missing. TCC keys the grant to the bundle id, `dev.gonativeui.probe.p9.dev`, signed by gnui-dev, not to the identity alone, so M1's live test bundle will need its own one-time grant. To run it: `cd cmd/probe-p9 && go run ../gnui run -- -request` once (or grant "GNUI Probe P9" in System Settings → Privacy & Security → Screen & System Audio Recording), then `go run ../gnui run`. `-preflight` checks the grant without prompting.
- **M2. `media/capture` on Windows** (D72). The WGC picker (`IInitializeWithWindow` on the panel's owner) and the monitor/window items, the frame pool, the video processor to NV12, WASAPI mic and loopback with mixing and a 200 ms buffer, the sink writer with hardware transforms, pause by timestamp offset, `ExcludeFromCapture` for the recording bar.
  - *Tests:* P4; a 5 s capture of our own window on both runners (where Media Foundation exists: Windows Server images may lack it without the Media Foundation feature, so `windows-11-arm` is the reference runner), decoded back with the MF source reader for duration, frame count within 5 %, and an audio track; a pause and resume leave a gap-free timeline.
  - *Gate:* while recording 1080p30 on the VM and on hardware, our process's CPU ≤ 10 % of one core with a hardware encoder, ≤ 35 % with the software one (reported); 0 Go allocations per frame in steady state; the recording bar absent from the file. *Opus (high).* L.

### Track N: network

- **N1. `fetch.Do`** (D73). The request type and streaming bodies on `NSURLSession` (an upload task from a bound stream, `didSendBodyData:` for progress); WinHTTP on Windows for every call (`WinHttpOpen` with automatic proxy, `WinHttpSendRequest`/`WriteData`/`ReceiveResponse`/`ReadData`, redirects, `Accept-Encoding: identity` as D60); `net/http` on Linux.
  - *Tests:* D60's loopback suite on each OS, plus methods, a 1 GB streamed upload with progress (generated, not stored), chunked request bodies, a streamed response read line by line, cancel during upload; `go list -deps` of a program that imports `app/fetch` shows no `net/http` or `crypto/tls` on darwin and windows (a CI check).
  - *Gate:* the size check; a hello world with `fetch.Do` ≤ +0.8 MB over hello world on windows/amd64. *Sonnet* (API, Linux), *Opus (high)* (WinHTTP). M.
  - *Wave 0 note:* `Options` gains `UploadProgress` and `AnyStatus`: an API's 4xx carries a JSON body the app reads, so `Do` can deliver it instead of a `*StatusError`. `Response` gains `Headers`. Until N1, `Do` serves a plain GET through `Get` and refuses everything else with `ErrUnsupportedRequest`, which matches `errors.ErrUnsupported`. `TestNoNetHTTP` (`app/fetch/deps_test.go`) runs `go list -deps` for darwin/arm64, windows/amd64 and windows/arm64 over `app/fetch`, `app/ws`, `app/hotkey`, `app/install`, `app/webview` and `media/capture`. It allows exactly one exception, `app/fetch` on Windows until N1, and fails once that exception is no longer needed.
- **N2. `app/ws`** (D73). The three backends; read into the caller's buffer; write with backpressure; close codes; timeouts; the same proxy and TLS as `fetch`.
  - *Tests:* a loopback WebSocket server in the test (our own Linux framing serves as the test server on every OS): text and binary, 32 MiB messages (the agent's `maxPayload`), fragmented frames, ping, server close, abrupt drop, cancel, 1,000 messages a second for 10 s with 0 Go allocations per message on the receive path.
  - *Gate:* the allocation check and the size check. *Opus (high).* M.
  - *Wave 0 note:* `ReadMessage(buf)` returns `(MessageKind, []byte, error)`, appending to `buf[:0]` and growing it only for a larger message. `app/ws` does not import `app/fetch`, which links net/http on Windows until N1, so wave 0 has no explicit proxy option. The system proxy applies, and N2 decides whether `fetch.Proxy` moves to a shared package.

### Track C: content

- **C1. `term` and `rexui.Terminal`** (D74). Parser, screen, scrollback (5,000 lines default, a byte budget), reflow, input encoding, selection and copy (`ui.SelectionScope` over rows), find, hyperlinks, title; the draw path: one shaped run per row segment of one style, cached by content hash and invalidated by damage tracking, backgrounds and cursor as quads, `ui.NoFontSmoothing` as rexlook's panes.
  - *Tests:* parser tables against the state diagram; screen fixtures recorded from xterm behaviour for the sequences `vim`, `htop`, `less`, `git log`, `tmux` and `claude`'s TUI emit (recorded with `script`, replayed byte by byte and in random chunks: the screen must be identical); fuzzed input never panics or grows past its budgets; reflow keeps content.
  - *Gate:* parsing ≥ 100 MB/s on the MacBook with 0 allocations per byte; a full 200×60 redraw ≤ 2 ms of CPU; `cat` of 50 MB keeps frames paced (rendering coalesced to the frame rate); 5,000 lines of 120 columns ≤ 6 MB. *Opus (high).* L.
  - *Wave 0 note:* `term` imports nothing from `ui` and has its own `Key` and `Mods` types, which `rexui.Terminal` maps. `Cell` is 8 bytes (tested). `Write` returns `term.ErrUnsupported` until C1.
- **C2. `markdown`** (D75), incremental, with spans for `rexui.Markdown` (headings, lists with markers, tables as a grid, block quotes, code blocks via C3, links as link spans).
  - *Tests:* the CommonMark spec examples for supported constructs; GFM tables and task lists; a 20 KB message streamed in 50-byte chunks re-parses only its open block (counted).
  - *Gate:* parse ≤ 1 µs per byte on a worker; 0 allocations per frame for a transcript of 1,000 rendered messages in a `VList`. *Sonnet.* M.
  - *Wave 0 note:* the spans are `ui.Span` with offsets relative to the block's body (`Doc.Body`). `Span.Style` is a bit set (`Emph`, `Strong`, `CodeSpan`, `Strike`; `NumStyles` = 16 combinations), and a link is `ui.SpanLink` with `Tag` indexing `Doc.Links`. The stub is D75's raw-text fallback: the source is one plain open paragraph, and storage is reused (0 allocations on a reparse of the same size).
- **C3. `highlight`** (D75) and `rexui.CodeBlock` (mono spans, a language label, copy, horizontal scroll for long lines). For a3-app's Files and Changes panes (its AF6), the block also takes an optional line-number gutter, a highlighted line, and added/removed/context line styles for diffs.
  - *Tests:* golden token streams per language; pathological inputs (unterminated strings, 1 MB lines) stay linear.
  - *Gate:* ≥ 20 MB/s per core. *Sonnet.* M.
  - *Wave 0 note:* `highlight.Line(lang, state, line, dst)` works line by line; `LangOf` takes fence names and file names, and `Supported` reports which languages have a lexer. Until C3 every language is Plain, one `Text` token per line. The mapping from roles to Rex code colours belongs to `rexui.CodeBlock`.
- **C4. `rexui.TextArea`**: a general multi-line field (settings, bot instructions, a3-kb's quick-add) on `platform.Editor`, sharing the composer's host but with no send policy: a fixed or growing line range, placeholder, read-only. On macOS it can start once wave 1 lands; on Windows it waits for W4. Requested by a3-app (AF4) and a3-kb. *Sonnet.* S.
  - *Wave 0 note:* `NewTextArea(w, TextAreaSetup)` and `TextArea(k, label, st, TextAreaOpts)` exist. Until C4 the field is the composer's drawn single-line fallback, with read-only shown disabled.
- **C5. `rexui.ContextMenu`**: one call that uses `OnContextMenu` natively on macOS and opens `Kit.OpenContextMenu` drawn elsewhere, so apps write one path (a3-app AF13). *Sonnet.* S.
  - *Wave 0 note:* `rexui.ContextMenu(k, id, build)` takes `OnContextMenu`'s arguments. Until C5 adds the drawn path it is `OnContextMenu`.
- **C6. WebP decoding off macOS**: `img` decodes WebP on Windows and Linux with `golang.org/x/image/webp` (pure Go, BSD-3; `x/image` is already in the module graph, so this needs a D2 row, not a new dependency). It covers a3-app's attachments, file previews and bot avatars (AF14). *Sonnet.* S.

### Track S: services

- **S1. `app/dock` on Windows and Dock progress.** `SetProgress(fraction | indeterminate | none)` on both (`NSDockTile` with a drawn progress view; `ITaskbarList3::SetProgressValue`/`SetProgressState`), the badge as a taskbar overlay icon (drawn digits), `Bounce` as `FlashWindowEx`. *Sonnet.* S.
- **S2. Large secrets on Windows.** Values over Credential Manager's 2,560 bytes are sealed with `CryptProtectData` (user scope) into a file in the app's data folder, with the credential holding its name; `Get`, `Set` and `Delete` are unchanged. Only if an app plan needs it (a3-app's agent link and token fit today). *Sonnet.* S.
- **S3. `app/login` on Windows** (D76): the `Run` value (its name set by the app, so a3-kb keeps its existing `HyperPedia` value and the user's choice carries over), `StartupApproved`, `--start-hidden`; the VM checks one real sign-in. *Sonnet.* S.
- **S4. `app/selfupdate` on Windows** (D76): `Supported` (installed per user, writable, not a development build), `Prepare` (the PE checks: machine type matches, the version resource is newer, Authenticode same signer or `Options.Unsigned` with the app's digest), the helper, the rename swap, the health marker, rollback, `LastResult`. Mark-of-the-Web is not added to a file the app downloads itself, so SmartScreen does not prompt on an update.
  - *Tests:* the D61 matrix ported, with test executables signed by a throwaway certificate made in the test (`New-SelfSignedCertificate` on the runner): a good update, a hang rolled back, another signer refused, older refused, unsigned refused without the option.
  - *Gate:* the matrix on both runners. *Opus (high).* M.

### Track T: tooling and verification

- **T1. `gnui package -target windows`** (D77): the `.syso` writer, `-H windowsgui`, signing hooks, the zip; `gnui.json` gains `windows` (publisher, AUMID override) fields. *Tests:* the resources read back from the built file (`FindResourceW` on Windows, our PE reader elsewhere); the manifest's DPI setting effective (`GetAwarenessFromDpiAwarenessContext`). *Sonnet.* M.
- **T2. The agent bridge on Windows.** `inspectwire.Listen` on a named pipe with a user-only DACL (as `app/instance`'s pipe is), so `gnui inspect` drives an app on the VM over SSH: `tree`, `do`, and `shot` (a WGC capture of the window, M2's code). *Sonnet.* S–M.
- **T3. `tools/winvm`**: cross-compile on the Mac mini, copy to the VM over SSH, run a test or an app with the bridge, fetch results and captures; `docs/measurements/phase8-windows.md` and `phase8-macos.md` filled from it. *Sonnet.* S.

## Budgets

The framework's bar (ARCHITECTURE §11, D21, D27), applied to what these apps do. "Gated" means a CI or checkpoint failure; "reported" means measured and written down. On Windows, runner numbers are never budgets (WARP, shared hosts); VM numbers are indicative; hardware numbers are gated.

| Metric | Budget | Where measured |
|---|---|---|
| Startup, a tray app | the tray or status icon set ≤ 100 ms after exec, warm; no window before it is asked for | MacBook; VM (reported), hardware (gated) |
| Startup, a windowed app | ≤ 150 ms exec → first complete frame, warm (D21); content-first on Windows through DWM cloaking (`platform/dwm_windows.go`) as today | MacBook; Windows by DXGI frame statistics |
| Hotkey → panel on glass | ≤ 50 ms warm (W2, W7) | MacBook, VM |
| Idle | 0 presents, ≤ 1 wakeup/s, 0 CPU growth over 60 s, with a tray icon, two hotkeys, a hidden palette, an open WebSocket and an idle hosted view (hidden WebView2 suspended) | MacBook; runners (presents only); VM |
| Allocations | 0 per steady-state frame, including host moves, terminal output, streaming markdown, a WebSocket receiving, a capture running | CI on macOS and both Windows runners |
| Memory, a3-kb idle (no window) | `phys_footprint` ≤ 30 MB on macOS; private working set ≤ 30 MB on Windows | MacBook; VM |
| Memory, a3-app idle (one window, a 500-message transcript, a terminal) | ≤ 120 MB in our process; the browser's processes reported separately | MacBook; VM |
| Capture CPU (our process, 1080p30) | ≤ 3 % of a core on macOS; ≤ 10 % on Windows with a hardware encoder | MacBook; VM and hardware |
| Terminal | ≥ 100 MB/s parse, 0 allocations per byte, ≤ 2 ms full redraw, ≤ 6 MB for 5,000 × 120 scrollback | MacBook, runners |
| Binary, stripped, per architecture | < 10 MB for each app (the CI gate), ≤ 8 MB target; no `net/http` or `crypto/tls` on darwin or windows (`go list -deps` check) | CI cross job |

The tray budgets are new: a tray app's user-visible start is its icon, and both apps' Windows predecessors are a 30–60 MB self-extracting .NET executable (`A3KB.csproj`: `SelfContained`, `IncludeAllContentForSelfExtract`) and Electron.

## How Windows is verified

**What exists.** CI's `windows-2025` (amd64) and `windows-11-arm` jobs run the full `go test`, real-window benches with allocation gates on WARP, and offscreen PNGs (`.github/workflows/ci.yml:323-410`); five tests are quarantined there (`docs/QUARANTINE.md`). The cross job enforces the 10 MiB size gate (`ci.yml:38`, `MAX_BINARY_BYTES`). Nobody has run one of our binaries on a Windows desktop: there is no Windows machine (ARCHITECTURE §13), and the agent bridge refuses Windows (`internal/inspectwire/listen_windows.go:11`).

**The probes** (wave 0, CI unless noted). Each answers one question that would change a decision:

| Probe | Question | Decides |
|---|---|---|
| P1 | What does `OleInitialize` on the main thread cost at startup, and does the hidden app window get `WM_QUERYENDSESSION` (sent by the test) and survive `WM_ENDSESSION`? | W1, D68 |
| P2 | A child window over a flip-model swap chain: correct z-order, clipping by a container, and how many frames a moved child trails a scrolled list (captured with WGC frame timestamps) | D66's fallback |
| P3 | Is the WebView2 runtime on the runners? Does the internal export create an environment and navigate to a loopback page on amd64 and arm64? | D65 option 1 or 2 |
| P4 | WGC capture of our own window and an MF sink-writer MP4 on each runner: does Media Foundation exist on `windows-2025`? Hardware or software encoder? | M2, the reference runner |
| P5 | Hosted RichEdit: colour emoji, IME messages, undo grouping through TOM, `EN_REQUESTRESIZE` heights | D67 |
| P6 | A toast posted with the registry AUMID only, and with the shortcut, read back from `ToastNotificationHistory` (`windows-11-arm`; the VM for Windows 10) | D71's registration |
| P7 | Does `SendInput` reach our windows in the runner's session? Do `Shell_NotifyIconW` and `RegisterHotKey` succeed there? | how W4, W5, W7 are tested |
| P8 | WinHTTP WebSocket and streamed uploads against a loopback server; system proxy read | N1, N2 |
| P9 | macOS: `SCRecordingOutput` driven through `internal/objc` with a window filter, in a `gnui run` bundle on the Mac mini (needs your one-time Screen Recording grant for the `gnui-dev` identity) | M1 |
| P10 | macOS: a Carbon hotkey handler reached through the ffi callback table, driven by a synthetic event | W7 |

**A Windows VM with a desktop** (needs you). The a3-box (Ryzen 9, Ubuntu 26.04, Incus, on Tailscale) can host a Windows 11 x64 VM under Incus (KVM): 4 vCPUs, 8 GB, 80 GB on its BTRFS pool, RDP and OpenSSH over Tailscale. Windows 11 Enterprise's 90-day evaluation is enough to start; a licence is needed after that. It gives a real interactive session (toasts, the tray, Explorer drops, an IME, a microphone through RDP audio redirection) with a virtual GPU, so timings there are indicative only. Agents use it over SSH with T2's bridge and T3's script; it is **not** registered as a GitHub runner, because the repository is public and a self-hosted runner would run pull requests' code.
- *Optional:* a Windows 11 arm64 VM in UTM on the MacBook, for arm64 desktop checks (the Mac mini's disk is tight).
- *Hardware:* gated Windows numbers (startup, idle wakeups, capture CPU, a hardware H.264 encoder, a real GPU) need a physical Windows PC for an afternoon per app checkpoint: yours or an A3 Lab member's.

**Measurements** go in `docs/measurements/phase8-windows.md` (runner, VM and hardware columns, each labelled) and `phase8-macos.md`.

## Order of work

```
wave 0   contracts + stubs, probes P1–P10, VM request (me)
wave 1   macOS for a3-kb:  W2 panels(mac)  W7 hotkeys(mac)  N1 fetch.Do(mac)  M1 capture(mac)
         pure Go:           C1 term  C2 markdown  C3 highlight  C5 context menu  C6 WebP
         macOS editor:      C4 TextArea(mac)
         Windows base:      W1 app object + windows   N1/N2 WinHTTP   T1 packaging   T2 bridge
wave 2   macOS for a3-app:  N2 ws(mac)  H1 webview(mac)
         Windows for a3-kb: W2 panels(win)  W5 tray  W6 toasts  W7 hotkeys(win)  W10 install  S3 login  S4 selfupdate  W9 chrome
         Windows hosts:     W3 child-window hosts (after W1)
wave 3   W4 RichEdit editor (after W3) + C4 TextArea(win)   M2 capture(win) (after W1)   W8 dialogs + drag   S1 dock
wave 4   H2 WebView2 (after W3)   S2 (only if asked)
```

**What unblocks each app first:**
- **a3-kb on macOS** starts after wave 1. Everything else it needs exists today: tray, notifications with actions, Keychain, login items, self-update (D61), URL schemes, the composer for quick-add, a drawn search field. Its checkpoint can come before any Windows work lands.
- **a3-kb on Windows** needs W1, W2, W4 (quick-add), W5, W6, W7, W9, W10, M2, S3, S4 and T1: waves 1–3. Without W4 its quick-add box is one line until wave 3; without M2 it ships without recording until wave 3. Its plan decides whether to ship a Windows build in between.
- **a3-app on macOS** needs N1, N2, H1, C1–C3: waves 1–2. Multiple windows, notifications, dialogs and drop exist.
- **a3-app on Windows** needs W1, W3, W4, W8, W9, H2, N1, N2, S1 on top: waves 1–4. It is last because WebView2 is the longest chain (W1 → W3 → H2).

**Checkpoints for you:** K1 a3-kb on macOS; K2 a3-kb on Windows on the VM (and an afternoon on hardware); K3 a3-app on macOS; K4 a3-app on Windows. Each is an app-plan milestone; the framework's part of each is its exit criteria below holding for the items that app uses.

**Size of the phase.** About 5 L, 15 M and 6 S or S–M items: roughly the scale of phase 6's tracks N, S and T together. With four agents in parallel, waves 1–4 take four to six weeks, the VM and your checkpoints aside.

## Exit criteria (macOS on the MacBook; Windows in CI and on the VM)

1. **Windows windows:** two windows routed and paced independently; 20 open/close cycles leak no native object; the app runs with zero windows at 0 presents; a second instance's URL arrives as `EvOpenURLs`; a quit refused and then allowed, including at sign-out on the VM.
2. **Panels and hotkeys on both OSes:** the palette opens from its hotkey on the active Space / desktop, takes the keyboard, hides on deactivation; the recording bar never takes focus and is absent from a capture; hotkey to glass ≤ 50 ms warm.
3. **Services on Windows:** tray icon and menu in light and dark; a toast's action reaches the app running and quit; file dialogs; a drop from Explorer; Credential Manager; login item with the user's disabled state read; install, uninstall and scheme round trip; self-update good, rolled back and refused (signed and unsigned paths); taskbar progress.
4. **Hosts:** child-window hosts clipped and hidden under overlays, with P2's trailing measured and the fallback applied if visible; the RichEdit matrix passes and your IME checklist is reported.
5. **Web:** `WKWebView` and WebView2 pass H1's matrix (a non-persistent session, a named persistent profile that keeps a cookie across a restart and is isolated from the app, denied permissions, downloads, blocked schemes, popups in place); a missing runtime gives the fallback.
6. **Capture:** a 5 s recording with microphone and system audio on both OSes, with pause and resume, decodes back to the right duration and tracks.
7. **Network:** `fetch.Do` and `app/ws` pass their suites on all three OSes; no `net/http` or `crypto/tls` in darwin or windows binaries that use them.
8. **Content:** `term` replays the recorded TUI sessions identically in any chunking; `markdown` passes its CommonMark subset; streaming re-parses only the open block.
9. **Performance:** the budgets table holds where it says gated; every Windows number carries its source.
10. **CI:** all jobs green, including Linux with the new APIs returning `ErrUnsupported`; quarantine additions each have an issue.
11. **Docs:** every exported identifier documented, `Example`s for new packages, guide chapters for panels, hotkeys, capture, the webview, `fetch`/`ws`, the terminal and Windows packaging.

## What needs you

**Adam's decisions (2026-10-27):** D64 approved (hosting the OS browser view as content; WebView2 without the bundled loader, as D65 option 1). No Intel Macs on the team, so arm64 only and no Swift/Electron bridge release. Signing by bought certificates: an Apple Developer ID (A3 Lab) and Azure Trusted Signing for Windows, which Adam enrolls; until they exist, builds stay ad-hoc/unsigned and are not released. No Windows VM yet: Windows work is verified on the CI runners only, and checkpoints K2 and K4 wait for the VM decision.

- **D64** (amending D2: hosting the OS browser view as content). Without it a3-app has no browser pane and opens pages in the system browser.
- **D65's fallback** (bundling `WebView2Loader.dll`) only if P3 or a later runtime breaks our loader. Not needed to start.
- **D63** (Linux waits) and the rest of D66–D78, at review.
- **The Windows VM** on the a3-box, and a Windows licence after the 90-day evaluation.
- **A Windows PC for an afternoon** per Windows checkpoint, for gated numbers and the IME, voice typing and microphone checks.
- **A Windows code-signing identity** (an OV certificate, or Azure Trusted Signing): SmartScreen reputation for first downloads, and the same-signer update rule (D76). Until then both apps update on the unsigned path.
- **The Apple Developer ID** still missing since phase 6: notarisation, and D61's same-signer updates for real releases of both apps.
- **One-time grants** on the Mac mini for the `gnui-dev` identity: Screen Recording and Microphone (P9, M1).

## Risks

| Risk | Impact | Mitigation |
|---|---|---|
| WebView2's internal export changes (Microsoft says it may) | a3-app's browser pane fails on an Edge update | Detect and fall back to the system browser; D65 option 2 ready to approve; P3 rerun in CI weekly against the runner's runtime |
| A child window trails a scrolling list (D66) | a visible lag under scroll | P2 measures; hide hosts during momentum; DirectComposition later |
| RichEdit looks unlike Rex (GDI-era rendering, solid background) | the composer is the one non-Rex control on Windows | P5 first; windowless RichEdit (D67's refinement) if your eye says so at K2 |
| No UI Automation for drawn UI | Narrator users cannot use either app on Windows (the C# app had WinForms' providers) | Stated now; the UIA bridge as the next Windows item; RichEdit and WebView2 are already accessible |
| Media Foundation missing on Windows Server runners | M2 untested on amd64 CI | `windows-11-arm` and the VM as the reference; P4 decides |
| Undocumented `uxtheme` ordinals for dark menus | a light tray menu in a future Windows | D76's switches; the public path is light |
| Capture needs macOS 15 | a3-kb on macOS 14 records nothing | a3-kb already requires 15 (`Info.plist` `LSMinimumSystemVersion`); `ErrUnsupported` on 14 |
| Intel Mac users of either app | lose support (D55) | each app's plan states it before release |
| D-number collision with `p8-rex` | renumbering at merge | numbers fixed when this plan is approved |
| The phase's size | four to six weeks before a3-app on Windows | the order above ships a3-kb on macOS first and Windows next |

## Open questions

1. ~~a3-app's browser session~~ **Settled at reconciliation (2026-10-27):** a3-app keeps parity with Electron's `persist:a3-browser` and uses a named persistent profile, isolated from the app. The framework default stays non-persistent.
2. **Windows 10:** a3-kb's .NET updater will push the Go exe to every installed copy, Windows 10 ones included, so dropping 10 needs a manifest gate there. support it (toasts, capture and WebView2 differ there; corners are square), or Windows 11 only? The framework's floor is Windows 10 (D1); a3-kb's C# app targets 10 2004+.
3. **Linux** after this phase, or UIA first?
4. **Signing on Windows:** an OV certificate or Azure Trusted Signing, and under which entity (A3 Lab)?
5. **The VM:** may agents create it on the a3-box and use the evaluation licence?
