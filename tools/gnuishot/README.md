# GnuiShot

Dev tool (not framework code): a small macOS screen-capture helper so agents
can **look at our live app windows over SSH**, including what the OS draws
around them: titlebar, traffic lights, menus, native controls, shadows. It is
Objective-C (ARC, built by clang) like `tools/bouncerec`, uses
ScreenCaptureKit (`SCScreenshotManager`, macOS 14+), and is ad-hoc signed with
the stable identifier `dev.gonativeui.gnuishot`. `LSUIElement`: no Dock icon
or menu bar of its own to leak into captures.

## Build and install

```sh
bash tools/gnuishot/build.sh                 # -> tools/gnuishot/bin/GnuiShot.app (clang -Wall -Wextra, -mmacosx-version-min=14.0)
tools/gnuishot/bin/GnuiShot.app/Contents/MacOS/GnuiShot selftest    # pure-logic tests, no screen or grant needed

ssh -o BatchMode=yes user@testmac 'mkdir -p ~/gnui-test'
scp -r tools/gnuishot/bin/GnuiShot.app tools/gnuishot/shot.sh user@testmac:gnui-test/
```

Install location on the test Mac: **`~/gnui-test/GnuiShot.app`** (and
`~/gnui-test/shot.sh`). `tools/gnuishot/bin/` is build output (ignored by git).

## Grant Screen Recording (once, at the keyboard of the test Mac)

System Settings → Privacy & Security → **Screen & System Audio Recording** →
enable **GnuiShot**.

To make it show up in the list the first time, run (any command without a grant
also does it, unless `-no-prompt`):

```sh
~/gnui-test/shot.sh permission -request
```

then enable it, and check with `~/gnui-test/shot.sh permission` (`"granted": true`).
If macOS asks to quit and reopen the app, that is fine: every run is a fresh
`open -n`.

### Rebuild caveat (read before rebuilding)

For an ad-hoc signature macOS ties the Screen Recording grant to the **code
directory hash**, not only the bundle id. **Any rebuild that changes the binary
changes the hash and voids the grant**: someone has to redo it at the keyboard
(remove GnuiShot with "−", add/enable again, if the toggle looks on but capture
fails with "permission needed"). So: build once, grant once, copy that same
bundle, and do not rebuild casually.

How to tell whether the installed app is the one that was granted:

```sh
codesign -dvvv ~/gnui-test/GnuiShot.app 2>&1 | grep CDHash=
```

Recorded for the current source (v1.0, built 2026-10-18, macOS 27.2, arm64):

```
CDHash=62c5942f4107a6c8c34ead34846517a13f0c04f3
```

The build is deterministic here: rebuilding unchanged source with the same
toolchain gave the same CDHash (checked twice), so an unchanged rebuild should
not need a new grant. Any edit to `main.m`, `Info.plist` or the compiler flags
changes it. Update the line above whenever the app is deliberately rebuilt.

**Granted copies on the test Macs.** Each grant belongs to the bundle as it is installed. Don't rebuild, re-sign or replace an installed `~/gnui-test/GnuiShot.app`: Adam would have to grant it again at the keyboard.

| Test Mac | Signed with | CDHash | Granted |
|---|---|---|---|
| Mac mini (macOS 27.2, 26B5101f; LG UltraGear 5K at 2×) | `gnui-dev` | `b000e88b4f0342d2a8d2a88870fab6aa4621a2a0` | 2026-10-20 |

On the Mac mini the bundle is also write-protected (`chmod -R a-w`), so a stray copy fails instead of replacing it. Undo with `chmod -R u+w` only for a deliberate, re-granted replacement.
For a grant that survives rebuilds sign with a real identity:
`SIGN_IDENTITY="Apple Development: Name (TEAMID)" bash tools/gnuishot/build.sh`.

Without an Apple identity, `tools/resign-tools.sh gnuishot` builds the app and
signs it with the stable self-signed `gnui-dev` identity (`cmd/gnui/README.md`,
"The development identity"). Its designated requirement is
`identifier "dev.gonativeui.gnuishot" and certificate root = H"<gnui-dev SHA-1>"`
and does not change when the code does, so after the one switch from the
ad-hoc build, the Screen Recording grant should survive rebuilds signed by the
same certificate. (Confirm on the test Mac once: grant, rebuild a changed
GnuiShot, `shot.sh permission` still says granted.) The CDHash recorded above
stays the identity of the ad-hoc build only.

## Run (over SSH)

GnuiShot must be launched through LaunchServices so macOS treats *it* as the
responsible process for the Screen Recording check (a process started directly
from an SSH session is not). The display must be awake and the session
unlocked. `shot.sh` wraps this:

```sh
~/gnui-test/shot.sh <command> [options]       # prints the result JSON on stdout; non-zero exit on an error JSON
```

