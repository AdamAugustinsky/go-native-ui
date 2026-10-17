# Phase 4 step 2: text editing (caret clusters, text field, clipboard, IME)

**Machine:** Mac mini (Apple M4, macOS), over SSH, no display session for the IME. Everything below that needs a real input method, a second OS or a human is listed under "Needs a human" at the end. Windows and Linux code compiles and vets for amd64 and arm64 here; their tests run in CI.

## What was built

| Layer | Files | Purpose |
|---|---|---|
| `text` | `grapheme.go`, `unidata.go`, `caret.go`; clusters in `cache.go`, `coretext_darwin.go`, `dwrite_shape_windows.go`, `shape.go` | cluster info per glyph; `CaretX`, `HitByte`; grapheme segmentation |
| `ui` | `textbuf.go`, `textfield.go` | `TextBuffer`, `Context.TextField`, caret blink, IME rectangle, preedit display |
| `platform` | `clipboard_{darwin,windows,linux}.go`, `ime_windows.go`, `platform_darwin.go`, `platform.go` | `Clipboard`/`SetClipboard`, `EvPreedit`, `SetIMERect`, `SetTextInput`, `NSTextInputClient` |
| `app` | `input.go`, `frame.go`, `regions.go`, `app.go` | preedit and lossless text in the translator, clipboard wiring, IME rectangle, caret-blink wake-up |

## Exact new API

### `text`

```go
// RunView gained two fields; all backends fill them.
Clusters []uint32 // per glyph: byte offset in Text of the first character of its cluster; bit 31 (text.ClusterRTL) = right-to-left glyph
Text     []byte   // the shaped source string, aliasing the run cache
const ClusterRTL = 1 << 31

func (v RunView) CaretX(off int) float32   // x of the caret at a byte offset (clamped; inside a cluster snaps back to its start)
func (v RunView) HitByte(x float32) int    // byte offset of the caret stop nearest x (always a grapheme boundary)

func IsGraphemeBoundary(b []byte, off int) bool
func NextGrapheme(b []byte, off int) int   // first boundary after off, len(b) at the end
func PrevGrapheme(b []byte, off int) int   // last boundary before off, 0 at the start
func GraphemeStart(b []byte, off int) int  // start of the cluster containing off
```

### `ui`

```go
type TextBuffer struct{ Max int; /* private: bytes, caret, anchor, scroll, blink origin, drag mode */ }
func (t *TextBuffer) Bytes() []byte;  String() string;  Len() int;  Rev() uint32;  Reset()
func (t *TextBuffer) SetString(string); SetBytes([]byte); Insert([]byte); InsertString(string)
func (t *TextBuffer) Caret() int; Selection() (lo, hi int); HasSelection() bool; Selected() []byte
func (t *TextBuffer) SetSelection(anchor, caret int); SetCaret(int); SelectAll()

func (c *Context) TextField(label string, buf *TextBuffer) TextFieldSignal
type TextFieldSignal struct {
	ID ID; Signal               // the field box and its pointer signal
	Focused, Changed, Submitted, Cancelled bool
}
func (c *Context) IMERect() (r [4]float32, ok bool)  // window px, focused field's caret or composition
func (c *Context) WakeAfter() int64                  // ns after Input.Time at which a frame is wanted (caret blink); 0 = none
func (c *Context) IMECancel() bool                   // the composition in Input.Preedit is orphaned: discard the platform's (review fix 4)

type TextEditStyle struct{ Selection, Caret uint32; BlinkHalfNs, BlinkTimeoutNs int64 }
Context.TextEdit TextEditStyle                       // defaults: 0x2D6BFF55, text colour, 500 ms, 10 s
Context.ClipboardGet func() string; ClipboardSet func(string)

// Input gained:
Time int64                                          // platform.Now at frame start (0 = no clock: solid caret)
Preedit [MaxPreedit]byte; NPreedit int; PreeditSel [2]int   // composition: state, not per frame
func (in *Input) SetPreedit(p []byte, selStart, selLen int)
const MaxPreedit = 256
```

### `platform` (same on every OS)

