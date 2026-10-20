//go:build darwin

package platform

import (
	"runtime"
	"sync"
	"unicode/utf8"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The macOS editor: GNUITextView, an NSTextView subclass on TextKit 1, in an
// NSScrollView that is the host's view (HostView), with a GNUIEditorDelegate.
//
//   - Text reaches Go on textDidChange: only: the delegate copies the text
//     storage's string into the editor's buffer, rebuilds the UTF-16 map and
//     reads the selection and the marked range.
//   - The completion handoff and the Enter policy live in GNUITextView's
//     doCommandBySelector:, which every key binding of the text view goes
//     through (keyDown: → interpretKeyEvents: → the input method →
//     doCommandBySelector:), and its keyDown: for ⌘Enter, which no binding
//     names. Tab, Shift+Tab and Escape with nothing to complete go back to
//     the app as EvHostCommand (N3's focus hand-off), Enter by the policy
//     as EditorSubmitted.
//   - Replace runs as a user edit: shouldChangeTextInRange:replacementString:
//     (which registers the undo and asks the delegate, which applies the
//     maximum length), the text storage's replaceCharactersInRange:withString:
//     between beginEditing and endEditing, didChangeText; undo coalescing is
//     broken before and after, so the replacement is one undo step of its own.
//   - Each editor has its own NSUndoManager (undoManagerForTextView:), so
//     SetText can clear it without touching the window's.
//   - A paste with an image and no plain text is the app's
//     (readSelectionFromPasteboard:): EditorPastedImage, nothing inserted.
//   - Mentions are temporary attributes of the layout manager, display-only.
//   - TextKit 1 (initUsingTextLayoutManager: NO) because the contract's
//     height (usedRectForTextContainer:) and styling (temporary attributes)
//     are TextKit 1's; Writing Tools then works through its panel.

var (
	edOnce                 sync.Once
	edTVClass, edDelClass  objc.ID
	edTVDef, edDelDef      *objc.ClassDef
	edEmpty                objc.ID           // @""
	edApp                  objc.ID           // NSApp
	edPHClass              objc.ID           // GNUIEditorPlaceholder
	edTable                [MaxHosts]*Editor // index+1 is in the views' Index ivar
	edStret                uintptr
	edImageTypes           objc.ID // NSArray of the image types a paste routes to the app
	edPlainType            objc.ID
	edFgAttr, edBgAttr     objc.ID
	edFrameNote            objc.ID
	clsNSScrollView        objc.ID
	clsNSColor, clsNSFont  objc.ID
	clsNSUndoManager       objc.ID
	clsEdString, clsNSNote objc.ID

	selInitUsingTLM        = objc.Sel("initUsingTextLayoutManager:")
	selSetDocumentView     = objc.Sel("setDocumentView:")
	selDocumentView        = objc.Sel("documentView")
	selSetHasVScroller     = objc.Sel("setHasVerticalScroller:")
	selSetAutohides        = objc.Sel("setAutohidesScrollers:")
	selSetDrawsBackground  = objc.Sel("setDrawsBackground:")
	selSetBorderType       = objc.Sel("setBorderType:")
	selSetMinSize          = objc.Sel("setMinSize:")
	selSetMaxSize          = objc.Sel("setMaxSize:")
	selSetVResizable       = objc.Sel("setVerticallyResizable:")
	selSetHResizable       = objc.Sel("setHorizontallyResizable:")
	selSetRichText         = objc.Sel("setRichText:")
	selSetImportsGraphics  = objc.Sel("setImportsGraphics:")
	selSetAllowsUndo       = objc.Sel("setAllowsUndo:")
	selSetFont             = objc.Sel("setFont:")
	selSetTextColor        = objc.Sel("setTextColor:")
	selTextColor           = objc.Sel("textColor")
	selSystemFontOfSize    = objc.Sel("systemFontOfSize:")
	selSetContainerInset   = objc.Sel("setTextContainerInset:")
	selTextContainer       = objc.Sel("textContainer")
	selLayoutManager       = objc.Sel("layoutManager")
	selTextStorage         = objc.Sel("textStorage")
	selSetWidthTracks      = objc.Sel("setWidthTracksTextView:")
	selSetContainerSize    = objc.Sel("setContainerSize:")
	selDefaultLineHeight   = objc.Sel("defaultLineHeightForFont:")
	selEnsureLayout        = objc.Sel("ensureLayoutForTextContainer:")
	selUsedRect            = objc.Sel("usedRectForTextContainer:")
	selSetContSpell        = objc.Sel("setContinuousSpellCheckingEnabled:")
	selSetAutoCorrect      = objc.Sel("setAutomaticSpellingCorrectionEnabled:")
	selSetSelectedRange    = objc.Sel("setSelectedRange:")
	selShouldChange        = objc.Sel("shouldChangeTextInRange:replacementString:")
	selBeginEditing        = objc.Sel("beginEditing")
	selEndEditing          = objc.Sel("endEditing")
	selReplaceChars        = objc.Sel("replaceCharactersInRange:withString:")
	selSetAttributesRange  = objc.Sel("setAttributes:range:")
	selTypingAttributes    = objc.Sel("typingAttributes")
	selDidChangeText       = objc.Sel("didChangeText")
	selBreakUndoCoalescing = objc.Sel("breakUndoCoalescing")
	selSetString           = objc.Sel("setString:")
	selRemoveAllActions    = objc.Sel("removeAllActions")
	selLengthOfBytes       = objc.Sel("lengthOfBytesUsingEncoding:")
	selLengthEd            = objc.Sel("length")
	selFrameEd             = objc.Sel("frame")
	selCurrentEvent        = objc.Sel("currentEvent")
	selTypes               = objc.Sel("types")
	selContainsObject      = objc.Sel("containsObject:")
	selFirstObjectCommon   = objc.Sel("firstObjectCommonWithArray:")
	selArrayWithObjects    = objc.Sel("arrayWithObjects:count:")
	selArrayByAdding       = objc.Sel("arrayByAddingObjectsFromArray:")
	selSRGBColor           = objc.Sel("colorWithSRGBRed:green:blue:alpha:")
	selAddTempAttr         = objc.Sel("addTemporaryAttribute:value:forCharacterRange:")
	selRemoveTempAttr      = objc.Sel("removeTemporaryAttribute:forCharacterRange:")
	selDefaultCenter       = objc.Sel("defaultCenter")
	selAddObserver         = objc.Sel("addObserver:selector:name:object:")
	selRemoveObserver      = objc.Sel("removeObserver:")
	selSetPostsFrame       = objc.Sel("setPostsFrameChangedNotifications:")
	selConvertRectFromScr  = objc.Sel("convertRectFromScreen:")
	selConvertRectFromView = objc.Sel("convertRect:fromView:")
	selSetDelegateEd       = objc.Sel("setDelegate:")
	selPlaceholderColor    = objc.Sel("placeholderTextColor")
	selUndo                = objc.Sel("undo:")
	selRedo                = objc.Sel("redo:")
	selUndoManager         = objc.Sel("undoManager")
	selUndoNow             = objc.Sel("undo")
	selRedoNow             = objc.Sel("redo")
	selCanUndo             = objc.Sel("canUndo")
	selCanRedo             = objc.Sel("canRedo")
	selReadSelectionPB     = objc.Sel("readSelectionFromPasteboard:")
	selReadablePBTypes     = objc.Sel("readablePasteboardTypes")
	selResignFirstResp     = objc.Sel("resignFirstResponder")
	selComplete            = objc.Sel("complete:")
	selMoveUp              = objc.Sel("moveUp:")
	selMoveDown            = objc.Sel("moveDown:")
	selInsertLineBreak     = objc.Sel("insertLineBreak:")
	selInsertNLIgnoring    = objc.Sel("insertNewlineIgnoringFieldEditor:")
	selTextDidChange       = objc.Sel("textDidChange:")
	selDidChangeSelection  = objc.Sel("textViewDidChangeSelection:")
	selShouldChangeDel     = objc.Sel("textView:shouldChangeTextInRange:replacementString:")
	selUndoManagerFor      = objc.Sel("undoManagerForTextView:")
	selFrameChanged        = objc.Sel("gnuiFrameChanged:")
	selSetMarkedText2      = objc.Sel("setMarkedText:selectedRange:")
	selExtraLineRect       = objc.Sel("extraLineFragmentRect")
	selLabelWithString     = objc.Sel("labelWithString:")
	selSetLineBreakMode    = objc.Sel("setLineBreakMode:")
	selSetAXElement        = objc.Sel("setAccessibilityElement:")
	selSetAXPlaceholder    = objc.Sel("setAccessibilityPlaceholderValue:")
	selAddSubviewRel       = objc.Sel("addSubview:positioned:relativeTo:")
	selSizeToFit           = objc.Sel("sizeToFit")
	selLineFragPadding     = objc.Sel("lineFragmentPadding")
	selHitTestEd           = objc.Sel("hitTest:")
)

// editorNative is the macOS half of an Editor.
type editorNative struct {
	w        *Window
	slot     int // its index in edTable
	host     HostID
	scroll   objc.ID // the host's view (owned by the host)
	tv       objc.ID // GNUITextView, the scroll view's document view (+1)
	del      objc.ID // GNUIEditorDelegate (+1)
	undo     objc.ID // NSUndoManager (+1)
	lm, tc   objc.ID
	lineH    float64 // points
	maxLines int
	insetY   float64
	enter    EnterPolicy
	dirtyH   bool    // the height is to be measured again (sync)
	frameW   float64 // the text view's width when the height was last measured
	quiet    bool    // SetText is setting the text: no EditorChanged
	ph       objc.ID // the placeholder label, a subview of the text view (0: none)
	phShown  bool
	insetX   float64
	inKey    bool // keyDown: is running
	keyMark  bool // the key being handled came while marked text was there
	released bool
}

func registerEditorClasses() {
	tv := objc.Define("GNUITextView", objc.Cls("NSTextView"), nil)
	tv.Method(selDoCommand, "v@::", edDoCommand)
	tv.Method(selKeyDown, "v@:@", edKeyDown)
	tv.Method(selReadSelectionPB, "c@:@", edReadSelection)
	tv.Method(selReadablePBTypes, "@@:", edReadableTypes)
	tv.Method(selResignFirstResp, "c@:", edResign)
	tv.Method(selUnmarkText, "v@:", edUnmark)
	tv.Method(selSetMarkedText, "v@:@{_NSRange=QQ}{_NSRange=QQ}", edSetMarked)
	tv.Method(selSetMarkedText2, "v@:@{_NSRange=QQ}", edSetMarked2)
	tv.Method(selUndo, "v@:@", edUndo)
	tv.Method(selRedo, "v@:@", edRedo)
	tv.Method(selValidateItem, "c@:@", edValidate)
	edTVClass, edTVDef = tv.Register(), tv

	ph := objc.Define("GNUIEditorPlaceholder", objc.Cls("NSTextField"), nil)
	ph.Method(selHitTestEd, "@@:{CGPoint=dd}", edPlaceholderHit)
	edPHClass = ph.Register()

	d := objc.Define("GNUIEditorDelegate", objc.Cls("NSObject"), []string{"NSTextViewDelegate"})
	d.Method(selTextDidChange, "v@:@", edTextDidChange)
	d.Method(selDidChangeSelection, "v@:@", edSelectionDidChange)
	d.Method(selShouldChangeDel, "c@:@{_NSRange=QQ}@", edShouldChange)
	d.Method(selUndoManagerFor, "@@:@", edUndoManager)
	d.Method(selFrameChanged, "v@:@", edFrameChanged)
	edDelClass, edDelDef = d.Register(), d

	clsNSScrollView, clsNSColor, clsNSFont = objc.Cls("NSScrollView"), objc.Cls("NSColor"), objc.Cls("NSFont")
	clsNSUndoManager, clsEdString, clsNSNote = objc.Cls("NSUndoManager"), objc.Cls("NSString"), objc.Cls("NSNotificationCenter")
	edApp = objc.Send(objc.Cls("NSApplication"), selSharedApplication)
	edEmpty = objc.Retain(objc.Str(""))
	edPlainType = objc.Retain(objc.Str("public.utf8-plain-text"))
	edFgAttr, edBgAttr = objc.Retain(objc.Str("NSColor")), objc.Retain(objc.Str("NSBackgroundColor")) // NSForegroundColorAttributeName, NSBackgroundColorAttributeName
	edFrameNote = objc.Retain(objc.Str("NSViewFrameDidChangeNotification"))
	types := &[...]objc.ID{objc.Str("public.png"), objc.Str("public.tiff"), objc.Str("public.jpeg"), objc.Str("public.heic")} // on the heap
	edImageTypes = objc.Retain(objc.ID(objc.F.Reset().Arg(uintptr(clsNSArray)).Arg(uintptr(selArrayWithObjects)).
		ArgPtr(unsafe.Pointer(&types[0])).Arg(uintptr(len(types))).Call(objc.MsgSend).R0()))
	if runtime.GOARCH == "amd64" {
		if h, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL); err == nil {
			edStret, _ = purego.Dlsym(h, "objc_msgSend_stret")
		}
	}
}

