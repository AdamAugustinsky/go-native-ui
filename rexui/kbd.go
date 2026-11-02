package rexui

import (
	"strings"
	"sync"

	"gonativeui/menu"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Key hints: a shortcut written once, as macOS writes it ("⌘⇧F", "⌃`",
// "⌥⌘↑"), shown as the platform writes it (KeyText: "Ctrl+Shift+F" off
// macOS), in a chip (Kbd) or in the shortcuts sheet (ShortcutsSheet). A3 and
// Zap each wrote their own replacer, and both missed ⌥ (D95).

// keyTexts caches KeyText's results: each shortcut is rewritten once, so a
// frame that shows it allocates nothing.
var keyTexts sync.Map

// KeyText is keys, a shortcut written as macOS writes it, as this platform
// writes it: itself on macOS; elsewhere each run of modifier symbols becomes
// the modifiers' names in the platform's order (Ctrl+, Alt+, Shift+; ⌘ is
// Ctrl), and the keys macOS draws as symbols take their names (↩ Enter, ⎋
// Esc, ⌫ Backspace, ⌦ Del, ⇥ Tab), as menu.Shortcut.AppendLabel writes them.
// Text that is not a modifier or one of those keys is kept ("⌘1–9", "J / K").
//
// Text already written for the platform comes back as it was, so a label
// may pass through it twice.
func KeyText(keys string) string { return KeyTextFor(keys, ui.PrimaryMod() == ui.ModSuper) }

// KeyTextFor is KeyText for macOS (mac) or for the other platforms, whatever
// this one is: for an app that builds another platform's screens (its
// Windows look, tested on a Mac).
func KeyTextFor(keys string, mac bool) string {
	if mac {
		return keys
	}
	if t, ok := keyTexts.Load(keys); ok {
		return t.(string)
	}
	t := keyTextFor(keys, false)
	keyTexts.Store(keys, t)
	return t
}

// The modifier symbols, in the order Windows writes them.
var modSyms = [...]struct{ sym, name string }{{"⌃", "Ctrl+"}, {"⌘", "Ctrl+"}, {"⌥", "Alt+"}, {"⇧", "Shift+"}}

// keySyms are the keys macOS draws as symbols and their names elsewhere.
var keySyms = strings.NewReplacer("↩", "Enter", "⎋", "Esc", "⌫", "Backspace", "⌦", "Del", "⇥", "Tab", "⇞", "PgUp", "⇟", "PgDn")

// keyTextFor is KeyText on macOS (mac) or elsewhere.
func keyTextFor(keys string, mac bool) string {
	if mac {
		return keys
	}
	var b strings.Builder
	for i := 0; i < len(keys); {
		var ctrl, alt, shift, any bool
		j := i
	run:
		for j < len(keys) {
			for _, m := range modSyms {
				if strings.HasPrefix(keys[j:], m.sym) {
					switch m.sym {
					case "⌃", "⌘":
						ctrl = true
					case "⌥":
						alt = true
					case "⇧":
						shift = true
					}
					any = true
					j += len(m.sym)
					continue run
				}
			}
			break
		}
		if any {
			if ctrl {
				b.WriteString("Ctrl+")
			}
			if alt {
				b.WriteString("Alt+")
			}
			if shift {
				b.WriteString("Shift+")
			}
			i = j
			continue
		}
		// up to the next modifier symbol
		next := len(keys)
		for _, m := range modSyms {
			if k := strings.Index(keys[i:], m.sym); k >= 0 && i+k < next {
				next = i + k
			}
		}
		b.WriteString(keySyms.Replace(keys[i:next]))
		i = next
	}
	return b.String()
}

// ShortcutText is s as this platform writes it (menu.Shortcut.AppendLabel).
func ShortcutText(s menu.Shortcut) string { return string(s.AppendLabel(nil)) }

// tips caches WithShortcut's results.
var tips sync.Map

// WithShortcut is a tooltip's text: what a control does and, when keys is
// not empty, its shortcut as this platform writes it ("Open the terminal
// (⌃`)", "… (Ctrl+`)"). Each is made once.
func WithShortcut(what, keys string) string {
	if keys == "" {
		return what
	}
	k := [2]string{what, keys}
	if t, ok := tips.Load(k); ok {
		return t.(string)
	}
	t := what + " (" + KeyText(keys) + ")"
	tips.Store(k, t)
	return t
}

// KbdRecipe is a key chip's look (T3 Code's Kbd), in points.
type KbdRecipe struct {
	// Height is the chip's height and least width; PadX the space at its
	// sides; Radius its corners.
	Height, PadX, Radius float32
	// Fill is the label colour's opacity under the keys (light, dark).
	Fill [2]float32
	// Style and Color are the keys' text.
	Style TextStyle
	Color Tint
}

// KbdLook is the key chips' recipe: 20 pt high, 12 pt medium keys in the
// secondary label's colour on 7 % of the label's.
var KbdLook = KbdRecipe{Height: 20, PadX: theme.Space1, Radius: theme.Space1, Fill: [2]float32{0.07, 0.07},
	Style: TextFootnote, Color: Of(theme.Label2)}

// Kbd is a key chip: keys, written as macOS writes them, as this platform
// writes them (KeyText), in a quiet rounded chip (a menu row's or a picker
// row's shortcut).
func Kbd(k *base.Kit, keys string) {
	c, r := k.Context(), k.Theme()
	rc := &KbdLook
	h := theme.Snap(k.Pt(rc.Height))
	c.NextSize(ui.Children(1), ui.Px(h))
	c.NextMinSize(h, 0)
	c.NextAxis(ui.AxisX)
	c.NextJustify(ui.JustifyCenter)
	c.NextCross(ui.CrossCenter)
	c.NextPadding(k.Pt(rc.PadX), 0, k.Pt(rc.PadX), 0)
	c.NextRadius(k.Pt(rc.Radius))
	c.NextBG(theme.WithAlpha(r.Colors[theme.Label], theme.Lerp(rc.Fill[0], rc.Fill[1], r.T)))
	c.Push(c.Box(ui.DrawBackground, "kbd"))
	Label(k, c.Fmt().Str(KeyText(keys)).Str("###key").String(), LabelOpts{Style: rc.Style, Color: rc.Color})
	c.Pop()
}

// ShortcutRow is one line of the shortcuts sheet: Keys, written as macOS
// writes them (KeyText), or Shortcut when Keys is empty; and What it does.
type ShortcutRow struct {
	// Keys are the keys as macOS writes them ("⌘⇧F", "J / K").
	Keys string
	// Shortcut is the chord, when Keys is empty.
	Shortcut menu.Shortcut
	// What is what the keys do.
	What string
}

// Text is the row's keys as this platform writes them.
func (s ShortcutRow) Text() string {
	if s.Keys != "" {
		return KeyText(s.Keys)
	}
	return ShortcutText(s.Shortcut)
}

// ShortcutRowsOf appends to dst a row for each command of m with a
// shortcut and a title, in the menus' order (the bar's menus, then any
// other): the shortcuts sheet of an app's menu model, which the app extends
// with the keys no menu shows (J/K in a list, a right click).
func ShortcutRowsOf(dst []ShortcutRow, m *menu.Model) []ShortcutRow {
	seen := map[menu.ID]bool{}
	var walk func(i int)
	walk = func(i int) {
		ms := m.Menus()
		if i < 0 || i >= len(ms) {
			return
		}
		for _, it := range ms[i].Items {
			switch it.Kind {
			case menu.ItemCommand:
				cmd, ok := m.Command(it.Command)
				if !ok || seen[cmd.ID] || cmd.Shortcut.IsZero() {
					continue
				}
				seen[cmd.ID] = true
				title := cmd.Title
				if title == "" && cmd.Role != 0 {
					title = menu.RoleTitle(cmd.Role)
				}
				if title != "" {
					dst = append(dst, ShortcutRow{Shortcut: cmd.Shortcut, What: title})
				}
			case menu.ItemSubmenu:
				walk(it.Submenu)
			}
		}
	}
	for _, i := range m.Bar() {
		walk(i)
	}
	for i := range m.Menus() {
		walk(i)
	}
	return dst
}

// ShortcutsOpts are the shortcuts sheet's options.
type ShortcutsOpts struct {
	// Title is the sheet's title; Width its width (0: 380 pt); KeysW the keys'
	// column (0: 70 pt).
	Title        string
	Width, KeysW float32
	// Close is the button that closes it (""; Strings.Close).
	Close string
}

// ShortcutsSheet is a dialog listing rows, the keys in a column of their
// own in the metadata's face and what they do beside them, while *open is
// true; its button closes it.
func ShortcutsSheet(k *base.Kit, label string, open *bool, rows []ShortcutRow, o ShortcutsOpts) {
	w, kw := o.Width, o.KeysW
	if w <= 0 {
		w = 380
	}
	if kw <= 0 {
		kw = 70
	}
	d := BeginDialog(k, label, open, DialogOpts{Title: o.Title, Width: w})
	if d.Visible {
		c := k.Context()
		for i, row := range rows {
			k.NextWidth(ui.Pct(1))
			k.NextGap(theme.Space3)
			k.Row(c.Fmt().Str("k").Int(i).String())
			k.NextWidth(ui.Px(k.Pt(kw)))
			Label(k, c.Fmt().Str(row.Text()).Str("###key").String(), LabelOpts{MetaPt: theme.MetaPt + 1, Color: Of(theme.Label2)})
			k.NextWidth(ui.Grow(1))
			Label(k, c.Fmt().Str(row.What).Str("###what").String(), LabelOpts{})
			k.EndRow()
		}
		d.BeginActions()
		cl := o.Close
		if cl == "" {
			cl = str.Close
		}
		if d.Action(cl, ButtonPrimary) {
			*open = false
		}
		d.EndActions()
	}
	EndDialog(k, &d)
}
