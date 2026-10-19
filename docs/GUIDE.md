# Guide: build a notes app

This guide builds a small app, a list of notes, from an empty window to the
finished program in [`examples/notes`](../examples/notes). It is for a Go
developer who has not used the framework before.

The app has a window in the Rex theme that follows the system's light or dark
mode, a text field to add a note, a list with keyboard selection, Add and
Remove buttons with disabled states, a switch, a menu with shortcuts, and a
dialog that confirms removal. It allocates nothing while it runs.

**Every code block is an excerpt of the example.** The fence names the file, the
prose names the function, and a line `// …` marks lines left out. A test
(`TestGuideExcerptsAreTheExample`) fails if a block stops matching the source,
so the guide cannot drift from code that compiles and is tested. The API is
not stable yet and the project name is a placeholder.

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
func runWindow(exitFirst, verbose bool) error {
	n, err := notes.New()
	if err != nil {
		return err
	}
	res, err := app.RunWith(notes.Config(), n, app.RunOptions{ExitFirst: exitFirst, Verbose: verbose})
```

`notes.go`, function `Config`. The size is in points, the pixels per point come
from the display. `Titlebar` would give the window a custom macOS titlebar for
`Kit.Titlebar` to fill, as the gallery does; this app keeps the system's.

```go examples/notes/notes/notes.go
func Config() app.Config {
	return app.Config{Title: "Notes", Width: 420, Height: 560, MinWidth: 320, MinHeight: 360, Clear: [4]float32{0.92, 0.92, 0.92, 1}}
}
```

`Init` runs once, after the window, the renderer and the text system exist and
before the first frame. It makes the window's kit: the widget set bound to this
window's `ui.Context`, its text system and a theme. `rex.Theme()` is the
default look. `Reserve` and `ReserveRuns` size the caches up front, which is
what keeps the frames allocation-free (section 10).

```go examples/notes/notes/notes.go
func (n *Notes) Init(a *app.App) error {
	n.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	n.k.SetMenu(n.model)
	a.UI.Reserve(1024)
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
	k := n.k
	k.Begin(c)
	n.commands()
	// …
	k.End()
}
```

Between the two, the smallest app is `k.BeginWindow()`, widgets, `k.EndWindow()`:
the next section shows it in the example.

The first frame is built and drawn before the window is shown, so the window
appears with its content (D21).

## 3. Layout

`BeginWindow` draws the theme's window background and opens the content: a
column that covers the window, over the background, with the theme's padding
and gap (`NextPad` changes the padding, as here). Everything built until
`EndWindow` is in flow layout: `Column` stacks children top to bottom, `Row`
lays them left to right, and `Card` is a raised surface, a column with the
theme's padding. A `Row` centres its children across, so that a switch and a
button of different heights sit on one line (`NextCross` changes that).

```go examples/notes/notes/notes.go
	k.NextPad(10)
	k.BeginWindow() // the background, and the window's content in flow layout over it
	k.MenuBar()
	k.NextHeight(widget.Fill)
	k.BeginCard("card")
	k.LabelStyle("Notes", widget.TextSemi)
```

- **Begin and End pair up.** `BeginWindow` ends with `EndWindow`, `Column` with
  `EndColumn`, `Row` with `EndRow`, `BeginCard` with `EndCard`. The label
  identifies the container among its siblings.
- **`Next…` applies to the next widget only.** `NextSize`, `NextWidth`,
  `NextHeight`, `NextPad` and `NextGap` set one property of the next widget or
  container, and are then forgotten.
- **Sizes.** A child fits its content by default. `widget.Fill` takes the room
  its siblings leave, and `Kit.Fixed(pt)` is a fixed size.
- **Units.** The widget API takes points (`NextPad(10)`); boxes are in
  pixels, as everywhere in `ui`.
- **Absolute placement** stays available. The background and the window's
  box float, so that the content draws over them, and an app that places its
  panes itself builds `Background` and floating boxes of its own (`theme.At`
  places the next box, `theme.Floating` makes it float), as the gallery does.

## 4. State, a text field and a button

The state of the app is one struct. The notes live in an array of fixed-size,
pointer-free records, so adding a note never allocates; the limit is the
overflow policy, and the Add button is disabled when it is reached. The text
field's state is a `ui.TextBuffer` the app owns.

`notes.go`, type `Notes` and its limits.

```go examples/notes/notes/notes.go
const (
	maxNotes = 64
	maxLen   = 80 // bytes of a note
)

// note is one note: pointer-free, so the array of them is too.
type note struct {
	n uint8
	b [maxLen]byte
}
// …
	input   ui.TextBuffer // the text field
	field   ui.Key        // the text field's key, to focus it
	list    widget.ListState
	newest  bool // the Switch: show the newest note first
	confirm bool // the Dialog is open
}
```

`New` sets the buffer's `Max`, which cuts typed or pasted text at a character
boundary, and sizes the selection.

```go examples/notes/notes/notes.go
func New() (*Notes, error) {
	n := &Notes{}
	n.input.Max = maxLen
	n.list.Sel.Reserve(maxNotes)
```

`Build`, the row with the field and the button. `TextField` edits the buffer in
place (caret, selection, copy, cut, paste, undo and the input method are all
handled) and returns a `FieldSignal`, which carries the field's key (`f.Key`,
the same in every frame, unlike `f.ID`). `BeginDisabled(true)` draws what is
inside dimmed, takes it out of the Tab order and makes it deaf to the pointer,
the keyboard and any activation already queued. The pair can wrap a widget
conditionally. Enter in the field or a click on Add adds the note, and
`Kit.Focus` puts the keyboard back in the field.

```go examples/notes/notes/notes.go
	k.Row("add")
	f := k.TextField("New note", &n.input, "Add a note…")
	n.field = f.Key
	k.BeginDisabled(n.input.Len() == 0 || n.count == maxNotes)
	added := k.ButtonKind("Add", widget.ButtonPrimary).Clicked
	k.EndDisabled()
	k.EndRow()
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
	nt := &n.notes[n.count]
	nt.n = uint8(copy(nt.b[:], n.input.Bytes()))
	n.count++
	n.input.Reset()
	n.rebuild()
}
```

## 5. The list

A note's *id* is its index in the array, and the list works in ids. A
`widget.ListState` holds the selection (`Sel`, a `widget.Selection` indexed by
id), the display order (`Order`, which maps a display row to an id; `Item(r)` is
the id in row `r`, and `r` itself while `Order` is nil) and the scroll state. Because the selection is by id and not by row, reordering the list keeps
the selection on the same notes.

`rebuild` sets the order: the ids, or the ids reversed for the switch.

```go examples/notes/notes/notes.go
func (n *Notes) rebuild() {
	for r := 0; r < n.count; r++ {
		n.order[r] = uint32(r)
		if n.newest {
			n.order[r] = uint32(n.count - 1 - r)
		}
	}
	n.list.Order = n.order[:n.count]
}
```

`Build`, the list. `BeginList` is virtual: `First` and `Last` are the rows in view
plus two, so a frame costs what a screenful costs, whether there are 3 rows or a
million. `RowNamed(r, id, name)` starts a row whose boxes are keyed by the id, so
their state follows the note when the order changes, and whose accessible name is
the note's text: what assistive technology reads for the row, and what a test
finds it by (`Row` without a name is for rows that have none; in a `Table` the
first cell's text names the row). The row's children flow left to right. The
loop is an ordinary `for`; there is no closure or interface call per row.

```go examples/notes/notes/notes.go
	k.NextHeight(widget.Fill)
	l := k.BeginList("notes", &n.list, n.count)
	for r := l.First; r < l.Last; r++ {
		id := n.list.Item(r) // the stable id of the note in row r
		l.RowNamed(r, id, n.Text(int(id)))
		k.NextWidth(widget.Fill)
		k.LabelBytes(n.Text(int(id)))
	}
	k.EndList(&l)
