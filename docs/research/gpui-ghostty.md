# Native macOS integration: lessons from GPUI (Zed) and Ghostty

Date: 2026-10-18. Scope: how a GPU-rendered app feels native on macOS and hosts real AppKit views next to its own surface, judged against `gonativeui` (`~/Projects/go-native-ui`, read-only, branch `main`).

Sources (shallow clones, read for ideas only; nothing was copied):
- `~/Projects/research/zed`: GPUI is now split into `crates/gpui` (core), `crates/gpui_macos` (AppKit) and `crates/gpui_apple` (Metal, CoreText, dispatcher). These are shortened below to `gpui/`, `gpui_macos/` and `gpui_apple/`, all under `zed/crates/`.
- `~/Projects/research/ghostty`: the Swift/AppKit app in `macos/Sources`, plus the Zig renderer in `src/renderer`. Swift paths below are relative to `ghostty/macos/Sources`.
- `~/Projects/research/zapfast`: to know what the first real app needs.

Our paths are relative to `go-native-ui/`.

---

## 1. Summary

**Where we stand.** Our macOS layer is a fast, correct single-window core:
- content-first startup and a `CAMetalDisplayLink` (D18), which is better pacing than GPUI's `CVDisplayLink` (`gpui_macos/src/display_link.rs`);
- live resize, IME through `NSTextInputClient` on one runtime class, cursor and drag regions (D24), and appearance (D32);
- the menu model (`menu/`), which exists, but the native bridge does not.

Everything a *real app* touches beyond the window is still missing on `main`:
- the native NSMenu bar (D29, planned 5B) and an `NSApplication` delegate (reopen, terminate, open URLs, dock menu);
- more than one window (D30 is planned; `platform/platform_darwin.go:299-301` still refuses a second window);
- file dialogs, sheets and alerts, drag and drop, and notifications;
- the dock badge and status item, accessibility, scroll phases, and any way to host a native view.

**The common pattern.** GPUI and Ghostty differ, but they agree on the core:
1. **One custom-rendered view, many native views around it.** GPUI adds its `GPUIView` as a *subview* of the window's content view (`gpui_macos/src/window.rs:1088-1202`) and slides an `NSVisualEffectView` *below* it (`window.rs:1888-1902`). Ghostty puts its Metal surface inside a real `NSScrollView` for native scrollers (`Ghostty/Surface View/SurfaceScrollView.swift`) and layers SwiftUI/AppKit overlays (search field, banners, badges) above it in a `ZStack` (`Ghostty/Surface View/SurfaceView.swift:35-170`).
   - Native views never interleave with GPU content: they are wholly above or wholly below the surface.
2. **The responder chain does the routing.** Menu items use nil-targeted standard selectors (`copy:`, `paste:`, `selectAll:`), and whichever view is first responder answers them, through:
   - GPUI's app delegate: `gpui_macos/src/platform.rs:108-137, 387-397`;
   - Ghostty's surface view: `Ghostty/Surface View/SurfaceView_AppKit.swift:1639-1717, 2256-2283`.

   That one choice makes key equivalents, Services, Writing Tools and native text views work with no extra glue.
3. **The custom view implements the AppKit protocols that native views get for free:**
   - `NSTextInputClient`, plus `performKeyEquivalent:` care;
   - `NSServicesMenuRequestor` (Ghostty `SurfaceView_AppKit.swift:2180-2252`);
   - `NSDraggingDestination`;
   - the `NSAccessibility` text protocol (Ghostty `:2327-2425`) or an AccessKit tree (GPUI `window.rs:2369-2402`);
   - `menuForEvent:` for native context menus (Ghostty `:1561-1637`);
   - `quickLookWithEvent:` for Look Up (Ghostty `:1532-1558`).

**What makes them feel non-native.** Zed's UI font is `.ZedSans` at 16 px (`zed/assets/settings/default.json:59-71`), its context menus and popovers are drawn rather than NSMenu (`zed/crates/ui/src/components/right_click_menu.rs`), and GPUI has no rubber-banding for trackpad scrolling. Ghostty feels native because the chrome *is* AppKit: xib menus, native tabs, NSToolbar, sheets, Services and the dock menu. Only the terminal grid is custom.

**For ZapFast** (a chat app with a composer, media and notifications), the highest-leverage moves are:
- the NSMenu bar and app delegate;
- a hosted `NSTextView` composer, which brings IME, spell check, autocorrect, dictation, emoji, Writing Tools, undo and VoiceOver in one view;
- `UNUserNotificationCenter` with a dock badge;
- an NSAccessibility bridge from our existing `ui` semantics;
- trackpad scroll phases with rubber-banding;
- two text fixes: fonts sized in points (optical size and tracking) and AppKit's font-smoothing dilation;
- media decoded by the OS (ImageIO, AVFoundation into `CVMetalTextureCache`).

All of these are possible without cgo once `internal/objc` gains three things: blocks, per-instance ivars or `objc_msgSendSuper`, and safe callbacks from foreign threads.

---

## 2. Feature table

Priority: **P0** ZapFast on macOS needs it, **P1** most real apps, **P2** nice to have, **P3** niche. Effort: **S** ≤ 2 days, **M** ≤ 2 weeks, **L** more.

### Application shell

**NSMenu bar with standard selectors and app-wide commands**
- *Gives an app:* ⌘Q/⌘W/⌘M/⌘H, the Edit menu, Window list, Help search; key equivalents resolved by AppKit, layout-aware.
- *How they do it:*
  - GPUI builds NSMenu from actions and puts the keymap's shortcut on each item (`gpui_macos/src/platform.rs:282-501`), with `setAllowsAutomaticKeyEquivalentLocalization:NO` (`:435`).
  - Validation goes through `validateMenuItem:` (`:1517`) and `menuWillOpen:` (`:1540`), and `setWindowsMenu:` sets the Window menu (`:313-316`).
  - Ghostty uses `App/MainMenu.xib` and keeps item shortcuts in sync with its config (`App/AppDelegate.swift:1203-1285`, `Ghostty/Ghostty.MenuShortcutManager.swift`).
- *Ours:* **partial.** The model is in `menu/menu.go` and `menu/match.go` (D29); the bridge is missing. ⌘Q is hard-coded (`platform/platform_darwin.go:619-621`).
- *Priority / effort:* P0 / M.
- *macOS notes:* Name the menu "Edit" and AppKit appends Start Dictation…, Emoji & Symbols and AutoFill itself; name a menu "Help" and set `NSApp.helpMenu` to get the search field. Use nil targets for edit items.

**NSApplication delegate**
- *Gives an app:* dock-click reopen after ⌘W, quit confirmation, `openURLs:`/`openFile:`, the dock menu, launch hooks.
- *How they do it:*
  - GPUI's `GPUIApplicationDelegate` (`gpui_macos/src/platform.rs:89-170, 1369-1500, 1552`);
  - Ghostty's `applicationShouldHandleReopen`, `applicationShouldTerminate` and `reply(toApplicationShouldTerminate:)` (`App/AppDelegate.swift:384-475, 1369-1430`).
