package locale_test

import (
	"gonativeui/app/locale"
)

// The user's language and regional settings. Current reads a cached value;
// the first call asks the OS, so make it at startup or from a worker, not in
// the middle of a frame. Serial counts the changes the system reported, a
// cheap atomic read to compare each frame.
func Example() {
	info := locale.Current()
	_ = info.Languages // ["pt-BR", "en-US"]: BCP 47, most preferred first
	_ = info.Region    // "BR"
	_ = info.Hour24    // true

	seen := locale.Serial()
	// every frame (or on a wake):
	if s := locale.Serial(); s != seen {
		seen = s
		info = locale.Current()
	}
}
