# Go toolkits and other modern native toolkits: what a real app expects from us

Date: 2026-10-18. Scope: Gio, gio-x, gogio, Fyne and its `fyne` tool, Wails v3, Slint and Iced. I read them to compare their app-level surfaces with `~/Projects/go-native-ui` (module `gonativeui`) and to rank our gaps for ZapFast on macOS. I read their code for ideas only and copied none of it.

**Sources:** shallow clones under `~/Projects/research/`. Paths below are relative to that directory.

| Repo | Commit | Date |
|---|---|---|
| `gio` | c170b58 | 2026-10-15 |
| `gio-cmd` | 2e72e8f | 2026-02-28 |
| `gio-x` | 428afa5 | 2026-09-28 |
| `fyne` | 37712f5 | 2026-10-18 |
| `tools` (Fyne CLI) | e55dfe1 | 2026-10-16 |
| `wails`, branch `v3-beta` (there is no `v3-alpha` branch) | ad2026e | 2026-05-21 |
| `slint` | 549b40e | 2026-10-18 |
| `iced` | 08eb93a | 2026-10-17 |

I read our repo without changing it. I used ZapFast (`zapfast/Cargo.toml`, `zapfast/src/`) only to decide what counts as P0.

**Priority:**
- **P0:** ZapFast on macOS needs it.
- **P1:** most real apps need it.
- **P2:** nice to have.
- **P3:** niche.

**Effort:**
- **S:** 2 days or less.
- **M:** up to about 2 weeks.
- **L:** more than that.

---

## 1. Summary

**Where we stand.** Our frame-path core is ahead of every Go toolkit I read:
- zero allocations per frame, event-driven idle, content-first startup;
- semantics on every widget;
- a test harness driven by semantics: `widget/widgettest` finds widgets by role and name, waits on conditions, gates allocations and renders offscreen.

Only Slint's testing backend is close to the harness (`slint/internal/backends/testing/search_api.rs`).

**What we lack.** Everything around the frame that an app touches once it leaves the demo:
- images;
- rich, selectable, multi-line text;
- notifications;
- the Dock badge;
- a tray (status) item;
- the app lifecycle without windows;
- preferences and app paths;
- the Keychain;
- a single instance;
- i18n with plurals;
- logging;
- packaging, signing and notarisation.

Some of these are already planned for phase 5B: native menus, file dialogs, drag and drop, more than one window, and `task`. Phase 6 covers accessibility.

**Where the other toolkits are.**

- **Fyne and Wails v3** have the most complete app-service surfaces in Go:
  - preferences and secret preferences, storage, cache, notifications, tray, i18n, metadata, and the `fyne` tool (Fyne);
  - single instance, dock badge, notifications with actions, a slog logger, a panic handler, path kinds, events, and `wails3` packaging (Wails).

  Their APIs are callback- and closure-heavy, so we should borrow their **coverage and option shapes**, not their call style.
- **Gio** is closest to us in model: immediate mode, Go. Its lessons:
  - an item-anchored virtual list (`ScrollToEnd`, `Position{First, Offset}`);
  - an app-global event iterator (`URLEvent`);
  - separate state and style types.

  Its app services are thin, spread across `gio-x`, and partly experimental.
- **Slint** sets the bar for:
  - i18n: `@tr` with contexts and plurals, an extractor, bundled catalogs;
  - styles: fluent, cupertino, material, cosmic and qt, each with `-light`/`-dark`;
  - live preview;
  - agent tooling: an embedded MCP server that exposes the element tree, screenshots, clicks and recording.
- **Iced** has the cleanest async model (`Task` and `Subscription`):
  - a daemon mode without windows;
  - rich clipboard kinds;
  - markdown and rich-text widgets;
  - a `sensor` widget that reports when content scrolls into view;
  - palette generation from seed colours;
  - recorded `.ice` end-to-end tests with a devtools recorder.

**ZapFast's P0 needs.** ZapFast's own `fastframe-*` crates (`zapfast/Cargo.toml` lines 140–187) are a ready-made checklist of what the framework should own:
- i18n, icons, fonts, emoji, theme, log, tray;
- shell (run without a window, remember geometry), titlebar, single instance, scroll;
- dock (badge), autostart, notifications, power (sleep/wake), atomic file saves, update.

Beyond those, the largest P0 gaps for the chat UI itself are:
1. a decoded, cached and animated **image pipeline**;
2. a **variable-height, bottom-anchored virtual list**, for the message timeline;
3. **rich, selectable text** (WhatsApp markup, links, mentions, bidi);
4. a **multi-line composer**.

---

## 2. Feature table

"Ours" columns cite `go-native-ui/…`. The `task` package, native menus, file dialogs, drag and drop and more than one window are **planned** in `docs/PHASE5-PLAN.md` §5B but not yet in the tree (there is no `task/` directory).

### 2a. App services (outside the frame path)

