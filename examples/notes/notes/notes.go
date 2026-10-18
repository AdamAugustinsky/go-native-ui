// Package notes is the example application of the guide (docs/GUIDE.md): a
// small list of notes with a text field to add one, a list with keyboard
// selection, Add and Remove buttons, a switch, a menu with shortcuts and a
// confirmation dialog.
//
// It is the Builder the notes command runs in a window and in the benchmark,
// and that the tests drive offscreen. Everything the window remembers lives in
// the Notes value; a frame allocates nothing once it is warm.
package notes

import (
	"gonativeui/app"
	"gonativeui/menu"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

// Limits. The notes live in an array, so adding one never allocates; when it
// is full, Add is disabled.
const (
	maxNotes = 64
	maxLen   = 80 // bytes of a note
)

// note is one note: pointer-free, so the array of them is too.
type note struct {
	n uint8
	b [maxLen]byte
}

// The commands of the menus.
const (
	cmdNew menu.ID = iota + 1
	cmdRemove
	cmdNewest
)

// Notes is the application: its notes, the state of its widgets and the kit
// that builds them. It implements app.Builder and app.Initer.
type Notes struct {
	c     *ui.Context
	k     *widget.Kit
	model *menu.Model

	notes [maxNotes]note
	count int
	order [maxNotes]uint32 // display row → note, rebuilt when the notes or the order change

	input   ui.TextBuffer // the text field
	field   ui.Key        // the text field's key, to focus it
	list    widget.ListState
	newest  bool // the Switch: show the newest note first
	confirm bool // the Dialog is open
}

var (
	_ app.Builder = (*Notes)(nil)
	_ app.Initer  = (*Notes)(nil)
)

// Config is the window: 420×560 points, no custom titlebar.
func Config() app.Config {
	return app.Config{Title: "Notes", Width: 420, Height: 560, MinWidth: 320, MinHeight: 360, Clear: [4]float32{0.92, 0.92, 0.92, 1}}
}

// New returns the application with a few notes in it.
func New() (*Notes, error) {
	n := &Notes{}
	n.input.Max = maxLen
	n.list.Sel.Reserve(maxNotes)
	for _, s := range [...]string{"Buy milk", "Call Ana", "Read the guide"} {
		n.input.SetString(s)
		n.add()
	}
	var err error
	if n.model, err = newModel(); err != nil {
		return nil, err
	}
	return n, nil
}

// newModel builds the commands and menus: a Note menu and a View menu in the bar.
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

// Init implements app.Initer: it makes the kit and sizes the caches.
func (n *Notes) Init(a *app.App) error {
	n.c = a.UI
	n.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	n.k.SetMenu(n.model)
	a.UI.Reserve(1024)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	return nil
}

// Count is the number of notes.
func (n *Notes) Count() int { return n.count }

// Selected is the number of selected notes.
func (n *Notes) Selected() int { return n.list.Sel.Count() }

// Text is the text of note i, which is valid until the notes change.
func (n *Notes) Text(i int) []byte { return n.notes[i].b[:n.notes[i].n] }

// add appends the text of the field as a note, if there is room.
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

// removeSelected removes the selected notes. The notes after them move up, so
// their numbers change, and the selection, which held only the removed ones,
// is reset.
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

// rebuild sets the display order of the list: the notes' numbers, or their reverse.
func (n *Notes) rebuild() {
	for r := 0; r < n.count; r++ {
		n.order[r] = uint32(r)
		if n.newest {
			n.order[r] = uint32(n.count - 1 - r)
		}
	}
	n.list.Order = n.order[:n.count]
}

// Build builds the frame. It implements app.Builder.
func (n *Notes) Build(c *ui.Context) {
	k := n.k
	k.Begin(c)
	n.commands()
	k.Background() // its boxes float; so does the window's content, built after them

	w, h := c.Size()
	theme.At(c, 0, 0, float32(w), float32(h))
	c.Push(c.Box(theme.Floating, "window"))
	k.NextSize(widget.Fill, widget.Fill)
	k.NextPad(10)
	k.Column("content")
	k.MenuBar()
	k.NextHeight(widget.Fill)
	k.BeginCard("card")
	k.LabelStyle("Notes", widget.TextSemi)

	k.Row("add")
	f := k.TextField("New note", &n.input, "Add a note…")
	n.field = c.KeyOf(f.ID)
	k.BeginDisabled(n.input.Len() == 0 || n.count == maxNotes)
	added := k.ButtonKind("Add", widget.ButtonPrimary).Clicked
	k.EndDisabled()
	k.EndRow()
	if added || f.Submitted {
		n.add()
		c.SetFocus(n.field)
	}

	k.NextHeight(widget.Fill)
	l := k.BeginList("notes", &n.list, n.count)
	for r := l.First; r < l.Last; r++ {
		id := n.list.Order[r] // the stable id of the note in row r
		l.Row(r, id)
		k.NextWidth(widget.Fill)
		k.LabelBytes(n.Text(int(id)))
	}
	k.EndList(&l)

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
	k.EndColumn()
	c.Pop()

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
}

// commands applies the menu commands of the frame and sets their state.
func (n *Notes) commands() {
	k := n.k
	if k.Command(cmdNew) {
		n.c.SetFocus(n.field)
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
