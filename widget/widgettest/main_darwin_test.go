//go:build darwin

package widgettest

import "testing"

// The harness's own live tests need the main thread, for LiveApps in the test
// binary and in the child processes of its scenarios.
func TestMain(m *testing.M) { RunMain(m) }
