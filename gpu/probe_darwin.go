//go:build darwin

package gpu

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// Throwaway perf-survey probe (never commit).
type ProbeCounters struct {
	Encodes, InstBytes, Quads            uint64
	Uploads, UploadBytes, Stages, StageB uint64
	Flushes, Waits, WaitNs               uint64
	RetireWaits, RetireWaitNs           uint64
	Encoders, Backdrops                  uint64
	GPUSamples                           []float64 // frame command buffer GPU time, µs
	UCBSamples                           []float64 // staged-upload command buffer GPU time, µs
	Enabled                              bool
}

var Probe = ProbeCounters{GPUSamples: make([]float64, 0, 1<<16), UCBSamples: make([]float64, 0, 1<<12)}

var (
	selGPUStart = objc.Sel("GPUStartTime")
	selGPUEnd   = objc.Sel("GPUEndTime")
	probeFrame  = new(ffi.Frame)
)

// probeGPU records cb's GPU time if it completed; its own calls are not
// counted in ffi.ProbeCalls.
func probeGPU(cb objc.ID, dst *[]float64) {
	if !Probe.Enabled || cb == 0 {
		return
	}
	c := ffi.ProbeCalls
	st := probeFrame.Reset().Arg(uintptr(cb)).Arg(uintptr(objc.Sel("status"))).Call(objc.MsgSend).R0()
	if st == 4 {
		t0 := probeFrame.Reset().Arg(uintptr(cb)).Arg(uintptr(selGPUStart)).Call(objc.MsgSend).F64(0)
		t1 := probeFrame.Reset().Arg(uintptr(cb)).Arg(uintptr(selGPUEnd)).Call(objc.MsgSend).F64(0)
		if t1 > t0 && len(*dst) < cap(*dst) {
			*dst = append(*dst, (t1-t0)*1e6)
		}
	}
	ffi.ProbeCalls = c
}

func nowNs() int64 { return platform.Now() }

// Quad mix, area-weighted (pixels the vertex stage covers, clipped).
var ProbeMix struct {
	N      [8]uint64
	Area   [8]float64
	Inside [8]float64 // area inside the clip
}

var ProbeMixNames = [8]string{"shape-flat", "shape-rounded", "shape-border", "shadow", "maskR8", "rgba", "backdrop", "rgba-rounded"}

func probeMix(qs []Quad, w, h int) {
	if !Probe.Enabled {
		return
	}
	for i := range qs {
		q := &qs[i]
		k := 0
		pad := float32(0)
		rounded := q.Radii != [4]float32{}
		switch q.Mode {
		case ModeShape:
			pad = 1
			switch {
			case q.Soft > 0:
				k = 3
				pad = float32(int(3*q.Soft+0.999)) + 1
			case q.Border > 0:
				k = 2
			case rounded:
				k = 1
			}
		case ModeMaskR8:
			k = 4
		case ModeRGBA:
			k = 5
			if rounded {
				k = 7
			}
		case ModeBackdrop:
			k = 6
		}
		if q.Mode != ModeShape && rounded {
			pad = 1
		}
		r := [4]float32{q.Rect[0] - pad, q.Rect[1] - pad, q.Rect[2] + pad, q.Rect[3] + pad}
		r = [4]float32{max(r[0], 0), max(r[1], 0), min(r[2], float32(w)), min(r[3], float32(h))}
		a := float64(0)
		if r[2] > r[0] && r[3] > r[1] {
			a = float64(r[2]-r[0]) * float64(r[3]-r[1])
		}
		ProbeMix.N[k]++
		ProbeMix.Area[k] += a
		if c := q.Clip; c != [4]float32{} {
			r = [4]float32{max(r[0], c[0]-0.5), max(r[1], c[1]-0.5), min(r[2], c[2]+0.5), min(r[3], c[3]+0.5)}
		}
		if r[2] > r[0] && r[3] > r[1] {
			ProbeMix.Inside[k] += float64(r[2]-r[0]) * float64(r[3]-r[1])
		}
	}
}

// ProbeDumpBig prints the largest quads of the next Encode (once).
var ProbeDumpBig bool

