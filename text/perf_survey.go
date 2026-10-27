package text

// Throwaway survey counters. No allocation or printing on the frame path.
import (
	"fmt"
	"os"
)

var PerfEnabled = os.Getenv("GNUI_TEXT_PERF") != ""

type perfCounts struct {
	RunHit, RunMiss, RunHashBytes, RunEvict     uint64
	ParaHit, ParaMiss, ParaHashBytes, ParaEvict uint64
	GlyphHit, Raster, RasterBytes, AtlasReset   uint64
	Uploads, UploadBytes, FontNew, FaceSlow     uint64
}

var perf perfCounts
var perfFrames [32768]perfCounts
var perfN int

func PerfEnd() {
	if PerfEnabled && perfN < len(perfFrames) {
		perfFrames[perfN] = perf
		perfN++
	}
}

func perfDump() {
	if !PerfEnabled {
		return
	}
	f, err := os.Create(os.Getenv("GNUI_TEXT_PERF"))
	if err != nil {
		panic(err)
	}
	defer f.Close()
	fmt.Fprintln(f, "frame,run_hit,run_miss,run_hash_bytes,run_evict,para_hit,para_miss,para_hash_bytes,para_evict,glyph_hit,raster,raster_bytes,atlas_reset,uploads,upload_bytes,font_new,face_slow")
	prev := perfCounts{}
	for i := 0; i <= perfN; i++ {
		v := perf
		if i < perfN {
			v = perfFrames[i]
		}
		fmt.Fprintf(f, "%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d\n", i,
			v.RunHit-prev.RunHit, v.RunMiss-prev.RunMiss, v.RunHashBytes-prev.RunHashBytes, v.RunEvict-prev.RunEvict,
			v.ParaHit-prev.ParaHit, v.ParaMiss-prev.ParaMiss, v.ParaHashBytes-prev.ParaHashBytes, v.ParaEvict-prev.ParaEvict,
			v.GlyphHit-prev.GlyphHit, v.Raster-prev.Raster, v.RasterBytes-prev.RasterBytes, v.AtlasReset-prev.AtlasReset,
			v.Uploads-prev.Uploads, v.UploadBytes-prev.UploadBytes, v.FontNew-prev.FontNew, v.FaceSlow-prev.FaceSlow)
		prev = v
	}
}
