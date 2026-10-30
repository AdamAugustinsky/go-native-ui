package app

import (
	"fmt"
	"math/rand/v2"
	"time"

	"gonativeui/platform"
)

// SimUser is a seeded stream of native-shaped input for a simulated window
// (D90): pointer moves, clicks, double clicks and drags, wheel
// and trackpad scrolls with phases and momentum, keys with modifiers, typed
// text in several scripts, input-method compositions that commit, cancel or
// lose the focus, press-and-hold replacements, pastes, window focus changes,
// resizes and scale changes. Each Step sends one action and advances the
// virtual clock by at least one frame, so the same seed sends the same events
// at the same instants: with the simulator's own determinism, a seed replays
// a run exactly. Install SimInvariants (NewSim does by default) to check the
// frames it causes.
//
// It never sends EvClose, and no shortcut that quits or closes (⌘Q, ⌘W).
type SimUser struct {
	rng       *rand.Rand
	seed      uint64
	x, y      float32 // the pointer, physical pixels
	inside    bool
	composing bool
	unfocused bool
	// Steps counts the actions sent; Events the events.
	Steps, Events int
}

// NewSimUser returns the stream for seed.
func NewSimUser(seed uint64) *SimUser {
	return &SimUser{rng: rand.New(rand.NewPCG(seed, seed^0x6a09e667f3bcc909)), seed: seed}
}

// simTexts are what the stream types and composes: scripts, combining marks,
// emoji sequences, line breaks and tabs, controls and invalid UTF-8 (which a
// field drops), and long runs.
var simTexts = []string{
	"a", "Z", "7", " ", "hello", "café", "é", "über", "日本語", "かな", "한국어", "עברית", "مرحبا",
	"👩‍💻", "🇧🇷", "👍🏽", "a‍b", "\r\n", "\t", "x\ny", "\x00\x1b", "\xff\xfe", "\xe2\x82", "--", "ÿ",
	"The quick brown fox jumps over the lazy dog ", "🙂🙂🙂🙂🙂🙂🙂🙂", "ffi", "ạ̈́",
}

// simKeys are the keys a step presses, with the letters and digits below.
var simKeys = []platform.Key{
	platform.KeyEscape, platform.KeyEnter, platform.KeyTab, platform.KeyBackspace, platform.KeyDelete,
	platform.KeySpace, platform.KeyLeft, platform.KeyRight, platform.KeyUp, platform.KeyDown,
	platform.KeyHome, platform.KeyEnd, platform.KeyPageUp, platform.KeyPageDown, platform.KeyF10,
	platform.KeyComma, platform.KeyPeriod, platform.KeyMinus, platform.KeyEqual, platform.KeySlash,
}

// simScales are the display scales a step moves the window to.
var simScales = []float32{1, 1.25, 1.5, 2, 2.5, 3}

// Step sends one random action to window id and advances the clock.
func (g *SimUser) Step(s *Sim, id int) error {
	g.Steps++
	v := s.windows[id]
	w, h := float32(v.a.w), float32(v.a.h)
	r := g.rng
	switch n := r.IntN(100); {
	case n < 18:
		return g.move(s, id, w, h)
	case n < 32:
		return g.click(s, id, w, h)
	case n < 38:
		return g.drag(s, id, w, h)
	case n < 48:
		return g.scroll(s, id, w, h)
	case n < 66:
		return g.key(s, id)
	case n < 76:
		return g.typeText(s, id)
	case n < 83:
		return g.compose(s, id)
	case n < 86:
		return g.paste(s, id)
	case n < 90:
		return g.resize(s, id)
	case n < 93:
		return g.windowFocus(s, id)
	case n < 95:
		g.inside = false
		if err := g.send(s, id, platform.Event{Kind: platform.EvPointerLeave}); err != nil {
			return err
		}
		return g.frames(s, 1)
	default: // a pause: caret blinks, animations, tooltips, idle cleanups
		return s.Advance(time.Duration(r.IntN(3000)) * time.Millisecond)
	}
}

// Run sends n steps to window id, stopping at the first failure.
func (g *SimUser) Run(s *Sim, id, n int) error {
	for i := 0; i < n; i++ {
		if err := g.Step(s, id); err != nil {
			return fmt.Errorf("input seed %d step %d: %w", g.seed, g.Steps, err)
		}
	}
	return nil
}

func (g *SimUser) send(s *Sim, id int, e platform.Event, data ...[]byte) error {
	g.Events++
	return s.Event(id, e, data...)
}

// frames advances the clock by n display ticks.
func (g *SimUser) frames(s *Sim, n int) error { return s.Advance(time.Duration(n) * s.opts.Tick) }

