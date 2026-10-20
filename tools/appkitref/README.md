# AppKitRef

A dev tool for measuring macOS controls, not framework code. Like
`tools/bouncerec`, it is Objective-C.

It renders real AppKit controls offscreen at 2×, in Aqua and DarkAqua, by
setting each view's `appearance`, so the system's own setting is ignored. It
writes:

- **`light/*.png` and `dark/*.png`:** every sample on `windowBackgroundColor`.
- **`contact-light.png` and `contact-dark.png`:** one sheet per appearance.
- **`metrics.json`:**
  - intrinsic and fitting sizes, alignment insets and first baselines;
  - fonts per control size, the system text styles, and the system font from 9 to 34 pt (optical-size axis and advance per em, which show the tracking), plus every weight at 13 pt;
  - table, outline and sidebar metrics: rows, indentation, cell frames and fonts;
  - scroller widths, menu metrics, and window titlebar or toolbar heights with traffic-light frames for five window styles;
  - every NSColor semantic and system colour, resolved in Aqua, DarkAqua and both high-contrast appearances, as sRGB `#RRGGBBAA`;
  - corner radii, body edges and fill colours, and focus-ring geometry, all measured from the pixels;
  - behaviour values: double-click interval, key repeat, animation duration, reduce motion and the accessibility settings.

It never shows a window. The activation policy is Prohibited (with
`LSBackgroundOnly`), and windows are created deferred and never ordered on
screen. So it is safe to run over SSH on a machine someone is using, and it
needs no Screen Recording grant.

## Build and run

```sh
bash tools/appkitref/build.sh                       # -> tools/appkitref/bin/AppKitRef.app (ad-hoc signed)
tools/appkitref/bin/AppKitRef.app/Contents/MacOS/AppKitRef -o OUT -AppleAccentColor 4
```

`-AppleAccentColor 4` uses the argument domain to make this one process see
the default blue accent. It changes no setting. Leave it out to render the
machine's own accent.

Our counterparts and the side-by-side sheets:

```sh
CGO_ENABLED=0 go run ./tools/appkitref/ours -o OURS     # widget kit, Rex theme, same sample names
sh tools/appkitref/sheets.sh OUT OURS SHEETS           # AppKit left, ours right, per group, light and dark
```

Scroller geometry the renders cannot show (the knob is drawn by an
uncaptured layer): `sh tools/appkitref/scroller.sh` prints NSScroller's
widths, knob slots and knobs per control size and style as JSON
(`-[NSScroller rectForPart:]`, no window, no GUI session). The theme's test
reads it from `theme/testdata/appkit-scroller-27.2.json`.

## Which machine, which reference set

Every measurement file has a top-level `machine` object (`machine.h`): the
model, macOS version and build, the main display (name, points, backing scale,
refresh), the system appearance and accent (and the accent this process
rendered with, when `-AppleAccentColor` was passed), the scroller style and
the date. `AppKitRef -machine FILE` writes it alone; `scroller.sh -machine`
does the same for the scroller tool.

There are two committed sets, the MacBook's (the file names without a suffix)
and the Mac mini's (`-macmini`):

| | MacBook | Mac mini |
|---|---|---|
| Machine | MacBook Pro 14", `Mac15,6`, 26B5091g, built-in 1512×982 @2x, 120 Hz | Mac mini, `Mac16,10`, 26B5101f, LG UltraGear 5K, 2560×1440 @2x, 120 Hz |
| Recipes | `rexui/testdata/appkit-27.2.json` | `…-macmini.json` |
| NSScroller | `theme/testdata/appkit-scroller-27.2.json` | `…-macmini.json` |
| Scroll recordings | `ui/testdata/appkit-scroll/NAME.appkit` | `NAME-macmini.appkit` |
| Runs and scenes (outside the repo) | `~/Projects/research/appkit-ref/macbook-blue`, `macbook-system`, `scenes/captures` | `macmini-blue`, `macmini-system`, `scenes/captures-macmini` |

Which one a comparison reads is decided in one place, `internal/appkitref`:

- **CI tests** read the MacBook set, unless `GNUI_APPKIT_REF=macbook|macmini` names one (`TestRecipesMatchAppKit`, `TestLegacyScrollersMatchAppKit`, `TestScrollTracksAppKit`). The MacBook is reserved for the exit-criteria measurements.
- **Live comparisons and the dev tools** use the same variable, else the set whose machine is this host's (model and macOS build), else the MacBook's. `measure -set` prints the choice and the files; `measure -ref auto`, `ours -ref auto` and `gallery -ref auto` read that set's `metrics.json`.
- **Recording** never falls back: `measure -set-name` (used by `scroll.sh`) names the host's own set, or fails on an unknown machine unless the variable is set, so a new machine cannot write over another's files.

The scroller style is the system's, as a running app sees it. The Mac mini has a mouse, so its style is legacy and D0's live windows have 17 pt scroll bars; the MacBook's trackpad gives overlay. The mini's `appkitscenes` were captured both ways (`captures-macmini`, and `captures-macmini-overlay` with `EXTRA="-AppleShowScrollBars WhenScrolling"`), the second being the like-for-like set against the MacBook's.

`measure -diff macbook macmini` (from the repository root) lists what differs between the sets, biggest first: `docs/design/appkit-macbook-vs-macmini.md`. `unlocked.sh` exits 3 when the screen is locked; `live.sh` and the live runs start with it, and run under `caffeinate -d`.

## What offscreen rendering cannot show

- **Key-window state.** An app that is never active has no key window. `ARWindow` and `ARApp` report key, main and active, so controls draw their active look. Samples marked `inactive` are drawn honestly. Emphasised and unemphasised selection are both rendered.
- **Focus rings.** AppKit draws them in a separate layer that `cacheDisplayInRect:` does not capture. The tool draws them with the documented API (`NSSetFocusRingStyle(NSFocusRingOnly)` plus `drawFocusRingMask`) from the view's own mask, and measures that ring.
- **Window-server work.** Vibrancy and materials (sidebars, menus, popovers, toolbars), Liquid Glass (`NSBezelStyleGlass`), window shadows and the rounded window mask are all composited by the window server and are missing.
- **Some layers.** Scroller knobs (legacy and overlay), `NSTabView` tabs, and a sidebar's unemphasised selection are drawn by layers that are not captured; scroller widths are in the metrics instead.
- **Menus, popovers and tooltips.** They live in their own system windows. Only menu metrics are reported.
