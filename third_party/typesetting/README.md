# go-text/typesetting v0.3.5, patched

The packages of `github.com/go-text/typesetting` at v0.3.5 that the Linux
text engine uses (`text/engine.go`, `text/shape.go`). The root `go.mod`
points the module here with `replace github.com/go-text/typesetting => ./third_party/typesetting`.
See DECISIONS.md D23.

## Contents

The non-test `.go` files of these packages:

- `bidi`, `di`, `font`, `font/cff`, `font/cff/interpreter`
- `font/opentype`, `font/opentype/tables`
- `harfbuzz`, `internal/unicodedata`, `language`, `segmenter`, `shaping`

Also included:

- `font/testdata`: the three fonts our engine tests use, with their licences;
- the licences: `LICENSE` (Unlicense OR BSD-3-Clause) and `harfbuzz/LICENSE` (MIT, from HarfBuzz).

go-text's own tests and test data are not copied.

## Our change

`zeroalloc.patch` makes `harfbuzz.Buffer.Shape` and `bidi.Paragraph.Segment` allocate nothing per call once warm, and so does extracting a TrueType (glyf) outline:

- `harfbuzz/ot_layout_gsubgpos.go`: `matcherFunc` is a small struct, not a per-lookup closure.
- `harfbuzz/ot_shape_normalize.go`, `harfbuzz/buffer.go`: the normalize context lives in a field on `Buffer`, and the shaper is called directly.
- `harfbuzz/ot_layout_gsub.go`, `ot_layout_gpos.go`, `ot_layout_gsubgpos.go`: recursive lookups use a per-level scratch array in the context and a pointer to the lookup, not a stack array stored in the context and a boxed copy.
- `harfbuzz/ot_shape_arabic.go`: the Arabic fallback plan is kept on the shape plan once built (upstream rebuilt it on every call), with its apply context.
- `bidi/bidi.go`, `core.go`, `bracket.go`: level runs, isolating run sequences and bracket pairs live in scratch buffers on `Paragraph`; the bracket opener stack is an array, not a `container/list`. The regression test is `text/rtl_alloc_test.go`.
- `font/glyphs.go`, `font/renderer.go`, `font/font.go`: `Face.GlyphDataOutline` for glyf outlines extracts into scratch storage on the `Face` (points per nesting depth, loop guard, segments), so a glyph the engine rasterizes for the first time, or at a new subpixel offset, allocates nothing once the storage has grown. The returned segments are valid until the next extraction on the same `Face`. CFF outlines still allocate. The regression test is `text/glyph_alloc_test.go`.
- `harfbuzz/aat_layout.go`, `harfbuzz/buffer.go`: the AAT/kern apply context (fonts with a `kern`, `kerx`, `morx` or `trak` table) lives in a field on `Buffer` and keeps its glyph set's storage; upstream made one, and a glyph set, on every shape. The regression test is `harfbuzz/zeroalloc_test.go`, which CI runs from the root module (`go test github.com/go-text/typesetting/harfbuzz`).
- `font/opentype/tables/ot_layout.go`: `parseValueRecord` reads into a 16-element stack array. Sixteen because the size counts every format bit, reserved ones included; the regression test is `font/opentype/tables/zeroalloc_test.go`, which CI runs.

`forgetface.patch` (C9, 2026-10-20) adds two methods to `harfbuzz.Buffer`, `ForgetFace` and `CachedFaces`. Upstream's plan cache keeps every face it has shaped with alive; our text engine releases a zoom level's faces (`ReleaseScale` in `text/engine.go`) and drops their plans with `ForgetFace`. `TestEngineReleaseScaleBoundsThePlanCache` checks it.

## Updating

1. Copy the same package set from the new upstream version.
2. Re-apply `zeroalloc.patch` and `forgetface.patch`.
3. Run `go test -tags textengine ./text/` and `go test github.com/go-text/typesetting/harfbuzz` from the root. Its exact zero-allocation test (`TestEngineMissAllocsAreHarfbuzzsAlone`) and the reference-pipeline tests in `text/shape_test.go` must pass.
