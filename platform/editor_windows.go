//go:build windows

package platform

import (
	"errors"
	"fmt"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// The Windows editor (W4, D67 with its windowless refinement): a windowless
// RichEdit (msftedit.dll's CreateTextServices, ITextServices2) whose
// ITextHost2 is implemented in Go (internal/com), in a child window of ours
// that is the host's view (W3). The child window has its own swap chain and
// RichEdit draws into it with Direct2D (editgfx_windows.go), so colour emoji
// show (the windowed control draws them in one colour, P5).
//
//   - The child window receives the keyboard, the input method's messages
//     and the pointer, and hands them to the text services
//     (TxSendMessage), after the editor's own keys: the Enter policy, the
//     completion handoff, Tab and Escape as host commands, and the edge
//     arrows.
//   - Text reaches Go when RichEdit reports a change (EN_CHANGE, through
//     ITextHost::TxNotify) and the message that caused it returns: the
//     editor copies the text (EM_GETTEXTEX; RichEdit's paragraph mark CR,
//     and the vertical tab of a soft break, are '\n'; both are one UTF-16
//     unit, so offsets do not move), rebuilds the UTF-16 map and reads the
//     selection and the composition.
//   - Marked text is the input method's composition string
//     (ImmGetCompositionStringW) at the caret, between
//     WM_IME_STARTCOMPOSITION and WM_IME_ENDCOMPOSITION.
//   - Replace is one undo step: an edit collection of TOM's ITextDocument
//     around EM_REPLACESEL, with typing grouping stopped before and after.
//     SetText clears the undo stack (EM_SETTEXTEX, ST_DEFAULT).
//   - Height comes from EN_REQUESTRESIZE (the content's height for the
//     current width).
//   - Pastes go through IRichEditOleCallback::QueryAcceptData: files
//     (CF_HDROP) and an image without text are the app's (EditorPastedFiles,
//     EditorPastedImage, nothing inserted); text is pasted as plain text.
//   - Mentions (SetSpans) are character formatting set with the undo stack
//     suspended.
//   - The caret and the placeholder are drawn by the editor over RichEdit's
//     drawing; a hidden system caret follows the caret, for input methods
//     and accessibility tools that look for one.

// RichEdit messages and notifications (richedit.h, winuser.h).
const (
	emSetSel            = 0x00B1
	emGetLineCount      = 0x00BA
	emReplaceSel        = 0x00C2
	emCanUndo           = 0x00C6
	emUndo              = 0x00C7
	emEmptyUndoBuffer   = 0x00CD
	emPosFromChar       = 0x00D6
	emExGetSel          = 0x0434
	emExLineFromChar    = 0x0436
	emExSetSel          = 0x0437
	emGetOleInterface   = 0x043C
	emRequestResize     = 0x0441
	emSetCharFormat     = 0x0444
	emSetEventMask      = 0x0445
	emSetOleCallback    = 0x0446
	emRedo              = 0x0454
	emStopGroupTyping   = 0x0458
	emGetTextEx         = 0x045E
	emGetTextLengthEx   = 0x045F
	emSetTextEx         = 0x0461
	emSetLangOptions    = 0x0478
	emGetLangOptions    = 0x0479
	emSetTypographyOpts = 0x04CA

	enChange        = 0x0300
	enSelChange     = 0x0702
	enRequestResize = 0x0701

	enmChange        = 0x00000001
	enmRequestResize = 0x00040000
	enmSelChange     = 0x00080000

	imfSpellChecking = 0x0800

	toAdvancedTypography = 0x0001
	toDefaultColorEmoji  = 0x1000
	toDisplayFontColor   = 0x2000

	scfSelection = 0x0001
	scfAll       = 0x0004

	cfmBold      = 0x00000001
	cfmItalic    = 0x00000002
	cfmUnderline = 0x00000004
	cfmBackColor = 0x04000000
	cfmCharSet   = 0x08000000
	cfmFace      = 0x20000000
	cfmColor     = 0x40000000
	cfmSize      = 0x80000000
	cfeAutoColor = 0x40000000
	cfeAutoBack  = 0x04000000

	// TXTBIT_* (textserv.h).
	txtRichText         = 0x00000001
	txtMultiLine        = 0x00000002
	txtReadOnly         = 0x00000004
	txtWordWrap         = 0x00000400
	txtAllowBeep        = 0x00000800
	txtClientRectChange = 0x00100000
	txtCharFormatChange = 0x00020000
	txtD2DDWrite        = 0x01000000

	esAutoVScroll = 0x0040

	wmMouseFirst      = 0x0200
	wmMouseLast       = 0x020E
	wmImeChar         = 0x0286
	wmImeNotify       = 0x0282
	wmImeRequest      = 0x0288
	wmInputLangChange = 0x0051
	wmGetDlgCodeEd    = 0x0087

	niCompStr   = 0x0015
	cpsComplete = 0x0001

	blinkTimerID = 0x7E57
	vkUp         = 0x26
	vkDown       = 0x28
	dlgcAll      = 0x0001 | 0x0002 | 0x0004 | 0x0080 // DLGC_WANTARROWS | WANTTAB | WANTALLKEYS | WANTCHARS
)

// charFormat2 is CHARFORMAT2W (richedit.h, 116 bytes).
type charFormat2 struct {
	size     uint32
	mask     uint32
	effects  uint32
	height   int32
	offset   int32
	color    uint32
	charset  uint8
	pitch    uint8
	face     [32]uint16
	weight   uint16
	spacing  int16
	back     uint32
	lcid     uint32
	reserved uint32
	style    int16
	kerning  uint16
	ulType   uint8
	anim     uint8
	rev      uint8
	ulColor  uint8
}

// paraFormat is PARAFORMAT (richedit.h, 156 bytes).
type paraFormat struct {
	size      uint32
	mask      uint32
	numbering uint16
	effects   uint16
	start     int32
	right     int32
	offset    int32
	align     uint16
	tabCount  int16
	tabs      [32]int32
}

// chrRange is CHARRANGE; posL POINTL.
type chrRange struct{ min, max int32 }

// editorNative is the Windows half of an Editor.
type editorNative struct {
	w        *Window
	slot     int // index in edTable
	host     HostID
	hwnd     uintptr // the host's view: our editor window
	unk, ts  uintptr // CreateTextServices's IUnknown, ITextServices2
	reOle    uintptr // IRichEditOle
	doc      uintptr // ITextDocument
	th, cb   *com.Object
	surf     edSurface
	cf       charFormat2
	pf       paraFormat
	enter    EnterPolicy
	maxLines int
	minLines int
	scale    float32
	lineH    int32 // one line's height, pixels
	contentH int32 // the text's height, pixels (EN_REQUESTRESIZE)
	cw, ch   int32 // the window's client size
	dirtyH   bool
	released bool
	quiet    int  // inside SetText, SetSpans and the like: no signals
	changed  bool // EN_CHANGE since the last after
	selMoved bool // EN_SELCHANGE since the last after
	ime      bool // between WM_IME_STARTCOMPOSITION and WM_IME_ENDCOMPOSITION
	// eat are the characters of keys the editor took (Enter, Tab,
	// Escape), whose WM_CHAR TranslateMessage queued: dropped in order.
	eat     [8]uint16
	nEat    int
	imeMsgs int           // WM_IME_* messages seen (tests)
	imeLog  [8][4]uintptr // the first WM_IME_* messages: message, wParam, lParam, TxSendMessage's HRESULT (tests)
	// The composition the editor keeps in the text (windowless RichEdit
	// inserts none): its start and length in UTF-16 units, the IME's strings
	// read into imeBuf, and the candidate window's form.
	compStart, compLen int
	compEdited         bool // putComposition changed the text since compositionMoved
	imeBuf             [256]uint16
	cand               candidateForm
	focused            bool
	// The caret RichEdit asks for (TxCreateCaret, TxSetCaretPos,
	// TxShowCaret), drawn by render; blinkOn is its blink phase.
	caretX, caretY, caretW, caretH int32
	caretShown, blinkOn            bool
	sysCaret                       bool
	textCol, backCol, phCol        colorF
	textRef                        uint32 // COLORREF of the text, for TxGetSysColor
	backRef                        uint32
	ph16                           []uint16
	u16                            []uint16
	cr                             chrRange
	pt                             point
	lres                           uintptr
	rc                             rect
	gtl                            [2]uint32 // GETTEXTLENGTHEX
	gte                            getTextEx
	ste                            [2]uint32 // SETTEXTEX
	spanCF                         charFormat2
	render                         bool // a render is pending (WM_PAINT asked for)
}

// getTextEx is GETTEXTEX.
type getTextEx struct {
	cb       uint32
	flags    uint32
	codepage uint32
	defChar  uintptr
	usedDef  uintptr
}

const maxEditors = 2 * MaxHosts

var (
	edTable [maxEditors]*Editor

	richOnce struct {
		done bool
		err  error
	}
	rich struct {
		create              uintptr
		iidTS2, iidTH       win32.GUID
		iidTH2, iidTS       win32.GUID
		hostKind, cbKind    *com.Kind
		class               wndClassEx
		classProc           uintptr
		fe                  formatEtc
		pImmGetCompStr      uintptr
		pCreateCaret        uintptr
		pDestroyCaret       uintptr
		pSetCaretPos        uintptr
		pGetCaretBlink      uintptr
		pGetDC, pReleaseDC  uintptr
		pGetDeviceCaps      uintptr
		pGetSysColor        uintptr
		pInvalidateRect     uintptr
		pClientToScreen     uintptr
		pScreenToClientEd   uintptr
		pMapWindowPoints    uintptr
		pSetForeground      uintptr
		pGetAncestor        uintptr
		pMessageBeep        uintptr
		pSysAllocString     uintptr
		pImmSetCompositionW uintptr
	}

	edClassName = mustUTF16("GoNativeUIEditor")

	// textserv.h: IID_ITextServices 8d33f740-cf58-11ce-a89d-00aa006cadc5,
	// IID_ITextServices2 8d33f741-…, IID_ITextHost 13e670f4-1a5a-11cf-
	// abeb-00aa00b65ea1, IID_ITextHost2 13e670f5-…. msftedit.dll exports
	// them; its exports are read first.
	iidTextServices  = win32.GUID{Data1: 0x8d33f740, Data2: 0xcf58, Data3: 0x11ce, Data4: [8]byte{0xa8, 0x9d, 0x00, 0xaa, 0x00, 0x6c, 0xad, 0xc5}}
	iidTextServices2 = win32.GUID{Data1: 0x8d33f741, Data2: 0xcf58, Data3: 0x11ce, Data4: [8]byte{0xa8, 0x9d, 0x00, 0xaa, 0x00, 0x6c, 0xad, 0xc5}}
	iidTextHost      = win32.GUID{Data1: 0x13e670f4, Data2: 0x1a5a, Data3: 0x11cf, Data4: [8]byte{0xab, 0xeb, 0x00, 0xaa, 0x00, 0xb6, 0x5e, 0xa1}}
	iidTextHost2     = win32.GUID{Data1: 0x13e670f5, Data2: 0x1a5a, Data3: 0x11cf, Data4: [8]byte{0xab, 0xeb, 0x00, 0xaa, 0x00, 0xb6, 0x5e, 0xa1}}
	// tom.h: IID_ITextDocument 8cc497c0-a1df-11ce-8098-00aa0047be5d.
	iidTextDocument = win32.GUID{Data1: 0x8CC497C0, Data2: 0xA1DF, Data3: 0x11CE, Data4: [8]byte{0x80, 0x98, 0x00, 0xAA, 0x00, 0x47, 0xBE, 0x5D}}
	// richole.h: IID_IRichEditOleCallback 00020d03-0000-0000-c000-000000000046.
	iidRichEditOleCallback = win32.GUID{Data1: 0x00020D03, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}

	errNoRichEdit = errors.New("platform: editor: msftedit.dll has no CreateTextServices")
)

// ITextServices (textserv.h): TxSendMessage 3, TxDraw 4, TxGetHScroll 5,
// TxGetVScroll 6, OnTxSetCursor 7, TxQueryHitPoint 8, OnTxInPlaceActivate 9,
// OnTxInPlaceDeactivate 10, OnTxUIActivate 11, OnTxUIDeactivate 12,
// TxGetText 13, TxSetText 14, TxGetCurTargetX 15, TxGetBaseLinePos 16,
// TxGetNaturalSize 17, TxGetDropTarget 18, OnTxPropertyBitsChange 19,
// TxGetCachedSize 20; ITextServices2: TxGetNaturalSize2 21, TxDrawD2D 22.
// ITextDocument (tom.h): IDispatch 3-6, GetName 7 … BeginEditCollection 20,
// EndEditCollection 21, Undo 22.
const (
	vtTSSendMessage         = 3
	vtTSOnSetCursor         = 7
	vtTSInPlaceActivate     = 9
	vtTSInPlaceDeactivate   = 10
	vtTSUIActivate          = 11
	vtTSUIDeactivate        = 12
	vtTSPropertyBitsChange  = 19
	vtTSDrawD2D             = 22
	vtDocBeginEditColl      = 20
	vtDocEndEditColl        = 21
	vtDocUndo               = 22
	vtDataObjectQueryGetDat = 5
	tomSuspend              = -9999995
	tomResume               = -9999994
)

// loadRichEdit loads msftedit.dll and makes the COM kinds, once.
func loadRichEdit(hinst uintptr) error {
	if richOnce.done {
		return richOnce.err
	}
	richOnce.done = true
	richOnce.err = func() error {
		dll := win32.LoadSystem("msftedit.dll")
		rich.create = win32.Proc(dll, "CreateTextServices")
		if rich.create == 0 {
			return errNoRichEdit
		}
		exported := func(name string, def win32.GUID) win32.GUID {
			if p := win32.Proc(dll, name); p != 0 {
				return *(*win32.GUID)(win32.Ptr(p))
			}
			return def
		}
		rich.iidTS = exported("IID_ITextServices", iidTextServices)
		rich.iidTS2 = exported("IID_ITextServices2", iidTextServices2)
		rich.iidTH = exported("IID_ITextHost", iidTextHost)
		rich.iidTH2 = exported("IID_ITextHost2", iidTextHost2)
		gdi := win32.LoadSystem("gdi32.dll")
		oleaut := win32.LoadSystem("oleaut32.dll")
		rich.pImmGetCompStr = pImmGetCompStringW
		rich.pCreateCaret = win32.Proc(user32, "CreateCaret")
		rich.pDestroyCaret = win32.Proc(user32, "DestroyCaret")
		rich.pSetCaretPos = win32.Proc(user32, "SetCaretPos")
		rich.pGetCaretBlink = win32.Proc(user32, "GetCaretBlinkTime")
		rich.pGetDC = win32.Proc(user32, "GetDC")
		rich.pReleaseDC = win32.Proc(user32, "ReleaseDC")
		rich.pGetDeviceCaps = win32.Proc(gdi, "GetDeviceCaps")
		rich.pGetSysColor = win32.Proc(user32, "GetSysColor")
		rich.pInvalidateRect = win32.Proc(user32, "InvalidateRect")
		rich.pClientToScreen = win32.Proc(user32, "ClientToScreen")
		rich.pScreenToClientEd = pScreenToClient
		rich.pMapWindowPoints = win32.Proc(user32, "MapWindowPoints")
		rich.pSetForeground = win32.Proc(user32, "SetForegroundWindow")
		rich.pGetAncestor = win32.Proc(user32, "GetAncestor")
		rich.pMessageBeep = win32.Proc(user32, "MessageBeep")
		rich.pSysAllocString = win32.Proc(oleaut, "SysAllocString")
		rich.hostKind = com.NewKind("ITextHost2", maxEditors, []win32.GUID{rich.iidTH2, rich.iidTH}, textHostMethods())
		rich.cbKind = com.NewKind("IRichEditOleCallback", maxEditors, []win32.GUID{iidRichEditOleCallback}, oleCallbackMethods())
		rich.classProc = syscall.NewCallback(editorWndProc)
		rich.class = wndClassEx{wndProc: rich.classProc, instance: hinst, className: edClassName, cursor: win32.Call(pLoadCursorW, 0, idcIBeam)}
		rich.class.size = uint32(unsafe.Sizeof(rich.class))
		rich.class.style = 0x0008 // CS_DBLCLKS
		if win32.Call(pRegisterClassExW, uintptr(unsafe.Pointer(&rich.class)))&0xFFFF == 0 {
			return errors.New("platform: editor: RegisterClassExW failed")
		}
		return nil
	}()
	return richOnce.err
}

// NewEditor creates a native multi-line editor as a host of the window
// (focusable), hidden until SetHosts places it: a windowless RichEdit in a
// child window drawn with Direct2D. Build its box with ui.Context.Host(
// e.Host(), …, ui.HostFocusable), Height points high. At a safe point, main
// thread; it allocates. It takes one of the window's MaxHosts hosts.
// RichEdit, Direct3D and Direct2D load with the first editor.
func (w *Window) NewEditor(o EditorOptions) (*Editor, error) {
	if w.hwnd == 0 {
		return nil, ErrNoHosts
	}
	if err := loadRichEdit(w.hinst); err != nil {
		return nil, err
	}
	if err := editorGraphics(); err != nil {
		return nil, err
	}
	slot := -1
	for i := range edTable {
		if edTable[i] == nil {
			slot = i
			break
		}
	}
	if slot < 0 {
		return nil, ErrTooManyHosts
	}
	e := &Editor{}
	e.w, e.slot = w, slot
	e.applyOptions(o)
	e.scale = w.scale
	if e.scale <= 0 {
		e.scale = 1
	}
	e.hwnd = win32.Call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(edClassName)), 0, wsChild|wsClipSiblings, 0, 0, 8, 8, w.hwnd, 0, w.hinst, 0)
	if e.hwnd == 0 {
		return nil, errors.New("platform: editor: CreateWindowExW failed")
	}
	edTable[slot] = e
	fail := func(err error) (*Editor, error) {
		e.teardown()
		return nil, err
	}
	e.charFormat()
	e.pf = paraFormat{mask: 0x8000003F /* PFM_ALL */, align: 1 /* PFA_LEFT */, tabCount: 1}
	e.pf.size = uint32(unsafe.Sizeof(e.pf))
	e.pf.tabs[0] = 720
	var err error
	if e.th, _, err = rich.hostKind.New(uintptr(slot + 1)); err != nil {
		return fail(err)
	}
	if hr := win32.HR(win32.Call(rich.create, 0, e.th.Ptr(), uintptr(unsafe.Pointer(&e.unk)))); hr < 0 {
		return fail(fmt.Errorf("platform: editor: CreateTextServices: %#x", uint32(hr)))
	}
	if hr := win32.QueryInterface(e.unk, &rich.iidTS2, &e.ts); hr < 0 {
		return fail(fmt.Errorf("platform: editor: ITextServices2: %#x", uint32(hr)))
	}
	e.send(emSetEventMask, 0, enmChange|enmSelChange|enmRequestResize)
	e.send(emSetTypographyOpts, toAdvancedTypography|toDefaultColorEmoji|toDisplayFontColor, toAdvancedTypography|toDefaultColorEmoji|toDisplayFontColor)
	e.send(emGetLangOptions, 0, 0)
	e.send(emSetLangOptions, 0, e.lres|imfSpellChecking)
	if e.cb, _, err = rich.cbKind.New(uintptr(slot + 1)); err != nil {
		return fail(err)
	}
	e.send(emSetOleCallback, 0, e.cb.Ptr())
	e.send(emGetOleInterface, 0, uintptr(unsafe.Pointer(&e.reOle)))
	if e.reOle != 0 {
		win32.QueryInterface(e.reOle, &iidTextDocument, &e.doc)
	}
	id, err := w.NewHost(HostView, HostOptions{View: e.hwnd, Focusable: true})
	if err != nil {
		return fail(err)
	}
	e.host = id
	// The swap chain is made on the window in its place (a child of the
	// host's container).
	if err := e.surf.open(e.hwnd, 8, 8); err != nil {
		return fail(err)
	}
	e.updateClient()
	win32.Com(e.ts, vtTSInPlaceActivate, uintptr(unsafe.Pointer(&e.rc)))
	e.Reserve(256)
	e.measureLine()
	e.copyText()
	e.dirtyH = true
	e.sync()
	return e, nil
}

