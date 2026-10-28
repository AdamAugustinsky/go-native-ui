//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "probe-p4: Windows only")
	os.Exit(2)
}
