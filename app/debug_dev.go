//go:build !gnui_release

package app

import (
	"gonativeui/platform"
	"gonativeui/ui"
)

// devBuild is true in development builds, every build without the gnui_release
// tag (gnui package always sets it): the app has the Debug menu and the chords,
// the inspector panel, and the overlay records caller program counters. This
// file and debug_release.go are the only difference.
const devBuild = true

// The commands of the Debug menu, from debugCmdBase (debug.go) up.
const (
	debugCmdOverlay   = debugCmdBase
	debugCmdInspector = debugCmdBase + 1
	debugCmdAudit     = debugCmdBase + 2
	debugCmdEnd       = debugCmdBase + 3
)

// debugChordMods is what the chords hold: the primary modifier (Cmd on macOS,
// Ctrl elsewhere), Alt (Option) and Shift.
func debugChordMods() ui.Mods { return ui.PrimaryMod() | ui.ModAlt | ui.ModShift }

// chordCommand is the Debug command a key event is the chord of, by the
// character the key types on the user's layout (platform.Event.Char, through
// shortcutCode, as every shortcut is matched), so ⌘⌥⇧D is where a Dvorak or
// AZERTY user's D is.
func chordCommand(e *platform.Event) (cmd uint16, ok bool) {
	m := mods(e.Mods)
	if e.Kind != platform.EvKey || m != debugChordMods() {
		return 0, false
	}
	switch shortcutCode(keyCode(e.Key), e.Char, m) {
	case 'D':
		return debugCmdOverlay, true
	case 'I':
		return debugCmdInspector, true
	case 'A':
		return debugCmdAudit, true
	}
	return 0, false
}

// debugIngress gives the Debug commands their turn when an event arrives. A
// menu item (EvCommand with one of the ids) and, where no native menu bar is
// installed (Windows, Linux, a window without one), the chord's key-down, are
// handled here and never reach the app. With the bar installed AppKit has
// matched the chord already; the key is not seen again.
func (a *App) debugIngress(e *platform.Event) bool {
	switch e.Kind {
	case platform.EvCommand:
		if e.Flags&platform.FlagStandard == 0 && e.Tag >= uint64(debugCmdBase) && e.Tag < uint64(debugCmdEnd) {
			a.debugCommand(uint16(e.Tag))
			return true
		}
	case platform.EvKey:
		if a.barOn || e.Mods == 0 {
			return false
		}
		if cmd, ok := chordCommand(e); ok {
			if e.Flags&platform.FlagDown != 0 && e.Flags&platform.FlagRepeat == 0 {
				a.debugCommand(cmd)
			}
			return e.Flags&platform.FlagDown != 0
		}
	}
	return false
}

// debugCommand carries out a Debug command.
func (a *App) debugCommand(cmd uint16) {
	switch cmd {
	case debugCmdOverlay:
		a.EnableDebug(a.dbg == nil)
	case debugCmdInspector:
		if a.dbg != nil {
			a.SetInspectPanel(!a.dbg.panel)
		}
	case debugCmdAudit:
		if a.dbg == nil {
			return
		}
		if a.dbg.auditOn {
			a.dbg.auditOn = false
			a.debugRepaint()
		} else {
			a.DebugRunAudit()
		}
	}
}

// debugMenu adds the Debug menu to a native menu spec, before the Help menu:
// the model's own menus are untouched, so an app that sets its own model gets
// the menu appended.
func (a *App) debugMenu(s *platform.MenuSpec) {
	const mm = platform.ModSuper | platform.ModAlt | platform.ModShift
	s.Menus = append(s.Menus, platform.Menu{Title: "Debug", Items: []platform.MenuItem{
		{ID: debugCmdOverlay, Title: "Toggle Overlay", Key: 'd', Mods: mm},
		{ID: debugCmdInspector, Title: "Inspector", Key: 'i', Mods: mm},
		{ID: debugCmdAudit, Title: "Run Layout Audit", Key: 'a', Mods: mm},
	}})
	i := len(s.Menus) - 1
	at := len(s.Bar)
	for k, bi := range s.Bar {
		if s.Menus[bi].Role == platform.MenuRoleHelp {
			at = k
			break
		}
	}
	s.Bar = append(s.Bar[:at], append([]int{i}, s.Bar[at:]...)...)
}

