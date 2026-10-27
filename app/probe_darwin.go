//go:build darwin

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"gonativeui/gpu"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// Throwaway perf-survey probe (never commit).
var probeW, probeH int

var probe struct {
	calls0, cbs0, main0                   uint64
	g0                                    gpu.ProbeCounters
	nGPU0, nUCB0                          int
	begin, build, enc                     uint64 // ffi calls per segment, summed
	encCPU, endCPU, beginCPU              int64
}

func probeBegin() {
	gpu.ProbeDumpBig = os.Getenv("GPUPROBE_DUMP") != ""
	gpu.Probe.Enabled = true
	ffi.ProbeTable = [8192]ffi.ProbeEntry{}
	ffi.ProbeTimeOn = os.Getenv("GPUPROBE_TIME") != ""
	probe.calls0, probe.cbs0 = ffi.ProbeCalls, ffi.ProbeCBs
	probe.main0 = ffi.ProbeMainCalls
	platform.ProbeOn = true
	platform.ProbeNextFirstNs, platform.ProbeNextDrainNs, platform.ProbeRouteNs = 0, 0, 0
	platform.ProbeNextFirstN, platform.ProbeNextDrainN = 0, 0
	probe.g0 = gpu.Probe
	gpu.ProbeMix.N, gpu.ProbeMix.Area, gpu.ProbeMix.Inside = [8]uint64{}, [8]float64{}, [8]float64{}
	probe.nGPU0, probe.nUCB0 = len(gpu.Probe.GPUSamples), len(gpu.Probe.UCBSamples)
	probe.begin, probe.build, probe.enc = 0, 0, 0
	probe.encCPU, probe.endCPU, probe.beginCPU = 0, 0, 0
}

func probeFrame(m *frameMeasure) {
	probe.begin += m.c1 - m.c0
	probe.build += m.c2 - m.c1
	probe.enc += m.c3 - m.c2
	probe.encCPU += m.e1 - m.e0
	probe.endCPU += m.cpu3 - m.e1
	probe.beginCPU += m.cpu1 - m.cpu0
}

type pstats struct{ N int; P10, P50, P90, Min, Max, Mean float64 }

