// Throwaway retained-memory probe; no windows or real filesystem operations.
package main

import (
	"encoding/json"
	"os"
	"runtime"
	"runtime/pprof"
	"gonativeui/cmd/pilot/pilot"
	"gonativeui/fs"
)

func snapshot() runtime.MemStats { runtime.GC(); var m runtime.MemStats; runtime.ReadMemStats(&m); return m }
func workload() map[string]int {
	l:=new(fs.Listing)
	pilot.FillSynth(l,1000000,0)
	fs.Sort(l,fs.SortName,false)
	r:=map[string]int{"Entries":len(l.Entries),"EntryCapacity":cap(l.Entries),"NameBytes":len(l.Names),"NameCapacity":cap(l.Names),"OrderCapacity":cap(l.Order)}
	runtime.KeepAlive(l)
	return r
}
func main() {
	before:=snapshot()
	counts:=workload()
	after:=snapshot()
	json.NewEncoder(os.Stdout).Encode(map[string]any{"Before":before,"AfterDroppingListing":after,"Counts":counts})
	if len(os.Args)>1 { f,e:=os.Create(os.Args[1]);if e!=nil {panic(e)};pprof.WriteHeapProfile(f);f.Close() }
}
