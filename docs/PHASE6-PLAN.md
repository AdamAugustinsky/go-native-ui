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

The plan is to be reviewed independently before any code, as phase 5's was. Revised 2026-10-18 after that review (GPT-6.1 Sol), whose findings you accepted, and again after its scoped re-review; the fixes are in the contracts, work items, tests and graph they concern, and the decisions are in `DECISIONS.md` as D34–D51. Behaviour not yet verified is marked as an assumption or tied to a named feasibility test.

## Revision 2026-10-19: native shell first, rexlook's language everywhere

The user used the apps on the MacBook: only rexlook looks and feels right; the gallery, notes and Pilot "look linux-like, or old versions of macos (much before liquid glass)" (D52). Two results changed the order of work:
- **D0, the Liquid Glass reference (done, `p6-scenes`):** `tools/appkitscenes` measures real macOS 27.2 windows in the Finder, Notes, Settings and chat patterns; the spec is `docs/design/liquid-glass-reference.md`. It corrects D4: the unified title is 15 pt bold (13 only in compact); selection radius 8; the sidebar is flush and full-height glass, not inset.
- **The native-shell spike (done, `p6-nativeshell`):** rexlook on a real glass backdrop with Rex's own tabs (`docs/design/native-shell-spike.md`).

**New order.** Native feel goes first; the foundations continue alongside.
1. **Now (running):** D2 `widget/base`; F1, C1, S1, T1, C8; F2 done.
2. **Next, as soon as D2 and the spike merge:** D1 tokens with D0's numbers; N1 native menus (the gallery's drawn menu bar goes); N3a materials per area, generalising the spike's backdrop to `ui` regions (sidebar, inspector, toolbar capsules); D4 window chrome (the Toolbar and Sidebar to D0's spec; Rex tabs as the tab component); D3 core `rexui`.
3. **Then:** D6, the gallery, notes and Pilot rebuilt in rexlook's language on D0's layouts, verified live against `appkitscenes` (Settings, Notes, Finder). **Checkpoint 1** follows directly.
4. The rest of the waves below keep their order after that.

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
// int32_t zf_send(zf_core *c, const uint8_t *cmd, size_t len, zf_err *err);  (ZapFast's core, D49)
type zfErr struct{ Code int32; Len uint32; Msg [256]byte }            // the C zf_err, laid out the same

type sender struct {                                                   // one per call site, used by one goroutine at a time
	call *clib.Call
	err  *zfErr                                                        // heap-resident, owned with the Call
	fn   clib.Func
}

lib, err := clib.Open(clib.BundleFramework("libzapfast_core.dylib"))  // absolute path; RTLD_NOW|RTLD_LOCAL
fn, err := lib.Func("zf_send")                                          // look up once, at setup
s := &sender{call: clib.NewCall(), err: new(zfErr), fn: fn}            // allocated once, at setup

st := s.call.Reset().Ptr(unsafe.Pointer(core)).Ptr(unsafe.Pointer(&buf[0])).Uint(uint64(n)).
	Ptr(unsafe.Pointer(s.err)).Do(s.fn).Int32()                         // four arguments; the error comes back in s.err
                                                                       // no runtime.KeepAlive(buf): Ptr keeps buf reachable until Do returns
