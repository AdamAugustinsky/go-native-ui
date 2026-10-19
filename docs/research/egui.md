# egui and its ecosystem: what real apps need, and what go-native-ui lacks

Scope: egui / eframe / egui_extras / egui-winit / egui_glow / epaint / egui_kittest / egui_inspection / the demo app, AccessKit (the macOS adapter), rfd, arboard and winit. Everything is judged against go-native-ui (`gonativeui`), with the ZapFast migration on macOS as the first target.

Clones (shallow, read-only), under `~/Projects/research/`:

| Repo | Commit | Notes |
|---|---|---|
| `egui` | `86099ac` (2026-10-15, workspace 0.37.0) | LFS snapshot images were not fetched (no git-lfs). `egui_plot` now lives in its own repo (`egui/crates/egui_plot/README.md`) and was not cloned. `egui_web` holds only a README. |
| `accesskit` | `a39329d` (2026-10-18) | |
| `rfd` | `07d213a` (2026-10-17) | |
| `arboard` | `e8ccf3e` (2026-10-16) | |
| `winit` | `758a50e` (2026-10-16) | split into crates: `winit-core`, `winit-appkit`, … |
| `zapfast` | `d1fdf55` (2026-10-18) | read only to see what it uses |

How the paths below are written:
- Paths are relative to `~/Projects/research/` for the clones.
- A path marked "ours:" is relative to `~/Projects/go-native-ui/`.
- No code was copied; this report describes ideas only.

---

## 1. Summary

**Where we are already ahead of egui:**
- **Text:** CoreText shaping and rasterisation, and real system fallback.
- **Idle and startup:** zero idle CPU and content-first startup.
- **Allocations:** zero per steady-state frame.
- **Pacing:** native frame pacing (`CAMetalDisplayLink`).
- **Live resize.**
- **Scripted tests:** semantic tests driven by role and name (`widget/widgettest`), which are the equivalent of kittest's queries.
- **Look:** the Rex theme.

egui spends effort on several of these and still falls short. Its own fonts are a good example: it bundles Ubuntu-Light and Hack (`egui/crates/epaint_default_fonts/fonts/`) and needs a separate crate just to reach system fallback (`egui/crates/egui_system_font_provider/src/lib.rs`).

**Where we are behind:** the *application* layer that egui, eframe and the crates around them give a chat app for free. For ZapFast on macOS, the blocking gaps are these:

1. **Rich text.** ZapFast builds every message body with `LayoutJob` and `RichText` (WhatsApp markup, links, mentions, emoji). Our wrapped text is single-font and single-colour, and labels cannot be selected.
2. **A virtual list with variable row heights.** ZapFast keeps a height cache, sticks to the bottom and keeps its anchor when rows above it grow (`zapfast/src/ui/conversation.rs:2665-2840`). Our lists need one fixed row height.
3. **A multi-line editor** for the composer (`TextEdit::multiline`, `zapfast/src/ui/conversation.rs:1651`). Ours is single-line only, and the PHASE5 plan explicitly excludes multi-line.
4. **An image pipeline:** decode JPEG, PNG, WebP, GIF and SVG off the main thread, cache, and evict. We can draw an RGBA texture (`ui.NextImage`), but textures are never freed (`gpu/renderer_darwin.go:257`, append-only), and there is no decoder or loader.
5. **Accessibility on macOS.** ZapFast ships with AccessKit, and the semantics we record still have no bridge (phase 6).
6. **Persistence and window state:** settings, window geometry, drafts and scroll positions, saved atomically.
7. **The app and window layer:**
   - set the title, position and size; fullscreen and minimise; focus; Dock attention; reopen from the Dock;
   - more than one window (D30, not built);
   - native menus, file dialogs and drop (5B, not built).
8. **Clipboard images and files** (arboard). Ours is text only.
9. **App zoom** with ⌘+, ⌘− and ⌘0 (egui `gui_zoom.rs`; ZapFast uses `zoom_factor`).
10. **Keys:** punctuation beyond `[ ] .` (so no ⌘, for Settings and no ⌘= for zoom), and modifier-change events.
11. **Objective-C blocks** in `internal/objc`. Sheets, notifications, event monitors and permission prompts all need completion-handler blocks.

The biggest native-feel lessons from egui are its failures:
- drawn widgets and its own fonts;
- no `doCommandBySelector:` key bindings, so Emacs keys and `DefaultKeyBinding.dict` don't work;
- a fixed double-click interval;
- until very recently, no momentum phase or rubber banding;
- an all-frames GIF decode;
- non-atomic persistence.

The best lessons are its developer tooling:
- the debug-on-hover overlay;
- repaint causes recorded with file and line;
- the kittest snapshot and accessibility lint;
- the AccessKit inspector plugin;
- `egui_inspection`: an opt-in local server that exposes the accessibility tree, injects input and takes screenshots, for agents and tools.

---

## 2. Feature table

Legend:
- **Ours:** have, partial or missing.
- **Priority:** P0 means ZapFast on macOS needs it; P1 means most desktop apps need it; P2 is nice to have; P3 is niche.
- **Effort:** S is days, M is one to three weeks, L is more.

### 2.1 State, memory and persistence

