// Package locale reports the user's language and regional settings: the
// preferred languages in order, the region, and whether the clock is 24-hour.
//
//	info := locale.Current()
//	info.Languages // ["pt-BR", "en-US"]: BCP 47, most preferred first
//	info.Region    // "BR"
//	info.Hour24    // true
//
//	// every frame (or on a wake): a cheap atomic read
//	if s := locale.Serial(); s != seen { seen = s; info = locale.Current() }
//
// # Cost
//
// Current reads a cached value, so it is cheap enough to call whenever the app
// wants the settings; the first call, and the first after a change, ask the OS
// (a few Objective-C messages on macOS, a fork on Linux) and allocate. Do the
// first call at startup or from a worker, not in the middle of a frame.
//
// # Changes
//
// Serial counts the changes the system has reported, and every change also
// calls platform.Wake, so a loop that is idle wakes up and can compare the
// serial. This is how theme follows the system appearance; when the application
// object (F3) lands, the same observation is delivered to the app as an event
// of the "changes" class (D37 class 4, one coalesced latest value), and Serial
// stays as the poll form of it.
//
//   - macOS: NSCurrentLocaleDidChangeNotification, observed from the first
//     call of Current or Serial on. The notification is posted when the
//     user changes Language & Region in System Settings, including the 24-hour
//     clock switch.
//   - Windows: no change is reported yet. Serial stays 0 and Current reads the
//     settings afresh on every call (a few kernel calls and a small
//     allocation), so it always sees the latest. WM_SETTINGCHANGE belongs to
//     the window loop and will come with F3.
//   - Linux and other Unix systems: the settings come from the process's
//     environment (LANGUAGE, LC_ALL, LC_MESSAGES, LC_TIME, LANG), which cannot
//     change under a running program, so Serial stays 0 and the value is read
//     once. The desktop's own 12/24-hour switch (GNOME's clock-format) is not
//     consulted; Hour24 comes from the locale's time format (the locale
//     command), and is false when that cannot be read.
package locale

import (
	"strings"
	"sync"
	"sync/atomic"

	"gonativeui/platform"
)

// Info is the user's locale settings at one moment.
type Info struct {
	// Languages are the preferred languages as BCP 47 tags ("en-US",
	// "pt-BR", "zh-Hans-CN"), the most preferred first. It may be empty
	// when the system does not say. Do not modify the slice.
	Languages []string
	// Region is the ISO 3166 region the user chose for formats ("US", "BR"),
	// or "" when unknown.
	Region string
	// Hour24 is true when the clock is shown with 24 hours.
	Hour24 bool
}

var (
	cur    atomic.Pointer[Info] // the cached value; nil before the first read
	stale  atomic.Bool          // a change was reported since cur was read
	serial atomic.Uint32
	once   sync.Once
	readMu sync.Mutex // one read at a time

	// wake is platform.Wake, and readFn the OS read; tests replace them.
	wake   = platform.Wake
	readFn = read
)

// Current returns the user's locale settings. See the package comment for what
// it costs. It may be called from any goroutine.
func Current() Info {
	once.Do(watch)
	if volatile {
		return readFn()
	}
	if p := cur.Load(); p != nil && !stale.Load() {
		return *p
	}
	readMu.Lock()
	defer readMu.Unlock()
	stale.Store(false) // before reading: a change that lands during the read marks it stale again
	v := readFn()
	cur.Store(&v)
	return v
}

// Serial counts the changes the system has reported since the first call of
// Current or Serial. It is 0 where no changes are reported (see the package
// comment). It may be called from any goroutine and does not allocate.
func Serial() uint32 {
	once.Do(watch)
	return serial.Load()
}

// changed records a change reported by the system: any thread.
func changed() {
	stale.Store(true)
	serial.Add(1)
	wake()
}

// normalizeTag turns a POSIX locale name or a loosely written tag into a
// BCP 47 tag: "pt_BR.UTF-8" and "pt_BR@euro" become "pt-BR". The empty string,
// "C" and "POSIX" (which name no language) give "".
func normalizeTag(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".@"); i >= 0 {
		s = s[:i]
	}
	if s == "" || s == "C" || s == "POSIX" {
		return ""
	}
	return strings.ReplaceAll(s, "_", "-")
}

// regionOf finds the region subtag in a BCP 47 tag: two letters or three
// digits after the language, as in "en-US", "zh-Hans-CN", "es-419".
func regionOf(tag string) string {
	parts := strings.Split(tag, "-")
	for _, p := range parts[1:] {
		switch {
		case len(p) == 2 && isUpper(p):
			return p
		case len(p) == 3 && isDigits(p):
			return p
		}
	}
	return ""
}

func isUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// hour24FromPattern reads a date or time format pattern, either ICU/CLDR
// ("HH:mm", "h:mm a", with 'quoted' text) or C strftime ("%H:%M", "%r"), and
// says whether it shows 24 hours. known is false when it shows no hour at all.
func hour24FromPattern(p string) (h24, known bool) {
	if strings.Contains(p, "%") { // strftime
		for i := 0; i+1 < len(p); i++ {
			if p[i] != '%' {
				continue
			}
			i++
			for i < len(p) && strings.IndexByte("-_0^#EO", p[i]) >= 0 { // flags and modifiers
				i++
			}
			if i >= len(p) {
				break
			}
			switch p[i] {
			case 'H', 'k', 'T', 'R':
				return true, true
			case 'I', 'l', 'r', 'p', 'P':
				return false, true
			}
		}
		return false, false
	}
	inQuote := false
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '\'':
			inQuote = !inQuote
		case inQuote:
		case c == 'H' || c == 'k':
			return true, true
		case c == 'h' || c == 'K':
			return false, true
		}
	}
	return false, false
}
