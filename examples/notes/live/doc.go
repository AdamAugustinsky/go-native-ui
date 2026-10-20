// Package live holds notes' tests that open a real window, apart from the
// offscreen tests of package notes: the system's menu bar and its Edit menu,
// the alert sheet, real mouse, key and trackpad events, the zoom commands and
// the first frame on glass. They need the main thread (widgettest.RunMain) and
// an unlocked screen on macOS.
package live
