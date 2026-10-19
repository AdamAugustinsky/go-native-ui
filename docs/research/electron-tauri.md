# App infrastructure in Electron and Tauri, and what go-native-ui lacks

Date: 2026-10-18. Scope: the app services and release tooling around the UI (lifecycle, OS integration, secrets, updates, packaging), not widgets or rendering. The target is ZapFast, a WhatsApp client now written in Rust on egui, moving to our framework on macOS first.

**Sources read (shallow clones, read-only, nothing copied):**
- `~/Projects/research/electron`: `docs/api/*`, `docs/tutorial/code-signing.md`, `shell/browser/**` (mac), `shell/common/platform_util_mac.mm`
- `~/Projects/research/tauri`: `crates/tauri`, `crates/tauri-bundler`, `crates/tauri-macos-sign`, `crates/tauri-cli/src/signer`
- `~/Projects/research/plugins-workspace/plugins/*`
- `~/Projects/research/zapfast`: `Cargo.toml`, `packaging/macos/*`, `src/{macos,updates,single_instance,autostart,notify,app}.rs`, which show what the app actually uses
- Ours, read-only: `~/Projects/go-native-ui` (`README.md`, `ARCHITECTURE.md`, `DECISIONS.md`, `docs/PHASE5-PLAN.md`, `docs/GUIDE.md`, `app/`, `platform/`, `fs/`, `menu/`, `internal/objc/`, `tools/mkapp.sh`)

Paths below are relative to those clone roots. `electron/…`, `tauri/…`, `plugins/…` (for `plugins-workspace/plugins`), `zapfast/…` and `gnui/…` (for `go-native-ui`) name the repo.

---

## 1. Summary

**Window and frame: strong.** Our framework has what's needed for one window and its frame: content-first startup, a custom titlebar with placed traffic lights (`gnui/platform/platform.go` `Options.Titlebar`/`TrafficLights`, `SetDragRegions`), the system appearance and accent colour (D32), a text clipboard, and opening a file or folder in its default app (`gnui/fs/open_darwin.go`, `LSOpenCFURLRef`).

**App level: almost nothing yet.** Phase 5B plans native menus, more than one window, file dialogs and drop, but not the rest:
- **No application object.** No `NSApplicationDelegate`, no Apple Event handlers, and the event pump is a method of `platform.Window` (`gnui/platform/platform_darwin.go` `(*Window).Wait`). So today the app cannot:
  - run without a window;
  - react to a Dock click (reopen);
  - receive `open-url`/`open-file`;
  - veto a Quit at logout.
- **No other OS services:** notifications, Dock badge, tray, Keychain, login items, single instance, power events, window-state persistence, screen enumeration, clipboard images, an updater, or a packaging, signing and notarisation CLI. `tools/mkapp.sh` makes only an ad-hoc-signed bundle, with no icon, entitlements or usage strings.

**What ZapFast actually uses** is listed in its `Cargo.toml`. It runs on the `fastframe-*` crates:
- `fastframe-tray`, `-dock` (badge), `-autostart` (`SMAppService` plus a bundled LaunchAgent for start-hidden), `-notifications` (deliver, permission, click, withdraw), `-instance`, `-shell` (keeps running without a window, remembers geometry), `-update` (GitHub releases, Ed25519-signed checksums, a helper, rollback), `-power`, `-file` (atomic saves) and `-log`;
- `keyring-core`, for the SQLCipher archive key;
- `rfd` and `arboard` (file picker, clipboard images), and `open`.

Its macOS bundle (`zapfast/packaging/macos/bundle.sh`, `Info.plist`, `entitlements.plist`, `autostart.plist`) has:
- hardened runtime and Developer ID signing;
- the microphone and camera entitlements and usage strings;
- an `.icns`;
- sounds in Resources, which notifications play by name;
- the LaunchAgent plist in `Contents/Library/LaunchAgents`.

All of that is our P0.

**How Electron and Tauri do it:**
- **Electron:** one ObjC app delegate (`electron/shell/browser/mac/electron_application_delegate.mm`) feeds a `Browser` singleton that emits events. Thin native wrappers per service: `UNUserNotificationCenter`, `NSStatusItem`, `NSDockTile`, `SMAppService`, `NSWorkspace`, `NSFileManager trashItemAtURL`. The heavy pieces come from Chromium or vendored frameworks: crashpad, Squirrel.Mac, `os_crypt` for safeStorage, and global shortcuts.
- **Tauri:** small plugins over shared crates (`tray-icon`, `muda`, `global-hotkey`, `auto-launch`, `notify-rust`, `minisign-verify`, `arboard`, `rfd`). Plus a bundler that shells out to `codesign`, `security`, `ditto`, `xcrun notarytool`/`stapler`, `hdiutil` and a create-dmg script.

**Ours needs neither shape.** Go's standard library already covers what Tauri needs plugins for: http, fs, process, os, log, crypto (Ed25519, SHA-256) and archives. The work that is truly ours is the AppKit/Security/ServiceManagement/UserNotifications bindings and one prerequisite in `internal/objc`.

**Prerequisites that block several P0 items:**
1. **Objective-C blocks.** We need to make global block literals whose `invoke` is one of our ffi callbacks, and to call blocks we receive. `UNUserNotificationCenter`, `AVCaptureDevice requestAccess…`, `NSWorkspace openURL:configuration:completionHandler:` and `nw_path_monitor` all need them. `gnui/internal/objc/objc.go` has no block support.
2. **Callbacks on threads other than main.** UserNotifications and AVFoundation run their completion handlers on background queues. Our objc helpers share one call frame and are not goroutine-safe (comment in `gnui/fs/open_darwin.go`). Callbacks must be able to arrive on a foreign thread, copy their result into the MPSC ring, and call `platform.Wake()`.
3. **An app-level object and event pump.** These are separate from `Window`, with an `NSApplicationDelegate` subclass made with `objc.NewClass`. 5B step 7 plans the app-wide pump; the delegate and lifecycle events are not in the plan yet.
4. **A bundle for development.** `UNUserNotificationCenter` throws when the process has no bundle identifier. TCC (microphone, camera, notifications) and the Keychain key their grants to the bundle id and code signature, and an ad-hoc signature changes on every build, so the prompts come back each time. So `gnui run` should run the app inside a development `.app` signed with a stable self-signed identity, as `tauri/crates/tauri-macos-sign/src/certificate.rs` can generate (`generate_self_signed`).
5. **Keeping startup cheap.** No new framework may load on the critical path to the first frame. UserNotifications, Security, ServiceManagement and AVFoundation are opened with `dlopen` on first use, after the first frame. **The exception:** when the app uses notifications, the notification-centre delegate has to be set before launch finishes, or a click that launched the app is lost (Electron does this in its app delegate). The cost of loading UserNotifications that early is not measured.

---

## 2. Feature table

"Ours": **have**, **partial**, **planned** (in PHASE5-PLAN but not built) or **missing**. Effort: S ≈ ≤2 days, M ≈ a week, L ≈ several weeks, per macOS first.

### App lifecycle and process

