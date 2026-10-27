//go:build windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

func init() { runtime.LockOSThread() } // the OLE STA is this thread

// RichEdit messages (richedit.h; WM_USER is 0x400) and flags.
const (
	emSetSel              = 0x00B1
	emReplaceSel          = 0x00C2
	emUndo                = 0x00C7
	emEmptyUndoBuffer     = 0x00CD
	emPosFromChar         = 0x00D6
	wmPaste               = 0x0302
	emGetCharFormat       = 0x0400 + 58
	emSetCharFormat       = 0x0400 + 68
	emRequestResize       = 0x0400 + 65
	emSetEventMask        = 0x0400 + 69
	emSetOleCallback      = 0x0400 + 70
	emSetLangOptions      = 0x0400 + 120
	emGetLangOptions      = 0x0400 + 121
	emSetTypographyOpts   = 0x0400 + 202
	emGetEditStyleEx      = 0x0400 + 276
	scfSelection          = 0x0001
	scfAll                = 0x0004
	cfmSize               = 0x80000000
	cfmFace               = 0x20000000
	cfmCharset            = 0x08000000
	enmRequestResize      = 0x00040000
	imfSpellChecking      = 0x0800 // Windows 8+ (richedit.h)
	toAdvancedTypography  = 0x0001
	wmImeStartComposition = 0x010D
	wmImeEndComposition   = 0x010E
	wmImeComposition      = 0x010F
	wmImeChar             = 0x0286
	gcsResultStr          = 0x0800
)

// charFormat2 is CHARFORMAT2W (richedit.h): 116 bytes; Go's layout matches
// C's (crBackColor aligned to 96).
type charFormat2 struct {
	size, mask, effects uint32
	height, offset      int32
	color               uint32
	charset, pitch      byte
	face                [32]uint16
	weight              uint16
	spacing             int16
	back                uint32
	lcid                uint32
	reserved            uint32
	style               int16
	kerning             uint16
	ulType, anim        byte
	revAuthor, ulColor  byte
}

type report struct {
	OleInitialize string         `json:"ole_initialize"`
	Emoji         map[string]any `json:"emoji"`
	IME           map[string]any `json:"ime"`
	Undo          map[string]any `json:"undo"`
	ResizeHeights map[string]any `json:"resize_heights"`
	Caret         map[string]any `json:"caret"`
	Spellcheck    map[string]any `json:"spellcheck"`
	OleCallback   map[string]any `json:"ole_callback"`
	Errors        []string       `json:"errors,omitempty"`
}

var (
	timeout = flag.Duration("timeout", 30*time.Second, "give up (exit 1) after this long")
	cf      charFormat2
	pt      [2]int32 // POINTL
)

func main() {
	flag.Parse()
	time.AfterFunc(*timeout, func() {
		fmt.Fprintf(os.Stderr, "probe-p5: timed out after %v\n", *timeout)
		os.Exit(1)
	})
	if unsafe.Sizeof(cf) != 116 {
		fail(fmt.Sprintf("CHARFORMAT2W is %d bytes, want 116", unsafe.Sizeof(cf)))
	}
	r := report{}
	r.OleInitialize = fmt.Sprintf("%#x", uint32(win32.HR(call(pOleInitialize, 0))))
	if win32.LoadSystem("msftedit.dll") == 0 {
		fail("msftedit.dll did not load from System32")
	}
	if err := makeWindows(); err != nil {
		fail(err.Error())
	}

	r.Emoji = emoji()
	r.IME = ime()
	r.Undo = undo()
	r.ResizeHeights = heights()
	r.Caret = caret()
	r.Spellcheck = spelling()
	r.OleCallback = callback()

	call(pDestroyWindow, wins.top)
	out, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(out))
}

func fail(why string) {
	out, _ := json.Marshal(map[string]string{"error": why})
	fmt.Println(string(out))
	os.Exit(1)
}

// posFromChar is the top left of character cp (EM_POSFROMCHAR, RichEdit
// 3.0+ form: wParam POINTL*, lParam the index).
func posFromChar(cp int) (int, int) {
	pt = [2]int32{}
	send(emPosFromChar, uintptr(unsafe.Pointer(&pt)), uintptr(cp))
	return int(pt[0]), int(pt[1])
}

func emoji() map[string]any {
	res := map[string]any{}
	setText("A\U0001F600B")
	send(emSetSel, 0, ^uintptr(0)) // all
	cf = charFormat2{mask: cfmSize, height: 480}
	cf.size = uint32(unsafe.Sizeof(cf))
	res["set_size"] = send(emSetCharFormat, scfAll, uintptr(unsafe.Pointer(&cf))) != 0
	res["typography_accepted"] = send(emSetTypographyOpts, toAdvancedTypography, toAdvancedTypography) != 0
	res["edit_style_ex"] = fmt.Sprintf("%#x", send(emGetEditStyleEx, 0, 0))
	send(emSetSel, 1, 3) // the emoji: two UTF-16 units
	cf = charFormat2{mask: cfmFace | cfmCharset}
	cf.size = uint32(unsafe.Sizeof(cf))
	send(emGetCharFormat, scfSelection, uintptr(unsafe.Pointer(&cf)))
	res["face"] = syscall.UTF16ToString(cf.face[:])
	res["charset"] = cf.charset
	send(emSetSel, 0, 0) // no selection highlight in the read
	pump()
	x0, y0 := posFromChar(1)
	x1, _ := posFromChar(3)
	h := 40 // 24 pt is 32 px at 96 dpi, with a margin
	res["cell"] = []int{x0, y0, x1, y0 + h}
	if grab() {
		res["coloured_pixels"] = colouredPixels(x0, y0, x1, y0+h)
	} else {
		res["coloured_pixels"] = -1
	}
	return res
}

