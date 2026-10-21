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
// The lookup rule. Cls and Sel remember every name, so a repeat call is a
// map lookup that allocates nothing, and they are safe from any goroutine;
// still, a handler on another thread should find its classes and selectors
// at setup and keep them in variables.
//
// Str creates an autoreleased NSString without keeping or allocating
// anything, so it is fine per call on the main thread. CString keeps its
// copy for ever (once per distinct string): for names fixed at setup only.
// Define and its builder, NewBlockKind and NewGlobalBlock allocate and are
// meant for startup.
package objc

import (
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"
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
	clsNSString = Cls("NSString")
	selAlloc, selInitBytes, selAutorel = Sel("alloc"), Sel("initWithBytes:length:encoding:"), Sel("autorelease")
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

// The intern tables. Each distinct name costs one C string, one class
// lookup and one selector registration for the life of the process; a repeat
// is a read-locked map lookup that allocates nothing. They are guarded by
// internMu, so Cls, Sel and CString are safe from any goroutine; a miss runs
// its C call on missFrame under the write lock, never on F.
var (
	internMu  sync.RWMutex
	cstrs     = map[string]*byte{}
	classes   = map[string]ID{}
	sels      = map[string]SEL{}
	missFrame = new(ffi.Frame)
)

// CString returns a NUL-terminated copy of s on the heap that is never freed
// and is the same pointer for every call with the same s, so the memory held
// is bounded by the number of distinct strings. Use it for strings the
// Objective-C runtime may keep (class_addMethod type strings, names given to
// objc_allocateClassPair), which are fixed at setup; never for values that
// vary at run time. It is safe from any goroutine.
func CString(s string) *byte {
	internMu.RLock()
	p := cstrs[s]
	internMu.RUnlock()
	if p != nil {
		return p
	}
	internMu.Lock()
	defer internMu.Unlock()
	return cstringLocked(s)
}

// cstringLocked is CString with internMu held for writing.
func cstringLocked(s string) *byte {
	if p := cstrs[s]; p != nil {
		return p
	}
	b := make([]byte, len(s)+1)
	copy(b, s)
	p := &b[0]
	cstrs[unsafe.String(p, len(s))] = p // the key shares the C string's bytes
	return p
}

// Cls looks up a class by name. A class found is remembered, so a repeat
// call costs a map lookup and allocates nothing. A class that does not exist
// (yet) returns 0 and is looked up again next time. Safe from any goroutine.
func Cls(name string) ID {
	internMu.RLock()
	c, ok := classes[name]
	internMu.RUnlock()
	if ok {
		return c
	}
	internMu.Lock()
	defer internMu.Unlock()
	if c, ok = classes[name]; ok {
		return c
	}
	c = ID(missFrame.Call1(getClass, uintptr(unsafe.Pointer(cstringLocked(name)))))
	if c != 0 {
		classes[unsafe.String(cstringLocked(name), len(name))] = c
	}
	return c
}

// Sel registers a selector. A repeat call costs a map lookup and allocates
// nothing. Safe from any goroutine.
func Sel(name string) SEL {
	internMu.RLock()
	s, ok := sels[name]
	internMu.RUnlock()
	if ok {
		return s
	}
	internMu.Lock()
	defer internMu.Unlock()
	if s, ok = sels[name]; ok {
		return s
	}
	p := cstringLocked(name)
	s = SEL(missFrame.Call1(selRegister, uintptr(unsafe.Pointer(p))))
	sels[unsafe.String(p, len(name))] = s
	return s
}

var (
	clsNSString                        ID
	selAlloc, selInitBytes, selAutorel SEL
	strEmpty                           byte // a valid address for the bytes of ""
)

// Str creates an autoreleased NSString from s. It allocates nothing in Go
// and keeps nothing: the string is built from s's own bytes
// (-initWithBytes:length:encoding:) and autoreleased, so it lives until the
// current autorelease pool drains; retain it to keep it longer. Main thread
// (it uses F).
//
// s is UTF-8. Invalid sequences become U+FFFD, as Go's conversions do
// (stringWithUTF8String: returned nil for them). A NUL is kept as a
// character, where stringWithUTF8String: stopped at it; C code that reads
// the result through -UTF8String still stops there, and AppendUTF8 returns
// every byte.
func Str(s string) ID {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\uFFFD")
	}
	p := &strEmpty
	if len(s) > 0 {
		p = unsafe.StringData(s)
	}
	o := Send3(Send(clsNSString, selAlloc), selInitBytes, uintptr(unsafe.Pointer(p)), uintptr(len(s)), 4 /* NSUTF8StringEncoding */)
	runtime.KeepAlive(s)
	return Send(o, selAutorel)
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
