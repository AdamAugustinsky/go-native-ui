//go:build darwin

package metallib

import "embed"

// Files holds lib/manifest.json and the libraries it lists, beside it.
//
//go:embed lib
var Files embed.FS
