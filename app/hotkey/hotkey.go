// Package hotkey registers global shortcuts: key chords that reach the app
// whichever app is active, such as the chord that opens a palette (the
// phase 8 plan's W7 and D70).
//
//	const paletteKey hotkey.ID = 1
//
//	err := hotkey.Register(paletteKey, hotkey.Shortcut{Key: 'K', Mods: ui.ModAlt, Primary: true}) // ⌥⌘K, Ctrl+Alt+K
//	if errors.Is(err, hotkey.ErrTaken) {
//		// another app holds the chord: say so in the settings, offer another
//	}
//
//	func (p *palette) Event(a *app.App, e *app.Event) {
//		if id, ok := hotkey.Pressed(e); ok && id == paletteKey {
//			p.win.Show() // a panel (app.Config.Panel) takes the keyboard
//		}
//		a.DefaultEvent(e)
//	}
//
//	hotkey.Unregister(paletteKey)
//
// A press arrives as an app.EvService from platform.SourceHotkey, on the main
// thread between frames, which Pressed decodes. A chord held down delivers
// one press: the key's auto-repeat is not reported.
//
// No permission is needed and no keystroke other than the registered chords
// reaches the app: there is no event tap or keyboard hook, so idle stays at
// 0 wakeups however much the user types elsewhere.
//
// # Threads
//
// Every call is made on the main thread: from Handler.Start or Handler.Event,
// or at a safe point. Register fails with ErrNotStarted before the
// application object exists.
//
// # What each OS does
//
//   - macOS: Carbon's RegisterEventHotKey, with one InstallEventHandler
//     callback for kEventHotKeyPressed (HIToolbox, part of macOS; deprecated
//     API without a replacement that needs no permission).
//   - Windows: RegisterHotKey on the app's hidden window with MOD_NOREPEAT;
//     WM_HOTKEY becomes the press. A press grants the app the foreground, so
//     a panel shown from it comes to the front.
//   - Until W7 lands on Windows, and on Linux (D63): Register returns
//     ErrUnsupported (errors.Is errors.ErrUnsupported).
//
// The chord is a physical key: Carbon and RegisterHotKey name keys by
// position, so on macOS a letter is the key at that letter's place on a US
// keyboard. Shortcut.AppendLabel writes the chord as the OS shows it.
package hotkey

import (
	"errors"
	"fmt"

	"gonativeui/app"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/ui"
)

var (
	// ErrUnsupported: this OS has no global hotkeys here yet. It matches
	// errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("hotkey: %w", errors.ErrUnsupported)
	// ErrNotStarted: the application object does not exist yet (the call came
	// before app.Main).
	ErrNotStarted = errors.New("hotkey: the app has not started (call from Handler.Start or later)")
	// ErrTaken: another app (or this one, under another ID) holds the chord.
	// macOS reports only this app's own chords: Carbon registers a chord
	// another process holds without an error (the phase 8 plan's W7 note).
	ErrTaken = errors.New("hotkey: the shortcut is taken by another app")
	// ErrInvalid: the shortcut has no key, a key the OS cannot register, or
	// no modifier (a bare key would take the key from every app).
	ErrInvalid = errors.New("hotkey: the shortcut cannot be registered")
	// ErrFull: MaxHotkeys are registered.
	ErrFull = errors.New("hotkey: too many hotkeys")
)

// MaxHotkeys is the most hotkeys an app registers at once.
const MaxHotkeys = 32

// ID names a hotkey: the app's choice, non-zero, reported by Pressed.
type ID uint32

// Shortcut is the chord: a key (letters and digits by their ASCII upper-case
// value, the rest ui.KeyCode's named keys) with modifiers. Primary adds ⌘ on
// macOS and Ctrl elsewhere, as for menus, and AppendLabel writes the chord
// as the OS shows it (⌥⌘K on macOS, Ctrl+Alt+K elsewhere).
type Shortcut = menu.Shortcut

// Valid reports whether s can be registered at all: a key and at least one
// modifier besides Shift (Shift alone would take a character from every
// app). The OS may still refuse it (ErrTaken, ErrInvalid).
func Valid(s Shortcut) bool {
	m := s.Effective()
	return s.Key != 0 && m&^ui.ModShift != 0
}

// Register makes s a global hotkey named id, replacing the shortcut id had.
// It fails with ErrTaken when another app holds the chord, ErrInvalid for a
// chord that cannot be one, and ErrUnsupported where the OS has none here.
func Register(id ID, s Shortcut) error {
	if id == 0 || !Valid(s) {
		return ErrInvalid
	}
	return osRegister(id, s)
}

// Unregister releases hotkey id. An id not registered does nothing.
func Unregister(id ID) { osUnregister(id) }

// UnregisterAll releases every hotkey the app registered.
func UnregisterAll() { osUnregisterAll() }

// Registered reports the shortcut of hotkey id, if it is registered.
func Registered(id ID) (Shortcut, bool) { return osRegistered(id) }

// Simulate delivers a press of hotkey id through the OS's own path, as a
// real press does, for tests and measurements: on macOS a synthetic
// kEventHotKeyPressed sent to the application's event target, which reaches
// the app before Simulate returns (or at the end of the frame, from Build).
// No permission is needed. It fails for an id that is not registered, and
// with ErrUnsupported where there are no hotkeys. Main thread.
func Simulate(id ID) error { return osSimulate(id) }

// Pressed decodes a hotkey press (an app.EvService from
// platform.SourceHotkey, whose ID is the hotkey's). It allocates nothing.
func Pressed(e *app.Event) (ID, bool) {
	if e == nil || e.Kind != app.EvService || e.Source != platform.SourceHotkey {
		return 0, false
	}
	return ID(e.ID), true
}
