//go:build darwin

package cf

import (
	"bytes"
	"sync"
	"testing"
)

func TestStringDataRoundTrip(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	fr := Acquire()
	defer Release(fr)
	for _, s := range []string{"", "plain", "caf\u00e9 \u4e16\u754c \U0001F600", "a b/c%d"} {
		cs := String(fr, s)
		if cs == 0 {
			t.Fatalf("String(%q) = nil", s)
		}
		if got := GoString(fr, cs); got != s {
			t.Errorf("GoString(String(%q)) = %q", s, got)
		}
		CFRelease(fr, cs)
	}
	for _, b := range [][]byte{nil, {0}, {1, 2, 3, 0, 255}, bytes.Repeat([]byte{7}, 5000)} {
		d := Data(fr, b)
		if got := DataBytes(fr, d); !bytes.Equal(got, b) {
			t.Errorf("DataBytes(Data(%d bytes)) = %d bytes", len(b), len(got))
		}
		CFRelease(fr, d)
	}
}

func TestDictAndArray(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	fr := Acquire()
	defer Release(fr)
	a, b := String(fr, "one"), String(fr, "two")
	arr := Array(fr, a, b)
	if n := ArrayLen(fr, arr); n != 2 {
		t.Fatalf("ArrayLen = %d", n)
	}
	if got := GoString(fr, ArrayAt(fr, arr, 1)); got != "two" {
		t.Errorf("ArrayAt(1) = %q", got)
	}
	d := Dict(fr)
	DictSet(fr, d, a, b)
	CFRelease(fr, d)
	CFRelease(fr, arr)
	CFRelease(fr, a)
	CFRelease(fr, b)
}

func TestObjCOffMainThread(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fr := Acquire()
			defer Release(fr)
			for j := 0; j < 200; j++ {
				pop := Pool(fr)
				cs := String(fr, "h\u00e9llo")
				// NSString and CFString are one object: send it a message.
				if n := Msg(fr, cs, Sel(fr, "length")); n != 5 {
					t.Errorf("[cfstring length] = %d", n)
				}
				up := Msg(fr, cs, Sel(fr, "uppercaseString"))
				if got := GoString(fr, up); got != "H\u00c9LLO" {
					t.Errorf("uppercaseString = %q", got)
				}
				if Cls(fr, "NSString") == 0 {
					t.Error("no NSString class")
				}
				CFRelease(fr, cs)
				if Cls(fr, "NSNoSuchClass") != 0 {
					t.Error("found a class that does not exist")
				}
				pop()
			}
		}()
	}
	wg.Wait()
}

func BenchmarkObjCLookup(b *testing.B) {
	if err := Init(); err != nil {
		b.Fatal(err)
	}
	fr := Acquire()
	defer Release(fr)
	Cls(fr, "NSString")
	Sel(fr, "length")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		Cls(fr, "NSString")
		Sel(fr, "length")
	}
}

func TestLookupRepeatAllocatesNothing(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	fr := Acquire()
	defer Release(fr)
	wantClass, wantSel := Cls(fr, "NSString"), Sel(fr, "length")
	if n := testing.AllocsPerRun(100, func() {
		if Cls(fr, "NSString") != wantClass || Sel(fr, "length") != wantSel {
			t.Error("unstable lookup")
		}
	}); n != 0 {
		t.Errorf("%v allocations", n)
	}
}
