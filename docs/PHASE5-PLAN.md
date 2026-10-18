# Phase 5 plan: the framework surface

You approved phase 5 on 2026-10-17. Phase 4 showed the framework meets the
bar (Pilot). Phase 5 makes it something other apps can be built on:
- What Pilot and rexlook built privately (`cmd/internal/rexkit`, hand-placed lists, columns and dividers) becomes public API.
- The pieces every desktop app needs are added: widgets, menus, more than one window, dialogs, the system appearance, drag and drop, background work.
- rexlook and Pilot are ported onto the public API. From then on they are test consumers of the framework, not the product.

A parity track closes the per-OS gaps at the same time.

**Not in this phase:**
- the OS accessibility bridges (phase 6); widgets record the semantics those bridges need from now on;
- multi-line text editing;
- dragging out of a window;
- popups that extend past the window edge;
- Pilot features (file operations, thumbnails, search);
- the final name, the license, and any promise that the API is stable.

The plan was reviewed independently (review-sol, 26 findings) before any code. The contracts below come from that review.

## Packages (D28)

Each package imports only the ones above it:

| Package | What it is |
|---|---|
| `core`, `gpu`, `text`, `platform` | as now; `gpu` and `platform` change for more than one window (below) |
| `ui` | the immediate-mode core, plus overlays, focus traversal, key claiming, edit actions, semantics, wrapped text |
| `menu` | the OS-independent command and menu model: `Command`, `Shortcut`, `Item`, pointer-free; shortcut matching against `ui.Input` |
| `theme` | design tokens (light and dark palettes, metrics in points, font descriptions, motion rates); `Resolve(scale)` for one window; the `Chrome` interface (window background, surfaces, titlebar, window buttons) |
| `theme/rex` | the default theme: Rex tokens and chrome moved out of `rexkit`, plus a new dark variant |
| `icon` | system symbols in an atlas: SF Symbols (macOS), Segoe Fluent Icons or Segoe MDL2 Assets (Windows 11 or 10), system-font glyphs (Linux) |
| `widget` | the widget set, on `ui`, `menu`, `theme` and `icon` |
| `task` | background work, generalised from `fs.Service` |
| `app` | the runtime: windows, the frame loop, native menus, dialogs, appearance, drag and drop |
| `fs` | optional, as now |

`widget` never imports `app`. A kit is made from what it needs:

```go
k := widget.NewKit(a.UI, a.Text, rex.Theme())
```

## Contracts

### Widgets (D28)

Immediate mode, like `ui`: a widget is a call that builds boxes and returns a signal struct by value.

```go
func (v *Files) Build(c *ui.Context) {
	k := v.kit
	k.Begin(c)
	if k.Button("Open…").Clicked {
		v.open()
	}
	k.Checkbox("Show hidden files", &v.hidden)
	t := k.BeginTable("files", &v.table, v.cols[:], len(v.order))
	if t.SortChanged { // the app sorts (on a task when large), not the Table
		v.resort(t.Sort)
	}
	for r := t.First; r < t.Last; r++ {
		item := v.order[r]       // display row → stable item id
		t.Row(r, uint64(item))   // the item id keys the row's boxes and its selection
		t.BeginCell(0)           // any boxes: an icon, styled and truncated text…
		k.Icon(v.icon(item))
		k.TextBytes(v.name(item))
		t.EndCell()
		t.CellBytes(1, v.size(item))
	}
	k.EndTable(&t)
	k.End()
}
```

**State.** Persistent state is either an app-owned value (`widget.TableState`, `widget.Selection`, `ui.TextBuffer`) or lives in `ui`'s box cache. Never in closures. Bulk records are pointer-free; small owning descriptors such as a `TextBuffer`'s slices are fine.

**Allocations.**
- A steady-state frame allocates 0 once the working set is warm. The working set is the boxes, rows, text, undo history, selection, overlays and semantics the scripted session reaches.
- Every growable store has a `Reserve` and a stated overflow policy. Examples: undo history is bounded in steps and bytes and drops the oldest; the semantics export is bounded per frame.
- No closures or interface calls per row.