if st != 0 {
	return s.err.toError()                                              // allocates only on failure
}
```

- **What it is.** dlopen/dlsym; integer, pointer, `float32`/`float64` and `bool` arguments; integer, pointer and float results; up to the register and stack limits of `internal/ffi.Frame`. It is built on the same trampolines (D3).
- **What it is not:**
  - no callbacks: a C function pointer is never accepted, and a library that calls back into Go is not supported;
  - no structs by value, and no variadic functions.
- **Threading.** A `Call` is used by one goroutine at a time. Any goroutine may call. Blocking C calls release the P (`runtime.cgocall`).
- **Errors come back in the same call.** Two consecutive `Do` calls may run on different OS threads, because the goroutine can move between them. So thread-local error state (`errno`, `dlerror`, a library's "last error") cannot be read reliably after a call. Libraries designed for `clib` return errors through caller-owned storage passed in the call itself, as ZapFast's core does (D49). That storage lives on the Go heap (or in C memory) next to the `Call` that passes it, as in the example, so it has the `Call`'s rule: one caller at a time. `Open` and `Func` read `dlerror` themselves, with the thread pinned (`runtime.LockOSThread`) for that one call. A caller that must read a thread-local error from an existing library brackets the call and the read with `LockOSThread`/`UnlockOSThread`, and the documentation says so.
- **Allocations.** 0 per call after `NewCall`, gated by a benchmark.
- **Memory.** Pointer arguments point to Go heap memory or C memory, never to a goroutine stack (ARCHITECTURE §4). The pointee holds no Go pointers. C must not keep it after the call returns. `clib.CStr(dst, s)` makes NUL-terminated copies into a caller buffer.
- **Foreign threads.** A library's own threads (tokio, in ZapFast's case) may run in the process, and Go's signal handling must tolerate them. We rely on fakecgo setting `iscgo` (D3). This is tested, not assumed: a test fixture library of our own, built with clang in the test as D19 allows for tools, starts threads that spin and block while the Go side runs under `SIGPROF` profiling and async preemption (`SIGURG`).
- **Per OS.** darwin and linux. Windows returns `ErrUnsupported` in this phase.
- **Lifetime.** `Lib.Close` only when nothing can still call into the library. Apps normally never close.

### The Objective-C runtime (`internal/objc`, D36)

- **Classes are defined across files.** `objc.Define(name, super, protocols)` returns a builder. Each feature file adds its methods (`def.Method(sel, types, handler)`) and ivars in its own registration function, and `platform` calls `Register` once at startup. Menus, hosts, drag and drop and accessibility can then each add selectors to `GNUIView` without editing one shared file. Wave 0 splits `platform/platform_darwin.go` (1456 lines) by concern onto this registry.
- **Ivars.** Every runtime class gets one `uintptr` ivar (`def.Index()`) holding an index into a Go-side table (windows, hosts, delegates); `def.Ivar(name)` adds more. This replaces the global `theWindow` (`gpui-ghostty.md` §3.2, item 1) and keeps dispatch allocation-free with many instances.
- **`SendSuper`.** `objc_msgSendSuper` with the `objc_super` in the frame's scratch words (so it works on any frame), for subclasses that override and call through (`NSWindow makeFirstResponder:`, `NSTextView` paste, `performKeyEquivalent:`).
- **Blocks we make: per-request blocks are copied by the runtime, never recycled under a caller.**
  - **Why not global blocks for requests.** A global block is returned unchanged by `_Block_copy`, so AppKit keeps *our* literal's address. If its slot were reused after a cancellation, a late callback would run the next request's handler, and a generation number stored in the reused literal would already belong to the new request.
  - **So a per-request block is a stack-class literal.** It has the `_NSConcreteStackBlock` isa, `BLOCK_HAS_SIGNATURE`, an invoke pointer that is one of our ffi callback entries, and two captured scalars: the slot index and the slot's generation. The literal sits in its slot of the table, valid for the duration of the call that passes it, exactly like a stack block: the issuing call holds a claim until `Request.Sent()`, which then poisons the literal, so an API that kept it uncopied would get a stale call. An API that keeps the block copies it, as the Blocks ABI requires. `_Block_copy` then makes a heap copy that carries its own captured slot and generation, and `Block_release` frees it.
  - **No copy or dispose helpers are needed.** The captures are plain integers; no object is retained. The block's lifetime is the runtime's business; our only state is the slot.
  - **A callback claims its slot atomically.** Each slot has one 64-bit atomic word: the generation (32 bits), a `retiring` bit, and an active-claim count (16 bits). The callback entry claims with a compare-and-swap that succeeds only if the generation equals the copy's captured one and `retiring` is clear, and increments the claim count; on return it decrements the count.
  - **A stale call does nothing to the slot.** A claim that fails (wrong generation, or retiring) is a stale call: it runs only the native acknowledgement the API requires (below), using nothing but the call's own arguments and the copy's captures, never the slot's context, which may already belong to another request.
  - **The generation never repeats** in a slot: 0 is never issued, and a slot whose generation would wrap past 2^32−1 is retired for good when freed (counted in `Leaks()`).
  - **Cancelling never waits.** Cancelling (or completing a one-shot request) bumps the generation and sets `retiring` in one compare-and-swap, so no new claim can succeed. The slot returns to the free list when its claim count reaches 0: at once if no callback is inside, otherwise by the last callback to leave, which sees `retiring` with a count of 0 and frees it. Cancelling never waits for callbacks that are running, nor for callbacks that have not entered (those will fail to claim). So a slot can never be reused while a callback still runs on it, and slots never leak waiting for a callback that may never come.
  - **Process-lifetime blocks** (a fixed dispatcher that carries no per-request state, like today's Metal block at `gpu/metal_darwin.go:295-340`) may be global. They then need both the `_NSConcreteGlobalBlock` isa **and** the `BLOCK_IS_GLOBAL` flag (`1 << 28`): `_Block_copy` tests the flag, not the isa, so a global isa without the flag would be copied anyway.
  - **Multi-shot callbacks** (observers) use selector-based APIs on our runtime classes (`addObserver:selector:name:object:`) rather than blocks, so their lifetime is the observer object's. The AVAudioEngine tap, which only takes a block, is a per-request block whose generation is bumped when the tap is removed.
  - **Cancellation, per API used in this phase.** The AppKit and AVAudio rows were measured by F1's test "block copy per API" (each API copies the block it keeps); the UserNotifications and AVCaptureDevice rows cannot be called from an unbundled test binary and stay **assumptions** until a bundled fixture checks them:

    | API | The block is called | Cancel | Native acknowledgement on a stale call |
    |---|---|---|---|
    | `NSAlert`/`NSSavePanel`/`NSOpenPanel` `begin…completionHandler:` | exactly once when the sheet ends, on the main thread; **0 times if the window closes under the sheet**, so the framework ends an attached sheet with `NSModalResponseAbort` before closing its window (then exactly once) | close the sheet (`endSheet:`); the call still arrives and is dropped | none |
    | `UNUserNotificationCenter` `requestAuthorization…`, `add…withCompletionHandler:`, `getNotificationSettings…` | exactly once | not cancellable; the generation drops it when the requester is gone | none |
    | `AVCaptureDevice requestAccessForMediaType:` | exactly once | as above | none |
    | `AVAudioPlayerNode scheduleBuffer:completionHandler:` | once per buffer: on AVFoundation's threads when it plays out, and **synchronously on the thread calling `stop`** for a buffer still pending then | `stop` bumps the generation | the buffer's lease is released anyway |
    | `AVAudioNode installTapOnBus:…block:` | many times on AVFoundation's threads, until removed | `removeTapOnBus:` then `Cancel`; a call already claimed finishes, no handler starts after | none |
    | `NSFilePromiseProvider` `writePromiseToURL:completionHandler:` (the handler is given to *us*) | — | — | we `_Block_copy` it before storing it, call it exactly once (with an error if the app never answers or the drag is abandoned), then `Block_release` it |
    | `willPresentNotification:withCompletionHandler:` and `didReceiveNotificationResponse:withCompletionHandler:` (given to us) | — | — | called before the delegate method returns, after the response is stored in its reserved slot (D37 class 2); a handler kept past the return would be `_Block_copy`'d first |
  - **Bounds.** Slots live in a fixed table (256). When none is free, the request fails with `ErrBlocksFull` at once; the table never grows. The process has 1024 ffi callbacks (`ffi.MaxCallbacks`); a class takes one, a block kind one.
- **Blocks we are given.** `objc.CallBlock(f, b, args…)` reads the block's invoke pointer and calls it with the block first. A block given to us is called by the framework code that received it, never left to the app, so an acknowledgement cannot be forgotten. A block given to us is only valid for the duration of the call that passed it, so **any block we keep past that call is copied first** with `_Block_copy` (a stack block becomes a heap copy; a heap block is retained) and released with `Block_release` after its one call. Keeping a block goes through one helper, `objc.KeepBlock` (copy, store, and a matching `objc.CallKeptBlock` that calls and releases), so the copy cannot be skipped.
- **Callbacks on foreign threads.** UserNotifications, AVFoundation and file promises call us on GCD threads. GCD makes no promise about how many threads it uses or for how long, so nothing is keyed by thread.
  - **Every callback brings its own frame.** The ffi trampoline reserves an `ffi.Frame` in the callback's own frame on the calling thread's C stack (`CB.Frame()`), valid until the handler returns. Main-thread, foreign-thread and nested callbacks all use it, so nothing is keyed to a thread, there is no frame pool to exhaust, and no callback waits for a frame or needs a fallback path. Nesting depth is bounded by the thread's native stack (about 0.5 KiB of trampoline per level plus the callees' frames), not by us; 32 levels are tested on a Go thread, a fresh pthread and a GCD thread.
  - **Attaching to the Go runtime.** A callback on a thread Go has never seen goes through `crosscall2`'s `needm`, which takes an extra M and may create one. That is a Go allocation the first time the number of threads inside callbacks at once reaches a new high. The steady state (no new high) must allocate nothing.
  - A foreign-thread handler may only copy its arguments (strings as UTF-8 into fixed buffers) into the app's event records (below) and call `platform.Wake`. It never touches AppKit, except calls Apple documents as thread-safe and that we list per use, and it never touches `ui`.
- **F1's feasibility tests (before anything builds on F1):**
  - concurrent callbacks: `dispatch_apply` 64-wide, and 10,000 callbacks from many queues;
  - thread churn: callbacks spread over queues created and released in a loop, so GCD retires and creates threads;
  - cold attachment: the first callback on a fresh `pthread`;
  - **Go allocations measured** with exact `ReadMemStats` deltas (D11), not inferred from the frames being C memory: 0 after the concurrency high-water mark is reached; the allowance before it is reported per new thread;
  - late callbacks: a request cancelled and its slot reused, then the old block called through a heap copy made by `_Block_copy`: the new request's handler is not run;
  - **a callback in flight during cancellation:** a fixture callback claims its slot and blocks inside its handler; the request is cancelled from the main thread (which returns at once), a new request is issued, and the test checks that the new request did not get that slot until the blocked callback returned, that the stale acknowledgement touched no slot context, and that the slot is then reused with the new generation;
  - the flags: a global block's `_Block_copy` returns the same pointer, and a per-request literal's returns a heap copy with the captures intact;
  - **block copy per API:** for each API in the cancellation table that a test binary can call, the test checks that the API copies the block it keeps (the literal is poisoned by `Sent`) and how many times it calls it, including on window close, node stop and tap removal. An API that misbehaves gets a recorded workaround before its service is built;
  - **callback progress:** 64 concurrent chains from many queues, each nesting three levels deep and mixing one-shot completions and multi-shot observations, are all delivered exactly once with a frame of their own per level; 32-level nesting on each kind of thread;
  - **frames under the runtime:** nested chains on the main thread and on GCD threads keep their frames intact while their goroutine stacks grow and move, handlers spin (async preemption) and block through forced GCs, under the CPU profiler;
  - **the generation rule:** a slot pushed to the end of its generations is retired, and old copies stay stale.
- **Main-thread rule.** The global `objc.F` frame stays main-thread only, checked in `gnuidebug` builds. Class and selector lookups are resolved at init, so foreign threads never look anything up.
- **Leak counters.** Live, kept and global blocks, retained delegates, observers, stale calls, refusals and retired slots are counted (`objc.Leaks`), for the open/close leak gate.

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
  - `QuitDeadline`: optional; zero (the default) means none. With a duration, a quit request still unanswered after it gets `QuitDeadlineAction`, which defaults to `Cancel`; `Allow` (force-quit) exists only as the app's explicit choice. Either way the app receives `EvQuitDeadline`, an error event.
  - `Policy`: Regular, or Accessory (no Dock icon).
  - `Mac`: grouped OS-only options (`go-and-others.md` §3.3).
- **The NSApplication delegate** is a runtime class, set before `finishLaunching`. It handles:
  - `applicationShouldHandleReopen:hasVisibleWindows:` → `EvReopen`;
  - `applicationShouldTerminate:` → `EvQuitRequested{Reason}`, then `NSTerminateNow`, `NSTerminateCancel` or `NSTerminateLater` (the quit policy, below);
  - become and resign active;
  - `application:openURLs:` and the `kAEGetURL` Apple event, registered in `applicationWillFinishLaunching:` so a launch by URL is not lost;
  - `applicationDockMenu:`, which `app/dock` supplies;
  - will-terminate.

  It also observes the `NSWorkspace` notifications: will sleep, did wake, screens changed, session active.
- **One app-wide pump.** It is the same `nextEventMatchingMask` loop (D5), owned by the app, not by a window (today it is `(*Window).Wait`). It blocks with zero windows open, and the idle rules hold with zero, one or several windows.
- **App events are dispatched by a run-loop source, not by the pump.**
  - Native code can spin the run loop without returning to our pump: `NSTerminateLater` runs it in `NSModalPanelRunLoopMode` until `replyToApplicationShouldTerminate:`; menu tracking runs it in `NSEventTrackingRunLoopMode`; sheets run it modally. With zero windows there is no frame to piggyback on either.
  - So app events have their own dispatcher: a `CFRunLoopSource` (version 0) on the main run loop, added for `kCFRunLoopCommonModes`. AppKit puts the default, modal-panel and event-tracking modes in the common set, so the source fires in every one of those loops. Producers mark their record ready, call `CFRunLoopSourceSignal` and `CFRunLoopWakeUp` (both thread-safe), and the source's perform callback delivers ready events to `Handler.Event` on the main thread.
  - It is independent of frames: it fires with zero windows, while every window is occluded, and inside nested loops. The frame handler (D30) keeps windows drawing in those loops through the display links, which are also in the common modes.
  - **Never inside Build.** The run loop runs only in the pump and in native loops, and native loops start only at safe points (D30), so the source can never fire during Build or encoding. A `Handler.Event` that opens a window or a sheet has it queued to the next safe point, as today.
  - **Safe-point work runs from the dispatcher too.** While the outer pump is suspended inside a native loop (`NSTerminateLater`'s modal-panel wait, menu tracking, a sheet), it never reaches its own safe point. So after delivering events, the source's perform callback runs the deferred safe-point queue itself: windows opened or closed, sheets and alerts begun, hosts created or released, menu rebuilds. The callback runs between events, never inside Build or encoding, so it is a safe point by the D30 definition. Without this, a "Save changes?" sheet asked for during a pending quit would never open.
- **App events.**
  - `app.Event` is a fixed-size, pointer-free record: kind, source, window, id, two integers, and a data span.
  - `e.Data()` is valid during the `Event` call only.
  - Kinds: `EvReopen`, `EvActivated`, `EvDeactivated`, `EvQuitRequested`, `EvOpenURLs`, `EvOpenFiles`, `EvWillSleep`, `EvDidWake`, `EvScreensChanged`, `EvWillTerminate`, `EvCommand` (app-wide menu commands; D29's planned `CommandHandler` becomes this), and `EvService` (results and clicks from `app/*` services, tagged by source).
- **Delivery has four classes. Nothing the framework has accepted is lost: observations are coalesced by design, `app.Post` refuses (returns false) instead of dropping, and every other event is either reserved before the action that produces it or refused at its source, visibly:**
  1. **Main-thread native events** (reopen, quit requests, open URLs and files, activation, sleep and wake, screens, will-terminate, menu commands). AppKit calls the delegate on the main thread, from a run loop, at a safe point. They are delivered **synchronously** to `Handler.Event` from the delegate method, with no queue. Events that arrive before `Start` has returned (a launch by URL) are held in a startup list, the one place this path allocates.
  2. **Request completions and responses** (an alert's button, a panel's paths, a permission answer, a notification's post result, a promise to fill, **and a notification's click, action or reply**). Every request takes a **ticket** bound to a completion record reserved when the request is made, from a fixed table per service (64; 256 for notification responses). A full table fails the *request*, synchronously, with `ErrBusy`; a completion never needs queue space, so it cannot be lost. The foreign thread writes its record, marks it ready and signals the source.
     - **Notifications reserve their response slot when they are posted.** `notify.Post` of a notification with a click payload, actions or a reply field reserves a response record (with room for the reply text, up to 4 KiB). When a response arrives, the delegate stores the action and the reply text in that record **before** it calls the system's completion handler, so an accepted reply cannot be lost after the system considers it delivered. The slot is freed when the response is delivered, or when the notification is removed (`Remove`, `RemoveThread`, or found gone from Notification Center at launch).
     - **No reservation, no actions.** When the table is full, the notification is posted without its actions or reply field (a plain banner whose click only activates the app) and `Post` reports `PostedWithoutActions`; or, if the app set `notify.Options.EvictOldest`, the oldest delivered notification is removed from Notification Center to free its slot. Notifications delivered by an earlier run are re-reserved at launch (`getDeliveredNotificationsWithCompletionHandler:`); those that do not fit are reported to the app, which may remove them.
  3. **Unsolicited requests from outside the process** (a second instance's arguments, over `app/instance`'s socket). Each source has its own reserved ring (32 records, 16 KiB). A request is accepted only if it fits; the primary then answers the secondary `accepted`. When the ring is full the primary answers `busy`, and the secondary retries with backoff for up to 2 s, then exits with an error message saying the running instance did not respond. Nothing is accepted and then dropped.
  4. **Observations** (recorder levels, playback position, thermal state, network changes): one latest-value slot per source, coalesced, never queued. Lossy by design, and the only class where a value is replaced without being seen.
  - **`app.Post(e, data)`** is the app's own path from its goroutines: a reserved ring of its own; it is allocation-free, never blocks, and returns false when the ring is full, so the caller keeps the event and retries. Framework producers never use it.
  - **Releasing native resources never depends on delivery:** block copies, buffer leases, pixel buffers and completion handlers are released or called by the native callback itself.
- **The quit policy.** Every route arrives as `EvQuitRequested{Menu|Dock|Logout|Update|Signal}`: ⌘Q (the native menu, N1), Dock → Quit, logout and `a.Quit()`.
  - `applicationShouldTerminate:` delivers `EvQuitRequested` synchronously (class 1). The default handler (`app.DefaultHandler`, embeddable) asks each window's `CloseRequested`; one refusal cancels the quit. `a.ReplyQuit(allow)` answers.
  - **An answer given inside `applicationShouldTerminate:` is latched, not sent.** If the handler answers synchronously, or a `ReplyQuit` arrives before the delegate method returns, the answer is recorded and the method returns `NSTerminateNow` or `NSTerminateCancel` directly. Only when no answer exists at return does it return `NSTerminateLater`; from then on `ReplyQuit` calls `replyToApplicationShouldTerminate:`. The framework never calls `replyToApplicationShouldTerminate:` before `NSTerminateLater` has been returned.
  - While a deferred answer is pending, AppKit's modal-panel loop runs, our source keeps dispatching events and safe-point work (a window's "Save changes?" sheet opens, a worker finishes), and windows keep drawing.
  - **Why `NSTerminateLater` and not cancel-then-restart.** The other design returns `NSTerminateCancel`, runs the policy in our own loop, and calls `terminate:` again once approved. `NSTerminateLater` is the documented way to defer the answer. That cancelling during a logout or restart abandons it, and that a later `terminate:` does not resume it, is **an assumption** (Apple's documentation says a cancel stops the logout; the resume behaviour is not documented); F3's feasibility test "logout behaviour" checks both on the MacBook with a real logout of a test user. The common-modes source is what makes `NSTerminateLater` safe for us.
  - **No automatic answer.** By default there is no watchdog: the app's quit policy owns the answer, however long it takes. An app may set `QuitDeadline` (above); its action defaults to cancelling the quit, with `EvQuitDeadline` reported, and force-quitting happens only if the app chose `Allow`. Whatever timeout the system applies at logout is the system's, not ours, and how long it is is not documented; the guide says to answer promptly when the reason is `Logout`.
  - The hard-coded ⌘Q (`platform/platform_darwin.go:649`) goes.
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
- **Trackpad gestures:** `EvMagnify` (`magnifyWithEvent:`, with the magnification and the phase), `EvSmartMagnify` (`smartMagnifyWithEvent:`, the two-finger double tap) and `EvRotate` (`rotateWithEvent:`). `ui` delivers them to the box under the pointer at Began and latches the gesture there, as scrolling does, through `Signal.Magnify` and `Signal.Rotate`. ZapFast's media viewer zooms with a pinch (`zf:src/ui/image_preview.rs:1346-1377`; `egui.md` §2.4, "Pinch, rotate and smart zoom").
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
  - wheels stay hard-clamped; on macOS they jump, as AppKit's do, and elsewhere they glide (review decision); Windows treats high-resolution wheels (fractions of a notch) as precise, like its touchpads;
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
- **Native colours win over a fixed contrast ratio.** The token values of System roles are exactly the system's colours, including their alpha. Light `secondaryLabelColor` is `#0000007F`: composited on white it is about 3.95:1, under the 4.5:1 that `rex-design-system.md` §2.1 asked of `Label2`. We keep the native value.
  - **The contrast check composites first.** Today `theme.Contrast` (`theme/color.go:81`) compares two colours' luminance and ignores alpha. It gains `theme.ContrastOn(fg, bg, under)`: the foreground composited over its real background, which is itself composited over what is under it (a pane over the Rex field, a fill over the window).
  - **The floors:**
    - primary text (`Label`) ≥ 4.5:1 on every background it is used on;
    - secondary, tertiary and quaternary text and placeholders ≥ AppKit's weakest own composited contrast for the same role in the same appearance: on the window, group box, stripe, sidebar, toolbar capsule and text field (light `Label2`: 3.95:1 on the window, 3.84:1 at its weakest), computed in the test only from colours measured from AppKit (the live or measured system values and D0's chrome), never from Rex-only roles. This floor is AppKit's, and the documentation says so (D39, amended);
    - checked in light, dark and both high-contrast appearances, on every background a recipe pairs the role with, Rex's field and glass included.
  - So the check bites where it matters: Rex's own backgrounds must not make secondary text weaker than AppKit makes it.

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
  - traffic-light centres within ±0.5 pt of the style's token (the close button's centre, below), read from AppKit by `RunLive`.
- **Traffic-light units.** AppKitRef's `metrics.json` (`windows.*.trafficLights`) gives each button's frame: `x` and `yFromTop` are its top-left origin, 14 × 14 pt, with `centerYFromTop`. `app.Config.TrafficLights`, `theme.TrafficCentrePt` and the platform's `layoutChrome` take the **close button's centre**, in points from the window's top-left. The window-chrome tokens therefore store centres: x = origin + 7.

  | Style | Titlebar | Close, minimise, zoom: centre x | Centre y |
  |---|---|---|---|
  | Plain (titled, full-size transparent) | 32 | 16, 39, 62 | 16 |
  | Compact (`toolbarUnifiedCompact`) | 40 | 19, 42, 65 | 20 |
  | Unified (`toolbarUnified`) | 52 | 26, 49, 72 | 26 |
  | Rex | 46 | 23, 46, 69 (today's `theme.TrafficCentrePt`, 23 pt spacing) | 23 |

  The spacing is 23 pt in every style. `rex-design-system.md` §2.3 lists frame origins (9/32/55 …) and, for Rex, 21/43/65, which match neither unit; this table supersedes it. The token-to-option conversion happens in one place, where `app` builds `platform.Options` from the window's chrome, and a unit test checks it against the committed reference numbers.

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
- **Offsets: the app sees UTF-8 bytes, AppKit sees UTF-16 units.**
  - Every range the app reads or passes (`Selection`, `Marked`, `Replace`, span styles) is a byte range into the app buffer, always on a UTF-8 scalar boundary. `NSTextView` ranges are UTF-16 code units.
  - The editor keeps a mapping built on each `textDidChange:` copy: a pointer-free checkpoint table (the UTF-16 offset every 64 bytes) over the buffer, so a conversion is a table lookup plus a scan of at most 64 bytes, both ways. A UTF-16 offset inside a surrogate pair rounds down to the pair's start; a byte offset inside a scalar is refused (`ErrNotBoundary`).
  - The selection is reported on `textViewDidChangeSelection:` as bytes (`Selection{Anchor, Focus}`), with no Go allocation.
- **Marked text (IME composition) is state the app can see and must respect.**
  - `Marked() (Range, bool)` reports the composition in bytes, while `hasMarkedText` is true; `Changed` still fires as the composition changes, with the `Composing` flag.
  - While text is marked, the editor refuses programmatic replacement and completion handoff (below): `Replace` returns `ErrComposing`, and completion popups should close. Committing (`insertText:replacementRange:`) or `unmarkText` clears the state and fires `Changed` without the flag.
  - The app never draws or rewrites marked text; the hosted view owns it.
- **Range replacement keeps undo.** `Replace(r Range, text []byte, sel Range)` replaces bytes `r` with `text` and puts the selection at `sel` (bytes, in the new text). It runs as a user edit: `shouldChangeTextInRange:replacementString:` → `replaceCharactersInRange:withString:` on the text storage inside `beginEditing`/`endEditing` → `didChangeText`. So the change is one undo step (⌘Z restores the typed `@pa` exactly), and the delegate and the maximum length apply. Mention styles on the replaced range come from the app's spans after `Changed`.
  - Setting the whole text (`SetText`: restoring a draft, editing a sent message) is the other programmatic path; it clears the undo stack, as AppKit does when a document is opened.
- **Completion handoff.**
  - The app opens a completion popup (mentions, emoji `:shortcode`, `/` commands) when it decides to from `Changed` and `Selection`, anchored at `CaretRect` (from `firstRectForCharacterRange:actualRange:`, converted to window points). The popup is drawn by `ui` (D41's z-order rule: a host is hidden only when a popup *overlaps* it, so the popup is placed above or below the editor).
  - While the app holds `ed.SetCompletionActive(true)`, the text view's `doCommandBySelector:` diverts `moveUp:`, `moveDown:`, `insertTab:`, `insertBacktab:`, `insertNewline:` (and the Enter-policy variants), and `cancelOperation:` to Go as `EvHostCommand{Up, Down, Tab, Backtab, Accept, Cancel}`, synchronously, and returns YES so the text view does nothing. Every other key goes to the text view, so typing continues to filter the list.
  - Accepting calls `Replace` with the trigger range; cancelling calls `SetCompletionActive(false)`. Losing first responder, or marked text starting, ends the completion and fires `Cancelled`.
  - Without an active completion, Tab, Shift+Tab, Escape and Enter keep the host-command routing above (focus traversal and the Enter policy).
- **Programmatic styling:** style ranges (mentions) from app spans, as temporary attributes, so styling never enters the undo stack.
- **The OS brings** spell check, autocorrect, text replacement, dictation, the emoji palette, Writing Tools, Services, undo and VoiceOver (`gpui-ghostty.md` §2, "Multi-line editor").
- **Pasting images.** An `NSTextView` subclass overrides `readSelectionFromPasteboard:` to route images to the app.
- **Tested on a real hosted view** (N4's live matrix, below): completion navigation and acceptance, Edit-menu editing on the hosted view, focus traversal in and out, IME acceptance through the `NSTextInputClient` calls, and byte/UTF-16 mapping over emoji, combining marks and RTL text.

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
- **Selection runs in document order, not over built boxes.** A transcript is virtualised (D43): most of a long selection has no boxes, so a selection anchored by box keys could not survive scrolling.
  - **Endpoints.** A `ui.SelectionScope` holds two endpoints, anchor and focus, each `Pos{Item uint64; Part uint16; Off uint32}`: the app's stable item id, a text part within the item (body, quote, caption), and a byte offset into that part. Two sentinels let an endpoint sit in an item that was never built, whose part lengths nobody has measured: `Part: ui.PartEnd` is the end of the whole item, and `Off: ui.OffEnd` the end of a part. Endpoints are app data; no box needs to exist for them.
  - **Enumeration.** The scope orders and walks items through an `Enumerator`:

    ```go
    type Enumerator interface {
    	IndexOf(id uint64) (int, bool) // current position, for ordering endpoints
    	First() (uint64, bool)
    	Last() (uint64, bool)
    	Next(id uint64) (uint64, bool) // the item after id, in document order
    }
    ```

    - **In a `VList`, the framework provides it:** `*VListState` implements `Enumerator` from its dense id array and id→index table (below): `First`, `Last` and `Next` are index lookups, O(1). So ⌘A (from `{First, 0, 0}` to `{Last, PartEnd, 0}`) and enumeration of the selected range need nothing from the app and build nothing. A pointer in an interface does not allocate.
    - **Outside a `VList`** (a scope over the app's own layout, or several lists), the app implements `Enumerator` over its model and passes it in `ScopeOpts.Items`.
    - The order is persistent across rebuilds, prepends and scrolling: endpoints compare by `IndexOf`, then part, then offset (`PartEnd` and `OffEnd` sort last).
  - **Building.** Each `RichText` inside the scope declares its `(Item, Part)`. When an item is built, the scope gives its text the clipped range it covers (all, none, a prefix, a suffix, or a middle), so the highlight is drawn only for built items, and rows in between are selected without being built.
  - **Dragging** moves the focus to the position under the pointer; past the list's edge it auto-scrolls (C1) and moves the focus to the first or last built item's edge, so a drag through three viewports selects everything it passed.
  - Double-click selects a word and triple-click a paragraph (in one part). ⌘A in the scope selects from the first item's start to the last item's end through the `Enumerator`, without building anything.
  - **Edits to the list.** Prepending or appending items leaves both endpoints untouched (ids are stable). When the item holding an endpoint is removed (`VListState.Splice`, below), the endpoint moves to the start of the next surviving item (focus) or the end of the previous one (anchor), and the scope reports `SelectionChanged`. A reset clears the selection.
  - **Copying the whole range.** `sc.Selected() (from, to Pos, ok bool)` gives the normalised range. ⌘C (the Edit role) and Services hand the app that range. `ui.ForEachSelected(sc, fn)` walks it through the scope's `Enumerator`, calling `fn(id, fromPart, fromOff, toPart, toOff)` once per item: whole items in the middle get `(0, 0, PartEnd, 0)`, and the first and last get their cut points (with the sentinels where the endpoint is in an unbuilt part). It allocates nothing. The app fetches each item's text from its model and assembles the result: ZapFast formats `[time, date] Name:` (`zapfast.md` §2, "Text selection"). The framework never holds or copies the text of unbuilt rows.
  - The highlight is the `TextSelection` role, unemphasised when the window is inactive.
  - Services and Look Up read the same range (N6).
  - **Tests:** a scripted drag across three viewports with auto-scroll; ⌘A then ⌘C over 10,000 `VList` rows of which 30 were ever built, enumerated by the framework and compared byte for byte with the expected text; the same through an app-provided `Enumerator` outside a `VList`; an endpoint left at `PartEnd` in an unbuilt item; skipped rows (scrolled past without layout) inside the range; 100 rows prepended mid-selection, endpoints and copied output unchanged; the endpoint's item deleted; the copied output's first and last partial parts.
- **Allocations:** hover, click, select and copy allocate 0 once warm, under the gate.

### Variable-height virtual lists (`widget/base`; D43)

```go
var st base.VListState           // app-owned, pointer-free: anchor id, Offset, AtEnd, ids, heights (Fenwick), estimated bitset
st.Reserve(n)
st.Splice(at, removed, insertedIDs) // insert, remove or replace a run; Prepend and Append are Splice at 0 and at n
st.Move(from, n, to)                // reorder a run; heights travel with their items
st.Reset(ids)                       // a new list; the anchor survives if its id is still present
st.Invalidate(i); st.ScrollTo(i, base.AlignCenter)
v := k.BeginVList("transcript", &st, base.VListOpts{Estimate: 64, StickToBottom: true})
for i := v.First; i < v.Last; i++ { v.Item(i); /* any boxes */ v.EndItem() }
k.EndVList(&v)
```

- **The position is an item plus an offset** (`go-and-others.md` §3.4), not a pixel offset into a total. **The item is held by its stable id** (`AnchorID`), not by index, so every edit can keep the view still.
- **Ids.** The state keeps the item ids (`[]uint64`, app-supplied and unique) in order, with an id→index table (open addressing, pointer-free, rebuilt incrementally on edits). The app's model and the list change together: the app applies an edit to its model and calls the matching `Splice`, `Move` or `Reset` in the same Build. `v.Item(i)` checks the id at `i` in debug builds.
- **Edits keep the anchor:**
  - an edit entirely above the anchor shifts its index and leaves the view in place (this is how prepending history works);
  - an edit entirely below changes nothing on screen;
  - deleting the anchor item moves the anchor to the first surviving item after the removed run (or the last before it, at the end), at offset 0, so the content under the top edge is the next message rather than a jump;
  - a `Move` that carries the anchor keeps it on the same item, wherever it goes; heights move with their items;
  - `Reset` keeps the anchor if its id is present in the new list and otherwise starts at the top (or the bottom with `StickToBottom`).
  - These rules are fixed before C1's interface freezes, because ZapFast deletes messages for everyone, inserts in the middle when history backfills, and reorders the chat list (pins, new messages).
- **Heights.** Items in view and in the overscan are measured after layout, and their heights are stored. Unmeasured items use estimates. Totals and offset-to-item lookups use a Fenwick tree: O(log n).
- **The cost of edits, stated honestly.** Ids and heights live in arrays with spare capacity at both ends, and the id→index table stores positions relative to a base, so **prepend and append of k items are O(k)** (plus the Fenwick updates for those k), with an O(n) regrow when a reserve runs out. **`Splice` in the middle and `Move` are O(distance)**: the ids and heights between the two positions shift (a `memmove`), every shifted id is re-indexed in the table, and the Fenwick tree is rebuilt over the shifted range (O(distance)). Moving the bottom item of a 1M-item list to the top is O(n).
  - **Benchmarked:** a distant `Move` (index 999,999 to 0) and a mid-list `Splice` on 1M items, CPU time reported, and gated at 2 ms when applied within one frame.
  - **Sliced when it is too long.** An edit whose distance exceeds what fits in the 2 ms budget (measured, then fixed as a constant) is applied as a sliced rebuild: the new ids, heights, Fenwick tree and id table are built in a back buffer over successive frames, 1 ms per frame (D27's slicing), while every reader (layout, the anchor, selection, `Enumerator`, semantics) keeps using the front buffer. The buffers are swapped at a frame boundary, before Build, so **no reader ever sees a half-updated index**. Edits that arrive during a rebuild are queued and applied to the back buffer before the swap; the app's model is ahead of the list for those frames, so `v.Item(i)` keeps reporting the front buffer's ids and the app builds by id. The back buffer is allocated the first time a sliced rebuild happens (about 40 MB for 1M items) and kept; it does not exist for lists that never need it.
- **Stability:**
  - when items above the anchor change height, the view keeps still;
  - prepending older history shifts `First` and leaves the view in place;
  - at the end with `StickToBottom`, the bottom stays pinned as items arrive or grow (images loading);
  - `ScrollTo` jumps to an item with an alignment, for replies and search hits.
- **The scroll bar** works from the estimated total, refined as items are measured.
- **Rules:** keyboard navigation, semantics (index of total), 0 allocations (edits included, within `Reserve`), and under 4 ms of CPU per frame scrolling **1M items of mixed heights** on the MacBook and in CI.
- **Tests:** a distant move and a mid-list splice on 1M items taking the sliced path, with selection, the anchor and the `Enumerator` checked on every frame of the rebuild to see only the old or the new order, never a mix; deletion above the anchor and of the anchor item, with the first visible pixel row unchanged and on the next item respectively; insertion in the middle, above and below the anchor; a reorder that carries the anchor and one that does not; heights following moved items; `Reset` with and without the anchor's id; prepending while `StickToBottom` is off and on.
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
  - **Leases until the GPU is done.** An import takes a lease that retains the `CVPixelBuffer` and both `CVMetalTexture`s (Y and CbCr). The lease is attached to the frame's command buffer and released only when that command buffer has completed, because the Metal textures alias the pixel buffer's memory and Core Video may recycle it as soon as it is released.
  - **Retirement.** Each ring slot of the renderer (`ringSlots = 3` frames in flight, `gpu/renderer_darwin.go:30-32`) owns the leases of the frame encoded into it. When `Encode` reuses a slot, its existing `retire` waits for that slot's previous command buffer (`waitUntilCompleted`, with no timeout, `renderer_darwin.go:355-368`: that wait is today's frame pacing and is unchanged) and then releases the slot's leases on the main thread. `CVMetalTextureCacheFlush` runs after release.
  - **When no frame comes to reuse the slot** (the video's window closed or went idle), retirement must not wait for one, and must not block either. A command buffer that carries leases, or that belongs to a closing window, gets the process-lifetime completion block (a global block with no per-request state, D36) through `addCompletedHandler:`. It runs on a Metal thread and only wakes the dispatcher, with its callback's own frame (D36); the dispatcher then reads each pending slot's `status` without waiting and releases the leases of the completed ones.
  - **Failure paths release too:** a frame that encodes but is never committed (no drawable, encode error, a window closing mid-frame) releases its leases at once, since the GPU never saw them; today's comment that an uncommitted command buffer does not protect its slot (`renderer_darwin.go:378`) becomes this rule.
  - **Closing a window does not wait for the GPU.** The window is ordered out at once, its pending command buffers are marked, and its layer and textures go on a retiring list released by the dispatcher as above. There is no timeout and no bounded-latency promise: **nothing the GPU may still read is ever freed on a timer.** A GPU that never completes leaves the window's memory on the retiring list, which is counted and visible in the leak gate.
  - **Bounded.** At most 16 imports per frame, so at most 48 live leases in the ring; the storage is a fixed array in the ring slot. A 17th import in one frame returns `ErrBusy` and the box draws its placeholder.
  - **Tests (live, MacBook and macOS CI):** a video playing while the window live-resizes, while its list scrolls it in and out of view, and while the window closes mid-playback; Core Video's live pixel-buffer count and our lease count return to their baseline, with no Metal validation errors.

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
  - Requests go in; answers, clicks and changes come out as `EvService` events, by the delivery classes of F3: completions and notification responses through slots reserved with the request or the post (class 2), outside requests accepted into a reserved ring or refused with `busy` (class 3), changes as observations (class 4). No framework service uses `app.Post`.
  - Nothing runs per frame. Payloads may allocate on delivery; they are rare.
  - System frameworks (UserNotifications, Security, ServiceManagement, AVFoundation) are loaded with `dlopen` on first use, after the first frame. The exception is the notification delegate, for apps that opt in.
  - The app ID drives the paths, preferences, Keychain service, instance lock and notifications (`go-and-others.md` §3.2).
  - Services with no bundle report unavailable instead of throwing. `notify.Available()` is false when unbundled.
- **The services:**

| Package | API (shape) | macOS |
|---|---|---|
| `app/notify` | `Available`, `RequestPermission`, `Status`, `RegisterCategories` (actions, reply, destructive), `Post(Note{ID, Thread, Title, Subtitle, Body, Sound, Attachment, Category, UserData}) (PostResult, error)` (the result says whether the response slot was reserved or the note went out `PostedWithoutActions`), `Remove(ids)`, `RemoveThread`, `Foreground(show)`; event: response (action, reply text, user data) | `UNUserNotificationCenter`. The delegate is set in `applicationWillFinishLaunching:` when the app opts in, so a click that launched the app is not lost (`electron-tauri.md` §2, "Notifications"). Categories are registered as a union. The tag is the request id (`gpui-ghostty.md` §2) |
| `app/dock` | `SetBadge`, `SetBadgeCount`, `Bounce(critical)`, `CancelBounce`, `SetMenu(model)` | `NSDockTile`, `requestUserAttention:`, `applicationDockMenu:` |
| `app/tray` | `New(Options{Symbol or PNG, Template, Tooltip})`, `SetIcon`, `SetTitle`, `SetMenu(model)`, `Remove`; menu choices arrive as commands | `NSStatusItem` with an NSMenu from N1 |
| `app/secret` | `Open(service).Get/Set/Delete(account)`; `ErrNotFound`, `ErrDenied`, `ErrLocked`, `ErrUnavailable`; blocking, so call it from a task | `SecItem*` C API with `kSecClassGenericPassword`; no data-protection keychain (no provisioning profile needed) |
| `app/paths`, `app/prefs` | `Config/Data/Cache/Logs(id)`; `prefs.WriteAtomic` (temp file, fsync, rename; `F_FULLFSYNC` optional), `prefs.JSON[T]` with defaults, loaded synchronously at startup, saved debounced on a worker | `~/Library/Application Support/<id>`, `Caches/<id>`, `Logs/<id>` |
| `app/log` | `Setup(Options) (*slog.Logger, error)`: stderr plus a rotating file; `debug.SetCrashOutput`; `PreviousCrash()`; never on the frame path | — |
| `app/winstate` | `Load(id)` before the window opens, `Apply(cfg, state, screens)` clamped to a visible screen, `Save` debounced; the state includes frame, maximised, fullscreen, screen, and app-owned bytes | our own file, not `setFrameAutosaveName:`: one rule on every OS, app bytes alongside, and read before the window exists (`electron-tauri.md` §2, "Window state") |
| `app/instance` | `Acquire(id)` → primary or secondary; `Secondary.Send(args)` returns `Accepted`, or an error after the primary answered `busy` for 2 s or did not answer; the primary gets an event only for accepted requests | `flock` in Application Support plus a Unix socket only the user can open (`zapfast/src/single_instance.rs`) |
| `app/shell` | `OpenURL`, `OpenPath` (`fs.Open` stays), `Reveal(paths)`, `Trash(path)` | `LSOpenCFURLRef` on a worker; `activateFileViewerSelectingURLs:`; `trashItemAtURL:` |
| `app/login` | `Status`, `Enable`, `Disable`, `OpenSettings` for the app itself; `Agent(name)` with the same methods for a bundled launch agent | `SMAppService mainAppService` (macOS 13+) launches the app with no arguments, so it cannot start hidden. `SMAppService agentServiceWithPlistName:` registers a launch agent that `gnui package` generates into `Contents/Library/LaunchAgents/` from `gnui.json` (`loginAgents`): its `Label` is `<app id>.<name>` and its program is `/usr/bin/open -g -b <app id> --args <args>`, both from the real identity, never a fixed string. An app that starts hidden at login uses an agent with `--start-hidden` |
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

- **`gnui.json`.** The standard library has JSON (`electron-tauri.md` §4): id, name, version, build, category, minimum system, icon, resources, frameworks (nested dylibs), URL schemes, document types, usage strings, entitlements, login agents (`loginAgents`: name and arguments; the label and program are derived from the id), extra plist.
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
4. **This plan's DECISIONS entries** (D34–D48, and ZapFast's D49–D51) are in `DECISIONS.md` on this branch, written at your request on 2026-10-18; you revise them at merge. D38 may be reconciled with the entry from `p6-textopsz`.

Phase 7's Z0 (the Rust ABI) starts now, in the ZapFast repo.

### Track F: foundations (first; it unblocks the others)

- **F1. The Objective-C runtime:** the class registry with ivars, `SendSuper`; per-request stack-class blocks with generation-checked slots and the per-API cancellation table; process-lifetime global blocks with `BLOCK_IS_GLOBAL`; calling and keeping blocks we are given; each callback's own frame on the C stack (no lease pool); 1024 callbacks; the generation rule; the lookup rule; leak counters. **F1's feasibility tests (Contracts, "The Objective-C runtime") run first**, and nothing builds on F1 until they pass: concurrent callbacks, thread churn, cold attachment, measured Go allocations, late callbacks after a cancelled and reused slot, a callback in flight during cancellation, the block flags, block copy per API, callback progress, frames under the runtime, and the generation rule. **Done 2026-10-19** (D36's evidence). *Opus (high).* M.
- **F2. `clib`**, with the fixture-library tests (threads, signals, float arguments and results, an error returned through caller-owned storage from a call that hopped threads) and its documentation, including the rule on thread-local errors. *Opus (high).* S–M.
- **F3. The application object:** the delegate; the app-wide pump with zero windows; the common-modes run-loop source that dispatches app events; the safe-point queue run from the dispatcher while the pump is suspended; the four delivery classes, with tickets, reserved response slots for notifications and accept-or-busy rings; reopen; the quit policy (answers latched inside `applicationShouldTerminate:`, `NSTerminateLater` otherwise, the optional `QuitDeadline`); open-URL and open-file events (with the startup list); sleep and wake, screens, activation policy; `app.Main`, `Handler` and `Post`. *Led by me, with an Opus (high) agent.* M.
  - **Tests (macOS CI, through AppKit state; T2's harness):**
    - Dock Quit (a `kAEQuitApplication` Apple event the test sends to its own process; that a self-addressed Apple event needs no Automation grant is an assumption, checked the first time the test runs in CI, with a fallback of calling the handler `NSApp` installs for that event) and ⌘Q through the menu, each with zero windows open, one window, and two;
    - a reply delayed by 2 s while a worker posts and an alert sheet completes, all delivered during the modal-panel loop; a "Save changes?" sheet requested by the handler during that wait opens (the safe-point queue runs from the dispatcher) and its answer then allows the quit;
    - answers inside `applicationShouldTerminate:`: a synchronous allow returns `NSTerminateNow`, a synchronous refusal `NSTerminateCancel`, and a `ReplyQuit` posted from a nested call before the method returns is latched; in none of them is `replyToApplicationShouldTerminate:` called (the test swizzles it to count calls);
    - a refusal, then a second quit that is allowed;
    - a handler that never replies: with no `QuitDeadline` the app is still running and still answering 60 s later; with `QuitDeadline: 1s` the quit is cancelled and `EvQuitDeadline` arrives; with `QuitDeadlineAction: Allow` the app quits;
    - **saturation:** `app.Post` and the observation slots flooded from 8 goroutines while 64 alert and panel completions (a full ticket table; a 65th request fails with `ErrBusy` at once), responses to 32 posted actionable notifications (each with a reserved slot; a 257th actionable post comes back `PostedWithoutActions`), 40 second-instance requests (32 accepted, 8 answered `busy` and accepted on retry once the ring drains) and a quit request arrive. It passes when every completion, every notification response with its reply text, every accepted second-instance request and the quit are delivered; every native acknowledgement is made; the only losses are coalesced observations and `app.Post` calls that returned false (both counted); and nothing deadlocks.
  - **Feasibility test "logout behaviour"** (on the MacBook, by the F3 agent, with a dedicated test user so your session is never logged out): `NSTerminateLater` held for 10 s during a real logout of that user, then allowed, completes the logout; `NSTerminateCancel` during logout stops it; a later `terminate:` does not resume it. Until it runs, those statements are assumptions.
- **F4. More than one window on macOS (D30):**
  - the shared device and atlas;
  - the window registry on ivars;
  - safe points and per-window pacing;
  - the frame handler for tracking loops;
  - the window commands, with fullscreen handled for the custom titlebar and tabbing off.

  On Windows and Linux, a second window returns `ErrUnsupported`. *Opus (high).* L.
- **F5. Keys and gestures:** punctuation, the numpad, F13–F20, `EvModifiers`, the selector bindings, `performKeyEquivalent:`, character matching, ctrl-click, the system click count, secure input; `EvMagnify`, `EvSmartMagnify` and `EvRotate` with phases, latched to the box under the pointer, and `Signal.Magnify`/`Signal.Rotate`. Gesture tests on the live path: synthetic magnify and rotate events built from `CGEvent`s (`eventWithCGEvent:`) and sent through `[NSApp sendEvent:]` in `RunLive`. If AppKit will not deliver synthetic gesture events, the routing is tested below `platform` and the live check becomes a line of the checkpoint-1 hand list, reported as such. ZapFast's Z3 (the media viewer's pinch zoom) depends on this. *Sonnet.* S–M.
- **F6. Trackpad scrolling:** phases, latching, axis lock, rubber-banding, springs, stop on touch, scroller style. *Opus (high).* M.
- **F7. Text at the point size,** the `FontDesc` additions and the `NSFont` advance test; refitting rexlook's text with D6. *Sonnet, with the glyph-cache key change reviewed by an Opus agent.* S–M.

### Track D: the Rex design system

- **D1. Tokens v2** and the live system values: colours (System roles exactly the system's values, alpha included), highlight, contrast, Reduce Motion, Reduce Transparency, scroller style. Text styles, sizes, space, radii, elevation, materials, motion; neutral dark. `theme.ContrastOn` composites alpha; the contrast tests use the floors in Contracts ("Design tokens v2"): primary ≥ 4.5:1, secondary roles ≥ AppKit's composited contrast for the same role, on every background a recipe uses. *Sonnet.* M.
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
- **D4. Window chrome:** the `Toolbar` (Plain 32, Compact 40, Unified 52, Rex 46), placing the real traffic lights from the token, stored as the close button's centre (plain 16, 16; compact 19, 20; unified 26, 26; Rex 23, 23; spacing 23: the table in Contracts, "Traffic-light units", which supersedes `rex-design-system.md` §2.3), converted to `platform.Options` in one place and unit-tested against `metrics.json`, with the title at 13 bold and Label2 when inactive. Also the Sidebar (rows 32, groups 19, the selection inset 10 with radius 8.5, over N3a's material) and the PathBar. *Sonnet.* M.
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
- **D7. Components apps need next**, in two parts because they have different inputs:
  - **D7a** (on D3, C2, C5): Popover, Toast/Banner, Badge, Avatar, Skeleton, EmptyState; TokenField, Combobox, ColorWell (hosting `NSColorPanel`); the chat pieces of `rex-design-system.md` §3.4 that do not depend on the composer: MessageBubble, ChatListRow, DateDivider, TypingIndicator, and the completion popup list. *Sonnet.* M.
  - **D7b** (on N4): the Composer's styling over the hosted editor (field, attachment row, send button, completion placement). *Sonnet.* S.

### Track N: native hosts

- **N1. Native menus:** the bar, roles, Services, Window and Help menus, validation, context menus, popup-button menus, item symbols, the Dock menu builder for S4; the responder chain; `Kit.MenuBar` empty on macOS; the ⌘Q hack removed. *Opus (high).* M.
- **N2. NSAlert sheets, open and save panels, native tooltips.** *Sonnet.* M.
- **N3a. Materials:** the non-opaque layer, effect views below, the Reduce Transparency fallback, changes in step with presents. *Opus (high).* M.
- **N3b. Native-view hosting:** the container view, the registry, transaction-synced geometry, routing, focus hand-off, the z-order rule, the placeholder, leak counters. *Opus (high).* L.
- **N4. The `NSTextView` composer (`NativeEditor`)**: the contract in "The composer" (byte/UTF-16 mapping, marked-text state, undo-preserving `Replace`, the completion handoff through `doCommandBySelector:`), and the dilation check (D38). *Opus (high).* M–L.
  - **The real-editor matrix (automated; run by the N4 agent on the MacBook through the live harness, and in macOS CI where a window can be key).** A `RunLive` test hosts a real `NativeEditor` in a real window and drives it in-process: keys through `[NSApp sendEvent:]` with `NSEvent`s built by `keyEventWithType:…`, and IME input through the `NSTextInputClient` methods an input method calls on the text view (`setMarkedText:selectedRange:replacementRange:`, `insertText:replacementRange:`, `unmarkText`, `firstRectForCharacterRange:actualRange:`). It checks the app buffer, the reported selection and marked ranges, and the signals, after each step:
    - marked-text submission: compose, then commit with Enter, which commits and does not send; compose, then cancel; `Replace` refused while composing;
    - undo and redo after typing, after `Replace` (a completion is one undo step) and after a paste;
    - paste of text, of an image (routed to `PastedImage`) and of rich text (plain text kept);
    - autocorrect off and on (`setAutomaticSpellingCorrectionEnabled:`; with it on, a known misspelling followed by a space is corrected through the text view's own path and the buffer follows);
    - completion navigation: `@pa` opens the app's popup; Down, Down, Up, Tab and Enter arrive as host commands and move nothing in the text; Enter accepts, the text reads `@Paolo ` with the mention span, and the caret follows; Escape cancels and the next Enter sends;
    - the Edit menu on the hosted view: Copy, Cut, Paste, Select All and Undo through the menu, each acting once (with N1);
    - focus: Tab into the editor and out, Shift+Tab back; an `NSAlert` sheet opened and dismissed returns first responder to the editor with the selection intact;
    - the mapping, with emoji (surrogate pairs and ZWJ sequences), combining marks and Arabic text, each round-tripped through every range API.
  - **Your hand checklist, before checkpoint 2** (about ten minutes on the MacBook; these need real system services an in-process test cannot stand in for): a real Japanese or Chinese input method (compose, choose a candidate, commit, then send); dictation into the composer; Writing Tools on a selected paragraph (rewrite, then undo). The results go in the checkpoint-2 report.
- **N5. Drag and drop:**
  - **in:** files, images and text, with the hover position and `ui` drop targets;
  - **out:** file URLs, and `NSFilePromiseProvider` for media not downloaded yet; the promise is fulfilled on a worker and completed through a block (`gpui-ghostty.md` §2, "Drag out").

  *Sonnet.* M.
- **N6. Services, Look Up and Writing Tools on our view,** from C4's selection and focused fields; the emoji palette and dictation for our own fields. Writing Tools on a custom view is unverified (`gpui-ghostty.md`, "Could not check"): if it does not appear, that is documented as `NSTextView`-only. *Sonnet.* S.
- **N7. The NSAccessibility bridge:** lazy activation, the element table, the diff, announcements, text ranges, hosts as children; the semantic additions; an accessibility snapshot test; a lint for missing names in `widgettest`. *Opus (high).* L.

### Track C: content

- **C1. `VList`**, variable height and anchored by item id: the id table, `Splice`, `Move` and `Reset` with the anchor rules; O(k) prepend and append; O(distance) middle edits with the 1M-item benchmark and the double-buffered sliced rebuild; the `Enumerator` C4 uses. The edit rules and their tests come before the interface is frozen for Z3. *Opus (high).* M–L.
- **C2. Rich text:** spans, the style table, CoreText's typesetter, cached paragraph layouts, link and mention hit regions, emoji-only messages drawn large. *Opus (high).* L.
- **C3. A wrapping flow layout** for chips, reactions and button rows, which ZapFast wraps (`egui.md` §2.2). *Sonnet.* S.
- **C4. Selectable text:** within a paragraph, then across a scope in document order, with endpoints on (item id, part, offset) independent of built boxes; drag with edge auto-scroll on C1; word and paragraph clicks; the `PartEnd`/`OffEnd` sentinels; ⌘A and enumeration through `VListState`'s `Enumerator`, or an app-provided one outside a `VList`; endpoints remapped on `Splice`; `Selected` and `ForEachSelected` so the app copies the whole range; the tests in Contracts ("Rich text and selection"). *Opus (high).* M–L.
- **C5. `img`:**
  - ImageIO on workers, at display size;
  - the cache with byte budgets;
  - `FreeTexture` and atlas pages on all three backends;
  - `k.Image`;
  - inline thumbnails from bytes;
  - the standard-library decoders off macOS.

  *Sonnet; the `gpu` part Opus (high).* M.
- **C6. Animated images,** lazy and visible-only. *Sonnet.* M.
- **C7. External textures** (`CVPixelBuffer` through `CVMetalTextureCache`), the YUV mode, and I420/NV12 uploads; leases on the pixel buffer and both plane textures, retired per ring slot after its command buffer completes (at slot reuse, or through the global completion block when no frame comes), released at once on uncommitted frames, and never freed on a timer when a window closes; at most 48 in the ring; the resize, scroll and close tests with live-object counts (`gpu/renderer_darwin.go` is shared, so the slot hook goes through me). *Opus (high).* M.
- **C8. QR:** the encoder, `rexui.QRCode`, the Vision decode test. *Sonnet.* S.
- **C9. App zoom.** *Sonnet.* S.

### Track S: services

- **S1. Pure Go:** `task`, `app/paths`, `app/prefs` and `app/log`. *Sonnet.* M.
- **S2. `app/secret`.** *Sonnet.* S.
- **S3. `app/notify`**, with response slots reserved at post time, replies stored before the system's completion handler is called, `PostedWithoutActions` and `EvictOldest`, and re-reservation at launch. Tests: a reply accepted while the event paths are saturated is delivered with its text; a post with the table full comes back without actions. *Opus (high).* M.
- **S4. `app/dock`, `app/tray`, `app/login` and `app/power`.** `app/login` covers the main app and bundled agents (`agentServiceWithPlistName:`); T1 generates the agent plist from `gnui.json` and the real bundle id. **Test (MacBook, a `gnui run` bundle):** register an agent with `--start-hidden`, start it as login would (`launchctl kickstart gui/$UID/<label>`; that this matches a real login launch is an assumption, so the checkpoint-2 hand list includes one real login), and check through AppKit state that the app runs with no visible window, no Dock bounce and `StartHidden` reported; then unregister and check `Status`. Registration may need one approval in System Settings → Login Items, at your keyboard. *Sonnet.* M.
- **S5. `app/winstate`** (with the screens from F4), `app/instance`, `app/shell` and `app/locale`. *Sonnet.* M.
- **S6. Clipboard images and files.** *Sonnet.* S–M.
- **S7. Audio:** `media.Player`, `media.Recorder` and `app/perm`. *Opus (high).* M–L.
- **S8. Video into Metal**, on C7. *Opus (high).* M.

### Track T: tooling

- **T1. `cmd/gnui`:** `package`, `run` and `doctor`, the `gnui-dev` identity; login-agent plists generated from `loginAgents` with the label and target from the bundle id in use (so a `.dev` bundle gets a `.dev` label); `mkapp.sh` retired; the tool apps re-signed. *Sonnet.* M.
- **T2. The live-window harness:**
  - **In macOS CI:** `RunLive` grows to several windows and to app-level flows (reopen, quit with zero windows by menu and by a self-sent quit Apple event, delayed replies and refusals, a menu command with no window open), checked through AppKit state.
  - **Driving real AppKit input:** helpers that build `NSEvent`s (keys, clicks, scrolls, gestures from `CGEvent`s) and send them through `[NSApp sendEvent:]`, and that call the `NSTextInputClient` methods on a hosted text view, for F5's gestures and N4's editor matrix.
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
        D7a components (D3, C2, C5) ─────────────────────────────────────── ═╬═▶ (D3, D4 ─▶ Z2)
        ══▶ CHECKPOINT 1: native feel (gallery, Pilot, notes on the MacBook)║
           │                                                                ║
wave 4  N4 composer (N3b, F5) ─▶ D7b composer styling ───────────────────── ═╬═▶ Z3b composer
        N5 drag  N6 services  N7 accessibility (C2, C4, N3b)                ║ Z3a transcript, media,
        S7 audio   S8 video (C7, S7)   T5 docs                              ║     pickers, dialogs (D7a, F5)
           │                                                                ║
wave 5  fixes from both checkpoints' findings                               ║ Z4 calls, notifications,
                                                                            ║    a11y, i18n; Z5 parity
                                                                            ║ ══▶ CHECKPOINT 2
```

