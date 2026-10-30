# Precompiled Metal shaders (2026-10-30)

The macOS renderer has two MSL sources: `quadMSL` (`gpu/shaders_darwin.go`)
and `backdropMSL` (`gpu/backdrop_darwin.go`). Until now both were compiled
at run time with `newLibraryWithSource:`. That takes about 0.1 ms when the
system's Metal compiler cache answers, but 87–119 ms (quad) and 44–47 ms
(backdrop) when it misses
([measurements](../measurements/metallib-macos.md)). The renderer now loads
a precompiled library and keeps the source as a supported fallback.

The perf merge changes the quad source (texture tables, capsule segments and
shadow early-out). The previously committed library is removed and the
manifest marks it absent until CI regenerates it; the merge host has no Metal
compiler. Runtime compilation uses the merged source. Run the metallib
workflow on this branch, copy its tested artifact into `internal/metallib/lib/`
and commit both files. The strict `-check` gate remains enabled and fails
until that artifact is committed.

## The artifact

`internal/metallib/lib/` holds two files:

- `shaders.metallib`: both sources compiled to AIR and linked into one
  library. The quad and backdrop entry points (`vs_main`, `fs_main`,
  `bd_vs`, `bd_down`, `bd_blur`) are distinct and every helper is `static`,
  so the two translation units link without clashes.
- `manifest.json` (schema 1) records the library's size and SHA-256, the
  sources' hash `source_sha256`, and each source's own hash. It also
  records the first line of `xcrun -sdk macosx metal --version`, the SDK,
  the target (`air64-apple-macos14.0`: macOS 14 is the floor, D1), the
  language version (`metal3.1`, macOS 14's), the generation time,
  `generated_by` (`ci` or `local`) and the repository commit (`-dirty` when
  the shader files were modified).

`source_sha256` is the SHA-256 of `quadMSL`, a NUL byte and `backdropMSL`,
in hex (`metallib.SourceHash`). It is computed on the constants' values,
which `internal/metallib/mslsrc` reads from the Go files and gpu's test
checks against the compiled constants.

The library is embedded with `go:embed` on darwin only. The manifest is
embedded on every system. A Go consumer needs no C or Metal toolchain, and
`CGO_ENABLED=0` cross-compiles from Linux and Windows as before.

## Checked in, with a CI staleness check

The library is committed. CI does not produce it at build time, because
consumers of the module (`go get`, or Zap's `replace` to a checkout) build
from the source tree and can only embed files that are in it. A CI-only
artifact would leave every consumer on the source path.

Generating needs Xcode's Metal toolchain. The Command Line Tools do not
include it, so generation runs in `.github/workflows/metallib.yml` on a
`macos-15` runner, on changes to `gpu/`, `internal/metallib/`,
`tools/metallib/` or `provenance/`. That workflow:

1. runs `go run ./tools/metallib -by ci -out $RUNNER_TEMP/metallib` and
   uploads the result as the `metallib` artifact;
2. runs gpu's tests against it (`GNUI_TEST_METALLIB_DIR`): both libraries
   load precompiled and draw what the source draws, within 1 per channel;
3. runs `go run ./tools/metallib -check`, which fails when the committed
   manifest names no library, when the committed library is not the one
   its manifest names, or when the sources changed since it was generated;
4. prints `gnui provenance -json`.

To update the library, unpack the `metallib` artifact into
`internal/metallib/lib/` and commit it. Libraries are not compared byte for
byte with a fresh compile, because the compiler's output changes between
Xcode releases. The source hash is what makes a library current.

Until the first library is committed, the manifest is `{"schema": 1}`. That
is the supported "absent" state: the renderer compiles the source and
`-check` fails, which keeps the gap visible.

## Loading and the fallback

`gpu/library_darwin.go` decides once per process. It loads the library
with `dispatch_data_create` (a copy) and `newLibraryWithData:error:` when:

- the manifest parses;
- it names a library, the library is present, and its SHA-256 matches;
- its `source_sha256` matches the compiled sources;
- `GNUI_METAL_SOURCE=1` is not set.

Otherwise, or when Metal refuses the library on this system, the renderer
compiles the source as before. `gpu.ShaderLoads()` returns, for `quad` and
`backdrop`, whether the library was precompiled, the reason when it was
not, and the time taken. An embedded library that cannot be used (missing,
corrupt, stale or refused) also prints one line to stderr. An absent
library and the override are silent. The tests cover every case, with the
refused case using bytes that Metal reports as "Invalid library file".

`gpu.Prewarm` stays. The precompiled library removes the front-end
compile, but not the pipeline states: Metal compiles their AIR to GPU code,
which takes about 0.2 ms from its own cache and about 32 ms (quad) plus
16 ms (backdrop) on a miss. That miss is expected at the first launch of a
new library.
