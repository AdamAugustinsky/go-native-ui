// Package menulive holds the live tests of the native menus: a real window
// with the native menu bar, real key equivalents sent through AppKit, and
// native menus that AppKit really tracks while the app's frames go on. They
// are a package of their own, as the gallery's live tests are, so that the
// window opens in a test binary of its own (see cmd/gallery/live).
package menulive
