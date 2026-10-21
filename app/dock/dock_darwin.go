//go:build darwin

package dock

import (
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
	return objc.Send(objc.Cls("NSApplication"), objc.Sel("sharedApplication"))
}

func osSetBadge(label string) error {
	a := nsApp()
	if a == 0 {
		return ErrNotStarted
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	var s objc.ID // nil removes the badge
	if label != "" {
		s = objc.Str(label)
	}
	objc.Send1(objc.Send(a, selDockTile), selSetBadgeLabel, uintptr(s))
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
