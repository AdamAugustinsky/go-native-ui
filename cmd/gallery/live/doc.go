// Package live holds the gallery's tests that open a real window. They are a
// package of their own because, in the same test binary as the offscreen tests
// (which make and close a text system), opening an AppKit window aborts
// inside AppKit now and then (NSFont creation sent an unrecognised selector);
// a binary of their own keeps them clear of that. The cause is not tracked
// down: an app opens its window before it makes its text system.
package live
