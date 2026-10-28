package win32

import (
	"sync"
	"syscall"
	"unsafe"
)

// WinRT plumbing (phase 8, D71, D72): activation factories, HSTRINGs and the
// IInspectable layout, for toasts (W6), Windows.Graphics.Capture (M2) and
// the probes that de-risk them (P4, P6). combase.dll is loaded on the first
// call, never at startup (the phase 8 standing rule: nothing new loads before
// the first frame).
//
// Reference: the Windows SDK's roapi.h (RoInitialize, RoGetActivationFactory,
// RoActivateInstance), winstring.h (WindowsCreateString, WindowsDeleteString,
// WindowsGetStringRawBuffer) and inspectable.h (IInspectable, slots 3-5 after
// IUnknown's 0-2).

// HSTRING is a WinRT string handle. 0 is the empty string, which WinRT
// accepts wherever it takes a string.
type HSTRING uintptr

// RoInitType is RoInitialize's apartment kind.
type RoInitType uint32

// The apartment kinds (roapi.h RO_INIT_TYPE).
const (
	// RoInitSingleThreaded joins a single-threaded apartment (the main
	// thread, which phase 8 makes an OLE STA with OleInitialize).
	RoInitSingleThreaded RoInitType = 0
	// RoInitMultiThreaded joins the multithreaded apartment (workers).
	RoInitMultiThreaded RoInitType = 1
)

// HRESULTs WinRT callers meet.
const (
	// SOK is S_OK.
	SOK int32 = 0
	// SFalse is S_FALSE: RoInitialize on a thread already in that apartment.
	SFalse int32 = 1
	// RPCEChangedMode is RPC_E_CHANGED_MODE: the thread is already in the
	// other apartment kind (it can still use WinRT there).
	RPCEChangedMode int32 = -2147417850 // 0x80010106
	// EProcNotFound is HRESULT_FROM_WIN32(ERROR_PROC_NOT_FOUND): the
	// function is missing (combase.dll before Windows 8).
	EProcNotFound int32 = -2147024769 // 0x8007007F
	// ENoInterface is E_NOINTERFACE.
	ENoInterface int32 = -2147467262 // 0x80004002
	// EPointer is E_POINTER.
	EPointer int32 = -2147467261 // 0x80004003
	// ENotImpl is E_NOTIMPL.
	ENotImpl int32 = -2147467263 // 0x80004001
	// EFail is E_FAIL.
	EFail int32 = -2147467259 // 0x80004005
	// REGDBEClassNotReg is REGDB_E_CLASSNOTREG: no such runtime class on this
	// Windows (a WinRT API newer than the system).
	REGDBEClassNotReg int32 = -2147221164 // 0x80040154
)

// IInspectable's vtable slots after IUnknown's (inspectable.h). A WinRT
// interface's own methods start at InspectableFirst.
const (
	// InspectableGetIids is IInspectable::GetIids.
	InspectableGetIids = 3
	// InspectableGetRuntimeClassName is IInspectable::GetRuntimeClassName.
	InspectableGetRuntimeClassName = 4
	// InspectableGetTrustLevel is IInspectable::GetTrustLevel.
	InspectableGetTrustLevel = 5
	// InspectableFirst is the first slot of the derived interface.
	InspectableFirst = 6
)

// IIDIUnknown is IUnknown's interface id, {00000000-0000-0000-C000-000000000046}.
var IIDIUnknown = GUID{0x00000000, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}

// IIDIInspectable is IInspectable's interface id,
// {AF86E2E0-B12D-4C6A-9C5A-D7AA65101E90} (inspectable.h).
var IIDIInspectable = GUID{0xAF86E2E0, 0xB12D, 0x4C6A, [8]byte{0x9C, 0x5A, 0xD7, 0xAA, 0x65, 0x10, 0x1E, 0x90}}

// IIDIAgileObject is IAgileObject's interface id,
// {94EA2B94-E9CC-49E0-C0FF-EE64CA8F5B90} (objidl.h). A WinRT delegate we
// implement answers it: our handlers may be called from any thread.
var IIDIAgileObject = GUID{0x94EA2B94, 0xE9CC, 0x49E0, [8]byte{0xC0, 0xFF, 0xEE, 0x64, 0xCA, 0x8F, 0x5B, 0x90}}

// combase holds the WinRT entry points, resolved on first use.
var combase struct {
	once                                  sync.Once
	roInit, roGetFactory, roActivate      uintptr
	createString, deleteString, rawBuffer uintptr
}