// applyOptions takes options o (NewEditor, SetOptions).
func (e *Editor) applyOptions(o EditorOptions) {
	e.opts = o
	e.maxLen, e.enter = o.MaxLen, o.Enter
	e.maxLines, e.minLines = editorLines(o)
	e.textRef, e.backRef = 0, 0
	tc, bc := o.TextColor, o.Background
	if tc == 0 {
		tc = sysColorPacked(8) // COLOR_WINDOWTEXT
	}
	if bc == 0 {
		bc = sysColorPacked(5) // COLOR_WINDOW
	}
	e.textCol, e.backCol = colorOf(tc|0xFF), colorOf(bc|0xFF)
	e.textRef, e.backRef = colorRef(tc), colorRef(bc)
	e.phCol = colorF{(e.textCol.r + e.backCol.r) / 2, (e.textCol.g + e.backCol.g) / 2, (e.textCol.b + e.backCol.b) / 2, 1}
	e.ph16 = append(e.ph16[:0], utf16.Encode([]rune(o.Placeholder))...)
}

// sysColorPacked is GetSysColor(i) as 0xRRGGBBAA.
func sysColorPacked(i int) uint32 {
	p := win32.Proc(user32, "GetSysColor")
	c := uint32(win32.Call(p, uintptr(i)))
	return (c&0xFF)<<24 | (c>>8&0xFF)<<16 | (c>>16&0xFF)<<8 | 0xFF
}