```

A click selects a row (Shift extends a range from the anchor, the primary modifier
toggles one). When the list has the focus, Up, Down, Page Up, Page Down, Home and
End move the cursor and select, Shift extends the range, and ⌘A or Ctrl+A selects
all. Enter or a double-click reports `l.Activated`, which this app does not use. The
list also records the semantics that the accessibility bridges will read.

Removing notes moves the later ones up, so their ids change. The selection held
exactly the removed notes, so it is reset. An app that wants the selection to
survive a change of the listing calls `Selection.Remap` with the new id of each old
one instead.

```go examples/notes/notes/notes.go
func (n *Notes) removeSelected() {
	j := 0
	for i := 0; i < n.count; i++ {
		if !n.list.Sel.Has(uint32(i)) {
			n.notes[j] = n.notes[i]
			j++
		}
	}
	n.count = j
	n.list.Sel.Reset()
	n.rebuild()
}
```

## 6. A switch, a button and a status line

`Switch` edits a `bool` of the app's and reports `Changed`; the order is rebuilt
when it does. `Spacer` pushes the Remove button to the right, and the row centres
the switch and the button across, whatever their heights. Remove is disabled
while nothing is selected. The status line is dynamic text: `c.Fmt()` builds it
in the frame's arena and `LabelBytesStyle` copies the bytes (section 10).

```go examples/notes/notes/notes.go
	k.Row("footer")
	if k.Switch("Newest first", &n.newest).Changed {
		n.rebuild()
	}
	k.Spacer()
	k.BeginDisabled(n.list.Sel.Count() == 0)
	if k.Button("Remove…").Clicked {
		n.confirm = true
	}
	k.EndDisabled()
	k.EndRow()
	k.LabelBytesStyle(c.Fmt().Int(n.count).Str(" notes, ").Int(n.list.Sel.Count()).Str(" selected").Bytes(), widget.TextMuted)
	k.EndCard()
	k.EndWindow()