**Every widget:**
- is keyboard-operable;
- shows a focus ring when focus came from the keyboard;
- has a disabled state, which also rejects activations already queued;
- works in light and dark;
- records its semantics.

**Layout.** Flow layout by default (`k.Row`, `k.Column`, fixed and fill sizes). Absolute placement stays available; rexlook needs it.

**Strings.** Labels are `string` (constants don't allocate). Dynamic text goes through `c.Fmt()` or `[]byte`.

**`widget.Selection`.** It is indexed by the app's stable item id, not by display row, so sorting and filtering keep it. It keeps a cursor and an anchor by item. When a listing is replaced, the app either remaps it (`Remap(old→new)`) or resets it. Tests cover the selection, cursor and anchor through sorting, filtering and replacement, as Pilot needs.

**Table.**
- Rows and columns have stable ids.
- Cells are scoped (`BeginCell`/`EndCell`), so any boxes can go in a cell; `CellBytes` is the shortcut for plain text.
- Columns resize, have minimum widths, align, and fade text that overflows.
- The header reports sort changes; the data is sorted by the app.

**The widgets:**
- **Text and buttons:** Label (wraps), Icon, Button (default, primary, plain, icon-only).
- **Choices:** Checkbox, Switch, RadioGroup, Slider, Select.
- **Text entry:** TextField, with a search variant.
- **Scrolling and data:** ScrollView with scrollbars, List, Table.
- **Structure:** Splitter (any number of panes, ratios, minimum sizes, keyboard), Tabs.
- **Overlays:** Tooltip, Menu, MenuBar and ContextMenu (drawn), and Dialog (an in-window modal).
- **Progress:** ProgressBar, determinate and indeterminate.

### Input order and edit actions (`ui`)

**Who gets a key event.** Every key event is delivered at most once, regardless of the order widgets are declared in. Each one carries a claimed bit. It is offered, in this order:
1. the input method's composition;
2. the focused text field, which claims the editing chords: copy, cut, paste, undo, redo, select all, caret moves;
3. commands, through `menu` shortcut matching;
4. the widgets' own keys (activation, list navigation, Tab traversal).

**macOS menus.** Native menus pre-empt keys, so the Edit menu's items become edit actions on the focused field. The field's own chords are then not also delivered as keys. A chord is handled once.

**The clipboard is asynchronous.**
- **Copy and cut** give the platform the bytes (`platform.SetClipboard([]byte)`); it copies them into its own buffer.
- **Paste** is a request (`platform.RequestPaste`). The bytes arrive later in an `EvPaste` event, in a reusable buffer, tagged for the field that asked. They are dropped if focus has moved, which is the rule IME commits already follow.
- **Limits:** transfers are size-limited and cancellable. On macOS and Windows the event comes within the same frame or the next. On Linux it comes when the transfer from the other app finishes.
- **Gate:** the exact allocation gate covers copy, cut and paste.

### Overlays, focus and modality (`ui`)

**Overlays** are built after the main tree, drawn above it and hit-tested first. Each one is placed against an anchor rectangle and kept inside the window. Nested overlays (submenus) form a stack.

**A popup** (menu, dropdown, tooltip):
- a click outside closes it, and that click is consumed;
- Escape closes the innermost popup;
- focus moves into it when it opens and goes back when it closes.

**A modal** (Dialog) blocks everything beneath it: hit testing, scrolling, drags (an active drag is cancelled), text input, and every shortcut except its own. Tab is trapped inside it.

**Focus traversal:**
- Tab and Shift+Tab move through focusable boxes in build order;
- `FocusVisible` is true when focus came from the keyboard.

### Semantics (`ui`; read by the phase-6 bridges)

**One record per box that has a role.** Records are pointer-free and hold:
- **identity:** the box key, plus the window when exported;
- **what it is:** a role and state bits (checked, mixed, selected, expanded, disabled, focused, editable);
- **where it is:** its bounds, and its parent, first-child and next-sibling indices;
- **text:** name and value as spans in the export's byte store;
- **ranges:** min, max, value and step;
- **lists:** the total row count and this row's index, so virtual rows report their real position;
- **editing:** the text selection, for editable fields;
- **actions:** a bitmask of supported actions.

**Export.** `Semantics(dst *ui.SemTree)` copies the records into an app-owned snapshot. The snapshot stays valid until the next export.

**Actions.** Action requests (target key, action) come back through `ui.Input`, like commands.

**Tests.** They check behaviour: virtual rows report their position in the whole list, editable and range controls report their values, and actions reach the right widget.

### Commands and menus (`menu`, D29)

**Registration.** Commands are registered once: id, title, shortcut, and whether they are app-wide (Quit, About, New Window) or per window. Shortcuts use Primary: ⌘ on macOS, Ctrl elsewhere.

**State.** A command's enabled and checked state is mutable. The focused window's Build sets it each frame, and app-wide commands also have defaults.

**Delivery.**
- Per-window commands go to the focused window's next Build (`k.Command(id)`), queued in order, each delivered exactly once.
- App-wide commands go to an `app.CommandHandler`, which runs even when no window is open (macOS).
- Quit asks each window to close, and a window can refuse.

**Showing the menus.**
- **macOS:** a native NSMenu bar and native context menus (5B).
- **Windows and Linux:** drawn MenuBar and ContextMenu from the same model (5A).

### More than one window (D30)

**GPU.** `app` owns the device and the shared resources (pipelines, glyph atlas textures). A surface owns only its swapchain or layer, and destroying one leaves the others intact.
- On Linux, one EGL display and context are shared, with `eglMakeCurrent` per surface. `eglTerminate` runs only when the app exits.

**Glyph atlas.** Uploads are coordinated once for all contexts. A context never consumes dirty regions another context needs. Tests cover:
- alternating windows;
- windows at different scales;
- atlas resets;
- glyphs seen for the first time in the second window.

**One event pump for the whole app.**
- It waits on every window's frame readiness and input at once:
  - macOS: one run loop, with a display link per window;
  - Windows: `MsgWaitForMultipleObjects` over every swapchain's waitable object;
  - Linux: one Wayland or X11 connection serves every window.
- Events are tagged with their window. A worker's wake marks only the windows it concerns.
- An idle window never presents and never holds up another.

**Safe points.** `OpenWindow`, `Close` and other window changes are queued and applied between frames, never during a Build or while a frame is encoding.
- Each window is a handle.
- Opening a window can fail.
- A close request can be refused (`CloseRequested`).
- When the last window closes, the app quits by default, except on macOS, where an option keeps it running.
- Closing a window cancels its open dialogs, overlays and tasks.

**Modal and tracking loops.** A native file dialog (Windows `Show`, macOS sheets), macOS menu tracking, and Windows size and move loops all run their own loops. The frame handler, generalised from live resize, keeps our windows drawing and worker results flowing while they run.
- Build is not re-entered.
- UI work never leaves the main thread.
- A native dialog or menu is started at a safe point, never from inside Build or the frame handler, so that its loop can call the frame handler.

### Background work (`task`)

**Shape.** A `task.Pool[In, Out]` runs a registered function on N workers. In and Out are value types; the pool checks once, at construction, that Out holds no pointers. Large results come as leases on pool-owned buffers, as `fs.Result` already does.

**Submitting.** `Submit(in, key)` returns a ticket, or `ErrFull` when the queue is saturated. It never blocks the UI thread.

**Latest wins, by key.** A newer submission with the same key supersedes the older one. Its result is dropped and its worker can see it was cancelled. Different keys never supersede each other.

**Results.** The UI drains them with `Poll(dst)`, which allocates nothing. Errors are part of `Out`.

**Closing.** `Close` waits a bounded time. Then it detaches the remaining work, whose results are discarded. Go work cannot be stopped by force, and the documentation says so.

**Use.** The gallery and the guide use it. `fs.Service` moves onto it only if there are no regressions.

## Work

### 5A: the widget layer

1. **Contracts in code first.** Done by me:
   - the `menu` types;
   - the `ui` key-claim and edit-action API;
   - the platform clipboard API;
   - the semantics record;
   - `Selection` and the Table signatures;
   - the overlay policy;
   - the `widget.NewKit` inputs.
   - Small stub implementations, so the agents below build against fixed interfaces.
2. **`ui`:**
   - overlays, focus traversal and `FocusVisible`;
   - key claiming and edit actions; asynchronous paste and the field side of the clipboard;
   - semantics and its export;
   - `Disabled`;
   - scroll extents and scroll-into-view;
   - undo and redo, bounded;
   - wrapped text for labels, using line breaking from the text stack.
3. **`theme`, `theme/rex`, `icon`:**
   - `rexkit` moves out; Rex gets a dark variant (a new design, for you to judge at the 5A review);
   - the `Chrome` interface also covers Wayland's client-side decorations: the titlebar and window buttons.
3b. **System appearance (D32), moved up from 5B at your request** (2026-10-18), so the 5A review shows it:
   - **reading at startup,** fast, so the first frame is already in the right mode (a dark-mode user never sees a light frame):
     - macOS: `effectiveAppearance` and `controlAccentColor`;
     - Windows: `AppsUseLightTheme` and DWM's `AccentColor`;
     - Linux: the portal's `color-scheme` and `accent-color`, read on a goroutine started at launch, with a bounded wait at the first frame;
   - **following changes live** with no polling and no idle wakeups:
     - macOS: the appearance and system-colour notifications;
     - Windows: `RegNotifyChangeKeyValue` on the two registry keys;
     - Linux: the portal's `SettingChanged` signal (the D-Bus client of D31);
   - **OS-drawn chrome matches the app's mode:**
     - Windows: the dark title bar through `DWMWA_USE_IMMERSIVE_DARK_MODE` (attribute 20 on Windows 11 build 22000+, 19 on Windows 10 builds that have it, nothing where neither works; the minimum OS stays Windows 10);
     - macOS: the window's `NSAppearance`, which covers the traffic lights and menus;
   - **the app** follows the system by default, can force light or dark, and crossfades between modes; the gallery, Pilot and rexlook follow it.
4. **`widget`, `menu` matching, `cmd/gallery`:**
   - The gallery shows every widget in every state, in light and dark.
   - CI renders it offscreen on all three OSes and runs a scripted bench with the exact allocation gate: hover, press, typing, copy, cut and paste, scrolling a 1M-row table, a menu, a dialog, switching light/dark.
5. **Ports:**
   - rexlook and Pilot move onto the public API; `cmd/internal/rexkit` is deleted.
   - In Pilot, the list becomes `widget.Table` with `Selection`, the divider `widget.Splitter`, the path bar `widget.TextField`.
6. **CI and the guide:**
   - the new packages are in CI's test, vet and format lists;
   - the size gate covers the gallery and Pilot on every target;
   - `docs/GUIDE.md` builds a small app step by step, and its code is a compiled, tested example.

**Then you look at the gallery and the ported Pilot on the MacBook before 5B starts.**

### 5B: the app layer

7. **More than one window.**
   - First: app-owned GPU resources and surface-only destruction on all three backends; the shared atlas.
   - Then: the app-wide event pump and window registry, the safe points, and the frame handler for modal and tracking loops.
8. **Native menus on macOS.** The NSMenu bar, the Edit menu as edit actions, context menus, app-wide commands without windows.
9. **Native file dialogs** (open files or a folder, save).
   - **macOS:** NSOpenPanel and NSSavePanel as sheets.
   - **Windows:** `IFileOpenDialog` and `IFileSaveDialog` (hand-written COM) on the UI thread, outside Build and outside the frame handler. The dialog is launched at a safe point; while its own message loop runs, the frame handler keeps our windows drawing.
   - **Linux:** the portal's FileChooser over D-Bus (D31).
   - Results arrive as events.
10. **Appearance:** moved to 5A (step 3b).
11. **Drop into a window:** files and text, with the hover position during the drag.
    - **macOS:** NSDraggingDestination on our view.
    - **Windows:**
      - `IDropTarget` (hand-written COM);
      - `OleInitialize` on the main thread (STA) the first time an app accepts drops, so startup doesn't pay for it;
      - registration and revocation per window;
      - effects negotiated with the source.
    - **Wayland:** `wl_data_device`.
    - **X11:** XDND.
12. **`task`**, then **proof:**
    - the gallery opens a second window and uses `task`;
    - Pilot gets the macOS menu bar, a row context menu (Open, Copy Path, Show Hidden Files), and dropping a folder on a pane opens it there.

### Parity track (alongside 5A)

The protocol work starts at once, in platform files only. Its integration into `ui`, `app` and the theme happens when those contracts exist (step 1) and the chrome exists (step 3).

- **Linux, protocol work:**
  - **Clipboard with other apps,** to the asynchronous API: Wayland `wl_data_device` (shared with drop later), X11 CLIPBOARD and PRIMARY.
  - **Cursors:**
    - Wayland `wp_cursor_shape_v1`;
    - when the compositor lacks it, `libwayland-cursor` with the system cursor theme, through dlopen like fontconfig;
    - failing that, our own rasterised arrow, I-beam and resize cursors;
    - X11: the core cursor font.
  - **Wayland decorations** follow the mode the compositor configures, not just whether xdg-decoration exists:
    - client-side decorations are drawn whenever it says `client_side`, including when the mode changes;
    - the protocol side covers window geometry, move, edge resize, and double-click to maximise.
  - **Wayland window export** with xdg-foreign (for portal dialog parents); X11 uses `x11:<XID>`.
  - **Content-first startup on X11.**
  - **RTL text** without allocations.
  - **Input methods** (text-input-v3; IBus on X11) only if time allows.
- **Windows:**
  - content-first startup with the DWM cloak (`DWMWA_CLOAK` until the first present);
  - the asynchronous clipboard;
  - the IME stays covered by unit tests of the message handling, since CI runners have no IME.
- **macOS:**
  - press-and-hold accents (`replacementRange`), fixing "eé";
  - the asynchronous clipboard.

### Who does what

- **Me:** the contracts (step 1), the shared `ui`/`app` integration points, and final integration.
- **focused-sonnet agents:**
  - **one owner per area:** `ui`; theme, rex and icon; Linux platform files; Windows and macOS platform files.
  - **then:** widgets, ports and the 5B pieces, as their inputs exist.
  - **files:** no two agents edit the same file at the same time; shared files (`app/`, `ui/input.go`) change only through me.
- **review-sol:** an independent review at the end of 5A and of 5B.

## Exit criteria (macOS on the MacBook; Windows and Linux in CI)

1. **Public API:**
   - rexlook, Pilot and the gallery use only public packages, and `cmd/internal/rexkit` is gone;
   - every exported identifier has a doc comment;
   - the guide's code is a compiled, tested example;
   - CI tests, vets and format-checks every package.
2. **The Rex bar holds:**
   - rexcompare stays 40/40;
   - Pilot's offscreen render matches its pre-port render, with any pixel difference reported and explained.
3. **Performance**, in CI on all three OSes and on the MacBook:
   - **allocations:** 0 in steady state in the gallery, Pilot and rexlook benches, including every scripted interaction (copy, cut and paste included);
   - **scrolling:** a 1M-row Table at < 4 ms CPU per frame;
   - **idle** with two windows open, task workers parked and clipboard ownership held, over 60 s:
     - 0 presents;
     - ≤ 1 wakeup/s;
     - 0 % CPU (process CPU time grows < 1 ms);
   - **startup:** the standing budget is ≤ 150 ms warm, exec → complete content on glass (D21).
     - The guide's minimal app must meet it.
     - Pilot (161–184 ms in phase 4, over budget) must not regress, and its number is reported against the budget.
     - Content-first and half-bounce hold.
   - **size and memory:** gallery and Pilot binaries < 10 MB; the guide's minimal app ≤ 40 MB footprint.
4. **Widgets:**
   - each one is keyboard-operable and has a focus ring, a disabled state, and light and dark;
   - behaviour tests cover semantics (virtual rows, editable and range controls, actions), input order (one chord, one handler), overlay modality, and `Selection` through sort, filter and replacement;
   - every state appears in the gallery renders.
5. **Windows:** in CI on all three OSes:
   - two windows get input routed correctly, are paced independently, and receive worker results correctly;
   - the original window can close while another stays open;
   - 20 open/close cycles leak nothing, counted by native-object counters (surfaces, textures, OS windows), with stated cache allowances.
6. **Menus and shortcuts:**
   - **macOS, your hand test:** ⌘Q with a refusing window; Edit → Copy and ⌘C in a text field, each acting once; an app shortcut while a field has focus; a command with no window open.
   - **Windows and Linux CI:** the drawn menus, scripted.
7. **Dialogs, appearance, drop:**
   - **macOS, your hand test:** open and save return their paths; dropping files.
   - **Windows CI:**
     - an open dialog completed by automation returns its path;
     - a save dialog does the same;
     - our windows animate while either is open;
     - `IDropTarget` driven with a real `IDataObject`.
   - **Linux CI:**
     - the portal client against a fake portal on a real `dbus-daemon`, covering immediate responses, cancellation, owner close and a missing portal;
     - X11 drop with a real XDND handshake between two clients;
     - Wayland drop and clipboard against a scripted compositor.
8. **Parity:**
   - the Linux clipboard round-trips with other apps in CI (`wl-copy`/`wl-paste`, `xclip`);
   - cursors work both with and without `wp_cursor_shape_v1`;
   - Wayland decorations under Weston (CI capture), both without xdg-decoration and with it configured client-side;
   - first-frame captures on X11 and Windows show content, not a blank window;
   - press-and-hold gives "é" (your hand test).
9. **System appearance:**
   - the first frame is in the system's mode on every OS (CI starts in dark where the runner allows it: the registry on Windows, a fake portal on Linux);
   - a change while running reaches the app with no idle cost: on Windows by writing the registry in CI; on Linux through the fake portal's signal; on macOS by your hand switch;
   - Windows CI checks the dark title bar attribute;
   - the light/dark crossfade passes the exact allocation gate.
10. **You:** a look at the gallery and the ported Pilot after 5A, including a light/dark switch, then a hand test of the gallery and Pilot on the MacBook at the end.
11. **Numbers:** Windows and Linux numbers come only from CI. Anything CI cannot exercise (a real Windows IME, Wayland drop from a real app) is listed as untested, not claimed.

## Status (2026-10-18)

**On `main`** (all merged branches were green on all 8 CI jobs):
- **Step 1, contracts:** the asynchronous clipboard (`RequestPaste` → `EvPaste`), the appearance contract, and CI gating every package.
- **Step 2, `ui`** (all nine items):
  - key claiming (one chord, one handler) and edit actions;
  - asynchronous paste and bounded undo/redo (`Reserve`);
  - press-and-hold replacements;
  - focus traversal, `FocusVisible`, `Disabled`;
  - overlays and modality;
  - semantics;
  - scroll extents;
  - wrapped text.
  - Plus the `menu` package.
- **Step 3, theme:** `theme`, `theme/rex` and `icon`.
  - Light Rex is pixel-identical to `rexkit` (six scenes, all OSes).
  - The dark Rex is a first design; contrast is asserted.
- **Step 3b, system appearance:** macOS, Windows and Linux (the in-house D-Bus client and the desktop portal), wired into the frame loop. The first frame has the system mode, and the OS-drawn chrome follows the mode the UI declares. Following it in the theme comes with the widget kit.
- **Parity, Windows and macOS:**
  - a zero-allocation clipboard (Windows round trip with other processes in CI);
  - content-first startup on Windows (DWM cloak, checked by screen capture in CI);
  - press-and-hold accents, platform and `ui` side. Your hand test of the real accent popover is still to do.
- **Parity, Linux:**
  - the clipboard with other apps: Wayland data device, X11 CLIPBOARD with INCR; round trips with `wl-copy`/`wl-paste` under Sway and with `xclip` in CI;
  - cursors: `wp_cursor_shape_v1`, then the `libwayland-cursor` theme, then our own raster cursors; on X11, Xcursor, then the core font;
  - Wayland decorations follow the configured mode, with move, edge resize, maximise, minimise and close (the protocol side; the drawing comes from the theme);
  - window handles for portal dialogs (`ExternalHandle`, xdg-foreign);
  - content-first startup on X11 (the window maps with its first frame);
  - RTL shaping allocates nothing;
  - a Wayland input method (text-input-v3; scripted compositor only).
  - Not done: the X11 PRIMARY selection and an X11 input method. Tested only against scripted compositors (Sway for the clipboard), not GNOME or KDE.
- **Pilot's gated bench** includes copy, cut, paste and undo through the platform clipboard on every OS.
- **Sleeping without allocating:**
  - **`core.Parker`:** a pipe read in the netpoller on Unix, an event wait on Windows; no runtime sudog.
  - **`core.Mutex`:** TryLock plus spinning; it never parks, and is meant for short critical sections only.
  - **Use:** the `fs` workers and shared locks use them, and `primeSudogs` is gone.
  - **Why:** this was the cause of the intermittent macOS CI gate failure. A probe on the runner found `sync.Cond.Wait` allocating a sudog in the `fs` workers.
  - **Result:** Pilot's gate now passes on the first window on macOS, Windows and Linux Wayland, at 100k and 1M rows.

- **Widgets and the gallery (step 4):**
  - all the planned widgets, in light and dark;
  - the gallery in CI on all three OSes, with the exact allocation gate;
  - a 1M-row table at 0.7–1.4 ms of CPU per frame;
  - keyboard reachability and semantics tests.
- **Following the system mode:** `theme.Follower` is the one implementation, used by `widget.Kit` and rexlook. The first frame is in the system mode; later changes crossfade.
- **rexlook port (step 5):**
  - on `theme`, `theme/rex` and `icon`;
  - light is pixel-identical to before (0 pixels in 15 scenes), and rexcompare is 40/40;
  - dark, following the system.
- **Doc comments:** every exported identifier of the public packages has one; `tools/doccheck` runs in CI.

- **Pilot port (step 5):** on `widget.Table` with `Selection`, `widget.Splitter`, `widget.TextField`, the Rex theme's chrome; follows the system mode (dark Rex).
  - The port found two real bugs: the glass sampled the background at the wrong offset inside a splitter, which the gallery had too, and the panes sat 1 px low. Both are fixed.
  - Remaining differences from the phase-4 render (5% of pixels, all in the content) come from the shared table: a semibold header, row padding, and a 2 pt focus ring.
  - Gates: 0 allocations, 0.5–0.6 ms CPU per frame at 1M rows in CI, both listings in the first frame.
  - `cmd/internal/rexkit` is deleted: rexlook, Pilot and the gallery use only public packages.

- **Guide (step 6):** `docs/GUIDE.md` builds `examples/notes` step by step.
  - Every code block is an excerpt of the real example, checked by a test.
  - The example is gated in CI on all three OSes: 0 allocations, 0.5–1.2 ms CPU per frame, 3.6–6.9 MB binaries.
  - Writing it surfaced API friction. For example, the obvious minimal app drew only the background, and two byte-labels in one container collided. It is being fixed before the review.

**In progress:**
- the API friction fixes from the guide;
- widgets and the gallery (step 4);
- X11: Pilot's gate needs a second window there (4–9 allocations in the first, likely the X11 clipboard warming up); being located.

**Known gaps so far:**
- Pilot's startup on Windows CI is about 160–180 ms (amd64), over the 150 ms budget.
- The macOS spike step sometimes fails "not vsync-paced", from the runner's display.