func loadCombase() {
	combase.once.Do(func() {
		dll := LoadSystem("combase.dll")
		combase.roInit = Proc(dll, "RoInitialize")
		combase.roGetFactory = Proc(dll, "RoGetActivationFactory")
		combase.roActivate = Proc(dll, "RoActivateInstance")
		combase.createString = Proc(dll, "WindowsCreateString")
		combase.deleteString = Proc(dll, "WindowsDeleteString")
		combase.rawBuffer = Proc(dll, "WindowsGetStringRawBuffer")
	})
}

// WinRTAvailable reports whether this Windows has the WinRT entry points
// (Windows 8 and later).
func WinRTAvailable() bool {
	loadCombase()
	return combase.roGetFactory != 0 && combase.createString != 0
}

// RoInitialize joins the calling thread to a WinRT apartment. S_FALSE and
// RPC_E_CHANGED_MODE mean the thread already is in one, which is fine for
// WinRT calls; the caller keeps the thread locked (runtime.LockOSThread).
func RoInitialize(t RoInitType) int32 {
	loadCombase()
	if combase.roInit == 0 {
		return EProcNotFound
	}
	return HR(Call(combase.roInit, uintptr(t)))
}

// NewHSTRING makes an HSTRING holding s. The caller deletes it (Delete)
// once the call that takes it returns; WinRT copies what it keeps. It
// allocates the UTF-16 copy: not for a per-frame path.
func NewHSTRING(s string) (HSTRING, int32) {
	if s == "" {
		return 0, SOK
	}
	loadCombase()
	if combase.createString == 0 {
		return 0, EProcNotFound
	}
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return 0, EFail // s holds a NUL
	}
	var h HSTRING
	hr := HR(callHeap(combase.createString, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&h))))
	return h, hr
}

// Delete frees the string. Deleting 0 does nothing.
func (h HSTRING) Delete() {
	if h != 0 && combase.deleteString != 0 {
		Call(combase.deleteString, uintptr(h))
	}
}

// String copies the string out as UTF-8 (allocating).
func (h HSTRING) String() string {
	if h == 0 || combase.rawBuffer == 0 {
		return ""
	}
	var n uint32
	p := callHeap(combase.rawBuffer, uintptr(h), uintptr(unsafe.Pointer(&n)))
	if p == 0 || n == 0 {
		return ""
	}
	return syscall.UTF16ToString(unsafe.Slice((*uint16)(Ptr(p)), n))
}

// RoGetActivationFactory gets the activation factory of the runtime class
// named class ("Windows.UI.Notifications.ToastNotificationManager") as
// interface iid into *out, which must be heap memory. The caller releases
// it.
func RoGetActivationFactory(class string, iid *GUID, out *uintptr) int32 {
	h, hr := NewHSTRING(class)
	if hr != SOK {
		return hr
	}
	defer h.Delete()
	if combase.roGetFactory == 0 {
		return EProcNotFound
	}
	return HR(callHeap(combase.roGetFactory, uintptr(h), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(out))))
}

// RoActivateInstance makes an instance of the runtime class named class
// with its default constructor, as IInspectable, into *out (heap memory).
// The caller releases it.
func RoActivateInstance(class string, out *uintptr) int32 {
	h, hr := NewHSTRING(class)
	if hr != SOK {
		return hr
	}
	defer h.Delete()
	if combase.roActivate == 0 {
		return EProcNotFound
	}
	return HR(callHeap(combase.roActivate, uintptr(h), uintptr(unsafe.Pointer(out))))
}

// RuntimeClassName asks an IInspectable for its runtime class name
// (allocating). It returns "" when the object does not say.
func RuntimeClassName(obj uintptr) string {
	if obj == 0 {
		return ""
	}
	var h HSTRING
	if HR(comHeap(obj, InspectableGetRuntimeClassName, uintptr(unsafe.Pointer(&h)))) != SOK {
		return ""
	}
	defer h.Delete()
	return h.String()
}

// callHeap is Call for the one-off calls here that pass the address of a
// local (an out parameter): uintptrescapes moves what such an argument
// points to onto the heap, as the package's convention requires.
//
//go:uintptrescapes
func callHeap(fn uintptr, args ...uintptr) uintptr { return Call(fn, args...) }

// comHeap is Com with callHeap's rule.
//
//go:uintptrescapes
func comHeap(obj, idx uintptr, args ...uintptr) uintptr { return Com(obj, idx, args...) }