// NewEditor creates a native multi-line editor as a host of the window
// (HostView kind, focusable), hidden until SetHosts places it. Build its box
// with ui.Context.Host(e.Host(), …, ui.HostFocusable), Height points high.
// At a safe point, main thread; it allocates. It takes one of the window's
// MaxHosts hosts.
func (w *Window) NewEditor(o EditorOptions) (*Editor, error) {
	edOnce.Do(registerEditorClasses)
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
	e.w, e.slot, e.maxLen, e.enter = w, slot, o.MaxLen, o.Enter
	e.maxLines = o.MaxLines
	if e.maxLines <= 0 {
		e.maxLines = 6
	}
	e.insetX, e.insetY = float64(o.InsetX), float64(o.InsetY)
	size := float64(o.FontSize)
	if size <= 0 {
		size = 13
	}

	tv := objc.Send(edTVClass, selAlloc)
	if objc.SendBool1(tv, selRespondsTo, uintptr(selInitUsingTLM)) {
		tv = objc.Send1(tv, selInitUsingTLM, 0) // TextKit 1, a network the view owns
	} else {
		tv = objc.ID(objc.F.Reset().Arg(uintptr(tv)).Arg(uintptr(selInitWithFrame)).ArgRect(0, 0, 100, 20).Call(objc.MsgSend).R0())
	}
	e.tv = tv
	edTVDef.Index().Set(tv, uintptr(slot+1))
	e.lm, e.tc = objc.Send(tv, selLayoutManager), objc.Send(tv, selTextContainer)
	font := objc.ID(objc.F.Reset().Arg(uintptr(clsNSFont)).Arg(uintptr(selSystemFontOfSize)).ArgF64(size).Call(objc.MsgSend).R0())
	objc.Send1(tv, selSetFont, uintptr(font))
	objc.Send1(tv, selSetTextColor, uintptr(objc.Send(clsNSColor, selTextColor)))
	objc.Send1(tv, selSetRichText, 0)
	objc.Send1(tv, selSetImportsGraphics, 0)
	objc.Send1(tv, selSetAllowsUndo, 1)
	objc.Send1(tv, selSetDrawsBackground, 0)
	objc.Send1(tv, selSetContSpell, 1)
	objc.Send1(tv, selSetVResizable, 1)
	objc.Send1(tv, selSetHResizable, 0)
	objc.Send1(tv, selSetAutoresizing, 2) // NSViewWidthSizable
	objc.F.Reset().Arg(uintptr(tv)).Arg(uintptr(selSetMinSize)).ArgPoint(0, 0).Call(objc.MsgSend)
	objc.F.Reset().Arg(uintptr(tv)).Arg(uintptr(selSetMaxSize)).ArgPoint(fltMax, fltMax).Call(objc.MsgSend)
	objc.F.Reset().Arg(uintptr(tv)).Arg(uintptr(selSetContainerInset)).ArgPoint(float64(o.InsetX), e.insetY).Call(objc.MsgSend)
	objc.Send1(e.tc, selSetWidthTracks, 1)
	objc.F.Reset().Arg(uintptr(e.tc)).Arg(uintptr(selSetContainerSize)).ArgPoint(fltMax, fltMax).Call(objc.MsgSend)
	e.lineH = objc.F.Reset().Arg(uintptr(e.lm)).Arg(uintptr(selDefaultLineHeight)).Arg(uintptr(font)).Call(objc.MsgSend).F64(0)
	if o.Placeholder != "" {
		e.setPlaceholder(o.Placeholder, font)
	}

	e.undo = objc.Send(objc.Send(clsNSUndoManager, selAlloc), selInitPlain)
	e.del = objc.Send(objc.Send(edDelClass, selAlloc), selInitPlain)
	edDelDef.Index().Set(e.del, uintptr(slot+1))
	objc.Send1(tv, selSetDelegateEd, uintptr(e.del))
	objc.Send1(tv, selSetPostsFrame, 1)
	objc.Send4(objc.Send(clsNSNote, selDefaultCenter), selAddObserver, uintptr(e.del), uintptr(selFrameChanged), uintptr(edFrameNote), uintptr(tv))

	sv := newViewOf(clsNSScrollView, 0, 0, 100, 20)
	objc.Send1(sv, selSetHasVScroller, 1)
	objc.Send1(sv, selSetAutohides, 1)
	objc.Send1(sv, selSetDrawsBackground, 0)
	objc.Send1(sv, selSetBorderType, 0) // NSNoBorder
	objc.Send1(sv, selSetDocumentView, uintptr(tv))
	// The scroll view owns the text view; the editor keeps its own +1 from
	// init, so that the view outlives a window that closes before Release.
	e.scroll = sv
	id, err := w.NewHost(HostView, HostOptions{View: uintptr(sv), Focusable: true})
	objc.Send(sv, selReleaseObj) // the host owns it (or nobody, on failure)
	if err != nil {
		objc.Send1(objc.Send(clsNSNote, selDefaultCenter), selRemoveObserver, uintptr(e.del))
		objc.Send1(tv, selSetDelegateEd, 0)
		objc.Release(tv)
		objc.Release(e.del)
		objc.Release(e.undo)
		return nil, err
	}
	e.host = id
	edTable[slot] = e
	e.frameW = objc.SendRect(tv, selFrameEd).W
	e.layoutPlaceholder()
	e.Reserve(256)
	e.copyText()
	e.dirtyH = true
	e.sync()
	return e, nil
}

