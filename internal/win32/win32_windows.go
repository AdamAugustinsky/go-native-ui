// Package win32 holds the small amount of syscall and COM plumbing shared by
// the Windows platform and gpu backends. It uses only the standard library
// (syscall.SyscallN), resolves every entry point once at init, and its call
// helpers do not allocate.
package win32

import (
	"syscall"
	"unsafe"
)

// GUID is a COM interface or class identifier.
type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLoadLibEx    = kernel32.NewProc("LoadLibraryExW")
	procGetProc      = kernel32.NewProc("GetProcAddress")
	procCloseHandle  = kernel32.NewProc("CloseHandle")
	loadLibEx        = procLoadLibEx.Addr()
	getProcAddr      = procGetProc.Addr()
	closeHandleProc  = procCloseHandle.Addr()
	loadSearchSystem = uintptr(0x00000800) // LOAD_LIBRARY_SEARCH_SYSTEM32
)

// LoadSystem loads a DLL from System32 only (never the application
// directory or the current directory). It returns 0 on failure.
func LoadSystem(name string) uintptr {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	h, _, _ := syscall.SyscallN(loadLibEx, uintptr(unsafe.Pointer(p)), 0, loadSearchSystem)
	return h
}

// Proc returns the address of an exported function, or 0 when the DLL or the
// export is missing (older Windows versions lack some DPI entry points).
func Proc(dll uintptr, name string) uintptr {
	if dll == 0 {
		return 0
	}
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return 0
	}
	r, _, _ := syscall.SyscallN(getProcAddr, dll, uintptr(unsafe.Pointer(p)))
	return r
}

// Call invokes the function at fn with the given arguments and returns the
// raw return register. For functions returning BOOL, HRESULT or int, only the
// low 32 bits are meaningful; use [HR] and [Bool]. Unlike syscall.SyscallN,
// Call does not keep alive what a pointer converted in its arguments points
// to: that memory must stay reachable through something else for the whole
// call (a heap field or a package variable, or runtime.KeepAlive after it).
func Call(fn uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(fn, args...)
	return r
}

// HR reduces a raw return register to an HRESULT.
func HR(r uintptr) int32 { return int32(r) }

// Bool reduces a raw return register to a Win32 BOOL.
func Bool(r uintptr) bool { return uint32(r) != 0 }

// CloseHandle closes a kernel handle.
func CloseHandle(h uintptr) { syscall.SyscallN(closeHandleProc, h) }

// Ptr converts a uintptr that holds a pointer owned by the OS (not by the Go
// heap) back to an unsafe.Pointer without tripping go vet's unsafeptr check.
func Ptr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

// Com calls method idx (zero-based vtable slot) of the COM object obj. The
// object pointer is passed as the implicit first argument. The method's
// arguments must stay valid for the duration of the call: pass pointers to
// heap fields, never to stack locals.
func Com(obj, idx uintptr, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(Ptr(obj))
	fn := *(*uintptr)(unsafe.Add(vtbl, idx*unsafe.Sizeof(uintptr(0))))
	var buf [12]uintptr
	buf[0] = obj
	n := copy(buf[1:], args)
	r, _, _ := syscall.SyscallN(fn, buf[:1+n]...)
	return r
}

// IUnknown vtable slots (every COM interface starts with these).
// Verified against mingw-w64 d3d11.h ID3D11DeviceChildVtbl, lines 2674-2682.
const (
	vtQueryInterface = 0
	vtAddRef         = 1
	vtRelease        = 2
)

// AddRef adds one reference. It is a no-op for a nil pointer.
func AddRef(obj uintptr) {
	if obj != 0 {
		Com(obj, vtAddRef)
	}
}

// QueryInterface asks obj for another interface. out must point to a heap
// location that receives the interface pointer.
func QueryInterface(obj uintptr, iid *GUID, out *uintptr) int32 {
	return HR(Com(obj, vtQueryInterface, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(out))))
}

// Release drops one reference. It is a no-op for a nil pointer.
func Release(obj uintptr) {
	if obj != 0 {
		Com(obj, vtRelease)
	}
}