// point is a pointer position: mostly inside the window, sometimes just
// outside it, sometimes near where the pointer already is.
func (g *SimUser) point(w, h float32) (float32, float32) {
	r := g.rng
	if g.inside && r.IntN(3) == 0 {
		return g.x + float32(r.NormFloat64()*20), g.y + float32(r.NormFloat64()*20)
	}
	return r.Float32()*(w+20) - 10, r.Float32()*(h+20) - 10
}

func (g *SimUser) moveTo(s *Sim, id int, x, y float32, mods platform.Mods) error {
	g.x, g.y, g.inside = x, y, true
	return g.send(s, id, platform.Event{Kind: platform.EvPointerMove, X: x, Y: y, Mods: mods})
}

func (g *SimUser) mods() platform.Mods {
	var m platform.Mods
	for _, bit := range [4]platform.Mods{platform.ModShift, platform.ModCtrl, platform.ModAlt, platform.ModSuper} {
		if g.rng.IntN(8) == 0 {
			m |= bit
		}
	}
	return m
}

func (g *SimUser) move(s *Sim, id int, w, h float32) error {
	for range 1 + g.rng.IntN(4) {
		x, y := g.point(w, h)
		if err := g.moveTo(s, id, x, y, 0); err != nil {
			return err
		}
		if err := g.frames(s, 1); err != nil {
			return err
		}
	}
	return nil
}

func (g *SimUser) button(s *Sim, id int, b platform.Button, down bool, clicks uint8, mods platform.Mods) error {
	e := platform.Event{Kind: platform.EvPointerButton, Button: b, X: g.x, Y: g.y, Mods: mods, Clicks: clicks}
	if down {
		e.Flags = platform.FlagDown
	}
	return g.send(s, id, e)
}

// click presses and releases a button where the pointer goes: left mostly,
// right for context menus, two or three clicks in a row at times.
func (g *SimUser) click(s *Sim, id int, w, h float32) error {
	r := g.rng
	x, y := g.point(w, h)
	mods := g.mods() &^ platform.ModSuper // (⌘-click is a selection gesture too, but ⌘ on its own in a step is rarer)
	if r.IntN(10) == 0 {
		mods |= platform.ModSuper
	}
	if err := g.moveTo(s, id, x, y, mods); err != nil {
		return err
	}
	b := platform.ButtonLeft
	switch r.IntN(10) {
	case 0, 1:
		b = platform.ButtonRight
	case 2:
		b = platform.ButtonMiddle
	}
	n := 1
	if b == platform.ButtonLeft && r.IntN(5) == 0 {
		n = 2 + r.IntN(2)
	}
	for c := 1; c <= n; c++ {
		if err := g.button(s, id, b, true, uint8(c), mods); err != nil {
			return err
		}
		if err := g.frames(s, r.IntN(2)); err != nil { // up in the same frame, or the next
			return err
		}
		if err := g.button(s, id, b, false, uint8(c), mods); err != nil {
			return err
		}
		if err := g.frames(s, 1); err != nil {
			return err
		}
	}
	return nil
}

// drag presses, moves over several frames and releases (or leaves the window
// with the button down, then comes back and releases).
func (g *SimUser) drag(s *Sim, id int, w, h float32) error {
	r := g.rng
	x, y := g.point(w, h)
	if err := g.moveTo(s, id, x, y, 0); err != nil {
		return err
	}
	mods := g.mods()
	if err := g.button(s, id, platform.ButtonLeft, true, 1, mods); err != nil {
		return err
	}
	tx, ty := g.point(w, h)
	steps := 2 + r.IntN(12)
	for i := 1; i <= steps; i++ {
		t := float32(i) / float32(steps)
		if err := g.moveTo(s, id, x+(tx-x)*t, y+(ty-y)*t, mods); err != nil {
			return err
		}
		if err := g.frames(s, 1); err != nil {
			return err
		}
	}
	if r.IntN(6) == 0 {
		if err := g.frames(s, 30); err != nil { // held still: autoscroll, hold timers
			return err
		}
	}
	if err := g.button(s, id, platform.ButtonLeft, false, 1, mods); err != nil {
		return err
	}
	return g.frames(s, 1)
}