| Feature | What it gives an app | How they do it (paths) | Ours | Pri | Effort | Notes |
|---|---|---|---|---|---|---|
| Preferences / settings store | Typed key-value settings that persist; change notification | Fyne: `fyne/preferences.go` (typed get, `…WithFallback`, set, `AddChangeListener`); JSON file, saved on change, watched for external edits (`fyne/app/preferences.go:70-128`, `app/preferences_other.go:21`). Wails: `wails/v3/pkg/services/kvstore/kvstore.go` (Get/Set/Delete/Save). gio-x has only read-only OS prefs (`gio-x/pref/`) | **Missing.** The only `NSUserDefaults` use is reading the system appearance (`platform/platform_darwin.go:1287`) | P0 | S | Settings are off the frame path, so allocating there is fine. Write a temp file, fsync, rename (as `fastframe-file` does). Save on a worker with a debounce. Typed fields in an app struct, read synchronously at startup (ARCHITECTURE §3 rule 3) |
| Secret storage (Keychain) | Tokens and DB keys kept out of plain files | Fyne `SecretPreferences` (`fyne/app.go:92-112`; `app/secret_preferences_darwin.m` uses `SecItem*`; an encrypted-file fallback elsewhere) | **Missing** | P0 | S | ZapFast keeps its SQLCipher key in the keyring (`keyring-core`). Without cgo: Security.framework `SecItemAdd/CopyMatching/Update/Delete` through our objc/ffi. Needs a signed bundle for a stable Keychain identity |
| App data / cache / log directories | The right per-OS places for the DB, the media cache and logs | Gio `app.DataDir` = `os.UserConfigDir` (`gio/app/app.go:134`, `app/datadir.go`). Fyne `Storage()` and `Cache()` (since 2.8) under the app ID (`fyne/app/storage.go`, `app/cache.go`). Wails path kinds: DataHome, ConfigHome, CacheHome, StateHome, Downloads and more (`wails/v3/pkg/application/path.go:5-70`) | **Missing.** There is no app ID concept: `tools/mkapp.sh` takes a bundle ID only for packaging | P0 | S | Provide `app.ID` (reverse-DNS) and `app.Dirs{Data, Cache, Logs, State}`: on macOS `~/Library/Application Support/<id>`, `Caches/<id>` and `Logs/<id>`. Read the ID from the bundle's `CFBundleIdentifier` when running bundled, as Gio does on iOS (`gio/app/app.go:30-41`) |
| Logging | Diagnosable field failures; a log file when launched from Finder | Wails `Options.Logger *slog.Logger` and `LogLevel` (`application_options.go:57-60`), log service (`services/log/log.go`). Gio redirects stderr to the OS log on Windows, Android and iOS (`gio/app/log_windows.go`, `log_ios.go`). Fyne has only `LogError` (`fyne/log.go`) | **Missing.** There is `app.StateLine` for frame diagnostics (`app/diag.go`) but no logger | P0 | S | Accept a standard `*slog.Logger` rather than inventing an API. Ship a default handler that writes stderr plus a per-run file in `~/Library/Logs/<id>` with rotation. Never log on the frame path |
| Panic / crash capture | A record of crashes without user data | Wails `PanicHandler func(*PanicDetails)` (`application_options.go:70`, `panic_handler.go:50`) | **Missing** | P1 | S | Recover in `app.Run`, write the stack to the log directory, and show a dialog on the next launch. ZapFast records panics without their payload (`fastframe-log`) |
| Single instance + hand-off | A second launch focuses the first and passes its arguments or URL | Wails `SingleInstanceOptions{UniqueID, OnSecondInstanceLaunch, AdditionalData, EncryptionKey}` (`single_instance.go:28-46`). On macOS: a lock file in TempDir plus `NSDistributedNotificationCenter` (`single_instance_darwin.go`) | **Missing** | P0 | S | macOS mostly enforces this through LaunchServices for bundles, but `open -n` and CLI launches do not. Use `flock` in a per-user runtime directory plus a Unix socket the user alone can open (as ZapFast does, `zapfast/src/single_instance.rs`). Deliver the hand-off as an app event |
| App lifecycle without windows | Keep running in the tray or Dock with no window; reopen on a Dock click; quit policy | Iced `daemon` (`iced/src/daemon.rs`). Wails `MacOptions{ActivationPolicy, ApplicationShouldTerminateAfterLastWindowClosed}` (`application_options.go:244-250`), the `mac:ApplicationShouldHandleReopen` event (`pkg/events/events.go:117`), `ShouldQuit` and `OnShutdown` (`application_options.go:81-94`). Fyne `Lifecycle()` (foreground, started, stopped; `fyne/app.go:199-205`) | **Partial.** D30 plans "the app quits on the last close, optional on macOS". There is no reopen event or activation policy. `app.Run` owns one window (`app/app.go`) | P0 | M | ZapFast "keeps running without a window" (`fastframe-shell`). Needed: an accessory or regular activation policy, `applicationShouldHandleReopen:`, `applicationShouldTerminate:` routed to the close-refusal path, and an app-level event queue drained by Build. The pump must keep running with zero windows (D29's `app.CommandHandler`) |
| Open URL / file events | Handle `zapfast://`, `whatsapp://` or a file dropped on the Dock icon | Gio `URLEvent` through the `app.Events` iterator; schemes registered by gogio (`gio/app/app.go:62-70,155`; `gio-cmd/gogio/macosbuild.go:153`). Wails `ApplicationOpenedWithFile`, `FileAssociations` (`events.go:9`, `application_options.go:109`). Fyne `CanOpen` metadata (`tools/cmd/fyne/internal/metadata/data.go`) | **Missing** | P1 | S | The `application:openURLs:` delegate becomes an app event. The packager writes `CFBundleURLTypes` and `CFBundleDocumentTypes` |
| Open in the default app / reveal in Finder | Open a link in the browser, an attachment in Preview, "Show in Finder" | Fyne `App.OpenURL` (`fyne/app.go:35`). Wails `EnvironmentManager.OpenFileManager(path, selectFile)` (`environment_manager.go:52`) and the browser manager | **Partial.** `fs.Open(path)` through `LSOpenCFURLRef` (`fs/open.go:8`, `fs/open_darwin.go:18`); no `OpenURL` for http(s), no reveal | P0 | S | Add `OpenURL(string)` (NSWorkspace `openURL:`) and `Reveal(path)` (`activateFileViewerSelectingURLs:`) |
| Notifications | Message alerts with a click back to the chat, a reply action, withdrawal | Wails `NotificationService` (`services/notifications/notifications.go:47-199`): authorisation, categories with actions and a reply field, results with the action ID and the typed text, removal of delivered and pending notifications; `notifications_darwin.m` uses UNUserNotificationCenter. Fyne `SendNotification` and `ScheduleNotification` (`fyne/app.go:65-84`, `notification.go`, `app/app_darwin.m`). gio-x `notify` (`notification_manager.go`), whose README warns of signing issues on macOS | **Missing** | P0 | M | UNUserNotificationCenter needs a signed `.app` with a bundle ID, so `go run` cannot exercise it (tooling, §4). The delegate callbacks (click, reply) become app events with an ID payload. ZapFast: delivery, permissions, clicks, withdrawal (`fastframe-notifications`, `zapfast/src/notify.rs`) |
| Dock badge / attention | Unread count on the Dock icon; bounce | Wails dock service: `SetBadge`, `SetCustomBadge`, `RemoveBadge`, `HideAppIcon` (`services/dock/dock.go:56-81`; `dock_darwin.go:22-37` uses `[NSApp dockTile] setBadgeLabel:`) | **Missing** | P0 | S | `NSDockTile setBadgeLabel:` and `NSApp requestUserAttention:`. Windows overlay icon later (ZapFast `fastframe-dock`) |
| Tray / status item | Stay linked in the menu bar; unread state; menu | Wails `SystemTray`: `SetIcon`, `SetDarkModeIcon`, `SetTemplateIcon`, `SetLabel`, `SetMenu`, `AttachWindow`, `PositionWindow`, `OnClick` (`systemtray.go:81-361`; `systemtray_darwin.m` uses NSStatusItem). Fyne `desktop.App.SetSystemTrayMenu`, `SetSystemTrayIcon` (themed) and `SetSystemTrayWindow` (`fyne/driver/desktop/app.go`) | **Missing** | P0 | M | NSStatusItem with a template image and an NSMenu built from our `menu.Model`. A menu click arrives as a command (D29). Rasterise the icon from SF Symbols or a bitmap |
| Native menus (NSMenu bar, context menus) | ⌘Q, Edit menu, Services, key equivalents | Wails menu and context-menu managers (`menu_darwin.go`, `context_menu_manager.go`). Fyne `menu_darwin.m` (`internal/driver/glfw/`) | **Planned** (D29, 5B step 8). Today the model is `menu/` and the drawn bar is `widget/menus.go` | P0 | M | The plan is sound. Native context menus matter for "right-click a message" |
| Native file dialogs | Attach a file; save media | Wails builder dialogs (`dialogs.go:212-489`: filters, a start directory, multiple selection, sheets through `AttachToWindow`). gio-x `explorer` (`explorer.go:58-254`, returning io.ReadCloser). Fyne `dialog/file_darwin.go` | **Planned** (5B step 9) | P0 | M | Return paths in an event (planned). Also add a **native alert** (`NSAlert`: message plus buttons, default and cancel), as in Wails `MessageDialog` (`dialogs.go:104-155`), for "quit while a call is active" |
| Drag and drop in / out | Drop files on the composer; drag an image out | Gio `io/transfer` (MIME-typed source and target, `transfer.go`). Wails `EnableFileDrop` (`webview_window_options.go:116`) | **Planned in only** (5B step 11); dragging out is excluded in phase 5 | P0 in, P1 out | M | Dropping attachments is core chat UX |
| Rich clipboard (image, files, HTML) | Paste a screenshot into the composer; copy an image | Iced `clipboard::read_text/html/files/image`, `write(Content)` (`iced/runtime/src/clipboard.rs:33-108`). Gio `io/transfer` by MIME type | **Partial.** Text only: `platform.SetClipboardText([]byte)`, `RequestPaste` → `EvPaste` (`platform/paste.go`, `paste_darwin.go:26,58`) | P0 | M | Extend `RequestPaste` with a kind mask (text, image, file URLs). Image bytes arrive in a leased buffer, as text does. ZapFast uses `arboard` with image data |
| Power / sleep-wake | Reconnect the socket after wake | Wails `windows:APMSuspend` and power events (`pkg/events/events.go:381`) | **Missing** | P1 | S | `NSWorkspaceWillSleepNotification` / `DidWake` → app event (ZapFast `fastframe-power`). No polling |
| Window geometry persistence | Reopen where it was; recover from a display that is gone | Wails `InitialPosition`, `Screen`, `StartState` (`webview_window_options.go:71-102`), screen manager (`screenmanager.go`). Fyne has no equivalent | **Missing.** `app.Config` has a size and minimum only (`app/app.go:84-93`) | P0 | S | On macOS, `setFrameAutosaveName:` persists and constrains the frame for free. It also fits content-first startup, because the frame is known before the first frame. Expose a `Frame` in Config plus a screen list for other OSes |
| Window extras | Vibrancy sidebar, always-on-top, content protection, full-size content | Wails `BackgroundTypeTranslucent`, `AlwaysOnTop`, `ContentProtectionEnabled`, `HideOnFocusLost`, Mac backdrop (`webview_window_options.go:46-216`). Gio `Decorated`, `TopMost` (`gio/app/window.go:984-993`) | **Partial.** A custom titlebar and traffic-light placement (`platform/platform.go:247-252`); no NSVisualEffectView | P1 | M | ZapFast's Messages theme uses AppKit's sidebar material behind the window (`zapfast/src/macos.rs:1-4, 337`). It needs a transparent clear plus an NSVisualEffectView behind the Metal layer |
| Autostart at login | Start in the tray at login | Wails has none in the core. ZapFast uses `SMAppService` (`fastframe-autostart`) | **Missing** | P2 | S | `SMAppService.mainAppService register` (macOS 13+) |
| Self-update | Ship fixes outside the App Store | None of the five | **Missing** | P2 | L | App-level. ZapFast has its own signed updater (`fastframe-update`). Document a pattern, not a framework feature |
| App metadata | Name, version and ID at runtime | Fyne `Metadata()` filled by `fyne package` or `go run` (`fyne/app.go:127`; `tools/cmd/fyne/internal/metadata/data.go`, FyneApp.toml) | **Missing** | P1 | S | Read the bundle's `Info.plist` through NSBundle; fall back to `-ldflags -X` |
| Permissions declarations | Microphone and camera prompts for calls | Fyne `Capabilities{Microphone, MicrophoneUsage}` → Info.plist and entitlements (`metadata/data.go:56-70`). Gio `app/permission/*` imports | **Missing** | P1 | S | Packager concern (`NSMicrophoneUsageDescription`, `NSCameraUsageDescription`, hardened-runtime entitlements) |

### 2b. UI and content

| Feature | What it gives an app | How they do it (paths) | Ours | Pri | Effort | Notes |
|---|---|---|---|---|---|---|
| Image loading + texture cache | Avatars, photos, stickers, QR codes, previews | Iced `image::Handle::from_path/from_bytes/from_rgba` (`iced/core/src/image.rs:111-134`), `image::allocate` returns a Task that guarantees the image draws in the next frame (`runtime/src/image.rs:15-20`). Gio `widget.Image{Src paint.ImageOp, Fit, Position}` (`gio/widget/image.go:17-33`), stdlib decoders. Fyne `canvas.Image` plus an SVG cache (`fyne/internal/cache/svg.go`) | **Partial.** `ui.NextImage(tex, uv, mode)` draws a texture rectangle (`ui/stack.go:134-148`, `ui/draw.go:313`); RGBA atlas pages exist (ARCHITECTURE §6); no decoder, no `Kit.Image`, no cache or eviction, no fit or rounded clip | P0 | L | Decode on workers: ImageIO `CGImageSource` on macOS gives JPEG, PNG, GIF, HEIC and WebP with no new dependencies; WIC on Windows; the stdlib plus a WebP decoder on Linux. Upload into atlas pages, or into dedicated textures for large photos. Clock eviction keyed by (id, size), so `Kit.Image` and an `Avatar` allocate nothing. ZapFast had to bolt eviction onto egui (`zapfast/src/image_cache.rs:1-7`), so cache eviction is a framework duty |
| Animated images (GIF, WebP, APNG) | GIFs and animated stickers in place | Iced and Fyne decode through crates or libraries; ZapFast uses `egui_extras` gif and webp | **Missing** | P0 | M | ImageIO exposes frame count and delays (GIF, WebP and APNG dictionaries). Animate only while the image is visible, via `RequestFrame` and deadlines, to keep the idle bar |
| SVG | Logo, vector icons | Iced `svg` widget (`widget/src/svg.rs`); Fyne oksvg; Slint built in | **Missing.** SF Symbols cover the icons (`icon/symbol_darwin.go`) | P2 | M | Rasterise once at build time with a tool, or write a small pure-Go path rasteriser. Not on the critical path for macOS |
| Variable-height virtual list, bottom-anchored | Chat timeline: messages of any height, stick to the bottom, load older messages without a jump | Gio `layout.List{ScrollToEnd}` with `Position{BeforeEnd, First, Offset}` (`gio/layout/list.go:25-76,313`): the scroll position is an item plus an offset, so items measure lazily. egui `stick_to_bottom` is used by ZapFast (`zapfast/src/ui/conversation.rs:2668`) | **Missing.** Row pitch is fixed per list (`widget/list.go:198-202`, ARCHITECTURE §8) | P0 | M–L | Keep `First` and `Offset` anchoring plus a pointer-free per-item height cache, filled as rows are laid out and estimated otherwise. Scroll by item. Prepending shifts `First` and leaves the offset alone. "At end" pins to the bottom. The scroll bar works from estimated totals. This is the core of ZapFast |
| Visibility sensor | Lazy-load media, mark messages read, pause GIFs off-screen | Iced `sensor` (messages when content enters or leaves view; `widget/src/sensor.rs`) | **Partial.** Virtual lists build only visible rows; there is no explicit signal | P1 | S | `Signal.Appeared` / `Disappeared` from the box cache (first frame built, pruned) |
| Rich text (styled spans, links) | WhatsApp markup (`*bold*`, `_italic_`, `~strike~`, mono), links, @mentions | Iced `rich_text` spans with link payloads (`widget/src/text/rich.rs:48-119`). gio-x `richtext.InteractiveSpan` (hover, click, long press, metadata; `richtext/richtext.go:24-237`) and `styledtext`. Fyne `RichText` segments (`fyne/widget/richtext.go`). Slint `StyledText::from_markdown` (`slint/internal/core/styled_text.rs:46-55`) | **Missing.** Labels have one style (`widget/text.go`); wrap exists (`ui/wrap.go`, `text/wrap.go`) | P0 | L | Spans hold offsets into a byte buffer plus a style id (pointer-free). Shape per run with one font key, then wrap across runs. Hit-test spans for link hover, cursor and click. Mentions and links come out as span ids in a signal. ZapFast's parser (`zapfast/src/markup.rs`) stays app-level; the framework renders spans |
| Selectable read-only text, across widgets | Select and copy text in and across messages | Gio `widget.Selectable` (`gio/widget/selectable.go:54`). Fyne `selectable.go`, `RichText.SelectedText` (`widget/richtext.go:190`). ZapFast: "select and copy text across messages" (README) | **Missing** | P0 | M–L | First a single label (caret logic exists in `text/caret.go`), then a selection that spans boxes in build order, with ⌘C through the Edit role |
| Multi-line text editor | The message composer: wrap, grow to N lines, Enter vs Shift+Enter, IME | Gio `widget.Editor` (multi-line by default; `SingleLine`, `Submit`, `MaxLen`, `Filter`, `ReadOnly`, `InputHint`; `gio/widget/editor.go:39-72`). Iced `text_editor` with a highlighter (`widget/src/text_editor.rs`). Fyne `Entry.MultiLine`, `RichTextEntry` with undo (`widget/richtextentry*.go`). egui `TextEdit::multiline` (`zapfast/src/ui/conversation.rs:1651`) | **Missing.** `ui.TextBuffer` and `TextField` are single-line; multi-line editing is excluded from phase 5 (`docs/PHASE5-PLAN.md`, "Not in this phase") | P0 | L | Reuse the undo, IME, paste and claim machinery. Needs wrapped caret navigation (up and down by x), visual-line selection and auto-height. The bidi caret exists (`text/caret.go`) |
| Bidi paragraphs + RTL UI mirroring | Arabic and Hebrew chats; RTL locales | Gio `system.Locale{Language, Direction}` in the layout context (`gio/io/system/locale.go`). ZapFast patched egui for bidi reordering per line (`zapfast/src/bidi.rs:1-12`) | **Partial.** Shaping is bidi (CoreText `CTLine`, `text/coretext_darwin.go:469`; go-text on Linux, `text/rtl_alloc_test.go`) and carets are bidi (`text/caret.go`). Wrapped multi-run paragraphs and mirrored layout are unverified or missing | P0 (bidi text) / P2 (mirrored UI) | M | ZapFast ships Turkish but no RTL UI language; RTL *content* is P0. Each wrapped line needs its own visual reorder |
| Colour emoji | Emoji in messages and the picker | Fyne bundles emoji (`fyne/theme/bundled-emoji.go`). ZapFast `fastframe-emoji` | **Have.** CoreText colour glyphs (`text/coretext_darwin.go:83`), CBDT on Linux (`text/cbdt.go`) | — | — | Check that one emoji cluster (families, skin tones, flags) lays out one emoji wide, which ZapFast had to patch in egui |
| Markdown | Help, release notes, rich docs | Iced `markdown::parse` + `view`, with links as messages (`widget/src/markdown.rs`). gio-x `markdown` on goldmark (`markdown/markdown.go`). Fyne `NewRichTextFromMarkdown` (`widget/markdown.go`). Slint `from_markdown` | **Missing** | P2 | M (after rich text) | A small CommonMark subset → spans. Comes almost for free once rich text exists |
| Theming API | Custom colours; follow the system; per-app accent | Fyne `Theme` interface with `Color(name, variant)`, `Font`, `Icon`, `Size` (`fyne/theme.go:28`), JSON themes (`theme/json.go:19-46`), `Settings.PrimaryColor/Scale/ShowAnimations` (`settings.go:17`). Iced built-in themes plus `Extended::generate` from seed colours (`core/src/theme/palette.rs:27,122`). Slint styles with `-light`/`-dark` (`internal/compiler/fileaccess.rs:100-105`), `SLINT_STYLE` (`lib.rs:327`). Gio `material.Palette` (`widget/material/theme.go:18-77`) | **Have (mostly).** Tokens, palettes, metrics, motion (`theme/`); Rex light and dark (`theme/rex`); `Follower` crossfade (`theme/follow.go`); `Kit.SetPreference`, `SetAccent` (`widget/kit.go`) | P1 | S–M | Gaps: (a) derive a full palette from 2–3 seed colours, for ZapFast's custom colours (Iced's idea); (b) load and save a palette file; (c) honour "reduce motion" and "increase contrast" (`NSWorkspace accessibilityDisplayShouldReduceMotion`) |
| Component library breadth | Fewer custom widgets per app | Fyne: Accordion, Calendar, DateEntry, Form with validation, Hyperlink, GridWrap, Tree, TextGrid, Toolbar, Activity, PasswordEntry, SelectEntry (`fyne/widget/`); dialogs for colour, form and progress (`fyne/dialog/`). Iced: combo_box, pane_grid, table, qr_code, image viewer, toggler, float, pin, responsive (`iced/widget/src/`). Slint: datepicker, time-picker, spinbox, spinner, combobox, tabwidget, menus, standardbutton (`slint/internal/compiler/widgets/common/`). gio-x `component`: app bar, nav drawer, sheet, modal layer, menu, tooltip, truncating label (`gio-x/component/`) | **Have the core set** (`widget/`: Button, Checkbox, Switch, Radio, Slider, Select, TextField and Search, ScrollView, List, Table, Splitter, Tabs, Tooltip, Menu, MenuBar, ContextMenu, Dialog, ProgressBar, Titlebar) | see the next row | — | — |
| Missing widgets for ZapFast | Chat chrome | as above | **Missing:** Spinner (indeterminate circular), Image and Avatar (circle-clipped), Badge (count pill), Popover (anchored, with an arrow; emoji and reaction picker), Toast (transient banner), Hyperlink, Image viewer (zoom and pan), Disclosure/Accordion, SpinBox, Tree, Date picker | P0: Spinner, Avatar, Badge, Popover; P1: Toast, Hyperlink, Image viewer; P2: Disclosure, SpinBox, Tree; P3: Date and time picker | S each (M for viewer and popover) | Overlays and placement already exist (`ui/overlay.go`), so Popover is mostly chrome |
| QR code | Linking the device (login) | Iced `qr_code` widget (`widget/src/qr_code.rs`); ZapFast uses the `qrcode` crate | **Missing** | P0 for ZapFast (app-level) | S | The encoder is app-level. The framework needs only a module-grid draw (quads) or an image. Do not vendor an encoder without a licence review (D2) |
| Data binding / reactive state | Widgets update when the model changes | Fyne `binding.Item[T]` with listeners; `BindPreferenceBool` (`data/binding/binding.go:28-104`, `preference.go:18-110`); `Do`/`DoAndWait` hops to the main thread (`fyne/thread.go`). Slint properties and bindings. Iced: none (Elm) | **N/A by design.** Immediate mode reads app state every frame (GUIDE §1) | P3 | — | Do not add listener-based binding: closures break the zero-allocation rule. The one lesson: binding preferences is handy, and typed preference fields give the same benefit |
| Async task helpers | Network and DB work off the UI thread; results into the UI; cancellation | Iced `Task::perform/run/sip/batch/abortable/blocking`, `Subscription::run/with/batch` (`iced/runtime/src/task.rs:31-492`, `futures/src/subscription.rs:127-464`). Fyne `fyne.Do` (`thread.go`). Slint `invoke_from_event_loop`, `spawn_local` (`internal/core/future.rs`), Timer (`timers.rs`). Gio `Window.Invalidate` from any goroutine | **Planned.** `task.Pool[In, Out]` with latest-wins by key and `Poll(dst)` (`docs/PHASE5-PLAN.md`, "Background work"); today `fs.Service` (`fs/service.go`), `platform.Wake` (`platform/platform.go:360`), `ui.Context.WakeAfter`/`RequestFrame` (`ui/textfield.go:48`, `ui/anim.go:69`) | P0 | M | Add a **subscription-like keyed declaration**: a task declared in Build with a key stays alive while it is declared and is cancelled once it is not, as in Iced. It fits immediate mode. Add **timers** (`k.Every(key, d)` / deadline) for "typing…" timeouts and relative timestamps, so apps do not run goroutines plus Wake |
| i18n / l10n | 12 UI languages; plurals; system language | Slint `@tr("…" \| "…" % n)`, contexts (`=>`), `slint-tr-extractor` → `.pot`, gettext or **bundled** catalogs chosen from the system locale (`docs/astro/.../guide/development/translations.mdx`, `internal/core/translations.rs:190-380`, `tools/tr-extractor`). Fyne `lang.L/X/LocalizePlural` on go-i18n JSON with CLDR plural keys, `AddTranslationsFS`, `fyne translate` that scans Go source (`fyne/lang/lang.go:1-176`, `tools/.../commands/translate.go`). gio-x `pref/locale.Language()` (`pref/locale/locale.go`) | **Missing.** Labels are string constants (`widget/text.go`) | P0 | M | ZapFast ships 12 languages as bundled gettext compiled at build time (`zapfast/src/i18n.rs:1-6`). Suggest `.po` → generated Go tables (`go generate`) for zero runtime parsing, plus `T(id)`/`TN(id, n)` with CLDR plural rules for the shipped languages only (a generated rules table, written in-house). System language from `NSLocale preferredLanguages` / `AppleLanguages`; an extractor that scans `k.T("…")` calls. Constant labels then become catalog lookups that return `string`, still with zero allocations |
| Locale-aware formatting | Dates ("Yesterday", 24h clock), numbers, sizes | gio-x has none; Slint uses a decimal separator per bundle (`translations.rs:303-312`); ZapFast reads GNOME's clock format (`fastframe-desktop`) | **Missing** | P1 | M | On macOS read `NSLocale` and the 24-hour preference once at startup plus on `NSCurrentLocaleDidChangeNotification`. Format into `c.Fmt()` without allocating |
| Accessibility bridge | VoiceOver | Slint AccessKit (`internal/backends/winit/accesskit.rs`). Fyne NSAccessibility for button, text and link behind a build tag (`fyne/accessibility.go`, `internal/driver/glfw/accessibility_darwin.go`). Gio Android only (`gio/app/os_android.go`, `io/semantic`) | **Partial.** Semantics recorded for every widget (`ui/semantics.go`); the bridge is phase 6 | P0 (ZapFast enables AccessKit) | L | Our semantics are richer than Fyne's or Gio's: virtual rows report their position, ranges, editable fields. The bridge should map `ui.SemTree` to NSAccessibilityElement proxies keyed by box key |

