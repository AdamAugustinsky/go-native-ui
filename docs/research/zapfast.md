# ZapFast: what a real app needs from go-native-ui

Research date 2026-10-18. Sources examined (read-only, nothing copied):

- `~/Projects/research/zapfast` at `d1fdf55` (crmne/zapfast 0.19.0, MIT)
- `~/Projects/research/fastframe` (crmne/fastframe, MIT): only manifests and file lists
- `~/Projects/research/whatsmeow` at `b2b1e71` (tulir/whatsmeow, MPL-2.0)
- `~/Projects/go-native-ui` (our repo, read-only): README, ARCHITECTURE, DECISIONS (D1–D32), `docs/PHASE5-PLAN.md`, `docs/GUIDE.md`, package sources

Paths written `zf:` are in ZapFast, `ff:` in fastframe, `wm:` in whatsmeow, and `ours:` in go-native-ui.

## 1. Summary

- **ZapFast is large.** It has about 163k lines of Rust in `zf:src/`, of which about **91.6k are non-test code**. I measured this by cutting each file at its first `#[cfg(test)]`. The UI side is `zf:src/ui/` (27.9k lines) plus `zf:src/app.rs` (9.8k), plus media, text and theme modules. The backend is `zf:src/backend*` (18.5k), `zf:src/archive*` (4.3k) and the call stack (`calls.rs`, `call_audio.rs`, `camera.rs`, `video_rate.rs`, about 5.7k).
- **The backend is already well separated.** The UI and the backend talk only through two enums: `Command` goes from the UI to the runtime and `Event` comes back (`zf:src/backend.rs:1-4`, `:130`, `:974`, `:1357-1506`). The backend runs a tokio runtime on its own thread. Views never see a protobuf: the worker translates messages in `classify()` and `parse_conversation()` (`zf:src/backend/worker.rs:10781`, `:11772`). A C ABI is therefore realistic. It needs a serialised DTO layer, because the enums have about 245 variants and some carry channels, `Arc`s and `egui::ColorImage`. A few UI concerns also leak into the worker and would have to move out: file dialogs, the updater and the opener.
- **Our framework covers the frame, the text and the widget foundations.** It has CoreText with colour emoji, IME on macOS, system appearance, the custom title bar, fixed-row virtual lists, overlays, menus as a model, an idle loop with deadlines, and zero allocations per frame.
- **The P0 path is blocked by three framework gaps:**
  - multi-line text editing, which PHASE5 explicitly puts out of scope;
  - rich wrapped text (mixed styles, links, inline emoji, RTL lines), where `ours:ui/wrap.go` has one font per box and assumes left-to-right;
  - a virtualised list with variable, measured row heights and scroll anchoring, where `ours:widget/list.go` has one fixed `rowH`.
- **The P0 path is also blocked by missing OS services:**
  - notifications and the Dock badge;
  - the native menu bar and file dialogs, which are planned for 5B but not built;
  - image decoding;
  - voice-note playback;
  - QR rendering;
  - a signed `.app` pipeline.
- **My recommendation is a Rust core with a Go UI.** whatsmeow cannot do calls (`wm:README.md`, "Things that are not yet implemented: Calls"). Its Signal dependency, `go.mau.fi/libsignal`, is GPL-3.0, which I verified. A Go port also cannot open ZapFast's SQLCipher archives without cgo, and it would have to re-implement about 23k lines of subtle protocol-state logic.
- **The cost of the Rust core** is a bundled non-system dylib. That conflicts with "only OS system libraries" and needs an explicit approval under D2. It also means two toolchains and an ABI to maintain.

## 2. Needs table

Priorities:
- **P0:** the first usable macOS slice: pairing, chat list, conversation, composer, media, notifications.
- **P1:** parity with ZapFast.
- **P2:** nice to have.

Effort is for our side: **S** is up to about 1 week, **M** is 1–3 weeks, **L** is more than 3 weeks.

### Text and input

