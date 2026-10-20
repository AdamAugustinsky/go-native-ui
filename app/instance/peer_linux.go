//go:build linux

package instance

import (
	"errors"
	"net"
	"syscall"
)

// peerUID returns the user id of the process on the other end of a Unix-domain
// connection (SO_PEERCRED). ok is false where the system cannot say.
func peerUID(c net.Conn) (uid uint32, ok bool, err error) {
	uc, isUnix := c.(*net.UnixConn)
	if !isUnix {
		return 0, false, nil
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return 0, true, err
	}
	var cred *syscall.Ucred
	var cerr error
	if err := rc.Control(func(fd uintptr) {
		cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, true, err
	}
	if cerr != nil {
		return 0, true, cerr
	}
	if cred == nil {
		return 0, true, errors.New("no credentials")
	}
	return cred.Uid, true, nil
}
