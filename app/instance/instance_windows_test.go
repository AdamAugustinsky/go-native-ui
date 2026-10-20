//go:build windows

package instance

import "time"

// rawDial connects without any check on the other end.
func rawDial(addr string) (conn, error) { return dialRaw(addr, time.Second) }
