//go:build darwin

// Package cf calls CoreFoundation, the Objective-C runtime and system
// frameworks from worker goroutines, for the app services that block (the
// Keychain, Launch Services, the trash) or that must not touch the main
// thread's state.
//
// Message helpers use a frame of the caller's own (Acquire/Release; a Frame
// must live on the heap, which a pool guarantees). Class and selector lookups
// share internal/objc's goroutine-safe intern tables; message sends never use
// its main-thread frame. CoreFoundation and service frameworks are opened
// with dlopen on first use (Init, Framework); objc's Foundation bootstrap
// still runs at package initialization. Call Init, and check its error,
// before any other function here.
//
// Every pointer argument that reaches C must point to heap memory: the frame
// holds addresses as integers, so a stack object could move between the
// moment its address is stored and the call (a Go call between the two may
// grow the stack). The helpers here allocate with make for that reason; use
// make (not a literal or a conversion that may stay on the stack) for buffers
// you pass yourself.
package cf

import (
	"errors"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// kCFStringEncodingUTF8.
const encUTF8 = 0x08000100

var (
	once    sync.Once
	initErr error

	fnRelease, fnRetain, fnStrCreate, fnStrGetCString, fnStrLen, fnStrMaxSize uintptr
	fnDataCreate, fnDataLen, fnDataBytes                                      uintptr
	fnDictCreate, fnDictSet                                                   uintptr
	fnArrayCreate, fnArrayLen, fnArrayAt                                      uintptr
	fnURLFromFS, fnURLFromString                                              uintptr
	fnMsgSend, fnPoolPush, fnPoolPop                                          uintptr
	keyCallbacks, valueCallbacks                                              uintptr
	cfLib                                                                     uintptr

	frames = sync.Pool{New: func() any { return new(ffi.Frame) }}
)

// ErrUnavailable means a system library or symbol could not be found.
var ErrUnavailable = errors.New("cf: system frameworks unavailable")

// Init opens CoreFoundation, Foundation and the Objective-C runtime and
// resolves the entry points the helpers use. It is cheap after the first call;
// every helper that needs it calls it, so callers need not.
func Init() error {
	once.Do(func() {
		var err error
		cfLib, err = Framework("CoreFoundation")
		if err != nil {
			initErr = err
			return
		}
		objc, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			initErr = err
			return
		}
		if _, err = Framework("Foundation"); err != nil {
			initErr = err
			return
		}
		bad := false
		sym := func(lib uintptr, name string) uintptr {
			p, err := purego.Dlsym(lib, name)
			if err != nil || p == 0 {
				bad = true
			}
			return p
		}
		fnRelease, fnRetain = sym(cfLib, "CFRelease"), sym(cfLib, "CFRetain")
		fnStrCreate, fnStrGetCString = sym(cfLib, "CFStringCreateWithBytes"), sym(cfLib, "CFStringGetCString")
		fnStrLen, fnStrMaxSize = sym(cfLib, "CFStringGetLength"), sym(cfLib, "CFStringGetMaximumSizeForEncoding")
		fnDataCreate, fnDataLen, fnDataBytes = sym(cfLib, "CFDataCreate"), sym(cfLib, "CFDataGetLength"), sym(cfLib, "CFDataGetBytePtr")
		fnDictCreate, fnDictSet = sym(cfLib, "CFDictionaryCreateMutable"), sym(cfLib, "CFDictionarySetValue")
		fnArrayCreate, fnArrayLen, fnArrayAt = sym(cfLib, "CFArrayCreate"), sym(cfLib, "CFArrayGetCount"), sym(cfLib, "CFArrayGetValueAtIndex")
		fnURLFromFS, fnURLFromString = sym(cfLib, "CFURLCreateFromFileSystemRepresentation"), sym(cfLib, "CFURLCreateWithString")
		keyCallbacks, valueCallbacks = sym(cfLib, "kCFTypeDictionaryKeyCallBacks"), sym(cfLib, "kCFTypeDictionaryValueCallBacks")

		fnMsgSend = sym(objc, "objc_msgSend")
		fnPoolPush, fnPoolPop = sym(objc, "objc_autoreleasePoolPush"), sym(objc, "objc_autoreleasePoolPop")
		if bad {
			initErr = ErrUnavailable
		}
	})
	return initErr
}

