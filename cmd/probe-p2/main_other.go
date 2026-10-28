//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "probe-p2: Windows only")
	os.Exit(2)
}