which is `open -g -W -n "$APP" --args <command> [options] -json <temp>` plus
reading the file back (`APP` defaults to `~/gnui-test/GnuiShot.app`). `open`
swallows stdout, so **every command writes its result to `-json <path>`
(required)**, images go to `-out <path>`. Use absolute paths when calling `open`
yourself (an app started by `open` has `/` as its working directory); `shot.sh`
makes `-out`/`-json` absolute for you. Don't pass `-json` to `shot.sh` unless
you want to keep the file.

Exit codes (in the JSON as `exitCode`, and the exit status of `shot.sh`; `open -W`
itself does not forward them): `0` ok, `2` screen recording permission needed
(`{"error":"screen recording permission needed"}`), `3` failure, `4` no matching
window/display, `5` timeout, `64` bad arguments (`{"error": ...}` with usage),
`1` (wrapper only) no result file, `66` (wrapper only) app bundle missing.

The app always exits by itself: after the command, or on its watchdog after
`15 s + -wait-ms + -poll-ms + (repeat-1)*interval + 0.5 s per frame` (`-timeout s`
replaces the 15). On timeout it writes `{"error":"timeout …","frames":[…captured so far…]}`
and exits 5, so `open -W` never hangs; the wrapper has its own backstop. A crash
or uncaught exception also leaves an error JSON.

### Commands

```
list        -json p [-app s] [-pid n] [-title s] [-id n] [-layer n|any] [-all]
window      -json p -out p.png  selectors, capture options (below)
display     -json p -out p.png [-display main|id] [-rect x,y,w,h [-global]] [-scale 1|2] [-cursor] [-wait-ms n]
            [-repeat n -interval-ms m] [-colorspace srgb|display-p3|native]
appearance  -json p
permission  -json p [-request]
selftest    [-json p]
```

