# rexui: the components

The guide's notes app is built from `widget`'s kit and a handful of `rexui`
components: the toolbar, the sidebar, the scroll bars, labels. This chapter is
about the library behind them. `rexui` is the **Rex component library**
(D40, D52): styled components in Rex, the framework's default look
([Rex](rex.md): capsule controls, quiet tints of the accent, glass over a
coloured field), on measurements taken from macOS 27, built on the headless
primitives of `widget/base`. It
is [shadcn](https://ui.shadcn.com)'s model without a registry: each component
is one file that reads only tokens, and an app that wants a component of its
own copies the file and edits it.

Every block is an excerpt of [`cmd/gallery`](../../cmd/gallery), which shows every
component in every state, or of [`examples/notes`](../../examples/notes), and is
checked by the same test as the guide's.

## 1. Two ways in

`widget.Kit` is the kit of a window: the text, the theme, the menu and the
widgets bound to them. It embeds a `*base.Kit`, which is what the components
take. A component is a plain function of that kit, a label and options passed by
value:

```go cmd/gallery/gallery/gallery.go
	g.wk = widget.NewKit(c, ts, rex.Theme())
// …
	g.k = g.wk.Kit
```

`wk.Select(...)` is the widget set's method, and `rexui.Button(k, ...)` is the
component, called with the base kit. They build the same boxes. The notes app
shows the mix: `k.Button`, `k.TextField` and `k.Switch` on the widget kit, and
`rexui.Sidebar(k.Kit, ...)` and `rexui.BeginToolbar(k.Kit, ...)` for the window
chrome, which has no widget method.

## 2. Options, variants and sizes

An option struct is small, passed by value, and its zero value is the default:
a regular-size default button. Variants and sizes are `uint8` enums that index
arrays, so that looking up a look costs nothing.

```go cmd/gallery/gallery/pages.go
	rexui.Button(k, "Save", rexui.ButtonOpts{Variant: rexui.ButtonPrimary})
	rexui.Button(k, "More", rexui.ButtonOpts{Variant: rexui.ButtonPlain})
	rexui.Button(k, "Remove", rexui.ButtonOpts{Variant: rexui.ButtonDestructive})
```

`ButtonDefault`, `ButtonPrimary`, `ButtonDestructive`, `ButtonPlain` and the
glass variants of a toolbar are `ButtonVariant`s; `theme.Size` is mini, small,
regular, large and extra large. The label follows `ui`'s rules: `"Name###id"`
shows `Name` and is identified by `id`, and a control in a form row is built
with an empty visible label (`"##Notifications"`) so that the row's label is its
caption:

```go cmd/gallery/gallery/pages.go
	g.named(rexui.Switch(k, "##Notifications", &g.sw, rexui.SwitchOpts{}).ID, "Notifications")
```

Every component is keyboard-operable and records its semantics. Its focus
ring sits outside the shape and shows for keyboard focus (a text field's,
whenever it is focused), never in an inactive window. A disabled control is
disabled by colour, not by opacity: wrap it in `BeginDisabled(true)` and
`EndDisabled()` as the notes app does with Add. And building a frame allocates
nothing: no closures, interfaces, maps or strings are made per frame.

## 3. Forms

`BeginForm` lays rows out as System Settings does: grouped inset sections of
40-pt rows with the label at the leading edge and the control at the trailing
one. `FormOpts.Aligned` is the other layout, an `NSGridView`'s: labels
right-aligned to a column. The form is a value; sections and rows are methods
that take the kit, and each pair is closed:

```go cmd/gallery/gallery/pages.go
	f.Section(k, "Choices")
// …
	f.Row(k, "Feature", rexui.RowOpts{})
	rexui.Checkbox(k, "Enable feature", &g.check, rexui.CheckboxOpts{})
	f.EndRow(k)
// …
	f.Row(k, "Notifications", rexui.RowOpts{})
	g.named(rexui.Switch(k, "##Notifications", &g.sw, rexui.SwitchOpts{}).ID, "Notifications")
	f.EndRow(k)
// …
	f.EndSection(k, "")
```

A form is built inside a scroll view whose content is a padded stack. The
gallery's `beginForm` and `endForm` are the whole recipe, scroll bars included:

```go cmd/gallery/gallery/pages.go
func (g *Gallery) beginForm(sc int, w, h float32) (base.ScrollView, rexui.Form) {
	k := g.k
	c := k.Context()
	k.NextSize(ui.Px(w), ui.Px(h))
	k.NextScrollInset(g.top)
	v := k.BeginScroll(defs[sc].title, &g.scroll[sc], base.ScrollVertical)
	g.scrollStart(v.Inner)
	_, sy := c.Scroll(v.Inner)
	g.under = sy > 0.5
	pad, bottom := theme.Snap(k.Pt(formPad)), theme.Snap(k.Pt(formPadBottom))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextPadding(pad, pad, pad, bottom)
	c.NextAxis(ui.AxisY)
	c.Push(c.BoxKeyed(0, ui.HashString(0, "form"), nil))
	return v, rexui.BeginForm(k, defs[sc].title, rexui.FormOpts{})
}
// …
func (g *Gallery) endForm(v *base.ScrollView, f *rexui.Form) {
	k := g.k
	rexui.EndForm(k, f)
	k.Context().Pop()
	k.CloseScrollContent(v)
	rexui.ScrollBars(k, v)
	k.CloseScroll(v)
}
```

Tab goes through the rows' controls in the order they are built.

## 4. Window chrome

`Toolbar`, `Sidebar`, `ScrollBars`, `Tabs`, `PathBar` and `TitlebarFor` are the
parts of a window as the Notes, Finder and Settings scenes have them, matched to
AppKit's frames within half a point (see the README of `rexui`). The notes
app's [section 3, 5 and 6](../GUIDE.md#3-layout) build them. Two
things are worth knowing:

- **The glass is the system's in a window.** `app.Config.Materials` lets the
  window host it, and the toolbar's capsules and the sidebar are drawn as live
  glass. Offscreen, under Reduce Transparency and off macOS they are drawn in the
  colour the system's glass measured.
- **A content pane's scroll view runs under the toolbar.** The notes app insets
  it by the band and draws the scroll edge with `rexui.ScrollBars`:

```go examples/notes/notes/notes.go
	k.CloseScrollContent(&v)
// …
	rexui.ScrollBars(k.Kit, &v)
// …
	k.CloseScroll(&v)
```

## 5. Lists and tables

`List`, `Table` and `Outline` are `NSTableView` and `NSOutlineView` over
`base.List`. They build only the visible rows, so a list of a million rows costs
what one screenful does. The selection is emphasised, in the accent with white
text, only while the list has the keyboard focus in the key window. The app
builds the rows between `l.First` and `l.Last`:

```go cmd/gallery/gallery/pages.go
	l := rexui.BeginList(k, "items", &g.list, listItems, rexui.ListOpts{})
	g.listFocus = l.Focused
	for r := l.First; r < l.Last; r++ {
		name := c.Fmt().Str("List item ").Int(r + 1).Bytes()
		l.Row(r, uint32(r), name)
		rexui.LabelBytes(k, name, rexui.LabelOpts{Color: l.Text()})
	}
	rexui.EndList(k, &l)
```

A file or a diff of thousands of lines is a `base.VList` of `rexui.CodeLine`
rows. `VListOpts.ContentW = rexui.CodeWidth(k, rexui.CodeColumns(src), opts)`
(once per file) makes every row as wide as the longest line, so the bands
span it, and the list scroll sideways to its end, all rows by one offset: a
wheel still scrolls it up and down, Shift with the wheel or a trackpad
sideways. Its horizontal bar comes from `Body` as the vertical one does
(`base.ScrollBoth`). A row that should not move sideways, a diff's file
header with its counts at the right, is built with `v.ItemFixedX(i)` instead
of `v.Item(i)`: as wide as the view, it stays put across while the lines
under it scroll.

## 6. System menus

On macOS a `Select`, a `MenuButton` and a context menu open the **system's**
menus: the pop-up that `NSPopUpButton` opens, with its look, keyboard handling and
VoiceOver support. Elsewhere the same call draws the open list. The app does
not change:

```go cmd/gallery/gallery/pages.go
	wk.Select("Format###format", &g.sel, formats)
// …
	wk.MenuButton("Menu", menuFile)
```

A native item's command reaches `Kit.Command` like a shortcut, as the notes
guide's section 8 explains. A question with a few buttons is not a component at
all: it is `app.Alert`, the system's `NSAlert`, and `rexui` does not wrap it
(the guide's section 7). `rexui.Dialog` is for custom content a native alert
cannot hold.

## 7. Restyling, and owning a component

A look is a **recipe** of tints: roles of the theme and, for the few colours no
role names, an opacity, so that light and dark, the accent and Reduce
Transparency cost nothing extra. Three levels of change:

- **A token:** change the theme before the kit is made. `rex.Theme()` returns a
  copy.
- **A recipe, for the whole app:** the variants' recipes are exported arrays,
  for example `rexui.ButtonRecipes[rexui.ButtonPrimary].Fill`. Change an entry
  and every button of that variant changes.
- **A component:** copy its file into your package and edit it.

**Words.** The few words the components supply themselves (a code block's
Copy button, a banner's "Warning:", the names of the sidebar's disclosure, a
sent message, the scroll bars, a terminal's scroll-to-bottom button, a web
view's loading line) are a `rexui.Strings`, English by default.
`rexui.SetStrings(rexui.StringsPtBR)`, before the first frame, switches them
to Brazilian Portuguese; another language is a `Strings` of the app's own,
whose empty fields stay English. They are read as they are each frame, at no
cost; a label or name the app passes always wins. `Strings.Menu` titles the
standard commands and menus a command with a role and no title of its own
gets ("Copiar", "Encerrar %s", "Editar", "Janela"), in drawn menus, the tray
and the native menu bar, which takes them when it is next built. An app on
`widget` alone sets the primitives' names with `base.SetStrings` and the
menus' with `menu.SetStrings`.

Rex is the default. `rexui.UseAppKitControls()`, called once before the first
frame, gives the push, icon and toggle buttons and the segmented controls
AppKit's rounded rectangles and blocks of the accent instead.

```sh
go run ./tools/rexui list                         # every component and the files it needs
go run ./tools/rexui add button ./internal/ui     # button.go, tint.go and strings.go, as package ui
```

`add` copies a component's file and the files it needs, rewrites the package
clause and the examples, and never overwrites a file unless given `-f`. The
copy builds on `widget/base` and `theme` as `rexui` does; `tools/rexui`'s test
copies each component alone into a scratch package and builds it.

## 8. What is in it

`go run ./tools/rexui list` prints them all; the README of `rexui` has each
one's variants, sizes and states, and how close it is to AppKit's.

| | |
|---|---|
| Buttons | `Button`, `ButtonIcon`, `IconButton`, `ToggleButton`, `HelpButton`, `Link`, `Disclosure` |
| Choices | `Checkbox`, `RadioGroup`, `Switch`, `Slider`, `SegmentedControl`, `Stepper`, `Select`, `Combobox`, `ColorWell` |
| Text entry | `TextField` (and `Secure`), `SearchField`, `TokenField`, `Composer` |
| Display | `Label`, `Badge`, `Avatar`, `ProgressBar`, `Spinner`, `Skeleton`, `EmptyState`, `Banner`, `Toast`, `Tooltip` |
| Containers | `Card`, `Form`, `Dialog`, `Popover`, `Tabs`, `Separator` |
| Chrome | `Toolbar`, `Sidebar`, `ScrollBars`, `PathBar` |
| Lists | `List`, `Table`, `Outline` |
| Chat | `MessageBubble`, `ChatListRow`, `DateDivider`, `TypingIndicator`, `CompletionList` |

The tests that keep it honest are in the package: `TestRecipesMatchAppKit` checks
the measurements against AppKit's committed numbers (under `UseAppKitControls`), and
`TestNoAllocationsPerFrame` builds every component and fails on an allocation.