### 2c. Testing, preview, docs

| Feature | What it gives an app | How they do it (paths) | Ours | Pri | Effort | Notes |
|---|---|---|---|---|---|---|
| Headless test app and interaction | Fast UI tests in CI | Fyne `test.NewApp`, `Tap`, `Type`, `Drag`, `Scroll`, `FocusNext` (`fyne/test/test.go:15-185`). Iced `Simulator` (click by text, `find`; `iced/test/src/simulator.rs`). Slint testing backend: `ElementQuery` by role and label, `invoke_accessible_default_action`, `set_accessible_value`, mock time (`internal/backends/testing/search_api.rs:239-838`, `README.md`). Gio `gpu/headless` | **Have, and strong.** `widgettest` Steps by role and name, `WaitFor`/`WaitGone`, `Rig` (real renderer offscreen or fake text system), deterministic `Clock` (`widget/widgettest/doc.go`, `step.go`, `rig.go`, `player.go:94`) | — | — | Ours already matches Slint's semantic query and goes beyond Fyne's direct method calls |
| Snapshot / golden assertions | Catch visual and structural regressions | Fyne `AssertRendersToMarkup` / `AssertRendersToImage`, which write `testdata/failed/<name>` on mismatch to promote as the new master (`fyne/test/test_helper.go:41-105`, `markup_renderer.go`). Iced `snapshot`, `matches_image`, `matches_hash` (`test/src/simulator.rs:260-348`) | **Partial.** `widgettest.Dump(SemTree)` is the golden text (`dump.go:38-44`; `cmd/gallery/gallery/testdata/semantics-*.txt`); pixel comparisons in `cmd/rexlook` and `tools/rexcompare`; no public helper | P1 | S | Add `widgettest.AssertTree(t, "name")` and `AssertImage(t, "name", tol)`, writing `testdata/failed/` plus an `-update` flag |
| Recorded, text-format E2E tests | Non-programmers record repro scripts; shareable bug reports | Iced `.ice` files (`viewport`, `mode`, `preset`, then `click "…"`, `type "…"`, `expect "…"`; `iced/test/src/ice.rs`, `examples/todos/tests/carl_sagan.ice`) plus a recorder (`iced/tester/src/recorder.rs`). Slint `start_event_recording` (`mcp_server.rs`) | **Missing.** Steps are Go code (`examples/notes/notes/script.go`) | P2 | M | Our Steps map 1:1 onto a line format (`click button "Add"`). Add a recorder that turns live input into role and name steps |
| Introspection / MCP server | An agent or dev tool inspects and drives a running app | Slint `SLINT_MCP_PORT`: `list_windows`, `get_element_tree`, `take_screenshot`, `click_element`, `set_element_value`, `dispatch_key_event`, `start/stop_event_recording` (`slint/internal/backends/testing/mcp_server.rs:1-150`), plus `systest.rs` | **Missing** | P2 | M | A natural fit: we already export `SemTree` and render offscreen. Behind a build tag or env var, off by default, loopback only |
| Hot reload / live preview | Iterate on UI without restarting | Slint live preview: the interpreter reloads `.slint` files, keeps state, and keeps the old UI on a syntax error (`docs/astro/.../tooling/live-preview.mdx`; `internal/live-preview/`), the viewer and slintpad (`tools/viewer`, `tools/slintpad`), the LSP (`tools/lsp`). Wails `wails3 dev` watcher rebuild (`internal/commands/dev.go`, `watcher.go`). Iced devtools time machine (`iced/devtools/src/time_machine.rs`) | **Missing** | P2 | M | Go can't hot-swap code. Practical: `gnui dev` watches, rebuilds (2–3 s), and relaunches into the same window frame with an app-provided state snapshot (`Snapshotter` interface), plus scene previews: run one gallery scene or one Build func offscreen and refresh a PNG. Our offscreen renderer makes this cheap |
| Debug overlays / devtools | Layout and perf inspection | Iced comet and beacon (`iced/devtools/src/comet.rs`, `iced/beacon/`). Slint inspector (`internal/live-preview/inspector.slint`) | **Partial.** Bench JSON, `StateLine` (`app/diag.go`), `-exit-first -v` timeline | P2 | M | Add a toggleable overlay: box outlines, keys, frame CPU, allocations this frame |
| Docs and examples | Learnability | Fyne `fyne_demo` app (`fyne/cmd/fyne_demo`), "Since: x.y" on every API (`fyne/app.go:92-147`). Gio `Example*` funcs (`gio/layout/example_test.go`, `widget/example_test.go`); gogio help text. Iced ~60 runnable examples (`iced/examples/`). Slint docs site with per-language tabs, `best-practices.mdx`, `debugging_techniques.mdx` | **Have a strong base.** `docs/GUIDE.md` with excerpts checked by `TestGuideExcerptsAreTheExample`, the gallery, `tools/doccheck`. **No `Example*` functions** (none in the tree) | P1 | S | Add `Example*` funcs per package for pkg.go.dev. Add a "recipes" section (images, rich text, notifications, prefs). Add "Since" tags once the API is stable |

