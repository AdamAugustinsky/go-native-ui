# Workspaces: panes, focus and surface profiles

`widget/workspace` is a layout of panes for apps whose window is a set of
panes: a tree of splits whose leaves are panes with identities of their own,
the pane in use, and the layout saved as JSON. `theme.Resolved.Surface` gives
each kind of surface (a pane, the sidebar, a toolbar capsule) one profile
resolved for the window's appearance, its active state, Reduce Transparency,
Increase Contrast, the backdrop and the app's density. `cmd/rexlook` runs on
both. This chapter is the API, then Zap's move to it, item by item, from Zap's
own inventory (`docs/gnui-panes-inventory.md` in Zap, commit 5abf420).

`base.Splitter` stays as it is: a row or column of up to eight panes with
ratios, for a screen that needs nothing more.

## 1. The layout

A `workspace.Layout` is app-owned, pointer-free state in fixed arrays, at most
16 panes. Changing it and building a frame allocate nothing.

| Call | What it does |
|---|---|
| `Reset(id)` | one pane, focused |
| `Split(target, id, dir, share)` | puts pane `id` beside `target` (`Right`, `Down`, `Left`, `Up`), taking `share` of its room; returns the new divider's ID |
| `Close(id)` | takes the pane out; its neighbour takes the room |
| `SetHidden(id, bool)` | the pane keeps its place and size, with no room |
| `SetEmpty(id, bool)` | the pane stays on screen, but the focus passes it by |
| `SetSolo(id)` | one pane over the whole work area (a narrow window) |
| `SetZoom(id)` | one pane maximised; the others keep their rects for the animation |
| `SetLimits(id, Limits{Min, Max, Yield})` | least and greatest size in points; `Yield` gives up the room instead of squeezing |
| `SetFixed(id, pt)` / `Fixed(id)` | the pane keeps a size in points instead of a share; drags change it |
| `Arrange(work, Metrics)` | places the panes on whole pixels |
| `Rect(id)`, `Shown(id)`, `Slot(id)`, `Pane(i)`, `Len()` | where each pane is; `Slot` is stable while the pane lives (`theme.Surface.Slot`) |
| `Focus()`, `SetFocus(id)`, `FocusDir(dir)`, `FocusNext(step)` | the pane in use |
| `Update(c, Keys)` | a press focuses a pane; dividers drag, and move with the keys while focused; `Keys` moves the focus |
| `NextPane(c, id, name)`, `NextDivider(c, &d)` | semantics for the pane's and the divider's boxes |
| `MarshalJSON`, `Restore(data, known)` | the layout saved with a version; unknown panes and fields are tolerated |

**Focus restoration.** The layout keeps the panes used before, most recent
first. When the focused pane closes, hides, becomes empty or is left no room
(a yielding pane, a solo pane elsewhere), the focus goes to the pane used most
recently before it that can take it, else to the nearest pane where it was,
else to the first in reading order. With nowhere to go, it stays.

**Keyboard.** `Keys{Dir: ui.ModSuper | ui.ModAlt}` makes ⌘⌥ and the arrows
move the focus to the nearest pane on that side (of two equally near, the one
used last). `Keys{Cycle: true}` makes F6 and ⇧F6 cycle. A divider is a tab
stop, and moves like `base.Splitter`'s: arrows (16 pt, 64 pt with ⇧), Home and
End, and the accessibility increment and decrement.

**Accessibility.** `NextPane` makes a pane's box a group named after the pane,
selected while it is in use. When the keys move the focus, the pane's name is
announced politely. `NextDivider` makes a divider a `RoleSplitter` with the
first pane's share as its value, named by `base.Strings.Divider`.

**Operations left out.** The model has no swap or move-pane operation. Neither
Zap nor rexlook moves a pane to another place, and adding one without a user
would be guessing at its rules. `Close` followed by `Split` does it today.

## 2. Surface profiles

```go
p := th.Surface(theme.SurfacePane, k.Inactive())
```

| Field | Means |
|---|---|
| `Fill` | `FillNative` (the system's material: leave the region clear), `FillDrawn` (a translucent colour over the app's background), `FillOpaque` |
| `Color`, `Opacity`, `Solid`, `Tint` | what to draw, its alpha, the opaque stand-in, the tint over a native material |
| `Radius`, `Continuous` | corner geometry |
| `Stroke`, `StrokeW` | the hairline; under Increase Contrast every surface that floats is outlined in `Label2`, at least a pixel |
| `Elevation`, `Shadows` | the elevation and its layers' colours |
| `Inset`, `Gutter`, `Header` | room from the window's edge, between panes, and the title row's height |