- *Ours:* **missing.** No delegate is set; `NSApplication` is created inside `Open` (`platform/platform_darwin.go:324-325`), so there is no app without a window.
- *Priority / effort:* P0 / S.
- *macOS notes:* ZapFast must keep running with no window so notifications keep arriving, and a dock click must show the window again.

**Notifications (`UNUserNotificationCenter`)**
- *Gives an app:* banners, actions, a click that opens the chat, withdrawing notifications when a chat is read.
- *How they do it:* GPUI `gpui_macos/src/system_notifications.rs:118-299`:
  - it guards on a bundle identifier (`:119-127`);
  - it handles `willPresent` so banners show in the foreground (`:286-299`);
  - categories are a union, because macOS replaces the whole set (`:197-231`);
  - the tag doubles as the request id (`:175-182`), which makes replacement and withdrawal easy.

  Ghostty: `Ghostty/Surface View/SurfaceView_AppKit.swift:1776-1906` and `App/AppDelegate.swift:946-967`.
- *Ours:* **missing.**
- *Priority / effort:* P0 / M (needs blocks).
- *macOS notes:* The delegate is called on a background queue: copy the arguments to a ring and `Wake`. Needs a signed `.app` (`tools/mkapp.sh` exists).

**Dock tile badge and dock menu**
- *Gives an app:* the unread count on the icon, quick actions.
- *How they do it:* Ghostty `NSApp.dockTile.badgeLabel` (`App/AppDelegate.swift:801-808`) and `applicationDockMenu` (`:1145-1157`); GPUI `create_dock_menu` (`gpui_macos/src/platform.rs:323-344`).
- *Ours:* **missing.**
- *Priority / effort:* P0 / S.

**Status item (menu bar extra)**
- *Gives an app:* "stay linked in the tray".
- *How they do it:* neither project has one (no `NSStatusItem` in either clone).
- *Ours:* **missing.**
- *Priority / effort:* P1 / M.
- *macOS notes:* `NSStatusBar.systemStatusBar` → `statusItemWithLength:`, a template `NSImage` (an SF Symbol), and an NSMenu. ZapFast uses `fastframe-tray`.

**App Nap, sleep/wake and power**
- *Gives an app:* reconnecting after wake, no throttling during a call or download.
- *How they do it:* GPUI registers for workspace sleep and wake (`gpui_macos/src/platform.rs:1349-1470`), uses `prevent_app_nap` → `NSProcessInfo beginActivityWithOptions:` (`gpui_apple/src/dispatcher.rs:76-104`), and tracks thermal state (`gpui_apple/src/thermal.rs`).
- *Ours:* **missing.**
- *Priority / effort:* P0 / S.
- *macOS notes:* `NSWorkspace.notificationCenter` (WillSleep/DidWake, SessionDidResignActive).

**Keychain**
- *Gives an app:* the archive key, credentials.
- *How they do it:* `gpui_apple/src/keychain.rs`.
- *Ours:* **missing.**
- *Priority / effort:* P0 / S.
- *macOS notes:* The `SecItem*` C API plus a CFDictionary; no ObjC needed.

**URL schemes and documents**
- *Gives an app:* `whatsapp://` links, files opened from Finder.
- *How they do it:* GPUI `application:openURLs:` (`gpui_macos/src/platform.rs:1476`); Ghostty `openFile` and `CFBundleDocumentTypes` (`macos/Ghostty-Info.plist`).
- *Ours:* **missing.**
- *Priority / effort:* P1 / S.
- *macOS notes:* Apple events are dispatched inside `nextEventMatchingMask`, so our own loop works; the handlers need the delegate.

**Login item**
- *Gives an app:* autostart.
- *How they do it:* neither project.
- *Ours:* **missing.**
- *Priority / effort:* P1 / S.
- *macOS notes:* `SMAppService.mainAppService register:` (macOS 13+).

**Updates**
- *Gives an app:* self-update.
- *How they do it:* Ghostty uses Sparkle (`Features/Update/UpdateController.swift`, `SUPublicEDKey` in its plist).
- *Ours:* **missing.**
- *Priority / effort:* P1 / M.
- *macOS notes:* Sparkle is not a system library. Use our own: download, verify an Ed25519 signature in Go, replace the bundle, relaunch.

### Windows

**More than one window**
- *Gives an app:* the image viewer, the call window, settings, popover hosts.
- *How they do it:*
  - GPUI's per-window state is an ivar on both the `NSWindow` and the `NSView` (`gpui_macos/src/window.rs:1156-1164`);
  - window kinds: Normal, Floating panel, PopUp (non-activating panel, `NSPopUpWindowLevel`) and Dialog (`beginSheet:`) (`window.rs:1016-1029, 1212-1261`).
- *Ours:* **missing** (D30 planned). One global `theWindow` (`platform/platform_darwin.go:91, 299`).
- *Priority / effort:* P1 / L.
- *macOS notes:* Prerequisite for NSPopover-style child windows rendered by us, and for detached media viewers.

**Native fullscreen with a custom titlebar**
- *Gives an app:* the green button and Spaces.
- *How they do it:*
  - GPUI saves and restores the traffic-light frames around `windowWillEnterFullScreen`/`windowDidExitFullScreen` (`gpui_macos/src/window.rs:3021-3071`), and offers a "simple fullscreen" with presentation options (`:594-720`);
  - Ghostty has native and non-native modes (`Helpers/Fullscreen.swift`).
- *Ours:* **partial.** The style mask is resizable, so the button works, but `layoutChrome` (`platform/platform_darwin.go:436-456`) does not handle fullscreen.
- *Priority / effort:* P1 / M.
- *macOS notes:* In fullscreen the titlebar lives in `NSToolbarFullScreenWindow` (Ghostty `Features/Terminal/Window Styles/TerminalWindow.swift:447`). Hide the drawn titlebar inset and stop moving the buttons.

**Window tabbing policy**
- *Gives an app:* no stray "Show Tab Bar" items in a single-window app; native tabs for document apps.
- *How they do it:* GPUI calls `NSWindow setAllowsAutomaticWindowTabbing:` once per window and uses `tabbingIdentifier` and `addTabbedWindow:` (`gpui_macos/src/window.rs:988-993, 1224-1291`). Ghostty: `Features/Terminal/TerminalController.swift:460-488`.
- *Ours:* **missing.** The default allows tabbing.
- *Priority / effort:* P1 / S.

**Restoring frame and state**
- *Gives an app:* the window reopens where it was, on the same Space.
- *How they do it:*
  - GPUI archives `encodeRestorableStateWithCoder:` into its own storage, with an unarchiver subclass for the macOS 15 Space bug (`gpui_macos/src/window.rs:1518-1626`, `:314-380`);
  - Ghostty uses `isRestorable` with a `restorationClass` (`Features/Terminal/TerminalController.swift:586-587`) and `applicationSupportsSecureRestorableState` (`App/AppDelegate.swift:914-944`).
- *Ours:* **partial.** The architecture persists app state (ARCHITECTURE §3, rule 3); the native frame and Space are not restored.
- *Priority / effort:* P1 / S.
- *macOS notes:* `setFrameAutosaveName:` is the cheap version and synchronous, so it is compatible with content-first startup.

