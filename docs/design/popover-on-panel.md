# rexui Popover on a native child panel (#14): evaluation, 2026-10-29

**Defect.** `BeginPopover` draws in the host window's `ui` tree, so a
popover near the window's edge flips or is pushed inside the window, and
can never reach past it as NSPopover does (#14). Its fill is opaque.

**What exists.** D84 added everything the platform side needs on macOS:
`platform.Panel.Parent`, `X`, `Y` (a child window ordered over its parent at
an offset that may lie past the parent's edge), `SetPanelOrigin` and
`SetPanelMask`. `app.OpenWindow` opens a panel with a Builder of its own
(`app.Config.Panel`), as `cmd/paneldemo` does. Windows and Linux have no
child panels (`SetPanelOrigin` is a no-op there), so they keep the in-window
popover.

## Design

An opt-in helper outside `rexui` (it needs `app`, which `rexui` does not
import), for example `rexui/panelpop`:

```go
var info panelpop.Popover
// in the host's Build, when the button is clicked:
info.Show(a, c.RectOf(infoKey), rexui.PopoverOpts{}, func(k *base.Kit) {
	rexui.Label(k, "Details", rexui.LabelOpts{Style: rexui.TextHeadline})
})
// each frame: if info.Dismissed() { ... }; info.Close() closes it.
```

- **Content and behaviour.** The panel's Builder calls `BeginPopover` with
  an anchor placed just outside the panel's own edge (for `PopoverBelow`, a
  rectangle ending at y = 0), so the arrow, body, focus move and
  semantics are the existing code. Inside the panel, the transient
  behaviour already does the right things: Escape dismisses, and the panel
  losing key (a click in the parent or another app) dismisses.
- **Side.** Chosen against the screen's visible frame (`app.Screens`) and
  the parent's content rectangle, not against the window, which is the
  point of the change.
- **Focus.** The panel is activating (not `NonActivating`), so the keyboard
  moves into it; on close the helper focuses the parent (`Win.Focus`).
- **Shape and shadow.** The panel is transparent with `Shadow`, so the
  system shadow follows the drawn outline. The fill stays the measured
  opaque recipe; a translucent material through `SetPanelMask` is a
  separate step.

## Why it is not built in this change

1. **Sizing needs a measured sequence.** The panel must be the body's size,
   which is known only after a Build. Resizing from inside Build is not a
   safe point (`SetFrameRect` draws through the frame handler), and a hidden
   panel builds nothing (`TestPanelShowHideKeepsStateAndBuildsNothingHidden`).
   The candidate sequence is: open hidden at the last known size for this
   popover → first Build measures → `Win.SetFrame` (deferred) → next Build
   at the right size → `SetPanelOrigin` → `Show`. Whether a hidden panel's
   deferred resize produces that next Build is unverified; if it does not,
   the first open shows one frame at the estimated size.
2. **The anchor's own press.** In the window, a press on the anchor closes
   a transient popover and does not reach the anchor. With a panel, the
   press goes to the parent (which toggles the popover) while the panel
   dismisses itself on losing key, so the order of the two decides whether
   it reopens. The helper needs a "dismissed this event turn" latch, tested
   with a real click.
3. **Semitransient** closes on a press in the window outside the popover.
   The panel never sees the parent's presses, so this needs the parent to
   report them.
4. **Nothing visual could be checked here.** The session that evaluated
   this has neither Screen Recording nor Accessibility permission
   ([rex-live-reference-2026-10-29.md](rex-live-reference-2026-10-29.md)).
   A popover that reaches past its window has to be seen once, light and
   dark, with the shadow, before it becomes an option apps can pick.

## Acceptance for the follow-up

- A popover anchored 20 pt from the parent's right edge, 280 pt wide,
  extends past the edge and keeps its arrow on the anchor (frame read back
  from `Win.Frame`).
- Escape and a click in the parent each dismiss it once and focus returns
  to the parent; a click on the anchor closes and does not reopen.
- Opening it allocates nothing per frame after the first; an idle open
  popover presents nothing (the existing idle gates, run on the panel).
- VoiceOver reads it as the in-window popover does.
- Off macOS, and on macOS without a parent window, `Show` builds the
  in-window popover instead.
