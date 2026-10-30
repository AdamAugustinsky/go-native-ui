//go:build darwin

package platform

import (
	"slices"
	"sync"
	"unicode/utf8"
	"unsafe"

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
//   - A paste with files (a Finder copy, which has their names as text
//     too) is the app's (readSelectionFromPasteboard:, and
//     pasteAsPlainText:, which does not call it): EditorPastedFiles, nothing
//     inserted; so is one with an image and no plain text:
//     EditorPastedImage.
//   - With SetEdgeArrows on, doCommandBySelector: reports moveUp: on the
//     first line and moveDown: on the last (EditorEdgeUp, EditorEdgeDown)
//     and lets the text view move the caret.
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
	edImageTypes           objc.ID           // NSArray of the image types a paste routes to the app
	edFileTypes            objc.ID           // NSArray of the file types a paste routes to the app
	edPlainType            objc.ID
	edFgAttr, edBgAttr     objc.ID
	edFrameNote            objc.ID
	clsNSScrollView        objc.ID
	clsNSColor, clsNSFont  objc.ID
	clsNSUndoManager       objc.ID
	clsEdString, clsNSNote objc.ID

	selInitUsingTLM        = objc.SelZ("initUsingTextLayoutManager:\x00")
	selSetDocumentView     = objc.SelZ("setDocumentView:\x00")
	selDocumentView        = objc.SelZ("documentView\x00")
	selSetHasVScroller     = objc.SelZ("setHasVerticalScroller:\x00")
	selSetAutohides        = objc.SelZ("setAutohidesScrollers:\x00")
	selSetDrawsBackground  = objc.SelZ("setDrawsBackground:\x00")
	selSetBorderType       = objc.SelZ("setBorderType:\x00")
	selSetMinSize          = objc.SelZ("setMinSize:\x00")
	selSetMaxSize          = objc.SelZ("setMaxSize:\x00")
	selSetVResizable       = objc.SelZ("setVerticallyResizable:\x00")
	selSetHResizable       = objc.SelZ("setHorizontallyResizable:\x00")
	selSetRichText         = objc.SelZ("setRichText:\x00")
	selSetEditable         = objc.SelZ("setEditable:\x00")
	selSetImportsGraphics  = objc.SelZ("setImportsGraphics:\x00")
	selSetAllowsUndo       = objc.SelZ("setAllowsUndo:\x00")
	selSetFont             = objc.SelZ("setFont:\x00")
	selSetTextColor        = objc.SelZ("setTextColor:\x00")
	selTextColor           = objc.SelZ("textColor\x00")
	selSystemFontOfSize    = objc.SelZ("systemFontOfSize:\x00")
	selSetContainerInset   = objc.SelZ("setTextContainerInset:\x00")
	selTextContainer       = objc.SelZ("textContainer\x00")
	selLayoutManager       = objc.SelZ("layoutManager\x00")
	selTextStorage         = objc.SelZ("textStorage\x00")
	selSetWidthTracks      = objc.SelZ("setWidthTracksTextView:\x00")
	selSetContainerSize    = objc.SelZ("setContainerSize:\x00")
	selDefaultLineHeight   = objc.SelZ("defaultLineHeightForFont:\x00")
	selEnsureLayout        = objc.SelZ("ensureLayoutForTextContainer:\x00")
	selUsedRect            = objc.SelZ("usedRectForTextContainer:\x00")
	selSetContSpell        = objc.SelZ("setContinuousSpellCheckingEnabled:\x00")
	selSetAutoCorrect      = objc.SelZ("setAutomaticSpellingCorrectionEnabled:\x00")
	selSetSelectedRange    = objc.SelZ("setSelectedRange:\x00")
	selShouldChange        = objc.SelZ("shouldChangeTextInRange:replacementString:\x00")
	selBeginEditing        = objc.SelZ("beginEditing\x00")
	selEndEditing          = objc.SelZ("endEditing\x00")
	selReplaceChars        = objc.SelZ("replaceCharactersInRange:withString:\x00")
	selSetAttributesRange  = objc.SelZ("setAttributes:range:\x00")
	selTypingAttributes    = objc.SelZ("typingAttributes\x00")
	selDidChangeText       = objc.SelZ("didChangeText\x00")
	selBreakUndoCoalescing = objc.SelZ("breakUndoCoalescing\x00")
	selSetString           = objc.SelZ("setString:\x00")
	selRemoveAllActions    = objc.SelZ("removeAllActions\x00")
	selLengthOfBytes       = objc.SelZ("lengthOfBytesUsingEncoding:\x00")
	selLengthEd            = objc.SelZ("length\x00")
	selFrameEd             = objc.SelZ("frame\x00")
	selCurrentEvent        = objc.SelZ("currentEvent\x00")
	selTypes               = objc.SelZ("types\x00")
	selContainsObject      = objc.SelZ("containsObject:\x00")
	selFirstObjectCommon   = objc.SelZ("firstObjectCommonWithArray:\x00")
	selArrayWithObjects    = objc.SelZ("arrayWithObjects:count:\x00")
	selArrayByAdding       = objc.SelZ("arrayByAddingObjectsFromArray:\x00")
	selSRGBColor           = objc.SelZ("colorWithSRGBRed:green:blue:alpha:\x00")
	selAddTempAttr         = objc.SelZ("addTemporaryAttribute:value:forCharacterRange:\x00")
	selRemoveTempAttr      = objc.SelZ("removeTemporaryAttribute:forCharacterRange:\x00")
	selDefaultCenter       = objc.SelZ("defaultCenter\x00")
	selAddObserver         = objc.SelZ("addObserver:selector:name:object:\x00")
	selRemoveObserver      = objc.SelZ("removeObserver:\x00")
	selSetPostsFrame       = objc.SelZ("setPostsFrameChangedNotifications:\x00")
	selConvertRectFromScr  = objc.SelZ("convertRectFromScreen:\x00")
	selConvertRectFromView = objc.SelZ("convertRect:fromView:\x00")
	selSetDelegateEd       = objc.SelZ("setDelegate:\x00")
	selPlaceholderColor    = objc.SelZ("placeholderTextColor\x00")
	selUndo                = objc.SelZ("undo:\x00")
	selRedo                = objc.SelZ("redo:\x00")
	selUndoManager         = objc.SelZ("undoManager\x00")
	selUndoNow             = objc.SelZ("undo\x00")
	selRedoNow             = objc.SelZ("redo\x00")
	selCanUndo             = objc.SelZ("canUndo\x00")
	selCanRedo             = objc.SelZ("canRedo\x00")
	selReadSelectionPB     = objc.SelZ("readSelectionFromPasteboard:\x00")
	selReadablePBTypes     = objc.SelZ("readablePasteboardTypes\x00")
	selResignFirstResp     = objc.SelZ("resignFirstResponder\x00")
	selComplete            = objc.SelZ("complete:\x00")
	selMoveUp              = objc.SelZ("moveUp:\x00")
	selMoveDown            = objc.SelZ("moveDown:\x00")
	selInsertLineBreak     = objc.SelZ("insertLineBreak:\x00")
	selInsertNLIgnoring    = objc.SelZ("insertNewlineIgnoringFieldEditor:\x00")
	selStorageEdited       = objc.SelZ("textStorage:didProcessEditing:range:changeInLength:\x00")
	selTextDidChange       = objc.SelZ("textDidChange:\x00")
	selDidChangeSelection  = objc.SelZ("textViewDidChangeSelection:\x00")
	selShouldChangeDel     = objc.SelZ("textView:shouldChangeTextInRange:replacementString:\x00")
	selUndoManagerFor      = objc.SelZ("undoManagerForTextView:\x00")
	selFrameChanged        = objc.SelZ("gnuiFrameChanged:\x00")
	selSetMarkedText2      = objc.SelZ("setMarkedText:selectedRange:\x00")
	selExtraLineRect       = objc.SelZ("extraLineFragmentRect\x00")
	selLabelWithString     = objc.SelZ("labelWithString:\x00")
	selSetLineBreakMode    = objc.SelZ("setLineBreakMode:\x00")
	selSetAXElement        = objc.SelZ("setAccessibilityElement:\x00")
	selSetAXPlaceholder    = objc.SelZ("setAccessibilityPlaceholderValue:\x00")
	selAddSubviewRel       = objc.SelZ("addSubview:positioned:relativeTo:\x00")
	selSizeToFit           = objc.SelZ("sizeToFit\x00")
	selLineFragPadding     = objc.SelZ("lineFragmentPadding\x00")
	selHitTestEd           = objc.SelZ("hitTest:\x00")
	selSetStringValueEd    = objc.SelZ("setStringValue:\x00")
	selNumberOfGlyphs      = objc.SelZ("numberOfGlyphs\x00")
	selGlyphIndexForChar   = objc.SelZ("glyphIndexForCharacterAtIndex:\x00")
	selLineFragForGlyph    = objc.SelZ("lineFragmentRectForGlyphAtIndex:effectiveRange:\x00")
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
	minLines int
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
	font     objc.ID    // the text's font (+1)
	lineRng  [2]uintptr // lineFragmentRectForGlyphAtIndex:effectiveRange:'s range (heap: AppKit writes it)
}

