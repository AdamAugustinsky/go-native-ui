# Phase 6 plan: native on macOS, and the app layer

Drafted 2026-10-18, for your approval. It answers four requests from that day:

- "the RexLook app has a very Mac OS native feel, but the other apps we did with the same theme do not … we probably should also do like shadcn-style library of reusable components/design-system out of the rex-look thing";
- "Research popular desktop frameworks and see what infrastrucutre, features, tools, utilities, etc, that they have, that we don't yet have, and that we should … and implement those";
- "it might also be interesting to use actual macos components in a couple places, like ghostty/rex do";
- "IMPORTANT: specially on macos, apps should feel native and fast."

The fourth, ZapFast, is phase 7 (D33). It starts at the same time, because its Rust ABI work does not need anything from this phase: [zapfast-gnui `docs/GNUI-MIGRATION-PLAN.md`](https://github.com/AdamAugustinsky/zapfast-gnui/blob/gnui/docs/GNUI-MIGRATION-PLAN.md) (private).

**What phase 6 does:**
- **Makes Rex a design system.** Today `theme/rex` holds only rexlook's chrome; every control was designed from generic tokens and reads as a web look against AppKit (`docs/design/native-audit.md`, "The short answer" and problems 3–10). The macOS measurements become the token scale, and the components are rebuilt shadcn-style: headless behaviour, then one styled file per component (`docs/design/rex-design-system.md`, option B).
- **Fixes what gives us away on a Mac.** Text at the wrong optical size (audit problem 1), drawn menus (problem 2), no scroll phases or rubber-banding (`gpui-ghostty.md` §4.2), no app object.
- **Uses AppKit where the OS does more than drawing:** menus, alerts, panels, tooltips, materials, the chat composer's `NSTextView`, accessibility (`gpui-ghostty.md` §1, "The common pattern").
- **Adds the app layer the research found missing in every report:** the application lifecycle, more than one window, rich and selectable text, variable-height lists, images, notifications and the other OS services, packaging and signing, and developer tooling (`zapfast.md` §2, `egui.md` §1, `electron-tauri.md` §1, `go-and-others.md` §5).

**macOS first (D34).** Windows and Linux keep compiling, keep all 8 CI jobs green, and keep everything they do today. The new APIs exist there and return `ErrUnsupported` (or, for menus and dialogs, keep the drawn versions). Their parity is a later phase. Windows and Linux numbers come only from CI.

**Not in this phase:**
- Windows and Linux parity for anything below (D34);
- a drawn multi-line editor: on macOS the composer is a hosted `NSTextView` (N4); the drawn one comes with the Windows and Linux phase;
- the other items under [Gaps](#gaps-what-the-research-found-and-this-phase-leaves-out), each with its reason;
- the final name, the licence, and any promise that the API is stable.

The plan is to be reviewed independently before any code, as phase 5's was.

## Packages

The D28 layering, extended (D40). Each package imports only the ones above it:

| Package | What it is | Status |
|---|---|---|
| `core`, `gpu`, `text`, `platform` | as now. `gpu` gains texture freeing and external (CVPixelBuffer) textures; `platform` gains the application object, hosts, menus, dialogs, keys, scroll phases, system colours | changed |
| `clib` | **public:** load a native library and call plain C functions without cgo (D35). For apps; the framework itself still loads only system libraries | new |
| `ui` | plus host regions, tooltip and material regions, rich-text spans, selection scopes, scroll physics and latching | changed |
| `menu` | plus standard roles and item icons; matching by character on macOS | changed |
| `theme` | tokens v2: indexed colour roles with a source, text styles, control sizes, motion, materials (D39) | changed |
| `theme/rex` | the Rex identity (field, panes, capsule, terminal palette) plus Rex values for every role | changed |
| `icon` | SF Symbol weights and scales matched to text styles | changed |
| `widget/base` | **headless primitives and the `Kit`**: behaviour, focus, semantics and visual state; draws only through helpers (D40) | new |
| `rexui` | **the styled component library**, one file per component, tokens only, copyable into an app (D40) | new |
| `widget` | the D28 entry point kept: `widget.Kit` embeds `*base.Kit`, and today's calls (`k.Button`, `k.Table` …) become thin wrappers over `rexui` defaults | changed |
| `img` | image decoding (ImageIO on macOS), the texture cache, animation (D44) | new |
| `qr` | a QR encoder (our own) | new |
| `task` | background work (the 5B contract, plus keyed declarations and timers) | new |
| `media` | AVFoundation audio playback and recording, video into Metal (D46) | new |
| `app` | plus the application object (`app.Main`, `Handler`, `Post`), more than one window, native menus and dialogs, hosts, accessibility, zoom, the debug overlay | changed |
| `app/notify`, `app/dock`, `app/tray`, `app/secret`, `app/prefs`, `app/paths`, `app/log`, `app/winstate`, `app/instance`, `app/shell`, `app/login`, `app/power`, `app/perm`, `app/locale` | OS services, each imported only by apps that use it, so binaries and startup stay proportional (D45; `go-and-others.md` §3.12) | new |
| `cmd/gnui` | the CLI: `package`, `run`, `doctor` (D47) | new |

Why `widget/base` and not primitives in `widget` itself: `rexui` builds on the primitives, and `widget`'s old calls must call `rexui`. With both in `widget` that is an import cycle. The proposal's own layering (`rex-design-system.md` §4.2) has this cycle; D40 resolves it.

## Contracts

The contracts are written in code first (wave 0, by me), with stub implementations, so that the tracks build against fixed interfaces. The standing rules apply to all of them:
- the UI runs on the main thread, and workers wake the loop;
- a steady-state frame allocates nothing, under the exact gate (D11, D27);
- bulk data is pointer-free;
- the loop is event-driven: idle means 0 presents and at most 1 wakeup per second;
- no cgo, no webviews, and only OS system libraries in the framework;
- content-first startup within 150 ms warm (D21): **no new framework loads before the first frame.** The one exception is the notification delegate, for apps that opt in (`electron-tauri.md` §1, prerequisite 5); its cost is measured.

### Calling C from an app (`clib`, D35)

```go
lib, err := clib.Open(clib.BundleFramework("libzapfast_core.dylib")) // absolute path; RTLD_NOW|RTLD_LOCAL
send, err := lib.Func("zf_send")                                       // look up once, at setup
call := clib.NewCall()                                                 // keep it (a field), never on the stack
st := call.Reset().Ptr(unsafe.Pointer(core)).Ptr(unsafe.Pointer(&buf[0])).Uint(uint64(n)).Do(send).Int32()
runtime.KeepAlive(buf)
```

- **What it is.** dlopen/dlsym; integer, pointer, `float32`/`float64` and `bool` arguments; integer, pointer and float results; up to the register and stack limits of `internal/ffi.Frame`. It is built on the same trampolines (D3).
- **What it is not:**
  - no callbacks: a C function pointer is never accepted, and a library that calls back into Go is not supported;
  - no structs by value, and no variadic functions.
- **Threading.** A `Call` is used by one goroutine at a time. Any goroutine may call. Blocking C calls release the P (`runtime.cgocall`).
- **Allocations.** 0 per call after `NewCall`, gated by a benchmark.
- **Memory.** Pointer arguments point to Go heap memory or C memory, never to a goroutine stack (ARCHITECTURE §4). The pointee holds no Go pointers. C must not keep it after the call returns. `clib.CStr(dst, s)` makes NUL-terminated copies into a caller buffer.
- **Foreign threads.** A library's own threads (tokio, in ZapFast's case) may run in the process, and Go's signal handling must tolerate them. We rely on fakecgo setting `iscgo` (D3). This is tested, not assumed: a test fixture library of our own, built with clang in the test as D19 allows for tools, starts threads that spin and block while the Go side runs under `SIGPROF` profiling and async preemption (`SIGURG`).
- **Per OS.** darwin and linux. Windows returns `ErrUnsupported` in this phase.
- **Lifetime.** `Lib.Close` only when nothing can still call into the library. Apps normally never close.

### The Objective-C runtime (`internal/objc`, D36)

- **Classes are defined across files.** `objc.Define(name, super, protocols)` returns a builder. Each feature file adds its methods (`def.Method(sel, types, handler)`) and ivars in its own registration function, and `platform` calls `Register` once at startup. Menus, hosts, drag and drop and accessibility can then each add selectors to `GNUIView` without editing one shared file. Wave 0 splits `platform/platform_darwin.go` (1456 lines) by concern onto this registry.
- **Ivars.** Every runtime class gets one `uintptr` ivar holding an index into a Go-side table (windows, hosts, delegates). This replaces the global `theWindow` (`gpui-ghostty.md` §3.2, item 1) and keeps dispatch allocation-free with many instances.
- **`SendSuper`.** `objc_msgSendSuper` with a static `objc_super`, for subclasses that override and call through (`NSWindow makeFirstResponder:`, `NSTextView` paste, `performKeyEquivalent:`).
- **Blocks we make.**
  - Block literals live in a fixed slab of C memory (never Go memory), with the `_NSConcreteGlobalBlock` isa, `BLOCK_HAS_SIGNATURE`, and an invoke pointer that is one of our ffi callback entries. The slot index identifies the Go handler and its context.
  - `_Block_copy` and `Block_release` leave global blocks alone, so the slab owns their lifetime:
    - a one-shot block (completion handlers) frees its slot after its single call, or on `Cancel`;
    - a multi-shot block (observers, event monitors) is freed by `Release`.
  - The slab is bounded (256 live blocks). Exhausting it returns an error; it never grows.
- **Blocks we are given.** `objc.CallBlock(b, args…)` reads the block's invoke pointer and calls it with the block first. Example: the `willPresentNotification:` completion handler.
- **Callbacks on foreign threads.** UserNotifications, AVFoundation and file promises call us on GCD threads.
  - The callback entry detects a thread other than main and takes a per-thread `Frame` from a fixed table of C-allocated frames keyed by `pthread_self` (64 entries; GCD's pool is bounded).
  - A foreign-thread handler may only copy its arguments (strings as UTF-8 into fixed buffers) into `app.Post` and call `platform.Wake`. It never touches AppKit, except calls Apple documents as thread-safe and that we list per use, and it never touches `ui`.
  - It allocates 0 Go objects once warm (tested).
- **Main-thread rule.** The global `objc.F` frame stays main-thread only, checked in debug builds. Class and selector lookups are resolved at init, so foreign threads never look anything up.
- **Leak counters.** Live blocks, retained delegates and observers are counted, for the open/close leak gate.

### The application object (`app`, `platform`; D37)

```go
type Handler interface {
	Start(a *app.App) error        // before the first window; open windows here (content-first applies)
	Event(a *app.App, e *app.Event) // app events, on the main thread, between frames, in order
}
err := app.Main(app.AppConfig{ID: "dev.example.notes", KeepRunning: true}, h)
w, err := a.OpenWindow(app.WindowConfig{Title: "Settings", Chrome: rexui.ChromeCompact}, settingsView)
```

- **`app.Main`** owns the main thread until quit. `app.Run(cfg, b)` stays, unchanged, as one window over `Main`. Today's per-window part of `App` (`Win`, `Surf`, `UI`, the kit) moves to a per-window `app.Win`; `Initer` and one-window apps keep compiling.
- **`AppConfig`:**
  - `ID`: reverse-DNS. It defaults to the bundle's `CFBundleIdentifier`, and services need it (D45).
  - `KeepRunning`: macOS stays running with no window.
  - `StartHidden`: launch without a window, for a login item.
  - `Policy`: Regular, or Accessory (no Dock icon).
  - `Mac`: grouped OS-only options (`go-and-others.md` §3.3).
- **The NSApplication delegate** is a runtime class, set before `finishLaunching`. It handles:
  - `applicationShouldHandleReopen:hasVisibleWindows:` → `EvReopen`;
  - `applicationShouldTerminate:` → `NSTerminateLater`, then `EvQuitRequested{Reason}`;
  - become and resign active;
  - `application:openURLs:` and the `kAEGetURL` Apple event, registered in `applicationWillFinishLaunching:` so a launch by URL is not lost;
  - `applicationDockMenu:`, which `app/dock` supplies;
  - will-terminate.

  It also observes the `NSWorkspace` notifications: will sleep, did wake, screens changed, session active.
- **One app-wide pump.** It is the same `nextEventMatchingMask` loop (D5), owned by the app, not by a window (today it is `(*Window).Wait`). It blocks with zero windows open, and the idle rules hold with zero, one or several windows.
- **App events.**
  - `app.Event` is a fixed-size, pointer-free record: kind, source, window, id, two integers, and a data span.
  - `e.Data()` is valid during the `Event` call only.
  - Kinds: `EvReopen`, `EvActivated`, `EvDeactivated`, `EvQuitRequested`, `EvOpenURLs`, `EvOpenFiles`, `EvWillSleep`, `EvDidWake`, `EvScreensChanged`, `EvWillTerminate`, `EvCommand` (app-wide menu commands; D29's planned `CommandHandler` becomes this), and `EvService` (results and clicks from `app/*` services, tagged by source).
  - **`app.Post(e, data)`** can be called from any goroutine or foreign thread. It is allocation-free and bounded: 256 events and 64 KiB of data. When full it returns false, counts the loss, and the handler receives `EvLost`.
- **The quit policy.** Every route arrives as `EvQuitRequested{Menu|Dock|Logout|Update|Signal}`: ⌘Q (the native menu, N1), Dock → Quit, logout and `a.Quit()`. The default handler (`app.DefaultHandler`, embeddable) asks each window's `CloseRequested`; one refusal cancels the quit. `a.ReplyQuit(allow)` answers, now or later. At logout AppKit's deadline applies, and the documentation says so. The hard-coded ⌘Q (`platform/platform_darwin.go:649`) goes.
- **The last window closing** quits the app, unless `KeepRunning`. `EvReopen` with no visible window shows or reopens the main window by default.
- **More than one window (D30, built here for macOS).** The PHASE5 contract "More than one window" holds as written: app-owned GPU device and atlas, surface-only destruction, safe points, per-window pacing, a refused close, and the frame handler for modal and tracking loops (menus, sheets, live resize), which keeps drawing without re-entering Build. On Windows and Linux, `OpenWindow` for a second window returns `ErrUnsupported` in this phase, and the first window works exactly as now.
- **Window commands** (`app.Win`, at safe points):
  - `SetTitle`, `Frame`/`SetFrame` in points;
  - `ToggleFullscreen`: with a custom titlebar, the inset is dropped and the traffic lights stop being moved while in fullscreen (`gpui-ghostty.md` §4.3);
  - `Miniaturize`, `Focus`, `Show`, `Hide`;
  - `RequestAttention(critical) AttentionID` and `CancelAttention`;
  - `SetDocumentEdited`.

  Automatic window tabbing is off unless the app opts in (`gpui-ghostty.md`, "Window tabbing policy"). `app.Screens(dst)` lists the screens, with their frames, visible frames and scales.

### Keys and text input (`platform`, `ui`, `menu`)

- **New keys:** `, = - / ; ' \` and `` ` ``, the numpad, and F13–F20 (`egui.md` §2.6, "Keys and modifiers"). This unblocks ⌘, for Settings and ⌘= / ⌘− for zoom.
- **`EvModifiers`** on every change (`flagsChanged:`), for ⌥ alternate menu items and hold-⌘ hints.
- **Key bindings.**
  - With a text field focused, `doCommandBySelector:` selectors become `ui` edit actions through a fixed table: `moveWordLeft:`, `moveToBeginningOfLine:`, `deleteToEndOfParagraph:` (⌃K), `transpose:`, `moveParagraphForward:`, and the rest of the standard set. This follows the user's `DefaultKeyBinding.dict` (`egui.md` §2.3, "Standard key bindings").
  - Without a focused field, the keys are delivered as today.
- **`performKeyEquivalent:`** on our view ignores synthetic events with a zero timestamp (`gpui-ghostty.md` §4.5).
- **Shortcut matching on macOS** goes by the character (`charactersIgnoringModifiers`), so ⌘Z sits where AZERTY and Dvorak users expect it; the physical code is still reported. Native menu items get this from AppKit anyway.
- **Mouse:**
  - a ctrl-click is a secondary click;
  - click count and the double-click interval come from the system (`NSEvent.clickCount`, `doubleClickInterval`), replacing the fixed `multiClickNs` (`app/input.go:11`).
- **`platform.SetSecureInput(bool)`** brackets `EnableSecureEventInput`/`DisableSecureEventInput`, balanced, released when the app resigns active. ZapFast's lock code needs it.
- **Allocations:** all of it is allocation-free, and the key gates cover it.

### Scrolling (`platform`, `ui`)

- **Phases.** `EvScroll` carries `Phase` and `Momentum` (None, MayBegin, Began, Changed, Ended, Cancelled, Stationary) and `FlagInverted`, read directly in `scrollWheel:` (we own the view, so no event monitor is needed; `egui.md` §3, item 5).
- **Latching.** At Began, the gesture latches to the scroll box under the pointer and stays there until the momentum ends, even when another scrollable passes under the cursor (`gpui-ghostty.md` §4.2, item 2).
- **Axis lock.** The dominant axis is taken over the first events of a gesture.
- **Rubber-banding, for precise (trackpad) devices only:**
  - past an edge with fingers down, the displayed offset is `edge + (1 − 1/(x·c/d + 1))·d`, with c = 0.55 and d the viewport size;
  - momentum into an edge overshoots slightly, then springs back;
  - Ended without momentum springs back, critically damped, on `ui`'s animation clock;
  - MayBegin (fingers resting) stops any spring and any smooth scroll;
  - wheels stay hard-clamped;
  - the constants are tuned by hand on the MacBook against an `NSScrollView` in AppKitRef's live window (work item D5).
- **Scrollers.** The scroller style (`NSScroller.preferredScrollerStyle` and its change notification) reaches `ui.Appearance`: overlay scrollers show only while scrolling; legacy ones reserve their width.
- **Tests and gates.** Scripted phase sequences give the expected offsets and spring curves, with 0 allocations.

### Text at the point size (`text`, D38)

- **The rule.** Fonts are created at their point size, not at `SizePt × Scale` pixels (`text/coretext_darwin.go:341-344`):
  - shaping and layout run at the point size, and advances are scaled to pixels;
  - glyphs are rasterised with `CGContextScaleCTM(scale)`, with subpixel offsets divided by the scale (`gpui-ghostty.md` §4.1);
  - CoreText then picks SF's optical size and tracking for each point size, as AppKit does.
- **`text.FontDesc` gains:** a `TabularDigits` feature flag, weights 510 / 590 / 700, and the monospaced design (SF Mono).
- **The test.** For 9–22 pt, at 1× and 2×, regular and bold, the advance of probe strings is within 0.5 % of `NSFont`'s, measured in the same test process, and the optical size equals `NSFont`'s. Today 13 pt at 2× reads 13.49 against AppKit's 14.28 per em (audit problem 1).
- **Dilation.** AppKit's font-smoothing dilation is not adopted by default. It is checked when the hosted `NSTextView` (N4) sits beside our text in a GnuiShot capture, and added only if that capture shows a weight difference (`gpui-ghostty.md` §4.1).

### Design tokens v2 (`theme`, `theme/rex`; D39)

- **Colour roles.** The palette becomes `[NumRoles]uint32` indexed by `Role uint8`, with named accessors kept. Each role has a source (`rex-design-system.md` §2.1):
  - **System:** read live from `NSColor` in the window's appearance;
  - **Rex:** the theme's value.

  On Windows and Linux every source is Rex.
- **The system values travel the D32 path.**
  - `platform` reads the semantic and system colours, the highlight colour, Increase Contrast, Reduce Transparency and Reduce Motion into a fixed array, at startup and on the appearance, system-colour, highlight and accessibility-display notifications. It bumps the serial.
  - `app` copies them into `ui.Appearance`, and `theme.Resolved` maps the roles.
  - The startup read is synchronous, about 40 colours, with a target under 1 ms, measured.
  - No polling; a switch allocates nothing once warm.
- **Text styles:** the eleven macOS text styles plus Mono, with control fonts per size (§2.2 of the proposal).
- **Control sizes:** `Size` (Mini … XLarge), resolved into `[NumSizes]SizeTokens`: height, padding, radius, icon box, gap, font, field height (§2.3, §2.6).
- **Elevation, materials and motion** as tokens (§2.4, §2.5). Hover is a per-component token, off by default on macOS. Reduce Motion zeroes every duration except the crossfade.
- **Rex** keeps its identity as its own roles: the field, glass, pane hairlines, shadows and the terminal palette. Dark surfaces become neutral, and the dark accent is the system accent with white on it (audit problems 8 and 20).
- **Contrast** tests run on the resolved system values too: light, dark and both high-contrast appearances.

### Headless primitives and the component library (`widget/base`, `rexui`, `widget`; D40)

- **`widget/base`** holds the `Kit` (moved from `widget`) and the primitives, as in `rex-design-system.md` §4.3:
  - `BeginPress`/`EndPress`, Toggle, Choice, Range, TextInput, Collection (list, table, outline), Popup, Scroll, Split;
  - draw helpers: Fill, Text, Icon, and a FocusRing drawn **outside** the box with the measured outset of 3 pt (audit problem 6).

  Primitives return visual state (`Hot`, `Down`, `Focus`, `Ring`, `Disabled`, `Inactive`) and signals. `Inactive` means the window is not key; recipes draw the unemphasised look from it.
- **`rexui`: one file per component.** Each reads only tokens. Its options are small structs passed by value; its variants and sizes are `uint8` enums that index arrays in `Resolved`. Recipes hold role indices, not colours, so a light/dark crossfade costs nothing extra. Nothing per frame uses closures, interfaces, maps or built strings.
- **Every component:**
  - is keyboard-operable;
  - shows the ring only when focus came from the keyboard;
  - is disabled through colours, not opacity (audit problem 11);
  - works in light and dark, and in an inactive window;
  - records its semantics.

  These are the D28 rules.
- **Copying a component into an app.** `go run ./tools/rexui add button ./internal/ui` copies a component and rewrites its package clause: shadcn's "you own the code", with no registry and no third-party code.
- **`widget`.** `widget.Kit` embeds `*base.Kit`. Today's methods (`k.Button`, `k.Checkbox`, `k.BeginTable` …) call `rexui` with the default variant and size. `Selection`, `TableState` and the other state types are aliases of `base` types, so existing apps compile unchanged.
- **One recipe set on every OS in this phase.** Windows 11 and GNOME recipes come later (D34).
- **Acceptance (the native-feel gate).** On the MacBook, GnuiShot captures of the gallery's `-scene appkit` window are compared with captures of AppKitRef's new live window, which has the same samples in the same grid (D47), in light and dark, with the machine's accent recorded. A measure tool reads edges, fills and rings from both and writes a table and side-by-side sheets. Tolerances:
  - lengths, paddings and radii within ±0.5 pt (1 px at 2×);
  - the focus ring outset 3 ± 0.5 pt;
  - system-sourced colours equal to the live `NSColor` value (±1/255);
  - flat fills, which mix a translucent role over the background, within ΔE2000 ≤ 2 of AppKit's;
  - text advances within 0.5 %, and baselines within ±0.5 pt;
  - traffic-light centres within ±0.5 pt of the measured style, read from AppKit by `RunLive`.

  In CI, a recipe test checks each component's resolved numbers against the committed reference numbers (heights, radii, fill roles, ring outset). We commit Apple's numbers, not its pixels. The Rex-only pieces (field, panes, capsule) are judged by eye, and rexlook's `rexcompare` stays 40/40 after the text refit.

### Native menus and the responder chain (`platform`, `app`, `menu`; D41)

- **The menu bar** is built from `menu.Model` (D29):
  - the app menu: About (the standard panel), Settings ⌘, when registered, Services, Hide, Hide Others, Show All, Quit;
  - File, Edit, View, Window and Help. Naming the menu "Edit" makes AppKit add Emoji & Symbols, Dictation and AutoFill; `setWindowsMenu:` and `setHelpMenu:` give the window list and Help search (`gpui-ghostty.md` §4.4).
- **Standard items** use nil-targeted standard selectors (`copy:`, `paste:`, `undo:`, `selectAll:`, `performMiniaturize:`, `orderFrontCharacterPalette:` …). `GNUIView` implements the edit ones as `ui` edit actions, which is the D28 contract unchanged. A hosted `NSTextView` answers them itself when it is first responder.
- **App commands** target the delegate's `gnuiCommand:`, with the command id in the item's tag.
- **Validation.** `validateMenuItem:` and `menuNeedsUpdate:` read the enabled and checked state that the focused window's Build set (D29). No Go allocation.
- **Building.** The NSMenu tree is rebuilt only when the model's revision changes, at a safe point **after the first present**, so startup does not pay for it. Key equivalents and their layout localisation come from AppKit.
- **Context menus.** `menuForEvent:` and ctrl-click open the model the app supplies (`k.ContextMenu(model)`), at a safe point. `popUpMenuPositioningItem:atLocation:inView:` runs its tracking loop while the frame handler keeps drawing; the choice arrives as a command. Items can carry SF Symbol images.
- **Popup buttons.** `Select` and `PullDown` draw the button and open a native menu with the selected item over the button (`rex-design-system.md` §3.1).
- **On macOS `Kit.MenuBar` draws nothing.** Windows and Linux keep the drawn menus.

### Dialogs, tooltips and materials (`platform`, `app`; D41)

- **Alerts.** `a.Alert(win, AlertSpec{Style, Message, Info, Buttons, Destructive, Suppression})` returns a ticket; the answer arrives as an `EvService` alert result.
  - It is an `NSAlert` sheet (`beginSheetModalForWindow:completionHandler:`, a block on the main thread).
  - The initial keyboard focus is set for keyboard users (`gpui-ghostty.md`, "Sheets").
  - Never `runModal`.
- **File panels.** `a.OpenPanel(win, OpenSpec{Files, Dirs, Multiple, Types, Start})` and `a.SavePanel(…)` are sheets, with `UTType` filters. They are launched at safe points, and their paths arrive in the event's data.
- **The drawn `Dialog`** stays for custom content. On macOS it is sheet-styled: attached under the titlebar, no scrim (audit problem 16).
- **Tooltips.** `ui` reports tooltip regions, as it does cursor regions (D24). `platform` registers them with `addToolTipRect:owner:userData:`, and the text is fetched on demand. The delay, look and placement past the window edge are native.
- **Materials.**
  - `ui` reports material regions (Sidebar, Titlebar, Menu, Popover, HUD; `rex-design-system.md` §2.4).
  - `platform` places `NSVisualEffectView`s **below** a non-opaque `CAMetalLayer`, and our pixels in those regions are cleared to alpha 0 (premultiplied). The window keeps its shadow.
  - Reduce Transparency switches every material to its opaque colour.
  - Region changes are applied in step with the present, as hosts are below.

### Hosting native views (`platform`, `ui`, `app`; D41)

The design is `gpui-ghostty.md` §3, adopted with these rules:

- **The view hierarchy.**
  - The content view becomes a flipped, layer-backed `GNUIContainer`. `GNUIView`, the layer-hosting view with the `CAMetalLayer` and the text input client, fills it.
  - Effect views sit below `GNUIView`.
  - Each host sits above it, inside a clip view: the host box's rectangle intersected with its ancestors' clips, so a native view in a scrolled list is clipped by the list.
- **Z-order.** Native views are wholly above or wholly below GPU content, never interleaved. When a drawn popup or modal overlaps a host, the host is hidden for that time. On macOS, menus near a host are native anyway. The rule is in the guide.
- **The API:**
  - `platform`: `NewHost(kind, opts) (HostID, error)` and `ReleaseHost`, at safe points;
  - `ui`: `c.Host(id, key, flags)` reserves a box and reports host regions after layout;
  - `app`: passes the regions to `SetHosts` only when they change.

  At most 32 hosts per window.
- **In step with the present.** A frame that moves a host applies the geometry inside a `CATransaction` with actions disabled, and presents with `presentsWithTransaction`: the D18 live-resize path. Other frames present asynchronously. The cost of such frames is measured.
- **Routing:**
  - pointer events over a host are not queued for `ui`;
  - scrolls a host does not use bubble to the container and reach `ui`;
  - keys and IME go to the first responder;
  - our cursor regions subtract the host rectangles.
- **Focus hand-off.**
  - `GNUIWindow` overrides `makeFirstResponder:` (through `SendSuper`) and queues `EvHostFocus`.
  - Tab, Shift+Tab, Escape and Enter in a hosted text view come back as host commands, so `ui`'s traversal continues.
- **Allocations.** Creating a host may allocate. Geometry updates and reading text back into an app buffer do not.
- **Offscreen and tests.** Offscreen renders draw a placeholder; tests drive the host state directly.
- **Leaks.** Hosts are counted in the leak gate.

### The composer (`widget/base.NativeEditor`, N4)

- **The state** is app-owned:
  - a UTF-8 buffer with `Reserve` and a maximum length; text beyond it is refused in `shouldChangeTextInRange:`;
  - the placeholder;
  - the Enter policy: Enter sends and Shift+Enter adds a line, or the reverse with ⌘Enter sending (ZapFast's setting).
- **Signals:** `Changed`, `Submitted`, `Cancelled`, `Height` (from `usedRectForTextContainer:`, growing from 1 to 6 lines, then scrolling), `CaretRect` (for completion popups), and `PastedImage`.
- **Text reaches Go** on `textDidChange:` only, copied into the app buffer; frames stay allocation-free.
- **Programmatic changes:** set the text (editing a message, restoring a draft), and style ranges (mentions) from app spans.
- **The OS brings** spell check, autocorrect, text replacement, dictation, the emoji palette, Writing Tools, Services, undo and VoiceOver (`gpui-ghostty.md` §2, "Multi-line editor").
- **Pasting images.** An `NSTextView` subclass overrides `readSelectionFromPasteboard:` to route images to the app.

### Accessibility (`platform`, `app`, `ui`; N7)

- **Lazy.** Nothing is built until an assistive technology asks (`accessibilityChildren`, `accessibilityHitTest:`, `accessibilityFocusedUIElement`). Until then the cost is 0, gated in CI (`egui.md` §2.8, "Lazy activation").
- **Once active**, each frame exports `ui.SemTree` (D28) into a per-window element table:
  - one runtime class, `GNUIAXElement`, with an ivar slot;
  - elements keyed by box key and reused across frames;
  - virtual rows report their real position.
- **Notifications** come from a diff of the previous and current trees: focus, value, title, selected text, selected rows, created and destroyed, layout.
- **Announcements:** `NSAccessibilityAnnouncementRequestedNotification` from `Live` semantics.
- **Text:** the range, line and frame functions for editable and selectable text, using C2's line geometry.
- **Hosted views** appear as children at their boxes' positions.
- **The semantics gain** Link, Heading, Image with an alt name, Live, and the dialog, sheet and toolbar groups.
- **Allocations.** The Go side allocates 0 when inactive (CI). When active, it is measured on the MacBook with Accessibility Inspector and reported.

### Rich text and selection (`text`, `ui`; D42)

```go
type Span struct{ Off, Len uint32; Style uint16; Flags uint16; Tag uint32 } // pointer-free; Tag is the app's id (link, mention)
sig := c.RichText(key, body, spans, &styles, ui.RichOpts{Dir: ui.DirAuto, MaxW: w})
if sig.Clicked != 0 { … }                                                       // the Tag under the click
```

- **Styles** live in a per-kit table indexed by `Style`: font key, colour role or RGBA, underline, strike, background, baseline shift.
- **Layout on macOS** goes through CoreText's typesetter (`CTTypesetter` over an attributed string built in a reused buffer). It gives native line breaking, per-line visual bidi order, font fallback and emoji clusters, matching AppKit.
  - Paragraph layouts are cached pointer-free: glyphs, positions, line boxes, and span-to-glyph maps. They are keyed by (hash of text and spans, width, style revision), with clock eviction.
  - They can be laid out on workers: CoreText's immutable objects are thread-safe, as D9 assumed and phase 2 checked for shaping.
  - Windows and Linux keep today's left-to-right wrap for spans in this phase.
- **Links and mentions** get hit regions, a hand cursor through the cursor regions, a Link role, and `Hovered`/`Clicked` tags.
- **An emoji-only message** draws larger, by an app flag.
- **Selection.**
  - A `ui.SelectionScope` (the transcript) lets a selection run across boxes in build order, anchored by (box key, byte offset).
  - Drag-select auto-scrolls at the edges, with C1.
  - Double-click selects a word and triple-click a paragraph.
  - The highlight is the `TextSelection` role, unemphasised when the window is inactive.
  - ⌘C (the Edit role) hands the app the selected ranges per box, and the app assembles the text: ZapFast formats `[time, date] Name:` (`zapfast.md` §2, "Text selection").
  - Services and Look Up read the same selection (N6).
- **Allocations:** hover, click, select and copy allocate 0 once warm, under the gate.

### Variable-height virtual lists (`widget/base`; D43)

```go
var st base.VListState           // app-owned, pointer-free: First, Offset, AtEnd, heights (Fenwick), estimated bitset
st.Reserve(n); st.Prepend(k); st.Append(k); st.Invalidate(i); st.ScrollTo(i, base.AlignCenter)
v := k.BeginVList("transcript", &st, count, base.VListOpts{Estimate: 64, StickToBottom: true})
for i := v.First; i < v.Last; i++ { v.Item(i, ids[i]); /* any boxes */ v.EndItem() }
k.EndVList(&v)
```

- **The position is an item plus an offset** (`go-and-others.md` §3.4), not a pixel offset into a total.
- **Heights.** Items in view and in the overscan are measured after layout, and their heights are stored. Unmeasured items use estimates. Totals and offset-to-item lookups use a Fenwick tree: O(log n).
- **Stability:**
  - when items above the anchor change height, the view keeps still;
  - prepending older history shifts `First` and leaves the view in place;
  - at the end with `StickToBottom`, the bottom stays pinned as items arrive or grow (images loading);
  - `ScrollTo` jumps to an item with an alignment, for replies and search hits.
- **The scroll bar** works from the estimated total, refined as items are measured.
- **Rules:** keyboard navigation, semantics (index of total), 0 allocations, and under 4 ms of CPU per frame scrolling **1M items of mixed heights** on the MacBook and in CI.
- **The fixed-pitch `List` and `Table` stay** as the fast path.

### Images (`img`, `gpu`; D44)

- **The cache.** `img.NewCache(img.Options{GPUBytes, CPUBytes, Workers})`. `c.Request(key, src)` returns a generation-tagged `Handle` and a state: Pending, Ready or Failed. The same key gives the same handle.
- **The key:** (content hash, decode width and height in pixels, flags).
- **The source:** a file path, an app-owned byte lease released by the cache, or inline bytes (WhatsApp's JPEG thumbnails).
- **Decoding** runs on workers:
  - macOS: ImageIO (`CGImageSourceCreateWithData`, `CreateThumbnailAtIndex` with `kCGImageSourceThumbnailMaxPixelSize`, the EXIF transform), at **display size**, converted to sRGB, into pooled C buffers, with a per-worker `ffi.Frame` (the `fs/open_darwin.go` pattern);
  - Windows and Linux: the standard library's PNG, JPEG and GIF decoders in this phase, with WebP `ErrUnsupported`.
- **Uploads.** `c.Poll()` in the frame uploads finished decodes within a per-frame byte budget, so a burst never makes a slow frame. Images up to 256 px go into RGBA atlas pages (ARCHITECTURE §6); larger ones get their own textures.
- **Eviction.** `gpu` gains `FreeTexture` with generation-checked ids on all three backends; today textures are append-only (`gpu/renderer_darwin.go:257`). Eviction is LRU by the frame an image was last drawn, within the byte budgets, and an image in view is never evicted.
- **Animation (GIF, WebP, APNG).** The frame count and delays come from ImageIO. Frames are decoded lazily, only for images drawn this frame, with the next frame as a `WakeAfter` deadline. With none in view the app is idle again (`egui.md` §3, item 9).
- **Drawing.** `k.Image(h, fit, radius)` draws it, with a skeleton placeholder while pending. The `Image` semantics carry an alt name.
- **Allocations.** `Request`, `Poll` and `Image` allocate 0 in steady state once warm. Decoding allocates C memory on workers only.
- **External textures.** `gpu.ImportPixelBuffer` wraps a `CVPixelBuffer` through `CVMetalTextureCache` (Y and CbCr textures, no copy), and the quad shader gains a YUV sampling mode. It serves video (S8), and I420/NV12 uploads serve ZapFast's call frames.

### QR codes and app zoom

- **QR.** `qr.Encode(&m, data, qr.LevelM)` is our own encoder: byte mode, versions 1–40, mask selection, Reed–Solomon. `rexui.QRCode(k, &m, size)` draws the modules as device-pixel-snapped quads, with the quiet zone. A darwin test decodes our output with the system's Vision barcode detector, and known-answer vectors cover the rest.
- **App zoom.** Each window has a zoom factor; ⌘+, ⌘− and ⌘0 are standard View commands. The effective scale is the backing scale times the zoom, through `theme.Resolve` and the atlas, as a scale change is today. Window geometry is stored in unzoomed points (`egui.md` §2.6, the ZapFast bug at `zapfast/src/main.rs:627-633`).

### Background work (`task`)

- **The PHASE5 "Background work" contract holds:**
  - `task.Pool[In, Out]`, with value types and an `Out` that holds no pointers;
  - `Submit` returns a ticket or `ErrFull`;
  - latest wins, by key;
  - `Poll(dst)` allocates nothing;
  - `Close` waits a bounded time.
- **Two additions:**
  - **Keyed declarations** (Iced's subscriptions, `go-and-others.md` §3.5). `pool.Keep(key, in)` in Build keeps that work alive while it is declared, and cancels it a frame after it is not.
  - **Timers.** `c.Deadline(key, t)` is a named deadline; it fires once as a wake, without a goroutine.

### App services (`app/*`; D45)

- **Common rules:**
  - Requests go in; answers, clicks and changes come out as `EvService` events through `app.Post`.
  - Nothing runs per frame. Payloads may allocate on delivery; they are rare.
  - System frameworks (UserNotifications, Security, ServiceManagement, AVFoundation) are loaded with `dlopen` on first use, after the first frame. The exception is the notification delegate, for apps that opt in.
  - The app ID drives the paths, preferences, Keychain service, instance lock and notifications (`go-and-others.md` §3.2).
  - Services with no bundle report unavailable instead of throwing. `notify.Available()` is false when unbundled.
- **The services:**

| Package | API (shape) | macOS |
|---|---|---|
| `app/notify` | `Available`, `RequestPermission`, `Status`, `RegisterCategories` (actions, reply, destructive), `Post(Note{ID, Thread, Title, Subtitle, Body, Sound, Attachment, Category, UserData})`, `Remove(ids)`, `RemoveThread`, `Foreground(show)`; event: response (action, reply text, user data) | `UNUserNotificationCenter`. The delegate is set in `applicationWillFinishLaunching:` when the app opts in, so a click that launched the app is not lost (`electron-tauri.md` §2, "Notifications"). Categories are registered as a union. The tag is the request id (`gpui-ghostty.md` §2) |
| `app/dock` | `SetBadge`, `SetBadgeCount`, `Bounce(critical)`, `CancelBounce`, `SetMenu(model)` | `NSDockTile`, `requestUserAttention:`, `applicationDockMenu:` |
| `app/tray` | `New(Options{Symbol or PNG, Template, Tooltip})`, `SetIcon`, `SetTitle`, `SetMenu(model)`, `Remove`; menu choices arrive as commands | `NSStatusItem` with an NSMenu from N1 |
| `app/secret` | `Open(service).Get/Set/Delete(account)`; `ErrNotFound`, `ErrDenied`, `ErrLocked`, `ErrUnavailable`; blocking, so call it from a task | `SecItem*` C API with `kSecClassGenericPassword`; no data-protection keychain (no provisioning profile needed) |
| `app/paths`, `app/prefs` | `Config/Data/Cache/Logs(id)`; `prefs.WriteAtomic` (temp file, fsync, rename; `F_FULLFSYNC` optional), `prefs.JSON[T]` with defaults, loaded synchronously at startup, saved debounced on a worker | `~/Library/Application Support/<id>`, `Caches/<id>`, `Logs/<id>` |
| `app/log` | `Setup(Options) (*slog.Logger, error)`: stderr plus a rotating file; `debug.SetCrashOutput`; `PreviousCrash()`; never on the frame path | — |
| `app/winstate` | `Load(id)` before the window opens, `Apply(cfg, state, screens)` clamped to a visible screen, `Save` debounced; the state includes frame, maximised, fullscreen, screen, and app-owned bytes | our own file, not `setFrameAutosaveName:`: one rule on every OS, app bytes alongside, and read before the window exists (`electron-tauri.md` §2, "Window state") |
| `app/instance` | `Acquire(id)` → primary or secondary; `Secondary.Send(args)`; the primary gets an event | `flock` in Application Support plus a Unix socket only the user can open (`zapfast/src/single_instance.rs`) |
| `app/shell` | `OpenURL`, `OpenPath` (`fs.Open` stays), `Reveal(paths)`, `Trash(path)` | `LSOpenCFURLRef` on a worker; `activateFileViewerSelectingURLs:`; `trashItemAtURL:` |
| `app/login` | `Status`, `Enable`, `Disable`, `OpenSettings` | `SMAppService mainAppService` (macOS 13+) |
| `app/power` | `Hold(kind, reason)` / `Release` (App Nap, display on during a call); events: screen locked and unlocked, thermal state | `NSProcessInfo beginActivityWithOptions:`; sleep and wake come from the app object (F3) |
| `app/perm` | `Status/Request(Microphone, Camera)`, `OpenSettings(kind)` | `AVCaptureDevice` (a block on a foreign queue) |
| `app/locale` | preferred languages, region, 24-hour clock; a change event | `NSLocale`, `NSCurrentLocaleDidChangeNotification` |

- **The clipboard** (`platform`, extending the PHASE5 contract):
  - `RequestPaste(kinds)` takes a mask: text, image, files;
  - `EvPaste` carries the kind, and image bytes (PNG; TIFF is converted with ImageIO on a worker) or NUL-separated paths, in the same kept buffer, up to `MaxKeptPaste`;
  - `SetClipboardImage(png)`;
  - a `Concealed` flag adds `org.nspasteboard.ConcealedType`, so secrets stay out of clipboard history.
- **URL schemes and documents.** `gnui package` writes `CFBundleURLTypes` and `CFBundleDocumentTypes`. Opens arrive as `EvOpenURLs` and `EvOpenFiles` (F3).

### Media (`media`; D46)

- **Audio playback.** `media.Player` is AVAudioEngine with an `AVAudioPlayerNode` and `AVAudioUnitTimePitch`, giving 1×, 1.5× and 2× with the pitch kept.
  - It plays app-supplied PCM: ZapFast's core decodes Opus, since AudioToolbox Opus is unverified (`zapfast.md` §8).
  - It also plays files AVFoundation decodes itself (AAC, MP3, WAV) through `AVAudioFile`.
  - Events: position, ended, error. Buffer-completion blocks arrive on foreign threads and only post.
- **Recording.** `media.Recorder` is an AVAudioEngine input tap. PCM goes into a preallocated ring, with levels for the waveform; the app encodes. The tap's block runs on an AVAudio queue (not the render thread) and copies only.
- **Video.** `media.Video` is AVPlayer with `AVPlayerItemVideoOutput`. In each frame while playing and visible, `copyPixelBufferForItemTime:` feeds `gpu.ImportPixelBuffer`; a hidden or paused video costs nothing. Inline video is never a hosted `AVPlayerLayer` (`gpui-ghostty.md` §2, "Inline video").
- **Permissions** come from `app/perm`. The usage strings and entitlements come from `gnui package`.

### Tooling contracts (`cmd/gnui`, `widgettest`, `app`; D47)

- **`gnui.json`.** The standard library has JSON (`electron-tauri.md` §4): id, name, version, build, category, minimum system, icon, resources, frameworks (nested dylibs), URL schemes, document types, usage strings, entitlements, launch agents, extra plist.
- **`gnui package`:**
  - builds darwin/arm64 and amd64 with `-trimpath -ldflags="-s -w"` and writes a universal Mach-O in Go;
  - assembles the `.app`, with a pure-Go `.icns` from a 1024 px PNG;
  - writes and lints `Info.plist`;
  - signs inside out with `codesign --options runtime --timestamp` and generated entitlements, nested frameworks first;
  - notarizes with `xcrun notarytool` and staples, once you have an identity;
  - verifies with `codesign --verify --deep --strict` and `spctl`;
  - builds the DMG with `hdiutil`.
- **`gnui run`:**
  - builds a development bundle (`.gnui/dev/<Name>.app`) whose bundle id ends in `.dev`;
  - signs it with **`gnui-dev`**, a self-signed code-signing identity that `gnui doctor --setup-dev-cert` creates once. Trusting it for code signing takes one approval at the MacBook's keyboard, like the GnuiShot grant;
  - the identity gives a stable designated requirement, so TCC, notification and Keychain grants survive rebuilds (`electron-tauri.md` §1, prerequisite 4);
  - no hardened runtime in development, so an app's nested dylib loads without a Team ID;
  - streams the logs.
- **Tool binaries.** GnuiShot, BounceRec and AppKitRef are signed with `gnui-dev` too, which ends GnuiShot's rebuild caveat (its README, "Rebuild caveat").
- **The debug overlay** (`GNUI_DEBUG=overlay`, or a hidden command in development builds):
  - box outlines under the pointer, with key, flags, rectangle, clip, role and name;
  - a frame HUD: CPU per phase, allocations this frame, atlas and texture bytes, box count;
  - **repaint causes:** each frame records why it ran (input, the animation's box key, a `WakeAfter` or deadline key, a task wake, appearance, resize). In development builds it also records the caller PC into a fixed array, symbolised only when shown (`egui.md` §4);
  - duplicate-key warnings;
  - an inspector in a second window.

  It costs a branch per site when off, and the gates run with it off. With it on, a steady-state frame still allocates 0 (fixed arrays).
- **The agent bridge.** Development builds only, with `GNUI_INSPECT=<path>`: a Unix socket on the loopback (never TCP) that serves the semantic tree, `widgettest` steps by role and name, and offscreen renders of the current frame (`egui.md` §4, "External inspection server"; Slint's MCP server, `go-and-others.md` §2c). Zero cost when unset.

## Work

Six tracks with disjoint file ownership, in waves. Effort letters come from the research (S up to about a week, M up to three, L more).

### File ownership

A file belongs to one track at a time. Files not listed here, and the shared ones, belong to me; other tracks change them only through me. The shared files are `go.mod`, `.github/workflows/ci.yml`, `platform/platform.go` (the event and key enums), `app/app.go`, `gpu/renderer_darwin.go`, `DECISIONS.md`, `ARCHITECTURE.md`, `README.md` and `docs/GUIDE.md`.

| Track | Owns |
|---|---|
| **F** foundations | `internal/objc/**`, `internal/ffi/**`, `clib/**`; `platform/app_darwin.go`, `window_darwin.go`, `view_darwin.go`, `keys_darwin.go`, `scroll_darwin.go`, `screens_darwin.go` (from the wave-0 split) and the matching stubs in `platform/*_{windows,linux}.go`; `app/lifecycle.go`, `app/events.go`, `app/window.go`, `app/frame.go`, `app/input.go`; `gpu/metal_darwin.go`, `gpu/surface*.go`; `text/coretext_darwin.go`, `text/font*.go`; `ui/scroll.go`, `ui/signal.go`, `ui/key*.go`, `ui/input.go`; `menu/match.go` |
| **D** design system | `theme/**`; `platform/appearance_darwin.go`, `platform/colors_darwin.go`; `ui/appearance.go`, `app/appearance.go`; `widget/base/**` except the files C and N own; `rexui/**` except `rexui/qr.go`; `widget/**` except `widget/widgettest/**`; `icon/**`; `cmd/gallery/**`, `cmd/pilot/**`, `cmd/rexlook/**`, `examples/notes/**`; `tools/appkitref/**`, `tools/rexcompare/**`, `tools/rexui/**` |
| **N** native hosts | `platform/menu_darwin.go`, `dialog_darwin.go`, `tooltip_darwin.go`, `material_darwin.go`, `host_darwin.go`, `textview_darwin.go`, `drag_darwin.go`, `services_darwin.go`, `a11y_darwin.go`; `app/menus.go`, `app/dialogs.go`, `app/hosts.go`, `app/drag.go`, `app/a11y.go`; `ui/host.go`, `ui/regions_native.go`, `ui/semantics.go`; `menu/menu.go`; `widget/base/native.go` |
| **C** content | `text/para*.go`, `text/wrap.go`; `ui/rich.go`, `ui/selection.go`, `ui/layout.go`, `ui/wrap.go`; `widget/base/vlist.go`, `widget/base/image.go`; `img/**`, `qr/**`, `rexui/qr.go`; `gpu/texture*.go`, `gpu/shaders*` (the YUV mode); `app/zoom.go` |
| **S** services | `task/**`, `media/**`, `app/notify/**`, `app/dock/**`, `app/tray/**`, `app/secret/**`, `app/prefs/**`, `app/paths/**`, `app/log/**`, `app/winstate/**`, `app/instance/**`, `app/shell/**`, `app/login/**`, `app/power/**`, `app/perm/**`, `app/locale/**`; `platform/clipboard*.go`, `platform/paste*.go` |
| **T** tooling | `cmd/gnui/**`, `tools/gnuishot/**`, `tools/mkapp.sh` (retired into `gnui`), `widget/widgettest/**`, `app/debug*.go`, `app/inspect*.go` |

### Wave 0: contracts and enablers (me)

1. **Merge `p6-gnuishot`** (a0ae73f) into `main`, so the capture helper is on the branch everyone works from.
2. **Split `platform/platform_darwin.go`** by concern onto the `objc.Define` registry (F0). The change is mechanical, and the gallery, Pilot and notes render byte-identically before and after.
3. **Contracts in code, with stubs,** for everything under Contracts:
   - `clib`;
   - `app.Main`, `Handler`, `Event`, `Post`, and `app.Win`;
   - the platform additions: keys, phases, screens, hosts, menus, dialogs, regions, system colours;
   - the `ui` types: host, tooltip and material regions; `Span`; `SelectionScope`; `Appearance` v2;
   - the `theme` v2 types, and the `widget/base` skeleton with the `Kit` moved and the `widget` wrappers compiling;
   - the `img`, `task` and `media` APIs;
   - the `app/*` service skeletons, `ErrUnsupported` everywhere until filled in;
   - the `gnui.json` schema.

   All 8 CI jobs green.
4. **This plan's DECISIONS entries,** which you write (D34–D48 below).

Phase 7's Z0 (the Rust ABI) starts now, in the ZapFast repo.

### Track F: foundations (first; it unblocks the others)

- **F1. The Objective-C runtime:** the class registry with ivars, `SendSuper`, blocks made and invoked, foreign-thread callbacks, the lookup rule, leak counters. *Opus (high).* M.
- **F2. `clib`**, with the fixture-library tests (threads, signals, float arguments and results) and its documentation. *Opus (high).* S–M.
- **F3. The application object:** the delegate, the app-wide pump with zero windows, reopen, the quit policy, open-URL and open-file events, sleep and wake, screens, activation policy, `app.Main`, `Handler` and `Post`. *Led by me, with an Opus (high) agent.* M.
- **F4. More than one window on macOS (D30):**
  - the shared device and atlas;
  - the window registry on ivars;
  - safe points and per-window pacing;
  - the frame handler for tracking loops;
  - the window commands, with fullscreen handled for the custom titlebar and tabbing off.

  On Windows and Linux, a second window returns `ErrUnsupported`. *Opus (high).* L.
- **F5. Keys:** punctuation, the numpad, F13–F20, `EvModifiers`, the selector bindings, `performKeyEquivalent:`, character matching, ctrl-click, the system click count, secure input. *Sonnet.* S.
- **F6. Trackpad scrolling:** phases, latching, axis lock, rubber-banding, springs, stop on touch, scroller style. *Opus (high).* M.
- **F7. Text at the point size,** the `FontDesc` additions and the `NSFont` advance test; refitting rexlook's text with D6. *Sonnet, with the glyph-cache key change reviewed by an Opus agent.* S–M.

### Track D: the Rex design system

- **D1. Tokens v2** and the live system values: colours, highlight, contrast, Reduce Motion, Reduce Transparency, scroller style. Text styles, sizes, space, radii, elevation, materials, motion; neutral dark; contrast tests. *Sonnet.* M.
- **D2. `widget/base`:** the `Kit` moved, the primitives, the outside focus ring, `Inactive`, per-component hover; the `widget` wrappers. *Opus (high).* M.
- **D3. The core `rexui` components**, each in its own file, three or four Sonnet agents on disjoint files:
  - Button, IconButton, Toggle, Checkbox, Radio, Switch, Slider, Stepper;
  - Input, SearchField, Select and PullDown (the native menu comes from N1);
  - SegmentedControl, and Tabs on it;
  - Table, List and Outline selection: emphasised or unemphasised, merged runs, striped, no hover (audit problem 5);
  - ScrollArea, Separator, Card (Rex, Box, Inset), text styles, Icon weights, Form;
  - Progress, Spinner, Disclosure, HelpButton, Link;
  - Tooltip and Alert (native, from N2), and Dialog (sheet-styled).

  L in total.
- **D4. Window chrome:** the `Toolbar` (Plain 32, Compact 40, Unified 52, Rex 46), placing the real traffic lights from the token (plain x 9/32/55 y 16, compact 12/35/58 y 20, unified 19/42/65 y 26; `rex-design-system.md` §2.3), with the title at 13 bold and Label2 when inactive. Also the Sidebar (rows 32, groups 19, the selection inset 10 with radius 8.5, over N3a's material) and the PathBar. *Sonnet.* M.
- **D5. The reference and acceptance tools:**
  - AppKitRef `-live`: a real window of the samples, for GnuiShot, run on the MacBook when you allow it;
  - the gallery's `-scene appkit` catalogue, in the same grid;
  - the measure tool and the sheets;
  - the recipe test against the committed reference numbers.

  *Sonnet.* M.
- **D6. Migration,** one Sonnet agent per app:
  - **the gallery;**
  - **notes:** the Compact toolbar, emphasised selection, a regular-size Switch, Remove as Destructive with an NSAlert;
  - **Pilot:** the Unified toolbar with back, forward and up as toolbar IconButtons; a PathBar; a Sidebar of locations over the material; tables in body type with tabular-digit sizes and dates in Label2; folders as symbols, not bold blue; striped rows; the filter as a SearchField; Menlo as an option (audit problem 10);
  - **rexlook:** TabTitle and PaneTitle, IconButton for the header buttons, no other visible change.

  M.
- **D7. Components apps need next:**
  - Popover, Toast/Banner, Badge, Avatar (on C5), Skeleton, EmptyState;
  - TokenField, Combobox, ColorWell (hosting `NSColorPanel`);
  - the chat pieces of `rex-design-system.md` §3.4 (MessageBubble, ChatListRow, DateDivider, TypingIndicator, and the Composer's styling over N4), built as phase 7 asks for them.

  *Sonnet.* M.

### Track N: native hosts

- **N1. Native menus:** the bar, roles, Services, Window and Help menus, validation, context menus, popup-button menus, item symbols, the Dock menu builder for S4; the responder chain; `Kit.MenuBar` empty on macOS; the ⌘Q hack removed. *Opus (high).* M.
- **N2. NSAlert sheets, open and save panels, native tooltips.** *Sonnet.* M.
- **N3a. Materials:** the non-opaque layer, effect views below, the Reduce Transparency fallback, changes in step with presents. *Opus (high).* M.
- **N3b. Native-view hosting:** the container view, the registry, transaction-synced geometry, routing, focus hand-off, the z-order rule, the placeholder, leak counters. *Opus (high).* L.
- **N4. The `NSTextView` composer (`NativeEditor`)**, and the dilation check (D38). *Opus (high).* M.
- **N5. Drag and drop:**
  - **in:** files, images and text, with the hover position and `ui` drop targets;
  - **out:** file URLs, and `NSFilePromiseProvider` for media not downloaded yet; the promise is fulfilled on a worker and completed through a block (`gpui-ghostty.md` §2, "Drag out").

  *Sonnet.* M.
- **N6. Services, Look Up and Writing Tools on our view,** from C4's selection and focused fields; the emoji palette and dictation for our own fields. Writing Tools on a custom view is unverified (`gpui-ghostty.md`, "Could not check"): if it does not appear, that is documented as `NSTextView`-only. *Sonnet.* S.
- **N7. The NSAccessibility bridge:** lazy activation, the element table, the diff, announcements, text ranges, hosts as children; the semantic additions; an accessibility snapshot test; a lint for missing names in `widgettest`. *Opus (high).* L.

### Track C: content

- **C1. `VList`**, variable height and item-anchored. *Opus (high).* M–L.
- **C2. Rich text:** spans, the style table, CoreText's typesetter, cached paragraph layouts, link and mention hit regions, emoji-only messages drawn large. *Opus (high).* L.
- **C3. A wrapping flow layout** for chips, reactions and button rows, which ZapFast wraps (`egui.md` §2.2). *Sonnet.* S.
- **C4. Selectable text:** within a paragraph, then across boxes in a scope; drag with edge auto-scroll on C1; word and paragraph clicks; copy through the Edit role. *Opus (high).* M–L.
- **C5. `img`:**
  - ImageIO on workers, at display size;
  - the cache with byte budgets;
  - `FreeTexture` and atlas pages on all three backends;
  - `k.Image`;
  - inline thumbnails from bytes;
  - the standard-library decoders off macOS.

  *Sonnet; the `gpu` part Opus (high).* M.
- **C6. Animated images,** lazy and visible-only. *Sonnet.* M.
- **C7. External textures** (`CVPixelBuffer` through `CVMetalTextureCache`), the YUV mode, and I420/NV12 uploads. *Opus (high).* M.
- **C8. QR:** the encoder, `rexui.QRCode`, the Vision decode test. *Sonnet.* S.
- **C9. App zoom.** *Sonnet.* S.

### Track S: services

- **S1. Pure Go:** `task`, `app/paths`, `app/prefs` and `app/log`. *Sonnet.* M.
- **S2. `app/secret`.** *Sonnet.* S.
- **S3. `app/notify`.** *Opus (high).* M.
- **S4. `app/dock`, `app/tray`, `app/login` and `app/power`.** *Sonnet.* M.
- **S5. `app/winstate`** (with the screens from F4), `app/instance`, `app/shell` and `app/locale`. *Sonnet.* M.
- **S6. Clipboard images and files.** *Sonnet.* S–M.
- **S7. Audio:** `media.Player`, `media.Recorder` and `app/perm`. *Opus (high).* M–L.
- **S8. Video into Metal**, on C7. *Opus (high).* M.

### Track T: tooling

- **T1. `cmd/gnui`:** `package`, `run` and `doctor`, the `gnui-dev` identity; `mkapp.sh` retired; the tool apps re-signed. *Sonnet.* M.
- **T2. The live-window harness:**
  - **In macOS CI:** `RunLive` grows to several windows and to app-level flows (reopen, quit refusal, a menu command with no window open), checked through AppKit state.
  - **On the MacBook:** `widgettest.Capture(t, win, name)` calls GnuiShot when `GNUI_GNUISHOT` is set, and skips otherwise. Captures need the MacBook awake and unlocked, so agents run them when you allow.
  - **Assertions:** `widgettest.AssertTree` and `AssertImage` with `-update` and `testdata/failed/` (`go-and-others.md` §2c).
  - **Fakes for native surfaces:** panel and alert answers, notification responses, menu commands, appearance flips, host text (`gpui-ghostty.md` §5, item 4).
  - **GnuiShot in CI:** a hosted runner probably cannot grant Screen Recording. That is tried once and reported; it is not assumed.

  *Sonnet.* M.
- **T3. The debug overlay,** frame HUD, repaint causes, duplicate-key warnings and inspector. *Sonnet.* M.
- **T4. The agent bridge.** *Sonnet.* S–M.
- **T5. Documentation:**
  - every new public package gets compiled `Example` functions (`go-and-others.md` §2c);
  - the guide gains chapters for the app object, `rexui`, images, rich text, services and packaging, as checked excerpts;
  - doccheck covers every new package.

  *Sonnet drafts; I integrate.*

### Waves and the dependency graph

```
wave 0  F0 split + contracts (me), T0 merge GnuiShot                       ║ Z0 Rust ABI (phase 7)
           │
wave 1  F1 objc ─────────┬──────────────┬───────────────┐                  ║
        F2 clib ─────────┼──────────────┼───────────────┼──────────────── ═╬═▶ Z1 Go bindings
        F5 keys, F6 scroll, F7 text      │               │                  ║
        D1 tokens ─▶ D2 base ─┐          │               │                  ║
        C1 VList, C2 rich text, C3 flow, C5 img, C8 QR                      ║
        S1 task/paths/prefs/log, S2 secret, S6 clipboard                    ║
        T1 gnui, T2 harness                                                 ║
           │                                                                ║
wave 2  F3 app object ─▶ F4 windows ──┐  N1 menus   N2 alerts/panels/tips   ║
        D3 rexui core, D4 chrome, D5 reference tools                       ║
        C4 selection (C2), C6 animation (C5), C9 zoom                       ║
        S3 notify (F1, F3, T1), S4 dock/tray(N1)/login/power, S5 (F4)       ║
        T3 overlay                                                          ║
           │                                                                ║
wave 3  D6 migrate gallery, notes, Pilot, rexlook   N3a materials           ║ Z2 shell, login,
        N3b hosting (F3, F4)   C7 external textures   T4 agent bridge       ║    chat list, settings
        ══▶ CHECKPOINT 1: native feel (gallery, Pilot, notes on the MacBook)║
           │                                                                ║
wave 4  N4 composer (N3b)  N5 drag  N6 services  N7 accessibility (C2, C4)  ║ Z3 conversation, media,
        D7 components   S7 audio   S8 video (C7)   T5 docs                  ║    pickers, dialogs
           │                                                                ║
wave 5  fixes from both checkpoints' findings                               ║ Z4 calls, notifications,
                                                                            ║    a11y, i18n; Z5 parity
                                                                            ║ ══▶ CHECKPOINT 2
```

- **Within a wave,** tracks run in parallel on disjoint files: up to about eight agents at once. I integrate at the end of each item, not at the end of the wave.
- **Critical path to ZapFast parity:** F1 → F3 → F4 → N3b → N4 (the composer) → Z3 (the conversation) → Z5.
- **Near-critical:**
  - C2 → C4 → N7 (selectable text, then accessibility text ranges);
  - C5 → C7 → S8 (video);
  - Z0 → Z1 → Z2 on the ZapFast side.
- **Not on the critical path:** the design-system work (D) runs beside it and gates checkpoint 1 only.

### Checkpoints for you

1. **Native feel.** You look at the gallery, Pilot and notes on the MacBook after F1–F7, D1–D6, N1, N2 and N3a.
   - **What you get:** a GnuiShot sheet set (ours beside AppKit, light and dark) and the measured tables.
   - **What to try by hand:**
     - the menus: ⌘Q with a refusing window; Edit → Copy and ⌘C in a field, each acting once; a context menu;
     - scrolling and rubber-banding on the trackpad;
     - focus rings from the keyboard;
     - switching accent, highlight colour and appearance;
     - Reduce Motion and Reduce Transparency;
     - an alert and a save panel;
     - a second window;
     - closing the last window and clicking the Dock icon.

   Your findings are fixed before wave 5 ends.
2. **ZapFast at parity.** See the phase-7 plan. You see ZapFast for the first time there, at full parity (D33).

### Who does what

- **Me:**
  - wave 0;
  - F3 and the integration of F4;
  - every shared file;
  - integration and CI;
  - DECISIONS drafts for you;
  - the guide.
- **Opus 5.5 (high) agents** (your instruction for hard tasks): the architectural and risky items marked above (F1, F2, F3 with me, F4, F6, D2, N1, N3a, N3b, N4, N7, C1, C2, C4, C7, S3, S7, S8, and the `gpu` part of C5).
- **Sonnet agents (`focused-sonnet`):** the bounded items marked Sonnet.
- **Luna (`bulk-luna`):**
  - extracting the committed reference numbers from `metrics.json` into the token and recipe tables;
  - the inventory of AppKit standard selectors for F5 and N1;
  - doc-comment and `Example` sweeps;
  - phase 7's variant inventory and parity checklist.
- **Reviews:** one independent review per track deliverable, and one of everything before each checkpoint, following the routing rules (a reviewer from the other model family).

## Exit criteria (macOS on the MacBook; Windows and Linux in CI)

1. **Native feel:**
   - the D-track tolerances hold for every component sample, in light and dark, with the tables and sheets committed under `docs/measurements/phase6-*.md`, without Apple's pixels;
   - `rexcompare` stays 40/40 after the text refit;
   - the text advance test passes at 9–22 pt, at 1× and 2×.
2. **Native pieces on macOS:**
   - the menu bar, context and popup menus, alerts, panels, tooltips and materials are native in the gallery, Pilot and notes;
   - no drawn menu bar on macOS;
   - VoiceOver reads the gallery and Pilot: a table row reports "row n of 1,000,000"; buttons press, sliders step, focus follows; Accessibility Inspector's audit finds no unnamed control (your hand test plus the snapshot test).
3. **The app object, by your hand test plus CI's AppKit-state tests:**
   - running with no window;
   - Dock reopen;
   - quit refused by a window and then allowed;
   - a menu command with no window open;
   - an open-URL event from `open notes-dev://…` on a `gnui run` bundle;
   - sleep and wake events.
4. **Windows:**
   - two windows get input routed correctly, are paced independently, and receive worker results correctly;
   - 20 open/close cycles of a second window, with a hosted text view, a shown menu and a shown sheet, leak nothing, counted by native objects: windows, surfaces, textures, hosts, blocks, observers.
5. **Content:**
   - **VList:** 1M mixed-height items under 4 ms CPU per frame and 0 allocations; prepend without a jump; stick-to-bottom while items grow; jump to an item (scripted, CI);
   - **rich text:** bidi paragraphs reorder per line (Hebrew, Arabic and English mixed); one emoji cluster lays out one emoji wide; hover, click, select and copy allocate 0;
   - **images:** decoded at display size; the cache stays within its budget while scrolling 10k images (gallery scene); animation idles when nothing animated is in view.
6. **Services** (gallery Services scene and tests):
   - a notification with a reply action round-trips under `gnui run`;
   - badge, bounce, status item, login item status, Keychain set/get/delete, atomic prefs, a log file and a captured crash, window state restored and clamped to a screen, a second-instance hand-off, reveal, trash, and clipboard image paste and copy;
   - voice playback at 1.5× with the pitch kept;
   - a recording's levels;
   - a video plays in a scrolling list without a copy (`CVMetalTextureCache`).
7. **Performance** (CI on all three OSes for what exists there, and the MacBook):
   - **allocations:** 0 in steady state in the gallery, Pilot and notes benches, extended with: phased scrolling and rubber-banding, menu validation, rich-text hover, select and copy, a warm image scroll, host geometry updates, and the debug overlay off and on;
   - **scrolling:** 1M rows under 4 ms CPU per frame, in `Table` and in `VList`;
   - **idle** over 60 s with two windows, a status item, the notification delegate, power observers, the menus and an unfocused hosted text view: 0 presents, ≤ 1 wakeup/s, 0 % CPU. A *focused* `NSTextView` blinks its own caret: that is measured and reported as the native composer's cost, not gated;
   - **startup:** ≤ 150 ms warm for the guide's minimal app; the gallery, notes and Pilot are reported against phase 5; content-first and half-bounce hold (BounceRec); the menu bar and services add nothing before the first frame;
   - **size and memory:** gallery, Pilot and notes binaries < 10 MB; the minimal app ≤ 40 MB footprint.
8. **Windows and Linux:**
   - CI green on all 8 jobs;
   - the new APIs return `ErrUnsupported` there, tested;
   - everything that worked in phase 5 still passes its benches and gates.
9. **API and docs:**
   - every exported identifier documented (doccheck in CI);
   - every new public package has compiled `Example`s;
   - the guide's new chapters are checked excerpts;
   - `go run ./tools/rexui add` copies a component into a scratch app that builds.
10. **`gnui`:**
    - `gnui package` produces a universal, hardened, signed bundle that passes `codesign --verify --deep --strict`. Notarization and stapling are verified once you have an identity, and listed as untested until then;
    - `gnui run` keeps a notification grant across two rebuilds.
11. **Numbers:** Windows and Linux numbers come only from CI. Anything not exercised is listed as untested, not claimed: GnuiShot in CI, Writing Tools on our view, VoiceOver with the bridge active in CI.
12. **You:** checkpoint 1 is passed and its findings are closed.

## New decisions (drafts for D34 onward)

One-line summaries, then the drafts. You write the entries.

| # | Summary |
|---|---|
| D34 | Phase 6 is macOS-first; Windows and Linux compile, stay green and return `ErrUnsupported` for new APIs; their parity is a later phase |
| D35 | A public `clib` package gives apps cgo-free C calls (no callbacks); the framework itself still loads only system libraries |
| D36 | `internal/objc` gains a cross-file class registry, ivars, `SendSuper`, blocks in C memory, and foreign-thread callbacks that only post and wake |
| D37 | An application object: NSApplication delegate, one app-wide pump, `app.Main`/`Handler`/`Post`, a quit policy, windowless running; D30 built for macOS now |
| D38 | Text is shaped and laid out at the point size and rasterised with a scaled context; dilation only if captures show it |
| D39 | Design tokens v2: indexed roles with System or Rex sources read live through the D32 path; macOS text styles, control sizes, motion, materials |
| D40 | Components in three layers: `widget/base` (headless and the Kit), `rexui` (styled, one file each, copyable), `widget` (wrappers); amends D28's layering |
| D41 | Native on macOS where the OS adds behaviour (menus, popup lists, alerts, panels, tooltips, materials, the composer, accessibility); native views never interleave with GPU content |
| D42 | Rich text as pointer-free spans laid out by CoreText's typesetter on macOS; selection scoped across boxes; copy assembled by the app |
| D43 | Variable-height lists are item-anchored, with a Fenwick height cache; fixed-pitch lists stay the fast path |
| D44 | Images through ImageIO on workers at display size, a byte-budgeted texture cache with freeing and atlas pages, lazy visible-only animation |
| D45 | App services are separate `app/*` packages: requests in, events out, frameworks loaded after the first frame, keyed by the app ID |
| D46 | Media through AVFoundation; video zero-copy into Metal; codecs the OS lacks stay with the app |
| D47 | Tooling: `gnui` CLI on `gnui.json` and OS-shipped tools, a stable self-signed `gnui-dev` identity, GnuiShot and AppKitRef's live mode as the native-feel instruments, a zero-cost debug overlay and agent bridge |
| D48 | Our own QR encoder rather than a dependency |

ZapFast's decisions are D49–D51, drafted in the phase-7 plan.

**D34. Phase 6 is macOS-first.**
- **Decision (you, 2026-10-18):** "it should feel macos native (we take care of other operating systems afterwards)". New APIs ship macOS implementations. On Windows and Linux they compile and return `ErrUnsupported`, or keep today's drawn equivalent (menus, dialogs); a second window is refused. CI stays green on all 8 jobs, and everything phase 5 delivered there keeps passing.
- **Alternative:** all three OSes per feature, as phases 1–5 did. That roughly triples the native work before ZapFast is possible.
- **Cost:** a parity phase later, whose size grows with this one: hosted views need child HWNDs and Wayland subsurfaces; the native pieces need their drawn versions.

**D35. `clib`: C calls for apps.**
- **Decision:** a public package for dlopen/dlsym and plain C calls (integer, pointer and float arguments; no callbacks, structs or variadics), on `internal/ffi`'s trampolines, with 0 allocations per call. darwin and linux now; Windows `ErrUnsupported`.
- **Why:** D33's ZapFast core lives in another module and cannot import `internal/ffi`. A public, narrow surface is safer than exporting `ffi.Frame`, and it keeps callbacks (the hard part) internal.
- **D2 is unchanged:** the framework bundles nothing and loads only OS libraries. What an app loads is that app's decision, recorded by the app (D33).
- **Risk:** foreign threads in a fakecgo process. Mitigated by the signal and thread fixture test.

**D36. The Objective-C runtime grows.**
- **Decision:** a class registry that collects methods from several files; one ivar per instance holding a Go-table index; `objc_msgSendSuper`; global-style blocks in a bounded C slab, with an ffi callback as invoke; calling blocks AppKit gives us; per-thread frames for callbacks on GCD threads, whose handlers only copy, post and wake.
- **Evidence:** every report found blocks the blocking prerequisite: `egui.md` §3, `electron-tauri.md` §1, `gpui-ghostty.md` §3.2. Only one global block exists today (`gpu/metal_darwin.go:295-340`).
- **Alternative rejected:** dispatching everything to the main queue. AVFoundation and UserNotifications still call us on their queues, and their completion handlers must be called.

**D37. The application object.**
- **Decision:** as in Contracts, "The application object": an NSApplication delegate set before `finishLaunching`, one pump owned by the app, app events through a bounded allocation-free `app.Post`, the quit policy, `KeepRunning` and `StartHidden`, Accessory policy. D30 is built for macOS in this phase.
- **Evidence:** `electron-tauri.md` §1 ("No application object"), `go-and-others.md` §2a ("App lifecycle without windows"), `gpui-ghostty.md` §2 ("NSApplication delegate").
- **Why events, not callbacks:** closures per event break the zero-allocation and main-thread rules (`go-and-others.md` §3.1). Gio's app-global event stream is the Go-idiomatic model.

**D38. Text at the point size.**
- **Decision:** fonts are created at the point size, and rasterised with a context scaled by the backing scale.
- **Evidence:** advance per em at 13 pt is 14.28 in AppKit and 13.49 for us at 2× (audit problem 1). GPUI does it this way (`gpui_apple/src/text_system.rs:489-526`).
- **Cost:** a glyph-cache key change; rexlook's text metrics are refit, with rexcompare as the gate. Dilation (`AppleFontSmoothing`) stays off unless N4's side-by-side capture shows a difference. This revisits D21's "slightly bolder text is not needed" only on that evidence.

**D39. Design tokens v2.**
- **Decision:** `rex-design-system.md` §2: indexed roles, each with a System or Rex source; text styles; control sizes; space, radii, elevation, materials and motion. System values come live through the D32 path, without polling. Reduce Motion, Reduce Transparency and Increase Contrast are followed. Rex keeps its identity roles; dark surfaces are neutral; white goes on the accent.
- **Evidence:** audit problems 3–8 and 11–20.
- **Alternative:** retuning `widget` in place (option A). Rejected as the end state: no variants and no sizes.

**D40. Component layering.**
- **Decision:** `widget/base` holds the `Kit` and the headless primitives; `rexui` holds the styled components, one file each, copyable; `widget` keeps its API as wrappers (`widget.Kit` embeds `*base.Kit`, state types are aliases).
- **Why the extra package:** with primitives inside `widget`, the wrappers would import `rexui` and `rexui` would import `widget`, which is a cycle (`rex-design-system.md` §4.2).
- **Alternatives rejected:** painter interfaces (option C: interface calls per row, escaping arguments), and hosting NSButton and friends (option D).
- **Amends D28's layering line:** `… → theme/rex, icon → widget/base → rexui → widget → task → app`.

**D41. Native AppKit where the OS adds behaviour.**
- **Decision:** on macOS these are native: the menu bar, context and popup menus, NSAlert, the open and save panels, tooltips, materials (`NSVisualEffectView`), the chat composer (`NSTextView`) and accessibility. Everything else is drawn.
- **The rules:**
  - native views are wholly above or below the Metal layer, never interleaved;
  - geometry changes are presented in the same transaction;
  - a host is hidden while a drawn overlay covers it.
- **Evidence:** `gpui-ghostty.md` §1 (the common pattern; "Ghostty feels native because the chrome is AppKit") and §3; `rex-design-system.md` §3 ("Draw / host").
- **Alternative rejected:** hosting standard controls (option D: AppKit's heap per control, no gain in behaviour).
- **Cost:** two text renderers side by side (D38 mitigates), synchronous presents on frames that move hosts (measured), and a z-order rule designers must follow.

**D42. Rich text and selection.**
- **Decision:** pointer-free spans and a per-kit style table. On macOS, paragraphs are laid out by `CTTypesetter`: native breaking, per-line bidi, fallback. The layouts are cached pointer-free and may be built on workers. A selection scope spans boxes in build order, and copy hands the app ranges to assemble.
- **Evidence:** the P0 rich text and selection rows in `zapfast.md` §2, `egui.md` §2.3 and `go-and-others.md` §2b. `ui/wrap.go` is one font per box and left-to-right only.
- **Alternative:** our own UAX #9 and UAX #14 on every OS now. Left for the Windows and Linux phase. go-text has bidi there already.

**D43. Variable-height lists.**
- **Decision:** `VList`: an item-anchored position (first item and offset), measured heights with estimates, a Fenwick tree for totals; prepend, stick to the bottom, jump to an item. The fixed-pitch `List`/`Table` remain.
- **Evidence:** Gio's `layout.List` position model (`go-and-others.md` §3.4), egui's heterogeneous rows (`egui.md` §2.4), ZapFast's own height cache (`zapfast/src/ui/conversation.rs:2289-2300`).

**D44. Images.**
- **Decision:**
  - decoding on workers through ImageIO at display size;
  - a cache keyed by content and size, with GPU and CPU byte budgets and LRU eviction, never evicting what is in view;
  - `gpu.FreeTexture` with generations, and RGBA atlas pages for small images;
  - animated frames decoded lazily for visible images only;
  - the standard library's decoders on Windows and Linux for now.
- **Evidence:** `egui.md` §2.5 (egui's GIF loader decodes every frame up front; textures need freeing), `go-and-others.md` §5 gap 1; our textures are append-only (`gpu/renderer_darwin.go:257`).

**D45. App services.**
- **Decision:** one package per service under `app/`, imported only when used. Requests go in, events come out through `app.Post`. Frameworks are loaded on first use after the first frame; the notification delegate is the opt-in exception. Everything is keyed by `AppConfig.ID`. Window state uses our own file, read before the window exists.
- **Evidence:** `electron-tauri.md` §3; `go-and-others.md` §3.2 and §3.12.
- **Alternatives:** Fyne- or Wails-style callback APIs, rejected for the allocation rule; `setFrameAutosaveName:`, rejected because it is macOS-only and cannot carry app state.

**D46. Media.**
- **Decision:**
  - AVAudioEngine for voice-note playback (with time-pitch speed) and recording taps;
  - AVPlayer with `AVPlayerItemVideoOutput` → `CVMetalTextureCache` for inline video, never a hosted player layer;
  - codecs the OS lacks or that are unverified (Opus, WebP encoding) stay with the app; ZapFast's core already has them.
- **Evidence:** `zapfast.md` §2 ("Media"), `gpui-ghostty.md` §2 ("Inline video").
- **Risk:** Go code on AVAudio's queues. Handlers only copy into preallocated rings.

**D47. Tooling and the native-feel instruments.**
- **Decision:**
  - `cmd/gnui` (`package`, `run`, `doctor`) on `gnui.json`, shelling out only to tools that ship with macOS (`codesign`, `hdiutil`, `security`, `plutil`) plus `notarytool` and `stapler` from the Command Line Tools for notarization;
  - a self-signed `gnui-dev` identity for development bundles (bundle id plus `.dev`), so TCC and Keychain grants survive rebuilds;
  - GnuiShot (D19) on the MacBook as the acceptance instrument for live windows, with AppKitRef's live mode as the reference;
  - a debug overlay with repaint causes, and a loopback agent bridge in development builds, both zero-cost when off.
- **Evidence:** `electron-tauri.md` §4; `egui.md` §4; `go-and-others.md` §4.
- **Alternative:** ad-hoc signing (today's `mkapp.sh`). Every rebuild voids the grants (GnuiShot README, "Rebuild caveat").

**D48. Our own QR encoder.**
- **Decision:** `qr` is written here (byte mode, versions 1–40, Reed–Solomon), with a darwin test that decodes our output with Vision.
- **Why:** pairing needs it (`zapfast.md` §2, "UI shell"). An encoder is a few hundred lines, and a dependency would need a D2 row and a licence review (`go-and-others.md` §2b, "QR code").

## Gaps: what the research found and this phase leaves out

| Gap | Source | Why it waits |
|---|---|---|
| Windows and Linux parity of everything here (hosted views as child HWNDs or Wayland subsurfaces, notifications through toast COM or D-Bus, tray through `Shell_NotifyIcon` or StatusNotifierItem, Keychain equivalents) | all five reports | D34: macOS first |
| A drawn multi-line editor | `zapfast.md` §2; `egui.md` §2.3 | The macOS composer is an `NSTextView` (N4), which also brings spell check and Writing Tools. The drawn editor is needed only where there is no `NSTextView` |
| i18n: catalogs, plurals, an extractor | `go-and-others.md` §2b, §4 | ZapFast's 11 languages are ported app-side in phase 7 (generated tables from its `.po` files). The framework package comes when a second app needs it |
| A framework self-updater (`app/updates`) | `electron-tauri.md` §2–4 | It needs your Apple identity and a release feed. Phase 7 handles ZapFast's updater as an open question |
| A Share menu, Quick Look, the Dock tile's progress, recent documents | `electron-tauri.md` §2; `gpui-ghostty.md` §2 | P2–P3; no current app needs them |
| Global shortcuts, Handoff, Touch Bar, push (APNs), in-app purchase | `electron-tauri.md` §2 | P2–P3; push and IAP need provisioning profiles |
| Touch ID for an app lock | `electron-tauri.md` §2 | ZapFast's lock uses a code; not parity |
| Network reachability and the system proxy | `electron-tauri.md` §2 | ZapFast's core reconnects on its own and has its own proxy setting |
| Content protection, always-on-top, represented file | `egui.md` §2.6; `electron-tauri.md` §2 | Cheap but unused; added when an app asks |
| Popovers past the window edge (child `NSPanel`s) | `egui.md` §2.2; PHASE5 "Not in this phase" | Native menus cover menus; Popover stays in-window |
| Hot reload, `gnui dev`, live preview | `go-and-others.md` §2c, §4 | P2; Go cannot swap code; offscreen scene renders already give fast feedback |
| Recorded text-format tests and a test recorder | `go-and-others.md` §2c; `egui.md` §4 | P2; `widgettest` steps are Go; the agent bridge (T4) covers driving a live app |
| Spell check in drawn fields | `gpui-ghostty.md` §2 | The composer gets it from `NSTextView`; drawn fields are single-line |
| SVG rasterisation | `egui.md` §2.5; `go-and-others.md` §2b | SF Symbols cover the icons; whether `NSImage` reads SVG on macOS 14 is unverified |
| Markdown, plots, date picker, syntax highlighting, a code editor | `go-and-others.md` §2b; `egui.md` §2.4 | P2–P3. Markdown is cheap once rich text exists, and comes when asked |
| Palettes from seed colours, theme files | `go-and-others.md` §2b, §3.6 | ZapFast's custom themes are mapped onto Rex roles app-side in phase 7 |
| Persisted UI memory keyed by id | `egui.md` §2.1 | App-owned state plus `app/prefs` cover it; a general store would invite pointers |
| Display P3 output | `gpui-ghostty.md` §4.6 | Photos look right in sRGB first; P3 is a later change to the layer and decode |
| Accessibility on Windows (UIA) and Linux (AT-SPI) | phase-5 plan | D34 |
| The X11 PRIMARY selection and an X11 input method | PHASE5 Status | Unchanged from phase 5 |

## Status (2026-10-18)

Planned; not started. Wave 0 starts on your approval.

- **Inputs, on `main` at d32d248:** the research reports, the native audit, the design-system proposal and AppKitRef.
- **GnuiShot:** on `p6-gnuishot` (a0ae73f), granted on the MacBook and not yet merged; wave 0 merges it.
- **Phase 5:** 5A is closed, and 5B is absorbed here (`docs/PHASE5-PLAN.md`, Status).