| Need | Where in ZapFast | Ours | Pri | Effort | macOS-native notes |
|---|---|---|---|---|---|
| Multi-line composer: grows from 1 to 6 lines then scrolls; Enter or Shift+Enter sends depending on the setting; placeholder and caption mode; draft persistence | `zf:src/ui/conversation.rs:1326-1760` (`TextEdit::multiline` at `:1651`, height clamp around `:1712`, `return_key` at `:1670`); drafts via `Command::SaveDraft` (`zf:src/backend.rs:203`) | **Missing.** `ours:ui/textfield.go` is single-line, and PHASE5 lists "multi-line text editing" under "Not in this phase" | P0 | L | Needs line-aware caret movement, wrapped hit-testing, a scroll-into-view caret and undo across lines. Native Emacs bindings (⌃A, ⌃E, ⌃K) are expected. |
| IME in the composer | through egui-winit | **Partial.** macOS `NSTextInputClient`, preedit and press-and-hold exist (D25; PHASE5 Status; `ours:ui/textfield_ime_test.go`); they must be extended to a multi-line field | P0 | S once the editor exists | Keep `firstRectForCharacterRange:` correct on wrapped lines. |
| Colour emoji in message text and in the editor; an emoji-only message drawn larger | `zf:src/emoji.rs` and `ff:fastframe-emoji`, which paints placeholders; composer at `zf:src/ui/conversation.rs:1683-1697` | **Have** at the glyph level: CoreText colour glyphs land on an RGBA page (`ours:text/coretext_darwin_test.go` `TestEmojiIsColor`), and grapheme clusters come from `ours:text/grapheme.go` | P0 | S | Apple Color Emoji comes from CoreText's cascade. Nothing should be bundled; ZapFast bundles Noto as a fallback, which we don't need on macOS. |
| WhatsApp markup (bold, italic, strike, mono, code blocks, quotes, lists), clickable links, phone numbers, emails and mentions | `zf:src/markup.rs` (779 lines, builds an egui `LayoutJob`) | **Missing.** Wrapped text is one font per box and left-to-right only (`ours:ui/wrap.go` header) | P0 | L | Needs styled spans in one paragraph, per-span hit regions (link hover and click, hand cursor through `SetCursorRegions`) and inline emoji. |
| Bidi: right-to-left paragraphs, base direction, mixed scripts in bubbles and in the composer | `zf:src/bidi.rs` (692 lines of code); composer base direction at `zf:src/ui/conversation.rs:1635` | **Partial.** RTL shaping exists without allocations (PHASE5 Status, `ours:text/rtl_alloc_test.go`), but line layout assumes LTR and arrow keys are not bidi-aware (D25 "Known gaps") | P1 | M | CoreText's `CTTypesetter` or `CTLine` can order runs visually. |
| Text selection across messages, and copy in WhatsApp's `[time, date] Name:` format | `zf:src/transcript.rs`, `markup::paint_selectable`, `SelectionLeash` (`zf:AGENTS.md`, "Message bodies paint…") | **Missing.** Only text fields select | P1 | L | Drag-select with edge auto-scroll. |
| Emoji picker, `:shortcode` completion and recent emoji | `zf:src/ui/picker.rs` (799 lines), the `emojis` crate, composer triggers at `zf:src/ui/conversation.rs:1735ff` | **Missing** (app level, on top of `ours:ui/overlay.go`) | P1 | M | ⌃⌘Space opens the native Character Viewer (`orderFrontCharacterPalette:`) for free, which is P0-cheap. Shortcode names need a CLDR-derived table that we generate ourselves. |
| `@` mention autocomplete and `@all` encoding | `zf:src/ui/conversation.rs:1748ff`, `zf:src/mentions.rs` | **Partial.** Overlays and popups exist (`ours:ui/overlay.go`); the logic is app level | P1 | M | — |

### Media

| Need | Where in ZapFast | Ours | Pri | Effort | macOS-native notes |
|---|---|---|---|---|---|
| Decoding JPEG, PNG, WebP and GIF; inline JPEG thumbnails; avatars; a texture cache with eviction | the `image` crate and `egui_extras` loaders (`zf:Cargo.toml`); `zf:src/image_cache.rs`; thumbnails at `zf:src/ui/conversation.rs:7165`, `:7923` | **Partial.** Texture creation and upload (`ours:gpu/renderer_darwin.go:257,292`) and image boxes (`ours:ui/stack.go:138` `NextImage`, `ours:ui/draw.go:314`) exist; there are no decoders and no budgeted image cache | P0 | M | ImageIO (`CGImageSource`) decodes JPEG, PNG, GIF and HEIC, and WebP including animated WebP since macOS 11. `CGImageSourceCreateThumbnailAtIndex` gives a downscaled decode. It is a plain C API, safe on worker goroutines with a per-call `ffi.Frame` (the pattern of `ours:fs/open_darwin.go`). |
| Voice-note playback: OGG/Opus, a 64-bar waveform, seek, 1×/1.5×/2× with pitch preserved, playing the next unheard note automatically | `zf:src/audio.rs` (`Player`), `zf:src/voice.rs`, `zf:src/timestretch.rs`; `Action::PlayVoice` (`zf:AGENTS.md`) | **Missing** | P0 | M–L | Demux Ogg ourselves (small). Decode Opus with AudioToolbox `AudioConverter` and `kAudioFormatOpus`, which exists but is **unverified**, or in the Rust core, which already has libopus. Play through AudioQueue or AVAudioEngine; `AVAudioUnitTimePitch` gives speed with the pitch kept. |
| Animated GIF and WebP stickers, MP4 "GIFs", posters, frame budgets | `zf:src/animation.rs` (`MAX_FRAMES`, `MAX_RESIDENT_FRAMES` at `:18-26`) | **Missing** | P1 | M | ImageIO frames (`kCGImagePropertyGIFDictionary` and the WebP frame delays). Animate only visible items, and keep the idle loop at 0 when nothing animates. |
| Inline video (H.264 and AAC MP4): seek, speed, round video notes, falling back to the system player | `zf:src/video.rs` (1459 lines of code; mp4 + openh264 + symphonia + rodio) | **Missing** | P1 (P0 = open in QuickTime via `ours:fs/open_darwin.go`) | L | AVPlayer with `AVPlayerItemVideoOutput` gives `CVPixelBuffer`s, which go straight into Metal through `CVMetalTextureCache` with no copy. Apple's licensed H.264 decoder avoids openh264's patent question. |
| Full-window media viewer: zoom and pan, album strip | `zf:src/ui/image_preview.rs`, `zf:src/image_preview.rs` | **Missing** (app level) | P1 | M | Pinch and rotate trackpad gestures: check the magnify events in `ours:platform`. |
| Voice recording: microphone, live levels, normalise, encode to Opus | `zf:src/audio.rs` (`Recorder`), `zf:src/voice.rs` | **Missing** | P1 | M | An AVAudioEngine input tap. Permission via `AVCaptureDevice requestAccessForMediaType:` (a block), plus `NSMicrophoneUsageDescription` and the `audio-input` entitlement (`zf:packaging/macos/Info.plist`, `entitlements.plist`). |
| Other audio documents (MP3, M4A) | rodio decoders | **Missing** | P2 | S | `AVAudioPlayer`. |
| Voice and video calls, including group calls: camera, H.264 encode, device pickers, ringback and ringtone | `zf:src/calls.rs`, `call_audio.rs`, `camera.rs` (nokhwa/AVFoundation), `video_rate.rs`, `call_tones.rs`, `ui/call.rs`, `app/calls.rs`, `backend/worker/calls.rs`; protocol from whatsapp-rust `voip` (`zf:Cargo.toml`) | **Missing.** whatsmeow has no call media | P1 | L | With a Rust core, calls keep running in Rust and only video frames cross the boundary. Natively: `AVCaptureSession`, VideoToolbox encode, and AVAudioEngine voice processing for echo cancellation. |
| Pausing other apps' media while recording | `zf:src/media_pause.rs:14-20`: a no-op on macOS | n/a | — | — | macOS has no public API for this. |

