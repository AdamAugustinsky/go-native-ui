//go:build !((darwin && arm64) || (linux && (arm64 || amd64)) || (windows && (amd64 || arm64)))

package term

// newCallbacks is never reached where clib cannot open a library.
func newCallbacks(*callbacks) error { return ErrUnavailable }
