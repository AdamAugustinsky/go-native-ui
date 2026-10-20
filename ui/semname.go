package ui

// SemMark is a position in this frame's semantic records: the boxes built
// from now on have a later one. NameFirstUnnamed takes it.
func (c *Context) SemMark() int { return len(c.sems) }

// NameFirstUnnamed gives name (copied) to the first control built since mark
// (SemMark) that is interactive and has no name of its own, and reports
// whether there was one. Controls that have a name keep it, and so does every
// control after the first unnamed one. The interactive roles are the controls
// that read out by their name: a button, check box, switch, radio button,
// slider, text or search field and combo box. It allocates nothing.
func (c *Context) NameFirstUnnamed(mark int, name []byte) bool {
	if len(name) == 0 || mark < 0 {
		return false
	}
	for i := mark; i < len(c.sems); i++ {
		r := &c.sems[i]
		if r.name.Len != 0 {
			continue
		}
		switch r.role {
		case RoleButton, RoleCheckbox, RoleSwitch, RoleRadio, RoleSlider, RoleTextField, RoleSearchField, RoleComboBox:
			r.name = c.semA.AppendBytes(name)
			return true
		}
	}
	return false
}