// colorRef is 0xRRGGBBAA as a COLORREF (0x00BBGGRR).
func colorRef(c uint32) uint32 { return c>>24 | (c>>16&0xFF)<<8 | (c>>8&0xFF)<<16 }

// charFormat sets the default character format: Segoe UI at the font size,
// scaled to the window's pixels, in the text colour.
func (e *Editor) charFormat() {
	pt := e.opts.FontSize
	if pt <= 0 {
		pt = 13 // as macOS's default; Windows' own message font is 9 pt
	}
	dpi := int32(96)
	if dc := win32.Call(rich.pGetDC, e.hwnd); dc != 0 {
		if d := int32(win32.Call(rich.pGetDeviceCaps, dc, 90 /* LOGPIXELSY */)); d > 0 {
			dpi = d
		}
		win32.Call(rich.pReleaseDC, e.hwnd, dc)
	}
	// Points are 1/72 inch at 96 DPI scaled by the window's scale (as the
	// app's own text is), in twips of the DC's resolution.
	px := pt * 96 / 72 * e.scale
	e.cf = charFormat2{mask: cfmSize | cfmFace | cfmColor | cfmBackColor | cfmCharSet | cfmBold | cfmItalic | cfmUnderline,
		effects: cfeAutoBack, height: int32(px*1440/float32(dpi) + 0.5), color: e.textRef, charset: 1 /* DEFAULT_CHARSET */}
	e.cf.size = uint32(unsafe.Sizeof(e.cf))
	copy(e.cf.face[:], []uint16{'S', 'e', 'g', 'o', 'e', ' ', 'U', 'I', 0})
}

// send is ITextServices::TxSendMessage; the result is in e.lres. Changes it
// caused are taken in when it returns (after).
func (e *Editor) send(m uint32, wp, lp uintptr) int32 {
	if e.ts == 0 {
		return win32.EFail
	}
	hr := win32.HR(win32.Com(e.ts, vtTSSendMessage, uintptr(m), wp, lp, uintptr(unsafe.Pointer(&e.lres))))
	e.after()
	return hr
}

// after takes in what RichEdit reported while a message ran.
func (e *Editor) after() {
	if e.changed {
		e.changed, e.selMoved = false, false
		if e.quiet > 0 {
			e.copyText()
			return
		}
		e.textChanged()
		return
	}
	if e.selMoved {
		e.selMoved = false
		if e.quiet > 0 || e.ts == 0 {
			return
		}
		prev := e.sel
		e.readSelection()
		if e.sel != prev {
			e.pend |= EditorSelected
			e.wake()
		}
	}
}

// textChanged copies the changed text: EditorChanged, with EditorComposing
// while marked text remains (which ends the app's completion). An edit that
// made the text longer than MaxLen is taken back.
func (e *Editor) textChanged() {
	before, beforeSel := len(e.text), e.sel
	e.copyText()
	if e.maxLen > 0 && len(e.text) > e.maxLen && len(e.text) > before && !e.marked {
		e.quiet++
		if e.doc != 0 {
			win32.Com(e.doc, vtDocUndo, u32(tomSuspend), 0)
		}
		// The text before the edit is the text now without what was
		// inserted at the old selection: put the old text back.
		e.restoreLonger(before, beforeSel)
		if e.doc != 0 {
			win32.Com(e.doc, vtDocUndo, u32(tomResume), 0)
		}
		e.quiet--
		e.copyText()
		win32.Call(rich.pMessageBeep, 0xFFFFFFFF)
		return
	}
	e.dirtyH = true
	e.pend |= EditorChanged
	if e.marked {
		e.pend |= EditorComposing
		if e.compl {
			e.SetCompletionActive(false)
			e.pend |= EditorCancelled
		}
	} else {
		e.pend &^= EditorComposing
	}
	e.invalidate()
	e.wake()
}

// restoreLonger removes the text an over-long edit inserted: the edit put
// len(e.text)-before bytes (or more, replacing a selection) at the old
// selection's start. It deletes what lies between the old selection's start
// and the caret now, and puts the old selection's text back where that was
// selected (it is no longer known, so a replaced selection is lost; such
// an edit is rare: a paste over a selection near the limit).
func (e *Editor) restoreLonger(before int, old TextSelection) {
	r := old.Range()
	caret := e.sel.Range().End
	if caret < r.Start {
		caret = r.Start
	}
	a, _ := e.m.toU16(e.text, r.Start)
	b, _ := e.m.toU16(e.text, caret)
	if b <= a {
		// Nothing to locate: cut the tail back to the limit.
		a, _ = e.m.toU16(e.text, lastBoundary(e.text, e.maxLen))
		b = e.m.n16
	}
	e.cr = chrRange{int32(a), int32(b)}
	win32.Com(e.ts, vtTSSendMessage, emExSetSel, 0, uintptr(unsafe.Pointer(&e.cr)), uintptr(unsafe.Pointer(&e.lres)))
	e.u16 = append(e.u16[:0], 0)
	win32.Com(e.ts, vtTSSendMessage, emReplaceSel, 0, uintptr(unsafe.Pointer(&e.u16[0])), uintptr(unsafe.Pointer(&e.lres)))
	e.changed, e.selMoved = false, false
}

// lastBoundary is the last scalar boundary of b at or before n.
func lastBoundary(b []byte, n int) int {
	n = min(n, len(b))
	for n > 0 && !boundary(b, n) {
		n--
	}
	return n
}

// copyText copies RichEdit's text into the buffer, rebuilds the map, and
// reads the selection and the marked text.
func (e *Editor) copyText() {
	if e.ts == 0 {
		return
	}
	e.rev++
	e.gtl = [2]uint32{2 | 8, 1200} // GTL_PRECISE | GTL_NUMCHARS, UTF-16
	win32.Com(e.ts, vtTSSendMessage, emGetTextLengthEx, uintptr(unsafe.Pointer(&e.gtl)), 0, uintptr(unsafe.Pointer(&e.lres)))
	n := int(e.lres)
	if cap(e.u16) < n+1 {
		e.u16 = make([]uint16, n+1, n+64)
	}
	e.u16 = e.u16[:n+1]
	e.gte = getTextEx{cb: uint32(2 * (n + 1)), codepage: 1200}
	win32.Com(e.ts, vtTSSendMessage, emGetTextEx, uintptr(unsafe.Pointer(&e.gte)), uintptr(unsafe.Pointer(&e.u16[0])), uintptr(unsafe.Pointer(&e.lres)))
	got := min(int(e.lres), n)
	e.text = appendText(e.text[:0], e.u16[:got])
	e.m.build(e.text)
	e.readSelection()
	e.readMarked()
}

// appendText appends UTF-16 text u as UTF-8, with RichEdit's paragraph mark
// (CR) and soft break (VT) as '\n'.
func appendText(b []byte, u []uint16) []byte {
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		switch {
		case c == '\r' || c == '\v':
			c = '\n'
		case utf16.IsSurrogate(c) && i+1 < len(u):
			if r := utf16.DecodeRune(c, rune(u[i+1])); r != utf8.RuneError {
				c = r
				i++
			} else {
				c = utf8.RuneError
			}
		case utf16.IsSurrogate(c):
			c = utf8.RuneError
		}
		b = utf8.AppendRune(b, c)
	}
	return b
}