---

## 3. API-design lessons for a Go framework

1. **Borrow their coverage, not their callbacks.**
   - **The problem:** Fyne (`binding`, `AddChangeListener(func())`) and Wails (`OnClick(func())`, `On(name, func(*CustomEvent))`) are closure-driven, and closures break our zero-allocation rule.
   - **What to do:** OS callbacks (notification click, Dock reopen, URL open, sleep and wake, tray click) should become fixed-size **app events in a ring**, read in Build like `k.Command(id)`. Gio's `app.Events` iterator (`gio/app/app.go:155`) is the Go-idiomatic model for app-global events that belong to no window.

2. **One app ID drives everything.** Gio's `app.ID` (bundle ID, Wayland app_id, X11 class; `gio/app/app.go:30-41`) and Fyne's `NewWithID` both make the ID the key for:
   - the data, cache and log directories;
   - preferences;
   - the Keychain service;
   - the single-instance lock;
   - notifications.

   Add `app.Config.ID`, defaulting to the bundle's `CFBundleIdentifier`.

3. **Group options per OS.**
   - Wails's `Options{Mac MacOptions; Windows WindowsOptions; Linux LinuxOptions}` (`application_options.go:24-30,244`) scales better than flat fields such as our `Titlebar`/`TrafficLights` ("macOS only", `app/app.go:90-91`).
   - Keep shared fields flat and put OS-only fields under `Config.Mac`.