func probeDump(qs []Quad, w, h int) {
	if !ProbeDumpBig {
		return
	}
	ProbeDumpBig = false
	type e struct {
		i int
		a float32
	}
	var es []e
	for i := range qs {
		q := &qs[i]
		pad := float32(1)
		if q.Mode == ModeShape && q.Soft > 0 {
			pad = float32(int(3*q.Soft+0.999)) + 1
		}
		x0, y0 := max(q.Rect[0]-pad, 0), max(q.Rect[1]-pad, 0)
		x1, y1 := min(q.Rect[2]+pad, float32(w)), min(q.Rect[3]+pad, float32(h))
		if x1 > x0 && y1 > y0 {
			es = append(es, e{i, (x1 - x0) * (y1 - y0)})
		}
	}
	for k := 0; k < 25 && k < len(es); k++ {
		m := k
		for j := k + 1; j < len(es); j++ {
			if es[j].a > es[m].a {
				m = j
			}
		}
		es[k], es[m] = es[m], es[k]
		q := &qs[es[k].i]
		println("BIGQUAD", es[k].i, "/", len(qs), "area", int(es[k].a), "mode", q.Mode, "rect", int(q.Rect[0]), int(q.Rect[1]), int(q.Rect[2]), int(q.Rect[3]),
			"clip", int(q.Clip[0]), int(q.Clip[1]), int(q.Clip[2]), int(q.Clip[3]),
			"col", hex(q.Color[0]), hex(q.Color[3]), "border", q.Border, "bcol", hex(q.BorderColor), "soft", q.Soft, "radii", q.Radii[0], q.Radii[2], "fadeX", q.FadeX[0])
	}
}

func hex(v uint32) string {
	const d = "0123456789abcdef"
	var b [8]byte
	for i := 7; i >= 0; i-- {
		b[i] = d[v&15]
		v >>= 4
	}
	return string(b[:])
}

var probeClipGeom = os.Getenv("GNUI_CLIPGEOM") != ""

var probeGPUTimeN, _ = strconv.Atoi(os.Getenv("GNUI_GPUTIME"))

// probeGPUTime renders list probeGPUTimeN times, one command buffer at a time,
// and prints the GPU time stats.
var probeGPUTimeDone int

func probeGPUTime(r *Renderer, width, height int, clear [4]float32, list *DrawList) {
	if probeGPUTimeDone++; probeGPUTimeDone != 3 {
		return
	}
	xs := make([]float64, 0, probeGPUTimeN)
	var cpu []float64
	cbs := make([]objc.ID, 0, probeGPUTimeN+20)
	for i := 0; i < probeGPUTimeN+20; i++ {
		pool := objc.F.Call0(objc.PoolPush)
		cb := objc.Retain(objc.Send(r.queue, rsCommandBuf))
		t0 := platform.Now()
		r.Encode(uintptr(cb), uintptr(r.offscreen), width, height, clear, list)
		t1 := platform.Now()
		objc.Send(cb, rsCommit)
		objc.F.Call1(objc.PoolPop, pool)
		cbs = append(cbs, cb)
		if i >= 20 {
			cpu = append(cpu, float64(t1-t0)/1e3)
		}
	}
	var first, last float64
	for i, cb := range cbs {
		objc.Send(cb, rsWait)
		if i >= 20 {
			xs = append(xs, (objc.SendF64(cb, selGPUEnd)-objc.SendF64(cb, selGPUStart))*1e6)
			if i == 20 {
				first = objc.SendF64(cb, selGPUStart)
			}
			last = objc.SendF64(cb, selGPUEnd)
		}
		objc.Release(cb)
	}
	fmt.Fprintf(os.Stderr, "GPUTHRU %.1f us/frame\n", (last-first)*1e6/float64(probeGPUTimeN))
	slices.Sort(xs)
	slices.Sort(cpu)
	n := len(xs)
	fmt.Fprintf(os.Stderr, "GPUTIME %dx%d quads=%d draws=%d gpu_us min %.1f p10 %.1f p50 %.1f p90 %.1f | encode_wall_us min %.1f p50 %.1f\n",
		width, height, len(list.Quads), r.draws, xs[0], xs[n/10], xs[n/2], xs[n*9/10], cpu[0], cpu[n/2])
}