func registerEditorClasses() {
	tv := objc.Define("GNUITextView", objc.Cls("NSTextView"), nil)
	tv.Method(selDoCommand, "v@::", edDoCommand)
	tv.Method(selKeyDown, "v@:@", edKeyDown)
	tv.Method(selReadSelectionPB, "c@:@", edReadSelection)
	tv.Method(actionSel[MenuPasteAndMatchStyle], "v@:@", edPastePlain)
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

	d := objc.Define("GNUIEditorDelegate", objc.Cls("NSObject"), []string{"NSTextViewDelegate", "NSTextStorageDelegate"})
	d.Method(selStorageEdited, "v@:@Q{_NSRange=QQ}q", edStorageEdited)
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
	files := &[...]objc.ID{objc.Str("public.file-url"), objc.Str("NSFilenamesPboardType")}
	edFileTypes = objc.Retain(objc.ID(objc.F.Reset().Arg(uintptr(clsNSArray)).Arg(uintptr(selArrayWithObjects)).
		ArgPtr(unsafe.Pointer(&files[0])).Arg(uintptr(len(files))).Call(objc.MsgSend).R0()))
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
	e.opts = o
	e.maxLines, e.minLines = editorLines(o)
	e.insetX, e.insetY = float64(o.InsetX), float64(o.InsetY)

	tv := objc.Send(edTVClass, selAlloc)
	if objc.SendBool1(tv, selRespondsTo, uintptr(selInitUsingTLM)) {
		tv = objc.Send1(tv, selInitUsingTLM, 0) // TextKit 1, a network the view owns
	} else {
		tv = objc.ID(objc.F.Reset().Arg(uintptr(tv)).Arg(uintptr(selInitWithFrame)).ArgRect(0, 0, 100, 20).Call(objc.MsgSend).R0())
	}
	e.tv = tv
	edTVDef.Index().Set(tv, uintptr(slot+1))
	e.lm, e.tc = objc.Send(tv, selLayoutManager), objc.Send(tv, selTextContainer)
	font := edFont(o.FontSize)
	e.font = font
	objc.Send1(tv, selSetFont, uintptr(font))
	objc.Send1(tv, selSetTextColor, uintptr(objc.Send(clsNSColor, selTextColor)))
	objc.Send1(tv, selSetRichText, 0)
	objc.Send1(tv, selSetImportsGraphics, 0)
	objc.Send1(tv, selSetAllowsUndo, 1)
	objc.Send1(tv, selSetDrawsBackground, 0)
	objc.Send1(tv, selSetContSpell, 1)
	objc.Send1(tv, selSetEditable, uintptr(boolU(!o.ReadOnly)))
	objc.Send1(tv, selSetVResizable, 1)
	objc.Send1(tv, selSetHResizable, 0)
	objc.Send1(tv, selSetAutoresizing, 2) // NSViewWidthSizable
	objc.F.Reset().Arg(uintptr(tv)).Arg(uintptr(selSetMinSize)).ArgPoint(0, 0).Call(objc.MsgSend)
	objc.F.Reset().Arg(uintptr(tv)).Arg(uintptr(selSetMaxSize)).ArgPoint(fltMax, fltMax).Call(objc.MsgSend)
	objc.F.Reset().Arg(uintptr(tv)).Arg(uintptr(selSetContainerInset)).ArgPoint(float64(o.InsetX), e.insetY).Call(objc.MsgSend)
	objc.Send1(e.tc, selSetWidthTracks, 1)
	objc.F.Reset().Arg(uintptr(e.tc)).Arg(uintptr(selSetContainerSize)).ArgPoint(fltMax, fltMax).Call(objc.MsgSend)
	e.lineH = objc.F.Reset().Arg(uintptr(e.lm)).Arg(uintptr(selDefaultLineHeight)).Arg(uintptr(font)).Call(objc.MsgSend).F64(0)
	e.setPlaceholder(o.Placeholder)

	e.undo = objc.Send(objc.Send(clsNSUndoManager, selAlloc), selInitPlain)
	e.del = objc.Send(objc.Send(edDelClass, selAlloc), selInitPlain)
	edDelDef.Index().Set(e.del, uintptr(slot+1))
	objc.Send1(tv, selSetDelegateEd, uintptr(e.del))
	objc.Send1(objc.Send(tv, selTextStorage), selSetDelegateEd, uintptr(e.del))
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
		objc.Send1(objc.Send(tv, selTextStorage), selSetDelegateEd, 0)
		objc.Send1(tv, selSetDelegateEd, 0)
		objc.Release(tv)
		objc.Release(e.del)
		objc.Release(e.undo)
		objc.Release(e.font)
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

// edFont is the system font of size points (0: 13), retained.
func edFont(size float32) objc.ID {
	if size <= 0 {
		size = 13
	}
	return objc.Retain(objc.ID(objc.F.Reset().Arg(uintptr(clsNSFont)).Arg(uintptr(selSystemFontOfSize)).ArgF64(float64(size)).Call(objc.MsgSend).R0()))
}

// setPlaceholder gives the text view placeholder s ("" none), through public
// API: a label in the placeholder colour and the text's font, under the text
// view's own drawing (its first subview, below the caret), where the first
// line's text starts, shown while the text is empty (with no composition),
// passing every click to the text view; and the accessibility placeholder
// value.
func (e *Editor) setPlaceholder(s string) {
	if s == "" {
		if e.ph != 0 {
			objc.Send(e.ph, selRemoveFromSuper) // the text view held the label
			e.ph, e.phShown = 0, false
		}
		objc.Send1(e.tv, selSetAXPlaceholder, 0)
		return
	}
	str := edNSString([]byte(s))
	defer objc.Release(str)
	if e.ph == 0 {
		l := objc.Send1(edPHClass, selLabelWithString, uintptr(str))
		objc.Send1(l, selSetTextColor, uintptr(objc.Send(clsNSColor, selPlaceholderColor)))
		objc.Send1(l, selSetLineBreakMode, 4) // NSLineBreakByTruncatingTail
		objc.Send1(l, selSetAXElement, 0)     // the text view reports it (placeholder value)
		objc.Send3(e.tv, selAddSubviewRel, uintptr(l), uintptr(^uintptr(0)) /* NSWindowBelow */, 0)
		e.ph, e.phShown = l, true
	} else {
		objc.Send1(e.ph, selSetStringValueEd, uintptr(str))
	}
	objc.Send1(e.ph, selSetFont, uintptr(e.font))
	objc.Send(e.ph, selSizeToFit)
	objc.Send1(e.tv, selSetAXPlaceholder, uintptr(str))
	e.layoutPlaceholder()
	if e.host != 0 {
		e.showPlaceholder() // a label made while there is text starts hidden
	}
}

// SetOptions changes the options NewEditor was given, while the editor runs:
// the placeholder, the maximum length (text already longer stays; edits that
// would make it longer are refused), the Enter policy, the most lines, the
// font size and the insets. What the text view shows follows at once, and a
// new height comes with EditorHeightChanged. The text, the selection, the
// undo stack and the spans stay. At a safe point, main thread; a change of the
// placeholder or the font allocates.
func (e *Editor) SetOptions(o EditorOptions) {
	if e.released {
		return
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	was := e.opts
	e.opts = o
	e.maxLen, e.enter = o.MaxLen, o.Enter
	e.maxLines, e.minLines = editorLines(o)
	relayout := o.MaxLines != was.MaxLines || o.MinLines != was.MinLines
	if o.ReadOnly != was.ReadOnly {
		objc.Send1(e.tv, selSetEditable, uintptr(boolU(!o.ReadOnly)))
	}
	if o.FontSize != was.FontSize {
		objc.Release(e.font)
		e.font = edFont(o.FontSize)
		objc.Send1(e.tv, selSetFont, uintptr(e.font))
		e.lineH = objc.F.Reset().Arg(uintptr(e.lm)).Arg(uintptr(selDefaultLineHeight)).Arg(uintptr(e.font)).Call(objc.MsgSend).F64(0)
		relayout = true
	}
	if o.InsetX != was.InsetX || o.InsetY != was.InsetY {
		e.insetX, e.insetY = float64(o.InsetX), float64(o.InsetY)
		objc.F.Reset().Arg(uintptr(e.tv)).Arg(uintptr(selSetContainerInset)).ArgPoint(e.insetX, e.insetY).Call(objc.MsgSend)
		relayout = true
	}
	if o.Placeholder != was.Placeholder || o.FontSize != was.FontSize {
		e.setPlaceholder(o.Placeholder)
	} else if relayout {
		e.layoutPlaceholder()
	}
	if relayout {
		e.dirtyH = true
		e.wake()
	}
}

// SetColors does nothing on macOS: the text view is transparent over the
// app's box and draws in the system's text colour (EditorOptions.TextColor
// and Background are Windows'). A widget can call it every frame.
func (e *Editor) SetColors(text, background uint32) {}

// SetPlaceholder changes the placeholder ("" none): SetOptions with only the
// placeholder changed.
func (e *Editor) SetPlaceholder(s string) {
	if s != e.opts.Placeholder {
		o := e.opts
		o.Placeholder = s
		e.SetOptions(o)
	}
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
	objc.Send1(objc.Send(e.tv, selTextStorage), selSetDelegateEd, 0)
	objc.Send1(e.tv, selSetDelegateEd, 0)
	edTVDef.Index().Set(e.tv, 0)
	e.w.ReleaseHost(e.host)
	objc.Release(e.tv)
	objc.Release(e.del)
	objc.Release(e.undo)
	objc.Release(e.font)
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

// CommitComposition ends an input method's composition, keeping its text as
// typed: the marked text becomes ordinary text where it is, the selection and
// the undo history stay as they are, and the input method forgets the
// composition. Nothing happens without marked text. It fires no EditorChanged
// (the text is the same) and ends the EditorComposing it had.
func (e *Editor) CommitComposition() {
	if !objc.SendBool(e.tv, selHasMarkedText) {
		return
	}
	e.quiet = true
	objc.Send(e.tv, selUnmarkText)
	if ctx := objc.Send(e.tv, selInputContext); ctx != 0 {
		objc.Send(ctx, selDiscardMarked)
	}
	e.quiet = false
	e.copyText()
	e.pend &^= EditorComposing
	e.dirtyH = true
	e.wake()
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
			objc.Send4(e.lm, selAddTempAttr, uintptr(edFgAttr), uintptr(nsColorPacked(s.Color)), uintptr(loc), uintptr(n))
		}
		if s.Background != 0 {
			objc.Send4(e.lm, selAddTempAttr, uintptr(edBgAttr), uintptr(nsColorPacked(s.Background)), uintptr(loc), uintptr(n))
		}
	}
	return nil
}

func nsColorPacked(c uint32) objc.ID {
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

// rectCall sends the message args builds, which returns an NSRect in d0-d3.
// Not on a per-frame path (args is a closure).
func (e *Editor) rectCall(args func(f *ffi.Frame) *ffi.Frame) objc.Rect {
	f := args(objc.F.Reset()).Call(objc.MsgSend)
	return objc.Rect{X: f.F64(0), Y: f.F64(1), W: f.F64(2), H: f.F64(3)}
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
	h := min(max(bottom, float64(e.minLines)*e.lineH), float64(e.maxLines)*e.lineH) + 2*e.insetY
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

// copyText initializes the text snapshot, then reads selection and marked
// text. Subsequent edits splice the snapshot from edStorageEdited.
func (e *Editor) copyText() {
	e.rev++
	if len(e.m.cp) == 0 {
		str := objc.Send(objc.Send(e.tv, selTextStorage), selString)
		e.text = objc.AppendUTF8(e.text[:0], str)
		e.m.build(e.text)
	}
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

// edStorageEdited receives the processed range in the new string and its
// change in UTF-16 length. Attribute-only changes leave the snapshot alone.
func edStorageEdited(c *ffi.CB) {
	e := delegateOf(objc.ID(c.Int(0)))
	if e == nil || c.Int(3)&2 == 0 {
		return
	} // NSTextStorageEditedCharacters
	e.spliceText(objc.ID(c.Int(2)), int(c.Int(4)), int(c.Int(5)), int(c.Int(6)))
}

func (e *Editor) spliceText(storage objc.ID, loc, n, delta int) {
	str := objc.Send(storage, selString)
	oldN := n - delta
	if len(e.m.cp) == 0 || loc < 0 || oldN < 0 || loc+oldN > e.m.n16 {
		e.text = objc.AppendUTF8(e.text[:0], str)
		e.m.build(e.text)
		return
	}
	lo, hi := e.m.toByte(e.text, loc), e.m.toByte(e.text, loc+oldN)
	// Size just the edited range, shift the suffix once, and convert directly
	// into its final place. Reserve's text capacity also covers replacements:
	// there is no second document-sized scratch buffer after a large paste.
	newN := objc.UTF8RangeLen(str, loc, n)
	size := len(e.text) - (hi - lo) + newN
	e.text = slices.Grow(e.text, max(size-len(e.text), 0))
	oldLen := len(e.text)
	e.text = e.text[:max(oldLen, size)]
	copy(e.text[lo+newN:], e.text[hi:oldLen])
	objc.CopyUTF8Range(e.text[lo:lo+newN], str, loc, n)
	e.text = e.text[:size]
	e.m.rebuild(e.text, lo)
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
			e.pushCommandSends(CmdAccept, sel == selInsertNewline && e.sends(currentMods()))
			e.wake()
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
			objc.F.Reset().Arg(uintptr(e.tv)).Arg(uintptr(selSetMarkedText)).Arg(uintptr(edEmpty)).
				Arg(0).Arg(0).Arg(uintptr(nsNotFound)).Arg(0).Call(objc.MsgSend)
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
		switch {
		case composed:
		case e.compl:
			if sel == selMoveUp {
				e.toApp(CmdUp)
			} else {
				e.toApp(CmdDown)
			}
			return true
		case e.edges && e.atEdge(sel == selMoveUp):
			if sel == selMoveUp {
				e.pend |= EditorEdgeUp
			} else {
				e.pend |= EditorEdgeDown
			}
			e.wake() // and the text view moves the caret as it would
		}
	}
	return false
}

// atEdge reports whether the caret, with nothing selected, is on the text's
// first line (up) or its last (down), as laid out: the line of a wrapped
// paragraph, and after a final line break the empty line that follows it.
// An empty text is both.
func (e *Editor) atEdge(up bool) bool {
	objc.F.Reset().Arg(uintptr(e.tv)).Arg(uintptr(selSelectedRange)).Call(objc.MsgSend)
	loc, n := int(objc.F.R0()), int(objc.F.R1())
	if n != 0 {
		return false
	}
	total := int(objc.Send(objc.Send(e.tv, selTextStorage), selLengthEd))
	if total == 0 {
		return true
	}
	nl := len(e.text) > 0 && e.text[len(e.text)-1] == '\n'
	if loc >= total { // the end: on the empty line after a final break, or the last glyph's
		if nl {
			return !up
		}
		if !up {
			return true
		}
		loc = total - 1
	}
	g := int(objc.Send1(e.lm, selGlyphIndexForChar, uintptr(loc)))
	glyphs := int(objc.Send(e.lm, selNumberOfGlyphs))
	e.lineRng = [2]uintptr{}
	objc.F.Reset().Arg(uintptr(e.lm)).Arg(uintptr(selLineFragForGlyph)).Arg(uintptr(g)).ArgPtr(unsafe.Pointer(&e.lineRng)).Call(objc.MsgSend)
	if up {
		return e.lineRng[0] == 0
	}
	return !nl && int(e.lineRng[0]+e.lineRng[1]) >= glyphs
}

// sends reports whether Enter with modifiers m submits.
func (e *Editor) sends(m Mods) bool {
	switch e.enter {
	case EnterNewline:
		return false
	case CmdEnterSends:
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
	if (code == 0x24 || code == 0x4C) && m&ModSuper != 0 && !marked && (e.compl || e.enter != EnterNewline) {
		if e.compl {
			e.pushCommandSends(CmdAccept, true)
			e.wake()
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

// edReadSelection is GNUITextView's readSelectionFromPasteboard:, which
// Paste calls: files are the app's (their names,
// which a Finder copy has as text too, are not inserted), and so is an image
// with no plain text; anything else is NSTextView's (plain text only: the
// view is not rich).
func edReadSelection(c *ffi.CB) {
	self, pb := objc.ID(c.Int(0)), objc.ID(c.Int(2))
	if e := editorOf(self); e != nil && e.pasteToApp(pb) {
		c.RetBool(true)
		return
	}
	c.RetBool(objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selReadSelectionPB).Arg(uintptr(pb)).Call(objc.MsgSendSuper).Bool())
}

// edPastePlain is GNUITextView's pasteAsPlainText: (Paste and Match Style),
// which reads the general pasteboard without readSelectionFromPasteboard::
// files and an image without text are the app's as for Paste.
func edPastePlain(c *ffi.CB) {
	self := objc.ID(c.Int(0))
	if e := editorOf(self); e != nil {
		pbInit()
		if e.pasteToApp(objc.Send(clsPasteboard, selGeneralPB)) {
			return
		}
	}
	objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, actionSel[MenuPasteAndMatchStyle]).Arg(c.Int(2)).Call(objc.MsgSendSuper)
}

// pasteToApp signals a paste of pasteboard pb that is the app's (files, an
// image without text) and reports whether it was.
func (e *Editor) pasteToApp(pb objc.ID) bool {
	types := objc.Send(pb, selTypes)
	switch {
	case objc.Send1(types, selFirstObjectCommon, uintptr(edFileTypes)) != 0:
		e.pend |= EditorPastedFiles
	case objc.Send1(types, selFirstObjectCommon, uintptr(edImageTypes)) != 0 && !objc.SendBool1(types, selContainsObject, uintptr(edPlainType)):
		e.pend |= EditorPastedImage
	default:
		return false
	}
	e.wake()
	return true
}

// edReadableTypes is GNUITextView's readablePasteboardTypes: NSTextView's,
// the image types and the file types, so that Paste is enabled for an image
// and for files.
func edReadableTypes(c *ffi.CB) {
	self := objc.ID(c.Int(0))
	sup := objc.ID(objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selReadablePBTypes).Call(objc.MsgSendSuper).R0())
	c.Ret(uintptr(objc.Send1(objc.Send1(sup, selArrayByAdding, uintptr(edImageTypes)), selArrayByAdding, uintptr(edFileTypes))))
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
	str, sl, sn, rl, rn := c.Int(2), c.Int(3), c.Int(4), c.Int(5), c.Int(6)
	objc.SuperFrame(c.Frame(), edTVDef.Superclass(), self, selSetMarkedText).Arg(str).Arg(sl).Arg(sn).Arg(rl).Arg(rn).Call(objc.MsgSendSuper)
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
