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
| precompiled `newLibraryWithData:`, back end warm (one run) | library 0.0 ms, pipeline 0.1 ms | pipelines 0.1 ms |

The quad's first compile in each process (the "comment" row) is 20–30 ms
slower than its second (the "new code" row). This is inferred, not
measured: the first request most likely also pays the compiler service's
start-up.

## Loading the precompiled library (2026-10-29)

The first `shaders.metallib` (32,607 bytes, SHA-256 `079d0273…339f`)
came from the `metallib` workflow's run on PR #41 (Apple metal
32023.620, macOS SDK 15.5, `air64-apple-macos14.0`, `-std=metal3.1`); this
Mac has only the Command Line Tools, so it could not make one. One run of
`GNUI_SHADER_COST=1 go test -count=1 -run TestShaderLoadCost -v ./gpu`,
right after `go test ./gpu` had loaded the same library: the library
loaded in 0.0 ms and the quad and backdrop pipelines took 0.1 ms each,
against 86.9–159.5 ms (quad) and 45.0–47.2 ms (backdrop) to compile the
source on a miss in the same run.

That number has the back end warm: the earlier tests had already built
pipelines from this library. The first launch of a new library on a Mac
also pays the back-end compile (the "pipelines, back end miss" row, about
32–45 ms and 16 ms). Showing that for the precompiled path would need the
system's shader caches cleared, which this run did not do. An earlier
attempt to make a library without the toolchain (an `MTLDynamicLibrary`
serialized with `serializeToURL:`) loaded, but asking it for `vs_main`
aborted in Metal's `validateMTLFunctionType`.

## What follows (inferred)

- The precompiled library removes the front-end compile on a miss: about
  90–120 ms (quad) and 45 ms (backdrop) here.
- The pipeline states stay a real cost. A new library's AIR has not been
  seen by the back-end cache at its first launch, so that launch pays about
  32–45 ms (quad) and 16 ms (backdrop), on either path. Later launches pay
  about 0.2 ms. `gpu.Prewarm` therefore stays and runs both off the main
  thread.