4. **Item-anchored scrolling.** Gio's `Position{First, Offset, BeforeEnd}` (`gio/layout/list.go:63-76`) is the right model for any list whose items can have different heights or arrive at the top. A pixel offset into a total height is wrong for chat and for logs. Keep the fixed-pitch fast path for tables.

5. **Async as keyed, declarative work.**
   - **The model:** Iced's `Subscription` is identified by a key, starts when first declared, and is dropped when no longer declared (`futures/src/subscription.rs:203-245`).
   - **For us:** that fits immediate mode exactly: "declare it in Build every frame you want it". Combine it with the planned latest-wins `task.Pool` (PHASE5-PLAN "Background work").
   - **Guarantee images ready for the first frame:** Iced's `image::allocate` (`runtime/src/image.rs:15-20`) guarantees an image draws in the next frame. For us that would be a "prepare before reveal" call for content-first startup, so cached avatars are decoded before the window shows.

6. **Theme = tokens + generated variants.**
   - Fyne's `Theme` interface is lookup by name and variant (`fyne/theme.go:28`); ours is resolved tokens, which is faster, so keep it.
   - Add Iced's idea: generate the extended palette from a few seed colours (`core/src/theme/palette.rs:27,122`), so "custom colours" in an app is three values, not forty.
   - Add Slint's idea of a style name with `-light`/`-dark` suffixes and an env override (`SLINT_STYLE`) for screenshot and CI runs.

