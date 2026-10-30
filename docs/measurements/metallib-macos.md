# Shader library cost on a cache miss: source compile and pipelines (2026-10-30)

**Machine:** Mac mini (Apple M4, macOS 27.2), Go 1.27.2, `CGO_ENABLED=0`.
Other agents were using the machine at the same time: the load average was
3.1 at the first run and 8.6 at the second.

## Method

The data comes from `TestShaderLoadCost` in `gpu/library_cost_darwin_test.go`
(`GNUI_SHADER_COST=1`). It was built once with `go test -c` and run 5 times,
each in a fresh process. No cache was cleared. A miss is forced by
compiling a source that no process compiled before:

- **Front-end miss (MSL to AIR):** the real source with a unique first-line
  comment (time and pid). The AIR is unchanged, so the pipelines then hit
  the back end's cache.
- **Front- and back-end miss:** the source with a never-taken branch
  holding a per-run number in each entry point (`iid += vid == N ? 1 : 0`,
  `if (pos.x < -N) discard_fragment()`). Each entry point's code changes, so
  the pipeline state compile (AIR to GPU code) misses too.
- **Hit:** the same library compiled again in the same process.

Times are wall clock around `newLibraryWithSource:options:error:` and
around `newRenderPipelineStateWithDescriptor:error:` (one pipeline for
quad, two for backdrop).

## Results (5 fresh processes)

| case | quad | backdrop |
|---|---:|---:|
| library, front-end miss (comment) | 106.7–118.8 ms | 44.6–47.2 ms |
| library, front-end miss (new code) | 86.1–90.1 ms | 44.1–46.2 ms |
| library, cache hit | 0.0–0.1 ms | 0.0–0.1 ms |
| pipelines, back end hit (after comment miss) | 0.2 ms | 0.2 ms |
| pipelines, back end miss (new code) | 32.1–45.3 ms | 15.7–17.1 ms |
| precompiled `newLibraryWithData:` + pipelines | **not measured** | **not measured** |

The quad's first compile in each process (the "comment" row) is 20–30 ms
slower than its second (the "new code" row). This is inferred, not
measured: the first request most likely also pays the compiler service's
start-up.

## Not measured: loading the precompiled library

This Mac has only the Command Line Tools, which do not include
`xcrun metal`, so no `.metallib` could be generated here. I tried a
runtime route: an `MTLDynamicLibrary` serialized with `serializeToURL:`
loads as an `MTLLibrary`, but asking it for `vs_main` aborts in Metal's
`validateMTLFunctionType`. Dynamic libraries do not carry render entry
points, so that route does not give a comparable number. Once CI has
committed the first `shaders.metallib`, `GNUI_SHADER_COST=1 go test -count=1
-run TestShaderLoadCost -v ./gpu` measures its load and pipelines on the
same machine.

## What follows (inferred)

- The precompiled library removes the front-end compile on a miss: about
  90–120 ms (quad) and 45 ms (backdrop) here.
- The pipeline states stay a real cost. A new library's AIR has not been
  seen by the back-end cache at its first launch, so that launch pays about
  32–45 ms (quad) and 16 ms (backdrop), on either path. Later launches pay
  about 0.2 ms. `gpu.Prewarm` therefore stays and runs both off the main
  thread.
