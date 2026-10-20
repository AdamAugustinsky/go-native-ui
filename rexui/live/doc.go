// Package live holds the live-window tests of the rexui components that take
// the keyboard through the system's own paths (D7a): a real window, real
// AppKit key events posted to it (so the arrows and Backspace arrive as
// doCommandBySelector: edit commands, as they do for a user) and the real
// NSColorPanel, read back through AppKit's own state. It is a package of its
// own because its tests need the main thread (widgettest.RunMain) for the whole
// run. They skip off macOS and need an unlocked console.
package live
