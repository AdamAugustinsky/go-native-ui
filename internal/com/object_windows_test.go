//go:build windows

package com

import (
	"testing"
	"unsafe"

	"gonativeui/internal/win32"
)

var (
	testIID   = win32.GUID{Data1: 0x12345678, Data2: 0x1234, Data3: 0x5678, Data4: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}}
	otherIID  = win32.GUID{Data1: 0x87654321, Data2: 0x4321, Data3: 0x8765, Data4: [8]byte{8, 7, 6, 5, 4, 3, 2, 1}}
	testOut   uintptr
	testCalls [MaxMethodArgs + 1]uintptr
)

// TestObjectTable drives a kind through its own vtable, as the system
// would: QueryInterface, AddRef, a method with arguments, the last Release
// freeing the record, and a stale Ref after it.
func TestObjectTable(t *testing.T) {
	before := Leaks()
	k := NewKind("test", 2, []win32.GUID{testIID}, []Method{{N: 3, Fn: func(o *Object, a Args) uintptr {
		testCalls[0] = o.Value()
		copy(testCalls[1:], a[:3])
		return 7
	}}})
	o, ref, err := k.New(42)
	if err != nil {
		t.Fatal(err)
	}
	p := o.Ptr()
	if hr := win32.HR(Call(p, 0, uintptr(unsafe.Pointer(&testIID)), uintptr(unsafe.Pointer(&testOut)))); hr != 0 || testOut != p {
		t.Fatalf("QueryInterface(test) = %#x, %#x", hr, testOut)
	}
	if hr := win32.HR(Call(p, 0, uintptr(unsafe.Pointer(&otherIID)), uintptr(unsafe.Pointer(&testOut)))); hr != win32.ENoInterface || testOut != 0 {
		t.Fatalf("QueryInterface(other) = %#x, %#x", hr, testOut)
	}
	if r := Call(p, 3, 10, 20, 30); r != 7 || testCalls != [MaxMethodArgs + 1]uintptr{42, 10, 20, 30} {
		t.Fatalf("method: %d %v", r, testCalls)
	}
	if n := Call(p, 1); n != 3 { // the owner's, QueryInterface's, this one
		t.Fatalf("AddRef = %d", n)
	}
	Call(p, 2)
	Call(p, 2)
	if ref.Resolve() != o {
		t.Fatal("the object went before its last reference")
	}
	o.Release()
	if ref.Resolve() != nil {
		t.Fatal("a freed object still resolves")
	}
	if got := Leaks(); got.Live != before.Live || got.Freed != before.Freed+1 {
		t.Fatalf("leaks: before %+v after %+v", before, got)
	}
	if r := Call(p, 3, 1, 2, 3); win32.HR(r) != win32.EFail {
		t.Fatalf("a call on a freed record = %#x", r)
	}
	a, _, _ := k.New(1)
	b, _, _ := k.New(2)
	if _, _, err := k.New(3); err != ErrFull {
		t.Fatalf("a third object in a table of two: %v", err)
	}
	a.Release()
	b.Release()
}
