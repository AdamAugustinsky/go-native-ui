//go:build darwin

package objc

import (
	"slices"
	"sync/atomic"
	"unsafe"

	"gonativeui/internal/ffi"
)

// Handler implements one Objective-C method in Go. Argument 0 of the callback
// is self, 1 is _cmd, and 2... are the method's arguments (ffi.CB.Int); the
// result goes back through the CB's Ret methods.
type Handler = func(*ffi.CB)

// Trace makes every call that reaches a Go method of a defined class print
// "imp <selector>" (GNUI_DEBUG). It allocates, so it is for debugging only.
var Trace bool

// ClassDef builds a runtime class whose methods are written in Go. The
// methods can be added from any number of files, each in its own registration
// function that is handed the same ClassDef; the class itself is created and
// registered once, by Register, after all of them have run:
//
//	view := objc.Define("MyView", objc.Cls("NSView"), []string{"NSTextInputClient"})
//	registerViewKeys(view)  // key.go:  view.Method(selKeyDown, "v@:@", keyDown)
//	registerViewMouse(view) // mouse.go: view.Method(selMouseDown, "v@:@", mouseDown)
//	cls := view.Register()
//
// A ClassDef is for startup on one thread and is not safe for concurrent use.
//
// Dispatch. All the methods added with Method share one ffi callback per
// class; it finds the handler of the selector by binary search in a slice
// sorted when the class is registered. A call allocates nothing and creates no
// closure, and costs a handful of comparisons. The ffi callback table has
// ffi.MaxCallbacks entries for the whole process; a class takes one.
//
// Instance state. Every class gets one uintptr instance variable, Index,
// meant to hold an index into a Go-side table (windows, hosts, delegates),
// so a handler finds its Go state from self without a global and without
// allocating. Ivar adds more uintptr instance variables.
type ClassDef struct {
	name       string
	super      ID
	protocols  []string
	methods    []method
	ivars      []*Ivar
	cls        ID
	registered bool
}

// An Ivar is a uintptr instance variable of a class made with Define. Get
// and Set are plain memory accesses (atomic, so any thread may read), valid
// after Register on instances of the class and its subclasses.
type Ivar struct {
	name string
	off  uintptr
}

// Get reads the variable of obj.
func (v *Ivar) Get(obj ID) uintptr { return atomic.LoadUintptr((*uintptr)(v.at(obj))) }

// Set writes the variable of obj.
func (v *Ivar) Set(obj ID, x uintptr) { atomic.StoreUintptr((*uintptr)(v.at(obj)), x) }

func (v *Ivar) at(obj ID) unsafe.Pointer {
	if v.off == 0 || obj == 0 {
		v.invalid()
	}
	return ffi.PtrFromC(uintptr(obj) + v.off)
}

//go:noinline
func (v *Ivar) invalid() { panic("objc: ivar " + v.name + " used before Register or on nil") }

// indexIvar is the name of the instance variable every class gets.
const indexIvar = "gnuiIndex"

type method struct {
	sel   SEL
	types string
	h     Handler
}

// Define starts the definition of a class named name that inherits from super
// and adopts the named protocols (a protocol the runtime does not know is
// skipped). Nothing is created until Register.
func Define(name string, super ID, protocols []string) *ClassDef {
	return &ClassDef{name: name, super: super, protocols: protocols, ivars: []*Ivar{{name: indexIvar}}}
}

// Index is the class's index instance variable: a uintptr, 0 in a new
// instance, that the code owning the instances sets to the index of its Go
// state (view.Index().Set(obj, i) after init, view.Index().Get(self) in a
// handler).
func (d *ClassDef) Index() *Ivar { return d.ivars[0] }

// Ivar adds a uintptr instance variable named name (unique in the class)
// and returns it; it can be used after Register.
func (d *ClassDef) Ivar(name string) *Ivar {
	if d.registered {
		panic("objc: ivar added to " + d.name + " after Register")
	}
	for _, v := range d.ivars {
		if v.name == name {
			panic("objc: " + d.name + " defines ivar " + name + " twice")
		}
	}
	v := &Ivar{name: name}
	d.ivars = append(d.ivars, v)
	return v
}

