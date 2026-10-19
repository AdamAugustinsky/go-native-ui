# Rex design system and component library: a proposal

**Asked (user, 2026-10-18).** "We have probably not done a good job in making
a theme out of the RexLook app, and we probably should also do like
shadcn-style library of reusable components/design-system out of the rex-look
thing." Also: "specially on macos, apps should feel native and fast."

**Builds on** `native-audit.md` (the problems) and the AppKit 27.2
measurements of `tools/appkitref`. Values marked **ref** are measured. Values
marked *designed* are a choice, because offscreen rendering cannot show them
(see the audit's last section).

**Hard requirements kept:**

- zero allocations per frame;
- no cgo in framework code;
- no third-party code or assets;
- every component keyboard-operable, accessible, in light and dark, with a disabled state (D28).

## 1. The idea in one paragraph

Rex is two things that the current `theme` mixes up:

- an **identity**: the gradient field, glass panes with layered shadows and hairlines, the tab capsule in the titlebar, focus dimming, and the terminal palette;
- a set of **controls**, which the Rex reference never showed.

The design system keeps the identity as Rex's own tokens. It takes the controls
from the platform: their sizes, type, fills, selection and focus are the
macOS measurements, and their colours are the live system semantic colours.
Components are shadcn-style:

- **Headless behaviour** in `widget`: hit testing, keyboard, focus, semantics and state.
- **Styled recipes** in a new package `rexui`. Each component is one small file that reads only tokens, in variants and sizes, and an app can copy it and own it.
- **Native hosting** where the OS does more than drawing: menus, file panels, alerts, and the chat composer's text view.

## 2. Tokens

Tokens live in `theme` as plain values (no pointers, no interfaces). They are
resolved to pixels and fonts once per scale change (`Theme.Resolve`, as
today) and mixed between light and dark per frame (`Resolved.Mix`, as
today).

### 2.1 Colour roles

The `Palette` becomes an indexed array, `[NumRoles]uint32` behind a
`Role uint8`, with named accessors kept for compatibility. Recipes can then
*name* a role in a table, and the crossfade is one loop.

Each role has a **source**:

- **System:** read live from `NSColor` in the window's appearance through `platform`, like the accent (D32). It updates on appearance, accent, highlight and contrast changes.
- **Rex:** the theme's own value.

On Windows and Linux every source is Rex, with values chosen to match.

| Role | Source on macOS | AppKit light / dark (ref) | Rex value (light / dark) | Used by |
|---|---|---|---|---|
| `Window` | System `windowBackgroundColor` | `#FFFFFF` / `#1E1E1E` | field `#E7E3E7` avg / `#18191F` | window behind content |
| `Field` (Rex) | Rex | — | the four-corner gradient | Rex chrome only |
| `Surface` | Rex | (`controlBackgroundColor` `#FFFFFF` / `#1E1E1E`) | `#FFFFFF` / **`#1E1E1E`** (was `#2A2C3A`) | panes, cards, tables |
| `SurfaceRaised` | Rex | — | `#FFFFFF` / `#2A2A2A` | menus (drawn), popovers, sheets |
| `UnderPage` | System | `#F6F6F6` / `#282828` | — | behind documents |
| `Label` | System `labelColor` | `#000000D8` / `#FFFFFFD8` | `#1E2228` / `#ECEDF3` | body text |
| `Label2` | System `secondaryLabelColor` | `#0000007F` / `#FFFFFF8C` | `#5A585E` / `#A9ACBB` | secondary text |
| `Label3`, `Label4` | System tertiary / quaternary | `#00000042`, `#00000019` / `#FFFFFF3F`, `#FFFFFF19` | — | placeholders of icons, disabled glyphs |
| `Placeholder` | System `placeholderTextColor` | `#0000007F` / `#FFFFFF8C` | | field placeholder |
| `TextDisabled` | System `disabledControlTextColor` | `#0000003F` / `#FFFFFF3F` | | disabled labels |
| `Fill1` … `Fill4` | System fill, secondary, tertiary, quaternary | `#00000019`, `#00000014`, `#0000000C`, `#00000007` / white | | control bodies (button = `Fill2`, checkbox off = `Fill1`), pressed (`#0000002B` measured), tracks |
| `TextBackground` | System `textBackgroundColor` | `#FFFFFF` / `#1E1E1E` (fields draw it at 98 %) | | fields, text views |
| `Separator` | System `separatorColor` | `#00000019` / `#FFFFFF19` | (`Line`) | dividers, field hairlines |
| `Grid` | System `gridColor` | `#E6E6E6` / `#1A1A1A` | | table grids |
| `Accent` | System `controlAccentColor` | `#007AFF` (user's pink `#F74F9E`) | `#1E66F5` / `#007AFF` (was `#7AA2FF`) | default button, checks, switches, sliders |
| `OnAccent` | Rex | white / white | white / white | text on the accent |
| `Selection` | System `selectedContentBackgroundColor` | `#0064E1` / `#0059D1` | | emphasised rows, menu highlight |
| `SelectionUnemph` | System `unemphasizedSelectedContentBackgroundColor` | `#DCDCDC` / `#464646` | | rows with no focus, inactive window |
| `TextSelection` | System `selectedTextBackgroundColor` | follows the user's highlight (`#E0E0E0` graphite on the MacBook) | | text selection |
| `FocusRing` | System `keyboardFocusIndicatorColor` | `#0067F47F` / `#1AA9FF7F` | | focus ring |
| `AltRow` | System `alternatingContentBackgroundColors[1]` | `#F4F5F5` / `#FFFFFF0C` | | striped tables |
| `Link` | System `linkColor` | `#0068DA` / `#419CFF` | | links |
| `Red`, `Orange`, `Yellow`, `Green`, `Teal`, `Blue`, `Purple`, `Pink`, `Gray`, … | System `system*Color` | e.g. red `#FF383C` / `#FF4245`, green `#34C759` / `#30D158` | | status, badges, destructive |
| `Danger`, `Warning`, `Success`, `Info` | aliases of `Red`, `Orange`, `Green`, `Blue` | | | semantic status |
| `Shadow`, `Scrim`, `PaneHairline`, `PaneHighlight`, `GlassTint` | Rex | | today's chrome values | Rex chrome |
| `Term*` (16 + dim set) | Rex | | `docs/design/rex-benchmark.md` palette | terminal and code content |

**Contrast.** The current test, which requires `Label` and `Label2` to pass
7:1 and 4.5:1, stays. It runs on the resolved system values too, for both
appearances and the high-contrast ones (`ref:colors.NSAppearanceNameAccessibilityHighContrast*`).

### 2.2 Type scale: the macOS text styles (ref)

| Style | Size / line | Weight | Use |
|---|---|---|---|
| `LargeTitle` | 26 / 32 | regular | onboarding and empty-state titles |
| `Title1` | 22 / 26 | regular | window-level titles |
| `Title2` | 17 / 22 | regular | section titles |
| `Title3` | 15 / 20 | regular | group titles |
| `Headline` | 13 / 16 | bold (700) | emphasised body, list titles |
| `Body` | 13 / 16 | regular | default text, controls (regular size) |
| `Callout` | 12 / 15 | regular | help text |
| `Subheadline` | 11 / 14 | regular | table headers, small controls, sidebar group headers (drawn bold) |
| `Footnote` | 10 / 13 | regular | footers |
| `Caption1` | 10 / 13 | regular | captions |
| `Caption2` | 10 / 13 | medium (510) | badges, small labels |
| `Mono` | 13 / 16 | regular | code, terminal (SF Mono via `kCTFontUIFontDesignMonospaced`) |

- **Control fonts by size (ref):** mini 9, small 11, regular 13, large 13, xlarge 13.
- **Menu font:** 14 on 27.2. Title bar: 13 bold.
- **Weights:** a `Weight` modifier per use: regular 400, medium 510, semibold 590, bold 700.
- **Numbers:** a `TabularDigits` flag on `text.FontDesc` gives monospaced digits.
- **Rex additions:**
  - `TabTitle`: 13 regular; the reference measured 13–14 and is refitted after the optical-size fix;
  - `PaneTitle`: 13 semibold;
  - `Terminal`: the grid font of `rex.NewGrid`.
- **Rendering prerequisite:** set every style at its point size's optical size and tracking (audit, problem 1).

The 11 styles plus the per-size control fonts make about 20 `FontKey`s,
created in `Resolve` (it already allocates there, once per scale).

### 2.3 Spacing, sizes, radii

| Token | Values (pt) | Source |
|---|---|---|
| Space scale | 2, 4, 6, 8, 12, 16, 20, 24, 32 | *designed* (HIG grid) |
| Control gap | 8 (between controls), 6 (checkbox box to label) | ref: label offset in `NSButton` checkbox about 6 |
| Window margin | 20 | *designed* (HIG) |
| Control height by size | mini 16, small 20, **regular 24**, large 28, xlarge 36 | ref |
| Field height | small 22, regular 24, large 24 | ref |
| Control side padding | mini 6, small 8, regular 12, large 14, xlarge 16 | ref (regular: (64.5 − label) ÷ 2 ≈ 12); others scaled, *to verify per size in the sheets* |
| Control radius | mini 4, small 5, regular 6, large capsule (14), xlarge capsule (18) | ref |
| Field radius | small 5, regular 6, large 7; search: capsule | ref |
| Checkbox / radio | 14 box, r 3.5 / 14 circle | ref |
| Switch | mini 36 × 16, small 44 × 20, regular 54 × 24, large 64 × 28 | ref |
| Slider | track 6, 16 tall | ref |
| Row heights | table 24, header 28, sidebar 32 (group header 19), menu item 24 | ref |
| Indent | outline 13 per level | ref |
| Scroller | 17 regular, 15 overlay small, 13 legacy small | ref |
| Focus ring | outset 3, width about 3.5 visual (3 solid), follows the radius + 3 | ref |
| Titlebar | plain 32, compact toolbar 40, unified toolbar 52; traffic lights 14 pt at x 9 / 32 / 55 (plain), 12 / 35 / 58 (compact), 19 / 42 / 65 (unified); **Rex** 46 with lights at 21 / 43 / 65 | ref + Rex benchmark |
| Radii, Rex | pane 10.5, tab pill full, window 16 (system) | Rex benchmark, ref |
| Sidebar selection | inset about 10, r ≈ 8.5 | ref (pixels) |

### 2.4 Elevation, shadows, materials

| Level | Use | Treatment |
|---|---|---|
| 0 `Flat` | content | no shadow |
| 1 `Pane` | Rex panes and cards | today's two layers (tight 1/1.5 and ambient 3/5, in pt) and the hairlines; focus-dependent (Rex benchmark) |
| 2 `Raised` | drawn popovers, drawn menus (non-macOS), tooltips | `SurfaceRaised`, a 0.5 pt separator edge, shadow about y 4 / σ 12 at 20 % (*designed*) |
| 3 `Modal` | sheets, drawn dialogs | shadow y 8 / σ 24; **no scrim** on macOS sheets; a scrim only on other OSes (*designed*) |

**Materials** are a token with a host:

| Material | Uses | macOS | Elsewhere |
|---|---|---|---|
| `Window` | the window | flat `Window` colour | same |
| `RexField` | Rex windows | the Rex gradient (drawn) | same |
| `Glass` | unfocused Rex panes | drawn: Rex's translucent fill sampling the field | same |
| `Sidebar`, `Titlebar`, `Menu`, `Popover`, `HUD` | — | real `NSVisualEffectView` (vibrancy) behind a transparent region of the Metal layer | `SurfaceRaised` or a drawn tint |
| `LiquidGlass` | — | `NSGlassEffectView`: optional, macOS 26+ | none |

`Reduce Transparency` forces every material to its opaque fallback.

### 2.5 Motion

| Token | Value | Notes |
|---|---|---|
| `Instant` | 0 | press highlight, hover on standard controls (none), appearing menus |
| `Quick` | rate 50 (about 20 ms half-life) | press release, focus ring (ref: AppKit draws it at once) |
| `Standard` | 0.25 s (ref: `NSAnimationContext` default) | switch knob, disclosure rotation, row insertion |
| `Fade` | 0.2 s | menu, popover and tooltip dismissal (*designed*) |
| `Dim` | rate 20 | Rex pane focus dimming |
| `Mode` | rate 10 (about 0.3 s) | light/dark crossfade |
| `Sheet` | 0.25–0.3 s | sheet slide (*designed*) |

- **Hover:** `HoverOn` is a per-component token, off by default on macOS. Toolbar buttons, menu items, sidebar disclosure, scrollers and Rex pane buttons turn it on.
- **Reduce Motion** (read from the system) sets every duration but the crossfade to 0 and stills the indeterminate progress, the spinner and the typing indicator.

### 2.6 Control sizes

`type Size uint8`: `Mini`, `Small`, `Regular` (default), `Large`, `XLarge`.
A resolved `SizeTokens` holds, in pixels: height, horizontal padding,
radius, the icon box, the gap, a `FontKey`, and the field height. It is one
array, `[NumSizes]SizeTokens`, in `Resolved`.

## 3. Components

Notes on the columns:

- **Draw:** the component is drawn by us on all OSes, with the macOS recipe on macOS.
- **Host (mac):** on macOS a real AppKit object does the work, and the component is a thin wrapper with the same Go API; elsewhere it is drawn.
- **Ref** names the AppKit sample in `~/Projects/research/appkit-ref/macbook-blue/{light,dark}/`.

### 3.1 Controls

| Component | Variants | States | Sizes | Native reference | Draw / host | Why |
|---|---|---|---|---|---|---|
| **Button** | `Default` (Fill2), `Primary` (accent, white), `Destructive` (Fill2, red label in alerts), `Plain` (borderless), `Toolbar` (hover-on, no fill at rest), `Glass` (macOS 26+) | rest, pressed, disabled, focused, inactive-window (the primary loses its accent) | all 5; large and xlarge are capsules | `push-*`, `push-default-*`, `push-destructive`, `push-capsule`, `push-xlarge` | Draw | A drawn button can be pixel-matched (ref fills are semantic colours). Hosting NSButton would cost compositing and zero-alloc control for no behavioural gain |
| **IconButton** | `Bordered` (Fill2 square, r = size radius), `Borderless`, `Toolbar` | as Button + hover (toolbar and borderless) | small, regular, large | `icon-rounded`, `icon-borderless`, `icon-toolbar` | Draw | SF Symbols already come from the icon atlas |
| **Toggle** (toggle button) | `Default`, `AccessoryBar` (accent when on) | off, on, pressed, disabled, focused | small, regular | `accessorybar` | Draw | |
| **Checkbox** | — | off, on, mixed, pressed, disabled, focused | small, regular, large | `checkbox-*` | Draw | |
| **Radio** (RadioGroup) | vertical, horizontal | off, on, disabled, focused | small, regular | `radio-*` | Draw | |
| **Switch** | — | off, on, disabled, focused; the knob slides `Standard` | mini to large | `switch-*` | Draw | |
| **Slider** | continuous, ticked (`Ticks n`), vertical | rest, dragging, disabled, focused | small, regular | `slider*` | Draw | |
| **Stepper** | — | rest, pressed up or down, disabled | small, regular | `stepper` | Draw | |
| **Input** (TextField) | `Default`, `Secure`, `Borderless` (inline rename), `Mono` (paths, commands) | empty with placeholder, filled, focused (ring), disabled, invalid (red ring) | small, regular, large | `field-*`, `secure` | Draw on `ui.TextBuffer` (IME and the macOS text input client exist already) | Single-line fields live inside lists and forms; a host view per field cannot scroll, clip or composite with our content |
| **SearchField** | `Default` (capsule), `Toolbar` | empty, filled (filled-circle clear button), focused, disabled | small, regular | `search-*` | Draw | |
| **Textarea** (multi-line) | `Plain`, `Bordered` | empty, focused, disabled, read-only | — | `textview` | Draw by default; `Native` option hosts NSTextView | See the Composer reasons; a drawn Textarea stays the zero-allocation default for notes-like editors |
| **Select** (popup button) | `Default` (Fill2 and the up-down chevron), `Borderless` | closed, open, disabled, focused | small, regular, large | `popup*` | Host the menu (mac): the button is drawn, the list is `NSMenu popUpMenuPositioningItem:atLocation:inView:` | The native popup list puts the selected item over the button, has type-select, and draws outside the window |
| **PullDown / MenuButton** | `Default`, `Borderless`, `Toolbar` | as Select | as Select | `pulldown` | Host the menu (mac) | as Select |
| **Combobox** | — | as Input + list open | small, regular | `combo` | Draw (field) + drawn list | An NSComboBox list is a separate window; a drawn suggestion list is enough |
| **TokenField** | — | tokens, editing, selected token | regular | `token` | Draw | |
| **SegmentedControl** | `SelectOne` (accent pill on the Fill2 track), `SelectAny`, `Momentary` (separated: back and forward) | per segment: selected, pressed, disabled | small, regular, large | `segmented*` | Draw | |
| **ColorWell**, **DatePicker** | — | — | regular | `color-well`, `date-picker` | Draw the well and the field; host NSColorPanel (mac) | The system colour panel is what users know |
| **Disclosure** | `Triangle`, `Button` (push-disclosure) | closed, open (rotates `Standard`) | — | `disclosure-*`, `push-disclosure` | Draw | |
| **HelpButton** | — | rest, pressed | regular | `help` | Draw | |
| **Link** | inline, standalone | rest, pressed, visited | text styles | `linkColor` | Draw | |

### 3.2 Collections and navigation

| Component | Variants | States | Sizes | Native reference | Draw / host | Why |
|---|---|---|---|---|---|---|
| **Table** | `FullWidth`, `Inset`, `Striped` (alternating), `Plain`; with header, sortable, resizable | row: rest, selected-emphasised (solid `Selection` + white text), selected-unemphasised, drop target; no hover on macOS | row 24 (`Small` 20, `Large` 28) | `table-*` | Draw | A million rows at zero allocation is our strength; NSTableView would add a data-source bridge per row |
| **List** | `Plain`, `Inset`, `Separated` | as Table | as Table | `table-inset-*` | Draw | |
| **Sidebar** (source list / outline) | `SourceList` (groups and items), `Navigation` | item: rest, selected-emphasised (accent block r 8.5, white bold text and icon), unemphasised, editing; group header (11 bold, secondary, 19 tall); disclosure | rows 32 (macOS 27 medium); small 24 *to measure* | `sidebar-*` | Draw rows over the `Sidebar` material (host NSVisualEffectView) | Vibrancy needs the window server; the rows themselves are ours |
| **Tabs** | `Segmented` (default on macOS: a SegmentedControl above a Box), `TabView`, `Rex` (the titlebar capsule) | selected, rest, disabled; Rex: active pill, fading titles | regular | `tabview` (tabs not captured), `segmented` | Draw | The Rex capsule is the identity; app tabs on macOS are segments |
| **Toolbar** (window chrome) | `Plain` (32), `Compact` (40), `Unified` (52), `Rex` (46) | active, inactive window (title Label2, lights grey: OS) | — | `window-*` | Draw the band; the OS draws the traffic lights at the token's position | One token places the lights, the title and the items |
| **PathBar / Breadcrumb** | — | segments, hover, pressed | small, regular | (Finder path bar) | Draw | Pilot's mono path field becomes this |
| **Splitter** | `Rex` (gutter + slim handle on hover), `Thin` (1 px separator, sidebars) | rest, hover, dragging | — | NSSplitView thin divider | Draw | |
| **ScrollArea** | `Overlay`, `Legacy`, following `NSScroller.preferredScrollerStyle` | rest (overlay hidden), scrolling, hover (expanded with track) | 17 / 15 / 13 | `scroll-*` (widths) | Draw | Our own scroll physics and zero-allocation paths |

### 3.3 Containers and overlays

| Component | Variants | States | Native reference | Draw / host | Why |
|---|---|---|---|---|---|
| **Card** | `Rex` (pane: glass to opaque, layered shadow), `Box` (NSBox titled), `Inset` (grouped form) | focused or dimmed (Rex) | `box` | Draw | The identity |
| **Separator** | horizontal, vertical, inset | — | `separator` | Draw (`Separator`, a 1 px hairline) | |
| **Dialog** | `Sheet` (macOS default: slides from the titlebar, no scrim), `Panel` (centred, other OSes, scrim) | opening, open, closing | `alert` (layout), NSWindow sheet | Draw on all OSes, with the macOS layout and behaviour (attached to the titlebar, no scrim) | Custom content needs our renderer |
| **Alert** | `Informational`, `Warning`, `Critical` (destructive button) | — | `alert`: 260 wide, centred icon, bold message, capsule buttons filling the width | **Host NSAlert as a sheet (mac)** | Text-only alerts gain the exact look, keyboard (Return/Esc/⌘D), the material and VoiceOver at no cost |
| **Popover** | `Default` (arrow), `Menu`-like | showing, shown, dismissing | NSPopover | Draw (in-window) now; host later if content must leave the window | |
| **Tooltip** | — | delayed show, hide | `toolTipsFont` 11 | **Host (mac)** through the content view's tooltip | Native delay, look and placement beyond the window, for nothing |
| **Menu** (menu bar) | — | — | NSMenu | **Host (mac)**, drawn elsewhere | The biggest tell of all (audit problem 2) |
| **ContextMenu** | — | — | NSMenu | **Host (mac)**, drawn elsewhere | Services, the share menu, AutoFill and system items |
| **Toast / Banner** | `Info`, `Success`, `Warning`, `Error`; inline banner, floating toast | entering, shown, leaving | (none in AppKit) | Draw | No native equivalent; the Raised level |
| **EmptyState** | with icon, title (Title3), message (Label2), action | — | (Finder or Mail empty views) | Draw | |
| **Skeleton** | line, block, avatar | shimmer, or still under Reduce Motion | — | Draw | |
| **Badge** | `Count` (red capsule, white Caption2), `Status` (dot), `Tag` (Fill2 capsule) | — | NSToolbarItem badge, sidebar counts | Draw | |
| **Avatar** | image, initials, group stack | presence dot | Contacts | Draw | |
| **Progress** | bar determinate and indeterminate, circular determinate | — | `progress-*` | Draw | |
| **Spinner** | — | small 16, regular 32 | `spinner*` | Draw (12-spoke, still under Reduce Motion) | |
| **Open/Save panel** | — | — | NSOpenPanel / NSSavePanel | **Host (mac)** | Sandbox access, Recents, iCloud and tags are only reachable this way |

### 3.4 Chat pieces

| Component | Variants | States | Draw / host | Notes |
|---|---|---|---|---|
| **MessageBubble** | `Outgoing` (Accent, white text), `Incoming` (Fill1 over Surface; *designed*), `System` (centred Caption1, Label2), grouped (tail on the last only) | sending, sent, failed (red badge), selected, highlighted (search); text, code (Mono on Fill3), attachment | Draw | Text in `Body`; max width 70 % of the column; 12 pt horizontal and 7 pt vertical padding, radius 17 (*designed*, after Messages) |
| **Composer** | `Default`, `Expanded` (multi-line, grows to 8 lines), with attachments row | empty (placeholder), typing, sending, disabled | **Host NSTextView (mac)** in a native child view over our layer; drawn Textarea elsewhere | Users expect spell check with squiggles and suggestions, autocorrect, text replacement, dictation, the emoji and symbols picker, Writing Tools, Services and the Format menu in a chat box. Dictation and the emoji picker already work through our `NSTextInputClient`; the rest needs NSTextView. Costs: the view sits above the Metal layer (nothing we draw may overlap it, so menus near it are native), and its text crosses to Go on change events only, into a fixed buffer, so frames stay allocation-free |
| **ChatListRow** | `Default`, `Compact` | rest, selected (Sidebar selection), unread (bold title and badge), muted, typing | Draw | 56 pt: Avatar 36, title `Headline`, preview `Subheadline` Label2 on 2 lines, time `Caption1`, Badge |
| **DateDivider** | — | sticky while scrolling | Draw | `Caption1` semibold, Label2, centred, with hairlines |
| **TypingIndicator** | in a bubble, inline | animating (three dots, 1.2 s loop), still under Reduce Motion | Draw | |

### 3.5 Also essential

| Component | Notes |
|---|---|
| **Text / Label** | takes a `TextStyle` and a colour role; truncation (tail, middle for paths); selectable text |
| **Icon** | SF Symbols (the atlas), with `Weight` matched to the text style and `Scale` (small, medium, large) |
| **Form** | label column right-aligned and content left-aligned on macOS (HIG), with section headers and footers |
| **Kbd / ShortcutHint** | for menus drawn on Windows and Linux and for help |
| **StatusBar** | `Footnote` Label2; Pilot's and the gallery's footers |
| **Inspector** | a trailing sidebar with grouped forms |
| **DragPreview and DropTarget** | the outline in `Selection` at the 0.5 pt edge; *designed* |

## 4. Architecture in Go

### 4.1 The options

| Option | What | Zero-alloc | Ownership | Native feel | Verdict |
|---|---|---|---|---|---|
| **A. Retune `widget` in place** | new token values, more fields in `Metrics` and `Palette` | yes | none: changing a look means changing `widget` | partly: no variants, no sizes | quick first step only |
| **B. Headless `widget` + styled `rexui` on tokens** (shadcn) | `widget` keeps behaviour, layout and semantics and exposes begin/end primitives with visual state; `rexui` components draw from token tables, each in its own file | yes: enums and value tables, no interfaces per widget | copy a file to own it; override a token to adjust it | full: recipes follow the macOS measurements | **recommended** |
| **C. Painter interfaces** | the theme supplies a `ButtonPainter` and others; widgets call them | at risk: interface calls per row, and pointer arguments escape (the `Chrome` comment in `theme/chrome.go` already warns) | swap a painter | full | rejected: cost per row, harder to read and test |
| **D. Host AppKit controls** | NSButton and friends as subviews | AppKit's heap, and a bridge per control | none | exact | rejected except where the OS adds behaviour (§ 3: menus, panels, alert, tooltip, composer) |

### 4.2 Recommended layering

```
ui ── layout, boxes, input, animation, semantics (unchanged)
theme ── Tokens: Roles (palette array), TextStyles, Sizes, Space, Radii, Elevation, Materials, Motion
         Resolved: px values, FontKeys, mixed palette, recipe tables          (allocates only in Resolve)
theme/rex ── Rex values + Rex chrome (field, panes, capsule, dividers)  ← the identity
theme/system ── (darwin) live semantic colours, highlight, contrast, reduce-motion via platform
widget ── headless behaviour: Pressable, Toggleable, Choice, Range, TextInput, Collection (List/Table/Outline),
          Popup, Scroll, Split, Focus, Disabled — returns visual state + signals; draws nothing visible
rexui ── styled components: Button, IconButton, …, MessageBubble — one file each, tokens only
platform/app ── native hosts: NSMenu bar & context menus, popup-button menus, NSOpenPanel, NSAlert sheet,
                tooltips, NSVisualEffectView regions, NSTextView composer
```

`widget` stays the D28 entry point during migration: its current calls become
thin wrappers over `rexui` with the default variant, so apps keep compiling.

### 4.3 A headless primitive, then a recipe

```go
// package widget: behaviour only.
type Press struct {
	ID       ui.ID
	Key      ui.Key
	Hot      float32 // 0 when the recipe turns hover off
	Down     float32
	Focus    float32
	Ring     float32 // focus ring strength (FocusVisible)
	Disabled bool
	Inactive bool // the window is not key: recipes draw the unemphasised look
}

// BeginPress opens a clickable, focusable root box with the role and the name,
// sized by the caller (NextSize or the recipe's tokens). Build the visuals
// inside it, then EndPress.
func (k *Kit) BeginPress(label string, role ui.Role, w, h ui.Size, flags ui.Flags) Press
func (k *Kit) EndPress(p *Press) Signal
```

```go
// package rexui: button.go, the whole component, which an app may copy.
type ButtonVariant uint8

const (
	ButtonDefault ButtonVariant = iota
	ButtonPrimary
	ButtonDestructive
	ButtonPlain
	ButtonToolbar
	numButtonVariants
)

type ButtonOpts struct { // passed by value: nothing escapes
	Variant ButtonVariant
	Size    theme.Size
	Icon    icon.ID
}

func Button(k *widget.Kit, label string, o ButtonOpts) widget.Signal {
	r := k.Theme()
	st := &r.Size[o.Size]                // [NumSizes]SizeTokens, px
	rc := &r.Recipes.Button[o.Variant]   // [numButtonVariants]ButtonRecipe: roles and flags, no pointers
	p := k.BeginPress(label, ui.RoleButton, ui.TextSize(2*st.PadX), ui.Px(st.H), 0)
	fill := r.Pal.Role(rc.Fill)          // indexed lookup, mixed for light/dark already
	switch {
	case p.Disabled:
		fill = r.Pal.Role(rc.FillDisabled)
	case p.Down > 0:
		fill = theme.MixRGBA(fill, r.Pal.Role(rc.FillPressed), p.Down)
	case p.Inactive && rc.InactiveFallback:
		fill = r.Pal.Role(r.Recipes.Button[ButtonDefault].Fill)
	}
	widget.Fill(k, fill, st.Radius, rc.Capsule && o.Size >= theme.Large)
	widget.Text(k, label, st.Font, r.Pal.Role(textRole(rc, p)), ui.AlignCenter)
	widget.FocusRing(k, p, st.Radius) // outside the box, by the FocusRing tokens
	return k.EndPress(&p)
}
```

- **Variants and sizes** are `uint8` enums that index fixed-size arrays in `Resolved`.
- **Recipes** hold `Role` indices and flags, not colours, so a light/dark crossfade costs nothing extra and a recipe never needs re-resolving.
- **Options** are small structs passed by value, with no closures, interfaces, maps or strings built per frame.
- **Tests:** the existing allocation gates (gallery, Pilot and notes `-bench -gate`) cover it. Add a `testing.AllocsPerRun` test per component in `rexui`.

### 4.4 Overriding and owning

- **Tokens:** `t := rex.Theme(); t.Sizes[theme.Regular].H = 26; t.Roles[theme.Accent] = theme.FromSystem(...)`. The theme is a value, copied per app, as today.
- **A recipe:** `t.Recipes.Button[rexui.ButtonPrimary].Fill = theme.RoleGreen`. Recipes for the app's own components live in an app-owned table indexed by its own enums.
- **A component:** each `rexui` file depends only on exported `widget` primitives and `theme`. Copy it into the app (`go run ./tools/rexui add button ./internal/ui`, which rewrites the package clause) and edit freely. That is shadcn's "you own the code", without a registry and without third-party code.
- **Per platform:** recipes are chosen at `Resolve` by OS (macOS measurements; Windows 11 and GNOME values later), so the Go API of an app does not change between OSes.

## 5. Migration

| Step | What | Who sees it |
|---|---|---|
| 0 | **Text optical size and tracking** (audit problem 1) in `text/coretext_darwin.go`, with a test against `NSFont` advances | everything, rexlook included |
| 1 | **Tokens v2** in `theme` and `theme/rex`: the role array with system sources (`platform` reads the semantic colours, highlight, contrast, reduce motion and reduce transparency, beside D32); text styles; sizes; motion; neutral dark surfaces; system accent in dark | everything that reads the palette |
| 2 | **`widget` headless primitives + `rexui`** for Button, IconButton, Checkbox, Radio, Switch, Slider, Input, SearchField, Select, SegmentedControl, Table/List selection, ScrollArea, Separator, Card. The old `widget` calls forward to `rexui` defaults | the gallery first |
| 3 | **Gallery:** its state matrix becomes a side-by-side catalogue (`-scene appkit`), with each component in its variants and sizes next to `tools/appkitref` metrics. A `rexui` review test checks each recipe's numbers against the measured ones (heights, radii, fills, ring outset), not against Apple's pixels, which are not ours to commit | the gallery |
| 4 | **Native hosts (5B):** the NSMenu bar and context menus from `menu.Model` (`Kit.MenuBar` draws nothing on macOS); Select and PullDown menus; tooltips; the NSAlert sheet; NSOpenPanel and NSSavePanel | gallery and notes lose their drawn bars |
| 5 | **Notes:** a full-size titlebar with the `Compact` Toolbar (40 pt) holding the title and Add; List with emphasised selection; Switch at regular; Remove as `Destructive` | notes |
| 6 | **Pilot:** the `Unified` Toolbar (52) with back, forward and up as `Toolbar` IconButtons and a PathBar; a Sidebar of locations over the vibrancy material; tables in `Body` with tabular-digit sizes and dates in `Label2`, folders as symbols rather than bold blue; striped rows; the filter as a SearchField. Menlo stays an option (`Mono` variant) for a "terminal" view | Pilot |
| 7 | **Rex identity everywhere:** the Rex field, panes and capsule are opt-in chrome (`Toolbar` variant `Rex`, `Card` variant `Rex`). rexlook moves its titles to `TabTitle` and `PaneTitle`, its header buttons to `IconButton{Borderless}`, and keeps its own scene code | rexlook (no visible change beyond step 0) |
| 8 | **Composer, Sidebar material, Popover, Toast, chat pieces** as the chat work needs them | new apps |

**Gates per step:**

- zero allocations in the existing benches;
- `tools/rexcompare` still passes for rexlook;
- the side-by-side sheets regenerated (`tools/appkitref/sheets.sh`) and looked at in light and dark.

## 6. Open questions

- **Sidebar sizes.** The sidebar row height (32) is AppKit 27's "medium" default. Small and large were not measured; one `AppKitRef` sample per `rowSizeStyle` will settle it.
- **Menu font.** It measured 14 pt (`menuFontOfSize:0`). Worth confirming against a live menu, once screen capture on a test machine is allowed.
- **Rex's own controls.** The Rex reference has no controls. If Superlogical later publishes a screenshot with controls, the Rex variants can be refitted. Until then Rex's controls are the native ones in Rex's colours, which keeps the trade-dress risk (D17) low.
