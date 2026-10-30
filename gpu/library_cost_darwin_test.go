//go:build darwin

package gpu

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gonativeui/internal/objc"
)

// TestShaderLoadCost measures what making the shader libraries and
// pipelines costs when the system's Metal compiler caches miss, without
// clearing any cache: each source with a comment no process compiled
// before (the time and pid) misses the front end's cache (MSL to AIR) and
// the same again hits it; the pipelines from it hit the back end's (AIR to
// GPU code), since the comment leaves the AIR as it was. Each source with a
// branch no vertex or fragment takes, holding a number of this run's,
// misses both. Then, if a precompiled library is embedded, loading it and
// its pipelines. docs/measurements/metallib-macos.md has the numbers. Set
// GNUI_SHADER_COST=1 to run it, in fresh processes.
func TestShaderLoadCost(t *testing.T) {
	if os.Getenv("GNUI_SHADER_COST") == "" {
		t.Skip("set GNUI_SHADER_COST=1 to measure")
	}
	resetWarm()
	r := newTestRenderer(t)
	dev, F := r.device, objc.F
	ms := func(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }
	salt := fmt.Sprintf("// cost probe %d %d\n", time.Now().UnixNano(), os.Getpid())
	timed := func(f func() objc.ID) (objc.ID, time.Duration) {
		start := time.Now()
		id := f()
		return id, time.Since(start)
	}
	// For the back end, a branch no vertex or fragment takes, with a number
	// of this run's in it, changes each entry point's code.
	n := uint32(time.Now().UnixNano()|1<<20) + uint32(os.Getpid())
	code := func(src string, at ...[2]string) string {
		for _, a := range at {
			if !strings.Contains(src, a[0]) {
				t.Fatalf("no %q in the shader", a[0])
			}
			src = strings.Replace(src, a[0], a[0]+fmt.Sprintf(a[1], n), 1)
		}
		return src
	}
	quadNew := code(quadMSL,
		[2]string{"constant float2& viewport [[buffer(1)]]) {", "\n\tiid += vid == %du ? 1u : 0u;"},
		[2]string{"sampler smp [[sampler(0)]]) {", "\n\tif (in.pos.x < -%d.0) discard_fragment();"})
	bdNew := code(backdropMSL,
		[2]string{"constant BU& u [[buffer(0)]]) {", "\n\tif (vid == %du) return float4(0);"},
		[2]string{"sampler s [[sampler(0)]]) {", "\n\tif (pos.x < -%d.0) discard_fragment();"},
		[2]string{"texture2d<float> t [[texture(0)]]) {", "\n\tif (pos.x < -%d.0) discard_fragment();"})
	for _, s := range []struct{ name, src, salt string }{{"quad", quadMSL, salt}, {"backdrop", backdropMSL, salt}, {"quad, new code", quadNew, ""}, {"backdrop, new code", bdNew, ""}} {
		lib, miss := timed(func() objc.ID { return compileLibrary(F, dev, s.salt+s.src, &nsErrorOut) })
		if lib == 0 {
			t.Fatalf("%s: %s", s.name, nsErrorText())
		}
		again, hit := timed(func() objc.ID { return compileLibrary(F, dev, s.salt+s.src, &nsErrorOut) })
		objc.Release(again)
		start := time.Now()
		if strings.HasPrefix(s.name, "quad") {
			p, _ := quadPipelineOf(F, dev, lib, &nsErrorOut)
			objc.Release(p)
		} else {
			d, b, _ := backdropPipelinesOf(F, dev, lib, &nsErrorOut)
			objc.Release(d)
			objc.Release(b)
		}
		pipe := time.Since(start)
		objc.Release(lib)
		t.Logf("%s source: library on a miss %s, on a hit %s; pipelines from the missed library %s", s.name, ms(miss), ms(hit), ms(pipe))
	}
	shaderLib.once.Do(shaderLib.verify)
	if shaderLib.data == nil {
		t.Logf("precompiled: not measured (%s)", shaderLib.why)
		return
	}
	lib, load := timed(func() objc.ID { return libraryFromData(F, dev, shaderLib.data, &nsErrorOut) })
	if lib == 0 {
		t.Fatalf("precompiled library: %s", nsErrorText())
	}
	start := time.Now()
	p, _ := quadPipelineOf(F, dev, lib, &nsErrorOut)
	quad := time.Since(start)
	start = time.Now()
	d, b, _ := backdropPipelinesOf(F, dev, lib, &nsErrorOut)
	bd := time.Since(start)
	objc.Release(p)
	objc.Release(d)
	objc.Release(b)
	objc.Release(lib)
	t.Logf("precompiled (%d bytes): library %s, quad pipeline %s, backdrop pipelines %s", len(shaderLib.data), ms(load), ms(quad), ms(bd))
}