const fltMax = 3.4028234663852886e38

// setPlaceholder gives the text view its placeholder, through public API: a
// label in the placeholder colour and the text's font, under the text view's
// own drawing (its first subview, below the caret), where the first line's
// text starts, shown while the text is empty (with no composition), passing
// every click to the text view; and the accessibility placeholder value.
func (e *Editor) setPlaceholder(s string, font objc.ID) {
	str := objc.Str(s)
	l := objc.Send1(edPHClass, selLabelWithString, uintptr(str))
	objc.Send1(l, selSetFont, uintptr(font))
	objc.Send1(l, selSetTextColor, uintptr(objc.Send(clsNSColor, selPlaceholderColor)))
	objc.Send1(l, selSetLineBreakMode, 4) // NSLineBreakByTruncatingTail
	objc.Send1(l, selSetAXElement, 0)     // the text view reports it (placeholder value)
	objc.Send(l, selSizeToFit)
	objc.Send3(e.tv, selAddSubviewRel, uintptr(l), uintptr(^uintptr(0)) /* NSWindowBelow */, 0)
	objc.Send1(e.tv, selSetAXPlaceholder, uintptr(str))
	e.ph, e.phShown = l, true
	e.layoutPlaceholder()
}

// labelInset is the space an NSTextField label keeps between its frame and
// its text, in points (its cell's padding).
const labelInset = 2

