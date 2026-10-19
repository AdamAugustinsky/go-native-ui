// Command hello is the smallest program the gnui tests package: it prints its
// arguments and a marker environment variable, then exits.
package main

import (
	"fmt"
	"os"
	"strings"
)

// build is changed by the tests with -ldflags -X to make a different binary.
var build = "1"

func main() {
	fmt.Printf("hello build=%s args=%s env=%s\n", build, strings.Join(os.Args[1:], ","), os.Getenv("GNUI_TEST_MARK"))
	fmt.Fprintln(os.Stderr, "hello on stderr")
}