| Feature | What it gives an app | How they do it (paths) | Ours | Pri | Eff | macOS API and signing notes |
|---|---|---|---|---|---|---|
| App delegate and lifecycle events: did-finish-launching, become/resign active, will-terminate, should-terminate (logout or update veto), will-power-off | Correct quitting, saving at logout, reacting to activation | `electron/shell/browser/mac/electron_application_delegate.mm` (`applicationWillFinishLaunching`, `applicationDidBecomeActive`, `NSWorkspaceWillPowerOffNotification`); `electron/shell/browser/mac/electron_application.mm` (`terminate:`, `setShutdownHandler`); `tauri/crates/tauri/src/app.rs` (`RunEvent::ExitRequested`, `Exit`, `Ready`) | **missing**: no delegate; `finishLaunching` is called by hand in `gnui/platform/platform_darwin.go` `Open` | P0 | M | `NSApplicationDelegate` class through `objc.NewClass`; `applicationShouldTerminate:` → `NSTerminateLater` + `replyToApplicationShouldTerminate:` so windows can refuse (5B's "Quit asks each window"). No signing needed. |
| Reopen (Dock click), running with no window, start hidden | Messenger behaviour: close hides, the Dock or tray brings it back; launch at login without a window | `electron_application_delegate.mm` `applicationShouldHandleReopen:hasVisibleWindows:` → `activate` event; `tauri/crates/tauri/src/app.rs` `RunEvent::Reopen`; ZapFast `zapfast/src/app.rs` (`fastframe_shell::Resident`, `Closed::Hide/Reopen`, `start_hidden`) | **missing**; 5B plans only "an option keeps it running" (`gnui/docs/PHASE5-PLAN.md` §More than one window). The pump is per window (`(*Window).Wait`) | P0 | M | Needs an app-level `Wait` that blocks with zero windows (same `nextEventMatchingMask` loop, no window). Idle invariants still hold. |
| Activation policy, Dock visibility, hide/show app, activate | Tray-only mode (no Dock icon), "Hide ZapFast", bring forward from tray or notification | `electron/shell/browser/browser_mac.mm` (`DockHide`, `DockShow`, `Focus` → `activateIgnoringOtherApps`); `tauri/crates/tauri/src/app.rs` `set_activation_policy`, `set_dock_visibility` | **partial**: Regular policy is fixed at Open (`selSetActivation`) | P1 | S | `setActivationPolicy:` (Regular/Accessory), `hide:`, `unhide:`, `activate` (macOS 14 cooperative activation: `activate` may be refused; D5 already uses `orderFrontRegardless`). |
| Single instance and second-launch forwarding | Second launch focuses the first and passes args or verbs (`zapfast --show`, a `whatsapp://` link) | Electron `app.requestSingleInstanceLock` (`electron/docs/api/app.md`; `electron/shell/browser/api/electron_api_app.cc` → Chromium ProcessSingleton); `plugins/single-instance/src/platform_impl/macos.rs` (Unix socket in /tmp, sends cwd + args); `zapfast/src/single_instance.rs` (lock + control channel with verbs) | **missing** | P0 | S | LaunchServices already keeps one instance for Finder/`open` launches and sends reopen. A CLI launch or `open -n` needs a `flock` on a lock file in Application Support plus a Unix-domain socket (pure Go `net`). No signing. |
| Open URL (custom scheme / deep link) | `whatsapp://send?phone=…` and `wa.me` links open in the app | `electron/shell/browser/mac/electron_application.mm` `registerURLHandler` (`NSAppleEventManager` … `kAEGetURL`); `electron/shell/browser/browser_mac.mm` `SetAsDefaultProtocolClient` (`LSSetDefaultHandlerForURLScheme`); `plugins/deep-link/src/lib.rs` (macOS schemes come from build-time `CFBundleURLTypes` only); `tauri/crates/tauri-bundler/src/bundle/macos/app.rs` `create_info_plist` writes `CFBundleURLTypes` | **missing** | P1 (ZapFast handles `whatsapp://` on Linux, `zapfast/src/main.rs`; not on macOS yet) | S | Handler registered before `finishLaunching`; the scheme goes in Info.plist `CFBundleURLTypes`. A launch by URL delivers the event after the first frame, which fits content-first startup. |
| Open file / open-with / drop on the Dock icon | Double-clicking documents, "Open With" | `electron_application_delegate.mm` `application:openFile:`; Tauri `RunEvent::Opened { urls }` (`tauri/crates/tauri/src/app.rs`); `CFBundleDocumentTypes` in `tauri/crates/tauri-bundler/src/bundle/macos/app.rs` | **missing** | P2 for ZapFast (P1 in general) | S | `application:openURLs:` on the delegate plus `CFBundleDocumentTypes`/UTIs in Info.plist. |
| Relaunch | Restart after an update or a settings change | `electron/shell/browser/relauncher_mac.cc`, `relauncher.cc`; `plugins/process/src/lib.rs` | **missing** | P1 | S | Spawn a tiny helper (or `/usr/bin/open -n -b <id>` after our pid exits, waiting on it with kqueue `EVFILT_PROC`). Pure Go `os/exec`. |
| Login item / autostart (start hidden) | Starts in the tray at login | `electron/shell/common/platform_util_mac.mm` `GetServiceForType` → `SMAppService mainAppService` / `agentServiceWithPlistName:`; `electron/docs/api/app.md` `setLoginItemSettings`; `plugins/autostart/src/lib.rs` (LaunchAgent file or AppleScript via `auto-launch`, the older approach); ZapFast `zapfast/packaging/macos/autostart.plist` + `bundle.sh` (agent in `Contents/Library/LaunchAgents`, `open -g -b id --args --start-hidden`) | **missing** | P0 | M | `SMAppService` (macOS 13+, so fine for our minimum of 14): `registerAndReturnError:`, `unregister…`, `status` (Enabled/RequiresApproval/NotFound), `+openSystemSettingsLoginItems`. **Signing:** behaves reliably only when packaged and Developer-ID signed, ideally notarized (`electron/docs/tutorial/code-signing.md` §APIs that require code signing). An unsigned app shows as an unidentified developer in Login Items. "Launched at login" detection: `keyAELaunchedAsLogInItem` (`electron_application_delegate.mm`). |
| About panel | Standard About box | `electron/shell/browser/browser_mac.mm` `ShowAboutPanel` | **missing** | P1 | S | `orderFrontStandardAboutPanelWithOptions:`. |

### Menus, Dock and tray

| Feature | What it gives an app | How they do it (paths) | Ours | Pri | Eff | macOS API and signing notes |
|---|---|---|---|---|---|---|
| Application menu bar with standard roles | ⌘Q/⌘H/⌘,/Edit/Window/Help menus that feel native, plus AppKit's Emoji & Symbols and Dictation | `electron/shell/browser/ui/cocoa/electron_menu_controller.mm` (`kRolesMap`: about, hide, hideothers, undo, copy… mapped to AppKit selectors); `electron/shell/browser/api/electron_api_menu_mac.mm`; `tauri/crates/tauri/src/menu/predefined.rs`; ZapFast `zapfast/src/macos.rs` | **planned** (5B step 8); the `menu` model exists (`gnui/menu/menu.go`, `match.go`) | P0 | M | `NSMenu` + `NSMenuItem` with a target/action through our shared IMP; standard items use AppKit selectors (`hide:`, `hideOtherApplications:`, `performMiniaturize:`, `orderFrontCharacterPalette:`); `setWindowsMenu:`, `setServicesMenu:`, `setHelpMenu:`. Validation through `validateMenuItem:` from the command's enabled state. |
| Context menus (native) | Native row menus | `electron/shell/browser/api/electron_api_menu_mac.mm` (`popUpContextMenu`) | **planned** (5B) | P1 | S | `popUpMenuPositioningItem:atLocation:inView:` runs a tracking loop; 5B's frame handler covers it. |
| Dock menu | Right-click Dock icon: "New Chat", status | `electron_application_delegate.mm` `applicationDockMenu:`; `electron/docs/api/dock.md` | **missing** | P2 | S | `applicationDockMenu:` delegate method returns an `NSMenu`. |
| Dock badge | Unread count on the Dock icon | `electron/shell/browser/browser_mac.mm` `SetBadgeCount`/`DockSetBadgeText` (`[[NSApp dockTile] setBadgeLabel:]`); `tauri/crates/tauri/src/window/mod.rs` `set_badge_count`/`set_badge_label`; ZapFast `fastframe-dock` | **missing** | P0 | S | `NSApp.dockTile.badgeLabel`. Unverified: whether the "Badge application icon" switch in Notifications settings also hides it. |
| Dock bounce / attention | Calls and mentions bounce the Dock | `electron/shell/browser/browser_mac.mm` `DockBounce` (`requestUserAttention:`) / `DockCancelBounce`; `tauri/crates/tauri/src/window/mod.rs` `request_user_attention` | **missing** | P0 (incoming call) | S | `requestUserAttention:` with critical or informational → id, `cancelUserAttentionRequest:`. |
| Dock icon at runtime, download/progress on the tile | Custom tile, progress | `browser_mac.mm` `DockSetIcon`, `DockDownloadFinished`; Tauri `set_progress_bar` | **missing** | P3 | S–M | `NSApp.applicationIconImage`; progress needs a custom `contentView` on the dock tile. |
| Tray / status item | Menu-bar icon with unread state and a menu (Show, Lock, Quit) | `electron/shell/browser/ui/tray_icon_cocoa.mm` (`NSStatusBar statusItemWithLength:`, `button setImage:`, `autosaveName`, menu on click); `electron/docs/api/tray.md`, `native-image.md` §Template Image; `tauri/crates/tauri/src/tray/mod.rs` (`tray-icon` crate); `zapfast/src/app.rs` `tray_config`, `tray_action` | **missing** | P0 | M | `NSStatusItem` + `NSStatusBarButton`; template `NSImage` from an SF Symbol (`imageWithSystemSymbolName:accessibilityDescription:`, the source our `icon` package already uses) or PNG bytes (`initWithData:`, `setTemplate:YES`); an `NSMenu` built from our `menu` model; `autosaveName` keeps the position. No signing. |
| Tray-relative window placement (positioner) | Popover-style window under the tray icon | `plugins/positioner/src/ext.rs` (`Position::TrayCenter` …) | **missing** | P3 | S | Status item button frame → `convertRectToScreen:`. |

### Notifications and attention

| Feature | What it gives an app | How they do it (paths) | Ours | Pri | Eff | macOS API and signing notes |
|---|---|---|---|---|---|---|
| Local notifications: permission, post, click routing, withdraw, sound, grouping, actions and reply, attachments | Message notifications that open the chat, are withdrawn when it is read, and are silenced by Focus | `electron/shell/browser/notifications/mac/cocoa_notification.mm` (`UNMutableNotificationContent`, `UNTextInputNotificationAction`, `UNNotificationCategory`, `UNNotificationAttachment`, `removeDeliveredNotificationsWithIdentifiers:`); `notification_presenter_mac.mm` (`requestAuthorizationWithOptions:completionHandler:`); `notification_center_delegate.mm` (`willPresentNotification:` → banner/list/sound while in front, `didReceiveNotificationResponse:`); launch-by-notification read in `electron_application_delegate.mm` (`NSApplicationLaunchUserNotificationKey`). Tauri: `plugins/notification/src/desktop.rs` (`notify-rust`, `request_permission`, `register_action_types`, `remove_active`). ZapFast `zapfast/src/notify.rs` (`fastframe-notifications`; bundled sounds by name so Focus silences them; `zapfast/packaging/macos/bundle.sh` copies the `.wav` files into Resources) | **missing** | P0 | L | UserNotifications.framework. **Needs:** blocks (prerequisite 1); background-queue callbacks (prerequisite 2); a delegate set before launch finishes; `threadIdentifier` per chat; `UNNotificationSound soundNamed:` from Resources; `userInfo` carrying the chat id. **Signing:** no entitlement for local notifications. Requires a real bundle with `CFBundleIdentifier`, or the API throws, so it must report unavailable when unbundled. Permission is per bundle id (TCC), and stable signing avoids losing it. Push (APNs) needs the `aps-environment` entitlement plus a provisioning profile: not needed (P3). |
| Sounds outside notifications | Message tick while in front, call ringtone | ZapFast `notify::play_sound` (rodio) | **missing** (audio is out of this scope) | P1 | S | `NSSound initWithContentsOfFile:byReference:` / `play` covers short sounds without an audio stack. |

### Secrets, settings, files

| Feature | What it gives an app | How they do it (paths) | Ours | Pri | Eff | macOS API and signing notes |
|---|---|---|---|---|---|---|
| Secret storage (Keychain) | Holds ZapFast's SQLCipher archive key and lock data | Electron `safeStorage` (`electron/shell/browser/api/electron_api_safe_storage.cc` → Chromium `os_crypt`, one Keychain item per app; `electron/docs/api/safe-storage.md`); Tauri has no Keychain plugin (`plugins/stronghold` is its own vault); ZapFast `keyring-core` (`zapfast/src/archive/encryption.rs`, `zapfast/src/ui/login.rs` handle "key missing" and "keyring locked") | **missing** | P0 | M | Security.framework C API: `SecItemAdd`/`SecItemCopyMatching`/`SecItemUpdate`/`SecItemDelete` with `kSecClassGenericPassword` (service, account), built as `CFDictionary`s. The `kSec*` constants are data symbols read through `Dlsym`. Pure C, no blocks, safe off the main thread. **Signing:** the file-based login keychain needs no entitlement, but the item's ACL is tied to the app's designated requirement, so ad-hoc builds prompt again after every rebuild or update (`electron/docs/tutorial/code-signing.md`). Developer ID gives a stable requirement. Avoid `kSecUseDataProtectionKeychain`: it needs `keychain-access-groups` plus a provisioning profile. Errors to surface: `errSecItemNotFound`, `errSecUserCanceled`, `errSecInteractionNotAllowed` (locked keychain at login). |
| Settings store and atomic writes | Settings and wallpaper survive crashes | `plugins/store/src/store.rs` (JSON, defaults, debounced autosave); ZapFast `fastframe-file` | **missing** (trivial in Go) | P0 | S | `~/Library/Application Support/<id>` (`os.UserConfigDir`); write temp + `fsync` + `rename`. Optional: `NSUserDefaults` (we already touch it, `gnui/platform/platform_darwin.go:1287`). |
| App directories | Config, data, cache, logs per OS | Electron `app.getPath` (`electron/shell/common/electron_paths.h`); Tauri path resolver | **missing** | P0 | S | Application Support, Caches, Logs (`~/Library/Logs/<App>`); Go stdlib plus one Logs helper. |
| Logging to file, panic and crash capture | Diagnosable bug reports | `plugins/log/src/lib.rs` (stdout + LogDir targets, rotation `KeepOne/KeepSome/KeepAll`, `max_file_size`); Electron `crashReporter` (`electron/shell/browser/api/electron_api_crash_reporter.cc`, crashpad); ZapFast `fastframe-log` ("records panics without their payload") | **missing** | P1 (P0 in practice for ZapFast support) | S | Pure Go: a rotating file in `~/Library/Logs/<App>/`; `runtime/debug.SetCrashOutput` sends fatal Go crashes to a file, found on the next launch. Native crashes are already in Apple's `~/Library/Logs/DiagnosticReports`. An upload service (crashpad-like) is P2. |
| Open URL in browser / open file / reveal in Finder / trash | Links in messages; open or reveal a downloaded attachment | `electron/shell/common/platform_util_mac.mm` (`OpenExternal` → `NSWorkspace openURL:configuration:completionHandler:`, `ShowItemInFolder` → `selectFile:inFileViewerRootedAtPath:`, `MoveItemToTrashWithError` → `NSFileManager trashItemAtURL:`); `plugins/opener/src/{open.rs,reveal_item_in_dir.rs}`; ZapFast `zapfast/src/opener.rs` `open_or_reveal` | **partial**: `gnui/fs/open_darwin.go` opens paths with `LSOpenCFURLRef` on a worker; no http URL, reveal or trash | P0 | S | `LSOpenCFURLRef` also opens `https:` URLs (CF URL from bytes), avoiding the main thread. Reveal: `NSWorkspace activateFileViewerSelectingURLs:`. Trash: `NSFileManager trashItemAtURL:resultingItemURL:error:`. No signing. |
| Default app for a scheme | "Make ZapFast the default for whatsapp:" | `electron/shell/browser/browser_mac.mm` (`LSCopyDefaultApplicationURLForURL`, `LSSetDefaultHandlerForURLScheme`) | **missing** | P2 | S | `NSWorkspace setDefaultApplicationAtURL:toOpenURLsWithScheme:completionHandler:` (macOS 12+; the user confirms). |
| Recent documents | Dock and File → Open Recent | `browser_mac.mm` `AddRecentDocument` (`NSDocumentController noteNewRecentDocumentURL:`) | **missing** | P3 | S | |

### Clipboard, dialogs, drop

| Feature | What it gives an app | How they do it (paths) | Ours | Pri | Eff | macOS API and signing notes |
|---|---|---|---|---|---|---|
| Clipboard images and file URLs | Paste a screenshot or a Finder file into a chat; copy an image | `electron/shell/browser/api/electron_api_clipboard_mac.mm`; `plugins/clipboard-manager/src/desktop.rs` (`read_image`/`write_image`, `write_html`); ZapFast uses `arboard` `image-data` | **partial**: text only (`gnui/platform/clipboard_darwin.go`, `public.utf8-plain-text`) | P0 | M | `NSPasteboard` types `public.png`, `public.tiff`, `public.file-url`; decode with ImageIO (`CGImageSourceCreateWithData`) or Go `image/png`. Keep the asynchronous, bounded API of 5A. |
| Concealed clipboard (secrets kept out of history) | Copying a lock code or password | `plugins/clipboard-manager/src/secret.rs` (`exclude_from_history`) | **missing** | P3 | S | Also write the `org.nspasteboard.ConcealedType` marker type. |
| File dialogs | Attach files, save media | `electron/shell/browser/ui/file_dialog_mac.mm`; `plugins/dialog` (`rfd`) | **planned** (5B step 9) | P0 | M | `NSOpenPanel`/`NSSavePanel` as sheets (`beginSheetModalForWindow:completionHandler:`, a block). |
| Native message box | "Delete chat?", critical errors before the UI exists | `electron/shell/browser/ui/message_box_mac.mm` (`NSAlert`, sheet, suppression checkbox) | **partial**: drawn `widget` Dialog | P2 | S | `NSAlert`; mainly for errors before the first frame. |
| Drop into a window | Drag attachments in | `electron/docs/tutorial/native-file-drag-drop.md` | **planned** (5B step 11) | P0 | M | `NSDraggingDestination` on our view. |
| Drag out of the window | Drag an image from a chat to Finder | Electron `webContents.startDrag` | **missing**; excluded from phase 5 | P2 | M | `beginDraggingSessionWithItems:event:source:`. |
| Share menu | Share an image or link through AirDrop or Messages | `electron/docs/api/share-menu.md`; `electron/shell/browser/ui/cocoa/electron_menu_controller.mm` (`NSSharingService`) | **missing** | P2 | S | `NSSharingServicePicker showRelativeToRect:ofView:preferredEdge:`. |

### Windows

| Feature | What it gives an app | How they do it (paths) | Ours | Pri | Eff | macOS API and signing notes |
|---|---|---|---|---|---|---|
| Window state save and restore with off-screen recovery | Reopens where it was; never lost on an unplugged display | `plugins/window-state/src/lib.rs` (`StateFlags` SIZE/POSITION/MAXIMIZED/VISIBLE/FULLSCREEN; restores the position only if a monitor intersects it); ZapFast `fastframe-shell` `Geometry` (`zapfast/src/app.rs`) | **missing**: `app.Config` has only Width/Height; ARCHITECTURE §3 rule 3 wants persisted UI state read at startup | P0 | M | Our own small file, read synchronously before `Open`, so content-first startup is unchanged. Clamp against `NSScreen.screens` `visibleFrame`. `setFrameAutosaveName:` is the AppKit alternative, but it couples to `NSUserDefaults` and restores after creation. |
| Screens / displays | Restore, placement, per-display scale | `electron/shell/browser/api/electron_api_screen.cc`, `electron/docs/api/screen.md` | **missing** | P1 (needed by the row above) | S | `NSScreen screens`, `frame`, `visibleFrame`, `backingScaleFactor`, `NSApplicationDidChangeScreenParametersNotification`. |
| More than one window | Image viewer, call window | `electron/shell/browser/native_window_mac.mm` | **planned** (5B step 7) | P1 | L | — |
| Custom titlebar and traffic-light position | ZapFast's header carries the traffic lights | `electron/shell/browser/ui/cocoa/window_buttons_proxy.mm`, `native_window_mac.mm` `SetWindowButtonPosition`; `tauri/crates/tauri/src/window/mod.rs` `set_title_bar_style`; ZapFast `fastframe-titlebar` | **have** (`gnui/platform/platform.go` `Options.Titlebar`, `TrafficLights`; `SetDragRegions`, D24) | — | — | — |
| Vibrancy / sidebar material | ZapFast's "Messages" theme sidebar is AppKit's sidebar material (`zapfast/src/macos.rs` header comment) | `electron/shell/browser/native_window_mac.mm` `SetVibrancy` (`NSVisualEffectView`, materials Sidebar/HeaderView/…, `BlendingModeBehindWindow`, state follows the window); `tauri/crates/tauri/src/window/mod.rs` `set_effects` | **missing** | P1 | M | An `NSVisualEffectView` under a non-opaque `CAMetalLayer` (`opaque = NO`, clear alpha in the sidebar region). Effects on the startup frame and GPU cost to measure; Rex glass is drawn by us today. |
| Fullscreen, zoom, window tabbing | Native fullscreen; no stray "Show Tab Bar" item | `native_window_mac.mm` (`toggleFullScreen`, `setTabbingIdentifier:`, `addTabbedWindow:`); `tauri/crates/tauri/src/window/mod.rs` `tabbing_identifier` | **partial**: zoom and miniaturize are wired for the custom titlebar (`selZoom`, `selMiniaturize`) | P1 | S | `NSWindow.allowsAutomaticWindowTabbing = NO` at startup (otherwise AppKit adds tab items to the View/Window menu); collection behaviour `FullScreenPrimary`; `toggleFullScreen:`. |
| Content protection | Keeps chats out of screen recordings | `native_window_mac.mm` `setSharingType:` | **missing** | P3 | S | `NSWindowSharingNone`. |
| Represented file / edited dot, window level, always on top | Document apps; floating call window | `native_window_mac.mm` (`setRepresentedFilename:`, `setDocumentEdited:`, `setLevel:`) | **missing** | P3 | S | — |
| State restoration (Resume) | Reopen windows after a reboot | `electron_application_delegate.mm` `applicationSupportsSecureRestorableState:` | **missing** | P3 | M | Our own window-state file is simpler and fits content-first startup. |

### System state and permissions

| Feature | What it gives an app | How they do it (paths) | Ours | Pri | Eff | macOS API and signing notes |
|---|---|---|---|---|---|---|
| Sleep/wake, lock/unlock, session events | Reconnect the WhatsApp link after a wake; mark presence away when locked | `electron/shell/browser/api/electron_api_power_monitor_mac.mm` (`com.apple.screenIsLocked` on `NSDistributedNotificationCenter`, `NSWorkspaceSessionDidBecomeActive/ResignActive`); suspend/resume through Chromium; ZapFast `fastframe-power` (Linux/Windows) | **missing** | P1 | S | `NSWorkspace.notificationCenter`: `NSWorkspaceWillSleepNotification`, `DidWakeNotification`, `ScreensDidSleep/Wake`. Observers through our shared IMP. No signing. |
| Keeping the system awake, App Nap | Keep the display awake during a video call; don't throttle a hidden messenger's timers | `electron/docs/api/power-save-blocker.md`, `electron/shell/browser/api/electron_api_power_save_blocker.cc` (Chromium → IOPMAssertion) | **missing** | P1 | S | `NSProcessInfo beginActivityWithOptions:reason:` (`IdleDisplaySleepDisabled` for calls; `UserInitiatedAllowingIdleSystemSleep` while syncing), or IOKit `IOPMAssertionCreateWithName`. |
| Idle time, thermal state, battery | Auto-away presence; back off on battery | `electron/docs/api/power-monitor.md` (`getSystemIdleTime`, `thermal-state-change`, `on-battery`) | **missing** | P2 (idle), P3 (the rest) | S | `CGEventSourceSecondsSinceLastEventType`; `NSProcessInfo.thermalState`; IOKit power sources. |
| Microphone and camera permission | Voice messages and calls | `electron/shell/browser/api/electron_api_system_preferences_mac.mm` (`AVCaptureDevice requestAccessForMediaType:`); `electron/docs/api/system-preferences.md` `getMediaAccessStatus`/`askForMediaAccess`; ZapFast `zapfast/packaging/macos/Info.plist` (`NSMicrophoneUsageDescription`, `NSCameraUsageDescription`) + `entitlements.plist` (`device.audio-input`, `device.camera`) | **missing** | P0 if voice messages and calls move with ZapFast | S (plus blocks) | `AVCaptureDevice authorizationStatusForMediaType:` / `requestAccessForMediaType:completionHandler:` (a block, background queue). **Signing:** without the usage string TCC kills the process. Under the hardened runtime, without the entitlement access is silently denied. Grants are keyed to the signature. |
| Accessibility trust, Touch ID | Global hotkeys with an event tap; unlocking the app lock | `electron_api_system_preferences_mac.mm` (`AXIsProcessTrustedWithOptions`, `LAContext`) | **missing** | P2 (Touch ID for ZapFast's app lock, `zapfast/src/app_lock.rs`) | S–M | LocalAuthentication `evaluatePolicy:localizedReason:reply:` (a block). No entitlement for Developer ID. |
| Appearance, accent, high contrast, reduced motion and transparency | Following system settings | `electron/shell/browser/api/electron_api_native_theme_mac.mm`; `electron/docs/api/native-theme.md` (`prefersReducedTransparency`, `shouldDifferentiateWithoutColor`), `system-preferences.md` `getAnimationSettings` | **have** for dark/light and accent (D32, `gnui/platform/appearance_darwin.go`); **missing** for reduce motion and transparency, contrast | P1 | S | `NSWorkspace accessibilityDisplayShouldReduceMotion/ReduceTransparency/IncreaseContrast` + `NSWorkspaceAccessibilityDisplayOptionsDidChangeNotification`. |
| Locale, OS version, hostname | i18n catalogs; the device name in Linked Devices | `plugins/os/src/lib.rs` (`locale`, `version`, `hostname`); ZapFast `fastframe-i18n`, `gethostname` | **missing** (locale) | P1 | S | `NSLocale preferredLanguages`; the rest is in Go stdlib or `sysctl`. |
| Network reachability, system proxy | Fast reconnect when the network returns; corporate proxies | Electron `session` and `net` (Chromium); `electron/docs/tutorial/online-offline-events.md`; ZapFast `zapfast/src/proxy.rs` (its own setting + env) | **missing** | P2 | S–M | `nw_path_monitor_create` + `nw_path_monitor_set_update_handler` (Network.framework C API, a block); `CFNetworkCopySystemProxySettings` → feed Go's `http.Transport.Proxy`. |
| Global shortcut | System-wide "show ZapFast" | `electron/shell/browser/api/electron_api_global_shortcut.cc`, `electron/docs/api/global-shortcut.md` (media keys need accessibility trust); `plugins/global-shortcut` (`global-hotkey`) | **missing** | P2 (P3 for ZapFast) | M | Carbon `RegisterEventHotKey` + `InstallEventHandler`: no permission needed. A `CGEventTap` needs Accessibility/Input Monitoring. |
| Secure keyboard entry, emoji panel | Password fields; system emoji picker | `browser_mac.mm` `SetSecureKeyboardEntryEnabled`, `ShowEmojiPanel` | **missing** | P2 (emoji panel through the Edit menu role), P3 (secure input) | S | `orderFrontCharacterPalette:`; `EnableSecureEventInput`. |
| Handoff / user activity, Touch Bar, in-app purchase, push | — | `electron/shell/browser/mac/electron_application.mm` (`NSUserActivity`), `electron/shell/browser/ui/cocoa/electron_touch_bar.mm`, `electron/shell/browser/mac/in_app_purchase.mm`, `electron/shell/browser/api/electron_api_push_notifications_mac.mm` | **missing** | P3 | — | IAP and push need App Store or provisioning-profile entitlements. |

### Updates, packaging, distribution

| Feature | What it gives an app | How they do it (paths) | Ours | Pri | Eff | macOS API and signing notes |
|---|---|---|---|---|---|---|
| Self-update | ZapFast updates itself from GitHub releases with signed checksums, refuses package-managed copies (Homebrew), rolls back if the new one doesn't start | Electron `autoUpdater` = Squirrel.Mac (`electron/shell/browser/auto_updater_mac.mm`, `SQRLUpdater`; `electron/docs/api/auto-updater.md`: a signed app is required, a zip feed, sha256, size, delta patches, ATS). Tauri `plugins/updater/src/updater.rs`: JSON manifest `{version, platforms{target:{url,signature}}}`, `minisign-verify`, `.app.tar.gz` extracted to a temp dir on the same volume, `renamex_np(RENAME_SWAP)` for an atomic bundle swap, falling back to renames with restore; on a permission error, `osascript … with administrator privileges`; `touch` the bundle afterwards. ZapFast `zapfast/src/updates.rs` + `zapfast/packaging/UPDATE_SIGNING.md` (Ed25519 over `checksums.txt`; embedded current and next keys; fails closed; a helper with rollback) | **missing** | P0 | L | Pure Go for HTTP, `crypto/ed25519`, sha256, zip/tar. **macOS side:** check the new bundle's signature and Team ID before swapping (Security `SecStaticCodeCreateWithPath` + `SecStaticCodeCheckValidity` with a requirement `anchor apple generic and certificate leaf[subject.OU] = "TEAMID"`); swap with `renamex_np(RENAME_SWAP)` from libSystem; relaunch through a helper; detect App Translocation and read-only volumes and offer "Move to Applications" (Electron `app.moveToApplicationsFolder`, `electron/shell/browser/ui/cocoa/electron_bundle_mover.mm`). **Signing:** Developer ID plus notarization, so the swapped bundle passes Gatekeeper. Our own HTTP download sets no quarantine xattr. |
| App bundle and Info.plist | A real app: identity, icon, version, schemes, usage strings | `tauri/crates/tauri-bundler/src/bundle/macos/app.rs` (`create_info_plist`: `CFBundle*`, `LSMinimumSystemVersion`, `CFBundleIconFile`/`CFBundleIconName` (Assets.car), `CFBundleURLTypes`, `NSAppTransportSecurity`, merging a user plist, Frameworks, custom files); `electron/shell/browser/resources/mac/Info.plist` (`NSSupportsAutomaticGraphicsSwitching`, `NSQuitAlwaysKeepsWindows`, usage strings); ZapFast `zapfast/packaging/macos/Info.plist` | **partial**: `gnui/tools/mkapp.sh` writes minimal keys, ad-hoc sign | P0 | M | See §4. Add `NSSupportsAutomaticGraphicsSwitching = true` so a Metal app does not force the discrete GPU on dual-GPU Macs. |
| Icons | Dock and Finder icon at every size | `tauri/crates/tauri-bundler/src/bundle/macos/icon.rs` (writes an `icns::IconFamily` from PNGs); `tauri/crates/tauri-cli/src/icon.rs`; ZapFast `bundle.sh` (`sips` + `iconutil`) | **missing** | P0 | S | An `.icns` with embedded PNG entries (ic07–ic14) is simple to write in pure Go. Unverified: macOS 26's new icon format (Icon Composer → `Assets.car` via `actool`, which needs Xcode); without it macOS 26 may show the legacy icon inside a squircle plate. |
| Code signing, hardened runtime, entitlements | Gatekeeper acceptance; the Keychain, login items, the updater and TCC behave | `tauri/crates/tauri-bundler/src/bundle/macos/sign.rs` (inside-out `SignTarget` list; entitlements as a path or inline plist; `--options runtime` only for executables when `hardened_runtime`); `tauri/crates/tauri-macos-sign/src/keychain.rs` (an ephemeral CI keychain from a base64 `.p12` in `APPLE_CERTIFICATE`/`APPLE_CERTIFICATE_PASSWORD`: `security create-keychain`, `import`, `set-key-partition-list`; `codesign --force --timestamp --options runtime --entitlements`); `tauri/crates/tauri-macos-sign/src/certificate.rs` (a self-signed identity); `electron/docs/tutorial/code-signing.md` | **partial**: ad-hoc only | P0 | M | `/usr/bin/codesign` ships with macOS (no Xcode). Sign nested code first (helpers in `Contents/MacOS` or `Contents/Helpers`), then the bundle. Never `--deep` for signing, only for verifying. Go binaries need no JIT or library-validation exceptions: our `dlopen` targets are Apple-signed system frameworks. |
| Notarization and stapling | First launch without "cannot be opened" | `tauri/crates/tauri-macos-sign/src/lib.rs` (`ditto -c -k --keepParent` zip → `xcrun notarytool submit --wait --output-format json`, then `notarytool log <id>` on failure, `xcrun stapler staple`); auth from an API key (`APPLE_API_ISSUER`, `APPLE_API_KEY`, `APPLE_API_KEY_PATH`) or Apple ID (`APPLE_ID`, `APPLE_PASSWORD`, `APPLE_TEAM_ID`) | **missing** | P0 | M | Needs `notarytool` (Command Line Tools or Xcode) or a direct call to Apple's Notary REST API (JWT ES256 from the `.p8`; pure Go is possible). Stapling needs `stapler` (CLT); without it Gatekeeper checks online. |
| DMG and zip | Download artefact; update artefact | `tauri/crates/tauri-bundler/src/bundle/macos/dmg/mod.rs` + `bundle_dmg` (create-dmg script driving Finder through AppleScript for the layout; flaky on CI, as its comment notes); `tauri/crates/tauri-bundler/src/bundle/updater_bundle.rs` (`.app.tar.gz` + `.sig`); `tauri/crates/tauri-cli/src/signer/{generate,sign}.rs` (minisign keys, `TAURI_SIGNING_PRIVATE_KEY`); ZapFast `zapfast/packaging/macos/dmg.rb`, `verify.sh` | **missing** | P0 | S–M | `hdiutil create -fs APFS -format ULMO -srcfolder <staging with /Applications symlink>`, then sign, notarize and staple the DMG. Skip the Finder-scripted layout at first. |
| Homebrew cask, package-manager awareness | Installs through `brew`; the updater stands down | `zapfast/packaging/homebrew`; `zapfast/src/updates.rs` (`refuses package-managed copies`) | **missing** | P2 | S | Generate a cask from the release; the updater checks a marker in the bundle. |

---

## 3. Proposed shape of our app-services packages (names and APIs only)

**Principles** (from our invariants):
- **Requests and events.** Services follow the clipboard's model: a call queues a request, and the answer comes back as an app event, drained by the frame loop like `EvPaste`.
- **Threading.** No service ever calls back into Build. AppKit work runs on the main thread at safe points. Foreign-thread callbacks write to the MPSC ring and call `Wake()`.
- **Allocations.** Events are rare, so their payloads may allocate. Nothing runs per frame.
- **Startup.** Nothing loads a framework before the first frame, except the notification delegate when the app opts in.
- **One API, three OSes.** Each package has one OS-independent API with a backend per OS. Windows and Linux follow later; their natural backends are named in brackets.

### `internal/objc` additions (prerequisite, not public)
- `objc.GlobalBlock(fn CallbackSlot) Block`: a static block literal whose invoke is an ffi callback.
- `objc.InvokeBlock(b ID, args…)`: calls a completion handler AppKit gave us.
- Thread-safe `Cls`/`Sel` caches.
- Per-thread call frames for foreign-thread callbacks.
- Helpers to build `CFDictionary` and `CFString` values and read `Dlsym` data constants.

### `app` (existing): the application object and lifecycle
- `app.Config` gains:
  - `ID string` (bundle id / app id);
  - `KeepRunning bool` (do not quit when the last window closes);
  - `StartHidden bool`;
  - `Restore winstate.Key`.
- Events on the app-wide queue:
  - `EvReopen{HasVisibleWindows}`, `EvActivated`, `EvDeactivated`;
  - `EvQuitRequested{Reason: Menu|Logout|Update|Signal}`;
  - `EvOpenURLs{URLs}`, `EvOpenFiles{Paths}`, `EvSecondInstance{Args, Cwd}`;
  - `EvWillTerminate`.
- Methods:
  - `App.Activate()`, `App.HideApp()`, `App.SetDockVisible(bool)` (activation policy);
  - `App.ReplyQuit(allow bool)`, `App.Relaunch(args…)`;
  - `App.ShowAbout(AboutOptions)`;
  - `App.LaunchedAtLogin() bool`, `App.LaunchedByNotification() (notify.Response, bool)`.
- Window additions (on `platform.Window`/app window handle): `SetBackdrop(Material)`, `SetFullscreen(bool)`, `RequestAttention(Critical|Info) AttentionID`, `SetContentProtected(bool)`, `SetDocumentEdited(bool)`.

### `app/instance`
- `instance.Acquire(id string) (Primary, error)` or `(Secondary)`.
- `Secondary.Send(args []string) error`, then exit.
- The primary sees `app.EvSecondInstance`. [Windows: named mutex + pipe; Linux: abstract socket.]

### `app/notify`
- `notify.Available() bool`: false when unbundled or unsupported.
- `notify.RequestPermission(opts Alert|Sound|Badge)` → `EvPermission{Status}`; `notify.Status()`.
- `notify.Post(Note)`. A `Note` has ID, Thread, Title, Subtitle, Body, Sound (Default, None, or Named), Attachment (path), Category, UserData (`[]byte`) and Silent.
- `notify.Remove(ids…)`, `notify.RemoveThread(thread)`, `notify.RemoveAll()`.
- `notify.RegisterCategories([]Category{ID, Actions{ID, Title, Reply bool, Destructive}})`.
- `notify.Foreground(show bool)`: policy for `willPresent`.
- Events: `EvNoteResponse{ID, Action (Default|Dismiss|custom), ReplyText, UserData}`. [Windows: toast COM with AUMID; Linux: org.freedesktop.Notifications over our D-Bus client (D31).]

### `app/dock` (macOS Dock; taskbar or launcher entry elsewhere)
- `dock.SetBadge(text string)`, `dock.SetBadgeCount(n int)` (0 clears).
- `dock.Bounce(critical bool) BounceID`, `dock.CancelBounce(id)`.
- `dock.SetMenu(*menu.Menu)`, `dock.SetIcon(icon.Image)`. [Windows: taskbar overlay + `FlashWindowEx`; Linux: `com.canonical.Unity.LauncherEntry`.]

### `app/tray`
- `tray.New(Options{Icon icon.Ref | PNG []byte, Template bool, Tooltip, Title}) (*Item, error)`.
- `Item.SetIcon`, `Item.SetTitle`, `Item.SetMenu(*menu.Menu)`, `Item.Remove()`.
- Events: `EvTray{Item, Kind: Click|RightClick|MenuCommand}`; commands go through the normal `menu` command path. [Windows: `Shell_NotifyIcon`; Linux: StatusNotifierItem over D-Bus.]

### `app/secret`
- `secret.Open(service string) Store`.
- `Store.Get(account) ([]byte, error)`, `Store.Set(account, []byte) error`, `Store.Delete(account) error`.
- Errors: `ErrNotFound`, `ErrDenied`, `ErrLocked`, `ErrUnavailable`.
- Safe from any goroutine; blocking; call it from a task. [Windows: Credential Manager / DPAPI; Linux: Secret Service over D-Bus, with no plaintext fallback by default.]

### `app/login` (autostart)
- `login.Status() State`: `Enabled|Disabled|RequiresApproval|NotFound`.
- `login.Enable(Mode)`: `MainApp`, or `Agent{Plist: "<id>.autostart.plist"}` for start-hidden; `login.Disable()`.
- `login.OpenSettings()`. [Windows: Run key; Linux: XDG autostart / Background portal.]

### `app/power`
- Events: `EvWillSleep`, `EvDidWake`, `EvScreenLocked`, `EvScreenUnlocked`, `EvSessionActive{bool}`, `EvThermal{State}`.
- `power.Hold(kind KeepDisplayOn|KeepSystemOn|UserInitiated, reason string) Hold`; `Hold.Release()`.
- `power.IdleSeconds() float64`, `power.OnBattery() bool`.

### `app/shell`
- `shell.OpenURL(url)`, `shell.OpenPath(path)` (moves the existing `fs.openPath` up a level), `shell.Reveal(paths…)`, `shell.Trash(path) error`.
- `shell.DefaultHandler(scheme) (appPath, error)`, `shell.SetDefaultHandler(scheme)`.
- Worker-safe. Results come back as events or errors.

### `app/screen`
- `screen.All() []Screen{ID, Frame, Visible, Scale, Primary}`, and the event `EvScreensChanged`.

### `app/winstate`
- `winstate.Load(id) (State, bool)`, read synchronously before the window opens.
- `winstate.Apply(cfg *app.Config, s State, screens)`: clamps to a visible screen, falling back to centred on the primary.
- `winstate.Capture(win) State`; `winstate.Save(id, State)` writes atomically, debounced, on move/resize/close.
- State: frame, maximized, fullscreen, screen id, plus an app-owned opaque `[]byte` (panes, sort).

### `app/paths`, `app/store`, `app/log`
- `paths.Config(id)`, `paths.Data(id)`, `paths.Cache(id)`, `paths.Logs(id)`.
- `store.WriteAtomic(path, b []byte) error`; `store.JSON[T]` with defaults and a debounced save. Small: the app owns its schema.
- `log.Setup(Options{ID, MaxBytes, Keep, Stderr bool}) (*slog.Logger, error)` sets up the rotating file plus `debug.SetCrashOutput`.
- `log.PreviousCrash() (path string, ok bool)`.

### `app/perm`
- `perm.Status(Microphone|Camera|ScreenCapture|Notifications) State`.
- `perm.Request(kind)` → `EvPermission{Kind, State}`.
- `perm.OpenSettings(kind)`: an `x-apple.systempreferences:` URL.

### `app/updates`
- `updates.Config{Feed (GitHub repo or manifest URL), PublicKeys [][]byte (Ed25519; current + next), Current version, TeamID, Channel}`.
- `u.Check(ctx) (Release, bool, error)`.
- `u.Download(ctx, Release, progress func(n, total int64)) (Staged, error)`: verifies the manifest signature, then sha256 and size, then the code signature and Team ID.
- `u.Install(Staged) error`: atomic swap, relaunch through a helper, rollback if the new version does not report healthy.
- `u.Installation() Kind`: `Bundle|Translocated|ReadOnly|PackageManaged|Dev`, so it refuses where it cannot update.
- `updates.MarkHealthy()`: the new version calls it after its first successful start.

### `app/net` (later)
- `net.Watch()` → `EvNetwork{Online, Constrained, Expensive}`.
- `net.SystemProxy(u *url.URL) (*url.URL, error)`, usable as `http.Transport.Proxy`.

### `cmd/gnui` (the CLI; see §4)
- `gnui init`, `gnui run`, `gnui package`, `gnui sign`, `gnui notarize`, `gnui dmg`, `gnui release`, `gnui keygen`, `gnui icon`, `gnui doctor`.
- The config file is `gnui.json`: the standard library has JSON but no TOML or YAML.

---

## 4. Packaging, signing, notarization and updates: what `gnui package` does on macOS

**The config (`gnui.json`):**

```json
{
  "id": "rocks.zapfast.app",
  "name": "ZapFast",
  "version": "1.2.3",
  "build": "123",
  "category": "public.app-category.social-networking",
  "minimumSystem": "14.0",
  "icon": "assets/icon-1024.png",
  "resources": ["assets/sounds/*.wav"],
  "urlSchemes": ["whatsapp"],
  "documentTypes": [],
  "usage": {"microphone": "...", "camera": "..."},
  "entitlements": ["audio-input", "camera"],
  "launchAgents": [{"label": "rocks.zapfast.app.autostart", "args": ["--start-hidden"]}],
  "infoPlist": "packaging/macos/extra.plist",
  "updates": {"feed": "github:owner/repo", "publicKeys": ["assets/update-key.pub"]}
}
```

**Steps, in order:**

1. **Build.**
   - `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=…"` for darwin/arm64 and darwin/amd64.
   - Merge the two into a universal Mach-O. A fat header is simple to write in Go; `lipo` is the alternative, but it needs the Command Line Tools.
   - Arch-only output is an option (`--arch arm64`).
2. **Assemble the `.app`:**
   - `Contents/MacOS/<exe>`, plus an optional `Contents/Helpers/<updater-helper>` (signed separately);
   - `Contents/Resources/<name>.icns`, made in pure Go from the 1024 px PNG (ic07–ic14 entries);
   - the copied resources (sounds, localisations);
   - `Contents/Library/LaunchAgents/<label>.plist`. Generate it from `launchAgents`, using `AssociatedBundleIdentifiers` so Login Items shows it under the app, as ZapFast's `autostart.plist` pattern does;
   - `Contents/PkgInfo`.
3. **Write Info.plist:**
   - identity: `CFBundleIdentifier`, `CFBundleName`, `CFBundleDisplayName`, `CFBundleExecutable`, `CFBundlePackageType=APPL`, `CFBundleShortVersionString`, `CFBundleVersion` (monotonic; the updater compares it), `CFBundleIconFile`, `CFBundleDevelopmentRegion`, `CFBundleLocalizations`;
   - system: `LSMinimumSystemVersion=14.0`, `LSApplicationCategoryType`, `NSHighResolutionCapable`, `NSPrincipalClass=NSApplication`, `NSSupportsAutomaticGraphicsSwitching=true`, `NSHumanReadableCopyright`;
   - optional: `LSUIElement` (tray-only apps), `NSUserNotificationAlertStyle` (`alert`/`banner`);
   - from the config: `CFBundleURLTypes`, `CFBundleDocumentTypes`/`UTExportedTypeDeclarations`, `NS*UsageDescription`;
   - the user plist merged last, as Tauri's `info_plist` does.

   Validate with `plutil -lint`.
4. **Sign** (`gnui sign`; also runs inside `package` when an identity is set):
   - **Identity**, in order:
     - `--identity` or `GNUI_SIGN_IDENTITY` ("Developer ID Application: … (TEAMID)");
     - CI: a base64 `.p12` plus password, imported into a temporary keychain that is deleted on exit (Tauri's `keychain.rs` flow: `security create-keychain`, `unlock-keychain`, `import -f pkcs12 -T /usr/bin/codesign`, `set-key-partition-list -S apple-tool:,apple:`, prepended to the search list, restored afterwards);
     - development: `gnui-dev`, a self-signed code-signing certificate that `gnui doctor --setup-dev-cert` creates once, giving builds a stable designated requirement so the Keychain and TCC stop prompting again;
     - last resort: ad-hoc `-`, with a warning that notifications, Keychain and login items will misbehave.
   - **Order:** inside-out (helpers, frameworks, if any), then the bundle.
   - **Command:** `codesign --force --timestamp --options runtime --entitlements <generated.plist> --sign <id> <path>`. The hardened runtime goes on executables only. Entitlements are generated from the short names: `com.apple.security.device.audio-input`, `…device.camera`. No JIT or library-validation exceptions are needed for a Go binary.
   - **Verify:** `codesign --verify --deep --strict --verbose=2`, then `codesign -d --entitlements :-` to confirm the entitlements.
5. **Notarize** (`gnui notarize`):
   - Zip the app with `ditto -c -k --keepParent --sequesterRsrc` (or notarize the DMG from step 6 instead).
   - **Submit:** either `xcrun notarytool submit --wait --output-format json`, or `--direct`: the Notary REST API (`POST /notary/v2/submissions`, upload, poll), with a JWT signed ES256 by the App Store Connect key. That needs no Xcode at all.
   - **Auth**, in order:
     - API key: `GNUI_API_ISSUER`, `GNUI_API_KEY_ID`, `GNUI_API_KEY_PATH`; the same roles as Tauri's `APPLE_API_*`;
     - Apple ID: `APPLE_ID`, app-specific password, `TEAM_ID`;
     - a `notarytool store-credentials` keychain profile.
   - On rejection, fetch and print `notarytool log <id>`.
   - Staple with `xcrun stapler staple` where available, and validate with `stapler validate`.
   - Final Gatekeeper check: `spctl -a -t exec -vv <app>` must say "source=Notarized Developer ID".
6. **DMG** (`gnui dmg`):
   - staging folder: the app plus an `/Applications` symlink;
   - `hdiutil create -volname <Name> -fs APFS -format ULMO -srcfolder staging out.dmg`;
   - `codesign --timestamp --sign <id> out.dmg`, then notarize and staple the DMG;
   - no Finder-scripted layout at first. Tauri's create-dmg drives Finder through AppleScript and is fragile on CI. A background and icon positions mean writing `.DS_Store` ourselves later (P3).
7. **Update artefacts** (`gnui release`):
   - **Produce:** `<Name>-<ver>-macos-universal.zip` (`ditto`, keeps the signature and stapled ticket), the DMG, and `checksums.txt` (sha256 of each artefact).
   - **Sign:** write `checksums.txt.sig`, an Ed25519 signature over the exact bytes; ZapFast's scheme in `zapfast/packaging/UPDATE_SIGNING.md`, simpler than minisign and stdlib-only.
   - **Manifest:** optional `latest.json` `{version, build, notes, pub_date, files{target: {url, sha256, size}}}`.
   - **Keys:** `gnui keygen` writes the key pair. The private key is read from an env var or file in-process and never logged. Trusting the current and next public keys allows rotation.
   - **Optional:** a Homebrew cask snippet.
8. **`gnui doctor`** checks:
   - the Xcode Command Line Tools (`notarytool`, `stapler`; optional with `--direct`);
   - the identities in the keychain (`security find-identity -v -p codesigning`), certificate expiry, and that the Team ID matches `updates.teamID`;
   - that the notarization credentials work (`notarytool history`);
   - that the bundle id is reverse-DNS and stable across releases (ZapFast keeps its old `me.paolino.fastsapp` for exactly this reason);
   - that every entitlement has its usage string.
9. **`gnui run`** (development):
   - builds into a cached development bundle (`.gnui/dev/<Name>.app`) with the same Info.plist, signed with `gnui-dev`, launched through `open -W` (or exec'd directly with `-v`) so it gets a bundle identity;
   - this makes `notify`, `secret`, `perm` and `login` testable during development;
   - the bundle id gets a `.dev` suffix so development permissions and Keychain items never mix with the release app's.

**What the app does at run time, so updates work:**
- The updater refuses to update when the app is:
  - translocated (`SecTranslocateIsTranslocatedURL`, or a path under `/private/var/folders/…/AppTranslocation/`);
  - on a read-only volume (running from the DMG);
  - managed by a package manager (a marker file the cask installs).

  It offers "Move to Applications" instead (Electron `electron/shell/browser/ui/cocoa/electron_bundle_mover.mm`).
- **Install:**
  1. Download into a temp directory on the same volume as the app (as Tauri's comment explains, renames only work within one volume).
  2. Verify the signature, sha256 and size.
  3. Unpack.
  4. Check the code signature and Team ID.
  5. Swap with `renamex_np(RENAME_SWAP)`, falling back to two renames with a restore.
  6. If permission is denied, ask for authorisation (Tauri uses `osascript … with administrator privileges`; we can do the same, or tell the user to update by hand), then `touch` the bundle.
  7. Relaunch through a helper that waits for our pid.
- **Rollback:** the old bundle is kept until the new version calls `updates.MarkHealthy()`. Otherwise the helper swaps it back on the next start.

---

## Notes and limits of this report

**Not checked:**
- **ZapFast's `fastframe-*` crates** are external git dependencies and were not cloned. Their macOS mechanisms (`fastframe-notifications`, `-update` helper and rollback, `-dock`, `-power`) are inferred from ZapFast's `Cargo.toml` comments and call sites, not read.
- **Electron's implementations of** `powerSaveBlocker`, `globalShortcut`, `safeStorage` (`os_crypt`), `crashReporter` (crashpad) and suspend/resume live in Chromium, which is not in the Electron repo. They are cited through Electron's docs and wrappers only.
- **Nothing was run.** No signing identity, notarization, or measurement of framework load cost (UserNotifications, Security, ServiceManagement) against the 150 ms budget.
- **Thread behaviour is unverified** for the UNUserNotificationCenter delegate and completion handlers (assumed off the main thread).
- **Unverified on macOS 26:**
  - whether a legacy `.icns` without `Assets.car` is shown inside a squircle plate;
  - whether the Notifications "Badge app icon" setting hides `NSDockTile` badges.
