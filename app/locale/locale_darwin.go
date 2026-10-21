//go:build darwin

package locale

import (
	"sync"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// macOS: NSLocale through internal/cf, on whatever goroutine asks (a frame and
// an autorelease pool of its own), and one block observer of
// NSCurrentLocaleDidChangeNotification on the default center. The observer's
// block is global and runs on the thread that posts the notification; it only
// marks the cache stale and wakes the loop.

const volatile = false

var syms struct {
	once    sync.Once
	err     error
	country uintptr // NSLocaleCountryCode
	didChg  uintptr // NSCurrentLocaleDidChangeNotification
}

func load() {
	if syms.err = cf.Init(); syms.err != nil {
		return
	}
	lib, err := cf.Framework("Foundation")
	if err != nil {
		syms.err = err
		return
	}
	syms.country, syms.didChg = cf.Const(lib, "NSLocaleCountryCode"), cf.Const(lib, "NSCurrentLocaleDidChangeNotification")
	if syms.country == 0 || syms.didChg == 0 {
		syms.err = cf.ErrUnavailable
	}
}

// watch installs the observer. A failure leaves Serial at 0 and Current
// re-reading only when asked to: it reports nothing wrong, as Current reads
// fresh values on its first call either way.
func watch() {
	syms.once.Do(load)
	if syms.err != nil {
		return
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()
	block := objc.NewGlobalBlock("v16@?0@8", func(*ffi.CB) { changed() })
	center := cf.Msg(fr, cf.Cls(fr, "NSNotificationCenter"), cf.Sel(fr, "defaultCenter"))
	if center == 0 {
		return
	}
	// addObserverForName:object:queue:usingBlock: with no queue calls the block
	// on the posting thread. The center keeps the returned observer and the block.
	cf.Msg(fr, center, cf.Sel(fr, "addObserverForName:object:queue:usingBlock:"), syms.didChg, 0, 0, uintptr(block))
}

func read() Info {
	syms.once.Do(load)
	if syms.err != nil {
		return Info{}
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()

	var info Info
	langs := cf.Msg(fr, cf.Cls(fr, "NSLocale"), cf.Sel(fr, "preferredLanguages"))
	if langs != 0 {
		for i, n := 0, cf.ArrayLen(fr, langs); i < n; i++ {
			if tag := normalizeTag(cf.GoString(fr, cf.ArrayAt(fr, langs, i))); tag != "" {
				info.Languages = append(info.Languages, tag)
			}
		}
	}
	loc := cf.Msg(fr, cf.Cls(fr, "NSLocale"), cf.Sel(fr, "autoupdatingCurrentLocale"))
	if loc == 0 {
		return info
	}
	info.Region = cf.GoString(fr, cf.Msg(fr, loc, cf.Sel(fr, "objectForKey:"), syms.country))
	// The template "j" asks for the user's preferred hour cycle: "h a" or "HH".
	tmpl := cf.String(fr, "j")
	pat := cf.Msg(fr, cf.Cls(fr, "NSDateFormatter"), cf.Sel(fr, "dateFormatFromTemplate:options:locale:"), tmpl, 0, loc)
	cf.CFRelease(fr, tmpl)
	info.Hour24, _ = hour24FromPattern(cf.GoString(fr, pat))
	if h24, forced := forcedHourCycle(fr); forced { // System Settings' 24-hour switch, read afresh: a running process's locale caches it
		info.Hour24 = h24
	}
	if info.Region == "" && len(info.Languages) > 0 {
		info.Region = regionOf(info.Languages[0])
	}
	return info
}

// forcedHourCycle reads the 24-hour switch of System Settings (Language &
// Region), which it stores as AppleICUForce24HourTime and
// AppleICUForce12HourTime in the global domain. forced is false when the user
// has not forced either.
func forcedHourCycle(fr *ffi.Frame) (h24, forced bool) {
	defs := cf.Msg(fr, cf.Cls(fr, "NSUserDefaults"), cf.Sel(fr, "standardUserDefaults"))
	if defs == 0 {
		return false, false
	}
	flag := func(name string) bool {
		key := cf.String(fr, name)
		defer cf.CFRelease(fr, key)
		return cf.MsgBool(fr, defs, cf.Sel(fr, "boolForKey:"), key)
	}
	switch {
	case flag("AppleICUForce24HourTime"):
		return true, true
	case flag("AppleICUForce12HourTime"):
		return false, true
	}
	return false, false
}
