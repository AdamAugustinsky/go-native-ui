//go:build darwin

package objc

import (
	"runtime"
	"sync"
	"testing"

	"gonativeui/internal/ffi"
)

// Each instance carries its own index (and any extra ivar), and a handler
// finds it from self.
func TestIvarsArePerInstance(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	d := Define("GNUITestIvars", Cls("NSObject"), nil)
	other := d.Ivar("gnuiOther")
	selIdx := Sel("gnuiMyIndex")
	d.Method(selIdx, "Q@:", func(c *ffi.CB) { c.Ret(d.Index().Get(ID(c.Int(0))) + 1000*other.Get(ID(c.Int(0)))) })
	cls := d.Register()
	a, b := newObject(cls), newObject(cls)
	if d.Index().Get(a) != 0 || other.Get(b) != 0 {
		t.Fatal("a new instance's ivars are not 0")
	}
	d.Index().Set(a, 1)
	d.Index().Set(b, 2)
	other.Set(a, 7)
	other.Set(b, 8)
	if x, y := Send(a, selIdx), Send(b, selIdx); x != 7001 || y != 8002 {
		t.Errorf("handlers read %d and %d, want 7001 and 8002", x, y)
	}
	if per := testing.AllocsPerRun(1000, func() { d.Index().Set(a, d.Index().Get(b)+other.Get(a)) }); per != 0 {
		t.Errorf("ivar access allocated %v objects", per)
	}
}

func TestIvarErrorsPanic(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	panics := func(f func()) (p bool) {
		defer func() { p = recover() != nil }()
		f()
		return
	}
	d := Define("GNUITestIvarErrors", Cls("NSObject"), nil)
	v := d.Ivar("gnuiX")
	if !panics(func() { d.Ivar("gnuiX") }) {
		t.Error("an ivar defined twice did not panic")
	}
	if !panics(func() { d.Ivar(indexIvar) }) {
		t.Error("an ivar named like the index did not panic")
	}
	if !panics(func() { v.Get(1) }) {
		t.Error("an ivar read before Register did not panic")
	}
	d.Register()
	if !panics(func() { d.Ivar("gnuiLate") }) {
		t.Error("an ivar added after Register did not panic")
	}
}

// SendSuper reaches the superclass's implementation, skipping the override,
// with integer and BOOL results, on the main thread's frame and on a GCD
// thread's own frame.
var (
	superOnce              sync.Once
	superSub               *ClassDef
	superObj               ID
	selSuperValue, selKind SEL
	superFromGCD           uintptr
	superGCDResult         uintptr
	superKindAsked         int
)

func superClasses() {
	superOnce.Do(func() {
		selSuperValue, selKind = Sel("gnuiValue:"), Sel("isKindOfClass:")
		base := Define("GNUITestSuperBase", Cls("NSObject"), nil)
		base.Method(selSuperValue, "Q@:Q", func(c *ffi.CB) { c.Ret(10 + c.Int(2)) })
		superSub = Define("GNUITestSuperSub", base.Register(), nil)
		superSub.Method(selSuperValue, "Q@:Q", func(c *ffi.CB) {
			c.Ret(2 * SendSuper(c.Frame(), superSub.Superclass(), ID(c.Int(0)), selSuperValue, c.Int(2)))
		})
		superSub.Method(selKind, "c@:#", func(c *ffi.CB) {
			superKindAsked++
			c.RetBool(SuperFrame(c.Frame(), superSub.Superclass(), ID(c.Int(0)), selKind).Arg(c.Int(2)).Call(MsgSendSuper).Bool())
		})
		superObj = newObject(superSub.Register())
		superFromGCD = ffi.NewCallback(func(c *ffi.CB) {
			f := c.Frame()
			superGCDResult = f.Reset().Arg(uintptr(superObj)).Arg(uintptr(selSuperValue)).Arg(5).Call(MsgSend).R0()
		})
	})
}

func TestSendSuper(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	superClasses()

	if got := Send1(superObj, selSuperValue, 1); got != 22 {
		t.Errorf("the override through super = %d, want 2*(10+1)", got)
	}
	if !SendBool1(superObj, selKind, uintptr(Cls("NSObject"))) || SendBool1(superObj, selKind, uintptr(Cls("NSString"))) {
		t.Error("isKindOfClass: through super answered wrongly")
	}
	if superKindAsked != 2 {
		t.Errorf("the override ran %d times, want 2", superKindAsked)
	}
	g := F.Call0(fnGroupCreate)
	F.Call4(fnGroupAsyncF, g, globalQueue(F, 1), 0, superFromGCD)
	F.Call2(fnGroupWait, g, ^uintptr(0))
	F.Call1(fnDispatchRelease, g)
	if superGCDResult != 30 {
		t.Errorf("from a GCD thread: %d, want 2*(10+5)", superGCDResult)
	}
	if per := testing.AllocsPerRun(1000, func() { Send1(superObj, selSuperValue, 1) }); per != 0 {
		t.Errorf("a message through super allocated %v objects", per)
	}
}

// AddObserver and RemoveObserver count the observers; RetainDelegate and
// ReleaseDelegate the delegates. A posted notification reaches the observer.
func TestObserverAndDelegateCounts(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var got int
	d := Define("GNUITestObserver", Cls("NSObject"), nil)
	sel := Sel("gnuiNoted:")
	d.Method(sel, "v@:@", func(*ffi.CB) { got++ })
	obs := newObject(d.Register())
	center := Send(Cls("NSNotificationCenter"), Sel("defaultCenter"))
	name := Retain(Str("GNUITestNotification"))
	base := Leaks()

	AddObserver(center, obs, sel, name, 0)
	RetainDelegate(obs)
	if l := Leaks(); l.Observers != base.Observers+1 || l.Delegates != base.Delegates+1 {
		t.Errorf("counts after adding: %+v", l)
	}
	Send3(center, Sel("postNotificationName:object:"), uintptr(name), 0, 0)
	RemoveObserver(center, obs, name, 0)
	Send3(center, Sel("postNotificationName:object:"), uintptr(name), 0, 0)
	ReleaseDelegate(obs)
	if got != 1 {
		t.Errorf("the observer saw %d notifications, want 1 (before removal)", got)
	}
	if l := Leaks(); l.Observers != base.Observers || l.Delegates != base.Delegates {
		t.Errorf("counts after removing: %+v, want %+v", l, base)
	}
	Release(obs)
	Release(name)
}
