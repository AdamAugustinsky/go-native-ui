//go:build darwin

package login

import (
	"fmt"
	"strings"
	"sync"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
)

// macOS, from whatever goroutine calls: a frame of internal/cf and an
// autorelease pool of the call's own. SMAppService is thread-safe and its
// calls block while the background-items service answers.

type smLib struct {
	once sync.Once
	err  error
}

var sm smLib

func (s *smLib) load() {
	if s.err = cf.Init(); s.err != nil {
		return
	}
	if _, s.err = cf.Framework("ServiceManagement"); s.err != nil {
		return
	}
}

// service returns the SMAppService (autoreleased: inside the caller's pool)
// for the app (agent "") or the agent.
func service(fr *ffi.Frame, agent string) (uintptr, error) {
	sm.once.Do(sm.load)
	if sm.err != nil {
		return 0, fmt.Errorf("%w: %v", ErrUnavailable, sm.err)
	}
	cls := cf.Cls(fr, "SMAppService")
	if cls == 0 {
		return 0, ErrUnavailable // before macOS 13
	}
	b := cf.Msg(fr, cf.Cls(fr, "NSBundle"), cf.Sel(fr, "mainBundle"))
	id := cf.Msg(fr, b, cf.Sel(fr, "bundleIdentifier"))
	if b == 0 || id == 0 || !strings.HasSuffix(cf.GoString(fr, cf.Msg(fr, b, cf.Sel(fr, "bundlePath"))), ".app") {
		return 0, ErrUnavailable
	}
	if agent == "" {
		return cf.Msg(fr, cls, cf.Sel(fr, "mainAppService")), nil
	}
	// The plist gnui writes is named after the agent's label.
	plist := cf.String(fr, cf.GoString(fr, id)+"."+agent+".plist")
	defer cf.CFRelease(fr, plist)
	return cf.Msg(fr, cls, cf.Sel(fr, "agentServiceWithPlistName:"), plist), nil
}

func status(fr *ffi.Frame, svc uintptr) State {
	if s := cf.Msg(fr, svc, cf.Sel(fr, "status")); s <= uintptr(NotFound) {
		return State(s)
	}
	return NotFound
}

// nsError describes the NSError left in the frame's out word.
func nsError(fr *ffi.Frame, what string) error {
	e := cf.Word(fr)
	if e == 0 {
		return fmt.Errorf("login: %s refused without saying why", what)
	}
	code := int(cf.Msg(fr, e, cf.Sel(fr, "code")))
	desc := cf.GoString(fr, cf.Msg(fr, e, cf.Sel(fr, "localizedDescription")))
	return fmt.Errorf("login: %s: %s (ServiceManagement error %d)", what, desc, code)
}

// run is the shape of every call: frame, pool, service.
func run(agent string, f func(fr *ffi.Frame, svc uintptr) (State, error)) (State, error) {
	sm.once.Do(sm.load)
	if sm.err != nil {
		return NotFound, fmt.Errorf("%w: %v", ErrUnavailable, sm.err)
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()
	svc, err := service(fr, agent)
	if err != nil {
		return NotFound, err
	}
	if svc == 0 {
		return NotFound, ErrUnavailable
	}
	return f(fr, svc)
}

func osStatus(agent string) (State, error) {
	return run(agent, func(fr *ffi.Frame, svc uintptr) (State, error) { return status(fr, svc), nil })
}

func osEnable(agent string) (State, error) {
	return run(agent, func(fr *ffi.Frame, svc uintptr) (State, error) {
		switch st := status(fr, svc); st {
		case Enabled, RequiresApproval:
			return st, nil
		}
		out := cf.Out(fr)
		if cf.MsgBool(fr, svc, cf.Sel(fr, "registerAndReturnError:"), uintptr(out)) {
			return status(fr, svc), nil
		}
		err := nsError(fr, "registering")
		if agent != "" && status(fr, svc) == NotFound {
			// Registration is how the system says why it cannot see the item.
			return NotFound, fmt.Errorf("%w: %q (%v)", ErrNotFound, agent, err)
		}
		// A registration that is there already (a race with another start)
		// is not a failure.
		if st := status(fr, svc); st == Enabled || st == RequiresApproval {
			return st, nil
		}
		return status(fr, svc), err
	})
}

func osDisable(agent string) (State, error) {
	return run(agent, func(fr *ffi.Frame, svc uintptr) (State, error) {
		if st := status(fr, svc); st == NotRegistered || st == NotFound {
			return st, nil
		}
		out := cf.Out(fr)
		if cf.MsgBool(fr, svc, cf.Sel(fr, "unregisterAndReturnError:"), uintptr(out)) {
			return status(fr, svc), nil
		}
		err := nsError(fr, "unregistering")
		if st := status(fr, svc); st == NotRegistered || st == NotFound {
			return st, nil
		}
		return status(fr, svc), err
	})
}

func osOpenSettings() error {
	sm.once.Do(sm.load)
	if sm.err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, sm.err)
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()
	cls := cf.Cls(fr, "SMAppService")
	if cls == 0 {
		return ErrUnavailable
	}
	cf.Msg(fr, cls, cf.Sel(fr, "openSystemSettingsLoginItems"))
	return nil
}

// A RunValue is Windows' kind of login item.

func osStatusItem(i Item) (State, error) {
	if i.run != "" {
		return NotFound, ErrUnsupported
	}
	return osStatus(i.agent)
}

func osEnableItem(i Item) (State, error) {
	if i.run != "" {
		return NotFound, ErrUnsupported
	}
	return osEnable(i.agent)
}

func osDisableItem(i Item) (State, error) {
	if i.run != "" {
		return NotFound, ErrUnsupported
	}
	return osDisable(i.agent)
}
