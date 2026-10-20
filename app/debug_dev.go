//go:build !gnui_release

package app

// devBuild is true in development builds, every build without the gnui_release
// tag: the debug overlay's chords work and it records caller program counters.
// (GNUI_DEBUG=overlay works in any build.)
const devBuild = true
