//go:build darwin && amd64

package ffi

// macOS means Apple silicon (DECISIONS.md D55): darwin/amd64 is not a
// target, so a build for it stops here, in the package every macOS
// binding goes through.
var _ = gonativeui_macOS_requires_Apple_silicon_see_DECISIONS_D55
