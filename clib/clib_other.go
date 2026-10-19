//go:build !((darwin || linux) && (arm64 || amd64))

package clib

const (
	supported     = false
	intArgRegs    = 8
	floatArgRegs  = 8
	stackArgWords = 12
)

// frame stands in for internal/ffi's call block where there is none. Open
// fails, so no Func can be called; Call still builds arguments, so code
// that shares a call site across systems compiles everywhere.
type frame struct{}

func (*frame) Reset()                {}
func (*frame) Arg(uintptr)           {}
func (*frame) Arg32(uint32)          {}
func (*frame) ArgNarrow(uint32, int) {}
func (*frame) ArgF32(float32)        {}
func (*frame) ArgF64(float64)        {}
func (*frame) Call(uintptr)          { panic(ErrUnsupported) }
func (*frame) R0() uintptr           { return 0 }
func (*frame) F64(int) float64       { return 0 }

func dlopen(string) (uintptr, error)         { return 0, ErrUnsupported }
func dlsym(uintptr, string) (uintptr, error) { return 0, ErrUnsupported }
func dlclose(uintptr) error                  { return ErrUnsupported }
