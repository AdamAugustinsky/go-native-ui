# Glass and blur over our own scrolled content (issue #25, step 1)

**Question.** Since D4b, content scrolls under the toolbar band, and the band
shows a flat bar. AppKit blurs what is under the band. Should we get that blur
from AppKit's own views over our `CAMetalLayer` (the native path), or draw it
in our renderer (the drawn path)? And could the toolbar's glass capsules be
real glass over our content?

**Answer.** Draw the scroll edge. AppKit's views do sample our Metal layer, live and in the same frame. But the only public ways to get AppKit's pocket over our content either take every click in the band or show the wrong colour. The pocket's recipe is now known exactly: a 6-pt blur under 82.45 % of the window's colour, plus 0.03. Drawn, it matches AppKit to mean ΔE 0.2–0.6, against D4b's flat bar at up to ΔE 16.5. It costs about 0.15 ms of GPU per frame. Real glass capsules over our content also work, and can come later as an option: native `NSGlassEffectView`s above the Metal layer, with the glyphs in a foreground layer.

**Machine.** Mac mini `Mac16,10` (Apple M4), macOS 27.2 (26B5101f), LG
ULTRAGEAR+ at 2560 × 1440 pt, 2×, 120 Hz. Light mode, pink accent. Reduce
Transparency and Increase Contrast were off. Run on 2026-10-20, with other
agents' live tests running on the same machine (see "Limits").

**Probe.** `probes/glass/` (Objective-C, clang, like `tools/appkitref`;
analysis in Go with `internal/cielab`). Captures and JSON are in
`~/Projects/research/glass-probe/` (outside the repository). Every capture is
GnuiShot's single-window capture at 2×, without the shadow. The content is
the same bitmap in every mode: an inset table with stripes, a two-row blue
selection, coloured icons and secondary text (`content-{light,dark}.png`):

- **AppKit's windows** show it as an `NSScrollView` document (`-mode ref`) or a plain layer (`-mode refplain`).
- **Our window** draws it in a full-window `CAMetalLayer` (`-mode metal`), as D52's shell does.

"Scrolled" means 100 pt under the band (text under it), or 160 pt (the blue
selection under it). ΔE is CIEDE2000 over the band, rows 2–51.5 pt, from x = 90 pt
(clear of the traffic lights) to the width less 10.

## 1. What AppKit draws under the band (27.2)

The probe dumped each pocket's layer tree (`*-dump.json`). On 27.2 the scroll
edge is `_UIScrollEdgeEffectLayer` inside an `NSScrollPocket`, in one of two
recipes. Both are window-server backdrops (`CABackdropLayer`), and the
"zero-size" views D0 saw are hidden leftovers:

| Recipe | Where AppKit uses it | Layers, bottom to top |
|---|---|---|
| **Hard** | The titlebar's own pocket: any `NSToolbar`, no scroll view under it. An `NSTableView` with a header: the pocket is 0–80 pt (`ref/reftable-light-inactive-dump.json`). D4b's tables measured this one | `HardPocketContentBlur`: backdrop at ¼ scale, `gaussianBlur` radius 6 (normalised edges, dithered), colour matrix identity + 0.03, `sdrNormalize`. Then `HardPocketBackgroundReplay`: the window's content colour (#FFFFFF / #1E1E1E) + 0.03, opacity **0.8245**. Then the separator, 0.5 pt, black 10 % / white 7 % |
| **ThinFilm** | A plain `NSScrollView` (no header) under a unified toolbar, with or without items | `ThinFilmBackgroundReplay`: the content colour at opacity 0.5 light / 0.6 dark, desaturated (s 0.75 / 0.83). Then `ThinFilmContentBlurFill`: backdrop at ½ scale, `variableBlur` radius 2 with a 1 × 256 vertical mask (1.0 at the top edge, falling to 0.25 at the hairline), saturation 1.25, + 0.03, `sdrNormalize`. Then the same separator |

Active and inactive windows use the same recipe. Their bands are identical
(ΔE 0.00, `ref/ref-light-{active,inactive}-scrolled.png`). D4b's measured
"18 % shows through" is the Hard recipe's 1 − 0.8245.

## 2. The native path

### Does AppKit's glass or blur sample our CAMetalLayer, live?

Yes, every kind tried:

