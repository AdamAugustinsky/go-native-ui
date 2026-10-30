//go:build !gnuiassert

package core

// Assertions reports whether the invariant assertions of core and ui are
// compiled in: build with -tags gnuiassert to turn them on. Without the tag
// it is a false constant, so every `if core.Assertions { ... }` block is
// removed by the compiler and release builds pay nothing.
const Assertions = false
