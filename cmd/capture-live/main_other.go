//go:build !darwin && !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "capture-live: macOS and Windows only")
	os.Exit(2)
}
