//go:build windows

package locale

import (
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"gonativeui/internal/win32"
)

// Windows: GetUserPreferredUILanguages for the languages and GetLocaleInfoEx
// for the user's region and clock. Nothing is cached (volatile): the calls are
// cheap and always current.

const volatile = true

func watch() {}

const (
	muiLanguageName    = 0x8  // MUI_LANGUAGE_NAME: names such as "en-US"
	localeSISO3166Ctry = 0x5A // LOCALE_SISO3166CTRYNAME
	localeITime        = 0x23 // LOCALE_ITIME: "0" 12-hour, "1" 24-hour

)

var api struct {
	once                    sync.Once
	preferred, localeInfoEx uintptr
}

func load() {
	k := win32.LoadSystem("kernel32.dll")
	api.preferred = win32.Proc(k, "GetUserPreferredUILanguages")
	api.localeInfoEx = win32.Proc(k, "GetLocaleInfoEx")
}

func read() Info {
	api.once.Do(load)
	var info Info
	if api.preferred != 0 {
		var n uint32
		var size uint32
		// First call: how many UTF-16 units the multi-string needs.
		r, _, _ := syscall.SyscallN(api.preferred, muiLanguageName, uintptr(unsafe.Pointer(&n)), 0, uintptr(unsafe.Pointer(&size)))
		if r != 0 && size > 0 && size < 1<<16 {
			buf := make([]uint16, size)
			r, _, _ = syscall.SyscallN(api.preferred, muiLanguageName, uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
			if r != 0 {
				info.Languages = splitMulti(buf[:min(int(size), len(buf))])
			}
		}
	}
	info.Region = localeString(localeSISO3166Ctry)
	info.Hour24 = localeString(localeITime) == "1"
	if info.Region == "" && len(info.Languages) > 0 {
		info.Region = regionOf(info.Languages[0])
	}
	return info
}

// splitMulti splits a double-NUL-terminated UTF-16 list.
func splitMulti(b []uint16) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c != 0 {
			continue
		}
		if i > start {
			if tag := normalizeTag(string(utf16.Decode(b[start:i]))); tag != "" {
				out = append(out, tag)
			}
		}
		start = i + 1
	}
	return out
}

// localeString reads one string setting of the user's default locale
// (LOCALE_NAME_USER_DEFAULT is a nil name).
func localeString(lctype uintptr) string {
	if api.localeInfoEx == 0 {
		return ""
	}
	var buf [80]uint16
	r, _, _ := syscall.SyscallN(api.localeInfoEx, 0, lctype, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:])
}