### Notifications and OS integration

| Need | Where in ZapFast | Ours | Pri | Effort | macOS-native notes |
|---|---|---|---|---|---|
| Desktop notifications: a click opens the chat and the message; per-chat sounds; withdrawing them when the chat is read; throttling; permission | `zf:src/notify.rs`; `ff:fastframe-notifications` uses `mac-usernotifications` (`UNUserNotificationCenter`) and `block2` (its `Cargo.toml`) | **Missing** | P0 | M | Needs a bundled, signed app with a bundle id. Clicks arrive through a delegate, which our objc class registration can provide. The completion handlers are blocks: we have one global-block example (`ours:gpu/metal_darwin.go:295-340`) and need blocks with captured context, or a side table. |
| Dock badge with the unread count | `ff:fastframe-dock` (`NSDockTile`); `zf:src/app.rs:8713` `sync_badge` | **Missing** | P0 | S | `[[NSApp dockTile] setBadgeLabel:]`. |
| Native menu bar: App (Settings ⌘,, Services, Hide, Quit), File, Edit, View (zoom, fullscreen), Window, Help | `zf:src/macos.rs:46-110` (muda) | **Partial.** The model exists in `ours:menu/`; the NSMenu bar is 5B step 8, not built (PHASE5) | P0 | M | The Edit menu must act on our focused field, as PHASE5 "macOS menus" already specifies. |
| Context menus on messages and chats | egui `Popup::menu` (`zf:AGENTS.md`, egui pitfalls) | **Partial.** A drawn `ContextMenu` exists (`ours:widget/menus.go`, `Kit.OpenContextMenu`); native context menus are 5B | P0 | S | Native `NSMenu popUpMenuPositioningItem:` would feel right on macOS. |
| Status-bar (tray) item with a menu, and a resident app with no window | `ff:fastframe-tray` (tray-icon `NSStatusItem`), `ff:fastframe-shell` `Resident`; `zf:src/app.rs:767,1059-1180` | **Missing** | P1 | M | `NSStatusItem` with an `NSMenu`, which depends on the native menus (5B). |
| Keychain storage of the archive key | `zf:src/archive/encryption.rs:145` (`apple-native-keyring-store`) | **Missing** | P0 for a Go backend; not needed with a Rust core, which keeps doing it | S | Security.framework `SecItemAdd` and `SecItemCopyMatching` through dlopen. |
| File open and save dialogs (attach, save attachment or album, download folder, wallpaper, sounds, sticker archive, pictures) | `rfd` 0.17, called **inside the backend** at 11 sites, `zf:src/backend/worker.rs:6189-6662` | **Missing** (5B step 9, not built) | P0 | M | `NSOpenPanel` and `NSSavePanel` as sheets. Their completion handler is a block. |
| Dropping files into a chat, with a hover state | `zf:src/app.rs:9209-9227` (egui `dropped_files`, `hovered_files`) | **Missing** (5B step 11) | P1 | M | `registerForDraggedTypes:` with `NSPasteboardTypeFileURL`. |
| Clipboard: pasting images and copied files; copying images | `zf:src/app.rs:9600-9700` (arboard `file_list`, `get_image`, `set_image`) | **Missing.** Text only (`ours:platform/clipboard_darwin.go:13-51`, `public.utf8-plain-text`) | P1 | S–M | `NSPasteboard readObjectsForClasses:` with `NSURL`, and `public.png` / `public.tiff`. |
| Window state: size, position, maximised, recovery when off screen | `zf:src/settings.rs:534-548`, `ff:fastframe-shell` | **Missing.** Only `app.Config` Width and Height exist; there is no frame or position API (`ours:platform/platform_darwin.go` Window methods) | P1 | S | `setFrameAutosaveName:` does it natively. |
| Window lifecycle: close to the background, reopen from the Dock (`applicationShouldHandleReopen:`) | `zf:AGENTS.md` ("The app outlives the window"), `ff:fastframe-shell` | **Missing.** One window only; D30 plans a macOS option to keep running | P1 | M | Can be done without real multi-window: order the window out and back in. |
| Multi-window | not used: one viewport (`zf:src/main.rs:598`); the media preview is in-window | **Missing** (D30, 5B) | P2 | — | Not needed for ZapFast. |
| Accessibility: an AccessKit tree, labels, live-region announcements, slower decorative animations while a screen reader runs | the eframe `accesskit` feature (`zf:Cargo.toml`); `zf:src/announcer.rs`, `zf:src/motion.rs`; about 78 `accesskit` references | **Partial.** Semantics are recorded (`ours:ui/semantics.go`); the NSAccessibility bridge is phase 6 | P1 | L | `NSAccessibilityElement` tree; `NSAccessibilityAnnouncementRequestedNotification` for announcements. |
| System font, script fallback, colour emoji | `ff:fastframe-fonts`, `ff:fastframe-emoji` | **Have** (`ours:text/coretext_darwin.go`, `ours:theme/fonts.go`) | P0 | S | — |
| UI animations: fades, hover, typing dots, sticky date, jump highlight, smooth scroll | egui `animate_*`; `zf:src/ui/conversation.rs:2411-2431,3258` | **Have** (`ours:ui/anim.go`; ARCHITECTURE §8) | P0 | S | — |
| Virtual lists: the chat list has fixed rows | `zf:src/ui/chats.rs:1004,1150,1815` (`show_rows`) | **Have** (`ours:widget/list.go`, `ours:widget/table.go`; 1M rows at 0.7–1.4 ms per frame, PHASE5 Status) | P0 | S | — |
| **The transcript: variable measured heights**, estimates for unmeasured rows, keeping the view still when rows above it grow, prepending older history, staying at the bottom, the unread divider, jumping to a message | `zf:src/ui/conversation.rs:2289-2300` (`estimated_height`), `:2590-2630`, `:3160-3190` (`grew_above`, `RowHeight`) | **Missing.** `ours:widget/list.go:153-300` has one `rowH` | P0 | L | — |
| Deep links (`wa.me`, `whatsapp://send`) | `zf:src/target.rs`, `zf:src/main.rs:222`; macOS `Info.plist` has **no** `CFBundleURLTypes`, so on macOS this is a CLI argument only | **Missing** | P2 | S | `NSAppleEventManager` with `kAEGetURL`. |
| Single instance; forwarding a second launch | `zf:src/single_instance.rs` (`ff:fastframe-instance`, Unix socket) | **Missing** | P2 | S | LaunchServices already keeps one instance of a bundle. A socket is only needed for CLI forwarding. |
| Starting at login | `zf:src/autostart.rs` (`SMAppService` and a bundled agent, `zf:packaging/macos/autostart.plist`) | **Missing** | P2 | S | `SMAppService mainApp`. |
| Proxy (HTTP and SOCKS5 for the WebSocket, media and updates) | `zf:src/proxy.rs` | n/a in the framework: a Rust-core setting (`Command::SetProxy`) or whatsmeow `SetProxy` / `SetSOCKSProxy` | P1 | S | — |
| Reconnecting after a wake | `ff:fastframe-power` (Linux and Windows) | **Missing** | P1 | S | `NSWorkspaceDidWakeNotification`. |
| Updates: signed GitHub releases, a helper, rollback | `zf:src/updates.rs`, `ff:fastframe-update`; also `Command::CheckForUpdates…InstallUpdate` in the backend | **Missing** | P2 | M | A Sparkle-style flow in Go, or a Homebrew cask first. |
| Packaging: `.app`, `Info.plist` usage strings, entitlements, hardened runtime, Developer ID signing, notarisation, stapling, DMG, both architectures | `zf:packaging/macos/{bundle.sh,Info.plist,entitlements.plist,dmg.rb,verify.sh}`; `zf:PACKAGING.md` ("Automatic macOS notarization") | **Missing.** Only the test bundle `ours:tools/bouncerec/bin/BounceRec.app` exists | P0 (notifications need a bundle) | M | lipo two Go binaries, plus the Rust dylib in `Contents/Frameworks` if used. Sign inside out. |

