//go:build darwin

package platform

import (
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Text from the other things a text drop can be: a web link, rich text (RTF,
// HTML). readText runs on the drop's worker (dropread_darwin.go).

// readText returns the text of the first pasteboard item that has some, nil if
// it has none or it is over limit bytes of UTF-8. Of an item, plain text wins;
// else a web link (public.url, not a file's), else rich text: RTF through
// NSAttributedString, HTML by dropping its tags (htmlToText).
func readText(f *ffi.Frame, items objc.ID, limit int) []byte {
	for i, cnt := 0, int(wsend(f, items, selURLCount)); i < cnt; i++ {
		item := wsend(f, items, selURLAt, uintptr(i))
		if t := itemText(f, item, limit); t != nil {
			if len(t) > limit {
				return nil
			}
			return t
		}
	}
	return nil
}

// itemText is the text of one item (see readText), nil if it has none; it may
// be longer than limit, which the caller checks.
func itemText(f *ffi.Frame, item objc.ID, limit int) []byte {
	if str := wsend(f, item, selStringForType, uintptr(tText)); str != 0 {
		return nsStrUTF8(f, str)
	}
	if str := wsend(f, item, selStringForType, uintptr(tURL)); str != 0 {
		if b := nsStrUTF8(f, str); len(b) > 0 && !hasFilePrefix(b) {
			return b
		}
	}
	if data := wsend(f, item, selDataForType, uintptr(tRTF)); data != 0 {
		if n := int(wsend(f, data, selDataLength)); n > 0 && n <= limit {
			as := wsend(f, wsend(f, clsNSAttrStr, selAlloc), selInitWithRTF, uintptr(data), 0)
			if as != 0 {
				defer wsend(f, as, selReleaseObj)
				if str := wsend(f, as, selString); str != 0 {
					return nsStrUTF8(f, str)
				}
			}
		}
	}
	if data := wsend(f, item, selDataForType, uintptr(tHTML)); data != 0 {
		if n := int(wsend(f, data, selDataLength)); n > 0 && n <= limit {
			src := unsafe.Slice((*byte)(ffi.PtrFromC(uintptr(wsend(f, data, selBytes)))), n)
			if t := htmlToText(src); len(t) > 0 {
				return t
			}
		}
	}
	return nil
}

func hasFilePrefix(b []byte) bool {
	return len(b) >= 5 && (b[0]|0x20) == 'f' && (b[1]|0x20) == 'i' && (b[2]|0x20) == 'l' && (b[3]|0x20) == 'e' && b[4] == ':'
}

// nsStringBytes copies an NSString's UTF-8 bytes; nil for an empty one.
func nsStrUTF8(f *ffi.Frame, str objc.ID) []byte {
	n := int(wsend(f, str, selLenBytes, 4 /* NSUTF8StringEncoding */))
	p := uintptr(wsend(f, str, selUTF8))
	if n <= 0 || p == 0 {
		return nil
	}
	return append([]byte(nil), unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)...)
}
