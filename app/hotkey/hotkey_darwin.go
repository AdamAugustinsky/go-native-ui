//go:build darwin

package hotkey

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/platform"
	"gonativeui/ui"
)

// Carbon hotkeys (D70, probe P10). Carbon (HIToolbox) is loaded by AppKit
// already; the first Register looks up its functions and installs one
// kEventHotKeyPressed handler on the application event target, an ffi
// callback. Each hotkey is a RegisterEventHotKey with our signature and the
// app's ID as its EventHotKeyID, so the handler reads the ID back from the
// event and delivers it, synchronously on the main thread, as an EvService
// from platform.SourceHotkey (platform.DeliverService). Carbon reports a
// chord this process registered twice as eventHotKeyExistsErr, which is
// ErrTaken; a chord another process holds registers without an error
// (TestHotkeyHeldByAnotherProcess, and W7's note in the phase 8 plan).

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
	signature               = 0x474E5549 // 'GNUI'
	eventHotKeyExistsErr    = -9878
	eventHotKeyInvalidErr   = -9879
	eventNotHandledErr      = -9874
)

// EventTypeSpec and EventHotKeyID, laid out as CarbonEvents.h has them.
type eventTypeSpec struct{ class, kind uint32 }
type eventHotKeyID struct{ signature, id uint32 }

type slot struct {
	id  ID
	s   Shortcut
	ref uintptr // EventHotKeyRef
}

// Heap-resident call state (internal/ffi: frames and out-parameters never on
// a goroutine stack). Main thread only.
var hk struct {
	loaded  bool
	loadErr error
	fn      struct {
		appTarget, install, register, unregister  uintptr
		create, setParam, getParam, send, release uintptr
	}
	target  uintptr
	f       *ffi.Frame
	spec    *eventTypeSpec
	hkid    *eventHotKeyID // the ID being registered or simulated
	read    *eventHotKeyID // the handler's read-back (one handler call at a time: main thread)
	out     *uintptr       // EventHandlerRef, EventHotKeyRef, EventRef out-parameters
	slots   [MaxHotkeys]slot
	n       int
	pressed uint64 // presses delivered (tests)
}

func load() error {
	if hk.loaded {
		return hk.loadErr
	}
	hk.loaded = true
	hk.loadErr = func() error {
		lib, err := purego.Dlopen("/System/Library/Frameworks/Carbon.framework/Carbon", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return fmt.Errorf("%w: Carbon: %v", ErrUnsupported, err)
		}
		f := &hk.fn
		for _, s := range []struct {
			p    *uintptr
			name string
		}{
			{&f.appTarget, "GetApplicationEventTarget"}, {&f.install, "InstallEventHandler"},
			{&f.register, "RegisterEventHotKey"}, {&f.unregister, "UnregisterEventHotKey"},
			{&f.create, "CreateEvent"}, {&f.setParam, "SetEventParameter"},
			{&f.getParam, "GetEventParameter"}, {&f.send, "SendEventToEventTarget"},
			{&f.release, "ReleaseEvent"},
		} {
			if *s.p, err = purego.Dlsym(lib, s.name); err != nil {
				return fmt.Errorf("%w: Carbon: %s: %v", ErrUnsupported, s.name, err)
			}
		}
		hk.f, hk.spec, hk.hkid, hk.read, hk.out = new(ffi.Frame), new(eventTypeSpec), new(eventHotKeyID), new(eventHotKeyID), new(uintptr)
		hk.target = hk.f.Call0(f.appTarget)
		*hk.spec = eventTypeSpec{kEventClassKeyboard, kEventHotKeyPressed}
		st := int32(hk.f.Reset().Arg(hk.target).Arg(ffi.NewCallback(pressed)).Arg(1).Arg(uintptr(unsafe.Pointer(hk.spec))).
			Arg(0).Arg(uintptr(unsafe.Pointer(hk.out))).Call(f.install).R0())
		if st != 0 {
			return fmt.Errorf("hotkey: InstallEventHandler: OSStatus %d", st)
		}
		return nil
	}()
	return hk.loadErr
}

