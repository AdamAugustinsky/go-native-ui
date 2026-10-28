package main

import (
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"gonativeui/internal/win32"
)

// The Start-menu shortcut that carries the AUMID (the second registration
// D71 considers): IShellLinkW, IPropertyStore with
// System.AppUserModel.ID, IPersistFile.

var (
	clsidShellLink   = guid("00021401-0000-0000-C000-000000000046") // shobjidl_core.h CLSID_ShellLink
	iidShellLinkW    = guid("000214F9-0000-0000-C000-000000000046")
	iidPropertyStore = guid("886D8EEB-8CF2-4446-8D02-CDBA1DBDCF99")                             // propsys.h
	iidPersistFile   = guid("0000010B-0000-0000-C000-000000000046")                             // objidl.h
	pkeyAppUserModel = propertyKey{fmtid: guid("9F4C2855-9F79-4B39-A8D0-E1D42DE1D5F3"), pid: 5} // propkey.h PKEY_AppUserModel_ID
)

// Slots (shobjidl_core.h IShellLinkW, propsys.h IPropertyStore, objidl.h
// IPersistFile).
const (
	vtShellLinkSetPath  = 20
	vtPropStoreSetValue = 6
	vtPropStoreCommit   = 7
	vtPersistFileSave   = 6
	clsctxInprocServer  = 1
	vtLPWSTR            = 31 // VT_LPWSTR (wtypes.h)
)

// propertyKey is PROPERTYKEY (wtypes.h): 20 bytes.
type propertyKey struct {
	fmtid win32.GUID
	pid   uint32
}

// propVariant is a PROPVARIANT (propidlbase.h) holding a VT_LPWSTR: 24 bytes
// on 64-bit, the pointer at offset 8.
type propVariant struct {
	vt       uint16
	reserved [3]uint16
	ptr      uintptr
	pad      uintptr
}

// shortcutPath is %APPDATA%\Microsoft\Windows\Start Menu\Programs\probe-p6.lnk.
func shortcutPath() string {
	return filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs\probe-p6.lnk`)
}

// makeShortcut writes the shortcut to this executable with the AUMID id;
// steps gets each step's HRESULT.
func makeShortcut(id string, steps map[string]string) int32 {
	exe, err := os.Executable()
	if err != nil {
		return win32.EFail
	}
	step := func(k string, hr int32) bool { steps[k] = hrText(hr); return hr >= 0 }
	var link, store, file uintptr
	if !step("CoCreateInstance(ShellLink)", win32.HR(call(pCoCreateInstance, uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&link))))) {
		return win32.EFail
	}
	defer win32.Release(link)
	p := utf16(exe)
	if !step("SetPath", win32.HR(cm(link, vtShellLinkSetPath, uintptr(unsafe.Pointer(&p[0]))))) {
		return win32.EFail
	}
	if !step("IPropertyStore", win32.QueryInterface(link, &iidPropertyStore, &store)) {
		return win32.EFail
	}
	defer win32.Release(store)
	idw := utf16(id)
	pv := &propVariant{vt: vtLPWSTR, ptr: uintptr(unsafe.Pointer(&idw[0]))}
	hr := win32.HR(cm(store, vtPropStoreSetValue, uintptr(unsafe.Pointer(&pkeyAppUserModel)), uintptr(unsafe.Pointer(pv))))
	runtime.KeepAlive(idw)
	if !step("SetValue(AppUserModel.ID)", hr) || !step("Commit", win32.HR(cm(store, vtPropStoreCommit))) {
		return win32.EFail
	}
	if !step("IPersistFile", win32.QueryInterface(link, &iidPersistFile, &file)) {
		return win32.EFail
	}
	defer win32.Release(file)
	lp := utf16(shortcutPath())
	if !step("Save", win32.HR(cm(file, vtPersistFileSave, uintptr(unsafe.Pointer(&lp[0])), 1))) {
		return win32.EFail
	}
	return 0
}
