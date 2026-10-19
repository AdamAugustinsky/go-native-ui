package live

import (
	"testing"

	"gonativeui/widget/widgettest"
)

// The live window test needs the main thread (see widgettest.RunMain).
func TestMain(m *testing.M) { widgettest.RunMain(m) }