```go
func Clipboard() string            // plain UTF-8, \n line ends; main thread
func SetClipboard(s string)
EvPreedit                          // Text = composition ("" = ended); SelStart, SelLen = caret/selection in it (bytes)
func (*Window) SetIMERect(x, y, w, h float32)   // window px, y down; copies, no-op when unchanged
func (*Window) SetTextInput(active bool)        // false cancels a composition in progress
func (*Window) DiscardComposition()             // cancel a composition without committing it (EvPreedit "" follows, no EvText)
```

`app.Run` wires `ui.Context.ClipboardGet/Set` to the platform, sets `Input.Time` each frame, calls `SetIMERect` / `SetTextInput` from `ui.IMERect()` after every frame (and `DiscardComposition` when `ui.IMECancel()`), and arms a wake-up from `ui.WakeAfter()`.

## Design decisions

### Cluster API

* **Per glyph, pointer-free, cached with the run.** `Clusters` is one `uint32` per glyph in the same slabs as `X`/`Faces` (the run cache compacts them with the rest), plus a slice of the cached source text. No per-frame allocation (`TestCaretAllocs`, `TestGraphemeAllocs`, and 0 mallocs in the typing tests below).
* **Shaper clusters are refined to grapheme clusters at query time**, not at shape time. Shape time must stay cheap and allocation-free for lists with millions of names that never need a caret. Backends report what they know: CoreText gives a combining mark its own string index, HarfBuzz merges per grapheme, DirectWrite maps text positions to the first glyph of a cluster. `CaretX`/`HitByte` snap every glyph's cluster to the grapheme containing it, merge neighbouring glyphs of one grapheme into a group, and divide a group that stands for several graphemes (a ligature: "fi") evenly. Result: caret stops are exactly grapheme boundaries on every backend.
* **RTL:** `ClusterRTL` marks glyphs of right-to-left runs (CoreText `CTRunGetStatus`, HarfBuzz direction, DirectWrite bidi level). Glyphs are in visual order, so a group's logical start is its right edge. A boundary between runs has two logical positions at one x; the caret is drawn where the *following* character starts, and the end of the text at the trailing edge of the logically last group. `HitByte` returns the offset of the group the point is in.
* **Pen positions are not monotone** (a mark can sit left of its base), so cell extents use the running maximum of `X`.
* **Invalid UTF-8:** each invalid byte is one U+FFFD character on all backends (CoreText used to collapse runs with `strings.ToValidUTF8`; now a per-byte replacement so UTF-16 indices map back exactly).
* **Grapheme segmentation:** own UAX #29 (Unicode 17, with GB9c Indic conjuncts, GB11 emoji ZWJ, GB12/13 flags, Hangul) on a verbatim copy of go-text's property tables (`unidata.go`, 5.4 KB), applied statelessly at an offset so any position can be queried without scanning from the start. `TestGraphemeMatchesGoText` compares 30,000 random sequences with go-text's segmenter. One deviation found: go-text v0.3.5 fails to start a new GB11 sequence at a second pictographic character (it breaks "😀 ZWJ 👨"); the spec rule and this code join them. Cost: `CaretX` 6.5 µs for a 100-glyph run, 27 µs for 1000 (M4), `HitByte` 1.1 µs / 9.3 µs, 0 allocs.

### Text field

