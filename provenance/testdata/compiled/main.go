// Command compiled prints provenance.Compiled() as JSON, for the test that
// builds it with -ldflags -X.
package main

import (
	"os"

	"gonativeui/provenance"
)

func main() { os.Stdout.Write(provenance.Compiled().JSON()) }