The profiles derive from the existing tokens: materials (`MacMaterials`),
elevations, radii, the `Metrics`' inset, gutter and large radius, and the
compact toolbar's height. They are resolved together the first time one is
read after the mode, the accent, the system's settings, the backdrop or the
density changed. Otherwise a read costs one comparison. Under Reduce
Transparency a native material is opaque and a translucent fill takes the
material's solid role. Call `th.SetDensity(d)` each frame next to
`SetBackdrop`. `DensityCompact` packs panes 4 pt apart, with corners no
larger than a selection's and 28 pt title rows. Rex's chrome takes its pane
radius from the profile and outlines panes under Increase Contrast, and
rexui's glass reads its fallbacks from the profiles.

## 3. Zap's panes on the workspace

The tree is `list | (chat | side)`. The rail and the titlebar band are not
panes: they stay in the shell, and the work area `Arrange` gets starts after
the rail.

```go
const (
	paneList workspace.PaneID = 1 + iota
	paneChat
	paneSide
)

// newPanes is the main window's layout, once.
func newPanes(st *state.Settings) workspace.Layout {
	var l workspace.Layout
	l.Key = ui.HashString(0, "zapfast/pane")
	l.Reset(paneList)
	l.Split(paneList, paneChat, workspace.Right, 0.5)
	l.Split(paneChat, paneSide, workspace.Right, 0.3)
	l.SetLimits(paneList, workspace.Limits{Min: [2]float32{minListPt}, Max: [2]float32{maxListPt}})
	l.SetFixed(paneList, st.SidebarWidth)
	l.SetLimits(paneChat, workspace.Limits{Min: [2]float32{320}})
	l.SetHidden(paneSide, true)
	return l
}

// syncPanes tells the layout this frame's state; every call is idempotent.
func (sh *Shell) syncPanes(a *state.Account, open, narrow bool) {
	s, l := sh.S, &sh.panes
	l.SetHidden(paneList, s.Panes.ListHidden)
	side := s.Panes.Pane != state.PaneNone && open
	l.SetHidden(paneSide, !side)
	if side {
		lim := workspace.Limits{Min: [2]float32{240}, Max: [2]float32{640}, Yield: true}
		if s.Panes.Pane == state.PaneSearch {
			lim.Min[0], lim.Max[0] = 300, 520
		}
		l.SetLimits(paneSide, lim)
		l.SetFixed(paneSide, sideWidth(s)) // InfoPaneWidth or SearchPaneWidth
	}
	if ref := chatRef(a); open && ref != sh.focusChat { // a chat opened: the conversation is in use
		l.SetEmpty(paneChat, false)
		l.SetFocus(paneChat)
	}
	l.SetEmpty(paneChat, !open)
	solo := workspace.PaneID(0)
	switch {
	case narrow && side:
		solo = paneSide // covers the window (Panes.Covering)
	case narrow && (open || s.Panes.ListHidden):
		solo = paneChat
	case narrow:
		solo = paneList
	}
	if l.Solo() != solo {
		l.SetSolo(solo)
	}
}

// in Build, in place of the paneRects arithmetic and noteFocus:
	sh.panes.Update(c, workspace.Keys{}) // presses; the list's divider
	sh.syncPanes(a, open, narrow)
	p := th.Surface(theme.SurfacePane, k.Inactive())
	sh.panes.Arrange([4]float32{max(railW, p.Inset), tbH, W - p.Inset, H - p.Inset},
		workspace.Metrics{Scale: k.Scale(), Gutter: [2]float32{p.Gutter, p.Gutter}, Min: [2]float32{e.Pt(120), e.Pt(120)}})
	if w := sh.panes.Fixed(paneList); w != s.Settings.SidebarWidth && w > 0 {
		s.Settings.SidebarWidth = w
		s.Touch()
	}
	for i := 0; i < sh.panes.Len(); i++ {
		if id := sh.panes.Pane(i); sh.panes.Shown(id) {
			sh.surface(id, sh.panes.Rect(id)) // NextPane, then SurfaceShadow, BeginSurface, EndSurface
		}
	}
	for i := 0; i < sh.panes.NumDividers(); i++ {
		d := sh.panes.Divider(i)
		sh.panes.NextDivider(c, &d)
		th.Chrome.Divider(c, th, d.Key, d.Axis, d.Rect, d.Gutter)
	}
```

`surface` keeps its shape. It takes `Key: l.PaneKey(id)`, `Slot:
l.Slot(id)`, and its focus target is `id == l.Focus() && !k.Inactive()`. It
calls `l.NextPane(c, id, paneName)` before `BeginSurface`, which builds the
pane's box first.

### Item by item

