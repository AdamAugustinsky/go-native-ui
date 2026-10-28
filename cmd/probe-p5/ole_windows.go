//go:build windows

package main

import (
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// TOM (tom.h) and the OLE callback (richole.h).

var (
	// IID_ITextDocument, tom.h: 8CC497C0-A1DF-11CE-8098-00AA0047BE5D.
	iidITextDocument = win32.GUID{Data1: 0x8CC497C0, Data2: 0xA1DF, Data3: 0x11CE, Data4: [8]byte{0x80, 0x98, 0x00, 0xAA, 0x00, 0x47, 0xBE, 0x5D}}
	// IID_ITextDocument2, tom.h: C241F5E0-7206-11D8-A2C7-00A0D1D6C6B3
	// (verify on first CI run).
	iidITextDocument2 = win32.GUID{Data1: 0xC241F5E0, Data2: 0x7206, Data3: 0x11D8, Data4: [8]byte{0xA2, 0xC7, 0x00, 0xA0, 0xD1, 0xD6, 0xC6, 0xB3}}
	// IID_IRichEditOleCallback, richole.h: 00020D03-0000-0000-C000-000000000046.
	iidIRichEditOleCallback = win32.GUID{Data1: 0x00020D03, Data2: 0, Data3: 0, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
)

// ITextDocument's slots (tom.h ITextDocumentVtbl): IUnknown 0-2, IDispatch
// GetTypeInfoCount 3, GetTypeInfo 4, GetIDsOfNames 5, Invoke 6, then
// GetName 7, GetSelection 8, GetStoryCount 9, GetStoryRanges 10, GetSaved 11,
// SetSaved 12, GetDefaultTabStop 13, SetDefaultTabStop 14, New 15, Open 16,
// Save 17, Freeze 18, Unfreeze 19, BeginEditCollection 20,
// EndEditCollection 21, Undo 22, Redo 23, Range 24, RangeFromPoint 25.
// ITextDocument2 keeps these slots (it derives from ITextDocument).
const (
	vtDocBeginEditCollection = 20
	vtDocEndEditCollection   = 21
	vtDocUndo                = 22
	tomSuspend               = -9999995 // tom.h tomConstants
	tomResume                = -9999994
)

// ole holds the TOM interfaces and the callback's record (package memory).
var ole struct {
	reOle, doc, doc2 uintptr
	undoCount        int32
	cb               *com.Object
	queryAccept      int
	lastFormat       uint32
	reallyCalls      int
}

// openTOM gets ITextDocument (and ITextDocument2 if there is one) from the
// control: EM_GETOLEINTERFACE (WM_USER+60, richedit.h).
func openTOM() (hasDoc, hasDoc2 bool) {
	send(0x0400+60, 0, uintptr(unsafe.Pointer(&ole.reOle)))
	if ole.reOle == 0 {
		return false, false
	}
	hasDoc = win32.QueryInterface(ole.reOle, &iidITextDocument, &ole.doc) >= 0
	hasDoc2 = win32.QueryInterface(ole.reOle, &iidITextDocument2, &ole.doc2) >= 0
	return
}

// beginGroup starts an edit collection on the newest document interface,
// and returns its HRESULT.
func beginGroup() int32 {
	d := ole.doc2
	if d == 0 {
		d = ole.doc
	}
	if d == 0 {
		return win32.ENoInterface
	}
	return win32.HR(comCall(d, vtDocBeginEditCollection))
}

func endGroup() int32 {
	d := ole.doc2
	if d == 0 {
		d = ole.doc
	}
	if d == 0 {
		return win32.ENoInterface
	}
	return win32.HR(comCall(d, vtDocEndEditCollection))
}

// suspendUndo is ITextDocument::Undo(tomSuspend or tomResume, NULL), the
// other way to keep a sequence of edits from making separate undo steps.
func suspendUndo(suspend bool) int32 {
	if ole.doc == 0 {
		return win32.ENoInterface
	}
	n := int32(tomResume)
	if suspend {
		n = tomSuspend
	}
	return win32.HR(comCall(ole.doc, vtDocUndo, uintptr(uint32(n)), 0))
}

// IRichEditOleCallback's methods after IUnknown (richole.h
// IRichEditOleCallbackVtbl): GetNewStorage 3 (LPSTORAGE*), GetInPlaceContext
// 4 (frame, doc, frameinfo), ShowContainerUI 5 (BOOL), QueryInsertObject 6
// (clsid, storage, cp), DeleteObject 7 (oleobj), QueryAcceptData 8
// (dataobj, CLIPFORMAT* cf, DWORD reco, BOOL really, HGLOBAL metapict),
// ContextSensitiveHelp 9 (BOOL), GetClipboardData 10 (chrg, reco,
// dataobj*), GetDragDropEffect 11 (BOOL drag, DWORD keys, DWORD* effect),
// GetContextMenu 12 (WORD seltype, oleobj, chrg, HMENU*).
func makeCallback() (*com.Object, error) {
	notImpl := func(n int) com.Method {
		return com.Method{N: n, Fn: func(*com.Object, com.Args) uintptr { return hresult(win32.ENotImpl) }}
	}
	k := com.NewKind("IRichEditOleCallback", 1, []win32.GUID{iidIRichEditOleCallback}, []com.Method{
		notImpl(1), // GetNewStorage
		notImpl(3), // GetInPlaceContext
		{N: 1, Fn: func(*com.Object, com.Args) uintptr { return 0 }}, // ShowContainerUI
		{N: 3, Fn: func(*com.Object, com.Args) uintptr { return 0 }}, // QueryInsertObject: allow
		{N: 1, Fn: func(*com.Object, com.Args) uintptr { return 0 }}, // DeleteObject
		{N: 5, Fn: func(_ *com.Object, a com.Args) uintptr { // QueryAcceptData
			ole.queryAccept++
			if a[1] != 0 {
				ole.lastFormat = uint32(*(*uint16)(win32.Ptr(a[1]))) // CLIPFORMAT is a WORD
			}
			if a[3] != 0 {
				ole.reallyCalls++
			}
			return 0 // S_OK: the control imports the data itself
		}},
		notImpl(1), // ContextSensitiveHelp
		notImpl(3), // GetClipboardData: E_NOTIMPL asks for the default
		notImpl(3), // GetDragDropEffect
		notImpl(4), // GetContextMenu
	})
	o, _, err := k.New(0)
	return o, err
}

func hresult(hr int32) uintptr { return uintptr(uint32(hr)) }
