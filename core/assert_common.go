package core

// Assert panics with msg when ok is false. Call it inside an
// `if Assertions { ... }` block, so that a build without the gnuiassert tag
// evaluates neither the condition nor the call (TigerStyle: assertions state
// what must hold, and a failed one stops the program at the fault, not at a
// later symptom).
func Assert(ok bool, msg string) {
	if !ok {
		panic("core: assertion failed: " + msg)
	}
}