7. **Translations need plurals and context from day one.**
   - Slint's `@tr("ctx" => "one {n}" | "many {n}" % count)` and Fyne's `LocalizePlural` show the minimum API: a key, a plural form and a context.
   - Bundling compiled catalogs (Slint bundled, ZapFast build-time `.po`) avoids runtime parsing and fits our startup budget.
   - Keep the API string-returning, so constant labels stay allocation-free.

8. **Services with a lifecycle beat globals.**
   - Wails's `Service` with `ServiceStartup`/`ServiceShutdown` (`services/*/`) gives notifications, the dock and logging a clean init and teardown. Our version: an `app.Service` interface whose `Start` runs **off** the startup critical path (D21), and whose `Stop` runs on quit, in order.
   - Interfaces for app services (Fyne `App`, `Preferences`, `Storage`) also give tests in-memory fakes. Offer fakes in `widgettest`: a fake notifier that records sends, and in-memory preferences.

9. **Builder dialogs and alerts.** Wails's dialogs (`dialogs.go:104-489`) cover what apps actually set:
   - filters, a start directory and a default filename;
   - a sheet attached to a window;
   - default and cancel buttons.

   Our plan returns results as events, which is right. Keep the option set comparable, and use a struct literal rather than chained setters.

10. **Notifications are a protocol, not a call.** Wails models them as:
    - categories with actions and a reply field;
    - a result that carries the action ID and the user's text;
    - removal of delivered and pending notifications by ID.

    That is what a chat app needs. Fyne's `SendNotification(title, body)` is too thin.

