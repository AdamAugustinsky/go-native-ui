//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

// The colour-emoji measurement (D67's open question).
//
// Background (Murray Sargent, "RichEditD2D Window Controls", Math in Office
// blog, 2020-11-01, updated since): RichEdit window controls render through
// GDI/Uniscribe, and GDI draws colour fonts in one colour. Colour fonts need
// RichEdit's D2D/DirectWrite path, which windowless controls have
// (ITextServices2::TxDrawD2D, with EM_SETTYPOGRAPHYOPTIONS
// TO_DEFAULTCOLOREMOJI | TO_DISPLAYFONTCOLOR, 0x1000 | 0x2000), and which
// Office's riched20.dll offers for windows (classes RichEditD2D and
// RichEditD2DPT, or EM_SWITCHTOD2D on a fresh control); "the RichEditD2D
// implementation hasn't been ported to Windows' msftedit.dll". The probe
// measures what the system's msftedit.dll does rather than relying on that.
//
// The text is "A😀B☺C": B is red (a positive control: the read sees
// colour), ☺ (U+263A, text presentation, a one-colour glyph) is the noise
// floor from ClearType's coloured fringes, and the emoji is counted for the
// yellow of Segoe UI Emoji's face.

const (
	emGetTypographyOpts = 0x0400 + 203 // EM_GETTYPOGRAPHYOPTIONS (richedit.h)
	emSwitchToD2D       = 0x0400 + 389 // EM_SWITCHTOD2D (Office RichEdit; the blog above)
	toDefaultColorEmoji = 0x1000       // TO_DEFAULTCOLOREMOJI (the blog above)
	toDisplayFontColor  = 0x2000       // TO_DISPLAYFONTCOLOR
	cfmColor            = 0x40000000   // CFM_COLOR (richedit.h)
	pwRenderFullContent = 0x0002       // PW_RENDERFULLCONTENT (Windows 8.1+)
	srcCopy             = 0x00CC0020   // SRCCOPY (wingdi.h)
	captureBlt          = 0x40000000   // CAPTUREBLT
	emojiText           = "A\U0001F600B☺C"
	emojiAt, redAt      = 1, 3 // UTF-16 offsets: the emoji is 1..3, B 3, ☺ 4
	symbolAt            = 4
)

var (
	pMapWindowPoints = win32.Proc(user32, "MapWindowPoints")
	pBitBlt          = win32.Proc(gdi32, "BitBlt")
	pGetForeground   = win32.Proc(user32, "GetForegroundWindow")
	d2dChild         = false // set by -d2d-child
)

// counts are the pixels of one cell by kind.
type counts struct {
	Coloured int `json:"coloured"` // channels differ by more than 60
	Yellow   int `json:"yellow"`   // r > 200, g > 150, b < 90: the emoji's face
	Red      int `json:"red"`      // r > 150, g < 90, b < 90: the control
	Dark     int `json:"dark"`     // all channels < 100: ink
}

// grabFor reads the edit control's pixels into the DIB with the first read
// that works and returns its name and the edit's origin in the DIB.
func grabFor() (method string, dx, dy int) {
	clearDIB()
	var pt [2]int32 // the edit's client origin in the host's client area
	call(pMapWindowPoints, wins.edit, wins.top, uintptr(unsafe.Pointer(&pt)), 1)
	if win32.Bool(call(pPrintWindow, wins.top, wins.memDC, pwClient|pwRenderFullContent)) && !dibBlank() {
		return "printwindow_host_fullcontent", int(pt[0]), int(pt[1])
	}
	clearDIB()
	if win32.Bool(call(pPrintWindow, wins.edit, wins.memDC, pwClient)) && !dibBlank() {
		return "printwindow_edit", 0, 0
	}
	clearDIB()
	var sp [2]int32
	call(pMapWindowPoints, wins.edit, 0, uintptr(unsafe.Pointer(&sp)), 1)
	screen := call(pGetDC, 0)
	ok := win32.Bool(call(pBitBlt, wins.memDC, 0, 0, editW, editH, screen, uintptr(sp[0]), uintptr(sp[1]), srcCopy|captureBlt))
	call(pReleaseDC, 0, screen)
	call(pGdiFlush)
	if ok && !dibBlank() {
		return "bitblt_screen", 0, 0
	}
	return "", 0, 0
}

