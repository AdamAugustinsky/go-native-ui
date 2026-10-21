//go:build darwin

package dock

import (
	"unsafe"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// NSRequestUserAttentionType.
const (
	nsCriticalRequest      = 0
	nsInformationalRequest = 10
)

var (
	selDockTile      = objc.Sel("dockTile")
	selSetBadgeLabel = objc.Sel("setBadgeLabel:")
	selStringUTF8    = objc.Sel("stringWithUTF8String:")
	selSharedApp     = objc.Sel("sharedApplication")
	selBadgeLabel    = objc.Sel("badgeLabel")
	selRequestAttn   = objc.Sel("requestUserAttention:")
	selCancelAttn    = objc.Sel("cancelUserAttentionRequest:")
	selSetPolicy     = objc.Sel("setActivationPolicy:")
	selPolicy        = objc.Sel("activationPolicy")
	selIsActive      = objc.Sel("isActive")
)

// nsApp is NSApp once the application object exists, 0 before.
func nsApp() objc.ID {
	if !platform.AppStarted() {
		return 0
	}
	if shared == 0 {
		shared = objc.Cls("NSApplication")
	}
	return objc.Send(shared, selSharedApp)
}

// shared is the NSApplication class, once looked up.
var shared objc.ID

// badge is what SetBadge last put on the tile, so that setting the same label
// again does nothing. Main thread only, as the calls are.
var badge struct {
	tile  objc.ID // NSApp.dockTile, once
	str   objc.ID // the NSString class
	label string
	set   bool   // label is known
	buf   []byte // label as a C string: the bridge copies it, the bytes stay on the heap
}

func osSetBadge(label string) error {
	a := nsApp()
	if a == 0 {
		return ErrNotStarted
	}
	b := &badge
	if b.set && b.label == label {
		return nil
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	if b.tile == 0 {
		b.tile, b.str = objc.Send(a, selDockTile), objc.Cls("NSString")
	}
	var s objc.ID // nil removes the badge
	if label != "" {
		b.buf = append(append(b.buf[:0], label...), 0)
		s = objc.Send1(b.str, selStringUTF8, uintptr(unsafe.Pointer(&b.buf[0])))
	}
	objc.Send1(b.tile, selSetBadgeLabel, uintptr(s))
	b.label, b.set = label, true
	return nil
}

func osBadge() string {
	a := nsApp()
	if a == 0 {
		return ""
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	return string(objc.AppendUTF8(nil, objc.Send(objc.Send(a, selDockTile), selBadgeLabel)))
}

func osBounce(critical bool) (Attention, error) {
	a := nsApp()
	if a == 0 {
		return 0, ErrNotStarted
	}
	kind := uintptr(nsInformationalRequest)
	if critical {
		kind = nsCriticalRequest
	}
	id := objc.Send1(a, selRequestAttn, kind)
	if id == 0 && !objc.SendBool(a, selIsActive) {
		// The first request of a process comes back 0 even for an inactive
		// app (observed on macOS 26): the second is made.
		id = objc.Send1(a, selRequestAttn, kind)
	}
	return Attention(id), nil
}

func osCancelBounce(id Attention) {
	if a := nsApp(); a != 0 {
		objc.Send1(a, selCancelAttn, uintptr(id))
	}
}

func osSetPolicy(p app.Policy) error {
	a := nsApp()
	if a == 0 {
		return ErrNotStarted
	}
	if !objc.SendBool1(a, selSetPolicy, uintptr(p)) {
		return ErrPolicy
	}
	return nil
}

func osPolicy() (app.Policy, error) {
	a := nsApp()
	if a == 0 {
		return app.PolicyRegular, ErrNotStarted
	}
	return app.Policy(objc.Send(a, selPolicy)), nil
}
