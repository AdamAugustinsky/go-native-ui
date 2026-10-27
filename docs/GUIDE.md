# Guide: build a notes app

This guide builds a small app, a list of notes, from an empty window to the
finished program in [`examples/notes`](../examples/notes). It is for a Go
developer who has not used the framework before.

The app is laid out as macOS 27's Notes window and follows the system's light or
dark mode and accent colour: a compact toolbar over the system's glass, a sidebar
listing the notes in groups, the open note beside it, a text field to add a
note, a switch for the order, a search, a menu with shortcuts and zoom, and a
confirmation that is the system's alert. It allocates nothing while it runs.
Its look is Rex, the framework's default ([Rex](guide/rex.md)).

**Every code block is an excerpt of the example.** The fence names the file, the
prose names the function, and a line `// …` marks lines left out. A test
(`TestGuideExcerptsAreTheExample`) fails if a block stops matching the source,
so the guide cannot drift from code that compiles and is tested. The API is
not stable yet and the project name is a placeholder.

The guide has chapters of its own for what the notes app does not need. Their
blocks are checked excerpts too, of programs that exist: three new examples (images,
rich text and services), each tested and allocating nothing in a steady frame, and the
demos that were already here (`cmd/windowsdemo`, `cmd/gallery`, `cmd/chromedemo`,
`cmd/imgbench`):