**Sheets: NSAlert, NSOpenPanel, NSSavePanel**
- *Gives an app:* native dialogs attached to the window.
- *How they do it:*
  - GPUI `beginWithCompletionHandler:` for the open and save panels (`gpui_macos/src/platform.rs:898-1017`, including a Sequoia double-extension workaround at `:988-1000`);
  - GPUI `NSAlert beginSheetModalForWindow:completionHandler:` with initial focus fixed for keyboard users (`gpui_macos/src/window.rs:1709-1791`);
  - Ghostty: `Features/Terminal/TerminalController.swift:440, 1002`.
- *Ours:* **missing** (5B step 9). We have the drawn `widget/dialog.go`.
- *Priority / effort:* P0 / M (blocks).
- *macOS notes:* Never use `runModal`; it would starve our loop. Completion blocks run on the main thread.

**Vibrancy behind content**
- *Gives an app:* sidebar material, as ZapFast does today (`zapfast/src/macos.rs:337-411`).
- *How they do it:* GPUI clears the window background with alpha 0.0001 so the shadow survives (`gpui_macos/src/window.rs:1866-1904`), and its `BlurredView` strips the tint and saturation layers (`:3808-3880`). Ghostty uses liquid glass on macOS 26 through SwiftUI (`Features/Terminal/TerminalViewContainer.swift`).
- *Ours:* **missing.** The Metal layer is opaque (`gpu/metal_darwin.go:101`); Rex draws its own glass.
- *Priority / effort:* P0 / M.
- *macOS notes:* Needs a non-opaque `CAMetalLayer` and premultiplied output. Honour Reduce Transparency.

**Click-through and titlebar clicks**
- *Gives an app:* the first click in an inactive window behaves like AppKit's.
- *How they do it:*
  - GPUI `acceptsFirstMouse:` (`gpui_macos/src/window.rs:3553`);
  - `_opaqueRectForWindowMoveWhenInTitlebar` returns the view's bounds, which avoids macOS 27's delay while it tells a click from a double-click (`:3560-3577`);
  - a spurious `windowDidBecomeKey` is worked around (`:3150-3175`).
- *Ours:* **partial.** D24 drag regions and `mouseDownCanMoveWindow` NO; no `acceptsFirstMouse:`.
- *Priority / effort:* P2 / S.
- *macOS notes:* Check by hand whether our drawn titlebar buttons suffer the macOS 27 delay.

### Input

**Context menus as NSMenu**
- *Gives an app:* native look, the Services submenu, the system font.
- *How they do it:* Ghostty `menuForEvent:` (`Ghostty/Surface View/SurfaceView_AppKit.swift:1561-1637`), with SF Symbol item images. GPUI draws its own (`zed/crates/ui/src/components/right_click_menu.rs`).
- *Ours:* **missing** on macOS (drawn `widget/menus.go`).
- *Priority / effort:* P0 / S (after the menu bar).
- *macOS notes:* `popUpMenuPositioningItem:atLocation:inView:` runs a tracking loop; the D30 frame handler must keep drawing during it.

**Ctrl-click as a right click**
- *Gives an app:* the expected macOS gesture.
- *How they do it:* GPUI `gpui_macos/src/window.rs:2827-2879`.
- *Ours:* **missing** (`platform/platform_darwin.go:546-583` maps types 1 and 2 to Left).
- *Priority / effort:* P1 / S.

**Layout-aware shortcuts**
- *Gives an app:* ⌘Z works on AZERTY, Dvorak and Cyrillic layouts.
- *How they do it:* GPUI `gpui_macos/src/keyboard.rs` (per-layout key equivalents, `UCKeyTranslate`) and keyboard-layout notifications (`gpui_macos/src/platform.rs:1391`).
- *Ours:* **missing.** `macKey` maps physical keycodes (`platform/platform_darwin.go:1366-1420`), and `menu.Match` uses those keys.
- *Priority / effort:* P1 / M.
- *macOS notes:* Native NSMenu solves this for menu items. `menu.Match` should match `charactersIgnoringModifiers` on macOS.

**Key-equivalent and IME ordering**
- *Gives an app:* Cmd keys never eaten by the IME; dead keys work; `jj`-style bindings coexist with IMEs.
- *How they do it:*
  - GPUI `handle_key_event` (`gpui_macos/src/window.rs:2574-2789`) checks whether a composing IME is active with `TISCopyCurrentKeyboardInputSource` (`:2623-2648`);
  - Ghostty `performKeyEquivalent:` replays an event identified by its timestamp (`SurfaceView_AppKit.swift:1276-1426`) and handles a keyboard-layout change during `interpretKeyEvents` (`:1165-1185`).
- *Ours:* **partial.** All keyDowns go through `interpretKeyEvents`; consumed keys are dropped (`platform/platform_darwin.go:626-643`); there is no `performKeyEquivalent:`.
- *Priority / effort:* P1 / S.
- *macOS notes:* Becomes necessary the moment an NSMenu exists.

**Scroll phases, momentum, rubber-banding, latching**
- *Gives an app:* the single biggest "feels like a Mac" signal.
- *How they do it:* GPUI maps `phase` for pinch and scroll (`gpui_macos/src/events.rs:238-284`) but has no rubber-band. Ghostty passes `momentumPhase` to its core (`SurfaceView_AppKit.swift:1058-1079`) and uses a real `NSScroller` (`SurfaceScrollView.swift`).
- *Ours:* **partial.**
  - Precise deltas are applied directly (`ui/signal.go:97-120`) and clamped hard at the edges;
  - the event carries no phase (`platform/platform_darwin.go:591-602`);
  - there is no axis lock and no latching.
- *Priority / effort:* P0 / M.
- *macOS notes:* See §4.

**Pinch, smart magnify, force click**
- *Gives an app:* zooming in the image viewer, Look Up.
- *How they do it:* GPUI `magnifyWithEvent:`, `pressureChangeWithEvent:`, `swipeWithEvent:` (`gpui_macos/src/window.rs:181-212`); Ghostty force-click → `showDefinitionForAttributedString:atPoint:` (`SurfaceView_AppKit.swift:1081-1099, 1532-1558`).
- *Ours:* **missing.**
- *Priority / effort:* P2 / S.

**Secure event input**
- *Gives an app:* the lock code is not visible to keyloggers.
- *How they do it:* Ghostty `Features/Secure Input/SecureInput.swift`: balanced enable and disable, released when the app resigns active.
- *Ours:* **missing.**
- *Priority / effort:* P1 / S.
- *macOS notes:* `EnableSecureEventInput` / `DisableSecureEventInput` (HIToolbox C).

### Text and typography

**Multi-line editor (composer)**
- *Gives an app:* the chat composer.
- *How they do it:* GPUI's editor is custom (the `editor` crate). Ghostty uses a native text field for search (`SurfaceView.swift:326-360`).
- *Ours:* **missing.** Single-line `ui/textfield.go`; multi-line is out of phase 5 (PHASE5-PLAN).
- *Priority / effort:* P0 / M as a hosted `NSTextView`, L as our own editor.
- *macOS notes:* `NSTextView` gives spell check, autocorrect, text replacements, dictation, Writing Tools, the emoji palette, undo, Services and VoiceOver.

