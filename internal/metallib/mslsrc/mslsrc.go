// Package mslsrc reads package gpu's two Metal shader sources out of its Go
// files, as the compiled package holds them, for the tools that compile
// them or check a precompiled library against them without building gpu
// for darwin.
package mslsrc

import (
	"path/filepath"

	"gonativeui/internal/goconst"
)

// Read returns the values of the constants quadMSL (shaders_darwin.go) and
// backdropMSL (backdrop_darwin.go) of the gpu package in gpuDir.
func Read(gpuDir string) (quad, backdrop string, err error) {
	if quad, err = goconst.String(filepath.Join(gpuDir, "shaders_darwin.go"), "quadMSL"); err != nil {
		return "", "", err
	}
	if backdrop, err = goconst.String(filepath.Join(gpuDir, "backdrop_darwin.go"), "backdropMSL"); err != nil {
		return "", "", err
	}
	return quad, backdrop, nil
}
