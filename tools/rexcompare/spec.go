package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Spec is the content of docs/design/rex-regions.json: everything a checker
// needs, in reference pixels, so that any implementation gets the same answer
// (docs/design/rex-benchmark.md, "Benchmark app and acceptance").
type Spec struct {
	Note       string     `json:"note,omitempty"`
	Ref        Size       `json:"ref"`
	Thresholds Thresholds `json:"thresholds"`
	Regions    []Region   `json:"regions"`
	Probes     []Probe    `json:"probes"`
	Text       TextSpec   `json:"text"`
}

// Size is an image size in pixels.
type Size struct {
	W int `json:"w"`
	H int `json:"h"`
}

// Thresholds are the acceptance limits.
type Thresholds struct {
	MedianDE float64 `json:"medianDeltaE"` // per-region median CIEDE2000
	P95DE    float64 `json:"p95DeltaE"`    // per-region 95th percentile CIEDE2000
	EdgePx   float64 `json:"edgePx"`       // allowed edge position difference, px
	TextPct  float64 `json:"textPct"`      // allowed text metric difference, percent
}

// Region is a set of rectangles compared pixel by pixel as one population:
// the median and the 95th percentile of the per-pixel CIEDE2000 between the
// reference and the candidate. Rectangles are [x0, y0, x1, y1) in reference
// pixels. They exclude text, icons, a 3 px band at every image edge and the
// shadow zones.
type Region struct {
	Name  string   `json:"name"`
	Group string   `json:"group"` // background | pane-focused | pane-unfocused | tabs
	Rects [][4]int `json:"rects"`
}

// Probe is a scanline that crosses one edge. The edge is the 50 % crossing
// between the plateau colours of the ranges A and B (ranges [from, to) along
// the scanline). It is measured in both images and must agree within
// Thresholds.EdgePx.
type Probe struct {
	Name string `json:"name"`
	Axis string `json:"axis"` // "x": the scanline is the row y=At, running along x; "y": the column x=At, running along y
	At   int    `json:"at"`
	A    [2]int `json:"a"`
	B    [2]int `json:"b"`
}

// TextSpec describes the text metric checks. The advance and pitch are
// measured on both images as the period of the ink profile (a DFT peak) over
// bands that hold at least 40 cells on a row and 8 rows. The candidate's own
// layout geometry (written by rexlook next to the PNG as <png>.metrics.json)
// is checked against the nominal values.
type TextSpec struct {
	NominalCellAdvancePt float64    `json:"nominalCellAdvancePt"`
	NominalRowPitchPt    float64    `json:"nominalRowPitchPt"`
	PxPerPt              float64    `json:"pxPerPt"`
	Advance              []TextBand `json:"advance"`
	Pitch                []TextBand `json:"pitch"`
}

// TextBand is a rectangle [x0, y0, x1, y1) whose ink profile is folded along
// one axis; Min is the number of periods it must contain.
type TextBand struct {
	Name string `json:"name"`
	Rect [4]int `json:"rect"`
	Min  int    `json:"min"`
}

// LoadSpec reads and validates a regions file.
func LoadSpec(path string) (*Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.Thresholds.MedianDE == 0 {
		s.Thresholds.MedianDE = 3
	}
	if s.Thresholds.P95DE == 0 {
		s.Thresholds.P95DE = 6
	}
	if s.Thresholds.EdgePx == 0 {
		s.Thresholds.EdgePx = 1
	}
	if s.Thresholds.TextPct == 0 {
		s.Thresholds.TextPct = 1
	}
	if s.Text.PxPerPt == 0 {
		s.Text.PxPerPt = 2
	}
	for _, r := range s.Regions {
		for _, q := range r.Rects {
			if q[0] >= q[2] || q[1] >= q[3] || q[0] < 0 || q[1] < 0 || q[2] > s.Ref.W || q[3] > s.Ref.H {
				return nil, fmt.Errorf("%s: region %q has an invalid rect %v", path, r.Name, q)
			}
		}
	}
	for _, p := range s.Probes {
		if p.Axis != "x" && p.Axis != "y" {
			return nil, fmt.Errorf("%s: probe %q: axis must be x or y", path, p.Name)
		}
		if p.A[0] >= p.A[1] || p.B[0] >= p.B[1] || p.A[1] > p.B[0] {
			return nil, fmt.Errorf("%s: probe %q: ranges must be ordered a < b", path, p.Name)
		}
	}
	return &s, nil
}
