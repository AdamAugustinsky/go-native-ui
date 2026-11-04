//go:build windows

package app

// nativeSample does nothing: Windows has no sampler to call.
var nativeSample = func(string) {}
