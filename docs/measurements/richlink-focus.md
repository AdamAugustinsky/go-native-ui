# SH-016: RichText span focus (2026-10-22)

Branch `p6-richlink-focus` on `origin/main` **724afd7**: the framework part
of ZapFast shell SH-016 (keyboard focus on link spans). Decision and cost:
DECISIONS.md, "SH-016 keyboard links".

## Contract

- `ui.RichLinkKey(boxKey, spanIndex)` is the existing semantic-key formula,
  by the full spans-slice index (not Tag, not a link ordinal). Derive the box
  key from `c.KeyOf(sig.ID)` when the builder derives it.
- The shell registers those keys in its `Env.TabStopKey` order, focuses with
  `SetFocus(key)` and `SetFocusVisible(true)`, and reads `FocusKey()`. Only
  built, visible links are registered.
- Enter/Space and AX press deliver the current tag through
  `RichSignal.Clicked`, as a click does.
- Box key and span indices stay stable per content entity; removed or fully
  clipped targets lose focus at End. `RichOpts.FocusColor` overrides the
  ring's colour.

See [the guide](../guide/richtext.md#keyboard-focus) for the lifecycle.

## Evidence

`TestRichLinkTabAndActivation` failed on the baseline (`Tab focus 0, want
span 237297a7c2cdcb2d`) and passes through the public key. The tests cover
Tab order and wrapping, the macOS text-only preference, Enter/Space once,
disabled/removed/clipped/hidden/truncated spans, AX focus, modal scope,
per-line LTR/RTL rings, CoreText multiline layout, Metal ring pixels, and a
real macOS window (`TestRealRichLinkFocusAndActivation`). Focused frames
allocate **0 once warm**.

Validation (Mac mini, Go 1.27.1, `CGO_ENABLED=0`): `ui`, `widget/...`,
`rexui`, `examples/richtext`, `examples/notes/notes`, `cmd/rexlook/rex`,
`tools/rexcompare` pass; vet/build for darwin, windows and linux targets.
`rexcompare` **40/40**; unfocused captures are byte-identical to the
baseline:

| Capture | SHA-256 |
|---|---|
| Rex light | `f545d6091a4fe5e8c8af14be2c239e1074aed7857a7fdea7c2595128f8f5db89` |
| Rex dark | `e8539c51bca8d39ef451194edd84ac064338c3fea0fc9c13a808bbeb866cf0d7` |
| Rich-text example, unfocused | `e9ef386e2e016fdf894391ffaa198b2f4ad15d848571ea1676fbdb13eb20bce6` |

Artifacts: `/Volumes/SSD/dev-toolchains/build/richlink-focus/`.

## Deferred field focus

CI **39726955876** (`1e04d5e`) failed Pilot's deferred first-open field focus
(`TestFilterButtonAndSidebarToggle`: `filter open true, focus 0`) and
`TestFocusRingsComeFromTheKeyboardExceptTextEntry`: the missing-target
cleanup dropped a focus key whose field is built on the next frame.
`TestRichLinkCleanupPreservesDeferredFieldFocus` reproduces it. The cleanup
now applies only to a key known to be a rich span (one key of history on
Context); other pending keys keep their old semantics. The Pilot suite, the
live Pilot test and `TestRealRichLinkFocusAndActivation` pass; rexcompare
stays 40/40 with the same hashes.

## Limits

No scroll-to-focus, no identity across span reordering, no app Tab-order
policy. ZapFast's wiring is the shell's.
