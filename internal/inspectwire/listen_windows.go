package inspectwire

import (
	"errors"
	"fmt"
)

// Listen is not supported on Windows yet: nothing here can make the socket
// private (a restrictive DACL on the socket and its directory is not set up),
// and macOS comes first (DECISIONS D34). The error wraps errors.ErrUnsupported.
func Listen(path string) (*Listener, error) {
	return nil, fmt.Errorf("inspect: the agent bridge is not supported on Windows yet (DECISIONS D34: macOS first): %w", errors.ErrUnsupported)
}
