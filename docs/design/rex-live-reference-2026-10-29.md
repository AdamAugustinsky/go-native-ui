# Rex live reference, 2026-10-29

**Goal.** Replace the single 2026-10-05 screenshot behind
[rex-benchmark.md](rex-benchmark.md) with a dated reference of Rex's live
behaviour: focus transitions, resizing, panes, keyboard navigation,
compact/comfortable density, light/dark, and the system accessibility
preferences. It informs our behavioural acceptance; it is not a matched
performance benchmark, and nothing of Rex's code, assets or material data
comes into this repository (D17).

**Status: partly observed.** The visual and interactive part is **blocked**
on this host (below). This note records only what was observed, and lists
what is owed with the exact steps that will produce it.

## Host and build

| Item | Value |
|---|---|
| Machine | Mac mini, Apple M4, 5120 × 2880 display, macOS 27.2 (26B5101f) |
| App | `/Applications/Rex Beta.app`, `com.superlogical.rex`, 0.1.0 build 1642, `LSMinimumSystemVersion` 15.0 |
| GUI binary SHA-256 | `086e129fbbdd7f8c16bc6690798399f5888088e56854ec797c8db7e633e7e08a` (matches the MacBook copy) |
| Running processes | the GUI (`Contents/MacOS/Rex`); `rex server run` from Application Support's `RexServer/Versions/Current` listening on a Unix socket under `~/Library/Application Support/rex/`, with a WebSocket origin pattern, and limits `--max-sessions 64 --max-blocks 1024 --max-windows 64 --max-panes 64`; `rex server logs --follow` |
| Shader libraries | two precompiled `default.metallib` files: the app UI's (9,712 B) and swift-ghostty's renderer's (86,612 B), both `MTLB` containers. Sizes only, for scale |
| Window (CGWindowList) | one on-screen window, layer 0, 1180 × 760 pt at (690, 355) |
| System appearance | Dark, automatic switching on |
| Accessibility preferences | Reduce Transparency, Increase Contrast, Reduce Motion, Differentiate Without Color: not set (off) |

What this adds to the architecture evidence: the server is a separate
process installed outside the app bundle under a `Versions/<build>`
layout (that it updates itself is inferred from the layout, not
observed), its resource limits are command-line flags, and shaders ship
compiled for both the UI and the terminal renderer.
Lanes 1 (compiled shaders) and 4 (session service) take these as
reference, not as specification.

## Blocked: what was not observed, and why

The coordinating session that wrote this note has neither permission
needed to watch or drive another app:

- **Screen Recording:** `screencapture -x` fails with "could not create image from display"; a window capture (`-l <id>`) fails with "could not create image from window".
- **Accessibility:** System Events refuses UI scripting: "osascript is not allowed assistive access (-1728)".

No setting was changed to work around this and no Rex settings or
account state were read. So **none** of the following was observed, and
this note claims nothing about them:

1. focus transitions between panes (dimming curve, duration, which chrome follows focus);
2. live resize of the window and of a split (minimum pane sizes, reflow, frame pacing);
3. creating, closing, moving and maximising panes;
4. keyboard navigation between panes and tabs (bindings, wrap-around, focus restoration after a close);
5. compact versus comfortable density (whether Rex has such a setting at all is unverified);
6. light versus dark, and the transition between them;
7. Reduce Transparency, Increase Contrast and Reduce Motion fallbacks.

## How to complete it

On this Mac mini, grant **Screen Recording** and **Accessibility** to the
process that runs the agent (System Settings → Privacy & Security), or run
the steps from a logged-in Terminal that has them. Then, with a disposable
Rex workspace (new window; panes running `sh -c 'while :; do date; sleep 1; done'`
and `top`; no real sessions):

1. Capture each state with `screencapture -o -l <windowid>` (window id from CGWindowList) into the gitignored `docs/design/ref/live-2026-10-29/`.
2. Record focus transitions and a live resize with `screencapture -v -l <windowid>` for 5 s each; read the dimming duration from frame differences, not by eye.
3. Toggle appearance and each accessibility preference, capture, and **restore each setting to the value in the table above** before finishing.
4. Fill in the table below and diff it against our acceptance in rex-benchmark.md's Phase 3 section.

| Behaviour | Rex (observed) | Ours (cmd/rexlook, cmd/shelldemo) | Gap |
|---|---|---|---|
| Focus dimming | owed | animated palette and fill dimming | owed |
| Split resize | owed | `base.Splitter`; workspace model (lane 2) | owed |
| Pane keyboard navigation | owed | workspace focus navigation (lane 2) | owed |
| Density | owed | surface profiles (lane 2) | owed |
| Light/dark | owed | `theme.Follow` | owed |
| Reduce Transparency / Increase Contrast | owed | opaque and high-contrast profiles | owed |
| Reduce Motion | owed | motion tokens collapse to instant | owed |