// layoutPlaceholder puts the placeholder where the text view's first line
// starts and as wide as the line.
func (e *Editor) layoutPlaceholder() {
	if e.ph == 0 {
		return
	}
	pad := objc.SendF64(e.tc, selLineFragPadding)
	f := objc.SendRect(e.tv, selFrameEd)
	ph := objc.SendRect(e.ph, selFrameEd)
	x := e.insetX + pad - labelInset
	objc.F.Reset().Arg(uintptr(e.ph)).Arg(uintptr(selSetFrame)).ArgRect(x, e.insetY, max(f.W-x-e.insetX-pad+labelInset, 0), ph.H).Call(objc.MsgSend)
}

// showPlaceholder shows the placeholder while the text is empty.
func (e *Editor) showPlaceholder() {
	if show := len(e.text) == 0 && !e.marked; e.ph != 0 && show != e.phShown {
		e.phShown = show
		objc.Send1(e.ph, selSetHidden, uintptr(boolU(!show)))
	}
}

// edPlaceholderHit is the placeholder's hitTest:: never, so that a click
// on it is the text view's.
func edPlaceholderHit(c *ffi.CB) { c.Ret(0) }

// Host is the editor's host, for ui.Context.Host.
func (e *Editor) Host() HostID { return e.host }

// View is the editor's NSTextView* (GNUITextView), for configuration the
// editor does not cover and for the accessibility bridge, which reports the
// host's views as children of its box.
func (e *Editor) View() uintptr { return uintptr(e.tv) }

// Release removes the editor and its host, before or after the window
// closes. At a safe point, main thread.
func (e *Editor) Release() {
	if e.released {
		return
	}
	e.released = true
	objc.Send1(objc.Send(clsNSNote, selDefaultCenter), selRemoveObserver, uintptr(e.del))
	objc.Send1(e.tv, selSetDelegateEd, 0)
	edTVDef.Index().Set(e.tv, 0)
	e.w.ReleaseHost(e.host)
	objc.Release(e.tv)
	objc.Release(e.del)
	objc.Release(e.undo)
	edTable[e.slot] = nil
}

// Focused reports whether the editor's text view holds the keyboard focus.
func (e *Editor) Focused() bool { return !e.released && e.w.hosts.focus == int(e.host&0xFF) }

// Focus gives the editor the keyboard focus (EvHostFocus follows).
func (e *Editor) Focus() { e.w.FocusHost(e.host) }

// SetAutocorrect turns AppKit's automatic spelling correction on or off for
// the editor (by default it follows the user's setting).
func (e *Editor) SetAutocorrect(on bool) { objc.Send1(e.tv, selSetAutoCorrect, uintptr(boolU(on))) }

