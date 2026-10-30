//go:build darwin

package gpu

import (
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/metallib"
	"gonativeui/internal/objc"
)

// Shader libraries (macOS). The renderer's two shader sources, quadMSL and
// backdropMSL, are embedded precompiled as one .metallib beside a manifest
// (internal/metallib, written by tools/metallib on a Mac with Xcode):
// newLibrary loads it with newLibraryWithData:, which skips the Metal
// compiler's front end, once for the quad pipeline and once for the
// backdrop ones. It compiles the source instead when the library is absent,
// is not the one the manifest names (its SHA-256), was compiled from other
// sources (a shader edited since; CI fails on that too) or does not load on
// this system, or when GNUI_METAL_SOURCE=1 asks for the source. The
// pipelines are built from either the same way, and cost Metal's back end
// on both paths (prewarm_darwin.go).

var (
	rsNewLibraryData = objc.Sel("newLibraryWithData:error:")
	rsUTF8String     = objc.Sel("UTF8String")
	rsLengthOfBytes  = objc.Sel("lengthOfBytesUsingEncoding:")

	// shaderFS holds lib/manifest.json and the library it names (tests
	// replace it).
	shaderFS fs.FS = metallib.Files

	shaderLib              libState
	quadSeen, backdropSeen atomic.Bool // recorded in ShaderLoads

	dispatchOnce                        sync.Once
	dispatchDataCreate, dispatchRelease uintptr
)

// libState is what newLibrary decided about the embedded library, once per
// process.
type libState struct {
	once sync.Once
	data []byte // the verified .metallib; nil: compile the source
	why  string // why data is nil
	warn bool   // a library is embedded but cannot be used: say so on stderr
}

// verify decides whether the embedded library can be loaded for the
// current sources.
func (s *libState) verify() {
	if os.Getenv("GNUI_METAL_SOURCE") == "1" {
		s.why = "GNUI_METAL_SOURCE=1"
		return
	}
	b, err := fs.ReadFile(shaderFS, "lib/manifest.json")
	m, ok := metallib.Parse(b)
	if err != nil || !ok {
		s.why, s.warn = "no readable manifest", true
		return
	}
	if m.File == "" {
		s.why = "no precompiled library" // none generated yet: the source is the supported path
		return
	}
	data, err := fs.ReadFile(shaderFS, "lib/"+m.File)
	switch {
	case err != nil:
		s.why = "the manifest names " + m.File + ", which is missing"
	case metallib.Hash(data) != m.SHA256:
		s.why = m.File + " is not the library the manifest names (SHA-256)"
	case metallib.SourceHash(quadMSL, backdropMSL) != m.SourceSHA256:
		s.why = m.File + " was compiled from other shader sources (stale)"
	default:
		s.data = data
		return
	}
	s.warn = true
}

// newLibrary makes the shader library for name ("quad" or "backdrop") on
// device: the precompiled one if it can, else src compiled (owned; 0 on
// failure, the NSError in *errOut), on frame fr.
func newLibrary(fr *ffi.Frame, device objc.ID, name, src string, errOut *uintptr) objc.ID {
	s := &shaderLib
	s.once.Do(s.verify)
	start := time.Now()
	var lib objc.ID
	why, warn := s.why, s.warn
	if s.data != nil {
		if lib = libraryFromData(fr, device, s.data, errOut); lib == 0 {
			why, warn = "the metallib did not load: "+errorText(fr, *errOut), true
		}
	}
	precompiled := lib != 0
	if lib == 0 {
		lib = compileLibrary(fr, device, src, errOut)
	}
	seen := &quadSeen
	if name == "backdrop" {
		seen = &backdropSeen
	}
	if lib != 0 && seen.CompareAndSwap(false, true) {
		recordLoad(ShaderLoad{Library: name, Precompiled: precompiled, Reason: why, Took: time.Since(start)})
		if warn {
			fmt.Fprintf(os.Stderr, "gpu: %s shaders: %s; compiled the source instead\n", name, why)
		}
	}
	return lib
}

// compileLibrary compiles src on device (owned; 0 on failure, the NSError
// in *errOut), on frame fr.
func compileLibrary(fr *ffi.Frame, device objc.ID, src string, errOut *uintptr) objc.ID {
	s := nsString(fr, src)
	defer msg(fr, s, selRelease)
	*errOut = 0
	return msg(fr, device, rsNewLibrary, uintptr(s), 0, uintptr(unsafe.Pointer(errOut)))
}

// libraryFromData loads a .metallib's bytes on device (owned; 0 on failure,
// the NSError in *errOut), on frame fr.
func libraryFromData(fr *ffi.Frame, device objc.ID, b []byte, errOut *uintptr) objc.ID {
	*errOut = 0
	dispatchOnce.Do(func() {
		sys, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		dispatchDataCreate, _ = purego.Dlsym(sys, "dispatch_data_create")
		dispatchRelease, _ = purego.Dlsym(sys, "dispatch_release")
	})
	if dispatchDataCreate == 0 || dispatchRelease == 0 {
		return 0
	}
	// No queue, the default destructor: dispatch copies the bytes now.
	data := fr.Call4(dispatchDataCreate, uintptr(unsafe.Pointer(unsafe.SliceData(b))), uintptr(len(b)), 0, 0)
	runtime.KeepAlive(b)
	if data == 0 {
		return 0
	}
	defer fr.Call1(dispatchRelease, data)
	return msg(fr, device, rsNewLibraryData, data, uintptr(unsafe.Pointer(errOut)))
}

// errorText is an NSError's localized description, on frame fr.
func errorText(fr *ffi.Frame, nsError uintptr) string {
	if nsError == 0 {
		return "unknown error"
	}
	str := msg(fr, objc.ID(nsError), rsLocalizedErr)
	p := uintptr(msg(fr, str, rsUTF8String))
	n := int(msg(fr, str, rsLengthOfBytes, 4)) // NSUTF8StringEncoding
	if p == 0 || n == 0 {
		return "unknown error"
	}
	return string(unsafe.Slice((*byte)(ffi.PtrFromC(p)), n))
}