- **Within a wave,** tracks run in parallel on disjoint files: up to about eight agents at once. I integrate at the end of each item, not at the end of the wave.
- **The ZapFast items' framework inputs, including design:**

  | Phase 7 | Needs from phase 6 |
  |---|---|
  | Z1 | F2 |
  | Z2 | F3, F4, F5, N1, N2, **D3, D4**, C1, C3, C5, C8, S1, S2 |
  | Z3a (transcript, media, pickers, dialogs) | F4, **F5** (pinch), C1–C6, N5, N6, S6, S7, S8, **D7a** |
  | Z3b (the composer) | N4, **D7b**, N5, S6 |
  | Z4 | S3, S4, S5, C7, N7 |

- **The critical path in one scheduling scenario.** This is a scenario for ordering work, not an estimate. It assumes a duration in elapsed weeks for each item's lead: S 1, S–M 2, M 3, M–L 4, L 5 (the research bounds S at about a week and M at about three, and gives L no upper bound, so 5 is an assumption); ZapFast's items as its plan assumes (Z0 6, Z1 3, Z2 5, Z3a 6, Z3b 3, Z4 5, Z5 2); wave 0 counts 1; an item starts when all its inputs are done; and agents are never the bottleneck. Under those assumptions the longest chain is:

  **wave 0 → F1 → F3 → F4 → N3b → N4 → D7b → Z3b → Z4 → Z5: 32 weeks in this scenario.**

  | Chain (same scenario) | Earliest finish (week) | Slack (weeks) |
  |---|---|---|
  | F1 4, F3 7, F4 12, N3b 17, N4 21, D7b 22, Z3b 25, Z4 30, Z5 32 | — | 0 |
  | D1 → D2 → D3 (12) → Z2 (17) → Z3a (23) | 23 | 2 |
  | N7 (C2, C4, N3b) | 22 | 3 |
  | Z0 (7) → Z1 (10) → Z2 | 10 | 4 |
  | D4 | 10 | 4 |
  | D7a (D3, C2, C5) | 15 | 4 |
  | S7 → S8 (video) | 14 | 5 |
  | C2 → C4 (selection) | 12 | 6 |

  - **So design is near-critical:** D3 and D4 gate Z2, and D7a gates Z3a with 2–4 weeks of slack. D runs as early as F, and D3's ZapFast-facing components (Input, SearchField, Table selection, Card) go first within D3.
  - **The path is re-derived after wave 0 from measured agent throughput:** how long wave 0's and F1's first items actually took per size letter, applied to the remaining items, and again after each checkpoint. Which chain is critical may change; the ordering rule (zero-slack items first) does not.
  - With about eight agents at once, zero-slack items get an agent first.

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
     - closing the last window and clicking the Dock icon;
     - Dock → Quit with no window open;
     - pinch and rotate on the trackpad over the gallery's image scene, if F5 could not test gestures on the live path.

   Your findings are fixed before wave 5 ends.
