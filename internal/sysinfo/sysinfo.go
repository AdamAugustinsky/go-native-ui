// Package sysinfo reads process metrics used by benchmarks: process age (for
// exec → first-frame timing), memory and CPU. Per-OS implementations:
//
//	ProcessAgeNs() int64  nanoseconds since the OS created this process, -1 if unknown
//	Read() Snapshot       current metrics
package sysinfo

// Snapshot holds process metrics. Fields an OS cannot provide are 0.
type Snapshot struct {
	// Footprint is the primary memory metric: macOS phys_footprint (Activity
	// Monitor "Memory"), Windows private commit (PrivateUsage; the private
	// working set is a phase-2 follow-up), Linux PSS.
	Footprint uint64
	// RSS is the resident set size (includes shared pages; informational).
	RSS uint64
	// CPUNs is user+system CPU time consumed so far.
	CPUNs int64
	// Wakeups counts wakeups that took the CPU package out of idle where the OS
	// reports them (macOS: Activity Monitor "Idle Wake Ups", top's idlew).
	Wakeups uint64
	// InterruptWakeups counts all interrupt wakeups of the process's threads
	// (macOS), a superset that includes wakeups while the package was busy.
	InterruptWakeups uint64
}
