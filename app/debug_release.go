//go:build gnui_release

package app

// devBuild is false in a build with the gnui_release tag: no chord turns the
// debug overlay on and it does not record caller program counters.
const devBuild = false
