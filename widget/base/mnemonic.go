package base

import (
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
)

// Mnemonics (access keys) of the drawn menus, as in Windows: each title of a
// menu bar and each item of a drawn menu has a letter or digit that chooses it
// from the keyboard. Alt and the letter opens a menu of the bar, and the letter
// (with or without Alt) chooses an item of the open menu; it is underlined
// while the keyboard drives the menus (Windows and Linux; macOS, whose menus
// have no access keys, matches the letters without showing them).
//
// A title's letter is the one the app asked for (menu.Command.Mnemonic,
// menu.Menu.Mnemonic, a ui.MenuItem's Key without modifiers) when it is free,
// else the first free letter that starts a word of the title, else its first
// free letter or digit; a title with none free has none. Only ASCII letters
// and digits are mnemonics, matched without case by the character the key
// types on the user's layout (ui.KeyEvent.Char).

// MaxMnemonics is how many titles of one menu get a mnemonic; the ones after
// have none.
const MaxMnemonics = 128

// Mnemonics assigns the mnemonics of the titles of one menu: Add each title in
// order, then Resolve. It is a value on the stack: it allocates nothing.
type Mnemonics struct {
	n      int
	titles [MaxMnemonics]string
	want   [MaxMnemonics]byte  // the asked-for letter, upper case, 0 for none
	off    [MaxMnemonics]int16 // the letter's byte offset in the title, -1 for none
	letter [MaxMnemonics]byte  // the mnemonic, upper case, 0 for none
}

// Add adds the next title, with the letter the app asked for (0: none). A
// disabled item still takes its letter, so the others do not move when it is
// enabled.
func (m *Mnemonics) Add(title string, want rune) {
	if m.n >= MaxMnemonics {
		return
	}
	m.titles[m.n], m.want[m.n] = title, mnemonicByte(want)
	m.n++
}

// Len is how many titles were added.
func (m *Mnemonics) Len() int { return m.n }

// Resolve assigns the letters: the asked-for ones first, in order, then the
// others.
func (m *Mnemonics) Resolve() {
	var used [36]bool
	for i := 0; i < m.n; i++ {
		m.off[i], m.letter[i] = -1, 0
		if w := m.want[i]; w != 0 && !used[slot(w)] {
			used[slot(w)] = true
			m.letter[i] = w
			m.off[i] = int16(indexFold(m.titles[i], w))
		}
	}
	for i := 0; i < m.n; i++ {
		if m.letter[i] != 0 {
			continue
		}
		t := m.titles[i]
		pick := -1
		for pass := 0; pass < 2 && pick < 0; pass++ {
			for j := 0; j < len(t) && j < 1<<15; j++ {
				b := mnemonicByte(rune(t[j]))
				if b == 0 || used[slot(b)] {
					continue
				}
				if pass == 0 && j > 0 && mnemonicByte(rune(t[j-1])) != 0 {
					continue // the first pass takes the starts of words
				}
				if pass == 0 && j > 0 && t[j-1] >= 0x80 {
					continue // after a letter of another script
				}
				pick = j
				break
			}
		}
		if pick >= 0 {
			b := mnemonicByte(rune(t[pick]))
			used[slot(b)] = true
			m.letter[i], m.off[i] = b, int16(pick)
		}
	}
}

// At is title i's mnemonic (upper case, 0 for none) and its byte offset in
// the title (-1 when the asked-for letter is not in the title, or none).
func (m *Mnemonics) At(i int) (letter byte, off int) {
	if i < 0 || i >= m.n {
		return 0, -1
	}
	return m.letter[i], int(m.off[i])
}

// Find returns the title whose mnemonic is letter, the first one after from
// (cycling; from -1 starts at the first), and how many have it; -1 when none.
func (m *Mnemonics) Find(letter byte, from int) (i, count int) {
	i = -1
	for k := 1; k <= m.n; k++ {
		j := (from + k + m.n) % m.n
		if from < 0 {
			j = k - 1
		}
		if m.letter[j] == letter && letter != 0 {
			if i < 0 {
				i = j
			}
			count++
		}
	}
	return i, count
}

