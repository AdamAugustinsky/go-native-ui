//go:build !darwin

// Command rexlook is a static recreation of the Rex look; it is implemented
// for macOS only so far.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "rexlook: darwin only")
	os.Exit(1)
}
