//go:build unix && !linux && !darwin

package instance

import "net"

// peerUID reports that this system cannot say who is on the other end of a
// Unix-domain connection: on Windows, SIO_AF_UNIX_GETPEERPID exists only from
// Windows 11 and Server 2022, so it is not relied on, and the socket's
// directory (checked in AcquireWith) is what keeps other users out.
func peerUID(net.Conn) (uid uint32, ok bool, err error) { return 0, false, nil }