### UI shell and app features

| Need | Where in ZapFast | Ours | Pri | Effort | macOS-native notes |
|---|---|---|---|---|---|
| QR code for pairing, plus pairing by phone-number code | `zf:src/qr.rs` (the `qrcode` crate), `zf:src/ui/login.rs`, `Command::PairWithPhone` | **Missing** (no QR encoder) | P0 | S–M | Write our own encoder, or approve a dependency under D2. |
| Light and dark from the system, accent colour; themes (dark by default, custom palettes) | `zf:src/theme.rs`, `ff:fastframe-theme` | **Have** for the system mode (`ours:platform/appearance_darwin.go`, `theme.Follower`) | P0 | S (palettes M) | — |
| Content under the titlebar, traffic lights centred on ZapFast's header | `ff:fastframe-titlebar`, `zf:src/macos.rs` | **Have** (`app.Config.Titlebar` / `TrafficLights`; D24 drag regions) | P0 | S | — |
| Translucent window and frosted headers ("Messages" theme) | `zf:src/backdrop.rs`, `zf:src/ui/glass.rs` (an OpenGL blur callback) | **Missing.** There is no backdrop blur; `ours:theme/rex/field.go` uses a procedural field | P2 | M | `NSVisualEffectView` behind a transparent `CAMetalLayer`. |
| Keyboard shortcuts (29 in `SHORTCUTS`), Tab focus | `zf:src/ui/keys.rs:622`, `zf:src/ui/focus.rs` | **Have** (`ours:menu/match.go`, `ours:ui/focus.go`) | P0 | S | — |
| Precise trackpad scrolling and momentum; a gesture stays with the pane it started over | `ff:fastframe-scroll`, `App::route_scroll` | **Have** (ARCHITECTURE §8) | P0 | S | — |
| UI zoom (⌘+, ⌘−, ⌘0) | `zf:src/macos.rs` View menu | **Missing** (scale is the display scale) | P1 | S–M | — |
| Window focus state, for read receipts and notifications | `window_focused` in `zf:src/app.rs` | **Partial** (`ours:ui/input.go:174` `FocusGained`; no `FocusLost` or occlusion) | P0 | S | — |
| Deadline timers (typing expiry, toasts) without breaking idle | egui `request_repaint_after` (`zf:src/ui/conversation.rs:2427`) | **Have** (`Context.WakeAfter`, `Wait(deadline)`; D25) | P0 | S | — |
| Dialogs, toasts, tooltips, switches, selects, tabs | `zf:src/ui/dialogs.rs`, `zf:src/ui/settings.rs`, `zf:src/ui/widgets.rs` | **Have** (`ours:widget/`: Dialog, Tooltip, Switch, Select, Tabs, Splitter…) | P0 | S | — |
| Translations (gettext, 11 languages, plurals) | `zf:src/i18n.rs`, `zf:assets/i18n/` | **Missing** | P2 | M | — |
| Image crop and rotate, sticker maker, WebP encoding | `zf:src/ui/image_editor.rs`, `zf:src/backend/picture_edit.rs`, `sticker_maker.rs` (libwebp) | **Missing** | P2 | M | ImageIO has **no** WebP encoder. Keep this in the Rust core. |
| App lock | `zf:src/app_lock.rs`, `zf:src/ui/lock.rs` | app level | P2 | S | — |