func ime() map[string]any {
	res := map[string]any{}
	setText("")
	h := call(pImmGetContext, wins.edit)
	res["context"] = h != 0
	if h != 0 {
		call(pImmReleaseContext, wins.edit, h)
	}
	res["start_composition_result"] = send(wmImeStartComposition, 0, 0)
	res["text_after_start"] = text()
	res["composition_result"] = send(wmImeComposition, 0, gcsResultStr)
	res["text_after_composition"] = text()
	res["end_composition_result"] = send(wmImeEndComposition, 0, 0)
	send(wmImeChar, 0x3042, 1)
	pump()
	res["text_after_ime_char"] = text()
	res["ime_char_inserted"] = strings.Contains(text(), "あ")
	return res
}

func replaceSel(s string) {
	u := utf16z(s)
	send(emReplaceSel, 1, uintptr(unsafe.Pointer(&u[0]))) // wParam TRUE: can be undone
}

func undo() map[string]any {
	res := map[string]any{}
	hasDoc, hasDoc2 := openTOM()
	res["itextdocument"], res["itextdocument2"] = hasDoc, hasDoc2

	// run makes "base", then two replacements between begin and end (nil:
	// none), then one EM_UNDO, and returns the text left.
	run := func(begin, end func() int32) (string, int32, int32) {
		setText("base")
		send(emEmptyUndoBuffer, 0, 0)
		send(emSetSel, 4, 4)
		var b, e int32
		if begin != nil {
			b = begin()
		}
		replaceSel("one")
		replaceSel(" two")
		if end != nil {
			e = end()
		}
		send(emUndo, 0, 0)
		return text(), b, e
	}
	hr := func(v int32) string { return fmt.Sprintf("%#x", uint32(v)) }
	res["baseline_text_after_undo"], _, _ = run(nil, nil)
	grouped, b, e := run(beginGroup, endGroup)
	res["collection_begin_hr"], res["collection_end_hr"] = hr(b), hr(e)
	res["grouped_text_after_undo"] = grouped
	res["grouped"] = grouped == "base"
	sus, sb, se := run(func() int32 { return suspendUndo(true) }, func() int32 { return suspendUndo(false) })
	res["suspend_hr"], res["resume_hr"] = hr(sb), hr(se)
	res["suspended_text_after_undo"] = sus
	return res
}

func heights() map[string]any {
	res := map[string]any{}
	send(emSetEventMask, 0, enmRequestResize)
	for _, n := range []int{1, 3, 10} {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = fmt.Sprintf("line %d", i+1)
		}
		send(emSetSel, 0, 0)
		setText(strings.Join(lines, "\r"))
		wins.lastResizeHeight, wins.resizeNotes = -1, 0
		send(emRequestResize, 0, 0)
		pump()
		res[fmt.Sprint(n)] = map[string]int{"height": int(wins.lastResizeHeight), "notifications": wins.resizeNotes}
	}
	send(emSetEventMask, 0, 0)
	return res
}

func caret() map[string]any {
	setText("first line\rsecond")
	n := len(utf16z(text())) - 1
	x, y := posFromChar(n)
	_, y1 := posFromChar(len("first line\r"))
	_, y0 := posFromChar(0)
	return map[string]any{"end_x": x, "end_y": y, "line_height": y1 - y0}
}

func spelling() map[string]any {
	before := send(emGetLangOptions, 0, 0)
	set := send(emSetLangOptions, 0, before|imfSpellChecking)
	after := send(emGetLangOptions, 0, 0)
	return map[string]any{
		"options_before": fmt.Sprintf("%#x", before), "set_result": set,
		"options_after": fmt.Sprintf("%#x", after), "accepted": after&imfSpellChecking != 0,
	}
}

func callback() map[string]any {
	res := map[string]any{}
	o, err := makeCallback()
	if err != nil {
		res["error"] = err.Error()
		return res
	}
	ole.cb = o
	res["set"] = send(emSetOleCallback, 0, o.Ptr()) != 0
	setText("")
	res["clipboard_ok"] = putClipboard("pasted text")
	send(wmPaste, 0, 0)
	pump()
	res["query_accept_calls"] = ole.queryAccept
	res["query_accept_really_calls"] = ole.reallyCalls
	res["last_format"] = ole.lastFormat
	res["pasted_text"] = text()
	res["refs_after"] = o.Refs()
	return res
}