* **State lives in `TextBuffer`**, owned by the app, so the field widget keeps no hidden state besides what `ui` boxes hold: one slice, caret/anchor (grapheme-aligned byte offsets), scroll, blink origin, drag mode. Growth only past the high-water mark.
* **Boxes:** the field is a `Clickable|Focusable|Clip` box (styled by the usual `Next…` stacks; width defaults to the parent's, height to line + padding); inside, a clipped content box holds the text, the selection quad, the composition underlines and the caret quad. The caret is a 1-pixel (device) quad, not a glyph.
* **Input order:** mouse, then typed text, then keys within the frame. Typed text goes first so "ab⌫" in one frame deletes the b.
* **Keys** (⌘ = primary on macOS, Ctrl elsewhere): arrows by grapheme, ⌥ (macOS) / Ctrl word moves (Windows convention for Ctrl+→: start of next word; macOS: end of word), ⌘←/→/↑/↓ and Home/End, Shift extends, Backspace/Delete by grapheme, with word modifier by word, ⌘⌫ to line start, primary+A/C/X/V, Enter/Esc as `Submitted`/`Cancelled`. Words: letters, digits, marks and `_`; spaces and punctuation separate (CJK runs are one word).
* **Mouse:** click places, shift-click extends, drag selects (autoscrolls while the pointer is outside, faster the farther, requesting frames), double click selects the word and drags by words, triple click selects all.
* **Single-line text:** line breaks and tabs become one space (CRLF one), other controls and invalid UTF-8 are dropped; `TextBuffer.Max` caps the size and cuts at the first character that does not fit.
* **Not done:** undo/redo; the macOS Emacs bindings (Ctrl+A/E/K..., swallowed by `doCommandBySelector:`); visual (bidi-aware) arrow movement; placeholder text; password mode.

### Caret blink vs. the idle budget (exit criterion 6: 0 presents, ≤ 1 wakeup/s)

A blinking caret costs a frame per transition. Decision: **blink with a 500 ms half-period only while the field has focus in an active window, and only for 10 s after the last interaction (typing, moving, clicking, focus arrival); then the caret stays solid and the app returns to 0 wakeups.** The field does not animate: it reports the time of the next transition through `Context.WakeAfter()` and the app's `Wait` timeout is shortened to it (`App.wakeAt`), so between transitions the loop sleeps. Resulting budget:

| State | Wakeups per second |
|---|---|
| no field focused, window inactive, or >10 s since the last interaction | 0 |
| focused and interacted within 10 s | 2 (one per caret transition) |

That exceeds "≤ 1 wakeup/s" during the 10 s after interaction. Strictly within the budget at all times: set `TextEdit.BlinkHalfNs = 1e9` (2 s period, 1 wakeup/s) or `TextEdit.BlinkTimeoutNs = 0` (solid caret, 0). **Your call for Pilot**; the idle measurement must either wait the 10 s out or use one of the two settings. Tests: `TestTextFieldBlink`, `TestWakeAtShortensTheWait`.

### Clipboard

| OS | Implementation | Status |
|---|---|---|
| macOS | `NSPasteboard generalPasteboard`, `stringForType:`/`clearContents`+`setString:forType:` with `public.utf8-plain-text`, in its own autorelease pool, selectors cached | round trip tested on arm64 and amd64 (restores the clipboard; skips when the pasteboard is unreachable) |
| Windows | `OpenClipboard` (retries 10×5 ms), `GetClipboardData(CF_UNICODETEXT)` / `GlobalAlloc`+`SetClipboardData`; `\r\n` ↔ `\n` | compile and vet only (no Windows machine here); not in CI as a test yet |
| Linux | **process-local fallback**: copy and paste work inside the app, nothing is exchanged with other applications | Wayland `wl_data_device` and the X11 `CLIPBOARD` selection were **not** implemented: both need a compositor/X server to test, and writing protocol code blind in the event loops risks the platform layer. The design notes are in `clipboard_linux.go` |

### macOS IME flow

1. `keyDown:` → `interpretKeyEvents:` (unchanged). The platform pushes the key's `EvKey` first, remembers its queue index, then lets AppKit run.
2. `setMarkedText:selectedRange:replacementRange:` → `EvPreedit` (UTF-8 text; selection converted from UTF-16 units to bytes; `NSNotFound` clamps to the end). `insertText:` while composing → `EvPreedit` (empty) then `EvText`. `unmarkText` with marked text → empty `EvPreedit` + the text as `EvText` (AppKit abandoning a composition leaves the text). `hasMarkedText`, `markedRange` (`{0, len}`, the app has no document positions), `selectedRange` (caret inside the marked text), `validAttributesForMarkedText` (empty), `attributedSubstringForProposedRange` (nil), `characterIndexForPoint` (`NSNotFound`) are answered.
3. **Key suppression:** after `sendEvent:` the queued `EvKey` is dropped if the input method was composing before the key or took it (set/unmark/commit) and did not hand it back with `doCommandBySelector:`. So composition keys, and the Enter that commits, are not key presses; an arrow or Enter with nothing composing is.
4. `firstRectForCharacterRange:actualRange:` returns the rectangle the app gave `SetIMERect`, converted from window pixels (y down) to screen points with AppKit (`convertPoint:toView:nil` then `convertPointToScreen:` on the lower-left corner; see review fix 18); the range is ignored. `SetIMERect` invalidates the input context's coordinates.
5. The app calls `SetIMERect` from the focused field's caret or composition rectangle after every frame (only changes reach AppKit), and `SetTextInput(false)` when no field has focus (and `DiscardComposition` when ui reports the composition orphaned): a composition in progress is forgotten, then `discardMarkedText` is sent, so an `unmarkText` answer finds nothing to commit.
6. Tested on the real view class: the NSTextInputClient methods are sent through `objc_msgSend` into the ffi callback, both architectures (`platform/ime_darwin_test.go`); the app-level flow with synthetic events (`app/text_test.go`).

**Windows:** `WM_IME_SETCONTEXT` hides the default composition window; `WM_IME_COMPOSITION` (handled without `DefWindowProc`, so no `WM_IME_CHAR`) reads `GCS_COMPSTR`/`GCS_CURSORPOS`/`GCS_RESULTSTR` with `ImmGetCompositionStringW` into `EvPreedit`/`EvText`; `ImmSetCompositionWindow`/`ImmSetCandidateWindow` place the candidate list from `SetIMERect`; `SetTextInput(false)` / `DiscardComposition` cancel with `ImmNotifyIME`. Keys the IME processes arrive as `VK_PROCESSKEY` and are dropped (no `EvKey`, down or up); a `WM_IME_COMPOSITION` that only moves the cursor republishes the composition. Compiled and vetted for amd64/arm64 only: **untested with a real IME**.

**Linux:** not implemented (`SetIMERect`/`SetTextInput` are no-ops). Next step: `zwp_text_input_v3` on Wayland (enable on focus, `set_cursor_rectangle`, `preedit_string`/`commit_string`/`done` mapped to `EvPreedit`/`EvText`), XIM or an IBus/fcitx client on X11.

### Text events and Input

`Input.Text` still holds 64 bytes per frame, but the translator no longer drops the excess: what does not fit (whole characters only) is carried to the next frame in order, behind which later events wait; a long IME commit or a paste of typing bursts arrives over several frames. `AppendText` and `SetPreedit` cut at character boundaries. The composition is persistent `Input` state (256 bytes).

## Tests and results

All run on this machine, `CGO_ENABLED=0`:

| Command | Result |
|---|---|
| `go test -count=1 ./text/ ./ui/ ./platform/ ./app/` (arm64) | ok |
| same, `GOARCH=amd64` (Rosetta) | ok |
| `go test -count=1 -tags textengine ./text/` (arm64 and amd64) | ok (go-text engine on darwin) |
| `go vet` of `./text ./ui ./platform ./app` for linux/windows/darwin × amd64/arm64 | clean |
| `go vet -tags textengine ./text/` (darwin, windows) | clean |
| `gofmt -l text ui platform app` | clean |

New tests: cluster/caret checks (`cluster_shared_test.go`: 22 cases and properties — monotone carets, snapping inside graphemes, `HitByte(CaretX)` round trips, emoji ZWJ/flags/skin tones/Hangul jamo/Devanagari conjunct as single stops, CJK clusters, ligature split, invalid UTF-8, Arabic/Hebrew/mixed/digits-in-RTL, empty) run on CoreText (with Hoefler Text for a real "fi" ligature) and on go-text (Roboto + Amiri); the DirectWrite instance (`TestDWriteClusters`) compiles and runs in CI; hand-built views pin the geometry (`caret_test.go`); grapheme conformance (`grapheme_test.go`); text field editing (`ui/textfield_test.go`, 16 tests: typing, controls, `Max`, caret moves on macOS and Windows conventions, deletion by grapheme/word, clipboard, mouse click/shift/drag/double/triple, ligature carets, drawing, scroll, drag autoscroll, preedit, blink, defaults); IME on the real view class; translator/ui end to end on the offscreen renderer.

**Zero allocations:** `TestTextFieldZeroAllocs` — 600 frames of typing (ASCII, é, "fi", CJK, spaces), arrows, Shift+arrows, ⌥-moves, Backspace, End, clicks, drags and composition: 0 allocs/frame. `TestRealTextFieldTyping` — the same with the real CoreText system and the offscreen Metal renderer, run-cache reserved to its limits, 900 measured frames: **0 mallocs, 0 bytes** (the first of five windows showed 4 mallocs / 3 KB of runtime noise, the second 0; without `ReserveRuns` the run cache slab doubles once per ~250 KB of typed strings, as for any new text). Benchmarks: `CaretX` / `HitByte` above.

## Needs a human (MacBook)

1. **Japanese Hiragana IME** (Kotoeri/"Hiragana"): in a focused field type `nihongo` — underlined `にほんご` at the caret; Space converts and the candidate window appears directly *below the composition* (check on Retina, near the window's bottom edge, and on a second display); ↑/↓/Tab among candidates; ←/→ and Shift+←/→ change clauses (the heavier underline follows); Enter commits (the field gets the text, no Submitted); Esc cancels; Backspace edits the composition. Type a second composition immediately after a commit.
2. **Other input methods:** Pinyin (candidate window, Space/number selection), Korean 2-set (each syllable stays marked until the next one starts).
3. **Focus changes while composing** (review fix 4; decision: *commit into the field that showed it*): compose `にほん` in Pilot's left path field, then click the right path field (and the same with Tab, and with a click on a list row): the left field keeps `にほん` as typed text, the right field stays empty, shows no underline, its selection is intact, and typing in it afterwards works at once (the first key must not be eaten or leak `にほん`). Switching apps or closing the window while composing must leave nothing stuck.
4. **Keys are not leaked:** while composing, arrows/Enter/Backspace/Esc must not move the caret, submit or delete in the field or trigger list navigation.
5. **Press-and-hold accents** (hold `e`, pick é) and dead keys (⌥e, e): AppKit may send `insertText:replacementRange:` replacing the first `e`; the replacement range is ignored, so this may produce `eé`. **Not fixed** (see Open issues); please report what happens (`eé`, `é`, no popover).
5a. **Selection and composition** (fix 5): select the whole path, start composing: the selection is replaced on screen by the underlined composition; Esc (cancel) brings the original text and selection back; committing replaces it. Same with a partial selection.
5b. **Candidate window position** (fix 18): with Pilot's full-size-content titlebar window, the candidate window is under the composition on a Retina and a non-Retina display and when the window sits partly off a screen edge (conversion now goes through AppKit; unit-tested on a real offscreen NSWindow only).
5c. **Blink with an unfinished composition** (fix 9): leave a composition untouched for 10 s: the caret stops blinking (no wakeups) and wakes again with the next key.
6. **Clipboard with other apps:** ⌘C in Pilot → paste in TextEdit and the reverse, with emoji, Japanese and a long line; ⌘X; ⌘A; Edit-menu-less ⌘Z does nothing (no undo).
7. **Editing feel:** ⌥←/→, ⌘←/→, ⌥⌫, ⌘⌫, Shift variants, double/triple click, drag selection past the field edge (autoscroll speed), I-beam cursor, selection colour on your display, caret thickness at 1x and 2x, caret blink cadence and that it stops after 10 s.
8. **Idle wakeups** with a focused field: 2 per second for 10 s, then 0 (Activity Monitor "Idle Wake Ups" or `sudo powermetrics`), versus exit criterion 6.
9. **Emoji & Symbols picker** (Ctrl+⌘Space) inserts text into a focused field.

On Windows (CI cannot do these): the real IME (Microsoft Japanese/Pinyin) flow and candidate window position, `WM_IME_COMPOSITION` reading, clipboard round trip with other apps, and the DirectWrite cluster order for right-to-left text (`TestDWriteClusters` runs in CI and will show it).

## Open issues and notes

* **Linux:** clipboard is process-local; no input method. Both documented in code.
* **Windows IME and clipboard are unexercised** (compile/vet only). The DirectWrite cluster mapping assumes, as the existing shaper does, that glyph arrays of right-to-left items are in logical order.
* `NOTICE` says no third-party source is copied; `text/delims.go` (existing) and now `text/unidata.go` are copies from go-text/typesetting (Unlicense/BSD-3). `NOTICE` is outside this step's write set: it should list them.
* **Press-and-hold replacement is not implemented.** `insertText:replacementRange:` ignores the range. A contained fix does not exist: AppKit computes the range from `selectedRange` and `attributedSubstringForProposedRange`, which the app cannot answer truthfully (it has no document), so it would need a fake caret position reported to AppKit (e.g. `selectedRange` = {1<<20, 0}), translating `replacementRange` into a "replace the previous N UTF-16 units" field on the text event, `Input.ReplaceBack` applied by the translator/ui before the frame's text (with the carry/overflow logic), and verification with the real accent popover (the approach libsdl-org/SDL#3622 outlines). Nothing of this was built blind.
* `Window.SetTextInput` only cancels compositions. An active macOS input method still composes when no field is focused, and the keys it takes produce no `EvKey` — a Pilot that types-to-filter with the list focused should focus the filter field when it sees `Input.NPreedit > 0`.
* `attributedSubstringForProposedRange` returns nil, so reconversion (selecting text and pressing the Kana key) is not supported.
* Text typed and keys pressed in one frame lose their relative order (text applies first).
* `CaretX` is O(glyphs) per call (27 µs at 1000 glyphs); a field with tens of thousands of characters would want a cached caret table.
* The idle `Input.Time` clock is 0 in `RenderOffscreen`/`NewOffscreen`, so offscreen renders show a solid caret and never ask for wake-ups (deterministic CI screenshots).

## Review fixes (second pass, after 9b9795d)

Each item: the test that failed first, then the fix. Nothing committed.

| # | Fix | Test(s) |
|---|---|---|
| 4 | **Composition stays with its field.** `Context.imeOwner` (the field that started the composition, reset when `Input.NPreedit` is 0) and `Context.IMECancel()`. A field that did not start the composition does not show or commit it; the field that showed it and lost the focus (`!focused`, with `buf.composing`) commits it (`Insert`) — AppKit's convention for a first-responder change, and it keeps what the user typed. Orphaned composition (owner gone, or focus on nothing): `IMECancel`; `app.syncText` calls `Window.DiscardComposition` and clears the translator's preedit; the platform discards without committing again (macOS forgets the marked text *before* `discardMarkedText`, so a late `unmarkText` commits nothing). A window that is merely inactive keeps its composition. | `ui`: `TestIMECompositionStaysWithItsField` (two fields, lingering preedit, typing afterwards, new composition in the second), `TestIMEOwnerGone`, `TestIMEFocusLeavesAllFields`; `app`: `TestTextFieldThroughTranslator` (discard once, translator cleared, late empty preedit ignored); `platform`: `TestIMEDiscardComposition` (real view class) |
| 5 | **Selection is provisional while composing.** The composition is displayed in place of the selection (text before + preedit + text after) but the buffer is untouched until the commit (`Insert` already replaces the selection). Cancel, focus discard and window deactivation therefore lose nothing. | `TestIMESelectionSurvivesCancel`, `TestIMESelectionSurvivesWindowDeactivation`; `TestTextFieldPreedit` adapted (it pinned the old deletion) |
| 7 | **AltGr.** `altGrIsCtrlAlt` (Windows): Ctrl+Alt is neither the primary nor the word modifier in text fields (macOS ⌥ and Linux unaffected). Pilot's `isPrimary` has the same rule; it could call a shared `ui` helper later (not edited: `cmd/` is out of scope). | `TestTextFieldAltGrTypesOnWindows` (AltGr+A with "ą": typed, not selected; next key appends; AltGr+←/⌫ are not word moves; Ctrl still is; no rule elsewhere) |
| 8 | **Grapheme alignment after edits.** `TextBuffer.replace` moves the caret onto a boundary: forward after an insertion (past the cluster the typed text joined), back after a deletion (no jump over untouched text). Anchor follows. | `TestTextBufferCaretStaysOnBoundaries` (accent after typing before an initial mark, ZWJ between emoji, flag letters and parity shifts, deleting between base and mark / inside ZWJ and flag sequences), `TestTextFieldBackspaceAfterJoin` |
| 9 | **Blink reset by a persistent preedit.** Activity is refreshed only when the composition starts or changes (FNV-1a of text + caret in `TextBuffer.preHash`), not on every frame it is shown. | `TestIMEPersistentPreeditLetsBlinkTimeOut` (blink-only frames for 10 s, then a changed text and a moved caret restart it) |
| 10 | **VK_PROCESSKEY.** `winKey` (new `platform/keys_win.go`, no build tag so it is tested everywhere; tables moved there) returns ok=false for `VK_PROCESSKEY`, down and up, before scancode mapping; `wndProc` returns without an `EvKey`. | `TestWinKeyProcessKeyIsDropped` (letters, digits, Enter, extended), `TestWinKeyTranslation` (mapping unchanged). **Not a window-proc-level test:** there is no Windows window harness; the helper is the whole decision, and the test runs on Windows CI too. |
| 11 | **Cursor-only composition updates.** `compositionChanged(flags)` = `GCS_COMPSTR` or `GCS_CURSORPOS`; the handler republishes the current composition with the new caret (`compositionCaret`); empty string ends the composition only when `GCS_COMPSTR` is set. Pure helpers in `platform/ime_win.go` (no build tag). | `TestCompositionUpdates` (flags, surrogate-pair carets, clamp). The Win32 call path itself is still untested with a real IME. |
| 17 | **Invalid continuation bytes.** `runeBoundary` replaces `utf8.RuneStart`: a continuation byte is not a boundary only inside a valid sequence starting up to 3 bytes before it (as `DecodeRune` walks, as the shapers see it); every invalid byte is a stop. | `TestGraphemeMalformedUTF8` (0x80 runs, truncated, overlong, encoded surrogates, above U+10FFFF, 5-byte forms, mixed with marks; boundaries compared with go-text on the sanitized text; Next/Prev/GraphemeStart walks), `TestGraphemeRandomBytes` (20,000 byte strings), and six malformed cases added to the shared backend checks, which now assert that every invalid byte is a caret stop (failing on CoreText before the fix; passing on CoreText and go-text after) |
| 18 | **IME screen coordinates.** `imeScreenRect` converts the rectangle's lower-left corner with `convertPoint:toView:nil` and `convertPointToScreen:` (view bounds height for the flip) instead of window + view frame origins. | `TestIMERectScreenConversion`: a real, never-shown NSWindow at (300,200) built on the main thread (`platform/main_darwin_test.go` locks the main goroutine and `onMain` runs AppKit work there): full-size content view (Pilot/Rex style), plain titled window, a view nested two levels deep at an offset; scales 1 and 2. Skips with a message when no window can be created. The old formula failed the nested case (and my rig left its `hPt` unset, which failed the others too). |

macOS IME review (same classes of issue): UTF-16 ↔ byte conversions (`u16ToByte`, `utf16Len`) are now pinned by `TestUTF16ByteConversions` (surrogate pairs count two, an offset inside a pair rounds up, clamping); `setMarkedText` clamps `NSNotFound` and over-long ranges; `markedRange`/`selectedRange` report UTF-16 units of the marked text. Left as is: `replacementRange` (see Open issues), `attributedSubstringForProposedRange` (nil), an NSString with unpaired surrogates (UTF8String fails, the composition would read as empty).

Validation after the fixes: gofmt clean; vet ×6 (+ `-tags textengine` darwin/windows) clean; `go test -count=1 ./text/ ./ui/ ./platform/ ./app/` ok on arm64 and amd64 (Rosetta); `-tags textengine ./text/` ok on both; `go build ./...` ok; `go test ./cmd/...` ok; `TestTextFieldZeroAllocs` 0 allocs and `TestRealTextFieldTyping` 0 mallocs (second attempt, as before).
