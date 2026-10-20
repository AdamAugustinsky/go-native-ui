// Package live holds the live-window tests of the widgettest harness: a real
// window, real AppKit events posted to it and read back through AppKit's own
// state (phase 6, T2). It is a package of its own because its tests need the
// main thread (widgettest.RunMain) for the whole run.
package live
