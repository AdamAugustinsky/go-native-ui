## Plan

I'll fix the **failing test** in `ui/richtext.go` first, then *check* the rest.

1. Read the test
2. Patch `layout()`:
   - keep the cache
   - drop ~~the old path~~
3. Run `go test ./ui/...`

See https://github.com/yuin/goldmark and [the guide](docs/guide/richtext.md "Rich text").
