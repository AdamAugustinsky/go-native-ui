//go:build darwin

// Package objc sends Objective-C messages through internal/ffi without heap
// allocation. All helpers use the main-thread frame F and must only be
// called on the main thread. Other threads use their own ffi.Frame with
// MsgSend.
//
// Setup helpers (Cls, Sel, Str, Define and its builder) allocate and are meant for startup.
package objc

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// ID is an Objective-C object pointer; SEL a selector.
type (
	ID  uintptr
	SEL uintptr
)

// F is the main thread's call frame.
var F = new(ffi.Frame)

var (
	MsgSend      uintptr // objc_msgSend
	msgSendStret uintptr // objc_msgSend_stret (amd64 only)
	getClass     uintptr
	selRegister  uintptr
	allocPair    uintptr
	registerPair uintptr
	addMethod    uintptr
	addProtocol  uintptr
	getProtocol  uintptr
	PoolPush     uintptr // objc_autoreleasePoolPush
	PoolPop      uintptr // objc_autoreleasePoolPop
)

func init() {
	h, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}
	sym := func(n string) uintptr {
		p, err := purego.Dlsym(h, n)
		if err != nil {
			panic(err)
		}
		return p
	}
	MsgSend = sym("objc_msgSend")
	if runtime.GOARCH == "amd64" {
		msgSendStret = sym("objc_msgSend_stret")
	}
	getClass, selRegister = sym("objc_getClass"), sym("sel_registerName")
	allocPair, registerPair = sym("objc_allocateClassPair"), sym("objc_registerClassPair")
	addMethod, addProtocol, getProtocol = sym("class_addMethod"), sym("class_addProtocol"), sym("objc_getProtocol")
	PoolPush, PoolPop = sym("objc_autoreleasePoolPush"), sym("objc_autoreleasePoolPop")
	selGetName = sym("sel_getName")
	if _, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		panic(err)
	}
	selRetain, selRelease = Sel("retain"), Sel("release")
	selUTF8String, selLength = Sel("UTF8String"), Sel("lengthOfBytesUsingEncoding:")
}

var selGetName uintptr

// SelName returns a selector's name (debugging; allocates).
func SelName(s SEL) string {
	p := ffi.PtrFromC(F.Call1(selGetName, uintptr(s)))
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}

// keep holds C strings handed to the Objective-C runtime for the process lifetime.
var keep []*byte

// CString returns a NUL-terminated copy of s on the heap that is never freed
// (setup use).
func CString(s string) *byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	keep = append(keep, &b[0])
	return &b[0]
}

// Cls looks up a class by name.
func Cls(name string) ID { return ID(F.Call1(getClass, uintptr(unsafe.Pointer(CString(name))))) }

// Sel registers a selector.
func Sel(name string) SEL { return SEL(F.Call1(selRegister, uintptr(unsafe.Pointer(CString(name))))) }

// Str creates an autoreleased NSString from s (setup use).
func Str(s string) ID {
	return Send1(Cls("NSString"), Sel("stringWithUTF8String:"), uintptr(unsafe.Pointer(CString(s))))
}

// Retain sends -retain and returns the object.
func Retain(id ID) ID { return Send(id, selRetain) }

var selRetain, selRelease, selUTF8String, selLength SEL

// Release sends -release.
func Release(id ID) { Send(id, selRelease) }

// Message sends with integer/pointer arguments and an integer/pointer result.
func Send(id ID, s SEL) ID { return ID(F.Call2(MsgSend, uintptr(id), uintptr(s))) }
func Send1(id ID, s SEL, a uintptr) ID {
	return ID(F.Call3(MsgSend, uintptr(id), uintptr(s), a))
}
func Send2(id ID, s SEL, a, b uintptr) ID {
	return ID(F.Call4(MsgSend, uintptr(id), uintptr(s), a, b))
}
func Send3(id ID, s SEL, a, b, c uintptr) ID {
	return ID(F.Call5(MsgSend, uintptr(id), uintptr(s), a, b, c))
}
func Send4(id ID, s SEL, a, b, c, d uintptr) ID {
	return ID(F.Call6(MsgSend, uintptr(id), uintptr(s), a, b, c, d))
}

// SendBool sends a message returning BOOL.
func SendBool(id ID, s SEL) bool {
	return F.Reset().Arg(uintptr(id)).Arg(uintptr(s)).Call(MsgSend).Bool()
}

// SendBool1 sends a message with one integer argument returning BOOL.
func SendBool1(id ID, s SEL, a uintptr) bool {
	return F.Reset().Arg(uintptr(id)).Arg(uintptr(s)).Arg(a).Call(MsgSend).Bool()
}

// SendF64 sends a message returning double (d0 / xmm0).
func SendF64(id ID, s SEL) float64 {
	return F.Reset().Arg(uintptr(id)).Arg(uintptr(s)).Call(MsgSend).F64(0)
}

// SendPoint sends a message returning NSPoint/NSSize (HFA d0,d1 on arm64;
// two SSE eightbytes xmm0,xmm1 on amd64).
func SendPoint(id ID, s SEL) (x, y float64) {
	F.Reset().Arg(uintptr(id)).Arg(uintptr(s)).Call(MsgSend)
	return F.F64(0), F.F64(1)
}

// Rect is NSRect/CGRect.
type Rect struct{ X, Y, W, H float64 }

var rectOut = new(Rect)

// SendRect sends a message returning NSRect: d0..d3 on arm64, a hidden
// result pointer through objc_msgSend_stret on amd64.
func SendRect(id ID, s SEL) Rect {
	if runtime.GOARCH == "arm64" {
		F.Reset().Arg(uintptr(id)).Arg(uintptr(s)).Call(MsgSend)
		return Rect{F.F64(0), F.F64(1), F.F64(2), F.F64(3)}
	}
	F.Reset().SetIndirectResult(unsafe.Pointer(rectOut)).Arg(uintptr(id)).Arg(uintptr(s)).Call(msgSendStret)
	return *rectOut
}

// AppendUTF8 appends the UTF-8 bytes of an NSString to dst.
func AppendUTF8(dst []byte, str ID) []byte {
	if str == 0 {
		return dst
	}
	p := uintptr(Send(str, selUTF8String))
	n := int(Send1(str, selLength, 4 /* NSUTF8StringEncoding */))
	if p == 0 || n == 0 {
		return dst
	}
	return append(dst, unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)...)
}
