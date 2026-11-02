package format_test

import (
	"fmt"
	"time"

	"gonativeui/format"
)

// The texts in Brazilian Portuguese, as rexui.SetStrings(rexui.StringsPtBR)
// sets them.
func Example() {
	format.SetLocale(format.PtBR)
	defer format.SetLocale(format.English)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	fmt.Println(format.Ago(now, now.Add(-3*time.Hour)))
	fmt.Println(format.Compact(12_345), format.Grouped(18_400))
	sched, _ := format.Schedule("0 9 * * 1-5")
	fmt.Println(sched)
	fmt.Println(format.ShortPath("/Users/ana/src", "/Users/ana"))
	// Output:
	// 3h
	// 12,3 mil 18.400
	// De segunda a sexta às 9h
	// ~/src
}
