# Build provenance for app packagers (2026-10-30)

Package `gonativeui/provenance` and `gnui provenance` describe the framework
build an application ships. The description is a small JSON document,
schema `gnui.provenance/v1`, for release tooling such as Zap's release
agent.

## Why BuildInfo is not enough

An app that uses the framework through `replace gonativeui => <checkout>`
(Zap does this) sees the framework in `runtime/debug.BuildInfo` as a
replaced module at version `(devel)`, with no `vcs.revision`. The Go
toolchain stamps VCS data for the main module only. So the framework's
identity comes from the source tree the packager builds from, or from a
value the packager links in. BuildInfo is used only when the framework is
the main module and carries a real revision.

## The document

```json
{
  "schema": "gnui.provenance/v1",
  "framework": { "module": "gonativeui", "revision": "<40 hex>", "dirty": false, "source": "git|stamp|ldflags|buildinfo" },
  "ghostty": { "commit": "<40 hex>", "zig_version": "0.17.0", "abi_check": "type-manifest", "libraries": [ { "goos": "darwin", "goarch": "arm64", "file": "...", "sha256": "..." } ] },
  "shaders": { "artifact": "metallib|none", "metallib_sha256": "...", "source_sha256": "...", "compiler": "...", "target": "...", "generated_by": "ci|local" },
  "go": { "version": "go1.27.x", "goos": "darwin", "goarch": "arm64" }
}
```

A field that cannot be established is omitted, never guessed.

| field | meaning | `Read(dir)` (packager side) | `Compiled()` (in the binary) |
|---|---|---|---|
| `schema` | always `gnui.provenance/v1` | | |
| `framework.module` | always `gonativeui` | | |
| `framework.revision` | 40-hex git commit | `git rev-parse HEAD` if dir is the top of a git checkout, else `.gnui-revision` | `FrameworkRevision` (-X), else `vcs.revision` when the framework is the main module, else omitted |
| `framework.dirty` | modified since `revision` | `git status --porcelain` lists anything (untracked included); the stamp's `-dirty` suffix | the -X value's `-dirty` suffix; `vcs.modified` |
| `framework.source` | where `revision` came from | `git` or `stamp` | `ldflags` or `buildinfo`; omitted with `revision` |
| `ghostty.commit`, `zig_version` | the libghostty-vt pin (`internal/ghosttyvt`) | parsed from the tree | compiled in |
| `ghostty.abi_check` | how the C ABI is verified: `type-manifest` means term's tests check every mirrored size, offset and enum value against the library's `ghostty_type_json` | | |
| `ghostty.libraries` | built libraries for that pin, from `.cache/libghostty-vt/<short>/manifest.json` (tools/libghostty), each file present and re-hashed | when built | omitted (loaded at run time, not compiled in) |
| `shaders.artifact` | `metallib`, or `none`: the source is compiled at run time | the tree's `internal/metallib/lib` | the embedded manifest on darwin; `none` elsewhere |
| `shaders.*` | from the manifest ([precompiled-shaders.md](precompiled-shaders.md)) | after checking the library's hash and that the tree's sources match | as embedded |
| `go.*` | toolchain and target | `go env GOVERSION GOOS GOARCH` in dir (the gnui process's own if `go` fails) | `runtime.Version()`, `GOOS`, `GOARCH` |

`Read` fails instead of reporting an inconsistent tree in these cases:

- no revision (neither git nor a stamp);
- not a framework tree;
- a shader library that is corrupt or stale;
- a built Ghostty library whose hash differs from its manifest.

**Build-time verified versus packager-asserted.**

- Verified at build time: everything in `Compiled()` except `ldflags`
  revisions. The shader identity is what `go:embed` put in the binary, the
  Ghostty pin and Go fields are compile-time constants, and `buildinfo`
  revisions are stamped by the toolchain.
- Asserted by the packager: `framework.source: "ldflags"`. The binary
  carries whatever revision the packager passed with `-X`.
- Verified on the packager's machine against the tree: everything from
  `Read(dir)`.

**Stability.** The field names and meanings above are fixed for
`gnui.provenance/v1`. A new field may be added within v1, and consumers
should ignore unknown fields. Removing or renaming a field, or changing
its meaning, makes a new schema. The Go types (`provenance.Info` and its
parts) follow the JSON.

## For packagers (Zap)

Zap pins a framework commit in its release manifest and builds from the
checkout `go list -m` resolves. Before packaging, verify that checkout and
keep its provenance with the release:

```sh
cd gnui                                   # Zap's module (replace gonativeui => ../../go-native-ui)
go run gonativeui/cmd/gnui provenance -json > dist/gnui-provenance.json
jq -e --arg pin "$PINNED_SHA" '.framework.revision == $pin and .framework.dirty == false and .shaders.artifact == "metallib"' dist/gnui-provenance.json
```

Then link the verified revision into the app, so that the binary reports it
too (`provenance.Compiled()`, for an about box or a crash report):

```sh
rev=$(jq -r .framework.revision dist/gnui-provenance.json)
go build -ldflags "-X gonativeui/provenance.FrameworkRevision=$rev" ...   # or gnui package -ldflags "-X ..."
```

`-dir <framework dir>` reads another tree. A tree that is not a git
checkout (an export) needs `.gnui-revision` at its top, holding
`<40 hex>` or `<40 hex>-dirty`. Without `-json`, gnui prints a summary.

Example (this branch's checkout, `Read`, before the first library was
committed):

```json
{
  "schema": "gnui.provenance/v1",
  "framework": {
    "module": "gonativeui",
    "revision": "977700e06e0a7ca7609fba7745dc806ef2e4589a",
    "dirty": true,
    "source": "git"
  },
  "ghostty": {
    "commit": "4a4fc017940bec38c93b278c10c6a18c591f3e63",
    "zig_version": "0.17.0",
    "abi_check": "type-manifest"
  },
  "shaders": {
    "artifact": "none"
  },
  "go": {
    "version": "go1.27.2",
    "goos": "darwin",
    "goarch": "arm64"
  }
}
```

With a committed library and built Ghostty libraries, these blocks are
filled in:

```json
"ghostty": { "commit": "4a4fc017940bec38c93b278c10c6a18c591f3e63", "zig_version": "0.17.0", "abi_check": "type-manifest",
             "libraries": [ { "goos": "darwin", "goarch": "arm64", "file": "libghostty-vt.dylib", "sha256": "<64 hex>" } ] },
"shaders": { "artifact": "metallib", "metallib_sha256": "<64 hex>", "source_sha256": "<64 hex>",
             "compiler": "Apple metal version …", "target": "air64-apple-macos14.0", "generated_by": "ci" }
```