## 3. The UI/backend boundary

### What exists

- **The bridge** is `zf:src/backend.rs:1354-1506`.
  - `Backend::spawn` builds a multi-threaded tokio runtime with 2 workers. It runs on a dedicated thread, `zapfast-backend`, and calls `worker::run(dirs, event_tx, command_tx, command_rx, waker)`.
  - Commands go through a `tokio::sync::mpsc::UnboundedSender<Command>`. Events come back through a `std::sync::mpsc` channel.
  - Every event wakes the window through `fastframe_shell::Waker`. The UI drains events with `poll() -> Vec<Event>` once per frame (`zf:src/app.rs:3041` `handle_events`), into `apply_backend_event` (`:3073`).
  - The worker does not start until the UI gives it a startup permit (`take_startup`, `:1493`). That permit comes after the first window frame, so database migrations never delay the window.
- **One `Backend` per linked account** (`zf:src/account.rs`). Events from an account that is not on screen are applied with `events_hidden` set (`zf:AGENTS.md`).
- **The vocabulary is large.** By my count `Command` has about 173 variants and `Event` about 72 (from a regex over `zf:src/backend.rs`, so ±5).
  - Some `Command`s are the worker's own completions, sent back into its own queue: `Picked`, `Sent`, `Downloaded`, `GroupInfo`, `PollCreated`, … are marked "Internal".
  - About 110 commands come from the UI.
- **Payloads** are the app's own types in `zf:src/model.rs`: `Chat` (26 fields), `Message`, `Content` (16 variants: Text, Interactive, Image, Video, Audio, Document, Sticker, StickerPack, Location, LiveLocation, Contact, Poll, Revoked, Unsupported, PhoneOnly, Call), `Contact`, `StickerPack`, `Gif`, `PollDraft`, and so on.
- **What has Serde and what doesn't:**
  - `Message` and `Content` already derive `Serialize` and `Deserialize` (`zf:src/model.rs:463,633`).
  - `Chat`, `Command` and `Event` do not.

### What prevents a clean ABI today

1. **Non-data fields.**
   - `Command::GroupRecipients.stored` is an `UnboundedSender<bool>` (`zf:src/backend.rs:892`).
   - Two variants carry `cancel: Arc<AtomicBool>` (`:450`, `:458`).
   - An update variant holds a `Box<crate::updates::Prepared>`.
   - Call video events carry `Arc<egui::ColorImage>` (`:1255`), and `zf:src/backend/worker/calls.rs:1168` builds one.
2. **UI work done inside the worker.**
   - `rfd` dialogs run at 11 sites (`zf:src/backend/worker.rs:6189-6662`).
   - `crate::opener::open_or_reveal` (`:6496`).
   - The updater commands (`CheckForUpdates`, `InspectUpdate`, `DownloadUpdate`, `InstallUpdate`).
   - `use crate::app::PAGE` (`:60`; `PAGE = 60` at `zf:src/app.rs:37`).
3. **Media playback lives in the UI crate**, though it barely depends on egui: `audio.rs`, `video.rs`, `animation.rs`, `voice.rs`, `timestretch.rs`. Call media (`call_audio`, `camera`) is driven by the worker (`zf:src/calls.rs:440-456`), so it naturally belongs to the core.
4. **Much app logic is UI-side.** `zf:src/app.rs` holds 9.8k lines of reducers: the notification policy (`notification_sound` around `:9700`), read state when focused, playing the next voice note, and so on. A Go UI has to port this whatever happens to the backend.

**Verdict.** The separation is clean at the level of the architecture, and the project's own rules enforce it (`zf:AGENTS.md`: "The two sides talk only through `Command` … and `Event`"). It is not FFI-ready, but the remaining work is mechanical:
- add a DTO layer;
- move the dialogs, the updater and the opener out of the worker;
- replace `ColorImage` with raw frames.

### C ABI sketch: commands in, events out, no callbacks into Go

This is a new crate, `zfcore`, of type `cdylib`, built in a fork of ZapFast that keeps `backend`, `archive`, `calls`, `voice` and `model` and drops `egui` and `eframe`.

```c
// zfcore.h (sketch). All functions are thread-safe unless noted.
#define ZF_ABI 1
typedef struct zf_core zf_core;               // one per linked account, as ZapFast's Backend
typedef struct { const uint8_t *px; uint32_t w, h, stride, fmt; int64_t pts_ns; } zf_frame;

uint32_t zf_abi_version(void);
int32_t  zf_core_open(const uint8_t *cfg, size_t len, zf_core **out);  // JSON: dirs, account id, device name, proxy, demo
int32_t  zf_core_start(zf_core *c);                                   // the startup permit (after the first frame)
int32_t  zf_event_fd(zf_core *c);                                     // O_NONBLOCK read end; readable when events wait
int32_t  zf_next_event(zf_core *c, uint8_t *dst, size_t cap, size_t *len); // 0 ok, 1 empty, 2 too small (*len = needed)
int32_t  zf_send(zf_core *c, const uint8_t *cmd, size_t len);         // copies and enqueues; never blocks
int32_t  zf_blob_read(zf_core *c, uint64_t id, uint8_t *dst, size_t cap, size_t *len); // large bytes named by events
void     zf_blob_free(zf_core *c, uint64_t id);
int32_t  zf_frame_acquire(zf_core *c, uint32_t stream, zf_frame *out); // call video; valid until release
void     zf_frame_release(zf_core *c, uint32_t stream);
int32_t  zf_core_close(zf_core *c, uint32_t timeout_ms);              // blocks: call off the main thread
size_t   zf_last_error(uint8_t *dst, size_t cap);                     // thread-local message
```

