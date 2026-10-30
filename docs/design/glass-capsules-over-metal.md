# Real glass over our own scrolled content: what is left to decide (#25)

**Status.** For Adam to decide. Nothing in this note is built.

**What #25 asked.** Native blur and glass over our own scrolled content (the
toolbar band, the scroll-edge pocket), with a step 1 to run before anything is
built: does `NSGlassEffectView` or a within-window `NSVisualEffectView` sample
a `CAMetalLayer` below it, and at what cost per frame?

## Step 1 is already answered

[docs/research/glass-over-metal.md](../research/glass-over-metal.md)
(`probes/glass/`, Mac mini M4, macOS 27.2, 120 Hz, 2026-10-20) measured exactly
that question the same day the issue was filed, and D54 decided the scroll edge
on it. This lane did not add a second probe: it would have repeated that one
with fewer measurements. What the study found, in the terms of the issue:

| Question | Answer | Where |
|---|---|---|
| Does a within-window `NSVisualEffectView` sample our Metal layer? | Yes, live, in the same frame (30 of 30 flash captures) | §2 |
| Does `NSGlassEffectView`? | Yes: it refracts the rows and takes the selection's colour. Inactive, it is flat but still tinted | §2 |
| Cost per frame | No measurable change in WindowServer CPU (44-49 % for every configuration at 120 Hz); whole-GPU busy 15 % to 22 % with the titlebar pocket, 33 % with AppKit's toolbar items, 10 % with 5 glass capsules (noisy, ±5 points); our own GPU time unchanged | §2 cost table |
| Hit-testing | A container whose `hitTest:` returns nil passes every click through to our view | §2 |
| A foreground layer above the glass | Stays in step with a transparent second `CAMetalLayer` presented in a transaction (0 of 7,129 frames apart), at about 1.4 ms of main-thread wait per frame | §2 |

D54 (2026-10-20) took the scroll edge: our renderer draws AppKit's measured
Hard and ThinFilm recipes (0.07-0.16 ms of GPU, mean ΔE 0.2-0.6 from AppKit,
offscreen renders and Windows/Linux included). Native glass stays below our
layer for the sidebar, inspector and window backdrop.

## What is still open

Only the toolbar's capsules. With `ToolbarOpts.ContentEdge`, rexui draws its
own capsules (flat tint, no refraction, no specular rim, no Liquid Glass slider,
no colour pickup from the content under them) instead of the system's glass.
D54 listed real capsules as "possible later, decided by eye once the drawn band
ships". The band has shipped; this is that decision.

## Options

1. **Keep the drawn capsules (today).** One drawable, offscreen renders and
   golden tests show them, Windows and Linux share them, and press feedback is
   ours. They do not refract or pick up colour. Effort: none.

2. **Native glass capsules above the Metal view, glyphs in a foreground layer**
   (the research's §4.2, effort L). `platform` puts `NSGlassEffectView`s *above*
   the app's view in a hit-refusing container (N3 puts them below), with a
   transparent band-sized `CAMetalLayer` over them for glyphs, hover and press.
   `gpu` presents a second drawable, in a `CATransaction` with the first when
   the toolbar changes together with the content. `ui` splits the draw list at
   the glass's z; rexui's toolbar moves its glyphs to the foreground.
   - Buys: the specular rim, refraction, colour pickup and the Liquid Glass slider.
   - Costs: a second drawable; about 1.4 ms of main-thread wait on frames
     presented in a transaction; AppKit's own press response
     (`effectIsInteractive`) is lost because the glass sees no events; offscreen
     renders and Windows/Linux fall back to option 1, so there are two looks to
     keep right.

3. **Glass capsules in a child panel** (main's panel windows, W2/D69, with the
   `Panel.Parent` child-window addition from #14). The
   panel sits over the band with behind-window blending, which blurs whatever is
   behind that window on screen, our Metal pixels included, and its own
   transparent Metal surface carries the glyphs, so it takes its own clicks.
   - Buys: no second drawable in the main window, no hit-refusing container, no
     transaction presents; the glyph layer is ordinary window content.
   - Costs, all unmeasured: the panel trails the parent during a live resize
     (child windows are repositioned after the parent), a second window in the
     accessibility tree, key-window and activation handling, Spaces and full
     screen, and the shadow of a window that is only a strip. The blur sampling
     the *parent window* from a separate window is expected to work (that is
     what behind-window blending is) but no run has shown it with our layer.

4. **Host an `NSToolbar`.** Rejected in D54 and still: it takes every click in
   the band, its pocket cannot be turned off, it sits above our layers, and it
   needs a rexui to `NSToolbarItem` bridge.

## Recommendation

Stay on option 1 and leave #25's capsule part parked, unless the capsules
looking flat has become a visible complaint. The evidence is that the scroll
edge, where the issue's ΔE 4.6 came from, is now drawn at a mean ΔE of 0.2-0.6 (2.5 over saturated colour in light mode), and the
capsules are the only difference left that a user would see by eye.

If Adam wants real glass: option 2, because it is the one that has been
measured end to end. Do it as an opt-in on macOS 26 and later with option 1 as
the fallback, so that goldens and the other platforms keep one look. Option 3 is
worth a one-evening probe before anyone chooses it (below), because if the
resize trailing is invisible it removes the second drawable and the transaction
wait, which are option 2's two real costs.

## Decisions for Adam

1. Is flat capsules acceptable for now? (Recommended: yes.)
2. If not, option 2 (measured, effort L) or probe option 3 first?
3. Should #25 be closed against D54, with the capsule work as its own issue?

## 2026-10-29: still parked

The Rex-inspired review asked again for richer glass over our content. No
new comparison was run: the earlier study's numbers stand (about 1.4 ms of
main-thread wait per frame for option 2), and the session had no Screen
Recording permission, so it could not capture glass against drawn
capsules by eye or by flash test
([rex-live-reference-2026-10-29.md](rex-live-reference-2026-10-29.md)).
Without a comparison that shows a visible benefit, nothing is enabled,
not even as an opt-in. The next step is unchanged: the option 3 probe mode
below, run from a session that can capture the screen.

## Not measured yet (needs an unlocked screen)

The study was one machine and one display. Before building anything: the
MacBook (the two Apple GPUs differ), Reduce Transparency, Increase Contrast,
the Liquid Glass slider, full screen with glass above the layer, and hover and
press on the glass. The study's commands are in its "Reproduce" section
(`probes/glass/run.sh`, `costs.sh`); the same tools run unchanged on the
MacBook. Option 3 needs a new probe mode: a borderless child window over the
band with an `NSGlassEffectView` and behind-window blending, the same flash
test (30 captures against a content that alternates every frame), and a live
resize with the capture running. That mode does not exist; it would be a small
addition to `probes/glass/glassprobe.m`.
