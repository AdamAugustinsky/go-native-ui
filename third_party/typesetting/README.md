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

`zeroalloc.patch` makes `harfbuzz.Buffer.Shape` allocate nothing per call:

- `harfbuzz/ot_layout_gsubgpos.go`: `matcherFunc` is a small struct, not a per-lookup closure.
- `harfbuzz/ot_shape_normalize.go`, `harfbuzz/buffer.go`: the normalize context lives in a field on `Buffer`, and the shaper is called directly.
- `font/opentype/tables/ot_layout.go`: `parseValueRecord` reads into a 16-element stack array. Sixteen because the size counts every format bit, reserved ones included; the regression test is `font/opentype/tables/zeroalloc_test.go`, which CI runs.

## Updating

1. Copy the same package set from the new upstream version.
2. Re-apply `zeroalloc.patch`.
3. Run `go test -tags textengine ./text/`. Its exact zero-allocation test (`TestEngineMissAllocsAreHarfbuzzsAlone`) and the reference-pipeline tests in `text/shape_test.go` must pass.