```

## 7. A confirmation dialog

A `Dialog` is an in-window modal, open while the app's `bool` is true. Build its
content only when `Visible`. Everything under it is inert: no pointer, scrolling,
text input or shortcut reaches the window, and Tab stays inside the dialog. An
`Action` closes the dialog and returns true once. Escape cancels, also from a text
field in the dialog; with dialogs stacked only the topmost takes Enter and Escape.
The first `ButtonPrimary` is the default action: it has the focus when the dialog opens, and
Enter chooses it. Build Cancel first, so that Enter on a focused Cancel is
Cancel's.

```go examples/notes/notes/notes.go
	d := k.BeginDialog("remove", "Remove notes?", &n.confirm)
	if d.Visible {
		k.Paragraph(c.Fmt().Str("Notes selected: ").Int(n.list.Sel.Count()).Str(". Removing them cannot be undone.").String())
		d.BeginActions()
		d.Action("Cancel", widget.ButtonDefault)
		if d.Action("Remove", widget.ButtonPrimary) {
			n.removeSelected()
		}
		d.EndActions()
	}
	k.EndDialog(&d)
	k.End()
```

The button that opens it is called "Remove…" and the dialog's action "Remove". The
labels differ so that a test, or an assistive technology, finds each by name.

## 8. Commands, menus and shortcuts

A command is an id, a title and a shortcut. `menu.NewModel` takes the commands,
the menus that show them and which menus are in the bar, and checks them. `Primary`
means ⌘ on macOS and Ctrl elsewhere.

```go examples/notes/notes/notes.go
func newModel() (*menu.Model, error) {
	primary := func(k ui.KeyCode, m ui.Mods) menu.Shortcut { return menu.Shortcut{Key: k, Mods: m, Primary: true} }
	cmds := []menu.Command{
		{ID: cmdNew, Title: "New Note", Shortcut: primary('N', 0)},
		{ID: cmdRemove, Title: "Remove…", Shortcut: primary(ui.KeyBackspace, 0)},
		{ID: cmdNewest, Title: "Newest First", Shortcut: primary('N', ui.ModShift)},
	}
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	menus := []menu.Menu{
		{Title: "Note", Items: []menu.Item{item(cmdNew), item(cmdRemove)}},
		{Title: "View", Items: []menu.Item{item(cmdNewest)}},
	}
	return menu.NewModel(cmds, menus, []int{0, 1})
}
```

`Init` gave the model to the kit (`SetMenu`). `Kit.Begin` then matches the
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
	if k.Command(cmdNewest) {
		n.newest = !n.newest
		n.rebuild()
	}
	if k.Command(cmdRemove) {
		n.confirm = true
	}
	n.model.SetEnabled(cmdRemove, n.list.Sel.Count() > 0)
	n.model.SetChecked(cmdNewest, n.newest)
}
```