2. **ZapFast at parity.** See the phase-7 plan. You see ZapFast for the first time there, at full parity (D33). Before it, you run N4's hand checklist (a real Japanese or Chinese input method, dictation, Writing Tools) and one real login with "Open at login" on, to confirm the app starts hidden; everything else in the editor matrix is automated and run by the N4 agent.

### Who does what

- **Me:**
  - wave 0;
  - F3 and the integration of F4;
  - every shared file;
  - integration and CI;
  - the DECISIONS entries D34–D51 (written into `DECISIONS.md` on this branch, for you to revise);
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
   - the text advance test passes at 9–22 pt, at 1× and 2×;
   - the composited contrast floors hold in light, dark and both high-contrast appearances (primary ≥ 4.5:1; secondary roles ≥ AppKit's own);
   - the traffic lights sit at the token centres for every toolbar style, within ±0.5 pt, read back from AppKit.
2. **Native pieces on macOS:**
   - the menu bar, context and popup menus, alerts, panels, tooltips and materials are native in the gallery, Pilot and notes;
   - no drawn menu bar on macOS;
   - VoiceOver reads the gallery and Pilot: a table row reports "row n of 1,000,000"; buttons press, sliders step, focus follows; Accessibility Inspector's audit finds no unnamed control (your hand test plus the snapshot test).
3. **The app object, by your hand test plus CI's AppKit-state tests:**
   - running with no window;
   - Dock reopen;
   - quit refused by a window and then allowed; ⌘Q and Dock → Quit with zero windows; a delayed reply, with app events still delivered during it;
   - nothing accepted is lost when the event paths are saturated during a quit (F3's saturation test); the logout feasibility test has run, or its assumptions are listed as untested;
   - a menu command with no window open;
   - an open-URL event from `open notes-dev://…` on a `gnui run` bundle;
   - sleep and wake events.
4. **Windows:**
   - two windows get input routed correctly, are paced independently, and receive worker results correctly;
   - 20 open/close cycles of a second window, with a hosted text view, a shown menu and a shown sheet, leak nothing, counted by native objects: windows, surfaces, textures, hosts, blocks, observers;
   - F1's feasibility tests pass: concurrent and churning foreign-thread callbacks, cold attachment, 0 Go allocations measured after the high-water mark, no late callback reaching a reused slot's new handler, no slot reused under a callback in flight, block copy per API recorded, callback progress with 32-level nesting, frames intact under stack growth, GC and profiling, and no generation reissued by a slot;
   - closing a window with a video playing frees its GPU resources only after the GPU has finished, with no wait on the main thread.
5. **Content:**
   - **VList:** 1M mixed-height items under 4 ms CPU per frame and 0 allocations; prepend without a jump; stick-to-bottom while items grow; jump to an item; deletion above and at the anchor, insertion in the middle and reorder, each keeping the view; a distant move on 1M items within 2 ms or through the sliced rebuild, with no reader seeing a mixed order (scripted, CI);
   - **rich text:** bidi paragraphs reorder per line (Hebrew, Arabic and English mixed); one emoji cluster lays out one emoji wide; hover, click, select and copy allocate 0;
   - **selection** across three viewports, over skipped rows and through a prepend, copies exactly the expected text (CI);
   - **the composer:** N4's real-editor matrix passes on the MacBook (and in macOS CI where it can run), and your hand checklist is reported before checkpoint 2;
   - **images:** decoded at display size; the cache stays within its budget while scrolling 10k images (gallery scene); animation idles when nothing animated is in view.
6. **Services** (gallery Services scene and tests):
   - a notification with a reply action round-trips under `gnui run`;
   - badge, bounce, status item, login item status, Keychain set/get/delete, atomic prefs, a log file and a captured crash, window state restored and clamped to a screen, a second-instance hand-off, reveal, trash, and clipboard image paste and copy;
   - voice playback at 1.5× with the pitch kept;
   - a recording's levels;
   - a video plays in a scrolling list without a copy (`CVMetalTextureCache`), and through live resize, scrolling and window close the pixel-buffer and lease counts return to baseline;
   - a login agent launches the app hidden (`--start-hidden`) under `gnui run`.
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

## New decisions (D34–D51)

The entries are in `DECISIONS.md`, written on this branch on 2026-10-18 at your request, for you to revise at merge. D38 may be reconciled with the entry from `p6-textopsz`.

| # | Summary |
|---|---|
| D34 | Phase 6 is macOS-first; Windows and Linux compile, stay green and return `ErrUnsupported` for new APIs; their parity is a later phase |
| D35 | A public `clib` package gives apps cgo-free C calls (no callbacks; errors returned in the same call); the framework itself still loads only system libraries |
| D36 | `internal/objc` gains a cross-file class registry, ivars and `SendSuper`; per-request blocks are stack-class literals copied by the runtime, with slots claimed atomically (generation plus active claims) and reused only when no callback is inside; blocks we keep are copied first; every callback has its own frame on the C stack, so none waits or runs out; 1024 callbacks; a slot's generation never repeats |
| D37 | An application object: NSApplication delegate, one app-wide pump, app events dispatched by a common-modes run-loop source, four delivery classes where nothing accepted is lost (responses reserved at post time, outside requests refused with `busy`), a quit policy (answers latched inside `applicationShouldTerminate:`, `NSTerminateLater` otherwise, no automatic answer), windowless running; D30 built for macOS now |
| D38 | Text is shaped and laid out at the point size and rasterised with a scaled context; dilation only if captures show it |
| D39 | Design tokens v2: indexed roles with System or Rex sources read live through the D32 path, System roles exactly the system's colours; contrast checked composited, with AppKit's own contrast as the floor for secondary roles; traffic lights stored as the close button's centre |
| D40 | Components in three layers: `widget/base` (headless and the Kit), `rexui` (styled, one file each, copyable), `widget` (wrappers); amends D28's layering |
| D41 | Native on macOS where the OS adds behaviour (menus, popup lists, alerts, panels, tooltips, materials, the composer, accessibility); native views never interleave with GPU content; the composer exposes byte ranges, marked text, undoable replacement and a completion handoff |
| D42 | Rich text as pointer-free spans laid out by CoreText's typesetter on macOS; selection in document order with endpoints on stable item ids (and end-of-item sentinels), independent of built boxes; `VList` enumerates and selects all itself, other scopes take an app `Enumerator`; the app copies the text |
| D43 | Variable-height lists are anchored by item id, with splice, move and reset that keep the view, and a Fenwick height cache; O(k) prepend and append, O(distance) middle edits, long edits rebuilt in a back buffer swapped at a frame boundary; fixed-pitch lists stay the fast path |
| D44 | Images through ImageIO on workers at display size, a byte-budgeted texture cache with freeing and atlas pages, lazy visible-only animation; imported pixel buffers leased until their command buffer completes; a closing window's GPU resources are retired asynchronously, never freed on a timer |
| D45 | App services are separate `app/*` packages: requests in, events out by the delivery classes, frameworks loaded after the first frame, keyed by the app ID; notification response slots reserved at post time; single-instance requests accepted or answered `busy`; login agents generated from the real identity |
| D46 | Media through AVFoundation; video zero-copy into Metal; codecs the OS lacks stay with the app |
| D47 | Tooling: `gnui` CLI on `gnui.json` and OS-shipped tools, a stable self-signed `gnui-dev` identity, GnuiShot and AppKitRef's live mode as the native-feel instruments, a live real-editor matrix, a zero-cost debug overlay and agent bridge |
| D48 | Our own QR encoder rather than a dependency |
| D49 | ZapFast's core ABI: bounded binary records from one schema, blobs and chunks for large payloads, errors in caller-owned storage, a pipe fd and a drain call (phase 7) |
| D50 | ZapFast's fork layout and upstream merges: the `headless` split gated by a dependency-tree check, a transitive licence audit, kept private (phase 7) |
| D51 | ZapFast's budgets and identity: `dev.gonativeui.zapfast` with its own data, decided defaults for the updater, Rex and startup (phase 7) |

## Gaps: what the research found and this phase leaves out

| Gap | Source | Why it waits |
|---|---|---|
| Windows and Linux parity of everything here (hosted views as child HWNDs or Wayland subsurfaces, notifications through toast COM or D-Bus, tray through `Shell_NotifyIcon` or StatusNotifierItem, Keychain equivalents) | all five reports | D34: macOS first |
| A drawn multi-line editor | `zapfast.md` §2; `egui.md` §2.3 | The macOS composer is an `NSTextView` (N4), which also brings spell check and Writing Tools. The drawn editor is needed only where there is no `NSTextView` |
| i18n: catalogs, plurals, an extractor | `go-and-others.md` §2b, §4 | ZapFast's 11 languages are ported app-side in phase 7 (generated tables from its `.po` files). The framework package comes when a second app needs it |
| A framework self-updater (`app/updates`) | `electron-tauri.md` §2–4 | It needs your Apple identity and a release feed. Phase 7 builds ZapFast's updater against a local test feed until then (D51) |
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

## Status (2026-10-20)

Running. **Checkpoint 1 is the priority:** D4 (matching D0's scenes, with N3's glass) → the rest of D3b (forms) → D6, one agent per app in parallel → the apps shown live to you.

**How the work runs (your instructions, 2026-10-19 and 2026-10-20):**
- **Parallelism:** up to about 14 agents at once, each item in its own worktree (`~/Projects/gnui-wt/<item>`) and branch (`p6-<item>`), keeping this plan's file ownership. Above the cap, items wait in the queue below. If builds contend (load above about 9 on the Mac mini's 10 cores, or memory pressure), it is measured and the cap lowered, not guessed. Build caches and worktrees live in `….noindex` folders, so Spotlight does not index them (a load spike to 19.5 on 2026-10-19 was Spotlight, not the builds).
- **Integration:** a dedicated integrator agent owns `main`, the main checkout (`~/Projects/go-native-ui`), merge conflicts in shared files and the CI runs on `main`. It merges a branch once three things hold: the branch's CI is green on its head, its review findings are closed with tests, and rexcompare is 40/40 on the merged tree. Shared-file changes that need a design decision come back to the plan owner. Merge-order rules: `p6-d4` merges after `p6-align`; whichever of `p6-t2` and `p6-f3` merges second removes the `gnui_f3` build tag, and the tagged tests must pass.
- **Reviews:** one independent review per item, plus at most one scoped re-review of the fixes. Anything still open after that is a GitHub issue labelled `follow-up`, not another blocking round. Small, mechanical follow-ups need no review.
- **Live testing is on the Mac mini (2026-10-20):**
  - **What runs here:** live window tests, frame timing, startup, half-bounce, the live AppKitRef and `appkitscenes` comparisons, GnuiShot captures and hand-test builds, plus the builds, unit tests, offscreen renders and CI dispatch. `GNUI_TEST_HOST` stays unset or `localhost`.
  - **Before every live run:** check `IOConsoleLocked = No` (`ioreg -n Root -d1`). If it reads Yes, report it; don't fall back silently.
  - **Keeping the display awake:** it sleeps after 10 minutes and gives no frames while asleep. Wrap every live run in `caffeinate -d`. The coordinator also holds a 12-hour `caffeinate -d` for the session.
  - **Why it works here now:** Adam turned off the lock-screen password, so the session stays unlocked after Screen Sharing disconnects. `cmd/spike -bench -gate` passes live: 600 frames paced, p50 8.33 ms, p99 8.97 ms, 0 allocations, 0 idle presents, ExecToOnGlass 136 ms. Before that change it re-locked on every disconnect, and a locked session gives no frames.
  - **The display:** an LG UltraGear 5K at 2× (looks like 2560×1440), 120 Hz, on macOS 27.2 (26B5101f).
  - **GnuiShot** is at `~/gnui-test/` here, signed with `gnui-dev`. If it fails on permission, that's reported, and Adam grants Screen Recording.
  - **The AppKit references are re-measured here** (`p6-refmini`), with the machine recorded in each measurement file. The MacBook's numbers (26B5091g) are kept.
  - **The MacBook is reserved** for the final exit-criteria numbers and Adam's hand tests. An ssh guard on the Mac mini (`~/.ssh/config`) refuses agent connections to it.
- **Captures:** every capture is looked at before anything is called matching, and agents quit every app they open.

**Merged on `main`:**
- D0, D1, D2 (`widget/base`), D3a (`rexui` core), D3b controls and lists, with their review fixes (`p6-d3b-fix`, including hidden windows no longer retrying frames);
- F1 (objc runtime), F2, F6 (trackpad scrolling), with the legacy scroller widths and table header corner (`p6-scrollw`);
- N1 (menus), N2 (dialogs, tooltips), N3 (materials and hosting) with its re-review fixes (`p6-n3fix`), the window buttons (D53);
- C1 (VList), C2 (rich text), C3 (flow), C5 (images), C8 (QR); C4 (selectable text) is being merged;
- S1, S2 and S5 (app services);
- T1 (`gnui` CLI), `clib`, the frame-pool crash fix, the Windows clipboard heap-corruption fix, the reliable full-screen test (`p6-fsflake`).

**In flight:**

| Item | Branch | State |
|---|---|---|
| D4 window chrome, Rex tabs | `p6-d4` | merging `main`, wiring N3's glass, the layout audit, matching D0's Finder, Settings and Notes scenes; then its review fixes |
| D3b forms and token fixes | `p6-d3b-forms` | its one fix round (semantics, append-only colour roles, sizes from the audit, D4's measured tokens) |
| Layout `NextJustify` and the layout audit | `p6-align` | its one fix round; `shelldemo` fixed and checked live |
| F3 application object | `p6-f3` | Dock/logout quit through our own Apple-event handler, then one scoped re-review |
| F5 keys and gestures | `p6-f5` | its one fix round (layout-aware shortcuts through `UCKeyTranslate`, secure input released on close) |
| C9 app zoom | `p6-c9` | its one fix round; the titlebar and lights follow the zoom |
| S6 clipboard images and files | `p6-s6` | its one fix round; one shared off-main pasteboard reader |
| N5 drag and drop | `p6-n5` | fixes done; scoped re-review of its two high fixes |
| T2 live-window harness | `p6-t2` | review fixes done, CI green; waiting for the integrator |
| T3 debug overlay | `p6-t3` | building |
| AppKit references on the Mac mini | `p6-refmini` | re-measuring: offscreen first, then live once GnuiShot has Screen Recording here |
| System file icons | `p6-sysicon` | building: real system icons as an `img` source, for D4 and Pilot |
| CI flakes, round 2 | `p6-flakes` | review fixes, and the new macOS timing failures; `gpu` `TestFirstFrameIsContent` on Windows is treated as a possible real white flash |

**Queued (next free slot):** D6 (gallery, notes, Pilot, one agent each) once D4 merges; then F4, N4, C6/C7, S3/S4, T4/T5, N6, N7, and ZapFast Z2 when its inputs land.

**Parked until an app needs them:** S7 audio and S8 video (not started).

**Follow-up issues:** #1–#6 (`follow-up` label).

**Waiting on you:**
- the Screen Recording grant for GnuiShot on the Mac mini (it is already listed in System Settings);
- creating a macOS test user on the MacBook for F3's logout test (the steps are in `app/applive/logout_darwin_test.go`);
- Rex public-test access, to measure Rex directly.

**History:**
- 2026-10-19: parallelism raised to about 14, the integrator owns `main`, the review cap; S7/S8 parked.
- 2026-10-18: planned; revised after the independent review and its scoped re-review.
- Phase 5: 5A is closed, and 5B is absorbed here (`docs/PHASE5-PLAN.md`, Status).
- N3a and N3b: materials per area and hosted native views, demo `cmd/shelldemo`; what each area uses and the deviations are in `docs/design/native-shell-spike.md`, "Generalised".