- **Threading (tokio).** Keep `Backend::spawn` as it is, with its own thread and runtime. The C functions only touch channels.
  - Commands: `UnboundedSender::send` is synchronous and thread-safe.
  - Events: wrap the `std::mpsc::Receiver`, which is not `Sync`, in a mutex, or switch to a queue with `try_recv`.
  - Replace `Waker` with a pipe writer that never loses a wakeup:
    - Rust pushes an event, and writes one byte if `pending.swap(true)` was false.
    - `zf_next_event` returning *empty* clears `pending` and checks the queue once more before returning.
  - Wrap every `extern "C"` function in `catch_unwind`. The release profile uses `panic = "abort"` (`zf:Cargo.toml`), which would kill the Go process.
- **Memory ownership.**
  - Events and commands are copied into buffers the caller owns. Go passes heap memory, which Go's GC does not move, and only for the duration of the call; ARCHITECTURE §4 already forbids stack locals as C pointers.
  - Large payloads stay in Rust: decrypted media bytes, decoded call frames and sticker bitmaps. They are named by id and freed explicitly.
  - Call video is drawn zero-copy: acquire, then upload into a Metal texture on the main thread, then release.
  - No pointer crosses the boundary and is kept.
- **Encoding.** Use JSON in v1. Serde exists for `Message` and `Content`; add DTOs for `Chat`, `Event` and the public `Command`s.
  - Decode on the event goroutine, not the main thread, so the frame path allocates nothing.
  - Hot commands are sent while typing: `Composing` on every change (`zf:src/ui/conversation.rs:1729-1733`), `SaveDraft`, `MarkRead`. Give them fixed-layout binary records written into a reused buffer, so that a typing session passes our exact allocation gate (D27).
- **The Go side, without cgo.**
  - Load the library with `purego.Dlopen("…/Contents/Frameworks/libzfcore.dylib")` and look the symbols up once. The fakecgo bootstrap we already rely on (D3) makes this legal.
  - One goroutine per core runs `os.NewFile(fd)` on the nonblocking pipe, so it sleeps in the netpoller and pins no thread (the same pattern as `core.Parker`). On each wake it calls `zf_next_event` until the queue is empty, decodes into the app's model, pushes onto the MPSC ring and calls `platform.Wake()`.
  - The main thread calls into Rust only for `zf_send`, which takes microseconds and never blocks, and for the frame calls.
  - Rust never calls into Go, so no Go callbacks are needed.
- **Moves the fork needs.** Replace `PickFiles` and friends with commands that carry paths, and open the panels in Go (`NSOpenPanel`). Remove the updater commands. Turn `CallVideo` into a frame id.

### Driving the UI without an account

- **ZapFast's own test hooks.** `Backend::detached()` (`zf:src/backend.rs:1403`) returns an event sender with no runtime. `recording()` and `recording_with_events()` (`:1431-1449`, test-only) capture commands and inject events.
- **The demo is not reusable as is.** `demo::populate` (`zf:src/demo.rs:417`) writes the `App` state directly and answers captured commands locally (`take_demo_commands`, `zf:src/demo/tour/session.rs:10`). It does not emit `Event`s, so it cannot drive a Go UI through the ABI.
- **Proposal: a demo core in the fork.** `zf_core_open({"demo":true})` emits a synthetic event stream: `Link(Connected)`, `Me`, `Contacts`, `Chats`, `Messages`, using the fork's own CC0 demo fixtures (`zf:assets/demo/SOURCES.md`). It answers `SendText` with `Sent` / `MessageUpdated`. This gives a fixture for the Go port's scripted sessions (`ours:widget/widgettest`) and its allocation gate.
- **A visual reference.** `--demo-shot` (`zf:DEMO.md`) renders every screen headless. Pairing it with our offscreen render of the same fixture gives a "zapcompare", as rexcompare does for Rex.
- **Privacy.** Use synthetic data only. `zf:AGENTS.md` forbids reading users' archives.

## 4. A full Go port with whatsmeow