// cellCounts counts the pixels of the edit's characters [from, to) on the
// first line, read at origin (dx, dy) in the DIB.
func cellCounts(from, to, lineH, dx, dy int) counts {
	x0, y0 := posFromChar(from)
	x1, _ := posFromChar(to)
	var c counts
	p := unsafe.Slice((*byte)(win32.Ptr(wins.bits)), dibW*dibH*4)
	for y := max(y0+dy, 0); y < min(y0+lineH+dy, dibH); y++ {
		for x := max(x0+dx, 0); x < min(x1+dx, dibW); x++ {
			i := (y*dibW + x) * 4
			b, g, r := int(p[i]), int(p[i+1]), int(p[i+2])
			if max(r, g, b)-min(r, g, b) > 60 {
				c.Coloured++
			}
			if r > 200 && g > 150 && b < 90 {
				c.Yellow++
			}
			if r > 150 && g < 90 && b < 90 {
				c.Red++
			}
			if r < 100 && g < 100 && b < 100 {
				c.Dark++
			}
		}
	}
	return c
}

func clearDIB() {
	if wins.bits != 0 {
		clear(unsafe.Slice((*byte)(win32.Ptr(wins.bits)), dibW*dibH*4))
	}
}

// dibBlank reports a DIB still all zero (the read wrote nothing).
func dibBlank() bool {
	p := unsafe.Slice((*byte)(win32.Ptr(wins.bits)), dibW*dibH*4)
	for _, v := range p {
		if v != 0 {
			return false
		}
	}
	return true
}

// setFace sets the font face of [from, to).
func setFace(from, to int, face string) bool {
	send(emSetSel, uintptr(from), uintptr(to))
	cf = charFormat2{mask: cfmFace}
	cf.size = uint32(unsafe.Sizeof(cf))
	u := utf16z(face)
	copy(cf.face[:len(cf.face)-1], u)
	return send(emSetCharFormat, scfSelection, uintptr(unsafe.Pointer(&cf))) != 0
}

// faceAt reads the font face of [from, to).
func faceAt(from, to int) (string, byte) {
	send(emSetSel, uintptr(from), uintptr(to))
	cf = charFormat2{mask: cfmFace | cfmCharset}
	cf.size = uint32(unsafe.Sizeof(cf))
	send(emGetCharFormat, scfSelection, uintptr(unsafe.Pointer(&cf)))
	return syscall.UTF16ToString(cf.face[:]), cf.charset
}

// emojiVariant sets the text at 24 pt with B red, applies prep, and
// measures the emoji, the control and the baseline.
func emojiVariant(prep func(map[string]any)) map[string]any {
	res := map[string]any{}
	setText(emojiText)
	send(emSetSel, 0, ^uintptr(0))
	cf = charFormat2{mask: cfmSize, height: 480} // twips: 24 pt
	cf.size = uint32(unsafe.Sizeof(cf))
	send(emSetCharFormat, scfAll, uintptr(unsafe.Pointer(&cf)))
	send(emSetSel, redAt, redAt+1)
	cf = charFormat2{mask: cfmColor, color: 0x000000FF} // COLORREF red
	cf.size = uint32(unsafe.Sizeof(cf))
	send(emSetCharFormat, scfSelection, uintptr(unsafe.Pointer(&cf)))
	if prep != nil {
		prep(res)
	}
	res["face"], res["charset"] = faceAt(emojiAt, emojiAt+2)
	send(emSetSel, 0, 0) // no selection highlight in the read
	pump()
	call(pUpdateWindow, wins.top)
	pump()
	_, y0 := posFromChar(0)
	_, yEnd := caretBelow()
	lineH := max(yEnd-y0, 20)
	method, dx, dy := grabFor()
	res["read"] = method
	if method == "" {
		res["read_ok"] = false
		return res
	}
	res["read_ok"] = true
	emoji := cellCounts(emojiAt, emojiAt+2, lineH, dx, dy)
	control := cellCounts(redAt, redAt+1, lineH, dx, dy)
	base := cellCounts(symbolAt, symbolAt+1, lineH, dx, dy)
	res["emoji"], res["control_red_b"], res["baseline_mono_symbol"] = emoji, control, base
	res["reader_sees_colour"] = control.Red > 20
	res["colour_emoji"] = control.Red > 20 && emoji.Yellow > 100 && emoji.Yellow > 5*base.Yellow
	return res
}

