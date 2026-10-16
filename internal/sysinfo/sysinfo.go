// Package sysinfo reads process metrics used by benchmarks: process age (for
// exec → first-frame timing), memory and CPU. Per-OS implementations:
//
//	ProcessAgeNs() int64  nanoseconds since the OS created this process, -1 if unknown
//	Read() Snapshot       current metrics
package sysinfo

// Snapshot holds process metrics. Fields an OS cannot provide are 0.
type Snapshot struct {
	// Footprint is the primary memory metric: macOS phys_footprint (Activity
	// Monitor "Memory"), Windows private working set, Linux PSS.
	Footprint uint64
	// RSS is the resident set size (includes shared pages; informational).
	RSS uint64
	// CPUNs is user+system CPU time consumed so far.
	CPUNs int64
	// Wakeups counts idle/interrupt wakeups where the OS reports them (macOS).
	Wakeups uint64
}
