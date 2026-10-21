//go:build darwin

package objc

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gonativeui/internal/ffi"
)

var classRun atomic.Int32

// className makes a class name unique to this test run: a class cannot be
// registered twice in a process, and -count reruns the tests.
func className(base string) string { return fmt.Sprintf("%s%d", base, classRun.Add(1)) }

// newObject sends +alloc and -init to cls.
func newObject(cls ID) ID { return Send(Send(cls, Sel("alloc")), Sel("init")) }

// A class defined by two "files": each adds its own selectors in its own
// registration function. Messages sent with the real runtime must reach the
// handler of the selector, with the right self and arguments.
func registerFileA(d *ClassDef, log *[]string) {
	d.Method(Sel("gnuiAdd:to:"), "Q@:QQ", func(c *ffi.CB) {
		*log = append(*log, "add")
		c.Ret(c.Int(2) + c.Int(3))
	})
	d.Method(Sel("gnuiSelf"), "@@:", func(c *ffi.CB) {
		*log = append(*log, "self")
		c.Ret(c.Int(0))
	})
}

func registerFileB(d *ClassDef, log *[]string) {
	d.Method(Sel("gnuiNegate:"), "Q@:Q", func(c *ffi.CB) {
		*log = append(*log, "negate")
		c.Ret(-c.Int(2))
	})
	d.Method(Sel("gnuiFlag"), "c@:", func(c *ffi.CB) {
		*log = append(*log, "flag")
		c.RetBool(true)
	})
}

func TestMethodsFromSeveralFilesReachTheirHandlers(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)

	var log []string
	d := Define(className("GNUITestTwoFiles"), Cls("NSObject"), nil)
	registerFileA(d, &log)
	registerFileB(d, &log)
	if d.Class() != 0 {
		t.Fatal("the class exists before Register")
	}
	cls := d.Register()
	if cls == 0 || d.Class() != cls {
		t.Fatalf("Register = %v, Class = %v", cls, d.Class())
	}
	o := newObject(cls)

	if got := Send2(o, Sel("gnuiAdd:to:"), 40, 2); got != 42 {
		t.Errorf("gnuiAdd:to: = %d, want 42", got)
	}
	if got := Send(o, Sel("gnuiSelf")); got != o {
		t.Errorf("gnuiSelf = %v, want the receiver %v", got, o)
	}
	if got := Send1(o, Sel("gnuiNegate:"), 5); got != ID(^uintptr(5-1)) {
		t.Errorf("gnuiNegate: = %#x, want -5", uintptr(got))
	}
	if !SendBool(o, Sel("gnuiFlag")) {
		t.Error("gnuiFlag = NO")
	}
	if want := "add self negate flag"; strings.Join(log, " ") != want {
		t.Errorf("handlers ran as %q, want %q", strings.Join(log, " "), want)
	}
	// The ordinary NSObject methods are still there.
	if !SendBool1(o, Sel("isKindOfClass:"), uintptr(Cls("NSObject"))) {
		t.Error("the instance is not an NSObject")
	}
}

// Many selectors registered in an order that has nothing to do with their
// addresses: a lookup that only works for the first or last entry, or for an
// unsorted table, fails here.
func TestManySelectorsEachFindTheirOwnHandler(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)

	const n = 61
	d := Define(className("GNUITestMany"), Cls("NSObject"), nil)
	sels := make([]SEL, n)
	for k := 0; k < n; k++ {
		i := (k * 17) % n // a permutation of 0..n-1
		sels[i] = Sel(fmt.Sprintf("gnuiMany%d:", i))
		d.Method(sels[i], "Q@:Q", func(c *ffi.CB) { c.Ret(c.Int(2)*1000 + uintptr(i)) })
	}
	o := newObject(d.Register())
	for round := 0; round < 3; round++ {
		for i := n - 1; i >= 0; i-- {
			if got, want := uintptr(Send1(o, sels[i], uintptr(round+1))), uintptr(round+1)*1000+uintptr(i); got != want {
				t.Fatalf("selector %d, round %d: got %d, want %d", i, round, got, want)
			}
		}
	}
}