| Native view over our Metal content | Samples it? | Capture |
|---|---|---|
| The titlebar's Hard pocket (an empty unified `NSToolbar`) | Yes. Over our Metal layer it is **pixel-identical** to the same pocket over an AppKit layer with the same pixels: mean ΔE 0.00–0.10, max ≤ 1.2 (dither) in five cases, light and dark, active and inactive | `native/none-*` vs `native/refplain-*` |
| `NSVisualEffectView`, within the window, material headerView, state active | Yes, heavily blurred. Over neutral text it is mean ΔE 0.32 from the Hard pocket (no hairline) | `native/vev10-light-inactive-scrolled.png` |
| `NSGlassEffectView` capsules in an `NSGlassEffectContainerView` | Yes: they refract the rows (text bends at the rim) and take the selection's blue. Inactive glass is flat with no refraction, but still tinted (#80C6FF at a capsule's centre over the selection) | `sheets/glass-active-vs-inactive.png` |
| Real `NSToolbar` items (AppKit's glass capsules, Hard pocket) | Yes, exactly as over AppKit content | `native/items-light-active-scrolled:160.png` |

**Live, in the same frame.** The content alternated orange and blue on every
120 Hz frame while GnuiShot took 30 captures 83 ms apart. The band followed
the content in **30 of 30** captures for each of the titlebar pocket, the
visual-effect view, the glass capsules and the toolbar items (`flash/`). A
backdrop one frame late would have disagreed every time. The window server
samples the drawable it composites, in the same pass.

### Cost

Each configuration scrolled at 120 Hz for 6 s in an active 900 × 560 pt
window, in four interleaved rounds (three for the last block), with no
captures meanwhile (`probes/glass/costs.sh`, `summarize.py`;
`costs2/`, `combo/`). Two drawables, as `gpu.Surface` uses. Medians over the
rounds whose window stayed active:

| Configuration | Our GPU per frame, p50 / p95 | Presented after the link's target, p50 | WindowServer CPU | Whole-GPU utilisation |
|---|---|---|---|---|
| Our content only (base) | 0.39 / 0.51 ms | 16.7–25 ms | 46 % | 15 % |
| + titlebar Hard pocket (empty `NSToolbar`) | 0.24 / 0.35 | 20.8 | 45 % | 22 % |
| + `NSToolbar` with 4 items (glass + pocket) | 0.40 / 1.24 | 25.0 | 46 % | 33 % |
| + `NSVisualEffectView` over the band | 0.34 / 0.50 | 16.7 | 44 % | 15 % |
| + 5 `NSGlassEffectView` capsules | 0.43 / 0.85 | 16.7 | 45 % | 10 % |
| Drawn blur, band re-rendered (§3) | 0.64 / 0.87 | 16.7 | 49 % | 18.5 % |
| Drawn blur + native glass + foreground layer (the combination) | 0.59 / 0.79 | 16.7 | 49 % | 23 % |

- **WindowServer CPU** is 44–49 % for every configuration. That is what it spends compositing this window at 120 Hz. No native view adds measurably to it: the spread between rounds is ±4 points.
- **Latency** is quantised to frames. Every configuration lands 16.7 or 25 ms after the display link's target, and none of them is consistently later.
- **GPU** is the only place where native views show up. Whole-GPU busy rose from 15 % to 22 % with the titlebar pocket and to 33 % with AppKit's toolbar items. At 120 Hz, one point is about 0.08 ms per frame, so that is about 0.6 and 1.5 ms of the window server's GPU time per frame. The figure is noisy (±5 points, other processes included), but the toolbar's rise held in all four rounds. Per-frame GPU time of the window server itself could not be read: `xctrace` needs Xcode, which this Mac lacks.
- **Idle:** after a still frame, every configuration made **0 presents** over 6 s, our process used 0.2–0.5 % CPU (that is the probe's own 10 Hz GPU sampler), and the WindowServer stayed at 0–4 %. A few runs read 15–45 %, from background load that hit the base configuration too. No native view keeps anything animating.

### A foreground layer in step with the content

The probe's foreground is a second, transparent `CAMetalLayer` above the
native views. It holds the toolbar glyphs, and a sync marker drawn by both
layers at the same x. Both layers were redrawn every frame, and the probe
compared their drawables' `presentedTime`:

| How both present | Frames on different vsyncs | Main thread per frame |
|---|---|---|
| Each `presentDrawable:` on its own command buffer (async) | 1 of 4,259 | 0.9 ms |
| `presentsWithTransaction` on both, presented in one `CATransaction` | **0 of 7,129** | 1.55 ms: about 1.4 ms waiting in `waitUntilScheduled` for two command buffers. Arrived a frame earlier (16.7 vs 25 ms after target, 3-drawable run, `sync3/`) |
| One command buffer presenting both drawables | 2 of 2,128 | — |

Captures agree: 40 of 40 frames during a scroll had both markers aligned, for
async and for transaction presents (`sync/`). The framework already presents
in a transaction when the shell's views move (`gpu.Surface.EndFrame`,
`ShellPending`). A foreground layer would need the same whenever its pixels
change together with the content's.

### Hit-testing

- **Native views inside a container that refuses hits** (`hitTest:` returns nil): every point in the band, on a capsule or between capsules, hit-tests to our view, and a synthesised click reached it (`native/glass-light-active-hit.json`).
- **Any `NSToolbar`, even an empty one:** every band point hit-tests to `NSToolbarView` or its item viewers, and our view gets nothing (`native/{refplain,hc,items}-*-hit.json`). This is D52's "the title area swallows clicks", and it holds for an empty toolbar too.

### Reduce Transparency, Increase Contrast, inactive, full screen

- **Inactive:** the pocket keeps its recipe and is always on. Glass turns flat (above).
- **Increase Contrast:** the window's high-contrast appearance (`NSAppearanceNameAccessibilityHighContrastAqua`) does not change the pocket: the same recipe, ΔE 0.00 (`native/hc-light-*`). The pocket follows the system setting, which was not changed. Not measured.
- **Reduce Transparency:** a system setting, not changed. Not measured. `platform` already swaps materials for opaque `NSBox`es under it, and rexui's scroll edge already draws its opaque fill.
- **Full screen:** not measured. Entering it moves the window to its own Space. With other agents' live windows on this Mac, that would have taken their windows off screen for several seconds. It matters only for the `NSToolbar` route, where AppKit may move the toolbar to a separate window. The recommended paths keep everything in our content view.

### Can AppKit's pocket be had through public API, under our control?

Not usably:

- **An `NSToolbar` (even empty)** gives the titlebar's Hard pocket over our layer, pixel-exact. But it is **always on**, active or inactive, scrolled or not. In an active window at rest it shows a hairline (light) and a #262626 bar over #1E1E1E (dark), where AppKit's own windows show nothing. `titlebarSeparatorStyle` does not hide it (`native/sep-*`). It also takes every click in the band (above), and it sits above our content view, so a foreground layer of ours would be under it.
- **A transparent `NSScrollView` laid over our layer** gets a pocket that follows its scroll offset: no hairline at rest in an active window, on when scrolled. But it is the ThinFilm recipe, and its "background replay" captures what is behind the transparent scroll view: our content. So it replays our blue instead of the window colour. Over the selection it reads #1779F7 where AppKit shows #D2E5FF, a mean ΔE of 27 (`native/pocket-empty-light-active-scrolled:160.png`). Giving it a background would cover our layer.
- **Without a toolbar** (our shell: transparent titlebar, title hidden), no pocket appears, with or without an overlay scroll view or manual `contentInsets` (`native/pocket-notb-*`).

### Could Apple's toolbar capsules replace our drawn ones?

They render correctly over our content, but adopting them means adopting
`NSToolbar`, and that brings:

- the band's clicks go to AppKit (drawn tabs, a drawn title or search field, and band dragging would all need event re-routing in our `NSWindow` subclass);
- the always-on pocket;
- AppKit's item layout and overflow, behind a bridge from rexui's toolbar API to `NSToolbarItem`s (symbols, groups, menus, the search item);
- about 1.5 ms more of the window server's GPU time per frame while scrolling.

Free-standing `NSGlassEffectView` capsules in our own view tree give the same
glass without any of that (§4).

## 3. The drawn path

### The primitive

The probe implements AppKit's Hard recipe in Metal:

1. Render the band's content at half size: the probe redraws the content quad, as our renderer would re-encode the quads under the band.
2. Halve again to a quarter. Both halvings are bilinear.
3. Blur with a separable Gaussian, σ = 6 pt (3 px at a quarter).
4. Composite in the main pass: mix the blur with the window's content colour at 0.8245, add 0.03, then the 1-px hairline (#0000001A / #FFFFFF12).

The textures are allocated once for the window's size, and nothing is allocated per frame. A second design reads the band back from the frame and composites in a second pass over the whole drawable. Dual Kawase (3 levels) was tried in place of the Gaussian.

### GPU cost

The cost was measured offscreen (`-mode bench`, Apple M4, 300 frames after a 20-frame warm-up, GPU time of one command buffer per frame,
`bench/light-bench.json`). Each figure is a whole frame: content plus band.

| Window (pt) | Band (px) | Flat bar (D4b) | Blur, re-rendered band | Blur, read back | Kawase, re-rendered |
|---|---|---|---|---|---|
| 900 × 560 | 1800 × 104 | 0.12 ms | 0.27 | 0.24 | 0.31 |
| 900 × 560 | 1800 × 160 (with a header) | 0.12 | 0.27 | 0.23 | 0.28 |
| 1440 × 900 | 2880 × 104 | 0.18 | 0.34 | 0.25 | 0.38 |
| 2560 × 1440 | 5120 × 104 | 0.38 | 0.53 | 0.52 | 0.57 |
| 2560 × 1440 | 5120 × 160 | 0.39 | 0.54 | 0.53 | 0.57 |

So the blur adds **0.07–0.16 ms** per frame, about 2 % of a 120 Hz frame,
mostly in the fixed cost of four small passes. Band size barely matters. On screen, a single command buffer measured 0.64 ms against 0.39 for the
base (§2 table), so about +0.2 ms. Kawase is no cheaper at this size.

### Look against AppKit

The reference is AppKit's Hard pocket over the same pixels. That is the titlebar pocket over our Metal layer, which §2 showed is pixel-identical to AppKit over AppKit. The captures (`drawn/`, `native/`) and the band, side by side (`sheets/band-native-vs-drawn.png`):

| ΔE2000 over the band | Light, text under (100) | Light, selection under (160) | Dark, text under (100) | Dark, selection under (160) |
|---|---|---|---|---|
| D4b's flat bar (82 % #FFFFFF / 78 % #292929): mean / p99 / max | 0.45 / 4.37 / 8.18 | 2.26 / 10.37 / 11.82 | 0.81 / 8.39 / 12.81 | 3.55 / 15.14 / 16.50 |
| D4b, over ink only: mean / max | 4.26 / 8.18 | 1.62 / 7.82 | 7.59 / 12.81 | 10.70 / 16.50 |
| **Drawn Hard recipe (Metal capture)**: mean / p99 / max | **0.23 / 1.34 / 1.58** | **2.50 / 3.64 / 4.28** | **0.24 / 1.15 / 1.25** | **0.61 / 1.42 / 1.98** |
| Same, dual Kawase | 0.23 / 1.34 / 1.58 | 2.48 / 3.59 / 3.92 | 0.20 / 1.15 / 1.25 | 0.60 / 1.42 / 2.11 |

"Ink" is content pixels whose luminance differs from the window colour by
more than a quarter: text, icons and the selection.

- The GPU captures equal the probe's CPU model of the same recipe (`analyze fit -eval 6,0.8245,1,0.03`). The implementation is the recipe.
- The residual floor, ΔE ≈ 1, is AppKit's dither (`inputDither`).
- **The one gap is light mode over saturated colour.** Over the blue selection, AppKit's band is #D2E6FF and ours is #CEE3FD, a mean ΔE of 2.5. The vertical profile through the blur matches to 1–2 levels. Doing the arithmetic in Display P3 made it worse (ΔE 3.0). A saturation of 1.25 fits light/blue (0.72) but breaks dark (3.0). It is probably the window server's extended-range handling (`sdrNormalize`). It is left as a known difference, well under D4b's.

The ThinFilm recipe fits as well: a constant σ of 5–6 pt gives mean ΔE 0.35 in light and 0.95 in dark against `ref/ref-*-active-scrolled.png`. Its variable-blur mask was not modelled.

## 4. Recommendation

**Draw the scroll edge, with AppKit's recipes. Keep native glass for later,
as an option, and keep `NSToolbar` out.**

1. **Now: a drawn backdrop primitive (effort M).** It gives AppKit's look within ΔE 1 (2.5 over saturated colour in light). The bar shows exactly when our scroll state says so, in the same frame, and always when inactive. Our toolbar keeps its clicks and its z-order. Offscreen renders and goldens show it, Windows and Linux can share it, and it costs about 0.15 ms of GPU per frame.
   - **`gpu`:** a backdrop command in the `DrawList`: a rectangle, a recipe, and the index that splits "under" from "over". The renderer:
     1. re-encodes the quads under the split that intersect the band, at half size, into a preallocated texture;
     2. halves it, and blurs it with a separable Gaussian;
     3. draws the composite as a new quad `Mode` inside the main pass.

     The textures are sized with the window and freed on resize, with nothing allocated per frame. Metal first, then the D3D11 and GL shaders.
   - **`ui`:** a way to record that command at the right point in z: after the content of the scroll view, before its header and the toolbar. AppKit draws the header text sharp, above the pocket (`sheets/reftable-band.png`).
   - **`rexui`:** `scrollEdge` and the toolbar's band use the backdrop. Tables with headers and the bare band use Hard; plain scroll views can later use ThinFilm (soft). Reduce Transparency keeps today's opaque fill, and ContentEdge capsules stay drawn.
   - **`theme`:** the recipes' constants, per measured build: σ 6 pt, replay 0.8245, +0.03, the hairlines. Add ThinFilm's when it is used.
   - **`platform`:** nothing.
   - **Tests:** goldens of the band in light and dark, and a live GnuiShot comparison against `-mode ref` / `refplain` with a ΔE gate.
2. **Later, optional: real glass capsules over our content (effort L).** It is shown to work: the capsules sample our layer, including a drawn pocket under them, in the same frame. Hits pass through a container that refuses them. A foreground layer stays in step when presented in a transaction: 0 splits in 7,129 frames. The look is AppKit's toolbar over its pocket (`sheets/combo-stack.png`).
   - **`platform`:** glass views *above* the Metal view (N3 puts them below), in a hit-refusing container, plus a transparent foreground `CAMetalLayer` view above them (band-sized).
   - **`gpu`:** a second drawable per frame for the foreground.
   - **`ui`:** splitting the draw list at the glass's z.
   - **`rexui`:** the toolbar's glyphs, hover and press moved to the foreground. AppKit's own press response (`effectIsInteractive`) is lost, because the glass gets no events.

   What it buys: the specular rim, the refraction, colour pickup and the Liquid Glass slider. What it costs: a second drawable whenever the toolbar changes; about 1.4 ms of main-thread wait on frames presented in a transaction; and offscreen renders fall back to the drawn capsules. Decide after the drawn pocket ships, by eye.
3. **Not recommended:**
   - **Hosting an `NSToolbar`:** it takes the band's clicks, its pocket is always on with no public switch, it sits above our layers, it needs a rexui→`NSToolbarItem` bridge, and it costs about 1.5 ms of window-server GPU.
   - **A transparent `NSScrollView` over our layer for its pocket:** it gives the wrong colour, and it depends on unspecified AppKit behaviour.

**Risks of the drawn path:**

- The constants are AppKit's for 27.2, read from private layer names, and a later release can change them. The probe re-reads them: `-phases inactive-dump`.
- The Liquid Glass slider and Increase Contrast were not measured. If they change the pocket, the drawn bar will not follow until we read the setting and measure that state.
- In light mode over saturated colour it stays about ΔE 2.5 off.

## Limits

- **A shared machine.** Other agents ran live tests during the runs, and active phases held the `gnui-testlock-activation` lock that live tests share. Some rounds lost activation, and those are left out of the medians. The WindowServer CPU and GPU-utilisation figures carry that background (±4 and ±5 points). The first cost run (`costs/`) used three drawables and had a probe bug in its two drawn configurations. It is superseded by `costs2/`.
- **Not measured:**
  - Reduce Transparency, Increase Contrast and the Liquid Glass slider: these are system settings, and none was changed;
  - full screen (see above);
  - the window server's own GPU time per frame (`xctrace` needs Xcode);
  - hover and press on native glass (the pointer was never moved; clicks were synthesised in-process).
- **One machine and one display.** The MacBook was not used.

## Reproduce

```sh
sh probes/glass/build.sh                                   # bin/GlassProbe.app (ad-hoc signed)
R=~/Projects/research/glass-probe
sh probes/glass/run.sh $R/ref ref-light inactive-scrolled,inactive-dump -mode ref -toolbar empty -appearance light
sh probes/glass/run.sh $R/native none-light inactive-scrolled:160,inactive-dump -mode metal -toolbar empty -appearance light
sh probes/glass/run.sh $R/drawn gauss-light inactive-scrolled:160 -mode metal -toolbar none -drawn 1 -appearance light
sh probes/glass/costs.sh $R/costs2 4 && python3 probes/glass/summarize.py $R/costs2
SEQ=30 SEQ_MS=83 sh probes/glass/run.sh $R/flash glass active-flash -mode metal -toolbar none -overlay glass -flashframes 1
probes/glass/bin/GlassProbe.app/Contents/MacOS/GlassProbe -mode bench -out $R/bench -tag light
go run ./probes/glass/analyze cmp A.png B.png                    # ΔE over the band
go run ./probes/glass/analyze fit -recipe hard -content $R/native/content-light.png -ref REF.png -scroll 160 -eval 6,0.8245,1,0.03
go run ./probes/glass/analyze flash $R/flash/glass-active-flash -x 114
```

Write probe arguments out literally in zsh: an unquoted `$args` is not split there.