**Services and Writing Tools on our own view**
- *Gives an app:* "Look Up", "Translate", "Make Sticky", Writing Tools on selected messages.
- *How they do it:* Ghostty `validRequestorForSendType:returnType:`, `writeSelectionToPasteboard:types:`, `readSelectionFromPasteboard:` (`SurfaceView_AppKit.swift:2180-2252`), `NSApp.servicesProvider` (`App/AppDelegate.swift:236-239`) and `NSServices` in `macos/Ghostty-Info.plist`; GPUI `setServicesMenu:` (`gpui_macos/src/platform.rs:491-494`).
- *Ours:* **missing.**
- *Priority / effort:* P1 / S.
- *macOS notes:* Writing Tools (macOS 15+) uses the same requestor protocol for custom views (Apple's documented behaviour; not exercised here).

**Fonts sized in points (optical size, tracking)**
- *Gives an app:* SF Pro text optics and the system tracking at 11–15 pt on Retina.
- *How they do it:* GPUI shapes at the logical size and rasterises with `CGContextScaleCTM(scale)` and the font at its logical size (`gpui_apple/src/text_system.rs:489-526, 576`).
- *Ours:* **gap.** The `CTFont` is created at `SizePt * Scale` pixels (`text/coretext_darwin.go:341-344, 386, 399`), so at 2× a 13 pt label gets 26 pt optical size and tracking.
- *Priority / effort:* P0 / S–M.
- *macOS notes:* See §4.1.

**Font-smoothing dilation**
- *Gives an app:* glyph weight that matches AppKit text next to native views.
- *How they do it:* GPUI picks one of 5 dilation levels from the text colour's luminance, honouring `AppleFontSmoothing` (`gpui_apple/src/text_system.rs:225-260, 509-514`), and keys the glyph cache on it (`gpui/src/text_system.rs:1272-1286`).
- *Ours:* **off by design** (`text/coretext_darwin.go:65-72`; D21 "not needed").
- *Priority / effort:* P1 / M.
- *macOS notes:* Once native views sit next to ours, the difference shows. Revisit D21.

**Spell checking in our own fields**
- *Gives an app:* red underlines.
- *How they do it:* neither project (GPUI's editor has no native spell check).
- *Ours:* **missing.**
- *Priority / effort:* P2 / M.
- *macOS notes:* `NSSpellChecker requestCheckingOfString:…completionHandler:`. Not needed if the composer is an `NSTextView`.

**The emoji palette and dictation on our own view**
- *Gives an app:* ⌃⌘Space and dictation in our fields.
- *How they do it:* GPUI `orderFrontCharacterPalette:` (`gpui_macos/src/window.rs:1937`); both work through `NSTextInputClient`.
- *Ours:* **partial.** `NSTextInputClient` exists (D25), `attributedSubstringForProposedRange` and `characterIndexForPoint` exist, but there is no menu item.
- *Priority / effort:* P1 / S.

### Data transfer

**Drop into the window: files, images, text**
- *Gives an app:* sending attachments by dropping them.
- *How they do it:* GPUI `registerForDraggedTypes:` on the window and `dragging*` on the view (`gpui_macos/src/window.rs:1078-1082, 3597-3700`); Ghostty `SurfaceView_AppKit.swift:2287-2323`.
- *Ours:* **missing** (5B step 11).
- *Priority / effort:* P0 / M.
- *macOS notes:* `draggingUpdated:` gives hover positions; read `NSPasteboardTypeFileURL` and image types.

**Drag out (attachments to Finder)**
- *Gives an app:* saving by dragging.
- *How they do it:* GPUI `beginDraggingSessionWithItems:event:source:` with a retained mouse-down event and a lazy image provider block (`gpui_macos/src/window.rs:2240-2357, 2798-2806`).
- *Ours:* **missing.**
- *Priority / effort:* P1 / M.
- *macOS notes:* Use `NSFilePromiseProvider` for media that isn't downloaded yet.

**Clipboard images**
- *Gives an app:* pasting screenshots.
- *How they do it:* GPUI clipboard items (`gpui_macos/src/pasteboard.rs`).
- *Ours:* **partial.** Text only (`platform/clipboard_darwin.go`, D25).
- *Priority / effort:* P0 / S.

### Media

**Image decoding (ImageIO)**
- *Gives an app:* JPEG, PNG, HEIC, WebP, GIF frames, downsampled thumbnails.
- *How they do it:* GPUI image elements (`gpui/src/elements/img.rs`, `image_cache.rs`).
- *Ours:* **partial.** We rasterise system icons and symbols (`icon/symbol_darwin.go`); no general image decoder.
- *Priority / effort:* P0 / M.
- *macOS notes:* `CGImageSourceCreateWithData` and `CGImageSourceCreateThumbnailAtIndex(kCGImageSourceThumbnailMaxPixelSize)` on workers. C API, cgo-free.

**Inline video and round video messages**
- *Gives an app:* playback inside the scrolling chat.
- *How they do it:* GPUI's `surface` element takes a `CVPixelBuffer` (`gpui/src/elements/surface.rs`); the renderer creates Y and CbCr textures through `CVMetalTextureCache` (`gpui_apple/src/metal_renderer.rs:398, 1211-1290`).
- *Ours:* **missing.**
- *Priority / effort:* P0 / L.
- *macOS notes:* Prefer `AVPlayerItemVideoOutput` → `CVMetalTextureCache` over a hosted `AVPlayerLayer` for inline video (no z-order, clipping or one-frame-lag problems). Use `AVPlayerView` only for a full-window viewer.

**Audio: voice notes and calls**
- *Gives an app:* play, record, VoIP.
- *How they do it:* GPUI raises audio-thread priority (`gpui_apple/src/dispatcher.rs:107`).
- *Ours:* **missing.**
- *Priority / effort:* P0 / L.
- *macOS notes:* Out of UI scope. AVAudioPlayer and AVAudioRecorder for voice notes; `AudioUnit` VoiceProcessingIO for calls has realtime callbacks into Go, which is risky (GC); keep DSP out of Go or use AVAudioEngine taps on a buffer ring.

**Quick Look, sharing**
- *Gives an app:* the space-bar preview, the Share menu.
- *How they do it:* neither project.
- *Ours:* **missing.**
- *Priority / effort:* P2 / M.
- *macOS notes:* `QLPreviewPanel sharedPreviewPanel` (responder-chain data source) and `NSSharingServicePicker`.

### Accessibility and settings

**Accessibility bridge**
- *Gives an app:* VoiceOver and Voice Control. ZapFast ships AccessKit today, plus a live-region announcer (`zapfast/src/announcer.rs`).
- *How they do it:*
  - GPUI: AccessKit `SubclassingAdapter` with lazy activation and `update_if_active` (`gpui_macos/src/window.rs:2369-2421`), and focus-state updates (`:3192-3200`); guide in `gpui/src/_accessibility.rs`.
  - Ghostty: the text protocol on the view (`SurfaceView_AppKit.swift:2327-2425`), and an `accessibilitySubrole` and identifier on the quick terminal (`Features/QuickTerminal/QuickTerminalWindow.swift:15-22`).
- *Ours:* **partial.** The semantics records and export exist (`ui/semantics.go`, PHASE5 "Semantics"); there is no NSAccessibility bridge (phase 6).
- *Priority / effort:* P0 / L.
- *macOS notes:* Build the element tree only when an assistive technology asks for it. Announcements go through `NSAccessibilityPostNotificationWithUserInfo(…AnnouncementRequested)`.

**Reduce motion, transparency and contrast**
- *Gives an app:* respects the user's settings.
- *How they do it:* neither project.
- *Ours:* **missing.**
- *Priority / effort:* P1 / S.
- *macOS notes:* The `NSWorkspace accessibilityDisplayShould*` properties plus their change notification. Feed `theme` (motion rates, vibrancy off).

**Scroller style preference**
- *Gives an app:* follows the "Show scroll bars" setting.
- *How they do it:* GPUI `NSScroller preferredScrollerStyle` (`gpui_macos/src/platform.rs:1222-1230`); Ghostty observes `preferredScrollerStyleDidChangeNotification` (`SurfaceScrollView.swift:112-121`) and uses a real overlay scroller.
- *Ours:* **missing.** We draw scrollbars (`widget/scroll.go`).
- *Priority / effort:* P1 / S.

### Native views and chrome

**Hosting native views**
- *Gives an app:* `NSTextView`, the effect view, `NSPopover`, `AVPlayerView`, `NSSearchField`.
- *How they do it:* GPUI uses a content view with a GPUI subview and an effect view below it (`gpui_macos/src/window.rs:1088-1202, 1888-1902`); Ghostty an `NSScrollView` with a document view, the surface, and SwiftUI overlays (`SurfaceScrollView.swift`, `SurfaceView.swift:35-170, 544-570`).
- *Ours:* **missing.**
  - `GNUIView` *is* the content view (`platform/platform_darwin.go:351-355`);
  - it is layer-hosting (`setLayer:` before `setWantsLayer:`, `gpu/metal_darwin.go:114-115`), and Apple says such views must not have subviews.
- *Priority / effort:* P0 / L.
- *macOS notes:* See §3.

**NSToolbar and titlebar accessories**
- *Gives an app:* the unified toolbar, titlebar tabs.
- *How they do it:* Ghostty `Features/Terminal/Window Styles/TitlebarTabsTahoeTerminalWindow.swift:48-290` (`NSToolbarDelegate`) and `TerminalWindow.swift:23-30, 255-290`; GPUI `addTitlebarAccessoryViewController:` (`gpui_macos/src/window.rs:542-547`).
- *Ours:* **missing.** Rex draws its own titlebar.
- *Priority / effort:* P2 / M.
- *macOS notes:* ZapFast doesn't need it. Useful for settings windows (`NSWindowToolbarStylePreference`).

**Panels and floating windows**
- *Gives an app:* a quick-compose or call window over other apps.
- *How they do it:* Ghostty `QuickTerminalWindow: NSPanel` with `nonactivatingPanel`, `canBecomeKey` YES and a floating subrole (`Features/QuickTerminal/QuickTerminalWindow.swift`); GPUI `NSPanel` kinds (`gpui_macos/src/window.rs:1022-1029`).
- *Ours:* **missing.**
- *Priority / effort:* P2 / M (needs D30).

### Runtime plumbing

**ObjC runtime pieces**
- *Gives an app:* the plumbing every row above needs.
- *How they do it:*
  - Ghostty builds blocks and `dispatch_async` to the main queue from Zig (`ghostty/src/renderer/metal/IOSurfaceLayer.zig:53-121`) and allocates a `CALayer` subclass with ivars at runtime (`:142-190`);
  - GPUI keeps per-object state in ivars (`gpui_macos/src/window.rs:1156-1164, 2466-2481`).
- *Ours:* **partial.**
  - `objc.NewClass` with one shared IMP (`internal/objc/objc.go:190-209`);
  - no ivars, `objc_msgSendSuper`, blocks or foreign-thread callback story;
  - the per-instance lookup is a global (`theWindow`).
- *Priority / effort:* P0 / M.
- *macOS notes:* See §3.2.

**Executors and the main queue**
- *Gives an app:* async UI work, timers.
- *How they do it:* GPUI: foreground = `dispatch_async_f` on the main queue, `dispatch_after` for timers, GCD global queues by QoS for background (`gpui_apple/src/dispatcher.rs:33-69`).
- *Ours:* **have, differently.** Goroutines, a ring, `Wake` (`postEvent:`), `core.Parker`, `task` (D28).
- *Priority / effort:* not applicable.
- *macOS notes:* Keep ours. The main dispatch queue is drained by our `nextEventMatchingMask` loop, so AppKit APIs that call back on the main queue still work.

**Actions, keymaps, context predicates**
- *Gives an app:* user-configurable, context-sensitive shortcuts.
- *How they do it:* GPUI `gpui/src/keymap/`, `key_dispatch.rs`; the menu shows the keymap's binding (`gpui_macos/src/platform.rs:364-385`); Zed `crates/settings/src/keymap_file.rs`.
- *Ours:* **partial.** `menu.Command` has one shortcut each; key claiming (D28).
- *Priority / effort:* P2 / M.

**Settings and themes as data**
- *Gives an app:* user themes; ZapFast has custom colours.
- *How they do it:* Zed `crates/settings/src/settings_store.rs`, `crates/theme/src/registry.rs`, JSON themes.
- *Ours:* **partial.** Themes are Go tokens (`theme/`, `theme/rex`).
- *Priority / effort:* P1 / M.
- *macOS notes:* Needed for ZapFast's custom colours; not macOS-specific.

### Widgets

**Variable-height, bottom-anchored virtual list**
- *Gives an app:* the chat history, with older messages prepended without a jump.
- *How they do it:* GPUI `gpui/src/elements/list.rs` (variable heights, `ListAlignment::Bottom` at `:164-168`, `splice`/`reset`), with `uniform_list.rs` for fixed heights.
- *Ours:* **missing.** Row heights are fixed per list (ARCHITECTURE §8; `widget/list.go`, `widget/table.go`).
- *Priority / effort:* P0 / L.
- *macOS notes:* Not macOS-specific, but ZapFast cannot ship without it.

---

## 3. Design: hosting native views

### 3.1 View hierarchy and z-order

```
NSWindow (GNUIWindow, runtime subclass of NSWindow)
└─ contentView: GNUIContainer (plain NSView, flipped, layer-backed, wantsLayer=YES)
   ├─ [below] NSVisualEffectView / NSGlassEffectView   (optional backdrop; Metal layer non-opaque)
   ├─ GNUIView (our layer-hosting view: CAMetalLayer, NSTextInputClient)  ← fills the container
   └─ [above] GNUIClip #n (NSView, clipsToBounds, layer.cornerRadius/masksToBounds)
         └─ hosted view #n (NSTextView-in-NSScrollView, NSSearchField, AVPlayerView, …)
```

This follows GPUI's structure: a subview of the content view, with the effect view `positioned:NSWindowBelow` (`gpui_macos/src/window.rs:1088-1202, 1895-1900`).

The rules:
- **Strict z-order.** Native views are above *all* GPU content or below all of it, never interleaved. A drawn overlay (tooltip, drawn menu, dialog) cannot cover a native view. The `ui` overlay pass must therefore:
  - hide covered hosted views (`setHidden:YES` while a modal or popup overlaps them, which is what Ghostty's SwiftUI z-stack ordering amounts to); or
  - use native overlays (NSMenu, NSPopover, sheets) on macOS.

  A second transparent overlay surface above the native views is possible later. It costs a second drawable per window; GPUI doesn't do it.
- **One clip container per hosted view.** Its frame is the intersection of the host box with its ancestors' clip rects; the child is offset inside it. That is how a native field inside a scrolled list is clipped by the list.
- **Coordinates.** The container is flipped (`isFlipped` → YES), so frames are our top-left pixel rects ÷ `scale`, snapped to device pixels.

### 3.2 Platform work in `platform_darwin.go` and `internal/objc`

**1. Runtime additions to `internal/objc`.**
- `NewClass` gets ivars. Add `class_addIvar(cls, "gnui", 8, 3, "Q")` before `objc_registerClassPair`, and read the ivar offset once (`ivar_getOffset`). A callback then reads `*(*uintptr)(self+off)` to find its window or host slot. That replaces `theWindow` and makes multi-window (D30) and many hosted instances zero-alloc to dispatch.
- `SendSuper`. `objc_msgSendSuper` with a static `objc_super{receiver, super_class}`, so runtime subclasses of `NSTextView`/`NSWindow` can override and call through (`keyDown:`, `makeFirstResponder:`, `performKeyEquivalent:`).
- **Blocks.** A block literal in non-Go, never-moving memory: a static array of slots, or a `mmap` page.
  - Layout: `{isa=&_NSConcreteGlobalBlock, flags=BLOCK_IS_GLOBAL(1<<28)|BLOCK_HAS_SIGNATURE(1<<30), reserved, invoke=ffi callback entry, descriptor*, slot id}`.
  - Descriptor: `{0, size, "v@?q"}`.
  - Both symbols come from `libSystem` through `Dlsym`. `_Block_copy` returns global blocks unchanged, so we own their lifetime: free the slot after the single completion call (or on cancel).
  - Ghostty builds its blocks through zig-objc (`IOSurfaceLayer.zig:89-121`); its source was not cloned.
  - Covers sheets, panels, notifications, `LAContext`, `NSDraggingItem` image providers and `dispatch_async`.
- **Callbacks from foreign threads.** `UNUserNotificationCenterDelegate`, notification completion handlers and AVFoundation call on GCD threads. Our ffi rule ("one Frame per OS thread", ARCHITECTURE §4) needs:
  - an entry path that uses a per-callback stack-allocated frame, or a lock-free pool of frames for foreign threads;
  - handlers that only copy arguments into the MPSC ring and `Wake()`. No AppKit and no `ui` work there.
- **Ownership rules.**
  - `alloc`/`new`/`copy` give +1, released when the host slot is freed. Everything else is autoreleased inside the existing per-`Wait` pool (`platform/platform_darwin.go:490-506`).
  - Delegates are weak in AppKit (`NSTextView.delegate`, `UNUserNotificationCenter.delegate`). Keep a strong reference in the slot, as GPUI does (`system_notifications.rs:105-107`).
  - Remove notification observers before release.

**2. Window restructure.**
- `Open` creates `GNUIContainer` as the content view and adds `GNUIView` as its full-size subview. `GNUIView` stays layer-hosting with the `CAMetalLayer`.
- Live resize (D18) is unchanged.
- `hitsView` and the drag-region logic (`:1263-1284`) test against `GNUIView`.

**3. Host registry, `platform.Window.Host*`:**
```go
type HostID uint32
type HostKind uint8 // HostTextView, HostSearchField, HostEffect, HostPlayer, HostCustom(NSView*)
func (w *Window) NewHost(kind HostKind, o HostOptions) (HostID, error) // safe point only
func (w *Window) SetHosts(r []HostRegion)  // after layout; applied only if changed
func (w *Window) FocusHost(id HostID)      // makeFirstResponder
func (w *Window) HostText(id HostID, dst []byte) []byte  // NSString → UTF-8, zero-alloc into dst
func (w *Window) SetHostText(id HostID, b []byte)
func (w *Window) ReleaseHost(id HostID)
type HostRegion struct{ ID HostID; Rect, Clip RectPx; Radius float32; Visible bool }
```

**4. Applying geometry in sync with the present.**
- When a frame changes any host rect, `SetHosts` runs inside `[CATransaction begin]` with `setDisableActions:YES`, and so does that frame's present.
- That frame presents with `presentsWithTransaction=YES` (commit, `waitUntilScheduled`, `[drawable present]`, commit the transaction): the path D18 already uses for live resize.
- Frames that don't move hosts keep asynchronous presents. Without this, a native field inside a scrolling list lags or leads the list by one frame.

**5. Routing in `handle()`** (`:531-631`):
- **Pointer.** If `[GNUIContainer hitTest:point]` is not `GNUIView`, don't queue `EvPointer*`. Push one `EvPointerLeave` when crossing, and let `sendEvent:` deliver the event.
- **Scroll.** Over a hosted view that can't scroll, AppKit passes `scrollWheel:` up the responder chain. `GNUIContainer` implements `scrollWheel:` and queues `EvScroll` for us, so the list under a native field still scrolls.
- **Keys.** If `[win firstResponder] != GNUIView`, don't queue `EvKey`/`EvText`/`EvPreedit`. AppKit (the hosted view and its own input context) owns them. Menu key equivalents still go through NSMenu first.
- **IME.** Each first responder has its own `NSTextInputContext`. `SetTextInput` and `DiscardComposition` apply to `GNUIView` only.
- **Cursors.** Subtract host rects from our cursor regions before `addCursorRect:` (`:1223-1254`); native views set their own I-beam.

**6. Focus hand-off.**
- `GNUIWindow` overrides `makeFirstResponder:`: call super, then queue `EvHostFocus{ID or 0}`. GPUI subclasses `NSWindow` for the same kind of hooks (`window.rs:429-560`).
- `ui` → native: when `ui` focus lands on a host box (Tab traversal or `SetFocus`), `app` calls `FocusHost`.
- Native → `ui`:
  - in the text-view delegate, `textView:doCommandBySelector:` sees `insertTab:`/`insertBacktab:` and returns YES;
  - we queue `EvHostCommand{Tab}`, `ui` moves focus to the next box, and `app` makes `GNUIView` first responder again.
- `cancelOperation:` (Escape) and `insertNewline:` (Enter to send; Shift+Enter or `insertNewlineIgnoringFieldEditor:` for a new line) become host commands.
- This is the pattern Ghostty uses between its search field and the surface (`SurfaceView.swift:1080-1118`; `makeFirstResponder` plus an explicit resign).

**7. Menus.**
- Edit items use nil-targeted `copy:` `cut:` `paste:` `undo:` `redo:` `selectAll:` `delete:`.
- `GNUIView` implements them (they become `ui` edit actions, the contract D28 already has) plus `validateMenuItem:`, as Ghostty's surface does (`SurfaceView_AppKit.swift:1639-1717, 2256-2283`). A hosted `NSTextView` answers them itself when it is first responder.
- App commands target the app delegate with `gnuiCommand:` and the command id in `tag`, as GPUI does with `handleGPUIMenuItem:` and `setTag:` (`platform.rs:387-397, 462-464`).

**8. Dark mode and colours.**
- Hosted views inherit the window's `NSAppearance`, which D32 already sets from the mode the UI declares.
- Text colours: use semantic colours (`textColor`, `controlAccentColor`), or push theme colours on each appearance serial bump.

### 3.3 API in `ui` and `widget`

```go
// ui: a box that reserves space for a platform view and reports it after layout.
func (c *Context) Host(id platform.HostID, key string, flags Flags) Signal // focusable if flags&Focusable
func (c *Context) HostRegions() []HostRegion  // like cursor/drag regions (D24), pointer-free
func (c *Context) HostFocus() (platform.HostID, bool)

// widget: thin, cross-platform where a fallback exists
func (k *Kit) NativeTextEditor(st *widget.NativeEditor) NativeEditorSignal // Changed, Submitted, Cancelled, Height
func (k *Kit) Backdrop(m widget.Material)                                  // vibrancy below the surface
func (k *Kit) VideoSurface(src media.Frame, fit Fit)                        // in-renderer CVPixelBuffer quad, not a host
```

- `ui` stays OS-free: a host is an opaque id plus a rect. `app` passes the regions to `platform.SetHosts` when they change (the D24 pattern).
- `NativeEditor` keeps the app-owned UTF-8 copy, refreshed on `textDidChange:`. Its height comes from the layout manager (`usedRectForTextContainer:`) for auto-growing composers.
- Windows and Linux fall back to our own editor once it exists, or to a hosted control later (a child HWND, a Wayland subsurface). Until then, `NativeEditor` is macOS-only behind a capability check.
- Offscreen renders (`app/offscreen.go`, `widgettest`) draw a placeholder box, and tests drive the `NativeEditor` state directly.
- **Accessibility.** The NSAccessibility bridge returns the hosted `NSView` as a child at the host box's position, so VoiceOver reads native and drawn elements in one order.

### 3.4 Risks

1. **No interleaving.** A drawn popup over a native field must hide the field or be native itself. Designers must know this rule.
2. **Synchronous presents** on frames that move hosts cost main-thread time (`waitUntilScheduled`). Measure them, and do it only on frames that change geometry. Inline video must therefore be in-renderer, never a host inside a scrolling list.
3. **Two text renderers.** AppKit text in the composer next to our text. Same CoreText glyphs, but our point-size and dilation gaps (§4.1) make the mismatch visible. Fix those first.
4. **Callbacks on foreign threads into Go** (notification, AV and LocalAuthentication delegates). Get the ffi frame rules wrong and it crashes.
5. **Lifetime bugs.** Weak delegates, block slots released early or never, observers left behind. Add native-object counters to the leak gate (PHASE5 exit criterion 5 already counts surfaces and windows; add hosts and blocks).
6. **Zero-alloc gate.** Creating a host allocates; steady-state geometry updates must not (rect args through `ffi.Frame` already allocate nothing). Text read-back reuses the app buffer.
7. **Modal and tracking loops.** NSMenu tracking, sheets and the `NSTextView` spelling panel run nested loops. The D30 frame handler must draw without re-entering Build.
8. **Private-API hacks.** GPUI strips `CAChameleonLayer` (`window.rs:3833-3880`); Ghostty patches `NSScrollPocket` frames (`SurfaceScrollView.swift:307-349`). Avoid them, or confine them behind OS-version guards.

---

## 4. Native-feel techniques worth adopting

### 4.1 Typography

- **Create fonts in points, not pixels.** Shape with a `CTFont` of `SizePt`, multiply advances by `scale`, and rasterise with `CGContextScaleCTM(scale, scale)` and subpixel offsets divided by `scale`, as GPUI does (`gpui_apple/src/text_system.rs:489-526`).
  - CoreText then picks SF Pro's optical size and the `trak` tracking for 11/13/15 pt, the way AppKit does.
  - Our `text/coretext_darwin.go:341` passes `SizePt*Scale`, so on Retina everything is set as if it were twice as large: display optics and looser tracking.
- **Match AppKit's font-smoothing dilation.** Do it when native views share the screen with ours. Five luminance-bucketed levels in the glyph cache key, off when `AppleFontSmoothing` = 0 (`gpui_apple/src/text_system.rs:225-260, 509-514`). This revisits D21, and costs at most 5× glyph variants, in practice 1–2 per theme.
- **Use `kCTFontUIFontSystem` and its weights** (ours already does), not a bundled face. Zed's `.ZedSans` default is a visible "not a Mac app" tell (`zed/assets/settings/default.json:59`).
- **Use SF Symbols with the text's point size and weight** (`icon/symbol_darwin.go` uses `configurationWithPointSize:weight:`). Keep symbols baseline-aligned with labels.

### 4.2 Scrolling

1. **Carry the phases.**
   - Add `Phase` and `MomentumPhase` to `platform.Event` for `EvScroll`: `phase` and `momentumPhase` from the NSEvent (MayBegin, Began, Changed, Ended, Cancelled, Stationary).
   - Also carry `hasPreciseScrollingDeltas` (which we have) and `isDirectionInvertedFromDevice`, so horizontal swipes map correctly.
   - Ghostty forwards `momentumPhase` (`SurfaceView_AppKit.swift:1076`).
2. **Latch.** At `Began`, latch the gesture to the scroll box under the pointer, and keep it latched through the momentum `Ended`, even if another scrollable comes under the cursor. Today `ui/signal.go:98` takes the current `scrollKey` on every event.
3. **Lock the axis.** Use the predominant axis within the first few events of a gesture, as `NSScrollView.usesPredominantAxisScrolling` does (Ghostty sets it, `SurfaceScrollView.swift:36`). GPUI's `OngoingScroll` does the same (`gpui/src/gestures.rs:24-50`).
4. **Rubber-band.**
   - Past an edge during a finger-down phase, display `offset = edge + f(overscroll)` with a saturating curve (the classic `(1 − 1/(x·c/d + 1))·d`, c ≈ 0.55, d = the viewport size).
   - During momentum into an edge, let it overshoot a little and spring back.
   - On `Ended` without momentum, spring back with a critically damped spring. GPUI has a reusable spring with retargeting (`gpui/src/spring.rs`), and our `ui/anim.go` approach can host one.
   - Only for precise (trackpad) devices; wheels stay hard-clamped.
5. **Stop on touch.** `MayBegin` (fingers rest) stops a programmatic smooth scroll and any spring.
6. **Scroller visibility** follows `NSScroller.preferredScrollerStyle` and its change notification: overlay scrollers fade in only while scrolling, legacy ones are always visible (`gpui_macos/src/platform.rs:1222-1230`).
7. **Wheel lines.** Use `scrollingDeltaY` as macOS accelerates it. Calibrate our 16 px per line (`platform/platform_darwin.go:597`) against `NSScrollView` by hand; not measured here.

### 4.3 Windows and chrome

- **Turn automatic tabbing off** unless the app opts in (`gpui_macos/src/window.rs:988-993`).
- **Use `setFrameAutosaveName:`** for frame restoration; full NSWindow state restoration later (GPUI `window.rs:1518-1626`).
- **Handle fullscreen.** On `windowWillEnterFullScreen:` remove our titlebar inset and stop repositioning traffic lights; restore them on exit (`gpui_macos/src/window.rs:3021-3071`).
- **Paint synchronously on activation.** On `windowDidBecomeKey:`, paint one frame synchronously when switching windows or tabs, to avoid stale content (GPUI does this with presents-with-transaction, `window.rs:3202-3230`).
- **Keep the window list right.** `changeWindowsItem:title:filename:` when the title changes (`gpui_macos/src/window.rs:1847`); show the document-edited dot through `setDocumentEdited:`.
- **Use sheets** for confirmations and file panels, and fix the initial keyboard focus of NSAlert (`gpui_macos/src/window.rs:1718-1762`).
- **`requestUserAttention:`** for incoming calls (`window.rs:1813-1826`).

### 4.4 The menu bar and the responder chain

- **Use standard selectors with a nil target**, so the Edit menu, Services, Writing Tools, the emoji palette and dictation "just work" for whichever view has focus (Ghostty `SurfaceView_AppKit.swift:1639-1717, 2180-2283`).
- **Disable key-equivalent localisation** only where it is wrong (`platform.rs:434-436`). Otherwise let AppKit remap shortcuts for the user's layout.
- **Validate items** through `validateMenuItem:` from the focused window's command state (the D29 model), and refresh dynamic menus in `menuNeedsUpdate:`.
- **Use NSMenu for context menus** (`menuForEvent:` plus ctrl-click), with SF Symbol images like Ghostty (`:1561-1637`). The Services submenu is added automatically when the view is a services requestor.

### 4.5 Input

- **Implement `performKeyEquivalent:`** on `GNUIView` and ignore synthetic events with a zero timestamp. Ghostty finds that `cancel:` (⌘.) produces a synthetic Escape (`SurfaceView_AppKit.swift:1372-1383`).
- **Don't send ⌘ chords to the IME**, and send printable keys to the IME first only while a composing input source is active (`gpui_macos/src/window.rs:2623-2748`).
- **Reuse the original NSEvent** when modifiers are unchanged. Korean input breaks with a re-created event (`SurfaceView_AppKit.swift:1130-1151`).
- **Turn on secure event input** while a password or lock-code field is focused and the app is active (`Features/Secure Input/SecureInput.swift`).

### 4.6 Rendering

- **Keep `contentsGravity = topLeft`** (we do) and disable implicit layer actions. Ghostty returns `NSNull` from `actionForKey:` (`IOSurfaceLayer.zig:172-186`); we should do the same on any layer we resize.
- **Drop frames of the wrong size** that are presented asynchronously during resize (`IOSurfaceLayer.zig:98-118`). Our live-resize path already presents synchronously (D18).
- **Display P3** is opt-in (`gpui_apple/src/metal_renderer.rs:196-210`). It matters for photos and video in ZapFast: set the layer's `colorspace` and tag decoded images.

---

## 5. Developer tooling

**What GPUI and Zed have**
- **Inspector** (`gpui/src/inspector.rs`, `zed/crates/inspector_ui/README.md`), debug builds only:
  - pick an element with the mouse; the scroll wheel cycles through occluded elements;
  - show its layout;
  - edit its style live (Rust-style or JSON);
  - jump to the source that built it: ids carry the construction location.
- **Frame-time overlay** (`gpui/src/debug_overlay.rs`): painted straight into the scene, bypassing layout and text, so it never causes frames itself. Three modes.
- **Profiler and hang detection** (`gpui/src/profiler/journal.rs`, `hang.rs`): a foreground event journal (task polls, actions, input dispatch, draws, presents) and post-hoc hang incidents above a threshold or a per-interval budget.
- **Test harness:**
  - `TestAppContext` with a deterministic `TestDispatcher`;
  - `simulate_keystrokes`/`simulate_input`/`simulate_click`/`simulate_mouse_*`;
  - simulated native dialogs (`simulate_path_prompt_response`, `simulate_prompt_answer`, `simulate_system_notification_response`);
  - simulated display, scale and visibility changes (`gpui/src/app/test_context.rs:392-986`);
  - seeded property tests (`gpui/src/test.rs`).
- **Visual tests** (`gpui/src/platform/visual_test.rs`, `zed/src/zed/visual_tests.rs`): real Metal rendering with a controllable clock, for screenshots of animations and tooltips.
- **Component registry** (`zed/crates/component/src/component.rs`, `component_preview`): components register a preview, scope and status. It replaces the old storybook, which is gone from this tree.
- **Hot reload** (`gpui/src/hot_reload.rs`, Dioxus `subsecond`). Rust-specific.

**What Ghostty has**
- An ImGui-based terminal inspector rendered in its own Metal view (`Ghostty/Surface View/InspectorView.swift`);
- `UpdateSimulator.swift` to exercise update UI states;
- AppleScript, App Intents and Shortcuts automation (`Features/AppleScript/`, `Features/App Intents/`), which double as UI-test hooks (`macos/GhosttyUITests`).

**What we already have, often stronger**
- `widget/widgettest`: role and name-addressed steps, `Rig`, `Dump` of the semantic tree, waits on conditions, real or fake text systems;
- offscreen renders (`app/offscreen.go`) and `tools/rexcompare` pixel gates;
- exact zero-allocation gates (D27) and `cmd/gallery` (our storybook);
- `tools/bouncerec`, `tools/doccheck`, `GNUI_DEBUG`, `GNUI_TRACE_LIVE`, `platform/debugstate.go`.

**Worth adding (P1–P2)**
1. **A live inspector overlay** (debug build, a hidden command):
   - pick a box, show its key, flags, rect, clip, style stack, signals and semantics;
   - the `Build` call site through `runtime.Caller` sampled only on the picked frame, never on the hot path;
   - draw it as a `ui` overlay window (D30) so it doesn't disturb the inspected layout.
2. **A frame HUD**: CPU time per phase (build, layout, record, encode), present latency from `presentedTime`, allocations this frame, with a bypass draw path like GPUI's.
3. **A slow-frame and hang journal**: per-frame phase timings in a fixed ring; dump the incident when a frame exceeds its budget. No allocation; the ring is preallocated.
4. **A fake platform for native surfaces in `widgettest`**: scripted file-panel answers, alert answers, notification clicks, menu commands, appearance flips and host-view text, so ZapFast flows are testable without AppKit.
5. **An accessibility snapshot test**: compare `Dump` with the tree the NSAccessibility bridge exports, so VoiceOver regressions fail CI.
6. **A host-view placeholder in offscreen renders**, and a leak counter for hosts and blocks.

---

## Could not check

- The zig-objc block implementation Ghostty uses (an external package, not cloned). The block layout in §3.2 follows the public Blocks ABI.
- Runtime behaviour on a Mac. Everything here is from reading; nothing was built or run. Unverified in particular:
  - the macOS 27 titlebar click delay on our drawn titlebar;
  - the wheel line height compared with `NSScrollView`;
  - Writing Tools through `NSServicesMenuRequestor` on a custom view;
  - the cost of `presentsWithTransaction` on frames that move hosts.
- GPUI's multi-window internals and the `editor` crate, and Ghostty's Zig input and renderer threads, were only skimmed.
- Windows and Linux counterparts to the hosting design (a child HWND, Wayland subsurfaces).