// Two classes can use one selector; each calls its own handler, and a class
// that did not define a selector does not run another class's.
func TestClassesKeepTheirOwnTables(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)

	shared := Sel("gnuiWho")
	only := Sel("gnuiOnlyInFirst")
	first := Define(className("GNUITestFirst"), Cls("NSObject"), nil)
	first.Method(shared, "Q@:", func(c *ffi.CB) { c.Ret(1) })
	first.Method(only, "Q@:", func(c *ffi.CB) { c.Ret(7) })
	second := Define(className("GNUITestSecond"), Cls("NSObject"), nil)
	second.Method(shared, "Q@:", func(c *ffi.CB) { c.Ret(2) })
	a, b := newObject(first.Register()), newObject(second.Register())
	if x, y := Send(a, shared), Send(b, shared); x != 1 || y != 2 {
		t.Errorf("gnuiWho = %d and %d, want 1 and 2", x, y)
	}
	if Send(a, only) != 7 {
		t.Error("the first class lost its own selector")
	}
	if SendBool1(b, Sel("respondsToSelector:"), uintptr(only)) {
		t.Error("the second class answers a selector only the first defined")
	}
}

func TestDefinitionErrorsPanic(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)

	panics := func(f func()) (p bool) {
		defer func() { p = recover() != nil }()
		f()
		return
	}
	h := func(*ffi.CB) {}
	name := className("GNUITestErrors")
	d := Define(name, Cls("NSObject"), nil)
	d.Method(Sel("gnuiTwice"), "v@:", h)
	if !panics(func() { d.Method(Sel("gnuiTwice"), "v@:", h) }) {
		t.Error("a selector defined twice did not panic")
	}
	if !panics(func() { d.Method(Sel("gnuiNil"), "v@:", nil) }) {
		t.Error("a nil handler did not panic")
	}
	d.Register()
	if !panics(func() { d.Method(Sel("gnuiLate"), "v@:", h) }) {
		t.Error("a method added after Register did not panic")
	}
	if !panics(func() { d.Register() }) {
		t.Error("registering twice did not panic")
	}
	if !panics(func() { Define(name, Cls("NSObject"), nil).Register() }) {
		t.Error("a second class with the same name did not panic")
	}
}

// A message to a Go method allocates nothing: the lookup is a search in a
// slice, and no closure is made per call.
func TestDispatchAllocatesNothing(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)

	var n uintptr
	d := Define(className("GNUITestAllocs"), Cls("NSObject"), nil)
	sel := Sel("gnuiCount:")
	d.Method(sel, "Q@:Q", func(c *ffi.CB) { n += c.Int(2); c.Ret(n) })
	for i := 0; i < 20; i++ {
		d.Method(Sel(fmt.Sprintf("gnuiPad%d", i)), "v@:", func(*ffi.CB) {})
	}
	o := newObject(d.Register())
	Send1(o, sel, 1) // warm up
	// The counters are process-wide: one clean round out of three passes (see
	// internal/ffi's TestZeroAlloc).
	var per float64
	for round := 0; round < 3; round++ {
		if per = testing.AllocsPerRun(2000, func() { Send1(o, sel, 1) }); per == 0 {
			return
		}
	}
	t.Errorf("a message allocated %v objects per call", per)
}

var (
	benchOnce sync.Once
	benchObj  ID
	benchSels []SEL
)

func BenchmarkDispatch(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	benchOnce.Do(func() { // the benchmark function runs once per b.N probe; a class is registered once
		d := Define("GNUIBenchDispatch", Cls("NSObject"), nil)
		for i := 0; i < 25; i++ { // as many methods as GNUIView has
			s := Sel(fmt.Sprintf("gnuiBench%d:", i))
			benchSels = append(benchSels, s)
			d.Method(s, "Q@:Q", func(c *ffi.CB) { c.Ret(c.Int(2)) })
		}
		benchObj = newObject(d.Register())
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Send1(benchObj, benchSels[i%len(benchSels)], 1)
	}
}

// BenchmarkLookup is the table search alone, for a table larger than any
// class is likely to have.
func BenchmarkLookup(b *testing.B) {
	x := &dispatcher{}
	for i := 0; i < 64; i++ {
		x.table = append(x.table, entry{SEL(0x1000 + 24*i), func(*ffi.CB) {}})
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if x.find(x.table[i%len(x.table)].sel) == nil {
			b.Fatal("lookup missed")
		}
	}
}

// find must miss selectors the class does not have, whatever their order
// relative to the table's.
func TestFindMisses(t *testing.T) {
	x := &dispatcher{}
	for i := 1; i <= 8; i++ {
		x.table = append(x.table, entry{SEL(10 * i), func(*ffi.CB) {}})
	}
	for _, s := range []SEL{0, 5, 15, 45, 85, 1000} {
		if x.find(s) != nil {
			t.Errorf("found %d", s)
		}
	}
	for i := 1; i <= 8; i++ {
		if x.find(SEL(10*i)) == nil {
			t.Errorf("lost %d", 10*i)
		}
	}
}
