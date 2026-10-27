//go:build darwin

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

func init() { runtime.LockOSThread() } // Carbon's application target is the main thread's

// Carbon constants (HIToolbox/CarbonEvents.h, Events.h, MacErrors.h).
const (
	kEventClassKeyboard     = 0x6B657962 // 'keyb'
	kEventHotKeyPressed     = 5
	kEventParamDirectObject = 0x2D2D2D2D // '----'
	typeEventHotKeyID       = 0x686B6964 // 'hkid'
	cmdKey                  = 1 << 8
	shiftKey                = 1 << 9
	optionKey               = 1 << 11
	controlKey              = 1 << 12
	kVKF19                  = 0x50       // kVK_F19
	signature               = 0x474E5549 // 'GNUI'
	eventHotKeyExistsErr    = -9878
)

// EventTypeSpec and EventHotKeyID, laid out as CarbonEvents.h has them.
type eventTypeSpec struct{ class, kind uint32 }
type eventHotKeyID struct{ signature, id uint32 }

// Heap-resident call state (internal/ffi: frames and out-parameters never on
// a goroutine stack).
var (
	f        = new(ffi.Frame)
	spec     = new(eventTypeSpec)
	hkid     = new(eventHotKeyID)
	readBack = new(eventHotKeyID)
	outRef   = new(uintptr)
	hotRef   = new(uintptr)
	hotRef2  = new(uintptr)
	event    = new(uintptr)

	calls, matched int
	lastThreadOK   = true
)

var fn struct {
	appTarget, install, remove, register, unregister uintptr
	create, setParam, getParam, send, release        uintptr
}

type result struct {
	OS              string  `json:"os"`
	HandlerReached  bool    `json:"handler_reached"`
	Calls           int     `json:"handler_calls"`
	IDMatched       int     `json:"id_matched"`
	Sends           int     `json:"sends"`
	InstallStatus   int32   `json:"install_status"`
	RegisterStatus  int32   `json:"register_status"`
	SecondRegister  int32   `json:"second_register_status"`
	SecondIsExists  bool    `json:"second_register_is_eventHotKeyExistsErr"`
	SendStatus      int32   `json:"send_status"`
	MallocsPerSend  float64 `json:"go_mallocs_per_send"`
	NsPerSend       float64 `json:"ns_per_send"`
	UnregisterState int32   `json:"unregister_status"`
	Callbacks       int     `json:"ffi_callbacks_taken"`
	Error           string  `json:"error,omitempty"`
}

func main() {
	n := flag.Int("n", 1000, "synthetic presses to send")
	flag.Parse()
	r := result{OS: runtime.GOOS + "/" + runtime.GOARCH}
	err := run(&r, *n)
	if err != nil {
		r.Error = err.Error()
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	if err != nil {
		os.Exit(1)
	}
}

func run(r *result, n int) error {
	lib, err := purego.Dlopen("/System/Library/Frameworks/Carbon.framework/Carbon", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("dlopen Carbon: %w", err)
	}
	for _, s := range []struct {
		p    *uintptr
		name string
	}{
		{&fn.appTarget, "GetApplicationEventTarget"}, {&fn.install, "InstallEventHandler"},
		{&fn.remove, "RemoveEventHandler"}, {&fn.register, "RegisterEventHotKey"},
		{&fn.unregister, "UnregisterEventHotKey"}, {&fn.create, "CreateEvent"},
		{&fn.setParam, "SetEventParameter"}, {&fn.getParam, "GetEventParameter"},
		{&fn.send, "SendEventToEventTarget"}, {&fn.release, "ReleaseEvent"},
	} {
		if *s.p, err = purego.Dlsym(lib, s.name); err != nil {
			return fmt.Errorf("dlsym %s: %w", s.name, err)
		}
	}

	// OSStatus handler(EventHandlerCallRef next, EventRef ev, void *userData)
	handler := ffi.NewCallback(func(c *ffi.CB) {
		calls++
		ev := c.Int(1)
		st := int32(c.Frame().Arg(ev).Arg32(kEventParamDirectObject).Arg32(typeEventHotKeyID).Arg(0).
			Arg(unsafe.Sizeof(eventHotKeyID{})).Arg(0).Arg(uintptr(unsafe.Pointer(readBack))).Call(fn.getParam).R0())
		if st == 0 && *readBack == *hkid {
			matched++
		}
		c.Ret(0) // noErr: handled
	})
	r.Callbacks = ffi.Callbacks()

	target := f.Call0(fn.appTarget)
	*spec = eventTypeSpec{kEventClassKeyboard, kEventHotKeyPressed}
	r.InstallStatus = int32(f.Reset().Arg(target).Arg(handler).Arg(1).Arg(uintptr(unsafe.Pointer(spec))).Arg(0).
		Arg(uintptr(unsafe.Pointer(outRef))).Call(fn.install).R0())
	if r.InstallStatus != 0 {
		return fmt.Errorf("InstallEventHandler: %d", r.InstallStatus)
	}
	*hkid = eventHotKeyID{signature, 1}
	mods := uintptr(cmdKey | optionKey | controlKey | shiftKey)
	r.RegisterStatus = int32(f.Reset().Arg32(kVKF19).Arg32(uint32(mods)).Arg(*(*uintptr)(unsafe.Pointer(hkid))).
		Arg(target).Arg32(0).Arg(uintptr(unsafe.Pointer(hotRef))).Call(fn.register).R0())
	second := eventHotKeyID{signature, 2}
	r.SecondRegister = int32(f.Reset().Arg32(kVKF19).Arg32(uint32(mods)).Arg(*(*uintptr)(unsafe.Pointer(&second))).
		Arg(target).Arg32(0).Arg(uintptr(unsafe.Pointer(hotRef2))).Call(fn.register).R0())
	r.SecondIsExists = r.SecondRegister == eventHotKeyExistsErr
	if r.SecondRegister == 0 {
		f.Call1(fn.unregister, *hotRef2)
	}

	// The synthetic press: kEventHotKeyPressed with the hotkey's id as its
	// direct object, as the system's own carries.
	st := int32(f.Reset().Arg(0).Arg32(kEventClassKeyboard).Arg32(kEventHotKeyPressed).ArgF64(0).Arg32(0).
		Arg(uintptr(unsafe.Pointer(event))).Call(fn.create).R0())
	if st != 0 {
		return fmt.Errorf("CreateEvent: %d", st)
	}
	st = int32(f.Reset().Arg(*event).Arg32(kEventParamDirectObject).Arg32(typeEventHotKeyID).
		Arg(unsafe.Sizeof(eventHotKeyID{})).Arg(uintptr(unsafe.Pointer(hkid))).Call(fn.setParam).R0())
	if st != 0 {
		return fmt.Errorf("SetEventParameter: %d", st)
	}
	r.SendStatus = int32(f.Call2(fn.send, *event, target)) // warm: the first call attaches nothing new
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	t0 := time.Now()
	for i := 0; i < n; i++ {
		f.Call2(fn.send, *event, target)
	}
	el := time.Since(t0)
	runtime.ReadMemStats(&m1)
	r.Sends = n + 1
	r.Calls, r.IDMatched = calls, matched
	r.HandlerReached = calls > 0
	r.MallocsPerSend = float64(m1.Mallocs-m0.Mallocs) / float64(n)
	r.NsPerSend = float64(el.Nanoseconds()) / float64(n)

	f.Call1(fn.release, *event)
	if r.RegisterStatus == 0 {
		r.UnregisterState = int32(f.Call1(fn.unregister, *hotRef))
	}
	f.Call1(fn.remove, *outRef)
	return nil
}