// SetText replaces the whole text (restoring a draft, editing a sent
// message) and puts the caret at its end. It clears the undo stack, as
// opening a document does, discards any marked text and fires no
// EditorChanged. It allocates an NSString.
func (e *Editor) SetText(b []byte) error {
	if e.maxLen > 0 && len(b) > e.maxLen {
		return ErrTooLong
	}
	if !utf8.Valid(b) {
		return ErrNotBoundary
	}
	str := edNSString(b)
	e.quiet = true // the composition's end and the new text are one quiet change
	if objc.SendBool(e.tv, selHasMarkedText) {
		if ctx := objc.Send(e.tv, selInputContext); ctx != 0 {
			objc.Send(ctx, selDiscardMarked) // the input method forgets it too
		}
		objc.Send(e.tv, selUnmarkText)
	}
	objc.Send1(e.tv, selSetString, uintptr(str))
	e.quiet = false
	objc.Release(str)
	objc.Send(e.undo, selRemoveAllActions)
	e.copyText()
	// A change of the replaced text not taken yet is moot: the app set the
	// text it now reads.
	e.pend &^= EditorChanged | EditorComposing
	e.dirtyH = true
	e.wake()
	return e.SetSelection(TextSelection{len(e.text), len(e.text)})
}

// SetSelection selects s (bytes; Anchor == Focus for a caret). AppKit widens
// a selection that splits a character (a grapheme cluster: an emoji with a
// modifier, a letter and its combining marks) to the whole character;
// Selection reports what it selected.
func (e *Editor) SetSelection(s TextSelection) error {
	r := s.Range()
	loc, n, err := e.rangeU16(r)
	if err != nil {
		return err
	}
	objc.Send2(e.tv, selSetSelectedRange, uintptr(loc), uintptr(n))
	e.readSelection() // AppKit may widen it to whole characters (grapheme clusters)
	if r = e.sel.Range(); s.Focus < s.Anchor {
		e.sel = TextSelection{r.End, r.Start}
	} else {
		e.sel = TextSelection{r.Start, r.End}
	}
	return nil
}

// Replace replaces bytes r with text and selects sel (bytes of the new text),
// as a user edit: one undo step, the delegate's checks (the maximum length)
// applied, EditorChanged fired. It refuses while marked text is there
// (ErrComposing), off scalar boundaries (ErrNotBoundary) and over MaxLen
// (ErrTooLong). It allocates an NSString.
func (e *Editor) Replace(r TextRange, text []byte, sel TextRange) error {
	if err := e.checkReplace(r, text, sel); err != nil {
		return err
	}
	if objc.SendBool(e.tv, selHasMarkedText) {
		return ErrComposing
	}
	loc, n, _ := e.rangeU16(r)
	str := edNSString(text)
	defer objc.Release(str)
	tv := e.tv
	if !objc.F.Reset().Arg(uintptr(tv)).Arg(uintptr(selShouldChange)).Arg(uintptr(loc)).Arg(uintptr(n)).Arg(uintptr(str)).Call(objc.MsgSend).Bool() {
		return ErrTooLong
	}
	objc.Send(tv, selBreakUndoCoalescing)
	ts := objc.Send(tv, selTextStorage)
	objc.Send(ts, selBeginEditing)
	objc.Send3(ts, selReplaceChars, uintptr(loc), uintptr(n), uintptr(str))
	objc.Send3(ts, selSetAttributesRange, uintptr(objc.Send(tv, selTypingAttributes)), uintptr(loc), uintptr(objc.Send(str, selLengthEd)))
	objc.Send(ts, selEndEditing)
	objc.Send(tv, selDidChangeText) // textDidChange: copies the text
	objc.Send(tv, selBreakUndoCoalescing)
	return e.SetSelection(TextSelection{sel.Start, sel.End})
}

// SetSpans styles the text's ranges (mentions) with temporary attributes,
// replacing the spans set before: display-only, outside the undo stack. Call
// it again after EditorChanged, with the app's spans for the new text.
func (e *Editor) SetSpans(spans []TextSpan) error {
	all := objc.Send(objc.Send(e.tv, selTextStorage), selLengthEd)
	objc.Send3(e.lm, selRemoveTempAttr, uintptr(edFgAttr), 0, uintptr(all))
	objc.Send3(e.lm, selRemoveTempAttr, uintptr(edBgAttr), 0, uintptr(all))
	for _, s := range spans {
		loc, n, err := e.rangeU16(s.Range)
		if err != nil {
			return err
		}
		if s.Color != 0 {
			objc.Send4(e.lm, selAddTempAttr, uintptr(edFgAttr), uintptr(nsColor(s.Color)), uintptr(loc), uintptr(n))
		}
		if s.Background != 0 {
			objc.Send4(e.lm, selAddTempAttr, uintptr(edBgAttr), uintptr(nsColor(s.Background)), uintptr(loc), uintptr(n))
		}
	}
	return nil
}

func nsColor(c uint32) objc.ID {
	f := func(s uint) float64 { return float64(c>>s&0xFF) / 255 }
	return objc.ID(objc.F.Reset().Arg(uintptr(clsNSColor)).Arg(uintptr(selSRGBColor)).
		ArgF64(f(24)).ArgF64(f(16)).ArgF64(f(8)).ArgF64(f(0)).Call(objc.MsgSend).R0())
}

// edNSString is a new NSString (+1) of UTF-8 b.
func edNSString(b []byte) objc.ID {
	s := objc.Send(clsEdString, selAlloc)
	var p unsafe.Pointer
	if len(b) > 0 {
		p = unsafe.Pointer(&b[0])
	}
	return objc.ID(objc.F.Reset().Arg(uintptr(s)).Arg(uintptr(selInitBytesStr)).ArgPtr(p).Arg(uintptr(len(b))).Arg(4).Call(objc.MsgSend).R0())
}

