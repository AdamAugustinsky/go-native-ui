package main

import "encoding/binary"

// isMachO reports whether the first bytes of a file are a Mach-O or universal
// magic number, which is what makes a file signable code. gnui builds thin
// arm64 executables, but a nested framework or helper may be universal.
func isMachO(head []byte) bool {
	if len(head) < 4 {
		return false
	}
	m := binary.BigEndian.Uint32(head)
	switch m {
	case 0xfeedface, 0xfeedfacf, 0xcefaedfe, 0xcffaedfe, 0xcafebabe, 0xbebafeca:
		return true
	}
	return false
}