// TypedMnemonic finds this frame's first unclaimed key press that can choose
// a mnemonic: a letter or digit with exactly the modifiers mods (0 for the
// letter alone, ui.ModAlt for Alt and the letter). It returns the letter
// (upper case) and the key event's index for ui.Context.ClaimKey; 0 and -1
// when there is none.
func TypedMnemonic(c *ui.Context, mods ui.Mods) (letter byte, at int) {
	for i := 0; i < c.KeyCount(); i++ {
		e, claimed := c.KeyAt(i)
		if claimed || !e.Down || e.Mods != mods {
			continue
		}
		if b := KeyMnemonic(e); b != 0 {
			return b, i
		}
	}
	return 0, -1
}

// ClaimMnemonic claims key event at (from TypedMnemonic) for a drawn menu
// that acts on it; a letter typed without Alt also types a character, which
// is kept out of the text fields (ui.Context.DropTyped).
func ClaimMnemonic(c *ui.Context, at int) {
	e, _ := c.KeyAt(at)
	c.ClaimKey(at)
	if e.Mods&ui.ModAlt == 0 {
		r := e.Char
		if r == 0 {
			r = rune(KeyMnemonic(e))
		}
		c.DropTyped(r)
	}
}

// KeyMnemonic is the mnemonic letter key event e types (upper case), 0 for
// none: the character on the user's layout when the platform tells it, else
// the key.
func KeyMnemonic(e ui.KeyEvent) byte {
	if e.Char != 0 {
		return mnemonicByte(e.Char)
	}
	if e.Code < 0x80 {
		return mnemonicByte(rune(e.Code))
	}
	return 0
}

// mnemonicByte is r as a mnemonic: an ASCII letter in upper case or a digit,
// 0 for anything else.
func mnemonicByte(r rune) byte {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(r - 'a' + 'A')
	case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return byte(r)
	}
	return 0
}

// slot is a mnemonic's index in a set of the 36 letters and digits.
func slot(b byte) int {
	if b >= 'A' {
		return int(b - 'A')
	}
	return 26 + int(b-'0')
}

// indexFold is the byte offset of the first b (an upper-case mnemonic) in s,
// without case; -1 when s has none.
func indexFold(s string, b byte) int {
	for j := 0; j < len(s) && j < 1<<15; j++ {
		if mnemonicByte(rune(s[j])) == b {
			return j
		}
	}
	return -1
}

// ShowMnemonics reports whether the drawn menus underline their mnemonics
// now: off macOS, while the keyboard drives them (the focus ring shows) or
// Alt is held, as Windows shows its access keys.
func (k *Kit) ShowMnemonics() bool {
	c := k.c
	if ui.PrimaryMod() == ui.ModSuper {
		return false
	}
	return c.FocusVisible() || c.Input().Mods == ui.ModAlt
}

// MnemonicUnderline draws the underline of the mnemonic at byte off of title
// as a floating child of the open box: the title is drawn in font f, from x
// (pixels from the box's left), centred vertically in a line of height h
// whose top is y, as a text box draws it. off < 0 draws nothing.
func (k *Kit) MnemonicUnderline(key ui.Key, f text.FontKey, title string, off int, x, y, h float32, color uint32) {
	if off < 0 || off >= len(title) {
		return
	}
	c := k.c
	m := k.ts.Metrics(f)
	x0 := x + k.TextWidth(f, title[:off])
	w := k.TextWidth(f, title[off:off+1])
	th := theme.Snap(m.UnderlineThickness)
	if th < 1 {
		th = max(theme.Snap(k.scale), 1)
	}
	pos := m.UnderlinePos
	if pos == 0 {
		pos = max(m.Descent*0.35, 1)
	}
	base := y + (h-(m.Ascent+m.Descent))/2 + m.Ascent
	c.NextPos(theme.Snap(x0), theme.Snap(base+pos-th/2))
	c.NextSize(ui.Px(max(w, 1)), ui.Px(th))
	c.NextBG(color)
	c.BoxKeyed(ui.DrawBackground|Floating, key, nil)
}
