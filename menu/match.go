package menu

import "gonativeui/ui"

// Match gathers the commands of this frame: the ones the system delivered
// (Input.Commands: a native menu item), then the ones whose shortcut matches a
// key-down that nothing has claimed yet. Matching keys are claimed, so no
// other handler sees them; a disabled command does not match. Call it once at
// the top of Build, before the widgets that take keys, then ask with Fired or
// Next. A second call in the same frame adds nothing that was taken before.
//
// A text field that has the focus has already claimed its own chords (copy,
// paste, undo and so on), so the shortcut of an Edit command never fires it
// twice. A command with an edit role (RoleCopy, RolePaste...) that arrives
// when a text field has the focus does not fire; it becomes the matching
// ui edit action for that field (ui.Context.QueueEdit), which acts once, for a
// native Edit menu item as much as for a shortcut the field did not claim.
// When no text field has the focus it fires like any other command, so that
// a list can copy its selection.
//
// While a modal overlay is open (ui.Context.ModalOpen) the dialog owns the
// keys, and commands are held back with two exceptions: a command with an edit
// role still reaches a text field that has the focus inside the modal, as an
// edit action (the shortcut of an edit command that the field did not claim
// too); and a command marked InModal fires as usual. Every other command and
// shortcut is dropped (a system command) or left unmatched (a shortcut, whose
// key stays unclaimed for the dialog's own widgets).
//
// The commands of a frame wait in a queue of 16; more are dropped. They are
// kept until the next Match of another frame.
func (m *Model) Match(c *ui.Context) {
	if m.qc != c || m.qframe != c.Frame() {
		m.qc, m.qframe = c, c.Frame()
		m.qn, m.qh = 0, 0
	}
	modal := c.ModalOpen()
	for {
		id, ok := c.TakeCommand()
		if !ok {
			break
		}
		if i, known := m.index(ID(id)); known && (!modal || m.inModal(c, i)) {
			m.dispatch(c, i)
		}
	}
	if len(m.keys) == 0 {
		return
	}
	for k := 0; k < c.KeyCount(); k++ {
		e, claimed := c.KeyAt(k)
		if claimed || !e.Down {
			continue
		}
		for j := range m.keys {
			if m.keys[j].key == e.Code && m.keys[j].mods == e.Mods {
				if i := m.keys[j].idx; !hasBit(m.off, i) && (!modal || m.inModal(c, i)) {
					c.ClaimKey(k)
					m.dispatch(c, i)
				}
				break
			}
		}
	}
}

// inModal reports whether command i may act while a modal overlay is open: it
// is marked InModal, or it has an edit role and a text field has the focus.
func (m *Model) inModal(c *ui.Context, i int) bool {
	cmd := &m.cmds[i]
	return cmd.InModal || (cmd.Role.editAction() != 0 && c.TextFieldFocused())
}

// dispatch queues command i, or turns it into an edit action.
func (m *Model) dispatch(c *ui.Context, i int) {
	cmd := &m.cmds[i]
	if hasBit(m.off, i) {
		return
	}
	if a := cmd.Role.editAction(); a != 0 && c.TextFieldFocused() {
		c.QueueEdit(a)
		return
	}
	if m.qn < len(m.q) {
		m.q[m.qn] = cmd.ID
		m.qn++
	}
}

// Fired reports whether command id was delivered this frame and takes it: it
// reports true once for each time the command was delivered.
func (m *Model) Fired(id ID) bool {
	for i := m.qh; i < m.qn; i++ {
		if m.q[i] == id {
			m.q[i] = 0
			return true
		}
	}
	return false
}

// Next returns the next command of this frame that nothing has taken yet, in
// the order they were delivered, and false when there are no more.
func (m *Model) Next() (ID, bool) {
	for m.qh < m.qn {
		id := m.q[m.qh]
		m.qh++
		if id != 0 {
			return id, true
		}
	}
	return 0, false
}
