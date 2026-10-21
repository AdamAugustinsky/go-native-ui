package live

import (
	"testing"

	"gonativeui/widget/widgettest"
)

// Each test runs in a process of its own: a composer app that does not end
// after Quit (issue #34) fails its test alone instead of every later one.
func TestMain(m *testing.M) { widgettest.RunMainIsolated(m) }
