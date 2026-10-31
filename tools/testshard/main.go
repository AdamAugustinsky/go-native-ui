// Command testshard reads Go import paths on its standard input, one a line
// (go list's output), and prints one shard of them, the slowest first, so a
// CI job's `go test` can be spread over several runners:
//
//	go list ./... | go run ./tools/testshard -shard 1/2
//
// The packages are dealt out the slowest first, each to the shard with the
// least test time so far, by the seconds a package's tests take on this OS
// (the tables below, from tools/testtimes' tables of main's CI runs); a
// package the table does not name counts as a few seconds. The order matters
// as much as the split: go test starts packages in the order it is given
// them, and fs, which sorts after cmd/..., started eight minutes into the
// Windows test job, then ran nine minutes more.
//
// The tables only need to be roughly right: a package that becomes slow and
// is missing from its table makes one shard longer than the others, never a
// package untested. Every package is in exactly one shard.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
)

// otherSeconds is what a package no table names counts as.
const otherSeconds = 5

// seconds are the packages whose tests take 15 s or more on an OS's runners,
// the slowest of its arches, rounded (CI runs 41082617966 and 41112603988,
// 2026-10-30).
var seconds = map[string]map[string]float64{
	"darwin": {
		"gonativeui/platform":             100,
		"gonativeui/app/applive":          95,
		"gonativeui/app":                  95,
		"gonativeui/cmd/pilot/pilot":      80,
		"gonativeui/cmd/gallery/gallery":  70,
		"gonativeui/media":                70,
		"gonativeui/cmd/gnui":             60,
		"gonativeui/cmd/listbench":        50,
		"gonativeui/qr":                   50,
		"gonativeui/internal/objc":        45,
		"gonativeui/fs":                   40,
		"gonativeui/rexui/live":           40,
		"gonativeui/app/simtests/ui":      30,
		"gonativeui/cmd/chromedemo/demo":  30,
		"gonativeui/cmd/pilot/live":       25,
		"gonativeui/gpu":                  25,
		"gonativeui/app/selfupdate":       25,
		"gonativeui/app/webview":          20,
		"gonativeui/examples/notes/notes": 20,
		"gonativeui/widget/widgettest":    20,
		"gonativeui/app/ws":               15,
	},
	"linux": {
		"gonativeui/cmd/gallery/gallery":  390,
		"gonativeui/cmd/pilot/pilot":      320,
		"gonativeui/cmd/chromedemo/demo":  140,
		"gonativeui/cmd/listbench":        130,
		"gonativeui/app":                  105,
		"gonativeui/examples/notes/notes": 90,
		"gonativeui/cmd/imgbench":         75,
		"gonativeui/app/simtests/ui":      60,
		"gonativeui/fs":                   60,
		"gonativeui/cmd/gnui":             30,
		"gonativeui/cmd/vlistbench":       25,
		"gonativeui/tools/rexui":          25,
		"gonativeui/rexui":                25,
		"gonativeui/highlight":            15,
		"gonativeui/platform":             15,
		"gonativeui/app/ws":               15,
		"gonativeui/cmd/windowsdemo":      15,
	},
	"windows": {
		"gonativeui/cmd/gallery/gallery":  510,
		"gonativeui/cmd/pilot/pilot":      470,
		"gonativeui/app/selfupdate":       440, // arm64; 70 on amd64
		"gonativeui/cmd/listbench":        375,
		"gonativeui/cmd/chromedemo/demo":  210,
		"gonativeui/cmd/imgbench":         175,
		"gonativeui/cmd/gnui":             165,
		"gonativeui/fs":                   60, // an estimate: 330-560 while NTFS made 8.3 names for its 100k files
		"gonativeui/examples/notes/notes": 125,
		"gonativeui/app":                  110,
		"gonativeui/tools/rexui":          100,
		"gonativeui/app/ws":               75,
		"gonativeui/platform":             75,
		"gonativeui/app/simtests/ui":      50,
		"gonativeui/media/capture":        50,
		"gonativeui/cmd/windowsdemo":      35,
		"gonativeui/gpu":                  35,
		"gonativeui/cmd/rexlook/rex":      30,
		"gonativeui/app/applive":          25,
		"gonativeui/examples/richtext":    25,
		"gonativeui/examples/services":    25,
		"gonativeui/examples/images":      25,
		"gonativeui/rexui":                25,
		"gonativeui/img":                  25,
		"gonativeui/text":                 20,
		"gonativeui/widget/widgettest":    20,
		"gonativeui/provenance":           20,
		"gonativeui/app/instance":         15,
		"gonativeui/highlight":            15,
		"gonativeui/core":                 15,
	},
}

func main() {
	spec := flag.String("shard", "1/1", "the shard to print, i/n (1 ≤ i ≤ n)")
	flag.Parse()
	var i, n int
	if _, err := fmt.Sscanf(*spec, "%d/%d", &i, &n); err != nil || n < 1 || i < 1 || i > n {
		fmt.Fprintf(os.Stderr, "testshard: -shard %q, want i/n with 1 ≤ i ≤ n\n", *spec)
		os.Exit(2)
	}

	var pkgs []string
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		if p := strings.TrimSpace(in.Text()); p != "" {
			pkgs = append(pkgs, p)
		}
	}
	if err := in.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "testshard:", err)
		os.Exit(2)
	}

	table := seconds[runtime.GOOS]
	weight := func(p string) float64 {
		if s, ok := table[p]; ok {
			return s
		}
		return otherSeconds
	}
	sort.SliceStable(pkgs, func(a, b int) bool {
		if wa, wb := weight(pkgs[a]), weight(pkgs[b]); wa != wb {
			return wa > wb
		}
		return pkgs[a] < pkgs[b]
	})
	load := make([]float64, n)
	count := 0
	out := bufio.NewWriter(os.Stdout)
	for _, p := range pkgs {
		least := 0
		for s := range load {
			if load[s] < load[least] {
				least = s
			}
		}
		load[least] += weight(p)
		if least == i-1 {
			fmt.Fprintln(out, p)
			count++
		}
	}
	out.Flush()
	var all float64
	for _, s := range load {
		all += s
	}
	fmt.Fprintf(os.Stderr, "testshard: shard %d/%d of %s: %d of %d packages, ~%.0f s of ~%.0f s\n", i, n, runtime.GOOS, count, len(pkgs), load[i-1], all)
}