// CoreFoundation is the handle of the CoreFoundation library (after Init), for
// Const and Sym: kCFBooleanTrue, kCFNull and the like.
func CoreFoundation() uintptr { return cfLib }

// Framework opens /System/Library/Frameworks/<name>.framework/<name> and
// returns its handle. Opening a framework twice is cheap.
func Framework(name string) (uintptr, error) {
	return purego.Dlopen("/System/Library/Frameworks/"+name+".framework/"+name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

// Sym returns the address of a symbol of lib, or 0.
func Sym(lib uintptr, name string) uintptr {
	p, err := purego.Dlsym(lib, name)
	if err != nil {
		return 0
	}
	return p
}

// Const reads a framework constant of pointer type (a CFStringRef such as
// kSecClass, an NSString* such as NSLocaleCountryCode): the symbol is the
// address of the pointer. It returns 0 when the symbol is missing.
func Const(lib uintptr, name string) uintptr {
	p := Sym(lib, name)
	if p == 0 {
		return 0
	}
	return *(*uintptr)(ffi.PtrFromC(p))
}

// Acquire returns a call frame that no other goroutine is using. Give it back
// with Release.
func Acquire() *ffi.Frame { return frames.Get().(*ffi.Frame) }

// Release returns a frame from Acquire.
func Release(fr *ffi.Frame) { frames.Put(fr) }

// Out is a word of memory that lives with the frame, for an out parameter
// (T **result): pass its address, read the word afterwards with Word. It is
// cleared first.
func Out(fr *ffi.Frame) unsafe.Pointer {
	fr.Scratch()[0] = 0
	return unsafe.Pointer(&fr.Scratch()[0])
}

// Word reads the word Out gave.
func Word(fr *ffi.Frame) uintptr { return fr.Scratch()[0] }

// CFRelease releases a Core Foundation object (not nil).
func CFRelease(fr *ffi.Frame, obj uintptr) {
	if obj != 0 {
		fr.Call1(fnRelease, obj)
	}
}

// CFRetain retains a Core Foundation object and returns it.
func CFRetain(fr *ffi.Frame, obj uintptr) uintptr { return fr.Call1(fnRetain, obj) }

// heapBytes returns a NUL-terminated copy of s on the heap.
func heapBytes(s string) []byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b
}

// String makes a CFString (owned: CFRelease it) from s.
func String(fr *ffi.Frame, s string) uintptr {
	b := heapBytes(s)
	r := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&b[0])).Arg(uintptr(len(s))).Arg32(encUTF8).ArgBool(false).Call(fnStrCreate).R0()
	runtime.KeepAlive(b)
	return r
}

// GoString copies a CFString or NSString into a Go string ("" for nil).
func GoString(fr *ffi.Frame, s uintptr) string {
	if s == 0 {
		return ""
	}
	n := fr.Reset().Arg(s).Call(fnStrLen).R0() // UTF-16 units
	max := fr.Reset().Arg(n).Arg32(encUTF8).Call(fnStrMaxSize).R0()
	if int(max) < 0 || max > 1<<20 {
		return ""
	}
	buf := make([]byte, max+1)
	ok := fr.Reset().Arg(s).ArgPtr(unsafe.Pointer(&buf[0])).Arg(uintptr(len(buf))).Arg32(encUTF8).Call(fnStrGetCString).Bool()
	runtime.KeepAlive(buf)
	if !ok {
		return ""
	}
	for i, c := range buf {
		if c == 0 {
			return string(buf[:i])
		}
	}
	return ""
}

// Data makes a CFData (owned) holding a copy of b.
func Data(fr *ffi.Frame, b []byte) uintptr {
	p := make([]byte, len(b)+1) // never nil, even for an empty secret
	copy(p, b)
	r := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&p[0])).Arg(uintptr(len(b))).Call(fnDataCreate).R0()
	runtime.KeepAlive(p)
	return r
}