// debugMenuState is the state of a Debug menu item; ok is false for any other id.
func (a *App) debugMenuState(id uint16) (enabled, checked, ok bool) {
	d := a.dbg
	switch id {
	case debugCmdOverlay:
		return true, d != nil, true
	case debugCmdInspector:
		return d != nil, d != nil && d.panel, true
	case debugCmdAudit:
		return d != nil, d != nil && d.auditOn, true
	}
	return false, false, false
}

// buildInspector writes the inspector panel's lines.
func buildInspector(s *InspectSnapshot, ls *dbgLines, sym *symCache) { inspectLines(s, ls, sym) }

// inspectLines is the inspector panel: the stack of boxes under the pointer,
// the recent frames with their causes, the duplicate keys and the findings.
func inspectLines(s *InspectSnapshot, ls *dbgLines, sym *symCache) {
	ls.next(colAccent).str("inspector")
	if s.Inside {
		ls.next(colDim).str("under the pointer, outer to inner:")
		for i := 0; i < s.NUnder; i++ {
			b := &s.Under[i]
			col := uint32(colText)
			if i == s.NUnder-1 {
				col = colInner
			}
			l := ls.next(col).str(" ").hex(uint64(b.Key), 8)
			if b.Role != ui.RoleNone {
				l.str(" ").str(ui.DebugRoleName(b.Role))
			}
			if b.NameLen > 0 {
				l.str(" \"").bytes(b.Name[:min(int(b.NameLen), 24)]).str("\"")
			}
		}
	} else {
		ls.next(colDim).str("the pointer is outside the window")
	}
	ls.next(colDim).str("frames (newest first):")
	hist := func(r *FrameRecord) {
		col := uint32(colText)
		if r.Unexplained() {
			col = colBad
		}
		l := ls.next(col).str(" #").unum(r.N).str(" ")
		appendCauses(l, r.Causes[:r.NCauses])
		l.str("  ").us(r.TotalCPUns()).str("us")
		if r.Allocs != 0 {
			l.str(" ").unum(r.Allocs).str(" allocs")
		}
		for i := 0; i < r.NCauses; i++ {
			c := &r.Causes[i]
			for j := 0; j < int(c.NCallers) && j < 1; j++ {
				ls.next(colDim).str("   at ").bytes(sym.symbol(c.Callers[j]))
			}
		}
	}
	this := FrameRecord{N: s.Frame, Causes: s.This, NCauses: s.NThis}
	hist(&this)
	if s.HasLast {
		hist(&s.Last)
	}
	for i := 0; i < s.NHistory; i++ {
		hist(&s.History[i])
	}
	if s.NDups > 0 {
		ls.next(colWarn).str("duplicate keys:")
		for i := 0; i < s.NDups; i++ {
			d := &s.Dups[i]
			l := ls.next(colWarn).str(" #").hex(uint64(d.Key), 16).str(" x").unum(uint64(d.Count))
			l.str(" (").unum(d.Total).str(" total)")
			if s.HasDupBox[i] {
				b := &s.DupBoxes[i]
				l = ls.next(colWarn).str("   ")
				if b.Role != ui.RoleNone {
					l.str(ui.DebugRoleName(b.Role)).str(" ")
				}
				if b.NameLen > 0 {
					l.str("\"").bytes(b.Name[:min(int(b.NameLen), 24)]).str("\" ")
				}
				l.str("at ").rect(b.Rect)
			}
		}
		if s.DupsMore > 0 {
			ls.next(colWarn).str(" and ").num(int64(s.DupsMore)).str(" more")
		}
	}
	if s.NFindings > 0 {
		ls.next(colBad).str("layout audit:")
		for i := 0; i < s.NFindings; i++ {
			ls.next(colBad).str(" ").str(s.Findings[i].Reason)
		}
	}
}