| Feature | What it gives an app | How they do it (paths) | Ours | P | Effort | macOS-native notes |
|---|---|---|---|---|---|---|
| Per-widget ephemeral state keyed by id | Widgets and app code keep small state (open, scroll, hover rects) without app-owned structs. ZapFast calls `get_temp`/`data_mut` about 360 times | `IdTypeMap`, `(Id, TypeId) → value`, temp or persisted: `egui/crates/egui/src/util/id_type_map.rs:367-503`; `Memory.data`: `egui/crates/egui/src/memory/mod.rs:31-48` | **Partial.** The box cache keeps hot, active, scroll, text-edit and focus state per key (ours: ARCHITECTURE §8, `ui/context.go`). There is no general typed store for app values; the PHASE5 contract wants app-owned values instead | P1 | S | Keep it pointer-free: a keyed slab of fixed-size records (e.g. `[32]byte` blobs or typed tables in `core`) rather than an any-map, so there are no allocations and no GC scan |
| Persisted UI memory with GC | Scroll positions, open panels and column widths survive a restart. Stale entries age out | Persisted values are kept serialised (RON) with a generation counter, and `max_bytes_per_type` (256 KiB) drops old generations: `id_type_map.rs:71,410-417,724-760`. `persist_egui_memory`: `egui/crates/eframe/src/epi.rs:262` | **Missing** | P1 | M | Persist by stable key hash. Restore synchronously at startup (ARCHITECTURE §3 rule 3 already asks for this) |
| App storage (key-value) with autosave | `App::save(&mut dyn Storage)`, autosave every 30 s, a save on exit | `Storage` trait: `eframe/src/epi.rs:1086-1097`; `auto_save_interval`: `epi.rs:237`; `FileStorage` flushes on a background thread: `eframe/src/native/file_storage.rs:167-195` | **Missing** | P0 | M | **Do better than eframe:** `save_to_disk` writes in place with `File::create` (`file_storage.rs:197-222`), so a crash can leave a torn file. ZapFast had to add `fastframe-file` ("saves that are never half written", `zapfast/Cargo.toml`). Write a temp file, `fsync`, then `rename` (macOS: `F_FULLFSYNC` for critical data), on a worker. Save on `applicationWillTerminate:` / quit |
| Storage location | Per-OS data directory | `storage_dir`: `~/Library/Application Support/<id>` on macOS: `file_storage.rs:17-39` | **Missing** | P0 | S | Use the bundle id. Caches go to `~/Library/Caches/<id>` (media thumbnails), which the system may purge |
| Window geometry persistence | The window reopens where it was, also on another monitor, and is recovered when off-screen. Maximised and fullscreen state is restored after the move | `WindowSettings`: `egui/crates/egui-winit/src/window_settings.rs` (saves the normal geometry while maximised; positions in points on Mac; fullscreen applied after creation) | **Missing.** There is no window position API at all (ours: `platform/platform.go` `Options`; `platform_darwin.go` has no `setFrame`/`windowDidMove`) | P0 | M | **Native way:** `NSWindow setFrameAutosaveName:` (AppKit saves, restores and clamps to the visible screens itself), or `setFrameFromString:`. Store geometry in logical points, *independent of in-app zoom*: ZapFast turned off eframe's `persist_window` because of exactly that bug (`zapfast/src/main.rs:627-633`) |
| App-level undo (`Undoer`) | Undo for app state, with time-based grouping | `egui/crates/egui/src/util/undoer.rs` (`stable_time`, `auto_save_interval`) | **Partial.** Bounded undo for text fields only (ours: `ui/textundo.go`) | P2 | S | Wire to the native Edit menu's Undo/Redo and to `NSWindow` `documentEdited` |

### 2.2 Layout helpers

| Feature | What it gives an app | How they do it (paths) | Ours | P | Effort | macOS-native notes |
|---|---|---|---|---|---|---|
| Flow layout, align, justify | Rows and columns, alignment, fill | `Layout`: `egui/crates/egui/src/layout.rs`; `Sides`: `containers/sides.rs` | **Have.** RAD per-axis layout plus `Row`/`Column`/`Spacer`/`Fixed` (ours: `ui/layout.go`, `widget/layout.go`) | — | — | — |
| Wrapping flow of children (`horizontal_wrapped`) | Chips, reaction bars and tag lists that wrap to the next line | `Layout::with_main_wrap`: `layout.rs`; `ui.horizontal_wrapped` | **Missing.** Only text wraps (ours: `ui/layout.go:33,127` `WrapText`) | P0 | M | ZapFast's reactions and button rows wrap. Add a wrap axis mode to the layout pass |
| Grid / Strip | Labelled forms, settings pages, proportional splits | `egui/crates/egui/src/grid.rs`; `egui_extras/src/strip.rs`, `sizing.rs` | **Partial.** Table, Row, Column, Fixed. No form grid that aligns label columns | P1 | S | Settings windows: right-aligned labels, as on macOS |
| Multi-pass sizing (`request_discard`, `max_passes`) | Auto-sized columns and popups are right in the first visible frame | `Options.max_passes = 2`: `memory/mod.rs:284-291`; sizing passes in `egui_extras/src/table.rs:42-51` | **Have (different).** Layout runs after build on the same frame, and hit testing uses the previous frame (ARCHITECTURE §8) | — | — | Keep. It is the reason we have no first-frame jitter |
| Resizable side panels, collapsible headers | Sidebars that drag, collapse and animate | `containers/panel.rs`, `collapsing_header.rs` | **Partial.** `widget.Splitter` (ours: `widget/split.go`); no collapsing header or disclosure | P1 | S | A disclosure triangle with ⌥-click to expand all is a macOS convention |
| Floating windows, scenes, pan and zoom canvas | In-app tool windows; zoomable canvases | `containers/window.rs`, `area.rs`, `scene.rs` | **Missing** | P2 | M | Prefer real NSWindows / NSPanels (D30) to in-canvas windows on macOS |
| Popups with alignment fallbacks | Menus and tooltips flip when there is no room | `containers/popup.rs` (`align_alternatives`, `get_best_align`) | **Partial.** Overlays are kept inside the window (ours: `ui/overlay.go`). Popups past the window edge are later (PHASE5) | P1 | M | Use NSMenu for context menus on macOS (D29), and child `NSPanel`s for custom popovers that extend past the window |

### 2.3 Text: display, rich text, selection, editing, IME