| ZapFast protocol feature (via whatsapp-rust) | whatsmeow | Evidence |
|---|---|---|
| QR pairing, pairing code, passkey | Yes | `wm:qrchan.go` `GetQRChannel`, `wm:pair-code.go` `PairPhone`, `wm:pair-passkey.go` |
| History sync: initial, and on demand (`FetchOlder`) | Yes | `DownloadHistorySync`, `BuildHistorySyncRequest`, `ManualHistorySyncDownload` (`wm:client.go:107-110`) |
| Media download and upload, re-upload after 403/404/410 | Yes | `Download*`, `Upload`, `wm:mediaretry.go` `SendMediaRetryReceipt` |
| Reactions, edits, revoke | Yes | `BuildReaction`, `EncryptReaction`, `BuildEdit`, `BuildRevoke` |
| Polls: create, vote, decrypt | Yes | `BuildPollCreation`, `BuildPollVote`, `DecryptPollVote` |
| Stickers, sticker packs | Yes | `FetchStickerPack` |
| App state: pin, mute, archive, star, labels, mark read, delete chat | Yes | `wm:appstate/encode.go:43-340` builders |
| App state: favourites, favourite stickers, delete-for-me, clear chat, lock chat | **Partial.** The index constants exist but there are no builders, so patches must be built by hand | `wm:appstate/keys.go:51,118-126` (`IndexFavorites`, `IndexFavoriteSticker`, `IndexDeleteMessageForMe`, `IndexClearChat`, `IndexLock`) |
| App-state snapshot recovery | Yes | `wm:appstate/recovery.go` |
| Voice and video calls, group calls, call links | **No.** Only signalling events and `RejectCall` | `wm:call.go:17-106`; `wm:README.md`: "not yet implemented: Calls" |
| Business accounts | Profile and links | `wm:business.go` (`GetBusinessProfile`, `ResolveBusinessMessageLink`); companion pairing to a Business account not verified |
| Channels (newsletters) | Yes, more than ZapFast, which treats channels as read-only | `wm:newsletter.go` (follow, messages, reactions, mute) |
| Communities | Yes | `LinkGroup`, `GetSubGroups`, `GroupLinkedParent` (`wm:types/group.go:34,57-62`) |
| Privacy, presence, typing, LID↔PN mapping, groups and invites, proxy | Yes | `wm:privacysettings.go`, `wm:presence.go`, `wm:store/sqlstore/lidmap.go`, `wm:group.go`, `SetProxy` / `SetSOCKSProxy` |
| Interactive messages (buttons, lists, carousels) | Protobufs only. ZapFast does this from raw protobufs too | `zf:src/backend/worker/interactive.rs` |

**Storage.**
- whatsmeow's `sqlstore` accepts any `*sql.DB` through `NewWithDB(db, "sqlite3", log)` (`wm:store/sqlstore/container.go:76`). So modernc.org/sqlite (pure Go) can back it.
- modernc has **no encryption**.
- The only cgo-free encrypted option I found is ncruces/go-sqlite3 (MIT; SQLite built as Wasm and translated to Go with wasm2go). Its `adiantum` and `xts` VFSes encrypt at rest, but deterministically and without page MACs. They are **not compatible with SQLCipher**, and memory use is higher.
- Consequences:
  - ZapFast's SQLCipher archive (`zf:src/archive/encryption.rs`, raw key `x'…'` at `:233`) cannot be opened from pure Go.
  - The device session (`session.db`, whatsapp-rust schema, `zf:src/backend/worker/device_store.rs:51`) cannot be reused either.
  - A Go port means re-linking, and losing the local archive unless it is exported once with the Rust code.
- Other options: application-level AEAD on message rows with the key in the Keychain (our own code), or relying on FileVault.

**Licences.**

| Component | Licence | How I know |
|---|---|---|
| ZapFast | MIT | `zf:Cargo.toml`, `LICENSE` |
| fastframe | MIT | GitHub API |
| whatsapp-rust and its own Signal code, `wacore-libsignal` | MIT | its `Cargo.toml` files and the GitHub API |
| whatsmeow | MPL-2.0 (file-level copyleft) | `wm:LICENSE` |
| `go.mau.fi/util` | MPL-2.0 | GitHub API |
| `go.mau.fi/libsignal` | **GPL-3.0** | its go-import meta points to `github.com/tulir/libsignal-protocol-go`, which the GitHub API reports as GPL-3.0 |
| ZapFast's bundled notification sounds | **GPL-2.0** (Pidgin) | `zf:THIRD-PARTY-NOTICES.md` |
| SQLCipher with vendored OpenSSL, openh264, libopus, libwebp, symphonia, rodio/cpal, egui, nokhwa, Noto Color Emoji | BSD-style + Apache-2.0, BSD-2, BSD-3, BSD-3, MPL-2.0, MIT/Apache, MIT/Apache, Apache-2.0, OFL | general knowledge, **not checked file by file** |

What follows from the licences:
- **A binary built on whatsmeow is bound by the GPL-3.0.** It ships libsignal-protocol-go inside the same program.
- **openh264 is built from source**, so Cisco's patent-licensed binaries don't cover it. VideoToolbox avoids the question.
- **Don't carry over** the Pidgin sounds or Noto.

## 5. Port size

These are measured Rust non-test lines, then estimates for Go. Go is more verbose than Rust. Our kit replaces part of ZapFast's widgets, and egui's built-ins cost ZapFast no lines but cost us framework work. I assume Go app code comes to 0.9–1.3× the Rust lines.

| Area | ZapFast (non-test lines) | Go app code (estimate) |
|---|---|---|
| App shell and state reducers (`app.rs`, `account.rs`, settings, `nav`, keys, focus, paths) | 9.8k + 0.35k + 1.0k + 0.9k + 0.4k | 11–14k |
| Chat list, rail, account switcher, labels | 1.07k + 0.39k + 0.32k + 0.53k | 2.5–3k |
| Conversation: transcript, bubbles, composer, header, pins, interactive messages, polls | 9.65k + 0.55k | 10–13k |
| Dialogs, settings, info panel, message info, search pane, login, lock, update | about 7.2k | 7–8k |
| Picker (emoji, GIF, stickers) | 0.8k | 1–1.5k, plus emoji data |
| Media viewer and editor | 1.8k | 2k |
| Calls UI | 1.3k + 0.69k + 0.66k | 2.5–3k |
| Widgets, theme, glass | 1.74k + 0.78k + 0.31k | 1.5–2.5k, much covered by `ours:widget` |
| Text: markup, mentions, transcript, bidi glue, emoji glue | about 2.2k | 2–3k |
| Native services: notifications, badge, status item, panels, drop, pasteboard, window frame, autostart | (fastframe) | 3–5k, in the framework |
| Native media: ImageIO, AVPlayer, Ogg/Opus, recorder, animation | 3.9k | 3–5k |
| **Go UI total for parity (Rust core)** | — | **about 45–55k** |
| **P0 slice (Rust core)** | — | **about 12–18k app**, plus about 8–10k framework (below) |
| Rust side: the `zfcore` ABI crate, DTOs, demo core, removing egui | — | 3–5k new Rust |
| **Full Go port: extra backend** (worker logic, archive, model translation) | 18.5k + 4.3k + part of 2.9k | **25–35k Go, without calls** |