11. **Stability signalling.**
    - Fyne writes "Since: 2.x" on every addition.
    - gio-x states each package's stability and whether it is intended for core (`gio-x/README.md`).

    Before the first external app, mark packages `stable`, `experimental` or `internal` in their package docs.

12. **Keep services out of `widget`.** Gio's split (`gioui.org` core, `gio-x` services with extra dependencies) keeps the core lean. For us: `app/notify`, `app/prefs`, `app/tray`, `app/dock`, `app/keychain` and `i18n`, imported only by apps that use them. That keeps binary size and startup work proportional (a `-ldflags` dead-code advantage).

---

## 4. Developer tooling

What the others ship, and what a `gnui` command should do (macOS first):

| Tool | Theirs | Proposal for us | Pri | Effort |
|---|---|---|---|---|
| Package `.app` | gogio `-target macos` makes icns with `iconutil`, an Info.plist with URL schemes, codesign and notarize (`gio-cmd/gogio/macosbuild.go:113-266`, `help.go`). `fyne package` builds from FyneApp.toml metadata: icon, ID, version, capabilities, `CanOpen` (`tools/cmd/fyne/internal/metadata/data.go`; templates `Info.plist`, `entitlements-darwin.plist`). Wails Taskfile-driven `create:app:bundle` (`internal/commands/build_assets/darwin/Taskfile.yml`) and icon generation from one PNG, including `Assets.car` (`internal/commands/icons.go:37`) | Grow `tools/mkapp.sh` into `gnui package`. Read a small `gnui.toml`: ID, name, version, icon, URL schemes, document types, usage strings, entitlements, `LSUIElement` for tray-only apps. Make icns from a PNG; ICNS is a simple container, so write it in Go and need no Xcode | P0 | M |
| Sign + notarize + DMG | `fyne release` runs codesign with entitlements, then productbuild and productsign (`tools/.../commands/release.go:238-292`). `wails3` sign plus notarize via `notarytool` (`internal/commands/sign.go:66-146`), DMG (`internal/commands/dmg/dmg.go`) | `gnui release`: Developer ID codesign with the hardened runtime, `notarytool submit --wait`, `stapler`, then `hdiutil` DMG. Ad-hoc signing for dev builds already exists in `mkapp.sh` | P0 | S–M |
| Universal binary | Wails `build:universal:lipo:go`, a Go `lipo` (`internal/commands/tool_lipo.go`) | Our builds need no SDK, so a Go fat-Mach-O writer gives arm64+amd64 universal apps without Xcode | P1 | S |
| Run as a bundle in dev | gio-x notes that notifications break unsigned (`gio-x/notify/README.md`) | `gnui run`: build, wrap into an ad-hoc-signed bundle, then `open -W`, streaming the logs. Notifications, the Keychain, URL schemes and the Dock badge need a bundle, so `go run` alone is not enough for them | P0 | S |
| Translations | `fyne translate` scans Go source into a JSON catalogue (`tools/.../commands/translate.go`). `slint-tr-extractor` produces `.pot` (`slint/tools/tr-extractor`) | `gnui i18n extract` scans `T("…")`/`TN(…)` via `go/ast` into `.pot`. `gnui i18n compile` turns `.po` into generated Go tables with plural rules (`go generate`) | P0 | M |
| Dev loop / hot reload | Slint live preview, viewer, LSP, slintpad. `wails3 dev` with a watcher. Iced devtools | `gnui dev`: watch, rebuild, relaunch with the window frame restored plus an optional app state snapshot. `gnui preview <pkg> <Scene>` renders one Build func offscreen to a PNG on every save | P2 | M |
| Inspect / agent bridge | Slint MCP server (`mcp_server.rs`), systest | `GNUI_INSPECT=…` (dev builds only): serve the `SemTree`, screenshots via offscreen, and `widgettest` steps (click and type by role and name) over loopback. It doubles as an MCP server for coding agents | P2 | M |
| Test recording | Iced tester and the `.ice` format | `gnui record` writes widgettest steps as text from live input; `widgettest.RunFile(t, "x.steps")` plays them | P2 | M |
| Doctor / init / templates | `fyne init`/`env`, `wails3 init`/`doctor` (`internal/commands/init.go`, `doctor.go`), gogio help | `gnui init` writes a `notes`-style template with `gnui.toml`. `gnui doctor` checks the Go version, `CGO_ENABLED=0`, codesign identities and notarytool credentials | P2 | S |
| Linux / Windows packaging | Wails AppImage, `.desktop`, nfpm deb/rpm/arch, MSIX, `.syso` (`internal/commands/appimage.go`, `dot_desktop.go`, `msix.go`, `syso.go`). Fyne for each OS | Later. A `.syso` for the Windows icon and manifest (DPI awareness is already required) | P2 | M |