// CaretRect is the caret, or the first line of the selection, in pixels of
// the window's content (top left, as ui's boxes), from the text view's
// firstRectForCharacterRange:actualRange: (what an input method places its
// candidates by). false while the editor is not on screen.
func (e *Editor) CaretRect() ([4]float32, bool) {
	objc.F.Reset().Arg(uintptr(e.tv)).Arg(uintptr(selSelectedRange)).Call(objc.MsgSend)
	loc, n := objc.F.R0(), objc.F.R1()
	r := e.rectCall(func(f *ffi.Frame) *ffi.Frame {
		return f.Arg(uintptr(e.tv)).Arg(uintptr(selFirstRect)).Arg(loc).Arg(n).Arg(0)
	})
	if r.W == 0 && r.H == 0 && r.X == 0 && r.Y == 0 {
		return [4]float32{}, false
	}
	r = e.rectCall(func(f *ffi.Frame) *ffi.Frame {
		return f.Arg(uintptr(e.w.win)).Arg(uintptr(selConvertRectFromScr)).ArgRect(r.X, r.Y, r.W, r.H)
	})
	r = e.rectCall(func(f *ffi.Frame) *ffi.Frame {
		return f.Arg(uintptr(e.w.content)).Arg(uintptr(selConvertRectFromView)).ArgRect(r.X, r.Y, r.W, r.H).Arg(0)
	})
	s := float64(e.w.scale)
	if s <= 0 {
		s = 1
	}
	return [4]float32{float32(r.X * s), float32(r.Y * s), float32((r.X + r.W) * s), float32((r.Y + r.H) * s)}, true
}

var edRectOut = new(objc.Rect)

// rectCall sends the message args builds, which returns an NSRect: in d0-d3
// on arm64, through objc_msgSend_stret's hidden pointer on amd64. Not on a
// per-frame path (args is a closure).
func (e *Editor) rectCall(args func(f *ffi.Frame) *ffi.Frame) objc.Rect {
	if runtime.GOARCH == "arm64" {
		f := args(objc.F.Reset()).Call(objc.MsgSend)
		return objc.Rect{X: f.F64(0), Y: f.F64(1), W: f.F64(2), H: f.F64(3)}
	}
	if edStret == 0 {
		return objc.Rect{}
	}
	args(objc.F.Reset().SetIndirectResult(unsafe.Pointer(edRectOut))).Call(edStret)
	return *edRectOut
}

// sync measures the height again when the text or the width changed.
func (e *Editor) sync() {
	if !e.dirtyH || e.released {
		return
	}
	e.dirtyH = false
	objc.Send1(e.lm, selEnsureLayout, uintptr(e.tc))
	used := e.rectCall(func(f *ffi.Frame) *ffi.Frame {
		return f.Arg(uintptr(e.lm)).Arg(uintptr(selUsedRect)).Arg(uintptr(e.tc))
	})
	// The empty line after a trailing newline is the extra line fragment;
	// the used rect includes it on the systems seen, the union keeps it if not.
	extra := e.rectCall(func(f *ffi.Frame) *ffi.Frame { return f.Arg(uintptr(e.lm)).Arg(uintptr(selExtraLineRect)) })
	bottom := used.Y + used.H
	if extra.H > 0 {
		bottom = max(bottom, extra.Y+extra.H)
	}
	h := min(max(bottom, e.lineH), float64(e.maxLines)*e.lineH) + 2*e.insetY
	if hf := float32(h); hf != e.height {
		e.height = hf
		e.pend |= EditorHeightChanged
	}
}

// wake queues one EvHostEdit until the signals are taken, and arms a frame:
// inside a nested loop (the correction bubble's) the app's loop does not
// run, and the frame's tick draws through the frame handler (linkTick).
func (e *Editor) wake() {
	if !e.woken {
		e.woken = true
		e.w.q.push(Event{Kind: EvHostEdit, Tag: uint64(e.host), Time: Now()})
		e.w.RequestFrame()
	}
}

// copyText copies the text view's text into the buffer, rebuilds the map,
// and reads the selection and the marked text.
func (e *Editor) copyText() {
	str := objc.Send(objc.Send(e.tv, selTextStorage), selString)
	e.text = objc.AppendUTF8(e.text[:0], str)
	e.m.build(e.text)
	e.readSelection()
	if e.marked = objc.SendBool(e.tv, selHasMarkedText); e.marked {
		objc.F.Reset().Arg(uintptr(e.tv)).Arg(uintptr(selMarkedRange)).Call(objc.MsgSend)
		loc, n := int(objc.F.R0()), int(objc.F.R1())
		e.mr = TextRange{e.m.toByte(e.text, loc), e.m.toByte(e.text, loc+n)}
	} else {
		e.mr = TextRange{}
	}
	e.showPlaceholder()
}

// readSelection reads the selected range; the anchor stays where it was when
// it is one of the new ends (a selection extended from it).
func (e *Editor) readSelection() {
	objc.F.Reset().Arg(uintptr(e.tv)).Arg(uintptr(selSelectedRange)).Call(objc.MsgSend)
	loc, n := int(objc.F.R0()), int(objc.F.R1())
	lo, hi := e.m.toByte(e.text, loc), e.m.toByte(e.text, loc+n)
	if lo != hi && e.sel.Anchor == hi {
		e.sel = TextSelection{hi, lo}
	} else {
		e.sel = TextSelection{lo, hi}
	}
}

// editorOf is the editor of a GNUITextView, delegateOf that of a
// GNUIEditorDelegate.
func editorOf(self objc.ID) *Editor   { return editorAt(edTVDef.Index().Get(self)) }
func delegateOf(self objc.ID) *Editor { return editorAt(edDelDef.Index().Get(self)) }

func editorAt(x uintptr) *Editor {
	i := int(x) - 1
	if i < 0 || i >= MaxHosts {
		return nil
	}
	return edTable[i]
}

// edTextDidChange is the delegate's textDidChange:.
func edTextDidChange(c *ffi.CB) {
	if e := delegateOf(objc.ID(c.Int(0))); e != nil {
		if e.quiet {
			e.copyText()
			return
		}
		e.compositionChanged()
	}
}

