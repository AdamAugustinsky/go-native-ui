//go:build darwin

package objc

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

func kept() (c, cl, s int) {
	internMu.RLock()
	defer internMu.RUnlock()
	return len(cstrs), len(classes), len(sels)
}

// inPool runs f on the main thread inside an autorelease pool.
func inPool(f func()) {
	onMain(func() {
		pool := F.Call0(PoolPush)
		defer F.Call1(PoolPop, pool)
		f()
	})
}

func TestStrKeepsAndAllocatesNothing(t *testing.T) {
	var before, after int
	var allocs float64
	inPool(func() {
		Str("warm up") // caches nothing per string; everything else was cached in init
		before, _, _ = kept()
		allocs = testing.AllocsPerRun(10000, func() {
			if Str("a notification title — with ünïcode and emoji 🙂") == 0 {
				t.Error("Str returned nil")
			}
		})
		after, _, _ = kept()
	})
	if allocs != 0 {
		t.Errorf("Str allocated %v times per call, want 0", allocs)
	}
	if before != after {
		t.Errorf("Str kept %d C strings in 10,000 calls", after-before)
	}
}

func TestStrLoopDoesNotGrowKeptStrings(t *testing.T) {
	inPool(func() {
		before, _, _ := kept()
		for i := range 10000 {
			Str(fmt.Sprintf("title %d", i))
		}
		after, _, _ := kept()
		if before != after {
			t.Errorf("10,000 distinct Str calls kept %d C strings", after-before)
		}
	})
}

func TestClsSelAllocateNothingOnRepeat(t *testing.T) {
	names := []string{"NSObject", "NSString", "NSArray"}
	sel := []string{"alloc", "init", "description"}
	for i := range names {
		Cls(names[i])
		Sel(sel[i])
	}
	before, bcl, bsel := kept()
	allocs := testing.AllocsPerRun(1000, func() {
		for i := range names {
			if Cls(names[i]) == 0 || Sel(sel[i]) == 0 {
				t.Error("lookup failed")
			}
		}
	})
	if allocs != 0 {
		t.Errorf("Cls/Sel allocated %v times per run, want 0", allocs)
	}
	after, acl, asel := kept()
	if before != after || bcl != acl || bsel != asel {
		t.Errorf("repeat lookups grew the tables: cstrs %d→%d classes %d→%d sels %d→%d", before, after, bcl, acl, bsel, asel)
	}
	if Cls("NSObject") != Cls("NSObject") || Sel("init") != Sel("init") {
		t.Error("repeat lookups differ")
	}
}

var lateSeq atomic.Int32

func TestMissingClassIsNotCachedAsFound(t *testing.T) {
	name := fmt.Sprintf("GNUILateClass%d", lateSeq.Add(1))
	if Cls(name) != 0 {
		t.Fatal("found a class that does not exist")
	}
	d := Define(name, Cls("NSObject"), nil)
	cls := d.Register()
	if got := Cls(name); got != cls || got == 0 {
		t.Errorf("a class registered after a failed lookup: Cls = %v, want %v", got, cls)
	}
}

func TestCStringIsInterned(t *testing.T) {
	a, b := CString("v@:"), CString(strings.Clone("v@:"))
	if a != b {
		t.Error("equal strings gave different C strings")
	}
	if *a != 'v' || *(*byte)(unsafe.Add(unsafe.Pointer(a), 3)) != 0 {
		t.Error("not NUL-terminated")
	}
}

// Cls, Sel and CString take any goroutine; each goroutine here asks for the
// same names and its own, and all must agree.
func TestInternConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	wantCls, wantSel := Cls("NSObject"), Sel("copy")
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				if Cls("NSObject") != wantCls || Sel("copy") != wantSel {
					t.Error("concurrent lookups disagree")
					return
				}
				n := fmt.Sprintf("gnuiConcurrent%d:%d:", g, i%20)
				if Sel(n) != Sel(n) || Sel(n) == 0 || *CString(n) != 'g' {
					t.Error("concurrent registration failed")
					return
				}
				Cls("NSString")
				Cls("GNUINoSuchClass")
			}
		}()
	}
	wg.Wait()
}

func TestStrRoundTrip(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{"", ""},
		{"ünïcode — ok", "ünïcode — ok"},
		{"emoji 🙂👩‍👩‍👧 end", "emoji 🙂👩‍👩‍👧 end"},
		{"a\x00b", "a\x00b"},                       // NUL is a character, not a terminator
		{"bad \xff\xfe bytes", "bad \uFFFD bytes"}, // invalid UTF-8 becomes U+FFFD, never nil
		{strings.Repeat("x", 100000), strings.Repeat("x", 100000)},
	}
	inPool(func() {
		for _, c := range cases {
			s := Str(c.in)
			if s == 0 {
				t.Errorf("Str(%q) = nil", c.in)
				continue
			}
			if got := string(AppendUTF8(nil, s)); got != c.want {
				t.Errorf("Str(%q) reads back %q, want %q", c.in, got, c.want)
			}
		}
	})
}