// toRichText is UTF-8 text b as NUL-terminated UTF-16 with '\n' as CR, in
// the editor's scratch buffer.
func (e *Editor) toRichText(b []byte) []uint16 {
	u := e.u16[:0]
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		b = b[n:]
		if r == '\n' {
			r = '\r'
		}
		u = utf16.AppendRune(u, r)
	}
	u = append(u, 0)
	e.u16 = u
	return u
}

// readSelection reads the selection; the anchor stays where it was when it
// is one of the new ends (a selection extended from it).
func (e *Editor) readSelection() {
	win32.Com(e.ts, vtTSSendMessage, emExGetSel, 0, uintptr(unsafe.Pointer(&e.cr)), uintptr(unsafe.Pointer(&e.lres)))
	lo, hi := e.m.toByte(e.text, int(e.cr.min)), e.m.toByte(e.text, int(e.cr.max))
	was := e.sel
	if lo != hi && e.sel.Anchor == hi {
		e.sel = TextSelection{hi, lo}
	} else {
		e.sel = TextSelection{lo, hi}
	}
	if e.sel != was {
		e.rev++
	}
}

// readMarked reads the marked text: the composition the editor keeps in
// the text.
func (e *Editor) readMarked() {
	e.marked, e.mr = false, TextRange{}
	if !e.ime || e.compLen <= 0 || e.compStart+e.compLen > e.m.n16 {
		return
	}
	e.marked = true
	e.mr = TextRange{e.m.toByte(e.text, e.compStart), e.m.toByte(e.text, e.compStart+e.compLen)}
}

// measureLine measures one line's height with an empty text.
func (e *Editor) measureLine() {
	e.quiet++
	e.contentH = 0
	e.send(emRequestResize, 0, 0)
	e.lineH = e.contentH
	if e.lineH <= 0 {
		e.lineH = int32(e.opts.FontSize*96/72*e.scale*1.33 + 0.5)
		if e.lineH <= 0 {
			e.lineH = int32(23*e.scale + 0.5)
		}
	}
	e.quiet--
}

// Host is the editor's host, for ui.Context.Host.
func (e *Editor) Host() HostID { return e.host }

// View is the editor's window (an HWND), the host's view.
func (e *Editor) View() uintptr { return e.hwnd }

// Release removes the editor and its host, before or after the window
// closes. At a safe point, main thread.
func (e *Editor) Release() {
	if e.released {
		return
	}
	e.teardown()
}

// teardown releases what the editor made, as far as it got.
func (e *Editor) teardown() {
	e.released = true
	if e.ts != 0 {
		win32.Com(e.ts, vtTSInPlaceDeactivate)
	}
	if e.ts != 0 {
		win32.Com(e.ts, vtTSSendMessage, emSetOleCallback, 0, 0, uintptr(unsafe.Pointer(&e.lres)))
	}
	for _, p := range [...]*uintptr{&e.doc, &e.reOle, &e.ts, &e.unk} {
		win32.Release(*p)
		*p = 0
	}
	if e.th != nil {
		e.th.Release()
		e.th = nil
	}
	if e.cb != nil {
		e.cb.Release()
		e.cb = nil
	}
	e.surf.close()
	if e.host != 0 {
		e.w.ReleaseHost(e.host) // destroys the window
		e.host = 0
	} else if e.hwnd != 0 {
		win32.Call(pDestroyWindow, e.hwnd)
	}
	e.hwnd = 0
	if edTable[e.slot] == e {
		edTable[e.slot] = nil
	}
}

// Focused reports whether the editor holds the keyboard focus.
func (e *Editor) Focused() bool {
	return !e.released && e.w.hosts.focus == int(e.host&0xFF) && e.w.hosts.slot(e.host) != nil
}

// Focus gives the editor the keyboard focus (EvHostFocus follows).
func (e *Editor) Focus() { e.w.FocusHost(e.host) }

// SetAutocorrect is a no-op on Windows: RichEdit corrects nothing by itself
// (its spell checking underlines; the user corrects).
func (e *Editor) SetAutocorrect(bool) {}

// SetText replaces the whole text and puts the caret at its end. It clears
// the undo stack, discards any composition and fires no EditorChanged.
func (e *Editor) SetText(b []byte) error {
	if e.released {
		return ErrNoHosts
	}
	if e.maxLen > 0 && len(b) > e.maxLen {
		return ErrTooLong
	}
	if !utf8.Valid(b) {
		return ErrNotBoundary
	}
	e.quiet++
	e.cancelComposition()
	u := e.toRichText(b)
	e.ste = [2]uint32{0 /* ST_DEFAULT: the undo stack goes */, 1200}
	e.send(emSetTextEx, uintptr(unsafe.Pointer(&e.ste)), uintptr(unsafe.Pointer(&u[0])))
	e.quiet--
	e.copyText()
	e.pend &^= EditorChanged | EditorComposing
	e.dirtyH = true
	e.invalidate()
	e.wake()
	return e.SetSelection(TextSelection{len(e.text), len(e.text)})
}

// cancelComposition makes the input method forget its composition.
func (e *Editor) cancelComposition() {
	if !e.ime {
		return
	}
	if himc := win32.Call(pImmGetContext, e.hwnd); himc != 0 {
		win32.Call(pImmNotifyIME, himc, niCompStr, cpsCancel, 0)
		win32.Call(pImmReleaseContext, e.hwnd, himc)
	}
	if e.compLen > 0 {
		e.putComposition(nil, false)
	}
	e.ime, e.marked, e.compLen = false, false, 0
}

// CommitComposition ends an input method's composition, keeping its text.
// Nothing happens without marked text. It fires no EditorChanged and ends
// the EditorComposing it had.
func (e *Editor) CommitComposition() {
	if !e.marked || e.released {
		return
	}
	e.quiet++
	if himc := win32.Call(pImmGetContext, e.hwnd); himc != 0 {
		win32.Call(pImmNotifyIME, himc, niCompStr, cpsComplete, 0)
		win32.Call(pImmReleaseContext, e.hwnd, himc)
	}
	e.quiet--
	e.ime = false
	e.copyText()
	e.pend &^= EditorComposing
	e.dirtyH = true
	e.invalidate()
	e.wake()
}

// SetSelection selects s (bytes; Anchor == Focus for a caret).
func (e *Editor) SetSelection(s TextSelection) error {
	if e.released {
		return ErrNoHosts
	}
	a, err := e.m.toU16(e.text, s.Anchor)
	if err != nil {
		return err
	}
	f, err := e.m.toU16(e.text, s.Focus)
	if err != nil {
		return err
	}
	e.quiet++
	e.send(emSetSel, uintptr(a), uintptr(f))
	e.quiet--
	e.readSelection()
	r := e.sel.Range()
	if s.Focus < s.Anchor {
		e.sel = TextSelection{r.End, r.Start}
	} else {
		e.sel = TextSelection{r.Start, r.End}
	}
	e.invalidate()
	return nil
}

// Replace replaces bytes r with text and selects sel (bytes of the new text),
// as a user edit: one undo step, EditorChanged fired. It refuses while marked
// text is there (ErrComposing), off scalar boundaries (ErrNotBoundary) and
// over MaxLen (ErrTooLong).
func (e *Editor) Replace(r TextRange, text []byte, sel TextRange) error {
	if e.released {
		return ErrNoHosts
	}
	if err := e.checkReplace(r, text, sel); err != nil {
		return err
	}
	loc, n, _ := e.rangeU16(r)
	e.send(emStopGroupTyping, 0, 0)
	if e.doc != 0 {
		win32.Com(e.doc, vtDocBeginEditColl)
	}
	e.cr = chrRange{int32(loc), int32(loc + n)}
	e.quiet++
	e.send(emExSetSel, 0, uintptr(unsafe.Pointer(&e.cr)))
	e.quiet--
	u := e.toRichText(text)
	e.send(emReplaceSel, 1, uintptr(unsafe.Pointer(&u[0])))
	if e.doc != 0 {
		win32.Com(e.doc, vtDocEndEditColl)
	}
	e.send(emStopGroupTyping, 0, 0)
	return e.SetSelection(TextSelection{sel.Start, sel.End})
}

// SetSpans styles the text's ranges (mentions), replacing the spans set
// before: character formatting set with the undo stack suspended. Call it
// again after EditorChanged, with the app's spans for the new text.
func (e *Editor) SetSpans(spans []TextSpan) error {
	if e.released {
		return ErrNoHosts
	}
	for _, s := range spans {
		if _, _, err := e.rangeU16(s.Range); err != nil {
			return err
		}
	}
	e.quiet++
	defer func() { e.quiet-- }()
	if e.doc != 0 {
		win32.Com(e.doc, vtDocUndo, u32(tomSuspend), 0)
		defer win32.Com(e.doc, vtDocUndo, u32(tomResume), 0)
	}
	keep := e.sel
	e.spanCF = charFormat2{mask: cfmColor | cfmBackColor, effects: cfeAutoBack, color: e.textRef}
	e.spanCF.size = uint32(unsafe.Sizeof(e.spanCF))
	e.send(emSetCharFormat, scfAll, uintptr(unsafe.Pointer(&e.spanCF)))
	for _, s := range spans {
		loc, n, _ := e.rangeU16(s.Range)
		e.cr = chrRange{int32(loc), int32(loc + n)}
		e.send(emExSetSel, 0, uintptr(unsafe.Pointer(&e.cr)))
		e.spanCF = charFormat2{mask: cfmColor | cfmBackColor, effects: cfeAutoBack, color: e.textRef}
		e.spanCF.size = uint32(unsafe.Sizeof(e.spanCF))
		if s.Color != 0 {
			e.spanCF.color = colorRef(s.Color)
		}
		if s.Background != 0 {
			e.spanCF.effects, e.spanCF.back = 0, colorRef(s.Background)
		}
		e.send(emSetCharFormat, scfSelection, uintptr(unsafe.Pointer(&e.spanCF)))
	}
	a, _ := e.m.toU16(e.text, keep.Anchor)
	f, _ := e.m.toU16(e.text, keep.Focus)
	e.send(emSetSel, uintptr(a), uintptr(f))
	e.invalidate()
	return nil
}