// edSelectionDidChange is the delegate's textViewDidChangeSelection:. While
// the buffer lags the text storage (the selection moves before textDidChange:
// during an edit), textDidChange: reads it.
func edSelectionDidChange(c *ffi.CB) {
	e := delegateOf(objc.ID(c.Int(0)))
	if e == nil || e.quiet {
		return
	}
	if int(objc.Send(objc.Send(e.tv, selTextStorage), selLengthEd)) != e.m.n16 {
		return
	}
	prev := e.sel
	e.readSelection()
	if e.sel != prev {
		e.pend |= EditorSelected
		e.wake()
	}
}

// edShouldChange is the delegate's
// textView:shouldChangeTextInRange:replacementString:: the maximum length.
func edShouldChange(c *ffi.CB) {
	e := delegateOf(objc.ID(c.Int(0)))
	repl := objc.ID(c.Int(5))
	if e == nil || e.maxLen <= 0 || repl == 0 {
		c.RetBool(true)
		return
	}
	loc, n := int(c.Int(3)), int(c.Int(4))
	gone := e.m.toByte(e.text, loc+n) - e.m.toByte(e.text, loc)
	add := int(objc.Send1(repl, selLengthOfBytes, 4 /* NSUTF8StringEncoding */))
	c.RetBool(len(e.text)-gone+add <= e.maxLen)
}

func edUndoManager(c *ffi.CB) {
	if e := delegateOf(objc.ID(c.Int(0))); e != nil {
		c.Ret(uintptr(e.undo))
		return
	}
	c.Ret(0)
}

// edFrameChanged: the text view's frame changed; a new width wraps the text
// again, so the height is measured again.
func edFrameChanged(c *ffi.CB) {
	e := delegateOf(objc.ID(c.Int(0)))
	if e == nil {
		return
	}
	f := objc.SendRect(e.tv, selFrameEd)
	if f.W != e.frameW {
		e.frameW = f.W
		e.layoutPlaceholder()
		e.dirtyH = true
		e.wake()
	}
}

// currentMods are the modifiers of the event AppKit is handling.
func currentMods() Mods {
	ev := objc.Send(edApp, selCurrentEvent)
	if ev == 0 {
		return 0
	}
	return mods(uintptr(objc.Send(ev, selModifierFlags)))
}

// command is the editor's part of doCommandBySelector:: true if it took sel.
//
// A key that came while marked text was there belongs to the composition,
// even when the input method commits it (unmarkText:, insertText:) before it
// hands the same key back as a command: the Enter that commits, the Tab or
// the Escape that ends a composition does nothing else.
func (e *Editor) command(sel objc.SEL) bool {
	marked := objc.SendBool(e.tv, selHasMarkedText)
	composed := marked || e.inKey && e.keyMark
	switch sel {
	case selInsertNewline, selInsertLineBreak, selInsertNLIgnoring:
		switch {
		case composed: // Enter commits the composition and sends nothing
			if marked {
				objc.Send(e.tv, selUnmarkText)
			}
		case e.compl:
			e.toApp(CmdAccept)
		case sel == selInsertNewline && e.sends(currentMods()):
			e.pend |= EditorSubmitted
			e.wake()
		default:
			return false
		}
		return true
	case selInsertTab, selInsertBacktab:
		switch {
		case composed:
			if marked {
				objc.Send(e.tv, selUnmarkText)
			}
		case e.compl && sel == selInsertTab && currentMods()&ModShift == 0:
			e.toApp(CmdTab)
		case e.compl:
			e.toApp(CmdBacktab)
		case sel == selInsertTab && currentMods()&ModShift == 0: // a Shift+Tab whose event types a tab is a backtab
			e.w.hostCommand(int(e.host&0xFF)-1, KeyTab, 0)
		default:
			e.w.hostCommand(int(e.host&0xFF)-1, KeyTab, ModShift)
		}
		return true
	case selCancelOperation, selComplete:
		switch {
		case marked: // Escape abandons the composition
			argRangeAfter5(objc.F.Reset().Arg(uintptr(e.tv)).Arg(uintptr(selSetMarkedText)).Arg(uintptr(edEmpty)).
				Arg(0).Arg(0), uintptr(nsNotFound), 0).Call(objc.MsgSend)
			objc.Send(e.tv, selUnmarkText)
		case composed: // the input method ended it already
		case e.compl:
			e.toApp(CmdCancel)
		default:
			e.pend |= EditorCancelled
			e.wake()
			e.w.hostCommand(int(e.host&0xFF)-1, KeyEscape, 0)
		}
		return true
	case selMoveUp, selMoveDown:
		if e.compl && !composed {
			if sel == selMoveUp {
				e.toApp(CmdUp)
			} else {
				e.toApp(CmdDown)
			}
			return true
		}
	}
	return false
}

// sends reports whether Enter with modifiers m submits.
func (e *Editor) sends(m Mods) bool {
	if e.enter == CmdEnterSends {
		return m&ModSuper != 0
	}
	return m&(ModShift|ModAlt|ModCtrl) == 0
}

func (e *Editor) toApp(c EditorCommand) {
	e.pushCommand(c)
	e.wake()
}

// edDoCommand is GNUITextView's doCommandBySelector:.
func edDoCommand(c *ffi.CB) {
	self, sel := objc.ID(c.Int(0)), objc.SEL(c.Int(2))
	if e := editorOf(self); e != nil && e.command(sel) {
		return
	}
	objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selDoCommand).Arg(uintptr(sel)).Call(objc.MsgSendSuper)
}