Framework work the P0 slice needs:

| Work | Estimate (Go lines) |
|---|---|
| Multi-line editor | 2–3k |
| Rich text with spans, links and inline emoji, and RTL wrapping | 2–3k |
| Variable-height virtual list | 1–1.5k |
| Image decoding and cache | 1–1.5k |
| Notifications and badge | 0.6k |
| NSMenu, panels and drop (5B, already planned) | 3–4k |
| Packaging scripts | 0.5k |

## 6. Recommendation: Rust core with a Go UI, or a full Go port

**A Rust core is better for reaching parity, and for reaching it sooner.**
- **The separation exists and is enforced** (§3).
- **About 23k lines of backend logic encode protocol rules that took the upstream project many releases to get right:**
  - favourites written as a whole list with a queue of local changes (`zf:AGENTS.md`);
  - "Delete for me" kept pending, with deletion barriers, so history cannot restore deleted messages;
  - poll baselines with a backoff on recovery requests;
  - group receipts taken from the least advanced recipient (`zf:src/archive/receipts.rs`);
  - LID canonicalisation.
- **Calls exist only in whatsapp-rust.**
- **The data stays compatible:** the SQLCipher archive and the session can be read.
- **Licences stay MIT.**

**Its costs:**
- a bundled, non-system dylib, which needs D2 approval and conflicts with the "only OS system libraries" constraint;
- two toolchains and a universal-binary build;
- maintaining an ABI against an upstream that changes daily (trunk-based, `zf:AGENTS.md` "Branches");
- Rust threads inside a Go process that uses fakecgo (signals, aborts);
- a larger bundle, with unknown startup cost.

**A full Go port** gives one toolchain and no dylib. It means:
- no calls;
- the GPL-3.0 through libsignal;
- re-linking and losing the archive;
- re-implementing 25–35k lines of backend.

It is reasonable later, if calls are dropped and the GPL is acceptable. It is not reasonable as the first route.

**A middle path:** start with the Rust core. Keep the Go UI's model types independent of the ABI's JSON, so that a later whatsmeow backend could sit behind the same internal interface.

## 7. Risks

1. **Framework gaps on the P0 path:** multi-line editing, rich text and variable-height lists are each L, and PHASE5 places none of them in scope.
2. **The zero-allocation rule against a chat app.** Decoding events, keeping image caches and sending hot commands while typing all need care. The rule for the JSON boundary is: decode off the main thread and make hot commands binary.
3. **Objective-C from worker threads.** `internal/objc` shares one call frame and is not goroutine-safe (`ours:fs/open_darwin.go` header). AVFoundation, notifications and panels need main-thread dispatch, or per-thread frames and blocks that capture context. Only a global block exists today (`ours:gpu/metal_darwin.go:295`).
4. **The bundled dylib against "only OS system libraries" (D2).** It also brings code signing, notarisation of the nested dylib, universal builds, and size: ZapFast's macOS binary size is unknown, and our < 10 MB gate covers framework examples, not this app.
5. **A Rust runtime inside Go.** With `panic=abort` a Rust panic ends the app. Non-Go threads in a fakecgo process need verifying for signals.
6. **ABI drift.** About 245 variants, a fast-moving upstream that is MIT but pinned to forks of egui, winit, rodio and whatsapp-rust, and fork maintenance.
7. **Licences.** whatsmeow brings libsignal under the GPL-3.0. openh264 raises patent questions. The bundled Pidgin sounds are GPL-2.0.
8. **Data compatibility** if the backend changes: SQLCipher and the session schema.
9. **Notifications need a signed bundle**, which makes the development loop heavier (`UNUserNotificationCenter` refuses unbundled binaries).
10. **Unverified native codecs.** Opus through AudioToolbox has not been tried. ImageIO cannot encode WebP, which sticker making needs.
11. **The startup budget.** A Keychain read and the SQLCipher open come before the first chat list. The raw key avoids a KDF (`zf:src/archive/encryption.rs:233`). ZapFast defers migrations until after the first frame (`zf:src/backend.rs:1493`), and our content-first rule wants the chat list in that frame. This needs measuring.
12. **Accessibility regresses at first.** ZapFast ships screen-reader support; our bridge is phase 6.
13. **Using an unofficial WhatsApp client can get the account banned.** This holds for both libraries; it is a product risk, not a technical one.

## 8. Not checked

- I did not build or run ZapFast, the demo or whatsmeow. All findings come from reading the source.
- I did not clone whatsapp-rust. I only fetched its manifests and licence.
- fastframe: I read only the crate manifests and file lists.
- Per-crate licence files beyond whatsapp-rust, fastframe, whatsmeow, libsignal-protocol-go, argo-go and go-util. The rows marked "general knowledge" above are unverified.
- Whether AudioToolbox Opus works on macOS 14+, and ImageIO's animated WebP in practice.
- ZapFast's macOS binary size and startup. The only measurements are Linux numbers: 152 ms to the first window and 150–200 MB idle (`zf:docs/_reference/benchmarks.md`).
- Whether whatsmeow can pair as a companion to a Business account.
- Whether Rust threads in a fakecgo Go process handle signals correctly.
