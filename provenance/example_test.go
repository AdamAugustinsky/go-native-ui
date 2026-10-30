package provenance_test

import (
	"fmt"

	"gonativeui/provenance"
)

// An application can report the framework compiled into it, for example in
// an about box or a crash report; a packager links in the revision with
// -ldflags "-X gonativeui/provenance.FrameworkRevision=<commit>".
func ExampleCompiled() {
	info := provenance.Compiled()
	fmt.Println(info.Schema, info.Framework.Module)
	// Output: gnui.provenance/v1 gonativeui
}