| Feature | What it gives an app | How they do it (paths) | Ours | P | Effort | macOS-native notes |
|---|---|---|---|---|---|---|
| Rich text (spans of font, colour, underline, background, baseline) | Markup (`*bold*`, `_italic_`, `~strike~`, monospace), mentions, highlighted search hits, timestamps inline | `LayoutJob` / `TextFormat` in `egui/crates/epaint/src/text/`; `RichText`: `egui/crates/egui/src/widget_text.rs`. ZapFast: `LayoutJob` ×20, `RichText` ×26, `zapfast/src/markup.rs` | **Missing.** One font per wrapped box (ours: `ui/wrap.go`, `text/wrap.go`) | **P0** | L | Shape per run with CoreText (`CTTypesetter` over an attributed string, or our run cache per span) and break lines across spans with our UAX #14 breaker. Keep spans pointer-free (offset, length, style index) |
| Inline links (hyperlinks) | Clickable URLs with a pointing cursor that open in the browser | `widgets/hyperlink.rs`; `OutputCommand::OpenUrl`: `egui/src/data/output.rs:140` (ZapFast: `OpenUrl` ×43) | **Partial.** `fs.Open` opens paths (ours: `fs/open.go`, `fs/open_darwin.go`); no link widget or URL opener | P0 | S | `NSWorkspace openURL:`. Link role in the semantics (missing from ours: `ui/semantics.go`) |
| Selectable static text, across widgets | Select and copy parts of messages, even across several bubbles | `LabelSelectionState`: `egui/crates/egui/src/text_selection/label_text_selection.rs:77-103`; `OutputCommand::TextSelectionSettled` (PRIMARY on X11): `output.rs:128-134`. ZapFast: 24 uses | **Missing** | **P0** | M | Selection highlight in `selectedTextBackgroundColor` (inactive-window grey when not key); ⌘C through the Edit menu |
| Multi-line text editor | Chat composer, notes, settings text areas | `TextEdit::multiline`, `desired_rows`, `return_key`, `char_limit`, `layouter`, `hint_text`, `password`: `egui/crates/egui/src/widgets/text_edit/builder.rs:109-497`; `TextBuffer` trait: `text_buffer.rs:26` | **Missing.** Single-line `TextField` (ours: `ui/textfield.go`, `widget/field.go`); multi-line is excluded from phase 5 (PHASE5-PLAN "Not in this phase") | **P0** | L | Growing height, Enter sends and ⇧Enter adds a line (ZapFast), ↑/↓ by visual line keeping the goal x, scroll the caret into view. Requires the rich-text layout above |
| Autocomplete popup | `@mention` and `:emoji:` completion | `widgets/text_edit/completion.rs` (`Suggestion`, `CompletionPopup`) | **Missing** | P1 | M | Anchor at the caret rect, which we already compute for the IME (`Window.SetIMERect`) |
| Undo and redo in fields | | `TextEditUndoer`: `text_edit/state.rs:12-79` | **Have.** Bounded (ours: `ui/textundo.go`) | — | — | — |
| IME (preedit, commit, cursor area, purpose) | CJK input, dead keys, press-and-hold | Preedit and commit events: `egui-winit/src/lib.rs:764-826`; cursor-area and purpose debouncing: `lib.rs:1266-1304`; IME ownership per widget: `builder.rs:617,1382-1415` | **Have (macOS).** `NSTextInputClient`, marked text, `firstRectForCharacterRange:`, replacement ranges (ours: `platform/platform_darwin.go:666-772`; D25) | — | — | Still to do: `attributedSubstringForProposedRange:` and `characterIndexForPoint:` returning real data, so reconversion, Dictation and the Writing Tools / Services work. `IMEPurpose::Password` → turn off IME and use `EnableSecureEventInput` (for ZapFast's lock code) |
| Standard key bindings (`doCommandBySelector:`) | Emacs keys (⌃A/E/K/T), user `DefaultKeyBinding.dict`, `insertBacktab:`, `cancelOperation:` | winit hands the selector to the app: `winit/winit-core/src/application/macos.rs:8-50`; view methods: `winit/winit-appkit/src/view.rs` (`doCommandBySelector:`, `insertTab:`, `insertBackTab:`, `cancelOperation:`) | **Partial.** The selector is only used to tell whether the IME consumed a key (ours: `platform/platform_darwin.go:620-640`); the field documents "macOS Emacs bindings not implemented" (ours: `ui/textfield.go:111-112`) | P1 | S | Map the selector set (`moveWordLeft:`, `deleteToEndOfParagraph:`, `transpose:`, …) to edit actions. This is the native route, and it respects user key-binding files |
| Text system fonts and fallback | Native fonts and emoji | egui bundles Ubuntu-Light, Hack and NotoEmoji (`epaint_default_fonts/fonts/`); system fallback is an add-on through fontique (`egui_system_font_provider/src/lib.rs`) | **Have.** CoreText with the system cascade (ours: `text/coretext_darwin.go`; D9) | — | — | A real advantage: keep SF Pro, the system emoji and the cascade |
| Spell check, substitutions, emoji palette | Red squiggles, autocorrect, ⌃⌘Space | not in egui | **Missing** | P2 | M | `NSSpellChecker` (async check on a worker); "Emoji & Symbols" in the Edit menu → `orderFrontCharacterPalette:` (needs a complete `NSTextInputClient`) |

### 2.4 Lists, tables, scrolling, plots

| Feature | What it gives an app | How they do it (paths) | Ours | P | Effort | macOS-native notes |
|---|---|---|---|---|---|---|
| Virtual list with fixed row height | 1M rows, O(1) visible range | `ScrollArea::show_rows`: `egui/crates/egui/src/containers/scroll_area.rs` | **Have.** `widget.List` / `Table` (ours: `widget/list.go`, `widget/table.go`; 0.5–1.4 ms per frame at 1M rows) | — | — | — |
| Virtual list with variable height | Chat transcripts: estimated heights, measured heights cached, viewport-only layout | `TableBody::heterogeneous_rows`: `egui_extras/src/table.rs:963-972`; `show_viewport`: `scroll_area.rs`; ZapFast's own cache, `estimated_height`, then measured: `zapfast/src/ui/conversation.rs:2293,2790-2840` | **Missing** | **P0** | L | Prefix sums over a pointer-free height array (Fenwick tree for O(log n) updates). Measure the rows in view plus the overscan only. Correct the scroll offset when rows above the anchor change height |
| Stick to bottom, anchor preservation, scroll-to-item | New messages keep the view at the bottom; history loading above doesn't jump; jump to a reply or search hit | `ScrollArea::stick_to_bottom`, `stick_to_right`, `scroll_offset`, `animated`: `scroll_area.rs`; `TableBuilder::stick_to_bottom`, `scroll_to_row`: `table.rs` | **Partial.** `ScrollIntoView` exists (ours: `ui/scroll.go`, `widget/scroll.go`); no stick-to-bottom or anchor | **P0** | M | — |
| Scroll physics (kinetic, rubber band, momentum phase) | Trackpad scrolling that feels like `NSScrollView` | `egui/crates/egui/src/containers/scroll_physics.rs:1-30` (trackpad rubber-band slope tuned "to match the tight feel of native macOS"); momentum detection with an `NSEvent` local monitor: `egui-winit/src/macos_scroll_momentum.rs:1-40`; winit's `ScrollSource` / phases: `winit-core/src/event.rs` | **Partial.** Precise deltas plus exponential smoothing; **no `phase`/`momentumPhase`** read on macOS (ours: no matches in `platform/platform_darwin.go`), no overscroll | P1 | M | Read `NSEvent phase` / `momentumPhase` / `hasPreciseScrollingDeltas` directly (we own the view, so no monitor is needed). Rubber band only for precise devices. Follow "Show scroll bars" (`NSScroller preferredScrollerStyle` + its change notification) |
| Pinch, rotate and smart zoom | Zoom in the image preview, double-tap smart zoom | `Event::Zoom`/`Rotate`: `egui/src/data/input/event.rs`; view `magnifyWithEvent:`, `rotateWithEvent:`, `smartMagnifyWithEvent:`, `pressureChangeWithEvent:`: `winit-appkit/src/view.rs` | **Missing** (ours: no matches) | P0 (ZapFast's image preview uses zoom/pinch, `zapfast/src/ui/image_preview.rs`) | S | Three view methods → events |
| Tables: resizable columns, sorting, sticky header, striping | Data views | `egui_extras/src/table.rs` (`Column::auto/remainder/exact/initial/resizable/clip`, `striped`, `sense`) | **Have.** `widget.Table` with sort, resize, fade (ours: `widget/table.go`) | — | — | Alternating row colours from `NSColor.alternatingContentBackgroundColors` |
| Plots | Charts | `egui_plot` (moved to its own repo; not cloned) | **Missing** | P3 | M | — |
| Date picker | Date input | `egui_extras/src/datepicker/` | **Missing** | P3 | M | — |
| Syntax highlighting / code editor | Code views | `egui_extras/src/syntax_highlighting.rs` (syntect) | **Missing** | P3 | M | — |

### 2.5 Images and textures

| Feature | What it gives an app | How they do it (paths) | Ours | P | Effort | macOS-native notes |
|---|---|---|---|---|---|---|
| Layered loaders (bytes → image → texture), keyed by URI | `ui.image("file://…")`, http, the bytes of a message attachment; caching per layer; pluggable | `egui/crates/egui/src/load.rs:1-80` (`BytesLoader`/`ImageLoader`/`TextureLoader`, `LoadError::NotSupported` chaining); `install_image_loaders`: `egui_extras/src/loaders.rs:67-110` | **Missing.** We can draw a texture (ours: `ui/stack.go:138` `NextImage`, `gpu.ModeRGBA`) but have no loader, cache or widget | **P0** | L | One `image` package: a request by key returns a handle that is pending, ready or failed. Workers decode and the UI is woken (as `fs` and `task` do) |
| Background decode | No UI stalls on big photos | `egui_extras/src/loaders/background_decode.rs` (a thread per decode, a pending entry, a repaint when done) | **Missing** | P0 | M | Use our `task` pool (5B), not a thread per image |
| Formats: JPEG, PNG, WebP, GIF (animated), SVG | Everything WhatsApp sends; stickers (animated WebP) | `image` crate loaders: `loaders/image_loader.rs`, `gif_loader.rs`, `webp_loader.rs`, `svg_loader.rs` (resvg); ZapFast enables `image`/`svg`/`webp`/`gif` (`zapfast/Cargo.toml`) | **Missing** | P0 | M | **ImageIO is a system framework** (no C library): `CGImageSourceCreateWithData` decodes JPEG, PNG, GIF, HEIC, WebP and AVIF. Animated frames come from `CGImageSourceGetCount` plus the per-frame delay properties. **Decode at display size** with `kCGImageSourceCreateThumbnailFromImageAlways` + `kCGImageSourceThumbnailMaxPixelSize`. egui's GIF loader decodes *every* frame to RGBA up front (`gif_loader.rs:20-39`); decode lazily per frame instead. For SVG on macOS there is no documented general rasteriser: check whether `NSImage` loads SVG on macOS 14+ (unverified), else write a small pure-Go SVG-subset rasteriser for icons and stickers. Windows: WIC. Linux: Go stdlib `image/png`, `jpeg` and `gif` |
| Texture lifetime and eviction | Thousands of avatars and thumbnails without unbounded VRAM | `TextureHandle` frees on drop; `reduce_texture_memory`, `forget_image`: `memory/mod.rs:323`, `load.rs` | **Missing.** `NewTexture` appends forever, with no free (ours: `gpu/renderer_darwin.go:257-276`) | **P0** | M | RGBA atlas pages for small images (as ARCHITECTURE §6 plans: "Images … go into RGBA atlas pages keyed by (path-hash, size)") plus dedicated textures for large ones; LRU with a byte budget; free on eviction |
| Sampling options and mipmaps | Downscaled photos without shimmer | `TextureOptions` (`magnification`, `minification`, `wrap_mode`, `mipmap_mode`): `egui/crates/epaint/src/textures.rs:160-199` | **Unknown** (sampler settings not checked) | P1 | S | Prefer decoding at the target size (ImageIO thumbnail) over mipmaps |
| Image widget (fit, rounding, alt text, spinner while loading) | Avatars, previews | `egui/crates/egui/src/widgets/image.rs`; `widgets/spinner.rs` | **Missing** (ours: `widget/text.go` has `Icon` only) | P0 | S | `RoleImage` exists in our semantics; add an alt name |
| Streaming textures (video, camera) | Video playback and calls in ZapFast | per-frame `TextureHandle::set` / partial `ImageDelta` (epaint) | **Partial.** Sub-rect `Upload` (ours: `gpu/renderer_darwin.go:292`) | P0 for ZapFast calls and video (outside egui's scope) | L | Zero-copy: `CVPixelBuffer` → `CVMetalTextureCache` (CoreVideo, a system framework); a YUV→RGB sampling mode in the quad shader |

### 2.6 Window, app and platform (winit, eframe)

| Feature | What it gives an app | How they do it (paths) | Ours | P | Effort | macOS-native notes |
|---|---|---|---|---|---|---|
| Runtime window commands | Title, size, min/max, position, minimise, maximise, fullscreen, focus, visibility, level, close and cancel close | `ViewportCommand` (Close, CancelClose, Title, InnerSize, OuterPosition, Minimized, Maximized, Fullscreen, Focus, RequestUserAttention, ContentProtected, Screenshot, …): `egui/crates/egui/src/viewport.rs`; winit `Window` trait: `winit/winit-core/src/window.rs` | **Partial.** Open with a title, size and min size; custom titlebar and traffic lights; close request (ours: `platform/platform.go` `Options`, `platform_darwin.go:298-1327`). `Minimize` exists only on Linux (`platform/decor_wl_linux.go:96`; a no-op elsewhere: `linuxext_other.go:43`). There is no setter for the title, position or fullscreen | **P0** | M | ZapFast uses Close, Focus, RequestUserAttention, Minimized, Fullscreen, InnerSize and Screenshot (`zapfast/src` grep). Native: `toggleFullScreen:` (a separate Space), `requestUserAttention:` (Dock bounce), `setTitle:`, `makeKeyAndOrderFront:`, `miniaturize:` |
| Several windows (viewports) | Image viewer, settings, call window | `egui/crates/egui/src/viewport.rs`; `egui_demo_lib/src/demo/extra_viewport.rs` | **Missing.** Designed (D30) but not built | P0 (settings and call windows) | L | One run loop with a display link per window (D30) |
| Stay running without a window; reopen from the Dock | A chat app keeps its connection when the window is closed | not in eframe (ZapFast uses `fastframe-shell`, `zapfast/Cargo.toml`) | **Missing** | **P0** | S | `applicationShouldHandleReopen:hasVisibleWindows:` on an app delegate; `NSApp hide:`; the "quit on last close" option is already in the D30 plan |
| NSApplication delegate hooks | Open URLs and files (URL schemes, a dropped file on the Dock icon), terminate decisions | winit leaves the delegate to the app and only observes notifications (`winit/winit-appkit/src/app_state.rs:134-195`); `with_activation_policy`, `with_default_menu`: `winit-appkit/src/lib.rs` | **Partial.** Activation policy only (ours: `platform/platform_darwin.go:97`) | P1 | S | `application:openURLs:`, `applicationShouldTerminate:` (save state, refuse while uploading), `applicationDockMenu:` |
| Window events: move, screen change, occlusion, fullscreen transitions, key and main | Geometry saving; pause video when occluded | winit delegate methods `windowDidMove:`, `windowDidChangeScreen:`, `windowDidChangeOcclusionState:`, `window{Will,Did}{Enter,Exit}FullScreen:`: `winit/winit-appkit/src/window_delegate.rs` | **Partial.** Occlusion (`EvVisible`), focus, resize, scale (ours: `platform/platform.go:80-100`); no move, screen or fullscreen events | P1 | S | — |
| Monitors | Placement, recovery off-screen, DPI per screen | `available_monitors`, `current_monitor`: `winit-core/src/window.rs`; `winit-appkit/src/monitor.rs` | **Missing** | P1 | S | `NSScreen screens`, `visibleFrame` (excludes the menu bar and Dock) |
| Cursors | Grab, grabbing, diagonal resize, not-allowed, custom images | 35+ `CursorIcon`s plus custom cursors: `winit-appkit/src/cursor.rs`. ZapFast uses PointingHand, Grab/Grabbing, ResizeNwSe/NeSw | **Partial.** 5 shapes (ours: `platform/platform.go` `Cursor`) | P1 | S | `NSCursor` has open and closed hand, and the system resize cursors (`frameResizeCursorFromPosition:inDirections:` on macOS 15+) |
| Keys and modifiers | All shortcuts, numpad, modifier-only events | winit `KeyboardInput`, `ModifiersChanged`, `flagsChanged:`: `winit-appkit/src/view.rs`; `option_as_alt`: `winit-appkit/src/lib.rs` | **Partial.** Letters, digits, F1–F12 and `[ ] .` only (ours: `platform/platform.go` `Key`, `app/input.go:402-410`, `app/keycode_test.go`). `flagsChanged:` is declared but there is no event (`platform_darwin.go:160`). ⌘Q is a hard-wired hack (`platform_darwin.go:614-616`) | **P0** | S | Need `, = - / ; ' \` and ` (⌘, opens Settings; ⌘= and ⌘− zoom), the numpad, F13–F20, and a modifiers-changed event (⌥ alternate menu items, hold-⌘ hints). Match app shortcuts by character, as `NSMenu` key equivalents do, so that Dvorak and AZERTY users get ⌘Z where they expect it |
| App zoom (⌘+ / ⌘− / ⌘0) | Larger UI for readability | `egui/crates/egui/src/gui_zoom.rs` (`zoom_factor`, `zoom_with_keyboard`: `memory/mod.rs:241-254`); ZapFast uses `zoom_factor` (`zapfast/src/theme.rs:1460`) | **Missing.** Scale is the backing scale only (ours: `theme.Resolve(scale)`) | **P0** | M | Multiply points→pixels per window. The glyph atlas re-rasterises at the new size; store geometry in unzoomed points |
| Theme follow | System light and dark | eframe `follow_system_theme`; `ThemeChanged`: winit | **Have** (ours: D32, `platform/appearance_darwin.go`) | — | — | — |
| Transparency and vibrancy | Sidebar showing the desktop (ZapFast's "Messages" theme is transparent: `zapfast/src/main.rs:620-622`) | `with_transparent`; winit `set_blur_material` with `NSVisualEffectView`: `winit-appkit/src/window_delegate.rs:34,87-89,1204-1218` (also a private `CGSSetWindowBackgroundBlurRadius` call) | **Missing** | P1 | M | Put an `NSVisualEffectView` (material `.sidebar`, blending `behindWindow`) under a non-opaque `CAMetalLayer`; avoid the private CGS call. Turn it off under "Reduce transparency" (`NSWorkspace accessibilityDisplayShouldReduceTransparency`) |
| Tabbing, document edited, represented file | Native window tabs; the dirty dot; proxy icon | `set_tabbing_identifier`, `select_next_tab`, `set_document_edited`: `winit-appkit/src/lib.rs` | **Missing** | P2 | S | `NSWindow tabbingMode`, `documentEdited`, `representedURL` |
| Content protection | Hide chats from screen capture | `ContentProtected`: `viewport.rs`; `set_content_protected`: winit | **Missing** | P2 | S | `NSWindow sharingType = NSWindowSharingNone` |
| Click-through, key equivalents | Clicks on inactive windows; ⌃Tab reaching the view | `acceptsFirstMouse:`, `_wantsKeyDownForEvent:`: `winit-appkit/src/view.rs` | **Missing** | P2 | S | Native apps accept the first mouse only on some controls (toolbar buttons) |
| App icon at runtime, Dock badge, attention | Unread badge; icon variants | eframe sets the Dock icon via `setApplicationIconImage` (from raw pixels, not PNG, to avoid a `libpng` dylib bug): `eframe/src/native/app_icon.rs:200-272`; badge through `fastframe-dock` in ZapFast | **Missing** | **P0** (badge, attention) | S | `NSApp.dockTile.badgeLabel`; `requestUserAttention:` |
| Notifications | New-message alerts with actions | not in egui (ZapFast: `fastframe-notifications`) | **Missing** | **P0** | M | `UNUserNotificationCenter` needs an app bundle and **Objective-C blocks** for its completion handlers (see the blocks row); clicks arrive through its delegate |
| Menu-bar extra (tray) | Run in the background | not in egui (ZapFast: `fastframe-tray`) | **Missing** | P1 | M | `NSStatusItem` with an NSMenu, which reuses D29's menu model |
| Power events | Reconnect after wake | ZapFast `fastframe-power` | **Missing** | P1 | S | `NSWorkspaceWillSleepNotification` / `DidWakeNotification` on the workspace notification centre |
| Locale and i18n | Translated UI, date and time formats | ZapFast `fastframe-i18n` (gettext) | **Missing** | P1 | M | `NSLocale preferredLanguages`; format dates with `NSDateFormatter` templates (follows the 12/24 h setting) |
| System input settings | Native timings and motion | egui uses a fixed `max_double_click_delay = 0.3` s: `egui/crates/egui/src/input_state/mod.rs:78,116` | **Partial.** A fixed `multiClickNs` (ours: `app/input.go:318-335`); `ReduceMotion` is a manual Kit flag (ours: `widget/kit.go:118-122`) | P1 | S | `NSEvent.doubleClickInterval` or the event's own `clickCount`; `accessibilityDisplayShouldReduceMotion`, `…IncreaseContrast`, `…ReduceTransparency`, and its change notification |

### 2.7 Clipboard, dialogs, drag and drop (arboard, rfd, winit)

| Feature | What it gives an app | How they do it (paths) | Ours | P | Effort | macOS-native notes |
|---|---|---|---|---|---|---|
| Clipboard text | | arboard `osx.rs`; egui-winit `clipboard.rs` | **Have.** Asynchronous, zero-allocation (ours: `platform/clipboard_darwin.go`, `paste.go`) | — | — | — |
| Clipboard images | Paste a screenshot into the composer; copy a photo | arboard reads TIFF → RGBA and writes images: `arboard/src/platform/osx.rs:224-251,343`; egui `Event::PasteImage`, `OutputCommand::CopyImage`: `egui/src/data/input/event.rs`, `output.rs:137`; egui-winit `get_image`: `egui-winit/src/clipboard.rs:145-158` | **Missing** | **P0** | M | Read `NSPasteboardTypePNG` first, then TIFF (decode with ImageIO on a worker). Write PNG and TIFF. Keep the bytes off the Go heap path or in a reusable buffer, like `EvPaste` |
| Clipboard file lists, HTML | Paste files to attach; rich paste | arboard `file_list`, `html`: `osx.rs:219,251,308,363` | **Missing** | P1 | S | `NSPasteboardTypeFileURL` / `readObjectsForClasses:@[NSURL]` |
| Leave no clipboard history for secrets | Password managers skip it | `org.nspasteboard.ConcealedType`: `arboard/src/platform/osx.rs:410-434` | **Missing** | P2 | S | Write it alongside the text for secret copies (lock codes) |
| File dialogs (open, save, folders, filters) | Attach files, save media | rfd: `NSOpenPanel`/`NSSavePanel` as **sheets** with `beginSheetModalForWindow:completionHandler:`, `UTType` filters: `rfd/src/backend/macos/file_dialog/panel_ffi.rs:14,48,110-147,212`; restores the key window afterwards: `rfd/src/backend/macos/utils/focus_manager.rs` | **Missing.** Planned (PHASE5 5B step 9) | **P0** | M | Sheets need **completion blocks**. Our own event loop dispatches with `sendEvent:` so a sheet works without `runModal`; set `allowedContentTypes` from `UTType typeWithFilenameExtension:` |
| Message boxes | Confirm "Delete chat?", errors | rfd `NSAlert` sheet or `runModal`: `rfd/src/backend/macos/message_dialog.rs:15-151` | **Partial.** In-window `widget.Dialog` (ours: `widget/dialog.go`) | P1 | S | `NSAlert` as a sheet for destructive confirmations (native look, the ⌘D "Don't Save" conventions) |
| Drop into a window (files, text, images) with hover position | Drag a photo into a chat | winit `DragEntered`/`DragPosition`/`DragDropped`/`DragLeft`, `DataTransferReceived`: `winit-core/src/event.rs`, `winit-core/src/data_transfer.rs`; `NSDraggingDestination`: `winit-appkit/src/dnd.rs`, `window_delegate.rs`; egui `hovered_files`/`dropped_files`: `egui-winit/src/dropped_file.rs` (ZapFast uses both) | **Missing.** Planned (5B step 11) | **P0** | M | Register `NSPasteboardTypeFileURL`, string and PNG/TIFF; show a drop highlight from the hover position |
| Drag out of a window | Drag an image to Finder | winit `start_drag`, `OutgoingDragDropped`: `data_transfer.rs`; `draggingSession:…`: `winit-appkit/src/window_delegate.rs` | **Missing.** Excluded in phase 5 | P2 | M | `beginDraggingSessionWithItems:event:source:` with `NSFilePromiseProvider` |
| In-app drag and drop with typed payloads | Reorder pinned chats; drag onto targets | `egui/crates/egui/src/drag_and_drop.rs` (`set_payload`, `take_payload`); demo: `egui_demo_lib/src/demo/drag_and_drop.rs` | **Missing** | P2 | M | — |

### 2.8 Accessibility (AccessKit)

| Feature | What it gives an app | How they do it (paths) | Ours | P | Effort | macOS-native notes |
|---|---|---|---|---|---|---|
| macOS bridge (NSAccessibility) | VoiceOver, Voice Control, Switch Control, Accessibility Inspector, UI automation | Platform nodes implement the `NSAccessibilityElement` methods (role, subrole, frame, value, children, actions, text ranges, rows): `accesskit/adapters/macos/src/node.rs`; the view is subclassed at runtime for the root: `adapters/macos/src/subclass.rs`; the window's focused element is patched: `adapters/macos/src/patch.rs` | **Partial.** Semantics are recorded and exported (ours: `ui/semantics.go`; `Semantics(dst *ui.SemTree)`); **no bridge** (phase 6) | **P0** (ZapFast ships with AccessKit, `accesskit` ×78 in `zapfast/src`) | L | We own the view class, so no runtime subclassing is needed: implement `accessibilityChildren` / `accessibilityHitTest:` / `accessibilityFocusedUIElement` on our view, plus one element class keyed by box key |
| Lazy activation | Zero cost until an assistive technology asks | The adapter stays inactive until `accessibilityChildren` etc. is called, then asks the app for the first tree: `adapters/macos/src/adapter.rs:174-220`; egui builds updates only after `enable_accesskit`: `egui/crates/egui/src/context.rs:4233-4239` | — | P0 | — | Fits the idle and allocation rules: export semantics only while active |
| Events by tree diff | Focus, value, title, selection and row changes announced correctly | `accesskit_consumer` tree diff → `NSAccessibilityPostNotification` (focus, value, title, selected text, selected rows, element destroyed): `adapters/macos/src/event.rs:25-335` | **Missing** | P0 | M | Diff our previous and current `SemTree` by key |
| Live regions (announcements) | "New message from …" spoken | `Live::Assertive` → `NSAccessibilityAnnouncementRequestedNotification`: `event.rs:35,97-99`; ZapFast uses `accesskit::Live::Assertive` | **Missing.** No live, link or heading roles in our semantics (ours: `ui/semantics.go:15-79`) | P0 | S | Add `Live` and `Link` (and heading or description) to `SemState`/`Role` |
| Text navigation for VoiceOver | Reading by line and word, caret announcements | `accessibilityNumberOfCharacters`, `…RangeForLine:`, `…StringForRange:`, `…FrameForRange:`, `…InsertionPointLineNumber`: `adapters/macos/src/node.rs`; egui text runs: `egui/crates/egui/src/text_selection/accesskit_text.rs` | **Partial.** Field selection is recorded (`sel` in `semRec`) | P1 | M | Needs per-line geometry from the text layout (multi-line and rich text) |
| Missing-name lint | Catches unlabeled icon buttons | `egui_kittest/src/accessibility.rs` (runs after settle and before every snapshot) | **Missing** | P1 | S | Run it inside `widgettest.Rig` |

---

## 3. Native macOS feel and speed techniques worth adopting

These are techniques we can take from egui and its ecosystem, or that egui shows we should avoid.

**What makes egui apps feel non-native on macOS (avoid all of these):**

1. **Bundled fonts and its own rasteriser.**
   - egui ships Ubuntu-Light and Hack (`epaint_default_fonts/fonts/`) and reaches system fallback only through an add-on crate (`egui_system_font_provider`), so its text does not look like SF Pro.
   - Its meshes are anti-aliased by feathering (epaint tessellator), which softens edges.
   - *We already avoid both:* CoreText and SDF quads. Keep it that way.
2. **Drawn chrome.**
   - Context menus, menus and alerts are drawn widgets.
   - eframe on macOS gets only winit's minimal default menu (`with_default_menu`), so ⌘C reaches text fields as raw keys, not through the Edit menu.
   - *Adopt:* NSMenu everywhere on macOS (D29, still to build), and `NSAlert` sheets for confirmations.
3. **Key handling outside AppKit's text system.**
   - No `doCommandBySelector:` mapping, so the Emacs keys and user key-binding files are ignored.
   - Shortcuts go by key, not by character.
   - *Adopt:* winit's `standard_key_binding` idea (`winit-core/src/application/macos.rs`), and character-based shortcut matching.
4. **Fixed timings.**
   - egui: a 0.3 s double click (`input_state/mod.rs:116`); ours is fixed too (`app/input.go`).
   - *Adopt:* `NSEvent.doubleClickInterval` or `clickCount`, the key-repeat settings, and reduce motion.
5. **Scrolling.**
   - Until `scroll_physics.rs` and `macos_scroll_momentum.rs`, momentum flings looked like new finger scrolls and there was no rubber band.
   - *Adopt:* read `phase` / `momentumPhase` directly in our `scrollWheel:`, rubber-band precise devices only, and follow the scroller style setting. egui tunes the trackpad slope to 0.3 against iOS's 0.55, "to match the tight feel of native macOS" (`scroll_physics.rs:25-30`).
6. **Startup flash.**
   - eframe starts the window hidden to avoid a white flash (`eframe/src/native/glow_integration.rs:272`) and still risks a black first frame (`:759`).
   - *We are ahead:* the first frame is presented inside the `orderFront` transaction (ARCHITECTURE §3).
7. **Redraw on every event.**
   - egui-winit marks almost every window event `repaint: true` (`egui-winit/src/lib.rs:390-603`), and ZapFast carries a busy-loop fix patch (`zapfast/Cargo.toml` `[patch.crates-io]`).
   - *Adopt:* a cheap "did this frame change anything?" check (a hash of the draw list or of the box tree) to skip the present when the pointer moves over inert content. Whether we already skip presents in that case was not verified in our `app/frame.go`.
8. **Persistence that loses data or is wrong across zoom.**
   - Writes are not atomic (`file_storage.rs:197-222`).
   - Window size is saved in zoomed points (`zapfast/src/main.rs:627-633`).
   - *Adopt:* atomic writes on a worker, geometry in unzoomed points, or simply `setFrameAutosaveName:`.
9. **Decoding everything up front.**
   - The GIF loader decodes every frame to RGBA (`gif_loader.rs:20-39`), and images decode at full size.
   - *Adopt:* ImageIO thumbnails at display size, and lazy decoding of animation frames.
10. **Accessibility cost.**
    - AccessKit is lazy (`adapter.rs:174-220`).
    - *Adopt the same:* build no NSAccessibility objects until they are asked for.

**Other macOS techniques from winit, rfd and arboard worth taking:**
- **Sheets, not app-modal dialogs.** Open and save panels and alerts become sheets, and focus goes back to the previous key window afterwards (`rfd/.../focus_manager.rs`).
- **Traffic lights and a full-size content view.** Re-centre the traffic lights after every titlebar layout (`eframe/src/native/macos.rs`, "AppKit resets the position … needs to be called every frame"). We already do traffic lights (`Options.TrafficLights`); verify that they survive fullscreen exit and resizes.
- **Dock icon from raw pixels:** build the `NSImage` from bitmap reps, not PNG data (`app_icon.rs:211-213`).
- **Blur:** an `NSVisualEffectView` instead of private CGS blur (`window_delegate.rs:1211`), gated by Reduce transparency.
- **The concealed pasteboard type** for secrets (arboard).
- **Objective-C blocks.** `internal/objc` (ours: one file, `internal/objc/objc.go`) has no block support. These all need completion-handler or event-handler blocks:
  - NSOpenPanel and NSAlert sheets;
  - `UNUserNotificationCenter`;
  - `NSEvent addLocalMonitor…`;
  - `AVCaptureDevice requestAccessForMediaType:` (calls);
  - `NSWorkspace openURL:configuration:completionHandler:`.

  A global-block layout whose invoke pointer is one of our zero-allocation callback trampolines would cover all of them. **P0, S–M.**

---

## 4. Developer tooling

| Tool | What it does (paths) | Ours | P | Effort | How we would build it |
|---|---|---|---|---|---|
| Debug-on-hover overlay | Hover any widget to see its rect, id, layout direction, expand width/height, resize, interactive widgets, widget hits, unaligned rects and the focused widget, plus a callstack: `DebugOptions` in `egui/crates/egui/src/style.rs` (`debug_on_hover`, `show_expand_width`, `show_resize`, `show_interactive_widgets`, `show_widget_hits`, `show_unaligned`, `show_focused_widget`, `warn_if_rect_changes_id`); `egui/src/callstack.rs` | **Missing** | P1 | M | An overlay layer drawn by `ui` behind a debug flag or hotkey: the hovered box's key, flags, size rules, final rect, clip, role and name; outlines for hit-testable boxes; drag and cursor regions (D24). Allocation-free, from the existing box records |
| Id-clash warning | Red overlay and text when two widgets share an id: `context.rs:1432-1549`; `Options.warn_on_id_clash`: `memory/mod.rs:307` | **Partial.** Duplicate keys are silently derived into new ones (ours: `ui/context.go:528`). The guide hit such a collision ("two byte-labels in one container collided", PHASE5 Status) | P1 | S | In debug builds, record duplicates and show them in the overlay or log them once per key |
| Repaint causes | Every `request_repaint` records file, line and reason; the UI shows why the app is repainting: `context.rs:283-320,2254` | **Partial.** `owedWhy` (A/H/S/I/B) and `StateLine` (ours: `app/diag.go`), but not which code asked | P1 | S | In debug builds, tag `WakeAfter`, animations and task wakes with a caller id (`runtime.Caller` behind the flag only, since it allocates) |
| Inspection, settings, memory and texture panels | `Context::settings_ui`, `inspection_ui`, `texture_ui`: `context.rs:3807-3969`; demo backend panel with a frame-time graph and reactive vs continuous mode: `egui_demo_app/src/backend_panel.rs`, `frame_history.rs` | **Partial.** Bench JSON, `StateLine`, startup report (ours: `app/bench.go`, `app/stats.go`) | P1 | M | An in-app HUD: frame CPU per phase (build, layout, encode), GPU time (`MTLCommandBuffer GPUStartTime`/`GPUEndTime`), allocations per frame, atlas pages and texture bytes, box count, glyph and run cache hit rates |
| Accessibility inspector plugin | ⌘⌥I shows the live AccessKit tree and node details: `egui_demo_app/src/accessibility_inspector.rs` | **Partial.** `widgettest.Dump`/`Tree` print the semantic tree in tests (ours: `widget/widgettest/dump.go`) | P2 | S | The same tree in the debug overlay; Xcode's Accessibility Inspector works once the bridge exists |
| External inspection server | Opt-in TCP (`EGUI_INSPECTION=1`, loopback by default): read the AccessKit tree, inject input, take screenshots, resize. It is the base for `egui_mcp`, which lets AI agents drive an app: `egui/crates/egui_inspection/README.md`, `src/protocol.rs`, `src/plugin.rs` | **Missing** | P1 | M | Expose `SemTree` plus `widgettest`-style steps plus `RenderToRGBA` over a local Unix socket, behind an env var. Lets agents and CI drive a real ZapFast build. Never bind beyond loopback by default (egui warns about no auth) |
| Profiler | `profiling` crate scopes throughout egui and eframe, with a puffin backend and a `puffin_http` viewer (`egui/Cargo.toml:126-127`; `egui_demo_app/src/main.rs:15-20,89`) | **Partial.** CPU per frame in the benches; `GNUI_TRACE_LIVE` (ours: `platform/platform_darwin.go:1092-1096`) | P1 | S | Go stdlib `runtime/trace` regions around build, layout, encode and present, and `trace.FlightRecorder` to dump the last seconds after a hitch over budget (check that both cost nothing while tracing is off). Instruments signposts (`os_signpost`) are header macros, so calling them without cgo is unverified |
| Snapshot tests | Render to an image and compare: YIQ distance `threshold` plus `max_failed_pixels`, per-OS thresholds, `UPDATE_SNAPSHOTS=true/force`, `.new.png`/`.diff.png` outputs, `kittest.toml`: `egui_kittest/README.md`, `src/snapshot.rs:19-169`, `egui/kittest.toml` | **Partial.** Offscreen renders and `rexcompare` (ours: `app/offscreen.go`, `tools/rexcompare`, `cmd/gallery/gallery/shot.go`); no general snapshot assertion in `widgettest` | P1 | S | `Rig.Snapshot(name)` with tolerance, update env var and diff images, on top of `RenderToRGBA` |
| Semantic queries in tests | `get_by_label`, `click`, typed AccessKit queries (kittest) | **Have.** `widgettest` finds by role and name and waits on conditions (ours: `widget/widgettest/doc.go`) | — | — | — |
| Test recording | `KITTEST_RECORD=1` streams frames to ffmpeg as MP4: `egui_kittest/src/recording.rs` | **Missing** | P3 | S | Offscreen frames to PNGs; `tools/bouncerec` already records the screen |
| Plugins (frame hooks) | `egui::Plugin` for inspection, recording, inspectors: `egui/crates/egui/src/plugin.rs` | **Partial.** `app.Scripter`, `Initer` (ours: `app/app.go:60-81`) | P2 | S | Add before-frame and after-frame hooks, for tools only |

---

## Not checked

- **ImageIO and SVG:** whether `NSImage` rasterises SVG on macOS 14+ was not tested.
- **Untested ideas:**
  - ImageIO thumbnail decoding was not timed;
  - Objective-C blocks through our trampolines were not prototyped;
  - `runtime/trace` and the flight recorder were not checked for allocations.
- **Our renderer:** the sampler and mipmap settings were not checked, nor whether `app` skips the present when a frame is unchanged.
- **egui:** `egui_plot` (moved out) and the LFS snapshot images (no git-lfs) were not read.
- **Platforms:** the Windows and Linux backends of rfd, arboard and winit were skimmed only by name; macOS was the focus.
- **ZapFast:** only its use of egui, eframe and the crates above was surveyed, not the whole app.
