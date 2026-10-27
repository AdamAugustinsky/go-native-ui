//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package ffi

import _ "unsafe"

// Throwaway perf-survey probe (never commit): per (fn, selector) call
// counts and wall time for calls on ProbeMain.

//go:linkname probeNanotime runtime.nanotime
func probeNanotime() int64

type ProbeEntry struct {
	Fn, Sel uintptr
	N       uint64
	Ns      int64
}

var (
	ProbeMain    *Frame
	ProbeMsgSend uintptr
	ProbeTimeOn  bool
	ProbeTable   [8192]ProbeEntry
)

func probeRecord(fn, sel uintptr, ns int64) {
	if fn != ProbeMsgSend {
		sel = 0
	}
	h := (fn*0x9E3779B97F4A7C15 ^ sel*0xBF58476D1CE4E5B9) >> 51
	for i := h; ; i = (i + 1) & 8191 {
		e := &ProbeTable[i]
		if e.N == 0 {
			e.Fn, e.Sel = fn, sel
		}
		if e.Fn == fn && e.Sel == sel {
			e.N++
			e.Ns += ns
			return
		}
	}
}