// SetOptions changes the options NewEditor was given, while the editor runs.
// The text, the selection, the undo stack and the spans stay.
func (e *Editor) SetOptions(o EditorOptions) {
	if e.released {
		return
	}
	was := e.opts
	e.applyOptions(o)
	if o.ReadOnly != was.ReadOnly {
		win32.Com(e.ts, vtTSPropertyBitsChange, txtReadOnly, uintptr(e.propertyBits()&txtReadOnly))
	}
	if o.FontSize != was.FontSize || o.TextColor != was.TextColor {
		e.charFormat()
		win32.Com(e.ts, vtTSPropertyBitsChange, txtCharFormatChange, txtCharFormatChange)
		e.measureLine()
	}
	if o.InsetX != was.InsetX || o.InsetY != was.InsetY {
		e.updateClient()
	}
	if o.MaxLines != was.MaxLines || o.MinLines != was.MinLines || o.FontSize != was.FontSize || o.InsetX != was.InsetX || o.InsetY != was.InsetY {
		e.dirtyH = true
		e.wake()
	}
	e.invalidate()
}

// SetColors sets the text's colour and the fill behind it (0xRRGGBBAA; 0:
// the system's), as EditorOptions.TextColor and Background: the colours of
// the box the editor sits in, which a widget passes every frame (nothing
// happens when they are unchanged).
func (e *Editor) SetColors(text, background uint32) {
	if text == e.opts.TextColor && background == e.opts.Background {
		return
	}
	o := e.opts
	o.TextColor, o.Background = text, background
	e.SetOptions(o)
}

// SetPlaceholder changes the placeholder ("" none).
func (e *Editor) SetPlaceholder(s string) {
	if s != e.opts.Placeholder {
		o := e.opts
		o.Placeholder = s
		e.SetOptions(o)
	}
}

// CaretRect is the caret, or the start of the selection, in pixels of the
// window's content; false while the editor is not on screen.
func (e *Editor) CaretRect() ([4]float32, bool) {
	s := e.w.hosts.slot(e.host)
	if e.released || s == nil || !s.shown {
		return [4]float32{}, false
	}
	win32.Com(e.ts, vtTSSendMessage, emExGetSel, 0, uintptr(unsafe.Pointer(&e.cr)), uintptr(unsafe.Pointer(&e.lres)))
	e.pt = point{}
	win32.Com(e.ts, vtTSSendMessage, emPosFromChar, uintptr(unsafe.Pointer(&e.pt)), uintptr(e.cr.min), uintptr(unsafe.Pointer(&e.lres)))
	win32.Call(rich.pMapWindowPoints, e.hwnd, e.w.hwnd, uintptr(unsafe.Pointer(&e.pt)), 1)
	x, y := float32(e.pt.x), float32(e.pt.y)
	return [4]float32{x, y, x + max(1, e.scale), y + float32(e.lineH)}, true
}

// sync measures the height again when the text, the width or the scale
// changed.
func (e *Editor) sync() {
	if e.released {
		return
	}
	if e.w.scale > 0 && e.w.scale != e.scale {
		e.scale = e.w.scale
		e.charFormat()
		win32.Com(e.ts, vtTSPropertyBitsChange, txtCharFormatChange, txtCharFormatChange)
		e.measureLine()
		e.updateClient()
		e.dirtyH = true
	}
	if !e.dirtyH {
		return
	}
	e.dirtyH = false
	e.quiet++
	e.send(emRequestResize, 0, 0)
	e.quiet--
	content := max(e.contentH, e.lineH)
	h := float32(min(max(content, int32(e.minLines)*e.lineH), int32(e.maxLines)*e.lineH))/e.scale + 2*e.opts.InsetY
	if h != e.height {
		e.height = h
		e.pend |= EditorHeightChanged
	}
}

// wake queues one EvHostEdit until the signals are taken, and arms a frame.
func (e *Editor) wake() {
	if !e.woken && !e.released {
		e.woken = true
		e.w.q.push(Event{Kind: EvHostEdit, Tag: uint64(e.host), Time: Now()})
		e.w.RequestFrame()
	}
}

// invalidate asks for a render of the editor's window (WM_PAINT).
func (e *Editor) invalidate() {
	if e.hwnd != 0 && !e.render {
		e.render = true
		win32.Call(rich.pInvalidateRect, e.hwnd, 0, 0)
	}
}

// updateClient reads the window's size and tells RichEdit its client
// rectangle: the window inset by InsetX and InsetY.
func (e *Editor) updateClient() {
	win32.Call(pGetClientRect, e.hwnd, uintptr(unsafe.Pointer(&e.rc)))
	e.cw, e.ch = e.rc.right, e.rc.bottom
	e.clientRect()
	if e.ts != 0 {
		win32.Com(e.ts, vtTSPropertyBitsChange, txtClientRectChange, txtClientRectChange)
	}
}

// clientRect puts RichEdit's client rectangle in e.rc.
func (e *Editor) clientRect() {
	ix, iy := int32(e.opts.InsetX*e.scale+0.5), int32(e.opts.InsetY*e.scale+0.5)
	e.rc = rect{ix, iy, max(e.cw-ix, ix), max(e.ch-iy, iy)}
}

// propertyBits are the TXTBIT_* the editor has.
func (e *Editor) propertyBits() uint32 {
	b := uint32(txtRichText | txtMultiLine | txtWordWrap | txtD2DDWrite)
	if e.opts.ReadOnly {
		b |= txtReadOnly
	}
	return b
}

// paint draws the editor: the background, RichEdit's text, the placeholder
// and the caret, and presents.
func (e *Editor) paint() {
	e.render = false
	s := &e.surf
	if e.ts == 0 || s.rt == 0 {
		return
	}
	win32.Com(s.rt, vtRTBeginDraw)
	win32.Com(s.rt, vtRTClear, uintptr(unsafe.Pointer(&e.backCol)))
	e.clientRect()
	s.bounds = e.rc
	win32.Com(e.ts, vtTSDrawD2D, s.rt, uintptr(unsafe.Pointer(&s.bounds)), 0, 0 /* TXTVIEW_ACTIVE */)
	if len(e.text) == 0 && !e.marked && len(e.ph16) > 0 {
		f := s.placeholderFormat(e.cf.pixels(e))
		s.drawText(e.ph16, f, float32(e.rc.left), float32(e.rc.top), float32(e.rc.right), float32(e.rc.bottom), e.phCol)
	}
	if e.focused && e.caretShown && e.blinkOn && !e.opts.ReadOnly {
		w := max(e.caretW, 1)
		s.fillRect(float32(e.caretX), float32(e.caretY), float32(e.caretX+w), float32(e.caretY+e.caretH), e.textCol)
	}
	if hr := win32.HR(win32.Com(s.rt, vtRTEndDraw, 0, 0)); hr == -2003238900 /* D2DERR_RECREATE_TARGET */ {
		win32.Release(s.brush)
		win32.Release(s.rt)
		s.brush, s.rt = 0, 0
		s.target()
		e.invalidate()
		return
	}
	win32.Com(s.sc, vtSwapChainPresent, 0, 0)
	s.frames++
}

// pixels is the format's font size in pixels.
func (c *charFormat2) pixels(e *Editor) float32 {
	pt := e.opts.FontSize
	if pt <= 0 {
		pt = 13
	}
	return pt * 96 / 72 * e.scale
}

// editorOfHWND is the editor whose window is hwnd.
func editorOfHWND(hwnd uintptr) *Editor {
	for _, e := range edTable {
		if e != nil && e.hwnd == hwnd {
			return e
		}
	}
	return nil
}

func editorAt(v uintptr) *Editor {
	i := int(v) - 1
	if i < 0 || i >= maxEditors {
		return nil
	}
	return edTable[i]
}

