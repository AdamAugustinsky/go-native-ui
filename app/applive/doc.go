// Package applive holds the live tests of the application object (phase 6,
// F3): the quit policy through real AppKit routes (a self-addressed quit
// Apple event for the Dock's Quit, ⌘Q through the native menu), deferred
// answers with sheets and app events delivered during AppKit's wait, quit
// deadlines, open events, the Dock menu and the delivery classes saturated
// during a quit.
//
// An allowed quit ends the process (AppKit's terminate:), so every scenario
// runs in a child process of the test binary: the test starts it with
// GNUI_APPLIVE set to the scenario's name, and reads the "R key=value" lines
// it prints and its exit status.
package applive