// DataBytes copies out the bytes of a CFData.
func DataBytes(fr *ffi.Frame, d uintptr) []byte {
	if d == 0 {
		return nil
	}
	n := int(fr.Call1(fnDataLen, d))
	out := make([]byte, n)
	if n > 0 {
		p := fr.Call1(fnDataBytes, d)
		copy(out, unsafe.Slice((*byte)(ffi.PtrFromC(p)), n))
	}
	return out
}

// Dict makes an empty mutable CFDictionary (owned) that retains its keys and
// values.
func Dict(fr *ffi.Frame) uintptr {
	return fr.Reset().Arg(0).Arg(0).Arg(keyCallbacks).Arg(valueCallbacks).Call(fnDictCreate).R0()
}

// DictSet sets key to val in a dictionary from Dict (both retained by it).
func DictSet(fr *ffi.Frame, d, key, val uintptr) { fr.Call3(fnDictSet, d, key, val) }

// Array makes a CFArray (owned) of the given objects, retained by the array.
func Array(fr *ffi.Frame, objs ...uintptr) uintptr {
	p := make([]uintptr, len(objs)+1)
	copy(p, objs)
	r := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&p[0])).Arg(uintptr(len(objs))).Arg(0).Call(fnArrayCreate).R0()
	runtime.KeepAlive(p)
	return r
}

// ArrayLen is CFArrayGetCount.
func ArrayLen(fr *ffi.Frame, a uintptr) int { return int(fr.Call1(fnArrayLen, a)) }

// ArrayAt is CFArrayGetValueAtIndex (not retained).
func ArrayAt(fr *ffi.Frame, a uintptr, i int) uintptr { return fr.Call2(fnArrayAt, a, uintptr(i)) }

// FileURL makes a CFURL (owned) for a file system path, as a directory when
// dir is set; 0 when the path cannot be a URL.
func FileURL(fr *ffi.Frame, path string, dir bool) uintptr {
	b := heapBytes(path)
	r := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&b[0])).Arg(uintptr(len(path))).ArgBool(dir).Call(fnURLFromFS).R0()
	runtime.KeepAlive(b)
	return r
}

// URL makes a CFURL (owned) from the text of a URL; 0 when it is not a valid
// URL.
func URL(fr *ffi.Frame, s string) uintptr {
	cs := String(fr, s)
	if cs == 0 {
		return 0
	}
	r := fr.Reset().Arg(0).Arg(cs).Arg(0).Call(fnURLFromString).R0()
	CFRelease(fr, cs)
	return r
}

// Cls looks up an Objective-C class by name (0 if missing).
func Cls(_ *ffi.Frame, name string) uintptr { return uintptr(objc.Cls(name)) }

// Sel registers a selector through the shared, goroutine-safe intern table.
func Sel(_ *ffi.Frame, name string) uintptr { return uintptr(objc.Sel(name)) }

// Msg sends a message with integer and pointer arguments (up to 6) and
// returns the integer result. Objective-C objects it returns are autoreleased
// unless the method follows the new/alloc/copy rule: wrap calls in Pool.
func Msg(fr *ffi.Frame, recv, sel uintptr, args ...uintptr) uintptr {
	fr.Reset().Arg(recv).Arg(sel)
	for _, a := range args {
		fr.Arg(a)
	}
	return fr.Call(fnMsgSend).R0()
}

// MsgBool is Msg for a method that returns BOOL.
func MsgBool(fr *ffi.Frame, recv, sel uintptr, args ...uintptr) bool {
	fr.Reset().Arg(recv).Arg(sel)
	for _, a := range args {
		fr.Arg(a)
	}
	return fr.Call(fnMsgSend).Bool()
}

// Pool pushes an autorelease pool and returns the function that pops it. A
// goroutine that sends Objective-C messages needs one around them, or what
// they autorelease leaks. A pool belongs to the thread that pushed it, so Pool
// locks the goroutine to its thread until pop runs.
func Pool(fr *ffi.Frame) (pop func()) {
	runtime.LockOSThread()
	p := fr.Call0(fnPoolPush)
	return func() {
		fr.Call1(fnPoolPop, p)
		runtime.UnlockOSThread()
	}
}

// MsgAddr is objc_msgSend's address, for callers that build their own calls
// (arguments that are not integers).
func MsgAddr() uintptr { return fnMsgSend }