// editorWndProc is the editor window's procedure.
func editorWndProc(hwnd, m, wp, lp uintptr) uintptr {
	e := editorOfHWND(hwnd)
	if e == nil || e.ts == 0 {
		return win32.Call(pDefWindowProcW, hwnd, m, wp, lp)
	}
	switch m {
	case wmEraseBkgnd:
		return 1
	case wmPaint:
		e.paint()
		win32.Call(pValidateRect, hwnd, 0)
		return 0
	case wmSize:
		w, h := uloword(lp), uhiword(lp)
		e.surf.resize(w, h)
		if w != e.cw || h != e.ch {
			e.updateClient()
			e.dirtyH = true
			e.wake()
		}
		e.invalidate()
		return 0
	case wmGetDlgCodeEd:
		return dlgcAll
	case wmSetFocus:
		e.focused = true
		win32.Com(e.ts, vtTSUIActivate)
		e.send(wmSetFocus, wp, lp)
		e.blinkOn = true
		if t := uintptr(uint32(win32.Call(rich.pGetCaretBlink))); t > 0 && t != 0xFFFFFFFF {
			win32.Call(pSetTimer, hwnd, blinkTimerID, t, 0)
		}
		e.invalidate()
		return 0
	case wmKillFocus:
		e.focused = false
		e.send(wmKillFocus, wp, lp)
		win32.Com(e.ts, vtTSUIDeactivate)
		win32.Call(pKillTimer, hwnd, blinkTimerID)
		if e.sysCaret {
			win32.Call(rich.pDestroyCaret)
			e.sysCaret = false
		}
		if e.compl {
			e.SetCompletionActive(false)
			e.pend |= EditorCancelled
			e.wake()
		}
		e.invalidate()
		return 0
	case wmTimer:
		if wp == blinkTimerID {
			e.blinkOn = !e.blinkOn
			e.invalidate()
			return 0
		}
		e.send(uint32(m), wp, lp)
		return 0
	case wmSetCursor:
		if uint32(lp&0xFFFF) == htClient {
			if x, y, ok := e.w.pointerNow(hwnd); ok {
				e.clientRect()
				win32.Com(e.ts, vtTSOnSetCursor, 1 /* DVASPECT_CONTENT */, ^uintptr(0), 0, 0, 0, 0, uintptr(unsafe.Pointer(&e.rc)), uintptr(int32(x)), uintptr(int32(y)))
				return 1
			}
		}
		return win32.Call(pDefWindowProcW, hwnd, m, wp, lp)
	case wmKeyDown, wmSysKeyDown:
		if m == wmKeyDown && e.key(wp) {
			return 0
		}
		if m == wmSysKeyDown { // F10, Alt and Alt chords are the window's too (a drawn menu bar's)
			e.w.keyMessage(m, wp, lp)
		}
		return e.forward(hwnd, m, wp, lp)
	case wmSysChar:
		if wp != ' ' {
			return 0 // the window's mnemonic (keyMessage); no beep
		}
		return e.forward(hwnd, m, wp, lp)
	case wmChar:
		if e.nEat > 0 && uint16(wp) == e.eat[0] {
			copy(e.eat[:], e.eat[1:e.nEat])
			e.nEat--
			return 0
		}
		if !e.fits(uint16(wp)) {
			win32.Call(rich.pMessageBeep, 0xFFFFFFFF)
			return 0
		}
		e.send(uint32(m), wp, lp)
		return 0
	case wmImeSetContext:
		// RichEdit draws the composition in its text; the system's IME
		// window must still be told of the context (DefWindowProc), or the
		// input method never composes here.
		e.send(uint32(m), wp, lp)
		return win32.Call(pDefWindowProcW, hwnd, m, wp, lp&^iscShowCompWindow)
	case wmImeStartComp:
		e.logIME(m, wp, lp)
		e.startComposition()
		return 0
	case wmImeComp:
		e.logIME(m, wp, lp)
		e.composition(lp)
		return 0
	case wmImeEndComp:
		e.logIME(m, wp, lp)
		e.endComposition()
		return 0
	case wmImeNotify, wmImeRequest, wmImeChar, wmInputLangChange, wmKeyUp, wmSysKeyUp:
		if m == wmSysKeyUp || m == wmKeyUp && vkIsModifier(wp) { // the Alt tap's release (keyMessage)
			e.w.keyMessage(m, wp, lp)
		}
		return e.forward(hwnd, m, wp, lp)
	case wmMouseWheel, wmMouseHWheel:
		if e.send(uint32(m), wp, lp) == 0 && e.lres == 0 && int32(e.maxLines)*e.lineH < e.contentH {
			return 0 // scrolled the editor
		}
		return win32.Call(pDefWindowProcW, hwnd, m, wp, lp) // up to the window, for ui
	}
	if m >= wmMouseFirst && m <= wmMouseLast {
		if m == wmLButtonDown || m == wmRButtonDown {
			if f := win32.Call(pGetFocus); f != hwnd {
				win32.Call(pSetFocus, hwnd)
			}
		}
		e.send(uint32(m), wp, lp)
		return 0
	}
	return win32.Call(pDefWindowProcW, hwnd, m, wp, lp)
}

// forward hands a message to the text services, and to DefWindowProc when
// they leave it (S_FALSE): an input method's UI, Alt+F4.
func (e *Editor) forward(hwnd, m, wp, lp uintptr) uintptr {
	hr := e.send(uint32(m), wp, lp)
	if m >= 0x0281 && m <= 0x0291 {
		e.logIME(m, wp, uintptr(uint32(hr)))
	}
	if hr == 1 {
		return win32.Call(pDefWindowProcW, hwnd, m, wp, lp)
	}
	return e.lres
}

// logIME records an IME message for the tests' report.
func (e *Editor) logIME(m, wp, lp uintptr) {
	if i := e.imeMsgs; i < len(e.imeLog) {
		e.imeLog[i] = [4]uintptr{m, wp, lp, 0}
	}
	e.imeMsgs++
}

// The composition (IMM): windowless RichEdit takes WM_IME_COMPOSITION but
// puts no composition string in its text (seen on both runners), so the
// editor does, as an editor that draws its own text does. The string sits
// at the composition's start, underlined, written with the undo stack
// suspended; the IME's result replaces it as one ordinary edit. A
// selection is deleted when the composition starts, as typing would.

// startComposition is WM_IME_STARTCOMPOSITION.
func (e *Editor) startComposition() {
	e.quiet++
	e.send(emExGetSel, 0, uintptr(unsafe.Pointer(&e.cr)))
	if e.cr.min != e.cr.max {
		e.u16 = append(e.u16[:0], 0)
		e.send(emReplaceSel, 1, uintptr(unsafe.Pointer(&e.u16[0])))
		e.send(emExGetSel, 0, uintptr(unsafe.Pointer(&e.cr)))
	}
	e.quiet--
	e.ime, e.compStart, e.compLen = true, int(e.cr.min), 0
	if e.compl {
		e.SetCompletionActive(false)
		e.pend |= EditorCancelled
		e.wake()
	}
}

// imeString reads one of the IME's strings (GCS_COMPSTR, GCS_RESULTSTR)
// into imeBuf.
func (e *Editor) imeString(himc uintptr, which uintptr) []uint16 {
	n := int(int32(win32.Call(rich.pImmGetCompStr, himc, which, uintptr(unsafe.Pointer(&e.imeBuf[0])), uintptr(2*(len(e.imeBuf)-1)))))
	if n <= 0 {
		return e.imeBuf[:0]
	}
	return e.imeBuf[:min(n/2, len(e.imeBuf)-1)]
}

// putComposition replaces the composition in the text with s, with the
// undo stack suspended, underlined or not.
func (e *Editor) putComposition(s []uint16, underline bool) {
	e.quiet++
	if e.doc != 0 {
		win32.Com(e.doc, vtDocUndo, u32(tomSuspend), 0)
	}
	e.cr = chrRange{int32(e.compStart), int32(e.compStart + e.compLen)}
	e.send(emExSetSel, 0, uintptr(unsafe.Pointer(&e.cr)))
	e.u16 = append(append(e.u16[:0], s...), 0)
	e.send(emReplaceSel, 0, uintptr(unsafe.Pointer(&e.u16[0])))
	e.compEdited = e.compEdited || e.compLen > 0 || len(s) > 0
	e.compLen = len(s)
	if len(s) > 0 {
		e.cr = chrRange{int32(e.compStart), int32(e.compStart + e.compLen)}
		e.send(emExSetSel, 0, uintptr(unsafe.Pointer(&e.cr)))
		e.spanCF = charFormat2{mask: cfmUnderline}
		if underline {
			e.spanCF.effects = 4 // CFE_UNDERLINE
		}
		e.spanCF.size = uint32(unsafe.Sizeof(e.spanCF))
		e.send(emSetCharFormat, scfSelection, uintptr(unsafe.Pointer(&e.spanCF)))
	}
	if e.doc != 0 {
		win32.Com(e.doc, vtDocUndo, u32(tomResume), 0)
	}
	e.quiet--
}

// composition is WM_IME_COMPOSITION: the result is committed, then the
// composition string (what is still being composed) is shown.
func (e *Editor) composition(flags uintptr) {
	if !e.ime {
		e.startComposition()
	}
	himc := win32.Call(pImmGetContext, e.hwnd)
	if himc == 0 {
		return
	}
	defer win32.Call(pImmReleaseContext, e.hwnd, himc)
	if flags&gcsResultStr != 0 {
		res := e.imeString(himc, gcsResultStr)
		e.putComposition(nil, false)
		if e.maxLen > 0 { // the result is cut to what MaxLen leaves
			e.copyText()
			room := e.maxLen - len(e.text)
			for len(res) > 0 && len(appendText(nil, res)) > room {
				res = res[:len(res)-1]
				if len(res) > 0 && utf16.IsSurrogate(rune(res[len(res)-1])) {
					res = res[:len(res)-1]
				}
			}
		}
		if len(res) > 0 {
			e.cr = chrRange{int32(e.compStart), int32(e.compStart)}
			e.quiet++
			e.send(emExSetSel, 0, uintptr(unsafe.Pointer(&e.cr)))
			e.spanCF = charFormat2{mask: cfmUnderline}
			e.spanCF.size = uint32(unsafe.Sizeof(e.spanCF))
			e.send(emSetCharFormat, scfSelection, uintptr(unsafe.Pointer(&e.spanCF))) // the typing format: not underlined
			e.quiet--
			e.u16 = append(append(e.u16[:0], res...), 0)
			e.send(emStopGroupTyping, 0, 0)
			e.send(emReplaceSel, 1, uintptr(unsafe.Pointer(&e.u16[0]))) // an ordinary edit: EditorChanged
			e.send(emStopGroupTyping, 0, 0)
			e.compStart += len(res)
		}
	}
	if flags&gcsCompStr != 0 {
		comp := e.imeString(himc, gcsCompStr)
		e.putComposition(comp, true)
		cur := len(comp)
		if flags&gcsCursorPos != 0 {
			cur = min(max(int(int32(win32.Call(rich.pImmGetCompStr, himc, gcsCursorPos, 0, 0))), 0), len(comp))
		}
		e.quiet++
		e.send(emSetSel, uintptr(e.compStart+cur), uintptr(e.compStart+cur))
		e.quiet--
		e.placeCandidates(himc, e.compStart+cur)
	}
	e.compositionMoved()
}

