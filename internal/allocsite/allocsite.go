// Package allocsite attributes the heap allocations of a stretch of a
// program to the code that made them, for the allocation gates (DECISIONS
// D27): an allocation the Go runtime makes for itself (a new thread, the
// scavenger's timer heap, the type-assertion cache) is a one-off that a gate
// may pass over; one made by our code is not, whichever window it lands in.
//
// Enable profiles every allocation. A Snapshot taken after a runtime.GC holds
// every allocation up to that collection (the runtime publishes its profile at
// GC), so the allocations between two such snapshots are their difference.
// Profiling does not allocate on the heap (the runtime keeps its buckets in
// persistent memory), so it leaves ReadMemStats deltas exact.
package allocsite

import (
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
)

// Enable profiles every allocation from now on and returns a function that
// restores the previous rate. With a rate of 1 every allocation is recorded
// at once, so it may be called at any point of a run.
func Enable() (restore func()) {
	old := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	return func() { runtime.MemProfileRate = old }
}

// A Snapshot is the allocation profile as of the latest collection.
type Snapshot struct {
	recs []runtime.MemProfileRecord
	n    int
}

// Take reads the profile into s. It allocates only when s has no room for
// the profile's records; call it once before the stretch to measure, so that
// the storage is made then.
func (s *Snapshot) Take() {
	for {
		n, ok := runtime.MemProfile(s.recs, true)
		if ok {
			s.n = n
			return
		}
		s.recs = make([]runtime.MemProfileRecord, n+n/2+256)
	}
}

// Class says whose an allocation is.
type Class string

const (
	// Runtime is an allocation the Go runtime made for its own purposes: the
	// first frame past the allocator is in package runtime.
	Runtime Class = "runtime"
	// Ours is an allocation of gonativeui code or of the program's main
	// package, made directly or through a library it called.
	Ours Class = "ours"
	// Other is an allocation of other code that no frame of ours called (a
	// goroutine of the testing package, say).
	Other Class = "other"
)

// A Site is the allocations of one call stack between two snapshots.
type Site struct {
	Objects int64
	Bytes   int64
	Class   Class
	// Func is the function that allocated: the first frame past the
	// allocator and the runtime's helpers that allocate for their caller
	// (growslice, mapassign, newproc and the like).
	Func  string
	Stack []string `json:",omitempty"` // function and line of each frame, innermost first
}

// Diff returns the allocations made between before and after, by call
// stack, the most objects first. Allocations of this package (taking a
// snapshot) are left out.
func Diff(before, after *Snapshot) []Site {
	type key = [32]uintptr
	type count struct{ objects, bytes int64 }
	base := make(map[key]count, before.n)
	for _, r := range before.recs[:before.n] {
		c := base[r.Stack0]
		base[r.Stack0] = count{c.objects + r.AllocObjects, c.bytes + r.AllocBytes}
	}
	grew := make(map[key]count)
	for _, r := range after.recs[:after.n] {
		c := grew[r.Stack0]
		grew[r.Stack0] = count{c.objects + r.AllocObjects, c.bytes + r.AllocBytes}
	}
	var sites []Site
	for k, c := range grew {
		b := base[k]
		if c.objects <= b.objects {
			continue
		}
		pcs := k[:]
		if i := slices.Index(pcs, 0); i >= 0 {
			pcs = pcs[:i]
		}
		s, own := classify(pcs)
		if own {
			continue
		}
		s.Objects, s.Bytes = c.objects-b.objects, c.bytes-b.bytes
		sites = append(sites, s)
	}
	slices.SortFunc(sites, func(a, b Site) int {
		if a.Objects != b.Objects {
			return int(b.Objects - a.Objects)
		}
		return strings.Compare(a.Func, b.Func)
	})
	return sites
}

// Objects returns how many of the sites' objects are of class c.
func Objects(sites []Site, c Class) int64 {
	var n int64
	for _, s := range sites {
		if s.Class == c {
			n += s.Objects
		}
	}
	return n
}

// classify names the stack's allocating function and its class; own is true
// for an allocation of this package.
func classify(pcs []uintptr) (s Site, own bool) {
	frames := runtime.CallersFrames(pcs)
	ours, past := false, false
	for {
		f, more := frames.Next()
		fn := f.Function
		if fn != "" {
			if !past && !allocHelper(fn) {
				// Only this package's own allocations: During's fn runs
				// under it too.
				if strings.HasPrefix(fn, "gonativeui/internal/allocsite.") {
					return Site{}, true
				}
				past, s.Func = true, fn
			}
			s.Stack = append(s.Stack, fmt.Sprintf("%s %s:%d", fn, f.File, f.Line))
			ours = ours || isOurs(fn)
		}
		if !more {
			break
		}
	}
	switch {
	case s.Func == "" || pkgOf(s.Func) == "runtime" || strings.HasPrefix(s.Func, "internal/runtime/"):
		s.Class = Runtime
	case ours:
		s.Class = Ours
	default:
		s.Class = Other
	}
	return s, false
}

func isOurs(fn string) bool {
	return strings.HasPrefix(fn, "gonativeui/") || strings.HasPrefix(fn, "main.")
}

// pkgOf returns the import path of a function name ("runtime.mallocgc",
// "gonativeui/app.(*App).loop").
func pkgOf(fn string) string {
	slash := strings.LastIndexByte(fn, '/')
	if dot := strings.IndexByte(fn[slash+1:], '.'); dot >= 0 {
		return fn[:slash+1+dot]
	}
	return fn
}

// allocHelper reports whether fn allocates on behalf of its caller, so the
// allocation is the caller's: the allocator itself, and the runtime's
// conversions, slices, maps, strings, closures, defers and goroutines that
// compiled code calls.
func allocHelper(fn string) bool {
	if strings.HasPrefix(fn, "internal/runtime/maps.") || strings.HasPrefix(fn, "reflect.") && strings.Contains(fn, "unsafe_New") {
		return true
	}
	name, ok := strings.CutPrefix(fn, "runtime.")
	if !ok {
		return false
	}
	for _, p := range [...]string{
		"mallocgc", "newobject", "newarray", "makeslice", "growslice", "makemap",
		"mapassign", "rawstring", "rawbyteslice", "rawruneslice", "concatstring",
		"slicebytetostring", "stringtoslice", "slicerunetostring", "intstring",
		"convT", "newproc", "malg", "newdefer", "deferproc", "makechan",
		"mapclone", "growWork", "hashGrow", "reflect_unsafe_New", "unsafe_New",
		"profilealloc", "mProf_Malloc", "heapSetType", "nextFreeFast",
	} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// Write prints the sites as indented call stacks.
func Write(w io.Writer, sites []Site) {
	for _, s := range sites {
		fmt.Fprintf(w, "  %d object(s), %d bytes (%s) at:\n", s.Objects, s.Bytes, s.Class)
		for _, f := range s.Stack {
			fmt.Fprintf(w, "    %s\n", f)
		}
	}
}

// During runs fn and returns the objects our code allocated in it, and every
// site (ours, the runtime's, others') as Write prints them. It collects
// garbage before and after fn.
func During(fn func()) (ours int64, sites string) {
	defer Enable()()
	var s0, s1 Snapshot
	s0.Take()
	s1.Take()
	runtime.GC()
	s0.Take()
	fn()
	runtime.GC()
	s1.Take()
	d := Diff(&s0, &s1)
	var w strings.Builder
	Write(&w, d)
	return Objects(d, Ours), w.String()
}
