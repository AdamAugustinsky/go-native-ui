//go:build linux

package platform

import (
	"os"
	"testing"
)

// TestMain turns the test binary into the scripted compositor when a test
// starts it as a helper process (see fakewl_linux_test.go).
func TestMain(m *testing.M) {
	if os.Getenv("GNUI_FAKE_WL") != "" {
		fakeCompositorMain()
		return
	}
	os.Exit(m.Run())
}