// scroll sends a wheel notch or two, or a trackpad gesture with its phases
// and, often, momentum; the axis is mostly vertical.
func (g *SimUser) scroll(s *Sim, id int, w, h float32) error {
	r := g.rng
	x, y := g.point(w, h)
	if err := g.moveTo(s, id, x, y, 0); err != nil {
		return err
	}
	dx, dy := float32(0), float32(r.NormFloat64()*60)
	if r.IntN(4) == 0 {
		dx = float32(r.NormFloat64() * 60)
	}
	if r.IntN(2) == 0 { // a wheel
		for range 1 + r.IntN(3) {
			if err := g.send(s, id, platform.Event{Kind: platform.EvScroll, X: x, Y: y, DX: dx, DY: dy}); err != nil {
				return err
			}
			if err := g.frames(s, 1); err != nil {
				return err
			}
		}
		return g.frames(s, r.IntN(20))
	}
	e := platform.Event{Kind: platform.EvScroll, X: x, Y: y, Flags: platform.FlagPrecise, Phase: platform.PhaseBegan}
	if err := g.send(s, id, e); err != nil {
		return err
	}
	n := 2 + r.IntN(10)
	for i := 0; i < n; i++ {
		e.Phase, e.DX, e.DY = platform.PhaseChanged, dx/4*float32(r.Float64()+.5), dy/4*float32(r.Float64()+.5)
		if err := g.send(s, id, e); err != nil {
			return err
		}
		if err := g.frames(s, 1); err != nil {
			return err
		}
	}
	e.Phase, e.DX, e.DY = platform.PhaseEnded, 0, 0
	if r.IntN(8) == 0 {
		e.Phase = platform.PhaseCancelled
	}
	if err := g.send(s, id, e); err != nil {
		return err
	}
	if r.IntN(3) != 0 { // momentum, decaying
		e.Phase = platform.PhaseNone
		for i, m := 0, 4+r.IntN(20); i < m; i++ {
			e.Momentum = platform.PhaseChanged
			if i == 0 {
				e.Momentum = platform.PhaseBegan
			}
			f := float32(m-i) / float32(m)
			e.DX, e.DY = dx/4*f, dy/4*f
			if err := g.send(s, id, e); err != nil {
				return err
			}
			if err := g.frames(s, 1); err != nil {
				return err
			}
		}
		e.Momentum, e.DX, e.DY = platform.PhaseEnded, 0, 0
		if err := g.send(s, id, e); err != nil {
			return err
		}
	}
	return g.frames(s, 1+r.IntN(40)) // the rubber band settles
}

// key presses and releases a key, with modifiers at times, or holds it for
// a few repeats. ⌘Q and ⌘W are never sent.
func (g *SimUser) key(s *Sim, id int) error {
	r := g.rng
	var k platform.Key
	var ch rune
	switch n := r.IntN(10); {
	case n < 6:
		k = simKeys[r.IntN(len(simKeys))]
		switch k {
		case platform.KeySpace:
			ch = ' '
		case platform.KeyComma:
			ch = ','
		case platform.KeyPeriod:
			ch = '.'
		case platform.KeyMinus:
			ch = '-'
		case platform.KeyEqual:
			ch = '='
		case platform.KeySlash:
			ch = '/'
		}
	case n < 9:
		i := r.IntN(26)
		k, ch = platform.KeyA+platform.Key(i), rune('a'+i)
	default:
		i := r.IntN(10)
		k, ch = platform.Key0+platform.Key(i), rune('0'+i)
	}
	mods := g.mods()
	if mods&platform.ModSuper != 0 && (k == platform.KeyA+('q'-'a') || k == platform.KeyA+('w'-'a')) {
		mods &^= platform.ModSuper
	}
	e := platform.Event{Kind: platform.EvKey, Key: k, Char: ch, Mods: mods, Flags: platform.FlagDown}
	if err := g.send(s, id, e); err != nil {
		return err
	}
	// The platform types printable keys without ⌘ or Ctrl as text as well.
	if ch != 0 && mods&(platform.ModSuper|platform.ModCtrl) == 0 && !g.composing {
		c := ch
		if mods&platform.ModShift != 0 && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if err := g.send(s, id, platform.Event{Kind: platform.EvText}, []byte(string(c))); err != nil {
			return err
		}
	}
	for range r.IntN(3) * r.IntN(4) { // repeats
		if err := g.frames(s, 2); err != nil {
			return err
		}
		e.Flags = platform.FlagDown | platform.FlagRepeat
		if err := g.send(s, id, e); err != nil {
			return err
		}
	}
	if err := g.frames(s, r.IntN(2)); err != nil {
		return err
	}
	e.Flags = 0
	if err := g.send(s, id, e); err != nil {
		return err
	}
	return g.frames(s, 1)
}

func (g *SimUser) text() []byte {
	r := g.rng
	t := simTexts[r.IntN(len(simTexts))]
	if r.IntN(6) == 0 {
		t += simTexts[r.IntN(len(simTexts))]
	}
	return []byte(t)
}

// typeText commits text, or replaces the character before the caret as
// press-and-hold accents do.
func (g *SimUser) typeText(s *Sim, id int) error {
	e := platform.Event{Kind: platform.EvText}
	if g.rng.IntN(6) == 0 {
		e.SelStart, e.SelLen = -1, 1
	}
	if err := g.send(s, id, e, g.text()); err != nil {
		return err
	}
	return g.frames(s, 1)
}

