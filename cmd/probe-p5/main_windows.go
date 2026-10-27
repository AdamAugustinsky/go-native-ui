//go:build windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
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
	timeout = flag.Duration("timeout", 60*time.Second, "give up (exit 1) after this long")
	cf      charFormat2
	pt      [2]int32 // POINTL
)

func main() {
	flag.BoolVar(&d2dChild, "d2d-child", false, "internal: the EM_SWITCHTOD2D child process")
	flag.Parse()
	if d2dChild {
		d2dChildMain()
	}
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
	if err := makeDIB(); err != nil {
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

func ime() map[string]any {
	res := map[string]any{}
	hex := func(v uintptr) string { return fmt.Sprintf("%#x", v) }

	// 1. The messages alone, as the first run sent them: with no input
	// method composing, WM_IME_STARTCOMPOSITION/COMPOSITION/ENDCOMPOSITION
	// carry no string (the control reads it from the IME), so they change
	// nothing; WM_IME_CHAR carries its character and inserts it.
	setText("")
	m := map[string]any{}
	m["start_composition_result"] = send(wmImeStartComposition, 0, 0)
	m["composition_result"] = send(wmImeComposition, 0, gcsResultStr)
	m["end_composition_result"] = send(wmImeEndComposition, 0, 0)
	m["text_after_composition_messages"] = text()
	send(wmImeChar, 0x3042, 1)
	pump()
	m["text_after_ime_char"] = text()
	m["ime_char_inserted"] = strings.Contains(text(), "あ")
	res["messages_only"] = m

	// 2. Which keyboard layouts are input methods on this machine.
	var hkls [32]uintptr
	n := int(call(pGetKbdLayoutList, uintptr(len(hkls)), uintptr(unsafe.Pointer(&hkls[0]))))
	var layouts []string
	imeInstalled := false
	for _, h := range hkls[:min(n, len(hkls))] {
		isIME := win32.Bool(call(pImmIsIME, h))
		imeInstalled = imeInstalled || isIME
		layouts = append(layouts, fmt.Sprintf("%s ime=%v", hex(h), isIME))
	}
	res["layouts"] = layouts

	// 3. A real composition through IMM: load Microsoft's Japanese IME
	// (layout 00000411), open it on the control's context and set a
	// composition string, then complete it (ImmSetCompositionStringW
	// SCS_SETSTR, ImmNotifyIME NI_COMPOSITIONSTR CPS_COMPLETE). TSF input
	// methods answer IMM through Windows' IMM-on-TSF layer. Without the
	// Japanese language feature the layout is not an IME and this is moot.
	before := call(pGetKeyboardLayout, 0)
	ja := call(pLoadKeyboardLayout, uintptr(unsafe.Pointer(&utf16z("00000411")[0])), 1) // KLF_ACTIVATE
	c := map[string]any{"hkl": hex(ja), "is_ime": ja != 0 && win32.Bool(call(pImmIsIME, ja))}
	c["focus_is_edit"] = call(pGetFocus) == wins.edit
	c["foreground_is_host"] = call(pGetForeground) == wins.top
	setText("")
	h := call(pImmGetContext, wins.edit)
	c["context"] = h != 0
	if h != 0 {
		c["open_before"] = win32.Bool(call(pImmGetOpenStatus, h))
		c["set_open"] = win32.Bool(call(pImmSetOpenStatus, h, 1))
		comp := utf16z("あ")
		c["set_composition"] = win32.Bool(call(pImmSetCompString, h, 9, uintptr(unsafe.Pointer(&comp[0])), 2, 0, 0)) // SCS_SETSTR
		pump()
		c["text_while_composing"] = text()
		c["notify_complete"] = win32.Bool(call(pImmNotifyIME, h, 0x15, 1, 0)) // NI_COMPOSITIONSTR, CPS_COMPLETE
		pump()
		c["text_after_complete"] = text()
		call(pImmSetOpenStatus, h, 0)
		call(pImmReleaseContext, wins.edit, h)
	}
	c["committed"] = strings.Contains(text(), "あ")
	res["imm_composition"] = c
	call(pActivateKbdLayout, before, 0)

	committed, _ := c["committed"].(bool)
	switch {
	case committed:
		res["answer"] = "an IMM composition set on the control's context was committed into RichEdit on this runner"
	case !imeInstalled && !c["is_ime"].(bool):
		res["answer"] = "no input method is installed on this runner: composition cannot be driven in CI; WM_IME_CHAR inserts text; marked text and IMEs stay on the VM/hand checklist"
	default:
		res["answer"] = "an input method is present but the IMM composition did not reach RichEdit (see imm_composition); IMEs stay on the VM/hand checklist"
	}
	res["ime_installed"] = imeInstalled || c["is_ime"].(bool)
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