// Superclass returns the class given to Define, for SendSuper and
// SuperFrame from the class's overriding methods.
func (d *ClassDef) Superclass() ID { return d.super }

// Method adds the instance method sel to the class. types is its Objective-C
// type encoding ("v@:@": void, self, _cmd, an object). h receives the call
// as described at Handler. A selector can be added once per class.
func (d *ClassDef) Method(sel SEL, types string, h Handler) {
	switch {
	case d.registered:
		panic("objc: method added to " + d.name + " after Register")
	case sel == 0 || h == nil:
		panic("objc: " + d.name + ": method needs a selector and a handler")
	}
	for _, m := range d.methods {
		if m.sel == sel {
			panic("objc: " + d.name + " defines " + SelName(sel) + " twice")
		}
	}
	d.methods = append(d.methods, method{sel, types, h})
}

// Register creates and registers the class, with its protocols and every
// method added so far, and returns it. It panics if the runtime cannot create
// the class (the name is taken). Call it once.
func (d *ClassDef) Register() ID {
	if d.registered {
		panic("objc: " + d.name + " registered twice")
	}
	cls := ID(F.Call3(allocPair, uintptr(d.super), uintptr(unsafe.Pointer(CString(d.name))), 0))
	if cls == 0 {
		panic("objc: class exists or cannot be created: " + d.name)
	}
	d.registered, d.cls = true, cls
	for _, p := range d.protocols {
		if proto := F.Call1(getProtocol, uintptr(unsafe.Pointer(CString(p)))); proto != 0 {
			F.Call2(addProtocol, uintptr(cls), proto)
		}
	}
	types := CString("Q")
	for _, v := range d.ivars {
		// class_addIvar(cls, name, size, log2(alignment), types)
		if F.Reset().Arg(uintptr(cls)).Arg(uintptr(unsafe.Pointer(CString(v.name)))).Arg(8).Arg(3).
			Arg(uintptr(unsafe.Pointer(types))).Call(addIvar).Bool() == false {
			panic("objc: " + d.name + ": cannot add ivar " + v.name)
		}
	}
	var table []entry
	for _, m := range d.methods {
		table = append(table, entry{m.sel, m.h})
	}
	slices.SortFunc(table, func(a, b entry) int {
		if a.sel < b.sel {
			return -1
		}
		return 1 // selectors are unique
	})
	var shared uintptr
	if len(table) > 0 {
		shared = ffi.NewCallback((&dispatcher{table}).dispatch)
	}
	for _, m := range d.methods {
		F.Call4(addMethod, uintptr(cls), uintptr(m.sel), shared, uintptr(unsafe.Pointer(CString(m.types))))
	}
	F.Call1(registerPair, uintptr(cls))
	for _, v := range d.ivars {
		iv := F.Call2(getIvar, uintptr(cls), uintptr(unsafe.Pointer(CString(v.name))))
		v.off = F.Call1(ivarOffset, iv)
	}
	return cls
}

// Class returns the registered class, or 0 before Register.
func (d *ClassDef) Class() ID { return d.cls }

type entry struct {
	sel SEL
	h   Handler
}

// dispatcher is the shared callback of one class: it looks the selector up in
// its table, sorted by selector.
type dispatcher struct{ table []entry }

func (x *dispatcher) dispatch(c *ffi.CB) {
	sel := SEL(c.Int(1))
	if Trace {
		println("imp", SelName(sel))
	}
	if h := x.find(sel); h != nil {
		h(c)
	}
}

// find returns the handler of sel, or nil.
func (x *dispatcher) find(sel SEL) Handler {
	t := x.table
	lo, hi := 0, len(t)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if t[m].sel < sel {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo < len(t) && t[lo].sel == sel {
		return t[lo].h
	}
	return nil
}