// endComposition is WM_IME_ENDCOMPOSITION: a composition still in the text
// (cancelled) is taken out.
func (e *Editor) endComposition() {
	if e.compLen > 0 {
		e.putComposition(nil, false)
		e.quiet++
		e.send(emSetSel, uintptr(e.compStart), uintptr(e.compStart))
		e.quiet--
	}
	e.ime, e.compLen = false, 0
	e.compositionMoved()
}

// placeCandidates puts the IME's candidate window under the caret at unit
// cp.
func (e *Editor) placeCandidates(himc uintptr, cp int) {
	e.pt = point{}
	win32.Com(e.ts, vtTSSendMessage, emPosFromChar, uintptr(unsafe.Pointer(&e.pt)), uintptr(cp), uintptr(unsafe.Pointer(&e.lres)))
	e.cand = candidateForm{style: cfsCandidatePos, pt: point{e.pt.x, e.pt.y + e.lineH}}
	win32.Call(pImmSetCandWindow, himc, uintptr(unsafe.Pointer(&e.cand)))
}

// compositionMoved signals a change putComposition made (quietly, as it is
// not an edit for the undo stack): EditorChanged, with EditorComposing while
// a composition remains.
func (e *Editor) compositionMoved() {
	edited := e.compEdited
	e.compEdited = false
	e.copyText()
	if e.quiet > 0 || !edited {
		return
	}
	e.pend |= EditorChanged
	if e.marked {
		e.pend |= EditorComposing
		if e.compl {
			e.SetCompletionActive(false)
			e.pend |= EditorCancelled
		}
	} else {
		e.pend &^= EditorComposing
	}
	e.dirtyH = true
	e.invalidate()
	e.wake()
}

// fits reports whether typing character u keeps the text within MaxLen.
func (e *Editor) fits(u uint16) bool {
	if e.maxLen <= 0 || u < 0x20 && u != '\r' && u != '\t' || e.ime {
		return true
	}
	add := 3
	switch {
	case u < 0x80:
		add = 1
	case u < 0x800:
		add = 2
	case utf16.IsSurrogate(rune(u)):
		add = 2 // half of a 4-byte scalar
	}
	r := e.sel.Range()
	return len(e.text)-(r.End-r.Start)+add <= e.maxLen
}

// key is the editor's part of WM_KEYDOWN: true if it took key vk (the
// WM_CHAR TranslateMessage made of it is eaten too).
func (e *Editor) key(vk uintptr) bool {
	if e.ime || e.marked {
		return false
	}
	mods := currentMods()
	switch vk {
	case vkReturn:
		switch {
		case e.compl:
			e.pushCommandSends(CmdAccept, e.sends(mods))
			e.wake()
		case e.sends(mods):
			e.pend |= EditorSubmitted
			e.wake()
		case e.opts.ReadOnly:
			return false
		default:
			// A line: Shift+Enter would be RichEdit's soft break (VT); every
			// Enter that adds a line adds a paragraph.
			if !e.fits('\r') {
				win32.Call(rich.pMessageBeep, 0xFFFFFFFF)
			} else {
				e.u16 = append(e.u16[:0], '\r', 0)
				e.send(emReplaceSel, 1, uintptr(unsafe.Pointer(&e.u16[0])))
			}
		}
		if mods&ModCtrl != 0 {
			e.eatNext('\n') // Ctrl+Enter's character is LF
		} else {
			e.eatNext('\r')
		}
		return true
	case vkTab:
		i := int(e.host&0xFF) - 1
		switch {
		case e.compl && mods&ModShift == 0:
			e.toApp(CmdTab)
		case e.compl:
			e.toApp(CmdBacktab)
		default:
			e.w.hostCommand(i, KeyTab, mods&ModShift)
		}
		e.eatNext('\t')
		return true
	case vkEscape:
		if e.compl {
			e.toApp(CmdCancel)
		} else {
			e.pend |= EditorCancelled
			e.wake()
			e.w.hostCommand(int(e.host&0xFF)-1, KeyEscape, 0)
		}
		e.eatNext(0x1B)
		return true
	case vkUp, vkDown:
		up := vk == vkUp
		switch {
		case e.compl:
			if up {
				e.toApp(CmdUp)
			} else {
				e.toApp(CmdDown)
			}
			return true
		case e.edges && mods&ModShift == 0 && e.atEdge(up):
			if up {
				e.pend |= EditorEdgeUp
			} else {
				e.pend |= EditorEdgeDown
			}
			e.wake() // and RichEdit moves the caret as it would
		}
	}
	return false
}

// eatNext drops the next WM_CHAR of c (the key's, which the editor took).
func (e *Editor) eatNext(c uint16) {
	if e.nEat < len(e.eat) {
		e.eat[e.nEat] = c
		e.nEat++
	}
}

// atEdge reports whether the caret, with nothing selected, is on the text's
// first line (up) or its last (down), as laid out.
func (e *Editor) atEdge(up bool) bool {
	win32.Com(e.ts, vtTSSendMessage, emExGetSel, 0, uintptr(unsafe.Pointer(&e.cr)), uintptr(unsafe.Pointer(&e.lres)))
	if e.cr.min != e.cr.max {
		return false
	}
	if len(e.text) == 0 {
		return true
	}
	win32.Com(e.ts, vtTSSendMessage, emExLineFromChar, 0, uintptr(e.cr.min), uintptr(unsafe.Pointer(&e.lres)))
	line := int(e.lres)
	if up {
		return line == 0
	}
	win32.Com(e.ts, vtTSSendMessage, emGetLineCount, 0, 0, uintptr(unsafe.Pointer(&e.lres)))
	return line >= int(e.lres)-1
}

// sends reports whether Enter with modifiers m submits: Ctrl is Windows'
// ⌘ for CmdEnterSends.
func (e *Editor) sends(m Mods) bool {
	switch e.enter {
	case EnterNewline:
		return false
	case CmdEnterSends:
		return m&ModCtrl != 0
	}
	return m&(ModShift|ModAlt|ModCtrl) == 0
}

func (e *Editor) toApp(c EditorCommand) {
	e.pushCommand(c)
	e.wake()
}

// ---- ITextHost2 ------------------------------------------------------------