// pressed is the kEventHotKeyPressed handler: OSStatus (EventHandlerCallRef,
// EventRef, void *). A press of one of ours is delivered and handled; any
// other event goes on to the next handler.
func pressed(c *ffi.CB) {
	ev := c.Int(1)
	st := int32(c.Frame().Arg(ev).Arg32(kEventParamDirectObject).Arg32(typeEventHotKeyID).Arg(0).
		Arg(unsafe.Sizeof(eventHotKeyID{})).Arg(0).Arg(uintptr(unsafe.Pointer(hk.read))).Call(hk.fn.getParam).R0())
	if st != 0 || hk.read.signature != signature || find(ID(hk.read.id)) < 0 {
		c.Ret(osStatus(eventNotHandledErr))
		return
	}
	hk.pressed++
	platform.DeliverService(platform.SourceHotkey, uint64(hk.read.id), 0, 0)
	c.Ret(0)
}

func osStatus(v int32) uintptr { return uintptr(uint32(v)) }

func find(id ID) int {
	for i := 0; i < hk.n; i++ {
		if hk.slots[i].id == id {
			return i
		}
	}
	return -1
}

func osRegister(id ID, s Shortcut) error {
	if !platform.AppStarted() {
		return ErrNotStarted
	}
	if err := load(); err != nil {
		return err
	}
	code, ok := keyCode(s.Key)
	if !ok {
		return ErrInvalid
	}
	i := find(id)
	if i >= 0 && hk.slots[i].s.Effective() == s.Effective() && hk.slots[i].s.Key == s.Key {
		hk.slots[i].s = s
		return nil // the same chord: nothing to do
	}
	if i < 0 && hk.n == MaxHotkeys {
		return ErrFull
	}
	*hk.hkid = eventHotKeyID{signature, uint32(id)}
	st := int32(hk.f.Reset().Arg32(code).Arg32(carbonMods(s.Effective())).Arg(*(*uintptr)(unsafe.Pointer(hk.hkid))).
		Arg(hk.target).Arg32(0).Arg(uintptr(unsafe.Pointer(hk.out))).Call(hk.fn.register).R0())
	switch st {
	case 0:
	case eventHotKeyExistsErr:
		return ErrTaken
	case eventHotKeyInvalidErr:
		return ErrInvalid
	default:
		return fmt.Errorf("hotkey: RegisterEventHotKey: OSStatus %d", st)
	}
	if i >= 0 { // the new chord holds: release the old one
		hk.f.Call1(hk.fn.unregister, hk.slots[i].ref)
	} else {
		i = hk.n
		hk.n++
	}
	hk.slots[i] = slot{id: id, s: s, ref: *hk.out}
	return nil
}

func osUnregister(id ID) {
	i := find(id)
	if i < 0 {
		return
	}
	hk.f.Call1(hk.fn.unregister, hk.slots[i].ref)
	hk.n--
	hk.slots[i] = hk.slots[hk.n]
	hk.slots[hk.n] = slot{}
}

func osUnregisterAll() {
	for hk.n > 0 {
		osUnregister(hk.slots[hk.n-1].id)
	}
}

func osRegistered(id ID) (Shortcut, bool) {
	if i := find(id); i >= 0 {
		return hk.slots[i].s, true
	}
	return Shortcut{}, false
}

// errNotRegistered is Simulate's error for an ID that is not registered.
var errNotRegistered = errors.New("hotkey: not registered")

// osSimulate sends a synthetic kEventHotKeyPressed carrying id to the
// application event target (SendEventToEventTarget), which calls the same
// handler a real press does, before it returns. No permission is needed: the
// event never leaves the process.
func osSimulate(id ID) error {
	if find(id) < 0 {
		return errNotRegistered
	}
	f := hk.f
	st := int32(f.Reset().Arg(0).Arg32(kEventClassKeyboard).Arg32(kEventHotKeyPressed).ArgF64(0).Arg32(0).
		Arg(uintptr(unsafe.Pointer(hk.out))).Call(hk.fn.create).R0())
	if st != 0 {
		return fmt.Errorf("hotkey: CreateEvent: OSStatus %d", st)
	}
	ev := *hk.out
	defer f.Call1(hk.fn.release, ev)
	*hk.hkid = eventHotKeyID{signature, uint32(id)}
	st = int32(f.Reset().Arg(ev).Arg32(kEventParamDirectObject).Arg32(typeEventHotKeyID).
		Arg(unsafe.Sizeof(eventHotKeyID{})).Arg(uintptr(unsafe.Pointer(hk.hkid))).Call(hk.fn.setParam).R0())
	if st != 0 {
		return fmt.Errorf("hotkey: SetEventParameter: OSStatus %d", st)
	}
	if st = int32(f.Call2(hk.fn.send, ev, hk.target)); st != 0 {
		return fmt.Errorf("hotkey: SendEventToEventTarget: OSStatus %d", st)
	}
	return nil
}

