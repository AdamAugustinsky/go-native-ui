package main

import "gonativeui/internal/objc"

// newTextField is an editable NSTextField (autoreleased: the host retains
// it).
func newTextField(s string) uintptr {
	f := objc.Send1(objc.Cls("NSTextField"), objc.Sel("textFieldWithString:"), uintptr(objc.Str(s)))
	objc.Send1(f, objc.Sel("setBezelStyle:"), 1) // NSTextFieldRoundedBezel
	return uintptr(f)
}