// textHostMethods are ITextHost's and ITextHost2's methods in vtable order
// (textserv.h).
func textHostMethods() []com.Method {
	ok := func(n int) com.Method { return com.Method{N: n, Fn: func(*com.Object, com.Args) uintptr { return 0 }} }
	yes := func(n int) com.Method { return com.Method{N: n, Fn: func(*com.Object, com.Args) uintptr { return 1 }} }
	notImpl := func(n int) com.Method {
		return com.Method{N: n, Fn: func(*com.Object, com.Args) uintptr { return u32(win32.ENotImpl) }}
	}
	ed := func(n int, fn func(e *Editor, a com.Args) uintptr) com.Method {
		return com.Method{N: n, Fn: func(o *com.Object, a com.Args) uintptr {
			if e := editorAt(o.Value()); e != nil && !e.released {
				return fn(e, a)
			}
			return 0
		}}
	}
	setL := func(p uintptr, v int32) {
		if p != 0 {
			*(*int32)(win32.Ptr(p)) = v
		}
	}
	return []com.Method{
		ed(0, func(e *Editor, a com.Args) uintptr { return win32.Call(rich.pGetDC, e.hwnd) }),           // TxGetDC
		ed(1, func(e *Editor, a com.Args) uintptr { return win32.Call(rich.pReleaseDC, e.hwnd, a[0]) }), // TxReleaseDC
		yes(2), // TxShowScrollBar
		yes(2), // TxEnableScrollBar
		yes(4), // TxSetScrollRange
		yes(3), // TxSetScrollPos
		ed(2, func(e *Editor, a com.Args) uintptr { e.invalidate(); return 0 }), // TxInvalidateRect
		ed(1, func(e *Editor, a com.Args) uintptr { e.invalidate(); return 0 }), // TxViewChange
		ed(3, func(e *Editor, a com.Args) uintptr { // TxCreateCaret
			e.caretW, e.caretH = int32(a[1]), int32(a[2])
			if e.focused {
				e.sysCaret = win32.Bool(win32.Call(rich.pCreateCaret, e.hwnd, 0, a[1], a[2]))
			}
			return 1
		}),
		ed(1, func(e *Editor, a com.Args) uintptr { // TxShowCaret
			e.caretShown = a[0] != 0
			e.blinkOn = true
			e.invalidate()
			return 1
		}),
		ed(2, func(e *Editor, a com.Args) uintptr { // TxSetCaretPos
			e.caretX, e.caretY = int32(a[0]), int32(a[1])
			e.blinkOn = true
			if e.sysCaret {
				win32.Call(rich.pSetCaretPos, a[0], a[1])
			}
			e.invalidate()
			return 1
		}),
		ed(2, func(e *Editor, a com.Args) uintptr { // TxSetTimer
			return uintptr(boolInt(win32.Call(pSetTimer, e.hwnd, a[0], a[1], 0) != 0))
		}),
		ed(1, func(e *Editor, a com.Args) uintptr { win32.Call(pKillTimer, e.hwnd, a[0]); return 0 }), // TxKillTimer
		ed(7, func(e *Editor, a com.Args) uintptr { e.invalidate(); return 0 }),                       // TxScrollWindowEx
		ed(1, func(e *Editor, a com.Args) uintptr { // TxSetCapture
			if a[0] != 0 {
				win32.Call(pSetCapture, e.hwnd)
			} else {
				win32.Call(pReleaseCapture)
			}
			return 0
		}),
		ed(0, func(e *Editor, a com.Args) uintptr { win32.Call(pSetFocus, e.hwnd); return 0 }), // TxSetFocus
		ed(2, func(e *Editor, a com.Args) uintptr { win32.Call(pSetCursor, a[0]); return 0 }),  // TxSetCursor
		ed(1, func(e *Editor, a com.Args) uintptr { return win32.Call(rich.pScreenToClientEd, e.hwnd, a[0]) }),
		ed(1, func(e *Editor, a com.Args) uintptr { return win32.Call(rich.pClientToScreen, e.hwnd, a[0]) }),
		ed(1, func(e *Editor, a com.Args) uintptr { setL(a[0], 0); return 0 }), // TxActivate
		ok(1), // TxDeactivate
		ed(1, func(e *Editor, a com.Args) uintptr { // TxGetClientRect
			e.clientRect()
			if a[0] != 0 {
				*(*rect)(win32.Ptr(a[0])) = e.rc
			}
			return 0
		}),
		com.Method{N: 1, Fn: func(_ *com.Object, a com.Args) uintptr { // TxGetViewInset (HIMETRIC): none, the client rect is inset
			if a[0] != 0 {
				*(*rect)(win32.Ptr(a[0])) = rect{}
			}
			return 0
		}},
		ed(1, func(e *Editor, a com.Args) uintptr { // TxGetCharFormat
			if a[0] != 0 {
				*(*uintptr)(win32.Ptr(a[0])) = uintptr(unsafe.Pointer(&e.cf))
			}
			return 0
		}),
		ed(1, func(e *Editor, a com.Args) uintptr { // TxGetParaFormat
			if a[0] != 0 {
				*(*uintptr)(win32.Ptr(a[0])) = uintptr(unsafe.Pointer(&e.pf))
			}
			return 0
		}),
		ed(1, func(e *Editor, a com.Args) uintptr { // TxGetSysColor
			switch a[0] {
			case 8: // COLOR_WINDOWTEXT
				return uintptr(e.textRef)
			case 5: // COLOR_WINDOW
				return uintptr(e.backRef)
			}
			return win32.Call(rich.pGetSysColor, a[0])
		}),
		com.Method{N: 1, Fn: func(_ *com.Object, a com.Args) uintptr { setL(a[0], 0); return 0 }},             // TxGetBackStyle: transparent (the editor clears)
		com.Method{N: 1, Fn: func(_ *com.Object, a com.Args) uintptr { setL(a[0], 1<<30); return 0 }},         // TxGetMaxLength: MaxLen is bytes, checked by the editor
		com.Method{N: 1, Fn: func(_ *com.Object, a com.Args) uintptr { setL(a[0], esAutoVScroll); return 0 }}, // TxGetScrollBars
		com.Method{N: 1, Fn: func(*com.Object, com.Args) uintptr { return 1 }},                                // TxGetPasswordChar: S_FALSE
		com.Method{N: 1, Fn: func(_ *com.Object, a com.Args) uintptr { setL(a[0], -1); return 0 }},            // TxGetAcceleratorPos
		notImpl(1), // TxGetExtent: the client rectangle, no zoom
		ok(1),      // OnTxCharFormatChange
		ok(1),      // OnTxParaFormatChange
		ed(2, func(e *Editor, a com.Args) uintptr { // TxGetPropertyBits
			setL(a[1], int32(e.propertyBits()&uint32(a[0])))
			return 0
		}),
		ed(2, func(e *Editor, a com.Args) uintptr { e.notify(uint32(a[0]), a[1]); return 0 }),                 // TxNotify
		ed(0, func(e *Editor, a com.Args) uintptr { return win32.Call(pImmGetContext, e.hwnd) }),              // TxImmGetContext
		ed(1, func(e *Editor, a com.Args) uintptr { win32.Call(pImmReleaseContext, e.hwnd, a[0]); return 0 }), // TxImmReleaseContext
		com.Method{N: 1, Fn: func(_ *com.Object, a com.Args) uintptr { setL(a[0], 0); return 0 }},             // TxGetSelectionBarWidth
		// ITextHost2.
		com.Method{N: 0, Fn: func(*com.Object, com.Args) uintptr { return 0 }}, // TxIsDoubleClickPending
		ed(1, func(e *Editor, a com.Args) uintptr { // TxGetWindow
			if a[0] != 0 {
				*(*uintptr)(win32.Ptr(a[0])) = e.hwnd
			}
			return 0
		}),
		ed(0, func(e *Editor, a com.Args) uintptr { win32.Call(rich.pSetForeground, e.w.hwnd); return 0 }), // TxSetForegroundWindow
		com.Method{N: 0, Fn: func(*com.Object, com.Args) uintptr { return 0 }},                             // TxGetPalette
		com.Method{N: 1, Fn: func(_ *com.Object, a com.Args) uintptr { setL(a[0], 0); return 0 }},          // TxGetEastAsianFlags: IME allowed (E_NOTIMPL would turn it off)
		ed(2, func(e *Editor, a com.Args) uintptr { return win32.Call(pSetCursor, a[0]) }),                 // TxSetCursor2
		com.Method{N: 0, Fn: func(*com.Object, com.Args) uintptr { return 0 }},                             // TxFreeTextServicesNotification
		notImpl(2), // TxGetEditStyle
		com.Method{N: 2, Fn: func(_ *com.Object, a com.Args) uintptr { // TxGetWindowStyles
			setL(a[0], int32(uint32(wsChild|wsVisible)))
			setL(a[1], 0)
			return 0
		}},
		ok(3), // TxShowDropCaret
		ed(0, func(e *Editor, a com.Args) uintptr { e.caretShown = false; e.invalidate(); return 0 }), // TxDestroyCaret
		com.Method{N: 1, Fn: func(_ *com.Object, a com.Args) uintptr { setL(a[0], 0); return 0 }},     // TxGetHorzExtent
	}
}

// u32 is a negative 32-bit value (an HRESULT, a TOM constant) as an
// argument or result word.
func u32(v int32) uintptr { return uintptr(uint32(v)) }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// notify is ITextHost::TxNotify: a change, a selection change, a height.
func (e *Editor) notify(code uint32, pv uintptr) {
	switch code {
	case enChange:
		e.changed = true
	case enSelChange:
		e.selMoved = true
		e.invalidate()
	case enRequestResize:
		if pv != 0 {
			// REQRESIZE: an NMHDR (24 bytes on 64-bit Windows), then the RECT.
			r := (*rect)(unsafe.Add(win32.Ptr(pv), 24))
			e.contentH = r.bottom - r.top
		}
	}
}

// ---- IRichEditOleCallback ---------------------------------------------------

const cfBitmapFmt = 2 // CF_BITMAP

// oleCallbackMethods are IRichEditOleCallback's methods after IUnknown
// (richole.h): GetNewStorage, GetInPlaceContext, ShowContainerUI,
// QueryInsertObject, DeleteObject, QueryAcceptData, ContextSensitiveHelp,
// GetClipboardData, GetDragDropEffect, GetContextMenu.
func oleCallbackMethods() []com.Method {
	notImpl := func(n int) com.Method {
		return com.Method{N: n, Fn: func(*com.Object, com.Args) uintptr { return u32(win32.ENotImpl) }}
	}
	ok := func(n int) com.Method { return com.Method{N: n, Fn: func(*com.Object, com.Args) uintptr { return 0 }} }
	return []com.Method{
		notImpl(1), // GetNewStorage: no embedded objects
		notImpl(3), // GetInPlaceContext
		ok(1),      // ShowContainerUI
		{N: 3, Fn: func(*com.Object, com.Args) uintptr { return u32(win32.EFail) }}, // QueryInsertObject: none
		ok(1), // DeleteObject
		{N: 5, Fn: func(o *com.Object, a com.Args) uintptr { // QueryAcceptData(dataobj, *cf, reco, really, hMetaPict)
			e := editorAt(o.Value())
			if e == nil || a[0] == 0 {
				return u32(win32.EFail)
			}
			return e.acceptData(a[0], a[1], a[3] != 0)
		}},
		notImpl(1), // ContextSensitiveHelp
		notImpl(3), // GetClipboardData: RichEdit's own
		notImpl(3), // GetDragDropEffect
		notImpl(4), // GetContextMenu
	}
}

// hasFormat asks data object d for clipboard format cf on an HGLOBAL.
func hasFormat(d uintptr, cf uint16) bool {
	rich.fe = formatEtc{cfFormat: cf, dwAspect: 1, lindex: -1, tymed: 1 /* TYMED_HGLOBAL */}
	if cf == cfBitmapFmt {
		rich.fe.tymed = 16 // TYMED_GDI
	}
	return win32.HR(win32.Com(d, vtDataObjectQueryGetDat, uintptr(unsafe.Pointer(&rich.fe)))) == 0
}

// acceptData decides a paste or a drop: files and an image without text are
// the app's (signalled when it really happens), text is taken as plain text.
func (e *Editor) acceptData(d, pcf uintptr, really bool) uintptr {
	pngFmt := uint16(pngClipFormat())
	switch {
	case hasFormat(d, cfHDrop):
		if really {
			e.pend |= EditorPastedFiles
			e.wake()
		}
		return u32(win32.EFail)
	case !hasFormat(d, cfUnicodeText) && (hasFormat(d, cfDIB) || hasFormat(d, cfDIBV5) || hasFormat(d, cfBitmapFmt) || pngFmt != 0 && hasFormat(d, pngFmt)):
		if really {
			e.pend |= EditorPastedImage
			e.wake()
		}
		return u32(win32.EFail)
	case e.opts.ReadOnly:
		return u32(win32.EFail)
	}
	if pcf != 0 {
		*(*uint16)(win32.Ptr(pcf)) = cfUnicodeText // plain text, whatever richer formats come with it
	}
	return 0
}

var pngFormat uint32

// pngClipFormat is the registered "PNG" clipboard format.
func pngClipFormat() uint32 {
	if pngFormat == 0 {
		p := win32.Proc(user32, "RegisterClipboardFormatW")
		pngFormat = uint32(win32.Call(p, uintptr(unsafe.Pointer(mustUTF16("PNG")))))
	}
	return pngFormat
}