**One key, one handler.** A key event is offered in a fixed order and handled at
most once: the input method, then the focused text field, then the commands, then
the widgets' own keys. While a field has the focus it takes every key but Tab
unless the primary modifier is down, and with it A, C, X, V, Z, the arrows,
Home, End, Backspace and Delete. So ⌘N works while you type, but ⌘Backspace in
the field means "delete to the start of the line", and it removes notes only when
the focus is elsewhere. A modal dialog holds back every command and shortcut
except the edit commands for a text field in it and the commands marked
`menu.Command.InModal`; its own keys are taken by its widgets.

**Where the menu shows.** `Kit.MenuBar` draws the bar inside the window on every
OS today. D29 plans a native menu bar on macOS built from the same model, with
the Edit menu acting on the focused field; the model above does not change for
it. Commands with edit roles (`menu.RoleCopy` and the like) belong in that Edit
menu and are not needed here, because the field handles its own chords.

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
   straight to `LabelBytes`, which copies it. Never build text with `fmt.Sprintf`,
   `+` or `string(b)` in `Build`.
3. **`Reserve` every growable store, and say what happens when it is full.**
   `Context.Reserve(n)` sizes the box cache for n live boxes. `Text.ReserveRuns` sizes
   the shaped-run cache so that new strings never grow it. `Selection.Reserve(items)`
   sizes the selection. A `TextBuffer` grows only past its high-water mark, and
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

`notes_test.go`. The interaction test types, adds, selects with the keyboard, removes
and confirms:

```go examples/notes/notes/notes_test.go
	r.play(click(ui.RoleTextField, "New note"), typeText("Buy bread"), key(ui.KeyEnter, 0))
	if got := r.texts(); len(got) != 4 || got[3] != "Buy bread" || d.input.Len() != 0 {
		t.Fatalf("after Enter the notes are %q, the field holds %q", got, d.input.String())
	}
	// …
	r.play(click(ui.RoleListItem, "").Nth(3), key(ui.KeyDown, ui.ModShift))
	// …
	r.play(click(ui.RoleButton, "Remove…"), waitFor(ui.RoleDialog, ""))
	// …
	r.play(click(ui.RoleButton, "Remove"), waitGone(ui.RoleDialog, ""))
```

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

## 12. Building, shipping and platforms

| OS | Minimum | Graphics | Needs at run time |
|---|---|---|---|
| macOS | 14 | Metal | nothing beyond the system frameworks |
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
  six targets and fails if any binary is over 10 MB.
- **Linux backend:** Wayland when `WAYLAND_DISPLAY` is set and the connection works, X11
  otherwise; `GNUI_BACKEND=wayland` or `x11` forces one.
- **Startup:** the budget is 150 ms from process start to the first complete frame on the
  display (D21). On macOS the first launch of a new binary also pays Gatekeeper's scan,
  so measure from the second.
- **macOS materials and native views.** With `app.Config.Materials`, a box marked with
  `c.Material` (sidebar, inspector, toolbar capsule, ...) shows the system's live glass or
  material where the app paints nothing; under Reduce Transparency it is an opaque fill
  of the colour you give. `c.Host` places a native view the app made with
  `App.Win.NewHost`. Native views lie above everything the app draws, so a host is hidden
  while a drawn popup or tooltip overlaps it and while any modal is open: place popups
  that belong to a host beside it, not over it. Materials lie below the app's pixels and
  cannot blur them. Offscreen renders show hosts as grey placeholders. Elsewhere these
  calls do nothing and `NewHost` returns `platform.ErrUnsupported`.

## 13. Where next

- `cmd/gallery` shows every widget in every state, in light and dark: tables of a million
  rows, a splitter, tabs, selects, tooltips, context menus, and a custom titlebar. Its
  `gallery` package is the most complete consumer of the API.
- [ARCHITECTURE.md](../ARCHITECTURE.md) §8 describes the UI core: keys, storage, layout,
  input, scrolling and animation. §3 has the threading and frame loop.
- [DECISIONS.md](../DECISIONS.md) has the reasons: D10 (the UI core), D21 (startup), D22 (the
  `app` package), D28 (widgets and packages), D29 (menus), D32 (system appearance).
- [docs/PHASE5-PLAN.md](PHASE5-PLAN.md) has the contracts of the widget layer and what is still to come:
  more than one window, native menus on macOS, file dialogs, drag and drop, and background work.