| Inventory item | On the workspace | Stays in Zap |
|---|---|---|
| Pane ids `paneList`, `paneChat`, `paneSide` (`panes.go:35-37`) | `PaneID`s; `Slot(id)` for `theme.Surface.Slot` | |
| Rail, titlebar band, chat tabs | | yes: not panes; the work area starts after the rail |
| List 300 by default, 260–520 at draw time and in a drag (`shell.go:43-44`, `453`, `1074-1100`) | `SetLimits` with Min 260 and Max 520, `SetFixed(paneList, SidebarWidth)`; the drag is the divider (`Update`, `Fixed`) | writing `Fixed(paneList)` back to `Settings.SidebarWidth` |
| Side pane 380, 240–640 in a drag; search 300–520 (`info.go:28-29`, upstream `pane.rs:22-23`) | `SetLimits` per kind; `SetFixed` from the kind's setting. A side pane's drag is its divider | the setting per kind; with search's Min of 300, search also disappears below 300 (not 240) |
| Side pane at draw: `min(width, W − rail − list − 320)`, none below 240 (`shell.go:474-483`) | `Limits.Yield` on the side pane and the chat's Min of 320: it takes only the room beside the chat's 320, and none below its Min | |
| Chat pane: the rest, shown above 120 (`shell.go:507-509`) | the split's remainder; `Metrics.Min` | |
| Pane header 40 high (`panes.go:20`) | `Surface(theme.SurfacePane, …).Header` (the compact toolbar's 40 pt; 28 at compact density) | the header's `rexui.Toolbar` and `ToolbarPart` |
| Inset and gutter from the theme (`shell.go:487-509`) | `SurfaceProfile.Inset`, `Gutter` | |
| ⌘B hides the list; ⌘K shows it (`menu.go:319-327`) | `SetHidden(paneList, …)`; the list keeps its width | the compact list in the rail |
| Narrow window below 720 (`shell.go:437-474`) | `SetSolo`: chat when one is open or the list is hidden, the list when not, the side pane over everything while open | the 720 threshold, Back in the side pane, the slide (offset the rects by `dx` as today), the input shield |
| Side pane only with an open chat; closes when the chat changes or the account switches (`state.go:244-259`, `accounts.go:181-182`) | `SetHidden(paneSide, …)` from the state each frame; the focus leaves it as on a close | `PaneCloser.Closed` |
| No chat open: the empty pane (`shell.go:697-698`) | `SetEmpty(paneChat, true)`: on screen, out of the focus, its focus to the list | the empty state's content |
| Pane in use follows an opened chat (`panes.go:64-89`) | `SetEmpty(paneChat, false)`, `SetFocus(paneChat)` when the open chat changes | noticing the change (`focusChat`) |
| … and a left press | `Update` (the pane's box is `Clickable`, as `BeginSurface` builds it) | |
| … falls back when the focused pane hides | automatic, the pane used before. Zap's rule is "the chat if open, else the list"; after the side pane closes that is the chat unless the list was used in between. To keep the rule exactly, call `SetFocus(paneChat)` after hiding the side pane | |
| Inactive window: every pane unfocused (`panes.go:91-114`) | | yes: the surface's focus target |
| Pane focus is visual only | the same; `NextPane` adds the group, its selection and the announcement | |
| Tab order SH-016, Escape order, ⌥↑/⌥↓, ⌘1…9, Find | | yes. `Keys{}` takes no keys; Zap may opt into `Cycle` (F6) |
| Persisted widths, window frame (`settings.json`) | `Fixed(id)`; or the whole layout as JSON (`MarshalJSON`) | keeping the settings fields avoids migrating `settings.json` |
| Not persisted: `ListHidden`, the open side pane, focus | the JSON would save hidden panes and the focus; Zap need not save it | |
| Per account: open chat parked; `clearAccountUI` resets `Panes` but `ListHidden` | one `Layout` per window: the hidden list stays; the switch hides the side pane and sets the chat's emptiness from the new account's state through `syncPanes` | the parked chat and the account state |
| Surfaces: `SurfaceShadow`, `BeginSurface`, `EndSurface`, focus at rate 14 (`panes.go:91-114`) | the same Chrome; the pane radius and the Increase Contrast outline come from the profile | the animation |
| Transparency: `SetBackdrop(Settings.Transparency && …)`; the system's Reduce Transparency | unchanged; profiles follow both | the setting |

## 4. Limits

- Not measured against Zap's tests: the guide's code was not built in Zap.
  Zap's coordinator owns that change.
- `SetSolo` has no animation of its own. The slide stays the app's.
- Profiles cover the pane, the sidebar, the inspector, the toolbar capsule, the
  titlebar band and the popover. Menus, sheets and HUDs still read their
  materials directly.
- Compact density's values (4 pt gutters, the selection's radius, 28 pt
  headers) are derived from existing tokens, not measured from a reference.