`-no-prompt` (list/window/display) skips the Screen Recording prompt that a
missing grant otherwise triggers (the prompt appears on the user's screen).

**`list`**: on-screen windows (`SCShareableContent`, desktop windows excluded;
`-all` adds off-screen/other-Space windows) front to back, as `windows[]`:
`windowID`, `title`, `frame` `{x,y,w,h}` in **points, top-left origin, global
display coordinates (main display at 0,0)**, `layer`, `onScreen`, `active`,
`zOrder`, `ownerName`, `bundleID`, `pid`, and `display {id, scale, isMain}` (the
display holding most of the window). Also `displays[]` with `frame`, `scale`,
pixel size and an AppKit cross-check (`appKitFrame`, `frameMatchesAppKit`).
Filters: `-app` (name or bundle id, case-insensitive; exact match wins over
substring), `-pid`, `-title` (substring, case-insensitive), `-id`, `-layer`.
Window frames come from the window server (`CGWindowList` bounds); if
ScreenCaptureKit's own frame differs by more than 1 pt it is reported as
`scFrame`.

**`window`**: one window via `SCScreenshotManager` with an `SCContentFilter`
for that single independent window.

| option | meaning |
|---|---|
| `-id n` `-pid n` `-app s` `-title s` | selectors, combinable (all must match). Without `-id` the frontmost matching window at `-layer` (default 0) wins; windows under 4 pt² are ignored. With `-id` the layer filter is bypassed. No match → error JSON with `selector` and `candidates` (exit 4). |
| `-layer n\|any` | window layer to consider (default 0; menus/popups live on higher layers) |
| `-all` | also consider windows not on screen |
| `-poll-ms n` | keep looking up to n ms for the window to appear (right after launching the app under test) |
| `-wait-ms n` | wait n ms before the window is looked up and captured |
| `-shadow` | include the window shadow (default off via `ignoreShadowsSingleWindow`) |
| `-scale 1\|2` | pixels per point; default the window's display backing scale |
| `-cursor` | draw the cursor (default off) |
| `-opaque` | back transparent areas (rounded corners) with solid white |
| `-unclipped` | do not clip a window that is partly off screen |
| `-colorspace srgb\|display-p3\|native` | default `srgb` (comparable to sRGB-rendered expected images) |
| `-repeat n -interval-ms m` | a sequence of n frames, frame i scheduled at i·m ms after the first, written to `out-%03d.png` (see below) |
| `-refresh` | re-look-up the window before every frame (follows moves/resizes during a sequence; adds latency) |
| `-crop` | capture the **display region** around the window instead: everything composited (menus, popups, other windows, desktop) |
| `-include-app` `[-menubar]` | like `-crop` but only windows of the window's **app** (its menus, popovers, sheets) on a transparent background; `-menubar` adds the menu bar |
| `-pad pt` | grow the `-crop`/`-include-app` region by pt on every side (clipped to the display); e.g. to include the shadow |

A single-window capture shows only that window; open menus and popovers are
separate windows, so use `-include-app` (or `-crop`, or `display`) when you need them.

`-out`: a `.png` path; with `-repeat n` (n > 1) frames go to `<name>-000.png`,
`<name>-001.png`, … (or a `%0Nd` pattern in the name, or `dir/` → `dir/out-%03d.png`).
Parent directories are created, files are written atomically.

Result JSON (besides `command`, `appearance` {name, accent}, `colorspace`,
`mode`): `window` (the `list` entry), `windowFrame`, `display {id, frame, scale}`,
`scale`, `pixelSize {w,h}` and `out` (first frame), and `frames[]`, one per
frame:

```
index, path, pixelSize {w,h}
requestMachAbsTime / requestMs     just before the capture call (mach_absolute_time ticks / ms since GnuiShot launched)
completeMachAbsTime / completeMs   when the image came back;  captureMs = difference
scheduledMs, lateMs                when the frame was supposed to start, and how late it did
wallClock                          ISO-8601 with ms
```

The image is a point between `request` and `complete`; use `requestMs` ordering
and `lateMs` when reasoning about animation timing. `timestamps` holds the launch
mach time and timebase (`ns = ticks * numer / denom`). `warnings[]` flags frames
that are one flat colour (blank capture) or whose pixel size differs from the
request; `filter` holds `contentRect`/`pointPixelScale` as ScreenCaptureKit
reported them, for diagnosing size surprises.

**`display`**: the whole main display (or `-display <id>`; ids are in `list`).
`-rect x,y,w,h` is in points relative to the display's top-left, or with
`-global` in the global coordinates `list` reports (the display is then chosen
by the rect unless `-display` is given); it is clipped to the display
(`rectClipped`). This is the way to capture the menu bar and menus.

**`appearance`**: read-only. `appearance` (`Aqua` or `DarkAqua`), `isDark`,
`appearanceFromPrefs` (cross-check from `AppleInterfaceStyle`), `autoSwitches`,
`accent {name, hex, rgb, prefIndex}`, plus reduce transparency/motion, increase
contrast, differentiate without colour, invert colours. Every `window`/`display`
result carries the same name and accent, so a test records which mode the
capture was taken in. It does not change anything.

**`permission`**: `granted` true/false (exit 0 / 2); `-request` triggers the prompt.

**`selftest`**: pure logic, no screen, no grant: argument parsing and every
error case, selector matching against a fake window list (exact/substring app,
layers, z-order, ids), AppKit(bottom-left) ↔ top-left conversion with displays
left, right, above and below the main one, which-display-holds-the-window,
global↔local/clipping, pixel sizes, sequence naming, timeouts, frame-source
choice, appearance/accent mapping, uniform-image detection, PNG write/read
round trip (atomic, non-ASCII paths, alpha), JSON output. Run it directly
(`…/Contents/MacOS/GnuiShot selftest`, exit 0 on success, per-section PASS/FAIL
on stderr) or via `shot.sh selftest`. `hangtest` (undocumented diagnostic) hangs
on purpose to prove the watchdog.

## Examples

```sh
S=~/gnui-test/shot.sh
$S list -app MyApp                                   # find windows and ids
$S window -app MyApp -out /tmp/shots/main.png        # frontmost layer-0 window of MyApp, no shadow, display scale
$S window -app MyApp -title Settings -scale 1 -out /tmp/s.png
$S window -id 1234 -shadow -out /tmp/s.png
$S window -app MyApp -include-app -pad 8 -out /tmp/with-menu.png       # window + its open menus/popups
$S window -app MyApp -crop -pad 24 -out /tmp/in-context.png            # everything around it, incl. shadow
$S window -app MyApp -repeat 30 -interval-ms 33 -out /tmp/seq/x.png    # -> /tmp/seq/x-000.png … x-029.png (+ per-frame timestamps)
$S window -app MyApp -poll-ms 5000 -out /tmp/launch.png                # wait for the window to appear
$S display -out /tmp/screen.png                                        # whole main display
$S display -rect 0,0,800,24 -out /tmp/menubar.png                      # menu bar strip
$S appearance                                                          # Aqua/DarkAqua + accent
```

## Limits and notes

* The display must be awake and unlocked; windows on other Spaces need `-all`.
* macOS shows its screen-recording indicator while a capture runs.
* `-wait-ms`, `-poll-ms` and `-refresh` happen inside the app; the app is
  a fresh process per call (`open -n`), so each call costs roughly 0.1–0.3 s plus
  the capture.
* Frames in a sequence are encoded to PNG on background threads (at most 6 in
  flight); a slow disk or a very large window shows up as `lateMs`.
* Only `selftest`, `appearance`, `permission`, the argument/error paths and the
  watchdog could be run without the grant. The ScreenCaptureKit paths (`list`,
  `window`, `display`) were written against the API headers and could not be run
  on the dev machine (no grant there): the first real run on the granted Mac is
  their first test. If a size or position surprise shows up, look at `filter`,
  `frames[].requestedPixelSize`, `warnings` and `scFrame` in the JSON first.
