// Throwaway idle fixture: the same million-row model as pilot -bench,
// without the script or a real directory/clipboard.
package main

import (
	"gonativeui/app"
	"gonativeui/cmd/pilot/pilot"
	"gonativeui/fs"
	"gonativeui/platform"
	"os"
	"time"
)

func main() {
	if os.Getenv("PERF_NO_PREWARM") == "1" {
		synth := new(pilot.SynthSource)
		o := pilot.Options{Src: fs.NewServiceWith(platform.Wake, synth.List), Home: "/", Loc: time.UTC}
		for i, dir := range []string{"/synth/1000000", "/synth/100000"} {
			l := new(fs.Listing)
			if err := synth.List(dir, l); err != nil {
				panic(err)
			}
			fs.Sort(l, fs.SortName, false)
			ch := make(chan pilot.Ready, 1)
			ch <- pilot.Ready{Dir: dir, L: l, At: platform.Now()}
			o.Initial[i] = ch
		}
		p := pilot.New(o)
		defer p.Close()
		if err := app.Run(pilot.Config(), p); err != nil {
			panic(err)
		}
		return
	}
	b := pilot.NewSynthBench(1000000)
	defer b.Close()
	if err := app.Run(pilot.Config(), b.Pilot); err != nil {
		panic(err)
	}
}
