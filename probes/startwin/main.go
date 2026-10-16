//go:build darwin

// Throwaway phase-0 probe: app-agnostic startup timer. Execs a command and
// polls CGWindowListCopyWindowInfo until a window owned by the child PID is
// on screen; then samples footprint/CPU after a settle period and kills it.
//
//	go run ./probes/startwin -n 5 -- /path/to/app [args]
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	cgWindowListCopyWindowInfo func(opt uint32, rel uint32) uintptr
	cfArrayGetCount            func(uintptr) int
	cfArrayGetValueAtIndex     func(uintptr, int) uintptr
	cfDictionaryGetValue       func(uintptr, uintptr) uintptr
	cfNumberGetValue           func(uintptr, int, unsafe.Pointer) bool
	cfRelease                  func(uintptr)
	kOwnerPID, kLayer          uintptr
)

func load() {
	cf, _ := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW)
	cg, _ := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW)
	purego.RegisterLibFunc(&cgWindowListCopyWindowInfo, cg, "CGWindowListCopyWindowInfo")
	purego.RegisterLibFunc(&cfArrayGetCount, cf, "CFArrayGetCount")
	purego.RegisterLibFunc(&cfArrayGetValueAtIndex, cf, "CFArrayGetValueAtIndex")
	purego.RegisterLibFunc(&cfDictionaryGetValue, cf, "CFDictionaryGetValue")
	purego.RegisterLibFunc(&cfNumberGetValue, cf, "CFNumberGetValue")
	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	p, _ := purego.Dlsym(cg, "kCGWindowOwnerPID")
	kOwnerPID = *(*uintptr)(unsafe.Pointer(p))
	p, _ = purego.Dlsym(cg, "kCGWindowLayer")
	kLayer = *(*uintptr)(unsafe.Pointer(p))
}

// hasWindow reports whether pid (or a descendant listed in pids) owns an on-screen layer-0 window.
func hasWindow(pids map[int32]bool) bool {
	arr := cgWindowListCopyWindowInfo(1|16, 0) // OnScreenOnly | ExcludeDesktopElements
	if arr == 0 {
		return false
	}
	defer cfRelease(arr)
	for i, n := 0, cfArrayGetCount(arr); i < n; i++ {
		d := cfArrayGetValueAtIndex(arr, i)
		var pid, layer int32
		cfNumberGetValue(cfDictionaryGetValue(d, kOwnerPID), 3, unsafe.Pointer(&pid)) // kCFNumberSInt32Type
		cfNumberGetValue(cfDictionaryGetValue(d, kLayer), 3, unsafe.Pointer(&layer))
		if layer == 0 && pids[pid] {
			return true
		}
	}
	return false
}

func childPIDs(root int) map[int32]bool {
	m := map[int32]bool{int32(root): true}
	out, _ := exec.Command("pgrep", "-P", fmt.Sprint(root)).Output()
	for _, f := range strings.Fields(string(out)) {
		var p int32
		fmt.Sscan(f, &p)
		m[p] = true
	}
	return m
}

func footprint(pid int) string {
	out, _ := exec.Command("footprint", "-p", fmt.Sprint(pid)).CombinedOutput()
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, "Footprint:") {
			return strings.TrimSpace(l[strings.Index(l, "Footprint:")+10:])
		}
	}
	return "?"
}

func cpuTime(pid int) string {
	out, _ := exec.Command("ps", "-o", "time=", "-p", fmt.Sprint(pid)).Output()
	return strings.TrimSpace(string(out))
}

func main() {
	n := flag.Int("n", 5, "runs")
	settle := flag.Duration("settle", 5*time.Second, "wait before sampling idle")
	idle := flag.Duration("idle", 10*time.Second, "idle CPU sampling window (last run only)")
	flag.Parse()
	args := flag.Args()
	load()
	if len(args) == 1 && args[0] == "list" {
		arr := cgWindowListCopyWindowInfo(1|16, 0)
		for i, n := 0, cfArrayGetCount(arr); i < n; i++ {
			d := cfArrayGetValueAtIndex(arr, i)
			var pid, layer int32
			cfNumberGetValue(cfDictionaryGetValue(d, kOwnerPID), 3, unsafe.Pointer(&pid))
			cfNumberGetValue(cfDictionaryGetValue(d, kLayer), 3, unsafe.Pointer(&layer))
			fmt.Println("pid", pid, "layer", layer)
		}
		return
	}
	var times []time.Duration
	for r := 0; r < *n; r++ {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		t0 := time.Now()
		if err := cmd.Start(); err != nil {
			panic(err)
		}
		pid := cmd.Process.Pid
		var dt time.Duration
		for i := 0; ; i++ {
			pids := map[int32]bool{int32(pid): true}
			if i%25 == 0 {
				pids = childPIDs(pid)
			}
			if hasWindow(pids) {
				dt = time.Since(t0)
				break
			}
			if time.Since(t0) > 20*time.Second {
				dt = -1
				break
			}
			time.Sleep(time.Millisecond)
		}
		times = append(times, dt)
		if r == *n-1 {
			time.Sleep(*settle)
			fp := footprint(pid)
			c0 := cpuTime(pid)
			time.Sleep(*idle)
			fmt.Fprintf(os.Stderr, "footprint=%s cpu_time %s -> %s over %v idle\n", fp, c0, cpuTime(pid), *idle)
		}
		syscall.Kill(-pid, syscall.SIGKILL)
		cmd.Wait()
		time.Sleep(500 * time.Millisecond)
	}
	sorted := append([]time.Duration(nil), times...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	fmt.Printf("exec->window on screen: runs=%v median=%v\n", times, sorted[len(sorted)/2])
}