// caretBelow is the top of a second line, for the line height: the text is
// one line, so it adds a paragraph mark at the end, reads it and removes it.
func caretBelow() (int, int) {
	n := len(utf16z(text())) - 1
	send(emSetSel, uintptr(n), uintptr(n))
	replaceSel("\r")
	x, y := posFromChar(n + 1)
	send(emSetSel, uintptr(n), uintptr(n+1))
	replaceSel("")
	return x, y
}

func emoji() map[string]any {
	res := map[string]any{"text": emojiText}
	res["edit_style_ex"] = fmt.Sprintf("%#x", send(emGetEditStyleEx, 0, 0))
	res["default_binding"] = emojiVariant(nil)
	res["segoe_ui_emoji"] = emojiVariant(func(r map[string]any) {
		r["face_set"] = setFace(emojiAt, emojiAt+2, "Segoe UI Emoji")
	})
	res["colour_typography"] = emojiVariant(func(r map[string]any) {
		r["face_set"] = setFace(emojiAt, emojiAt+2, "Segoe UI Emoji")
		const want = toAdvancedTypography | toDefaultColorEmoji | toDisplayFontColor
		r["set_result"] = send(emSetTypographyOpts, want, want)
		r["options_after"] = fmt.Sprintf("%#x", send(emGetTypographyOpts, 0, 0))
	})
	send(emSetTypographyOpts, 0, toDefaultColorEmoji|toDisplayFontColor) // the later sections as before
	res["switch_to_d2d"] = runD2DChild()
	coloured := false
	for _, k := range []string{"default_binding", "segoe_ui_emoji", "colour_typography"} {
		if v, _ := res[k].(map[string]any)["colour_emoji"].(bool); v {
			coloured = true
		}
	}
	res["colour_emoji_windowed"] = coloured
	return res
}

// runD2DChild runs this probe again with -d2d-child: a fresh control that
// gets EM_SWITCHTOD2D before any text (an Office RichEdit message; Office's
// DLL was reported to crash on emoji input after it, so it runs apart).
func runD2DChild() map[string]any {
	res := map[string]any{}
	exe, err := os.Executable()
	if err != nil {
		res["error"] = err.Error()
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, "-d2d-child")
	cmd.Stdout = &out
	err = cmd.Run()
	if cmd.ProcessState != nil {
		res["exit"] = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		res["error"] = err.Error()
	}
	var child map[string]any
	if json.Unmarshal(out.Bytes(), &child) == nil {
		res["child"] = child
	}
	return res
}

// d2dChildMain is the -d2d-child process: windows, EM_SWITCHTOD2D first,
// then the emoji measured if the switch was accepted.
func d2dChildMain() {
	call(pOleInitialize, 0)
	win32.LoadSystem("msftedit.dll")
	if err := makeWindows(); err != nil {
		fail(err.Error())
	}
	if err := makeDIB(); err != nil {
		fail(err.Error())
	}
	r := map[string]any{"result": send(emSwitchToD2D, 0, 0)}
	if r["result"] != uintptr(0) {
		r["measured"] = emojiVariant(func(m map[string]any) {
			m["face_set"] = setFace(emojiAt, emojiAt+2, "Segoe UI Emoji")
		})
	}
	out, _ := json.Marshal(r)
	fmt.Println(string(out))
	os.Exit(0)
}

// makeDIB makes the 32-bit top-down DIB the reads go into (the host's
// client size, which holds the edit at its offset).
func makeDIB() error {
	screen := call(pGetDC, 0)
	wins.memDC = call(pCreateCompatibleDC, screen)
	call(pReleaseDC, 0, screen)
	wins.bmi = bitmapInfo{width: dibW, height: -dibH, planes: 1, bits: 32}
	wins.bmi.size = 40
	wins.dib = call(pCreateDIBSection, wins.memDC, uintptr(unsafe.Pointer(&wins.bmi)), 0, uintptr(unsafe.Pointer(&wins.bits)), 0, 0)
	if wins.dib == 0 || wins.bits == 0 {
		return fmt.Errorf("CreateDIBSection failed")
	}
	call(pSelectObject, wins.memDC, wins.dib)
	return nil
}
