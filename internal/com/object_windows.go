package com

import (
	"errors"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// COM objects implemented in Go (phase 8's Windows rules, D36's pattern on
// Windows): event handlers for WebView2 (H2), WinRT delegates for toasts and
// capture (W6, M2), IDropTarget (W8) and IRichEditOleCallback (W4).
//
// A Kind is one class of such objects: its interfaces and its vtable, built
// once, with one syscall.NewCallback per method (a method's shape: its
// argument count) shared by every object of the kind, which dispatches on
// `this`. Objects are fixed-size records in the kind's table, allocated once
// at NewKind, so a COM pointer is the address of a record that never moves
// and nothing is allocated per object or per call.
//
// Reference counts are explicit. New hands out an object holding one
// reference, the owner's; the system AddRefs and Releases it as COM says;
// the record returns to the table when the count reaches 0, and its
// generation moves on so that the owner's stale handle (Ref) no longer
// resolves. Leaks counts what is alive, for the shell leak gate
// (platform.ShellObjects).
//
// Methods run on whatever thread the system calls them on (an STA's thread
// for WebView2 and OLE, any thread for an agile WinRT delegate). A method
// copies or enqueues and returns; it never builds UI.
//
// Limits: syscall.NewCallback's process-wide budget (at least 1024
// callbacks, never freed) is shared by all kinds, so kinds are made at setup,
// never per object. Callbacks take integer and pointer arguments only: a
// method with float or struct-by-value parameters cannot be implemented
// here (no COM interface phase 8 needs has one).

// MaxMethodArgs is the most arguments (after `this`) a Method takes.
const MaxMethodArgs = 8

// Args are a method's arguments after `this`, as raw words.
type Args [MaxMethodArgs]uintptr

// Method is one method of a Kind after IUnknown's three. It returns the raw
// result (an HRESULT for almost every COM method).
type Method struct {
	// N is the number of arguments after `this` (0 to MaxMethodArgs).
	N int
	// Fn runs the method on the object o.
	Fn func(o *Object, a Args) uintptr
}

// Object is the record a COM pointer we hand out points to. Its first word
// is the vtable pointer, as COM requires; the rest is ours. Owners keep
// their state in their own tables and find it through Value.
type Object struct {
	vtbl  uintptr       // *[3+len(methods)]uintptr, the kind's vtable
	refs  atomic.Int32  // the COM reference count; 0 is a free record
	gen   atomic.Uint32 // moves on each time the record is freed
	value atomic.Uintptr
	kind  uint32 // index into kinds
	slot  uint32 // index into the kind's table
}

// Value is the owner's word set by New (an index into the owner's table,
// typically).
func (o *Object) Value() uintptr { return o.value.Load() }

// Ptr is the COM pointer of the object: what is passed to the system.
func (o *Object) Ptr() uintptr { return uintptr(unsafe.Pointer(o)) }

// Ref is a handle to an object that survives the record's reuse: Resolve
// returns nil once the object was freed.
type Ref struct {
	k    *Kind
	slot uint32
	gen  uint32
}

// Resolve returns the object, or nil if it has been freed since.
func (r Ref) Resolve() *Object {
	if r.k == nil {
		return nil
	}
	o := &r.k.objs[r.slot]
	if o.gen.Load() != r.gen || o.refs.Load() <= 0 {
		return nil
	}
	return o
}

// Kind is a class of COM objects implemented in Go.
type Kind struct {
	name    string
	iids    []win32.GUID // answered by QueryInterface, besides IUnknown
	vtbl    []uintptr
	objs    []Object
	free    []uint32 // free slots, a stack
	mu      sync.Mutex
	methods []Method
	idx     uint32
}

// Counts are the COM objects alive and made, for the leak gate.
type Counts struct {
	// Live is the objects whose count has not reached 0.
	Live int64
	// Made and Freed count objects handed out and returned since start.
	Made, Freed int64
	// Stale counts calls on a freed record (a system bug or ours).
	Stale int64
}

// MaxKinds is the most kinds a process makes.
const MaxKinds = 64

var (
	kindsMu sync.Mutex
	nKinds  int
	kinds   [MaxKinds]atomic.Pointer[Kind]
	stats   struct{ live, made, freed, stale atomic.Int64 }
)

// ErrFull is New's error when every record of the kind is in use.
var ErrFull = errors.New("com: the object table of this kind is full")

// NewKind makes a class of objects: capacity records (made now, never
// grown), answering QueryInterface for IUnknown and each of iids (the first
// is the interface its vtable is), with methods after IUnknown's three in
// vtable order. Call it at setup: it takes 3+len(methods) callbacks for good.
func NewKind(name string, capacity int, iids []win32.GUID, methods []Method) *Kind {
	if capacity <= 0 || len(iids) == 0 {
		panic("com: NewKind needs a capacity and an interface")
	}
	k := &Kind{name: name, iids: append([]win32.GUID(nil), iids...), methods: methods}
	kindsMu.Lock()
	if nKinds == MaxKinds {
		kindsMu.Unlock()
		panic("com: more than MaxKinds kinds")
	}
	k.idx = uint32(nKinds)
	nKinds++
	kindsMu.Unlock()
	k.objs = make([]Object, capacity)
	k.free = make([]uint32, capacity)
	for i := range k.objs {
		k.objs[i].kind, k.objs[i].slot = k.idx, uint32(i)
		k.objs[i].gen.Store(1)
		k.free[i] = uint32(capacity - 1 - i)
	}
	k.vtbl = make([]uintptr, 3+len(methods))
	k.vtbl[0] = syscall.NewCallback(func(this, iid, out uintptr) uintptr { return k.queryInterface(this, iid, out) })
	k.vtbl[1] = syscall.NewCallback(func(this uintptr) uintptr { return k.addRef(this) })
	k.vtbl[2] = syscall.NewCallback(func(this uintptr) uintptr { return k.release(this) })
	for i, m := range methods {
		k.vtbl[3+i] = methodCallback(k, m)
	}
	vt := uintptr(unsafe.Pointer(&k.vtbl[0]))
	for i := range k.objs {
		k.objs[i].vtbl = vt
	}
	kinds[k.idx].Store(k)
	return k
}

// Name is the kind's name, for diagnostics.
func (k *Kind) Name() string { return k.name }

// New hands out an object holding one reference (the caller's, dropped
// with Release), with the owner's word v. It allocates nothing.
func (k *Kind) New(v uintptr) (*Object, Ref, error) {
	k.mu.Lock()
	if len(k.free) == 0 {
		k.mu.Unlock()
		return nil, Ref{}, ErrFull
	}
	s := k.free[len(k.free)-1]
	k.free = k.free[:len(k.free)-1]
	k.mu.Unlock()
	o := &k.objs[s]
	o.value.Store(v)
	o.refs.Store(1)
	stats.live.Add(1)
	stats.made.Add(1)
	return o, Ref{k: k, slot: s, gen: o.gen.Load()}, nil
}

// AddRef adds a reference on the owner's side.
func (o *Object) AddRef() { o.refs.Add(1) }

// Release drops a reference on the owner's side; the last one frees the
// record.
func (o *Object) Release() { kinds[o.kind].Load().release(o.Ptr()) }

// Refs is the reference count now (tests).
func (o *Object) Refs() int32 { return o.refs.Load() }

// object maps `this` to its record, or nil when it is not one of ours or
// not alive.
func (k *Kind) object(this uintptr) *Object {
	base := uintptr(unsafe.Pointer(&k.objs[0]))
	size := unsafe.Sizeof(Object{})
	if this < base || this >= base+uintptr(len(k.objs))*size || (this-base)%size != 0 {
		stats.stale.Add(1)
		return nil
	}
	o := &k.objs[(this-base)/size]
	if o.refs.Load() <= 0 {
		stats.stale.Add(1)
		return nil
	}
	return o
}

func (k *Kind) queryInterface(this, iid, out uintptr) uintptr {
	if out == 0 {
		return hresult(win32.EPointer)
	}
	pout := (*uintptr)(win32.Ptr(out))
	*pout = 0
	o := k.object(this)
	if o == nil || iid == 0 {
		return hresult(win32.ENoInterface)
	}
	g := (*win32.GUID)(win32.Ptr(iid))
	ok := *g == win32.IIDIUnknown || *g == win32.IIDIAgileObject
	for i := 0; !ok && i < len(k.iids); i++ {
		ok = *g == k.iids[i]
	}
	if !ok {
		return hresult(win32.ENoInterface)
	}
	o.refs.Add(1)
	*pout = this
	return 0
}

func (k *Kind) addRef(this uintptr) uintptr {
	o := k.object(this)
	if o == nil {
		return 0
	}
	return uintptr(o.refs.Add(1))
}

func (k *Kind) release(this uintptr) uintptr {
	o := k.object(this)
	if o == nil {
		return 0
	}
	n := o.refs.Add(-1)
	if n == 0 {
		o.gen.Add(1)
		o.value.Store(0)
		k.mu.Lock()
		k.free = append(k.free, o.slot)
		k.mu.Unlock()
		stats.live.Add(-1)
		stats.freed.Add(1)
	}
	return uintptr(n)
}

// methodCallback makes the one callback of method m of kind k, with the
// method's argument count (syscall.NewCallback needs a fixed signature).
func methodCallback(k *Kind, m Method) uintptr {
	call := func(this uintptr, a Args) uintptr {
		o := k.object(this)
		if o == nil {
			return hresult(win32.EFail)
		}
		return m.Fn(o, a)
	}
	switch m.N {
	case 0:
		return syscall.NewCallback(func(t uintptr) uintptr { return call(t, Args{}) })
	case 1:
		return syscall.NewCallback(func(t, a0 uintptr) uintptr { return call(t, Args{a0}) })
	case 2:
		return syscall.NewCallback(func(t, a0, a1 uintptr) uintptr { return call(t, Args{a0, a1}) })
	case 3:
		return syscall.NewCallback(func(t, a0, a1, a2 uintptr) uintptr { return call(t, Args{a0, a1, a2}) })
	case 4:
		return syscall.NewCallback(func(t, a0, a1, a2, a3 uintptr) uintptr { return call(t, Args{a0, a1, a2, a3}) })
	case 5:
		return syscall.NewCallback(func(t, a0, a1, a2, a3, a4 uintptr) uintptr { return call(t, Args{a0, a1, a2, a3, a4}) })
	case 6:
		return syscall.NewCallback(func(t, a0, a1, a2, a3, a4, a5 uintptr) uintptr {
			return call(t, Args{a0, a1, a2, a3, a4, a5})
		})
	case 7:
		return syscall.NewCallback(func(t, a0, a1, a2, a3, a4, a5, a6 uintptr) uintptr {
			return call(t, Args{a0, a1, a2, a3, a4, a5, a6})
		})
	case 8:
		return syscall.NewCallback(func(t, a0, a1, a2, a3, a4, a5, a6, a7 uintptr) uintptr {
			return call(t, Args{a0, a1, a2, a3, a4, a5, a6, a7})
		})
	}
	panic("com: a method takes 0 to 8 arguments")
}

// Leaks reports the objects of every kind.
func Leaks() Counts {
	return Counts{Live: stats.live.Load(), Made: stats.made.Load(), Freed: stats.freed.Load(), Stale: stats.stale.Load()}
}

// hresult is an HRESULT as a method's raw result.
func hresult(hr int32) uintptr { return uintptr(uint32(hr)) }
