// Package metallib holds the precompiled Metal library of package gpu's
// shaders and the manifest that names it: lib/manifest.json, and beside it
// shaders.metallib, both of gpu's shader sources (quadMSL, backdropMSL)
// compiled to AIR and linked into one library by tools/metallib on a Mac
// with Xcode (CI's metallib workflow). The files are committed, so that a
// consumer of the module needs no Metal toolchain; a manifest that names no
// file means none was generated yet, and gpu compiles the source. See
// docs/design/precompiled-shaders.md.
//
// The manifest is embedded on every system; the library only on darwin.
package metallib

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
)

// Schema is the manifest version this package reads and tools/metallib
// writes.
const Schema = 1

// Manifest is lib/manifest.json.
type Manifest struct {
	Schema       int      `json:"schema"`
	File         string   `json:"file,omitempty"`          // the library, beside the manifest; empty: none generated
	Bytes        int64    `json:"bytes,omitempty"`         // of File
	SHA256       string   `json:"sha256,omitempty"`        // of File
	SourceSHA256 string   `json:"source_sha256,omitempty"` // SourceHash of the sources File was compiled from
	Sources      []Source `json:"sources,omitempty"`       // each source's own hash, to tell which one changed
	Compiler     string   `json:"compiler,omitempty"`      // the first line of `xcrun -sdk macosx metal --version`
	SDK          string   `json:"sdk,omitempty"`           // `xcrun -sdk macosx --show-sdk-version`
	Target       string   `json:"target,omitempty"`        // the -target triple: air64 and the macOS deployment target
	Std          string   `json:"std,omitempty"`           // the Metal language version (-std)
	Generated    string   `json:"generated,omitempty"`     // RFC 3339, UTC
	GeneratedBy  string   `json:"generated_by,omitempty"`  // "ci" (GitHub Actions) or "local"
	Commit       string   `json:"commit,omitempty"`        // the repository commit the sources were read at, "-dirty" if modified
}

// Source is one shader source in the library.
type Source struct {
	Name   string `json:"name"` // "quad" or "backdrop"
	SHA256 string `json:"sha256"`
}

//go:embed lib/manifest.json
var manifestJSON []byte

// Embedded is the embedded manifest; ok is false if it does not parse or
// has another schema.
func Embedded() (m Manifest, ok bool) {
	return Parse(manifestJSON)
}

// Parse decodes a manifest of this Schema.
func Parse(b []byte) (m Manifest, ok bool) {
	if json.Unmarshal(b, &m) != nil || m.Schema != Schema {
		return Manifest{}, false
	}
	return m, true
}

// SourceHash is the source_sha256 of the two shader sources: the SHA-256
// of quad, a NUL byte and backdrop, in hex.
func SourceHash(quad, backdrop string) string {
	h := sha256.New()
	h.Write([]byte(quad))
	h.Write([]byte{0})
	h.Write([]byte(backdrop))
	return hex.EncodeToString(h.Sum(nil))
}

// Hash is the lowercase hex SHA-256 of b, as the manifest records hashes.
func Hash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