func pst(xs []float64) pstats {
	if len(xs) == 0 {
		return pstats{}
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	var sum float64
	for _, x := range s {
		sum += x
	}
	return pstats{len(s), s[len(s)/10], s[len(s)/2], s[len(s)*9/10], s[0], s[len(s)-1], sum / float64(len(s))}
}

func probeEnd(n int, mainUs float64, a *App) {
	if n == 0 {
		return
	}
	g := gpu.Probe
	f := float64(n)
	out := map[string]any{
		"frames":             n,
		"mainThreadUsPerFrame": mainUs,
		"ffiCallsPerFrame":   float64(ffi.ProbeCalls-probe.calls0) / f,
		"mainFrameCallsPerFrame": float64(ffi.ProbeMainCalls-probe.main0) / f,
		"nextEventFirstCPUus": float64(platform.ProbeNextFirstNs) / 1e3 / f,
		"nextEventFirstPerFrame": float64(platform.ProbeNextFirstN) / f,
		"nextEventDrainCPUus": float64(platform.ProbeNextDrainNs) / 1e3 / f,
		"nextEventDrainPerFrame": float64(platform.ProbeNextDrainN) / f,
		"routeCPUus": float64(platform.ProbeRouteNs) / 1e3 / f,
		"callbacksPerFrame":  float64(ffi.ProbeCBs-probe.cbs0) / f,
		"callsBeginFrame":    float64(probe.begin) / f,
		"callsBuild":         float64(probe.build) / f,
		"callsEncodeEnd":     float64(probe.enc) / f,
		"beginFrameCPUus":    float64(probe.beginCPU) / 1e3 / f,
		"encodeCPUus":        float64(probe.encCPU) / 1e3 / f,
		"endFrameCPUus":      float64(probe.endCPU) / 1e3 / f,
		"quadsPerFrame":      float64(g.Quads-probe.g0.Quads) / f,
		"instBytesPerFrame":  float64(g.InstBytes-probe.g0.InstBytes) / f,
		"uploadsPerFrame":    float64(g.Uploads-probe.g0.Uploads) / f,
		"uploadBytesPerFrame": float64(g.UploadBytes-probe.g0.UploadBytes) / f,
		"stagesPerFrame":     float64(g.Stages-probe.g0.Stages) / f,
		"stageBytesPerFrame": float64(g.StageB-probe.g0.StageB) / f,
		"flushes":            g.Flushes - probe.g0.Flushes,
		"uploadWaits":        g.Waits - probe.g0.Waits,
		"uploadWaitMs":       float64(g.WaitNs-probe.g0.WaitNs) / 1e6,
		"retireWaits":        g.RetireWaits - probe.g0.RetireWaits,
		"retireWaitMs":       float64(g.RetireWaitNs-probe.g0.RetireWaitNs) / 1e6,
		"encodersPerFrame":   float64(g.Encoders-probe.g0.Encoders) / f,
		"backdropsPerFrame":  float64(g.Backdrops-probe.g0.Backdrops) / f,
		"drawCallsLast":      a.R.DrawCalls(),
		"gpuUs":              pst(g.GPUSamples[probe.nGPU0:]),
		"ucbUs":              pst(g.UCBSamples[probe.nUCB0:]),
		"target":             [2]int{probeW, probeH},
	}
	mix := map[string]string{}
	var tot, ins float64
	for k, nm := range gpu.ProbeMixNames {
		tot += gpu.ProbeMix.Area[k]
		ins += gpu.ProbeMix.Inside[k]
		mix[nm] = fmt.Sprintf("n=%.1f area=%.2fMpx inClip=%.2fMpx", float64(gpu.ProbeMix.N[k])/f, gpu.ProbeMix.Area[k]/1e6/f, gpu.ProbeMix.Inside[k]/1e6/f)
	}
	mix["total"] = fmt.Sprintf("%.2fMpx inClip %.2fMpx (target %.2fMpx)", tot/1e6/f, ins/1e6/f, float64(probeW*probeH)/1e6)
	out["mix"] = mix
	b, _ := json.Marshal(out)
	fmt.Fprintf(os.Stderr, "GPUPROBE %s\n", b)
	if ffi.ProbeTimeOn {
		ffi.ProbeTimeOn = false
		var es []ffi.ProbeEntry
		var tot int64
		var totN uint64
		for _, e := range ffi.ProbeTable {
			if e.N > 0 {
				es = append(es, e)
				tot += e.Ns
				totN += e.N
			}
		}
		slices.SortFunc(es, func(a, b ffi.ProbeEntry) int { return int(b.Ns - a.Ns) })
		fmt.Fprintf(os.Stderr, "GPUPROBE_SEL total %.1f us/frame over %.1f calls/frame (main frame F only, wall incl. timer)\n", float64(tot)/1e3/f, float64(totN)/f)
		for i, e := range es {
			if i == 45 {
				break
			}
			name := "C-fn"
			if e.Sel != 0 {
				name = objc.SelName(objc.SEL(e.Sel))
			} else {
				switch e.Fn {
				case objc.PoolPush:
					name = "objc_autoreleasePoolPush"
				case objc.PoolPop:
					name = "objc_autoreleasePoolPop"
				default:
					name = fmt.Sprintf("C-fn %#x", e.Fn)
				}
			}
			fmt.Fprintf(os.Stderr, "GPUPROBE_SEL %8.2f us/f %7.2f calls/f %8.0f ns/call  %s\n", float64(e.Ns)/1e3/f, float64(e.N)/f, float64(e.Ns)/float64(e.N), name)
		}
	}
}
