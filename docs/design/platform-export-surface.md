# What package platform exports (proposal for #37)

Status: proposal, nothing is renamed or moved by it. It answers the item "`platform` exports a large surface that apps shouldn't need; decide what is public (relates to D53, `gnui_publicapi`)" in [#37](https://github.com/AdamAugustinsky/go-native-ui/issues/37).

## The surface today

Measured on main (a0c74d2) by parsing the module (top-level identifiers of `platform`, and where they are used outside it; a throwaway program, not committed):

- `platform` has 118 non-test files and **624 exported top-level identifiers**, plus 259 methods on exported types (`Window` has 187, `Editor` 44).
- Packages that import it (non-test code, outside `probes/`): 36. 19 are the framework (`app` and its eight service packages, `ui`, `gpu`, `img`, `media`, `theme`, `widget/base`, `widget/script`, `widget/widgettest`, `rexui`, `rexui/catalog`); 17 are programs and tools (14 under `cmd/`, one example, two under `tools/appkitref`).
- **Used by programs under `cmd/`, `examples/` and `tools/`: 56 identifiers** (11 only by them: the alert and button styles, `BackdropGlass`/`BackdropNone`, `HostButton`, `HostOptions`, `HostView`, `OpenTimeline`; 45 also by the framework: `Window`, `Event` and its `Ev*` kinds, `Key*` and `Cmd*` constants, the dialog specs and results, `SystemAppearance`, `Now`, `Wake`, `Open`, `Options`).
- **Used only by the framework: 363.** **Used by nothing outside `platform`: 205** (the `AX*` accessibility roles and actions, the `Area*` materials, `Cursor*`, `Max*` limits, `Err*` values, the app-event sources, debug counters such as `DebugPumpState`, `LiveWindowObjects`, `ShellCounts`).

An app written against `app` and `widget` almost never names `platform`. The 56 are what a program that drives a window or a dialog by hand reaches for, and the rest is plumbing between `app` and the OS layer that happens to be exported because Go has one visibility level between packages.

## Why it matters

- Everything exported is something an app can depend on, and the framework then has to keep. The private-API discipline of D53 is about what the binary contains; this is about what the source promises.
- godoc, completion and agents see 624 names, 568 of which an app should not use.
- `Window` is the largest piece, with 187 methods; which of them an app may call directly, rather than through `app.Win`, is part of the decision.

## Options

**A. Keep one package, mark the tiers.** A doc convention (`// Framework:` first line) and a check in `tools/doccheck` that every exported name of `platform` is in `docs/design/platform-api.txt` as `app` (public) or `framework`. No code moves. It makes the surface visible and stops it from growing unnoticed, but promises nothing to the compiler.

**B. Move the framework's half to `internal/platform`, keep a small `platform`.** Go's `internal` rule makes `gonativeui/internal/...` importable by every package of the module and by nothing outside it, which is exactly the line wanted. `platform` stays as a facade of type aliases and wrappers for the 56 names above (plus whatever the review adds), and the OS layer, the 363 and the 205 live in `internal/platform`. Cost: a `git mv` of 194 files and a rewrite of the import in 36 packages and their tests (mechanical, done with `gofmt -r` or `gopls rename`), build tags and `gnui_publicapi` file names unchanged. Risk: the facade's aliases must not hide a type's methods (aliases keep them), and the ones that are functions need a wrapper or a `var f = internal.F`, which costs an indirection on hot paths (`Now`, `Wake`): those are kept as wrappers the compiler inlines.

**C. Split by area into several packages** (`platform/ax`, `platform/dialog`, `platform/material` …). The cleanest to read, and the largest change: the areas share `Window`, so they would have to import each other or the core in a cycle-free order that does not exist today.

## Recommendation

Do **A** first, in one small change: it needs only the list. Use it to make the decision on B concrete, with the 56 reviewed one by one (do the dialog specs and `Window` belong behind `app` instead?). Do **B** as its own branch when no other branch is moving `platform` files (every lane that touches `platform/` conflicts with the `git mv`), in two commits: the move with the imports rewritten and nothing else, then the facade. C only if B shows that a tier of the facade (the dialogs, say) wants its own package.

Not decided here: whether `gnui_publicapi` should also remove exported names (it removes private selectors today); whether `widgettest` and `tools/appkitref`, which use 24 and some names, count as apps (they are test and tooling code of the module, so under B they would import `internal/platform`).
