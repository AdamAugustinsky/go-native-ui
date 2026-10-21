//go:build darwin

// Package objc sends Objective-C messages through internal/ffi without heap
// allocation, defines runtime classes whose methods are Go (Define), and
// makes and calls blocks (block.go).
//
// The main-thread rule. The Send helpers use the main-thread frame F and
// must only be called on the main thread (checked in gnuidebug builds).
// Other threads, and callbacks on any thread, use a frame of their own: a
// callback's ffi.CB.Frame, with MsgSend or the helpers that take a frame.
//
// The lookup rule. Classes and selectors are looked up at setup, on the main
// thread (Cls, Sel), and kept in variables; a handler on another thread
// never looks anything up.
//
// Setup helpers (Cls, Sel, Str, Define and its builder, NewBlockKind,
// NewGlobalBlock) allocate and are meant for startup.
package objc

import (
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

// C entry points of the Objective-C runtime.
var (
	MsgSend      uintptr // objc_msgSend
	MsgSendSuper uintptr // objc_msgSendSuper
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
	initMainCheck()
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
	getClass, selRegister = sym("objc_getClass"), sym("sel_registerName")
	allocPair, registerPair = sym("objc_allocateClassPair"), sym("objc_registerClassPair")
	addMethod, addProtocol, getProtocol = sym("class_addMethod"), sym("class_addProtocol"), sym("objc_getProtocol")
	PoolPush, PoolPop = sym("objc_autoreleasePoolPush"), sym("objc_autoreleasePoolPop")
	selGetName = sym("sel_getName")
	MsgSendSuper = sym("objc_msgSendSuper")
	addIvar, getIvar, ivarOffset = sym("class_addIvar"), sym("class_getInstanceVariable"), sym("ivar_getOffset")
	initBlocks()
	if _, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		panic(err)
	}
	selRetain, selRelease = Sel("retain"), Sel("release")
	selUTF8String, selLength = Sel("UTF8String"), Sel("lengthOfBytesUsingEncoding:")
	selAddObserver, selRemoveObserver = Sel("addObserver:selector:name:object:"), Sel("removeObserver:name:object:")
}

var selGetName, addIvar, getIvar, ivarOffset uintptr

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

// Cls looks up a class by name (setup, main thread).
func Cls(name string) ID {
	checkMain()
	return ID(F.Call1(getClass, uintptr(unsafe.Pointer(CString(name)))))
}

// Sel registers a selector (setup, main thread).
func Sel(name string) SEL {
	checkMain()
	return SEL(F.Call1(selRegister, uintptr(unsafe.Pointer(CString(name)))))
}

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
func Send(id ID, s SEL) ID {
	checkMain()
	return ID(F.Call2(MsgSend, uintptr(id), uintptr(s)))
}
func Send1(id ID, s SEL, a uintptr) ID {
	checkMain()
	return ID(F.Call3(MsgSend, uintptr(id), uintptr(s), a))
}
func Send2(id ID, s SEL, a, b uintptr) ID {
	checkMain()
	return ID(F.Call4(MsgSend, uintptr(id), uintptr(s), a, b))
}
func Send3(id ID, s SEL, a, b, c uintptr) ID {
	checkMain()
	return ID(F.Call5(MsgSend, uintptr(id), uintptr(s), a, b, c))
}
func Send4(id ID, s SEL, a, b, c, d uintptr) ID {
	checkMain()
	return ID(F.Call6(MsgSend, uintptr(id), uintptr(s), a, b, c, d))
}

// SendBool sends a message returning BOOL.
func SendBool(id ID, s SEL) bool {
	checkMain()
	return F.Reset().Arg(uintptr(id)).Arg(uintptr(s)).Call(MsgSend).Bool()
}

// SendBool1 sends a message with one integer argument returning BOOL.
func SendBool1(id ID, s SEL, a uintptr) bool {
	checkMain()
	return F.Reset().Arg(uintptr(id)).Arg(uintptr(s)).Arg(a).Call(MsgSend).Bool()
}

// SendF64 sends a message returning double (d0).
func SendF64(id ID, s SEL) float64 {
	checkMain()
	return F.Reset().Arg(uintptr(id)).Arg(uintptr(s)).Call(MsgSend).F64(0)
}

// SendPoint sends a message returning NSPoint/NSSize (an HFA in d0,d1).
func SendPoint(id ID, s SEL) (x, y float64) {
	checkMain()
	F.Reset().Arg(uintptr(id)).Arg(uintptr(s)).Call(MsgSend)
	return F.F64(0), F.F64(1)
}

// SuperFrame prepares f for objc_msgSendSuper: self's method sel as
// implemented by superclass and its ancestors, skipping the overriding
// class's own. f's scratch words hold the objc_super, so f may be any frame
// (F on the main thread, a callback's own frame elsewhere). Append the
// method's arguments and call MsgSendSuper:
//
//	ok := objc.SuperFrame(c.Frame(), def.Superclass(), self, sel).Arg(responder).Call(objc.MsgSendSuper).Bool()
func SuperFrame(f *ffi.Frame, superclass, self ID, sel SEL) *ffi.Frame {
	sup := f.Scratch()
	sup[0], sup[1] = uintptr(self), uintptr(superclass) // struct objc_super { id receiver; Class super_class; }
	return f.Reset().ArgPtr(unsafe.Pointer(sup)).Arg(uintptr(sel))
}

// SendSuper sends sel to self as implemented by superclass (SuperFrame),
// with integer or pointer arguments and result. It allocates nothing.
func SendSuper(f *ffi.Frame, superclass, self ID, sel SEL, args ...uintptr) uintptr {
	SuperFrame(f, superclass, self, sel)
	for _, a := range args {
		f.Arg(a)
	}
	return f.Call(MsgSendSuper).R0()
}

// Rect is NSRect/CGRect.
type Rect struct{ X, Y, W, H float64 }

// SendRect sends a message returning NSRect (an HFA in d0..d3).
func SendRect(id ID, s SEL) Rect {
	checkMain()
	F.Reset().Arg(uintptr(id)).Arg(uintptr(s)).Call(MsgSend)
	return Rect{F.F64(0), F.F64(1), F.F64(2), F.F64(3)}
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