// compose runs an input-method composition: marked text that changes over a
// few frames with its caret or converted part somewhere in it, then a commit,
// a cancel (empty marked text), or the window losing the focus mid-way.
func (g *SimUser) compose(s *Sim, id int) error {
	r := g.rng
	g.composing = true
	defer func() { g.composing = false }()
	var marked []byte
	for i, n := 0, 1+r.IntN(4); i < n; i++ {
		marked = append(marked, g.text()...)
		e := platform.Event{Kind: platform.EvPreedit}
		// The caret or the converted part: in the text, at a byte the input
		// method names (a character start, or anywhere, as a careless one
		// might).
		start := r.IntN(len(marked) + 1)
		e.SelStart = int32(start)
		if r.IntN(3) == 0 {
			e.SelLen = int32(r.IntN(len(marked) - start + 1))
		}
		if err := g.send(s, id, e, marked); err != nil {
			return err
		}
		if err := g.frames(s, 1+r.IntN(3)); err != nil {
			return err
		}
	}
	switch r.IntN(6) {
	case 0: // cancelled
		if err := g.send(s, id, platform.Event{Kind: platform.EvPreedit}); err != nil {
			return err
		}
	case 1: // the window loses the focus with the composition up
		if err := g.windowFocus(s, id); err != nil {
			return err
		}
		if err := g.send(s, id, platform.Event{Kind: platform.EvPreedit}); err != nil {
			return err
		}
	case 2: // a click elsewhere commits it, as AppKit does
		if err := g.send(s, id, platform.Event{Kind: platform.EvPreedit}); err != nil {
			return err
		}
		if err := g.send(s, id, platform.Event{Kind: platform.EvText}, marked); err != nil {
			return err
		}
	default: // committed: the marked text ends and the text arrives
		if err := g.send(s, id, platform.Event{Kind: platform.EvPreedit}); err != nil {
			return err
		}
		if err := g.send(s, id, platform.Event{Kind: platform.EvText}, g.text()); err != nil {
			return err
		}
	}
	return g.frames(s, 1)
}

// paste sets the clipboard and presses ⌘V (and ⌘C, ⌘X, ⌘A, ⌘Z around it).
func (g *SimUser) paste(s *Sim, id int) error {
	r := g.rng
	if r.IntN(2) == 0 {
		s.Clipboard(g.text())
	}
	for _, c := range []rune{rune("acxvzv"[r.IntN(6)]), 'v'} {
		k := platform.KeyA + platform.Key(c-'a')
		mods := platform.ModSuper
		if r.IntN(4) == 0 {
			mods |= platform.ModShift
		}
		if err := g.send(s, id, platform.Event{Kind: platform.EvKey, Key: k, Char: c, Mods: mods, Flags: platform.FlagDown}); err != nil {
			return err
		}
		if err := g.send(s, id, platform.Event{Kind: platform.EvKey, Key: k, Char: c, Mods: mods}); err != nil {
			return err
		}
		if err := g.frames(s, 1); err != nil {
			return err
		}
	}
	return g.frames(s, 2)
}

// resize changes the window's size or its scale (a move to another display),
// never below the configured minimum.
func (g *SimUser) resize(s *Sim, id int) error {
	r := g.rng
	v := s.windows[id]
	scale := v.a.scale
	if r.IntN(3) == 0 {
		scale = simScales[r.IntN(len(simScales))]
	}
	minW, minH := max(v.a.cfg.MinWidth, 200), max(v.a.cfg.MinHeight, 150)
	wPt, hPt := float32(v.a.w)/v.a.scale, float32(v.a.h)/v.a.scale
	if r.IntN(2) == 0 {
		wPt, hPt = minW+r.Float32()*1200, minH+r.Float32()*800
	}
	e := platform.Event{Kind: platform.EvResize, W: int32(wPt*scale + .5), H: int32(hPt*scale + .5), Scale: scale}
	if err := g.send(s, id, e); err != nil {
		return err
	}
	return g.frames(s, 1+r.IntN(3))
}

// windowFocus deactivates the window, or activates it again.
func (g *SimUser) windowFocus(s *Sim, id int) error {
	g.unfocused = !g.unfocused
	e := platform.Event{Kind: platform.EvFocus}
	if !g.unfocused {
		e.Flags = platform.FlagDown
	}
	if err := g.send(s, id, e); err != nil {
		return err
	}
	return g.frames(s, 1)
}

// Settle releases what the stream may hold (the window focus) and lets the
// window come to rest.
func (g *SimUser) Settle(s *Sim, id int) error {
	if g.unfocused {
		if err := g.windowFocus(s, id); err != nil {
			return err
		}
	}
	return s.Advance(15 * time.Second)
}
