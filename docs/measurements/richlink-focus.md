# SH-016: RichText span focus (2026-10-22)

Branch `p6-richlink-focus`, based on fetched `origin/main` **724afd7**.
This closes the framework portion of ZapFast shell SH-016; it does not
change ZapFast or claim its application-controlled phone-link order is wired.

## Contract and integration

- `ui.RichLinkKey(boxKey, spanIndex)` exports the original semantic-key
  formula unchanged. Use the full spans-slice index, **not Tag** or a link
  ordinal. Derive from `c.KeyOf(sig.ID)` when the builder derives the box key.
- Register those keys in the shell's existing `Env.TabStopKey` order. Its
  keyboard handler calls `SetFocus(key)` and `SetFocusVisible(true)`; read
  `FocusKey()` to find the current stop. Do not register unbuilt or invisible
  links. Leave macOS keyboard-navigation-off handling with the framework.
- Handle `RichSignal.Clicked` exactly as today. Enter/Space and AX press
  deliver the current tag there, without opening anything. No app-specific
  phone logic or secondary activation channel was added.
- Keep box key and span indices stable for the same content entity. Removed
  or fully clipped targets lose focus at End. Reordering a span at the same
  index reuses its identity, deliberately preserving the existing AX contract.
- `RichOpts.FocusColor` optionally overrides the span text colour for the
  keyboard-visible ring. Geometry comes from actual line/bidi range rectangles.

See [the guide](../guide/richtext.md#keyboard-focus) for lifecycle details.

## Regression evidence

Before production edits, `TestRichLinkTabAndActivation` derived the existing
private semantic key and failed on the baseline:

```text
Tab focus 0, want span 237297a7c2cdcb2d
FAIL gonativeui/ui
```

The same behavior now passes through the public key API. Added tests cover:

- mixed box/span build order, wrapping Tab/Shift+Tab, a non-link span between
  link targets, the macOS text-only preference, explicit focus, claimed Tab;
- Enter/Space exactly once, no repeat/key-up/idle-frame activation, activation
  keys claimed against other handlers, inactive-window suppression;
- disabled, removed, plain, clipped, flow-hidden and truncated-away spans;
- AX focus and focused semantics, modal confinement/occlusion/restoration;
- per-line LTR/RTL rings, no drawing change for non-visible programmatic
  focus, no ring in inactive windows, zero warm-frame allocations;
- CoreText Arabic/mixed-direction multiline layout, Metal ring pixels, and
  byte-identical pixels after clearing focus;
- a real synthetic macOS window: native Tab/Shift+Tab, Enter/Space,
  explicit span-key focus and unchanged pointer activation.

The existing rich-text pointer, cursor, AX press and selection regressions
remain in the full `ui` run; existing disabled semantic-node expectations
remain unchanged, while enabled link actions now include `ActFocus`.

## Local validation

Mac mini, Go **1.27.1**, `CGO_ENABLED=0`; build/cache/tmp outputs on SSD.
Window tests run under `caffeinate -d` and the shared
`opencode/perf/live.lock`, one test executable per hold, checking
`IOConsoleLocked = No` **inside** the hold. No external application, real
customer data or URL opening is involved.

Passed: `ui`, `widget`, `widget/base`, `widget/script`, `widget/widgettest`,
`rexui`, `examples/richtext`, `examples/notes/notes`, `cmd/rexlook/rex`,
`tools/rexcompare`; real-window `TestRealRichLinkFocusAndActivation`.
Module-wide vet/build passed for darwin/arm64, windows/amd64,
windows/arm64, linux/amd64 and linux/arm64 (excluding probes and vendored
packages, as CI does); darwin `gnui_publicapi` vet and exported doccheck passed.

`rexcompare` against the original reference: **40 passed, 0 failed**.
Baseline/candidate offscreen PNGs are byte-identical:

| Capture | SHA-256 |
|---|---|
| Rex light | `f545d6091a4fe5e8c8af14be2c239e1074aed7857a7fdea7c2595128f8f5db89` |
| Rex dark | `e8539c51bca8d39ef451194edd84ac064338c3fea0fc9c13a808bbeb866cf0d7` |
| Rich-text example, unfocused | `e9ef386e2e016fdf894391ffaa198b2f4ad15d848571ea1676fbdb13eb20bce6` |

Local artifacts: `/Volumes/SSD/dev-toolchains/build/richlink-focus/` (logs,
test executables, captures including `focused-rtl.png`/`unfocused.png`). The
reference screenshot is ignored by Git and was read from the main checkout.

## Costs and limits

Production adds one reused pointer-free **32-byte target per link** and
**24 bytes per RichText record** on 64-bit targets, using the existing box
tree, traversal, overlay scope and claimed-key activation. No new layout
boxes, native views, timers or per-frame maps. Visibility checks walk the
span's range rectangles each End; span-key resolution is linear in built
links. The rectangle scratch and target slice may grow when a new high-water
mark is reached. Both synthetic and real CoreText focused-frame tests measure
**0 allocations/frame once warm**. No new CPU/startup claim is made.

There is no automatic scroll-to-focus, stable identity across arbitrary span
reordering, or application-specific Tab-order policy. ZapFast integration and
its hosted composer hand-off remain the shell agent's work. This worker did
not delegate an independent review (recursive delegation was prohibited).