- [Rex](guide/rex.md): the default look, and the rules an app in it follows (the
  theme in your colours, transparency, controls, layout, the app's name). Read
  it before the first screen of a new app.
- [The app object](guide/app-object.md): `app.Main`, the handler, windows, quitting and
  threads (`cmd/windowsdemo`).
- [rexui](guide/rexui.md): the component library, forms, lists, system menus and
  owning a component (`cmd/gallery`).
- [Images](guide/images.md): the decoding cache, system icons and animations
  (`examples/images`).
- [Rich text](guide/richtext.md): spans, links, mentions and selection
  (`examples/richtext`).
- [Services](guide/services.md): notifications, the Dock, a status item, the
  Keychain, preferences, login items and window state (`examples/services`).
- [Packaging](guide/packaging.md): `gnui.json`, `gnui run`, `gnui package` and
  `gnui doctor`.

```sh
export PATH=$HOME/sdk/go1.27.1/bin:$PATH      # Go 1.27
export CGO_ENABLED=0
go run ./examples/notes                       # the window
go run ./examples/notes -o notes.png -dark    # render it offscreen
go test ./examples/notes/...                  # the tests of the example
```

| File | What it is |
|---|---|
| `examples/notes/main.go` | the command: window, offscreen render, `-bench`, `-exit-first` |
| `examples/notes/notes/notes.go` | the app: state, `Build`, menu model |
| `examples/notes/notes/script.go` | a scripted session (`widget/widgettest` steps) that gives the app the input a person would |
| `examples/notes/notes/notes_test.go` | the tests: render, interaction, zero allocations |
| `examples/notes/live/` | the tests that open a real window: the system's menus, alert and input |

## 1. The model

**Build runs every frame.** The app implements `Build(c *ui.Context)`. For each
frame that is drawn, it declares every box of the window again, from the
app's own state. There are no widget objects to create, update or destroy.
`ui` lays the boxes out and draws them after `Build` returns. A frame is drawn
only when something is owed (input, an animation, `Context.RequestFrame`, a
`platform.Wake`), so an idle window presents nothing and uses no CPU
([ARCHITECTURE.md](../ARCHITECTURE.md) §3 and §8, D10 and D22 in
[DECISIONS.md](../DECISIONS.md)).

**Signals come back by value.** `k.Button("Add")` builds the button and returns a
`widget.Signal` with `Clicked`, `Hovering` and the rest. Hit testing uses the
previous frame's layout, so a widget knows it was clicked while it is being
built, and you act on it right there with an `if`.

**Keys give boxes an identity.** Every box has a 64-bit key, the hash of its
parent's key and its label. The same key in the next frame is the same box, and
it keeps its hover, focus and scroll animation. Two siblings must not share a
label. `"Name###id"` shows `Name` and is identified by `id`, so the text may change
without losing state. List rows are keyed by the item id you give them, not by
their position.

**State belongs to the app.** Whatever outlives a frame is a value in your
struct: a `bool`, a `ui.TextBuffer`, a `widget.ListState`. A widget keeps nothing
except animation state in `ui`'s box cache. There are no callbacks and no
closures; you read the signal and change your state.

**Layers.** `app` runs the window and the frame loop. `ui` builds, lays out
and draws boxes. `theme` and `theme/rex` are the look. `menu` is the command
model. `widget` ties them together in a `Kit` per window (D28). Everything runs
on the main thread.

## 2. An empty window

`main.go`, function `runWindow`. `app.Run(cfg, b)` is `app.RunWith(cfg, b,
app.RunOptions{})` without the result; the example uses `RunWith` so that
`-exit-first` can print the startup timeline.

```go examples/notes/main.go
func runWindow(exitFirst, verbose bool, pref theme.Preference, scrolled bool) error {
	n, err := notes.New()
	if err != nil {
		return err
	}
	n.Mode, n.Scrolled = pref, scrolled
	res, err := app.RunWith(notes.Config(), n, app.RunOptions{ExitFirst: exitFirst, Verbose: verbose})
```

`notes.go`, function `Config`. The size is in points, the pixels per point come
from the display. `Titlebar` and `TrafficLights` give the window a custom macOS
titlebar for the toolbar to fill, with the system's window buttons where the
toolbar's style (`rexui.TitlebarFor`) puts them; `Materials` lets the window
host the system's live glass for the sidebar and the toolbar's capsules.

```go examples/notes/notes/notes.go
func Config() app.Config {
	cfg := app.Config{Title: "Notes", Width: windowW, Height: windowH, MinWidth: 720, MinHeight: 360, Clear: [4]float32{1, 1, 1, 1}, Materials: true}
	cfg.Titlebar, cfg.TrafficLights = rexui.TitlebarFor(theme.ToolbarCompact)
	return cfg
}
```

`Init` runs once, after the window, the renderer and the text system exist and
before the first frame. It makes the window's kit: the widget set bound to this
window's `ui.Context`, its text system and a theme. `rex.Theme()` is the
default look. `Materials` says whether this window hosts glass; elsewhere (and
under Reduce Transparency) the glass is drawn in its measured colour. `Reserve`
and `ReserveRuns` size the caches up front, which is what keeps the frames
allocation-free (section 10).

```go examples/notes/notes/notes.go
func (n *Notes) Init(a *app.App) error {
	n.a = a
	n.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	n.k.SetPreference(n.Mode)
	n.k.Materials = a.Win != nil && a.Win.HasMaterials() // the system's glass in a window
	n.k.SetMenu(n.model)
	a.SetMenu(n.model) // the native menu bar on macOS
	n.addIcons()
	a.UI.Reserve(2048)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	return nil
}
```

`Build` brackets everything with `k.Begin(c)` and `k.End()`. `Begin` resolves the
theme for the window's scale, steps the light/dark crossfade and matches the
menu shortcuts (section 8). `End` builds the open context menu, if any. The kit
keeps its context for good, so `k.Context()` reaches it from anywhere, also
between frames.

```go examples/notes/notes/notes.go
func (n *Notes) Build(c *ui.Context) {
// …
	k := n.k
	k.Begin(c)
	n.answers()
	n.commands()
	n.rebuild()
// …
	k.End()
}
```

The first frame is built and drawn before the window is shown, so the window
appears with its content (D21).

`Run` is one window. An app that opens more than one, that stays when its last
window closes, that is asked to quit by the system or answers a click on its Dock
icon is run with `app.Main` and a `Handler`: see [the app object](guide/app-object.md).

## 3. Layout

This window places its panes itself: the sidebar's glass runs to the top of the
window under the toolbar, which is a band over both panes, so each pane is a
floating box at a given place and size. `theme.At` sets the position and size of
the next box in pixels and `base.Floating` makes it float; `Push` and `Pop`
make it the parent of what is built meanwhile. Sizes come from the theme in
points (`r.Window.Toolbars`) and the app's own constants, times the scale, and
`theme.Snap` rounds them to whole pixels so that edges are sharp.

```go examples/notes/notes/notes.go
	if sbW > 0 {
		theme.At(c, 0, 0, sbW, H)
		c.Push(c.Box(base.Floating, "sidebar"))
		n.sidebar(tbH)
		c.Pop()
	}
	theme.At(c, sbW, 0, W-sbW, H)
	c.NextBG(r.Colors[theme.TextBackground])
	c.Push(c.Box(base.Floating|ui.DrawBackground, "content"))
	n.content(W-sbW, H, tbH)
	c.Pop()
	n.toolbar(tbH, sbW)
```

- **The toolbar is built last**, so that it draws over the panes. The content
  pane is as tall as the window: its scroll view is inset by the band
  (`Kit.NextScrollInset`, `ToolbarOpts.ContentEdge`), so the note starts below
  the band and scrolls up under it, where `rexui.ScrollBars` draws the scroll
  edge. The content's scroll state (`n.under`) is the previous frame's.
- **Inside the panes** the widgets flow: `Row`, `Column` and `Container` stack
  children along an axis with the theme's gap and padding, `NextWidth` and
  `NextSize` set one property of the next widget only (`widget.Fill` takes the
  room its siblings leave, `Kit.Fixed(pt)` is a fixed size), and `NextJustify`
  and `NextCross` place children along and across the axis.
- **The CSS you would reach for** is there, with the same meaning:
  rows and columns with `NextJustify`, `NextCross` and `NextGap` (flexbox);
  `ui.Grow(weight)` sizes a child from the room its siblings leave
  (`flex: weight`; one `Grow(1)` is a spacer); `NextMinSize` and
  `NextMaxSize` bound a box (`min-width`, `max-width`); `NextAlignSelf`
  places one child across the axis (`align-self`); `BeginFlow` wraps
  children onto lines (`flex-wrap`); and `BeginGrid` lays them in equal
  columns, a fixed number or as many as fit (`grid-template-columns:
  repeat(auto-fill, minmax(min, 1fr))`), each row as tall as its tallest
  child. A layout that needs `TextWidth` or a division by a column count is
  usually one of these.
- **Things laid over others** (an icon over a round button, a badge in a
  corner, a spinner over an image) are placed, not computed: `c.NextCenter()`
  centres the next box in its parent at whatever size it takes, and
  `c.NextAnchor(ui.AnchorBottomRight, x, y)` pins it to a corner or an edge
  with insets, as CSS's `position: absolute` with `inset`; `c.NextOffset(dx, dy)`
  nudges a box from its laid-out place without moving its siblings, as
  `position: relative`. Arithmetic such as `theme.At(c, (d-w)/2, (d-h)/2, w, h)`
  goes wrong as soon as `w` is not the size the box really takes; the layout
  audit (`widgettest.AuditLayout`) reports an icon off its button's centre,
  layered or not, and anything outside its parent.
- **The menu bar.** Where the system draws none (Windows, Linux) the app's own
  is the window's first strip and the panes lie below it. `Kit.MenuBar` builds
  nothing on macOS.
- **Units.** The widget API takes points (`NextPad(10)`); boxes are in pixels,
  as everywhere in `ui`.
- **Begin and End pair up.** `BeginContainer` ends with `EndContainer`, `Push`
  with `Pop`. The label identifies the container among its siblings.

The window's content is the rexui components the pane is made of:
`rexui.Toolbar`, `rexui.Sidebar` and `rexui.ScrollBars` are built from `base`'s
primitives and the theme's measured recipes, so that the window matches AppKit's
Notes scene (docs/design/liquid-glass-reference.md). The other components, the
forms and lists, and how to restyle a component or copy it into your own package are
in [the rexui chapter](guide/rexui.md).

## 4. State, a text field and a button

The state of the app is one struct. The notes live in an array of fixed-size,
pointer-free records, so adding a note never allocates; the limit is the
overflow policy, and the Add button is disabled when it is reached. A note's
`id` is never reused, so the list's selection follows the note, not its place.
The text field's state is a `ui.TextBuffer` the app owns.

`notes.go`, type `Notes` and its limits.

```go examples/notes/notes/notes.go
const (
	maxNotes = 64
	maxLen   = 80   // bytes of a note's title
	maxBody  = 2048 // bytes of its text
	maxLine  = 120  // bytes of the second line the list shows
)
// …
// note is one note: pointer-free, so the array of them is too.
type note struct {
	id     uint32 // never 0, and never reused: the list's selection follows it
	group  uint8
	tn, dn uint8  // bytes of the title and of the list's second line
	bn     uint16 // bytes of the text
	title  [maxLen]byte
	detail [maxLine]byte // the date and the start of the text, as the list shows them
	body   [maxBody]byte // the text; a line starting with "## " is a heading
}
```

```go examples/notes/notes/notes.go
	input    ui.TextBuffer      // the text field that adds a note
	field    ui.Key             // the text field's key, to focus it
// …
	newest   bool    // the Switch: show the newest note first
```

`New` sets the buffer's `Max`, which cuts typed or pasted text at a character
boundary, and makes the notes the Notes scene lists, the newest open.

```go examples/notes/notes/notes.go
func New() (*Notes, error) {
	n := &Notes{newest: true}
	n.input.Max = maxLen
	n.input.Reserve(maxLen)
	n.query.Max = maxLen
	n.query.Reserve(maxLen)
// …
	n.side.Sel = n.notes[n.count-1].id
```

`content`, the bar below the open note, holds the field and the button.
`TextField` edits the buffer in place (caret, selection, copy, cut, paste, undo
and the input method are all handled) and returns a `FieldSignal`, which carries
the field's key (`f.Key`, the same in every frame, unlike `f.ID`).
`BeginDisabled(true)` draws what is inside dimmed, takes it out of the Tab order
and makes it deaf to the pointer, the keyboard and any activation already
queued. The pair can wrap a widget conditionally. Enter in the field or a click
on Add adds the note, and `Kit.Focus` puts the keyboard back in the field.

```go examples/notes/notes/notes.go
	k.NextWidth(widget.Fill)
	f := k.TextField("New note", &n.input, "Add a note…")
	n.field = f.Key
	k.BeginDisabled(n.input.Len() == 0 || n.count == maxNotes)
	added := k.ButtonKind("Add", widget.ButtonPrimary).Clicked
	k.EndDisabled()
	k.LabelBytesStyle(c.Fmt().Int(n.count).Str(" notes").Bytes(), widget.TextMuted)
	k.EndContainer()
// …
	if added || f.Submitted {
		n.add()
		k.Focus(n.field)
	}
```

```go examples/notes/notes/notes.go
func (n *Notes) add() {
	if n.input.Len() == 0 || n.count == maxNotes {
		return
	}
```

## 5. The sidebar

The list of notes is `rexui.Sidebar`: a source list as macOS 27 draws one, on
the system's glass, with group headers and rows of two lines. The app describes
its rows every frame as a slice of `rexui.SidebarItem` in display order, a
header being an item with `Header` set, and the sidebar edits the selected
row's id in its state. A press or the keyboard (Up and Down) changes the
selection and reports `Changed`; the sidebar takes the keyboard when it is
pressed, and the selection is the accent while it has it and grey otherwise.

`rebuild` makes the rows: the notes in the order the Switch asks for, those the
search lets through, each group under its header. The items are views of the
notes' own bytes (`str`) in an array the app owns, so nothing is allocated; the
list is rebuilt every frame, which is as cheap as a screenful.

```go examples/notes/notes/notes.go
func (n *Notes) rebuild() {
	q := n.query.Bytes()
	rows := n.items[:0]
	group := -1
	for r := 0; r < n.count; r++ {
		i := r
		if n.newest {
			i = n.count - 1 - r
		}
		nt := &n.notes[i]
		if !containsFold(nt.titleText(), q) || len(rows)+2 > len(n.items) {
			continue
		}
		if int(nt.group) != group {
			group = int(nt.group)
			rows = append(rows, rexui.SidebarItem{ID: headerID + uint32(group), Title: groupTitles[group], Header: true})
		}
		rows = append(rows, rexui.SidebarItem{ID: nt.id, Title: str(nt.titleText()), Detail: str(nt.detail[:nt.dn])})
	}
	n.rows = rows
// …
}
```

`sidebar` builds the pane. It gives the keyboard to the list at the start, as
the Notes scene has it.

```go examples/notes/notes/notes.go
func (n *Notes) sidebar(tbH float32) {
	k := n.k
	c := k.Context()
	s := rexui.Sidebar(k.Kit, "Notes", &n.side, n.rows, rexui.SidebarOpts{Top: tbH, RowH: 52, HeaderH: 24})
	if !n.focused { // the keyboard starts in the list, as in the Notes scene
		c.SetFocus(c.KeyOf(s.ID))
		n.focused = true
	}
```

Removing a note moves the later ones up, which is why a note is told by its id
and not by its place.

```go examples/notes/notes/notes.go
func (n *Notes) removeSelected() {
	i := n.find(n.side.Sel)
	if i < 0 {
		return
	}
```

## 6. The note, a switch and a status line

The open note is rich text in a scroll view: the title in Title 1, the headings
in 15-pt semibold, the rest in body type, as the Notes scene's text view sets
them. `setText` makes it from the note when the note changes, into buffers the
app reserved in `New`; `BeginScroll` and `rexui.ScrollBars` are the scroll view
and the scroll bars of the system's style (legacy, taking room, or overlay).
Links, mentions, selection across paragraphs and the rest of the rich text are in
[the rich text chapter](guide/richtext.md).

```go examples/notes/notes/notes.go
	c.NextSize(ui.Pct(1), ui.Children(1))
	// the text ends in a newline: NSTextView lays out the empty line after it
	// (its extra line fragment), a body line more below the text
	c.NextPadding(px, py, px, py+r.Styles[theme.StyleBody].Line)
	page := c.Box(0, "page")
	if n.Scrolled && n.textID != 0 { // keep the end of the note, as the Notes scene's picture has it
		if pr := c.Rect(page); pr[3] > pr[1] {
			end := float64(max(top+pr[3]-pr[1]-h, 0))
			switch {
			case n.pinned != 0 && math.Abs(sy-n.pinned) > 0.5: // somebody scrolled it
				n.Scrolled = false
			case math.Abs(sy-end) > 0.5: // the layout changed (the scroll bars' style)
				c.SetScroll(v.Inner, 0, end)
				n.pinned = end
				c.RequestFrame()
			}
		}
	}
	c.Push(page)
	if i := n.find(n.side.Sel); i >= 0 {
		n.setText(&n.notes[i])
		c.NextSize(ui.Pct(1), ui.TextSize(0))
		c.NextTextColor(r.Colors[theme.Text])
		c.RichText(ui.KeyN(keyText, uint64(n.textID)), n.text, n.spans, &n.styles, ui.RichOpts{})
	} else {
		n.textID = 0
		rexui.Label(k.Kit, "No Note Selected", rexui.LabelOpts{Style: rexui.TextTitle1, Color: rexui.Of(theme.Label3)})
	}
	c.Pop()
```

`Switch` edits a `bool` of the app's and reports `Changed`. It is a regular-size
switch, the same one `NSSwitch` is. The status line is dynamic text: `c.Fmt()`
builds it in the frame's arena and `LabelBytesStyle` copies the bytes
(section 10).

```go examples/notes/notes/notes.go
	if k.Switch("Newest first", &n.newest).Changed {
		n.rebuild()
	}
```

The toolbar is `rexui.Toolbar`: its items are glass capsules, with the window's
traffic lights before them and the title after the capsule before it.
`NextPane` moves the items on to the content pane, as an `NSToolbar`'s tracking
separator does, `Flexible` is a flexible space, and `Search` is the expanded
search field, which the app reads as a `TextBuffer` like any other.

```go examples/notes/notes/notes.go
func (n *Notes) toolbar(tbH, sbW float32) {
	k := n.k
	r := k.Theme()
	o := rexui.ToolbarOpts{Style: theme.ToolbarCompact, Title: "Notes", Panes: [2]float32{sbW, 0},
		ContentUnder: n.under, Traffic: n.Traffic, BandFill: r.Colors[theme.TextBackground], ContentEdge: true}
	tb := rexui.BeginToolbar(k.Kit, "Toolbar", o)
	if tb.Item(n.ic.sidebar, "Sidebar").Clicked {
		n.hidden = !n.hidden
	}
	k.BeginDisabled(n.Selected() == 0)
	remove := tb.Item(n.ic.trash, "Remove…").Clicked
	k.EndDisabled()
	if sbW > 0 {
		tb.NextPane()
	}
	tb.Title()
	compose := tb.Item(n.ic.compose, "New Note").Clicked
	tb.Flexible()
	tb.Search("Search", &n.query, "Search", 0)
	tb.End()
```

## 7. A confirmation: the system's alert

Remove is a destructive action, and it is confirmed with the system's alert
(`NSAlert`) where there is one. `app.App.Alert` queues an alert sheet for the
window and returns a ticket; the sheet is shown after the frame, at a safe point,
and its answer comes back with `TakeDialog` in a later frame. The first button
is the default, and the destructive one is tinted. Where the system has no
alerts, or the call fails, `Alert` returns an error and the app shows its own
drawn dialog instead.

```go examples/notes/notes/notes.go
func (n *Notes) askRemove() {
	if n.Selected() == 0 || n.ticket != 0 || n.confirm {
		return
	}
```

`answers` reads the answer at the start of the next frame.

```go examples/notes/notes/notes.go
func (n *Notes) answers() {
	for r, ok := n.a.TakeDialog(); ok; r, ok = n.a.TakeDialog() {
		if r.Ticket != n.ticket {
			continue
		}
```

The drawn dialog is a `Dialog`: an in-window modal, open while the app's `bool`
is true. Build its content only when `Visible`. Everything under it is inert:
no pointer, scrolling, text input or shortcut reaches the window, and Tab stays
inside the dialog. An `Action` closes the dialog and returns true once. Escape
cancels, also from a text field in the dialog; with dialogs stacked only the
topmost takes Enter and Escape. The first `ButtonPrimary` is the default action:
it has the focus when the dialog opens, and Enter chooses it. Build Cancel first,
so that Enter on a focused Cancel is Cancel's.

```go examples/notes/notes/notes.go
	d := k.BeginDialog("remove", "Remove note?", &n.confirm)
	if d.Visible {
		k.Paragraph(c.Fmt().Str("“").Str(str(n.selectedTitle())).Str("” will be removed. This cannot be undone.").String())
		d.BeginActions()
		d.Action("Cancel", widget.ButtonDefault)
		if d.Action("Remove", widget.ButtonPrimary) {
			n.removeSelected()
		}
		d.EndActions()
	}
	k.EndDialog(&d)
```

The button that opens it is called "Remove…" and the dialog's action "Remove".
The labels differ so that a test, or an assistive technology, finds each by name.
The benchmark's window takes the drawn dialog always (`drawnOnly`): nobody
answers a system alert in a loop.

## 8. Commands, menus and shortcuts

A command is an id, a title and a shortcut. `menu.NewModel` takes the commands,
the menus that show them and which menus are in the bar, and checks them. `Primary`
means ⌘ on macOS and Ctrl elsewhere. The View menu's zoom commands are
`menu.StandardCommand`s: a model with commands of the zoom roles zooms the window
it was given to (`app.SetMenu`), with ⌘+, ⌘− and ⌘0.

```go examples/notes/notes/notes.go
func newModel() (*menu.Model, error) {
	primary := func(k ui.KeyCode, m ui.Mods) menu.Shortcut { return menu.Shortcut{Key: k, Mods: m, Primary: true} }
	cmds := []menu.Command{
		{ID: cmdNew, Title: "New Note", Shortcut: primary('N', 0)},
		{ID: cmdRemove, Title: "Remove…", Shortcut: primary(ui.KeyBackspace, 0)},
		{ID: cmdNewest, Title: "Newest First", Shortcut: primary('N', ui.ModShift)},
		menu.StandardCommand(cmdZoomIn, menu.RoleZoomIn),
		menu.StandardCommand(cmdZoomOut, menu.RoleZoomOut),
		menu.StandardCommand(cmdActualSize, menu.RoleActualSize),
	}
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	menus := []menu.Menu{
		{Title: "Note", Items: []menu.Item{item(cmdNew), item(cmdRemove)}},
		{Title: "View", Items: []menu.Item{item(cmdNewest), {Kind: menu.ItemSeparator}, item(cmdZoomIn), item(cmdZoomOut), item(cmdActualSize)}},
	}
	return menu.NewModel(cmds, menus, []int{0, 1})
}
```

`Init` gave the model to the kit and to the app (`SetMenu`). `Kit.Begin` then matches the
shortcuts against the frame's keys, and `Kit.MenuBar` (in `Build`) draws the bar. A
command fires once, whether it came from a shortcut or a menu item, and
`Kit.Command(id)` reports it. A command's enabled and checked state is set each
frame.

```go examples/notes/notes/notes.go
func (n *Notes) commands() {
	k := n.k
	if k.Command(cmdNew) {
		k.Focus(n.field)
	}
```

**One key, one handler.** A key event is offered in a fixed order and handled at
most once: the input method, then the focused text field, then the commands, then
the widgets' own keys. While a field has the focus it takes every key but Tab
unless the primary modifier is down, and with it A, C, X, V, Z, the arrows,
Home, End, Backspace and Delete. So ⌘N works while you type, but ⌘Backspace in
the field means "delete to the start of the line", and it asks to remove a note
only when the focus is elsewhere. A modal dialog holds back every command and shortcut
except the edit commands for a text field in it and the commands marked
`menu.Command.InModal`; its own keys are taken by its widgets.

**Where the menu shows.** On macOS the menu bar is the system's (D29): `app.SetMenu`
builds it from the model, between the application menu (About, Services, Hide,
Quit) and the Window and Help menus, with a standard Edit menu that acts on the
focused field because the model has none. `Kit.MenuBar` then draws nothing, and
context menus, `MenuButton` and `Select` open native menus. A native item's
command reaches `Kit.Command` like a shortcut; AppKit matches the key
equivalents, so a key fires its command once, and still after the focused field:
a chord the field takes (⌘Z in it) never fires a menu item. The system's items
(Quit, Hide, Minimize, Zoom, Close) follow the model and the modal rule too:
a command with their role decides them, and a dialog holds them back unless the
command is `InModal`. On Windows and Linux `Kit.MenuBar`
draws the bar inside the window. Commands with edit roles (`menu.RoleCopy` and
the like) go in an app's own Edit menu, where it wants one; they act on the
focused field, or reach the app when no field has the focus.

A native menu is also what `MenuButton`, `Select` and a context menu open on macOS
([rexui](guide/rexui.md#6-system-menus)); with more than one window each window sets its
own model and the bar follows the key window ([the app object](guide/app-object.md#6-one-menu-per-window)).

## 9. Light and dark

Nothing in the example chooses a mode. A kit follows the system by default
(`theme.System`): the first frame is already drawn in the system's light or dark
mode and accent colour (on Linux, when the desktop portal answers within 25 ms,
which a running portal does; a portal that has to start on demand gives a light
first frame that crossfades to dark), a change while the app runs crossfades
with no polling, and the OS-drawn parts of the window (the Windows title bar, the macOS traffic
lights) follow what the app shows (D32). An app that wants a mode of its own calls
`Kit.SetPreference` with `theme.ForceLight` or `theme.ForceDark`; the gallery has a
Select for it. Colours come from the theme's palette, never from constants in
the app, which is why every widget works in both.

An offscreen window has no system to ask, so the test hands the context the
appearance before the first frame, as a window's first frame would find it:

```go examples/notes/notes/notes_test.go
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Dark: dark, Known: true})
```

## 10. Performance rules

A steady-state frame allocates nothing, once the working set is warm: the boxes,
text runs, selection and undo history that the app's use reaches. The example
proves it twice, offscreen in a test and in a window with the benchmark
(section 11). These are the rules that keep it true.

1. **No closures and no interface calls per row.** A `Row` is a call in a plain
   loop. A callback that captures something allocates; a method on your state does
   not. Use signals and `if`.
2. **Strings and bytes.** A label is a `string`: a constant costs nothing. Dynamic
   text is built with `c.Fmt()` (`Str`, `Int`, …), which appends to a frame arena
   that `Begin` resets, then `String()` or `Bytes()`. The result is valid until
   the next frame; do not keep it. Text you own as `[]byte` (a note's text) goes
   straight to `LabelBytes`, which copies it, and a `string` the app needs of bytes it
   owns for as long as they do is a view (`str`, `unsafe.String`), as the sidebar's
   rows are. Never build text with `fmt.Sprintf`, `+` or `string(b)` in `Build`.
3. **`Reserve` every growable store, and say what happens when it is full.**
   `Context.Reserve(n)` sizes the box cache for n live boxes. `Text.ReserveRuns` sizes
   the shaped-run cache so that new strings never grow it. A `TextBuffer` grows only
   past its high-water mark, and
   `Max` bounds it. Undo history is bounded and drops the oldest. The note array is
   fixed, and the Add button is disabled when it is full. Pointer-free fixed
   records, like `note`, are what make a store simple.
4. **State is app-owned values.** A struct of plain fields, arrays and the widgets'
   state types. No maps that grow, no slices appended to in `Build`.
5. **Boxes with equal labels are told apart by order.** A `string` label is
   identified by its text; a `LabelBytes` is identified by its place among the
   container's other `LabelBytes`, so its text can change every frame and any
   number of them can share a container. Keep their order from frame to frame.
6. **Do not keep a `ui.ID` between frames.** IDs and rectangles belong to a frame;
   keys last. The example keeps the text field's `ui.Key` (`FieldSignal.Key`) to
   focus it.
7. **Animations ask for frames.** `Context.Animating` is what keeps the loop
   running; when nothing animates the app sleeps. The ordinary widgets handle it. If
   you animate a value of your own, call `Context.RequestFrame` while it moves.
8. **Read the last layout through the context.** `Rect`, `RectOf`,
   `ScrollExtent`, `ShownRect` and `ListRange` give the previous frame's layout.
   When the frame's own layout changes what they gave (content that grew in
   that frame), `End` asks for one more frame, at most two in a row
   (`Context.Settling`), so the window never rests on a stale layout. A
   rectangle you copy into your own state and use later is not seen: read it
   again in the next `Build`, or call `RequestFrame` when it changes.

## 11. Testing and benchmarking

`widget/widgettest` drives an app like a person would: it moves the pointer, presses,
types and presses keys, through `ui.Input` as translated platform events would. A
step finds its target by role and name in the semantic tree of the last frame
(`ui.Context.Semantics`), so it says "the Add button", not a coordinate, and it waits
on conditions rather than frame counts. `Click`, `Key`, `Type`, `WaitFor`, `WaitGone`,
`Settle` and the rest build steps; `Step.Nth` picks the n-th match and `Step.With`
holds modifiers. A `widgettest.Player` runs a list of steps as an `app.Scripter`, and
a `widgettest.Rig` is a window the tests drive: `Open` runs the app offscreen on the
real text system and renderer, `NewRig` on a fake text system without a GPU. `Play`
runs lists of steps one after another (the rig keeps scripting going between them),
and `Find`, `Must`, `Tree` and `Dump` look into the semantic tree.

`widgettest.AuditLayout` (or `Rig.AuditLayout`) runs `ui.Context.Audit` on the last
frame and fails the test for what it finds: a `NextAlign` on a box without text,
a child outside its parent, overlapping siblings, content under an opaque floating
box, a cell off its column header's edge, an icon off the centre of its capsule. A
deliberate case is marked with `NextAuditExempt` where it is built, or accepted in
the test with a `widgettest.Allow` that says why. The example, the gallery's scenes
and the other apps run it on their offscreen frames.

`script.go` holds the example's session. The tests run pieces of it, and the
benchmark loops the whole of it.

```go examples/notes/notes/script.go
func session() []step {
	prim, shift := ui.PrimaryMod(), ui.ModShift
	return []step{
		idle(6),
		// type and undo, then add two notes: Enter in the field, then the Add button
		click(ui.RoleTextField, "New note"), typeText("oops"), idle(2), key('Z', prim), idle(2),
		typeText("Water the plants"), idle(2), key(ui.KeyEnter, 0), idle(2),
		typeText("Book a table"), idle(2), click(ui.RoleButton, "Add"), idle(2),
```

`notes_test.go`. The interaction test types, adds, opens a note, removes it and
confirms with the drawn dialog (offscreen there is no system alert; the test with
`FakeDialogs` answers the alert's buttons instead):

```go examples/notes/notes/notes_test.go
	r.play(click(ui.RoleTextField, "New note"), typeText("Buy bread"), key(ui.KeyEnter, 0))
	// …
	r.play(click(ui.RoleListItem, "Buy bread"))
	// …
	r.play(click(ui.RoleButton, "Remove…"), waitFor(ui.RoleDialog, ""))
	// …
	r.play(click(ui.RoleButton, "Remove"), waitGone(ui.RoleDialog, ""))
```

What only a real window has is tested in `examples/notes/live`: the first frame
with the system's glass and the content in it, the Edit menu's items acting once
on the focused field, the real `NSAlert` sheet, the focus ring that follows Tab
and not the mouse, the appearance flip, the zoom commands, a trackpad scroll and
an idle window that draws nothing. They need the main thread (`widgettest.RunMain`)
and an unlocked screen, and skip where no window can be opened.

The allocation test measures exactly, with `runtime.ReadMemStats` over whole sessions
after a warm-up and a collection, as `app.RunBench` does (D11). It passes on the first
window with no allocation at all. A real per-frame allocation is in every window;
the runtime's own background allocations (a new OS thread, a timer) miss most of
them.

```go examples/notes/notes/notes_test.go
	for b.Cycles() < 2 { // warm-up: whole sessions
		o.Frame()
	}
	runtime.GC()
```

`main.go`, `-bench`, is the same session on a real window through `app.RunBench`,
which reports allocations, CPU per frame and pacing. `-gate` makes it exit with
status 3 if anything is off, and CI runs it on every OS:

```sh
go run ./examples/notes -bench                         # JSON report
go run ./examples/notes -bench -gate                   # exit 3 on any allocation, CPU over budget or unpaced frames
go run ./examples/notes -bench -gate -allow-unpaced    # the same on a machine whose display is asleep
go run ./examples/notes -exit-first -v                 # startup: exec → first frame on glass
```

### Driving a running app from outside: the agent bridge

A coding agent, or a script, can drive and inspect the running app without screen
recording or synthetic system events. Start a development build with
`GNUI_INSPECT=<socket>`; the app listens on a Unix socket at that path (never TCP)
and answers one JSON request per line. With the variable unset there is no listener,
no goroutine and no per-frame cost, and a release build (`gnui package`, tag
`gnui_release`) does not contain the bridge at all.

The socket must be in a directory you own and keep private: it is refused if the
directory is writable by its group or others, has an ACL, or sits under a directory
that others could rename it away in (every ancestor must be yours or root's, and
closed to others unless sticky, and on macOS must carry no ACL that allows anyone
to write, add, delete or change ownership; a deny-only ACL such as the "everyone deny
delete" on home folders is fine). `/tmp` itself is therefore always refused: make a
directory of your own first (`mktemp -d` gives a `0700` one under `$TMPDIR`). The
socket is made mode 0600 and published atomically, and a lock file `<socket>.lock`
next to it, held for as long as the app runs, tells a running app from a dead one's
leftover: a second app on the same path is refused while the first lives, and a
socket left by an app that died is replaced. A shot may be written only into a
directory that passes the same checks (`mktemp -d` does), and not into the socket's
directory, so keep one directory for the socket and another for the pictures. The
bridge works on macOS and Linux; on Windows it is off for now and says so in the log
(`errors.ErrUnsupported`), and `shot` of a live window is not supported on Linux yet.

What it defends against is other local users and what a client sends; it assumes
nobody but you can change the socket and shot directories. It does not defend against
your own processes, which could replace the socket anyway. And it is a development
tool, not made for a person to use the window while an agent drives it: a real Shift
pressed while a synthetic one is held, or the real pointer entering or leaving during
a `do`, can be overridden when the steps end (modifiers, pointer and focus are put
back as they were) until your next real event corrects it.

```sh
SOCK=$(mktemp -d)/notes.sock          # a private directory, and the socket in it
OUT=$(mktemp -d)                      # and another for the shots
GNUI_INSPECT=$SOCK go run ./examples/notes &                 # the app
gnui inspect $SOCK windows                                   # window 1 "Notes" 1800x1200 px at 2x, frame 4
gnui inspect $SOCK tree -role Button                         # [22] Button "Add" [disabled] (1544,1124,1640,1172) …
gnui inspect $SOCK do focus TextField "New note"             # a step by role and name
gnui inspect $SOCK do type "Water the plants"
gnui inspect $SOCK do click Button Add                       # ok: 1 step(s) applied in 4 frame(s)
gnui inspect $SOCK do key cmd+shift+n
gnui inspect $SOCK shot $OUT/notes.png                       # the current frame, offscreen
```

`tree` prints each node's id, role, name, value, states (`focused`, `selected`,
`disabled` …) and bounds in window pixels, indented by depth; `-role` and `-name`
filter it, `-json` prints the raw response. `do` takes the steps of `widgettest`:
`click`, `double_click`, `right_click`, `move`, `focus`, `type`, `key`
(`enter`, `cmd+shift+n`, `primary+z`), `scroll`, `wait_for`, `wait_gone` and `settle`,
with `-nth` for the second of two buttons of one name, and `-steps '<json list>'` for
several in one request. A step is queued to the main thread and plays at a frame
safe point, as the benchmark's script does; the reply comes once the steps have
been applied and two more frames built from their result, so the next `tree` shows
what they did. A step that cannot do what it says (no such node, a wait that ran
out) answers with an error that names the step. Errors are JSON with a `code`
(`bad_request`, `too_large`, `step_failed`, `timeout`, `bad_path`, `busy` …) and a
message; a request over 64 KiB is refused. The protocol is in
`internal/inspectwire`; `gnui inspect -h` lists the commands.

## 12. Building, shipping and platforms

| OS | Minimum | Graphics | Needs at run time |
|---|---|---|---|
| macOS | 14 on Apple silicon (arm64; Intel Macs are not supported, D55) | Metal | nothing beyond the system frameworks |
| Windows | 10 (amd64, arm64) | Direct3D 11 (WARP without a GPU) | nothing beyond the system DLLs |
| Linux | glibc (amd64, arm64); Wayland or X11 | EGL with GL or GLES | `libEGL`, `libGL` or `libOpenGL`, `libxkbcommon`, `libwayland-client` and `libwayland-egl` (Wayland) or `libX11` (X11), `libfontconfig` with at least one font; optional `libwayland-cursor`, `libXcursor` |

- **No cgo.** The system libraries are called without a C toolchain: Objective-C on
  macOS, COM and Win32 on Windows, `dlopen` on Linux (D2, D3). The build needs only Go.
  `CGO_ENABLED=0` is the setting for every target, and musl is not supported.
- **Cross-compiling works from any host**, with no SDK:

  ```sh
  CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o notes.exe ./examples/notes
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o notes-linux ./examples/notes
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o notes-mac ./examples/notes
  ```

- **Release builds** use `-trimpath -ldflags="-s -w"`. CI builds the example this way for
  five targets (darwin/arm64, and windows and linux on amd64 and arm64) and fails if any
  binary is over 10 MB. A build for darwin/amd64 stops with an error naming D55.
- **Linux backend:** Wayland when `WAYLAND_DISPLAY` is set and the connection works, X11
  otherwise; `GNUI_BACKEND=wayland` or `x11` forces one.
- **Startup:** the budget is 150 ms from process start to the first complete frame on the
  display (D21). On macOS the first launch of a new binary also pays Gatekeeper's scan,
  so measure from the second.
- **A macOS bundle** is what notifications and login items need, and what you ship. `gnui run`
  builds, signs and launches one for development and `gnui package` makes the release:
  [packaging](guide/packaging.md). What an app asks of the system once it has one is
  [services](guide/services.md).
- **macOS materials and native views.** With `app.Config.Materials`, a box marked with
  `c.Material` (sidebar, inspector, toolbar capsule, ...) shows the system's live glass or
  material where the app paints nothing; under Reduce Transparency it is an opaque fill
  of the colour you give. `c.Host` places a native view the app made with
  `App.Win.NewHost`. Native views lie above everything the app draws, so a host is hidden
  while a drawn popup or tooltip overlaps it and while any modal is open: place popups
  that belong to a host beside it, not over it. Materials lie below the app's pixels and
  cannot blur them. Offscreen renders show hosts as grey placeholders. Elsewhere these
  calls do nothing and `NewHost` returns `platform.ErrNoHosts`.

## 13. Where next

- The chapters of `docs/guide/`: [the app object](guide/app-object.md), [rexui](guide/rexui.md),
  [images](guide/images.md), [rich text](guide/richtext.md), [services](guide/services.md) and
  [packaging](guide/packaging.md). Their code is excerpted from programs that are tested.
- `cmd/gallery` shows every widget in every state, in light and dark: tables of a million
  rows, a splitter, tabs, selects, tooltips, context menus, and a custom titlebar. Its
  `gallery` package is the most complete consumer of the API.
- [ARCHITECTURE.md](../ARCHITECTURE.md) §8 describes the UI core: keys, storage, layout,
  input, scrolling and animation. §3 has the threading and frame loop.
- [DECISIONS.md](../DECISIONS.md) has the reasons: D10 (the UI core), D21 (startup), D22 (the
  `app` package), D28 (widgets and packages), D29 (menus), D32 (system appearance).
- [docs/PHASE5-PLAN.md](PHASE5-PLAN.md) has the contracts of the widget layer and what is still to come:
  more than one window, file dialogs, drag and drop, and background work.
