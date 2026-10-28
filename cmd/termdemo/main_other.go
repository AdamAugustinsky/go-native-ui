//go:build !darwin && !linux

// Command termdemo is a terminal window over libghostty-vt; it runs a local
// program on a pseudo-terminal, which it has on macOS and Linux only (a
// Windows ConPTY is not part of phase 8).
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "termdemo: macOS and Linux only")
	os.Exit(1)
}
