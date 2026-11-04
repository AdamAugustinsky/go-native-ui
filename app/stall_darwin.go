package app

import (
	"os"
	"os/exec"
	"strconv"
)

// nativeSample writes 2 s of the process's native stacks to path: where the
// main thread waits (a run loop, a drawable, a lock), which the Go stacks of a
// thread inside native code do not show.
var nativeSample = func(path string) {
	exec.Command("/usr/bin/sample", strconv.Itoa(os.Getpid()), "2", "-mayDie", "-file", path).Run()
}
