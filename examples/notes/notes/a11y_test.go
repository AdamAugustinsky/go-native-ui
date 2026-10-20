package notes

import (
	"testing"

	"gonativeui/widget/widgettest"
)

// Every interactive element of the window has an accessible name.
func TestAccessibleNames(t *testing.T) {
	r := newRig(t, false)
	widgettest.LintNames(t, "notes", r.Tree())
}
