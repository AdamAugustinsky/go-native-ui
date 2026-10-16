//go:build linux

package sysinfo

import (
	"bytes"
	"encoding/binary"
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

const (
	clockBoottime = 7  // CLOCK_BOOTTIME: /proc starttime counts from boot, including suspend
	atClktck      = 17 // AT_CLKTCK in /proc/self/auxv
)

// clockTicks returns the kernel's USER_HZ (AT_CLKTCK), 100 if unreadable.
func clockTicks() int64 {
	b, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		return 100
	}
	for len(b) >= 16 {
		k, v := binary.NativeEndian.Uint64(b), binary.NativeEndian.Uint64(b[8:])
		if k == atClktck && v > 0 {
			return int64(v)
		}
		if k == 0 { // AT_NULL
			break
		}
		b = b[16:]
	}
	return 100
}

// ProcessAgeNs returns nanoseconds since the kernel created this process:
// CLOCK_BOOTTIME now minus /proc/self/stat field 22 (starttime, in clock
// ticks since boot). Resolution is one tick (10 ms at 100 Hz).
func ProcessAgeNs() int64 {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return -1
	}
	// Fields are space separated, but field 2 (comm) is parenthesised and
	// may contain spaces: split after the last ')'. The rest starts at
	// field 3 (state), so starttime (field 22) is index 19.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return -1
	}
	f := bytes.Fields(b[i+1:])
	if len(f) < 20 {
		return -1
	}
	ticks, err := strconv.ParseInt(string(f[19]), 10, 64)
	if err != nil {
		return -1
	}
	var ts syscall.Timespec
	if _, _, e := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, clockBoottime, uintptr(unsafe.Pointer(&ts)), 0); e != 0 {
		return -1
	}
	hz := clockTicks()
	start := ticks / hz * 1e9 // split to avoid overflow
	start += ticks % hz * 1e9 / hz
	age := ts.Sec*1e9 + ts.Nsec - start
	if age < 0 {
		return -1
	}
	return age
}

// Read returns current process metrics: PSS and RSS from smaps_rollup
// (Linux 4.14+; RSS from statm otherwise) and CPU time from getrusage.
func Read() Snapshot {
	var s Snapshot
	if b, err := os.ReadFile("/proc/self/smaps_rollup"); err == nil {
		for _, line := range bytes.Split(b, []byte{'\n'}) {
			switch {
			case bytes.HasPrefix(line, []byte("Pss:")):
				s.Footprint = kB(line[4:])
			case bytes.HasPrefix(line, []byte("Rss:")):
				s.RSS = kB(line[4:])
			}
		}
	}
	if s.RSS == 0 {
		if b, err := os.ReadFile("/proc/self/statm"); err == nil {
			if f := bytes.Fields(b); len(f) >= 2 {
				pages, _ := strconv.ParseUint(string(f[1]), 10, 64)
				s.RSS = pages * uint64(os.Getpagesize())
			}
		}
	}
	if s.Footprint == 0 {
		s.Footprint = s.RSS
	}
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		s.CPUNs = ru.Utime.Nano() + ru.Stime.Nano()
	}
	return s
}

// kB parses " 1234 kB" into bytes.
func kB(b []byte) uint64 {
	f := bytes.Fields(b)
	if len(f) == 0 {
		return 0
	}
	n, _ := strconv.ParseUint(string(f[0]), 10, 64)
	return n * 1024
}