func carbonMods(m ui.Mods) uint32 {
	var c uint32
	if m&ui.ModSuper != 0 {
		c |= cmdKey
	}
	if m&ui.ModShift != 0 {
		c |= shiftKey
	}
	if m&ui.ModAlt != 0 {
		c |= optionKey
	}
	if m&ui.ModCtrl != 0 {
		c |= controlKey
	}
	return c
}

// keyCode is the kVK_* virtual key code (HIToolbox/Events.h) of k: the key
// at that position on a US keyboard, as Carbon hotkeys are physical keys.
func keyCode(k ui.KeyCode) (uint32, bool) {
	switch {
	case k >= 'A' && k <= 'Z':
		return uint32(letterCodes[k-'A']), true
	case k >= '0' && k <= '9':
		return uint32(digitCodes[k-'0']), true
	case k >= ui.KeyF1 && k <= ui.KeyF20:
		return uint32(fnCodes[k-ui.KeyF1]), true
	case k >= ui.KeyNum0 && k <= ui.KeyNum9:
		return uint32(numCodes[k-ui.KeyNum0]), true
	}
	if c, ok := namedCodes[k]; ok {
		return uint32(c), true
	}
	return 0, false
}

var (
	letterCodes = [26]uint8{0x00, 0x0B, 0x08, 0x02, 0x0E, 0x03, 0x05, 0x04, 0x22, 0x26, 0x28, 0x25, 0x2E,
		0x2D, 0x1F, 0x23, 0x0C, 0x0F, 0x01, 0x11, 0x20, 0x09, 0x0D, 0x07, 0x10, 0x06}
	digitCodes = [10]uint8{0x1D, 0x12, 0x13, 0x14, 0x15, 0x17, 0x16, 0x1A, 0x1C, 0x19}
	fnCodes    = [20]uint8{0x7A, 0x78, 0x63, 0x76, 0x60, 0x61, 0x62, 0x64, 0x65, 0x6D, 0x67, 0x6F,
		0x69, 0x6B, 0x71, 0x6A, 0x40, 0x4F, 0x50, 0x5A}
	numCodes   = [10]uint8{0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5B, 0x5C}
	namedCodes = map[ui.KeyCode]uint8{
		ui.KeyEnter: 0x24, ui.KeySpace: 0x31, ui.KeyEscape: 0x35, ui.KeyTab: 0x30, ui.KeyBackspace: 0x33,
		ui.KeyDelete: 0x75, ui.KeyLeft: 0x7B, ui.KeyRight: 0x7C, ui.KeyUp: 0x7E, ui.KeyDown: 0x7D,
		ui.KeyHome: 0x73, ui.KeyEnd: 0x77, ui.KeyPageUp: 0x74, ui.KeyPageDown: 0x79,
		ui.KeyLeftBracket: 0x21, ui.KeyRightBracket: 0x1E, ui.KeyPeriod: 0x2F, ui.KeyComma: 0x2B,
		ui.KeyEqual: 0x18, ui.KeyMinus: 0x1B, ui.KeySlash: 0x2C, ui.KeySemicolon: 0x29, ui.KeyQuote: 0x27,
		ui.KeyBackslash: 0x2A, ui.KeyGrave: 0x32,
		ui.KeyNumDecimal: 0x41, ui.KeyNumMultiply: 0x43, ui.KeyNumAdd: 0x45, ui.KeyNumClear: 0x47,
		ui.KeyNumDivide: 0x4B, ui.KeyNumSubtract: 0x4E, ui.KeyNumEqual: 0x51,
	}
)