// edKeyDown is GNUITextView's keyDown:: ⌘Enter, which no key binding names,
// submits (or accepts the completion); everything else is NSTextView's,
// with the composition state at the key's arrival kept for command.
func edKeyDown(c *ffi.CB) {
	self, ev := objc.ID(c.Int(0)), objc.ID(c.Int(2))
	e := editorOf(self)
	if e == nil {
		objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selKeyDown).Arg(uintptr(ev)).Call(objc.MsgSendSuper)
		return
	}
	marked := objc.SendBool(self, selHasMarkedText)
	code := uint32(objc.Send(ev, selKeyCode)) & 0xFFFF
	m := mods(uintptr(objc.Send(ev, selModifierFlags)))
	if (code == 0x24 || code == 0x4C) && m&ModSuper != 0 && !marked {
		if e.compl {
			e.toApp(CmdAccept)
		} else {
			e.pend |= EditorSubmitted
			e.wake()
		}
		return
	}
	inKey, keyMark := e.inKey, e.keyMark
	e.inKey, e.keyMark = true, marked
	if edKeyHook != nil {
		edKeyHook(e)
	}
	objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selKeyDown).Arg(uintptr(ev)).Call(objc.MsgSendSuper)
	e.inKey, e.keyMark = inKey, keyMark
}

// edKeyHook, when set (tests), runs in keyDown: before NSTextView takes the
// key: where an input method acts on it.
var edKeyHook func(e *Editor)

// edReadSelection is GNUITextView's readSelectionFromPasteboard:: an image
// with no plain text is the app's; anything else is NSTextView's (plain
// text only: the view is not rich).
func edReadSelection(c *ffi.CB) {
	self, pb := objc.ID(c.Int(0)), objc.ID(c.Int(2))
	if e := editorOf(self); e != nil {
		types := objc.Send(pb, selTypes)
		if objc.Send1(types, selFirstObjectCommon, uintptr(edImageTypes)) != 0 && !objc.SendBool1(types, selContainsObject, uintptr(edPlainType)) {
			e.pend |= EditorPastedImage
			e.wake()
			c.RetBool(true)
			return
		}
	}
	c.RetBool(objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selReadSelectionPB).Arg(uintptr(pb)).Call(objc.MsgSendSuper).Bool())
}

// edReadableTypes is GNUITextView's readablePasteboardTypes: NSTextView's and
// the image types, so that Paste is enabled for an image.
func edReadableTypes(c *ffi.CB) {
	self := objc.ID(c.Int(0))
	sup := objc.ID(objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selReadablePBTypes).Call(objc.MsgSendSuper).R0())
	c.Ret(uintptr(objc.Send1(sup, selArrayByAdding, uintptr(edImageTypes))))
}

// edResign is GNUITextView's resignFirstResponder: losing the keyboard
// focus ends the app's completion.
func edResign(c *ffi.CB) {
	self := objc.ID(c.Int(0))
	ok := objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selResignFirstResp).Call(objc.MsgSendSuper).Bool()
	if e := editorOf(self); ok && e != nil && e.compl {
		e.SetCompletionActive(false)
		e.pend |= EditorCancelled
		e.wake()
	}
	c.RetBool(ok)
}

// edUnmark is GNUITextView's unmarkText: the composition becomes text, which
// may not change the string (so no textDidChange:): EditorChanged without
// EditorComposing.
func edUnmark(c *ffi.CB) {
	self := objc.ID(c.Int(0))
	e := editorOf(self)
	was := e != nil && objc.SendBool(self, selHasMarkedText)
	objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selUnmarkText).Call(objc.MsgSendSuper)
	if was {
		e.compositionChanged()
	}
}

// edSetMarked is GNUITextView's setMarkedText:selectedRange:replacementRange:,
// and edSetMarked2 the older setMarkedText:selectedRange:: an input method's
// composition changed, which NSTextView reports with no textDidChange:.
func edSetMarked(c *ffi.CB) {
	self := objc.ID(c.Int(0))
	str, sl, sn := c.Int(2), c.Int(3), c.Int(4)
	rl, rn := rangeAfter5(c)
	f := objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selSetMarkedText).Arg(str).Arg(sl).Arg(sn)
	argRangeAfter5(f, rl, rn).Call(objc.MsgSendSuper)
	if e := editorOf(self); e != nil {
		e.compositionChanged()
	}
}

func edSetMarked2(c *ffi.CB) {
	self := objc.ID(c.Int(0))
	objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selSetMarkedText2).Arg(c.Int(2)).Arg(c.Int(3)).Arg(c.Int(4)).Call(objc.MsgSendSuper)
	if e := editorOf(self); e != nil {
		e.compositionChanged()
	}
}

// compositionChanged copies the text with its new composition: EditorChanged,
// with EditorComposing while marked text remains; marked text ends the app's
// completion.
func (e *Editor) compositionChanged() {
	if e.quiet {
		return
	}
	e.copyText()
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
	e.wake()
}

// edUndo and edRedo are GNUITextView's undo: and redo:, the Edit menu's
// items: the editor's own undo manager (the window's would be asked
// otherwise).
func edUndo(c *ffi.CB) {
	if e := editorOf(objc.ID(c.Int(0))); e != nil && objc.SendBool(e.undo, selCanUndo) {
		objc.Send(e.undo, selUndoNow)
	}
}

func edRedo(c *ffi.CB) {
	if e := editorOf(objc.ID(c.Int(0))); e != nil && objc.SendBool(e.undo, selCanRedo) {
		objc.Send(e.undo, selRedoNow)
	}
}

// edValidate is GNUITextView's validateMenuItem:: Undo and Redo by
// the editor's undo manager, the rest NSTextView's.
func edValidate(c *ffi.CB) {
	self, item := objc.ID(c.Int(0)), objc.ID(c.Int(2))
	if e := editorOf(self); e != nil && objc.SendBool1(item, selRespondsTo, uintptr(selAction)) {
		switch objc.SEL(objc.Send(item, selAction)) {
		case selUndo:
			c.RetBool(objc.SendBool(e.undo, selCanUndo))
			return
		case selRedo:
			c.RetBool(objc.SendBool(e.undo, selCanRedo))
			return
		}
	}
	c.RetBool(objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selValidateItem).Arg(uintptr(item)).Call(objc.MsgSendSuper).Bool())
}