---

## 5. Top gaps (ranked)

| # | Gap | Pri | Effort | Status |
|---|---|---|---|---|
| 1 | Image pipeline: decode on workers (ImageIO), texture cache with eviction, `Kit.Image`/`Avatar` with fit and round clip, animated GIF/WebP | P0 | L | missing (`ui.NextImage` only) |
| 2 | Variable-height, item-anchored virtual list with stick-to-bottom and prepend without a jump (the chat timeline) | P0 | M–L | missing (fixed pitch, `widget/list.go:198`) |
| 3 | Rich text: styled spans, links and mentions with hover and click, multi-run wrap, bidi per line | P0 | L | missing |
| 4 | Multi-line text editor (composer) | P0 | L | missing (out of phase 5) |
| 5 | Selectable read-only text, then selection across messages | P0 | M–L | missing |
| 6 | App lifecycle without windows (activation policy, Dock reopen, quit policy) plus an app-event ring (URL open, sleep and wake, second instance) | P0 | M | partial |
| 7 | Notifications (UNUserNotificationCenter: authorisation, send, click and reply events, withdraw) | P0 | M | missing |
| 8 | i18n: compiled `.po` catalogs, plurals, context, system language, extractor | P0 | M | missing |
| 9 | Packaging, signing and notarisation (`gnui package`/`release`/`run`) | P0 | M | partial (`tools/mkapp.sh`) |
| 10 | Tray status item from `menu.Model` | P0 | M | missing |
| 11 | Preferences plus app ID and app directories (atomic save), window frame autosave | P0 | S | missing |
| 12 | Keychain secrets | P0 | S | missing |
| 13 | Logging (slog file handler in `~/Library/Logs/<id>`) plus panic capture | P0 | S | missing |
| 14 | Dock badge and attention; single instance with hand-off; `OpenURL`/`Reveal` | P0 | S | missing (`fs.Open` only) |
| 15 | Rich clipboard (paste an image or file URLs; copy an image) | P0 | M | partial (text only) |

**Planned but P0 for ZapFast** (already in PHASE5 or phase 6):
- native NSMenu and context menus;
- file dialogs;
- dropping files in;
- `task` (add keyed declarations and timers);
- more than one window;
- the NSAccessibility bridge.

**Next P1s:**
- vibrancy and window materials;
- a visibility sensor;
- Spinner, Badge, Popover, Toast and Hyperlink widgets;
- palette generation from seed colours;
- locale-aware date formatting;
- golden-assert helpers;
- `Example*` docs.

## 6. What I could not check

- **I did not build or run** any of the five toolkits or ZapFast. Every claim comes from reading the source and docs at the commits above.
- **Wails v3 is on the `v3-beta` branch**, last commit 2026-05-21; there is no `v3-alpha` branch.
- **Gio's examples** live in a separate repo (`gio-example`), which I did not clone.
- **Fyne's notification and tray internals on macOS:** I located them (`fyne/app/app_darwin.m`, the `fyne.io/systray` dependency) but did not read them.
- **Unverified OS capabilities:**
  - whether ImageIO decodes animated WebP frames with delays on macOS 14 (I believe it does, since macOS 11);
  - whether NSImage rasterises SVG on macOS 14.
- **Our side, not verified by running:**
  - whether wrapped multi-run bidi paragraphs reorder correctly per line;
  - whether one emoji cluster lays out one emoji wide.

  Both need a test in `text/`.
- **Scope:** I did not compare against egui, Tauri, Electron, winit or Zed, which are other agents' scope (their clones exist under `~/Projects/research/`).
