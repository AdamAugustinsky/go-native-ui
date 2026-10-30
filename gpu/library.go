package gpu

import (
	"sync"
	"time"
)

// ShaderLoad says how one of the renderer's shader libraries was made, the
// first time this process made it (macOS: "quad" at the first renderer,
// "backdrop" at the first backdrop).
type ShaderLoad struct {
	Library     string        // "quad" or "backdrop"
	Precompiled bool          // loaded from the embedded shaders.metallib
	Reason      string        // why the source was compiled instead; empty if Precompiled
	Took        time.Duration // making the library, without its pipelines
}

var (
	loadsMu sync.Mutex
	loads   []ShaderLoad
)

// ShaderLoads returns how the shader libraries made so far were made, in
// order. On macOS the renderer loads the precompiled library embedded in
// the framework (internal/metallib) and compiles the shader source only if
// it is absent, does not match the source, or does not load, or if the
// environment sets GNUI_METAL_SOURCE=1; the reason is also printed to
// stderr when an embedded library is unusable. Windows and Linux: none.
func ShaderLoads() []ShaderLoad {
	loadsMu.Lock()
	defer loadsMu.Unlock()
	return append([]ShaderLoad(nil), loads...)
}

func recordLoad(l ShaderLoad) {
	loadsMu.Lock()
	loads = append(loads, l)
	loadsMu.Unlock()
}
