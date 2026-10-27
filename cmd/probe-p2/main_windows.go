//go:build windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

func init() { runtime.LockOSThread() } // windows and D3D on the main thread

// restCheck is what one read of the window at rest shows.
type restCheck struct {
	Read            string `json:"read"` // "screen" or "printwindow"
	OK              bool   `json:"ok"`
	ChildVisible    bool   `json:"child_visible"`     // green inside the container
	Clipped         bool   `json:"clipped"`           // the part above the container is not green
	OutsideColour   string `json:"outside_colour"`    // what shows there instead
	SwapChainBeside bool   `json:"swapchain_beside"`  // the swap chain's blue beside the container
	ContainerShown  bool   `json:"container_visible"` // the container's grey
}

type report struct {
	Driver        string         `json:"driver"`
	Adapter       string         `json:"adapter"`
	SwapEffect    string         `json:"swap_effect"`
	FeatureLevel  string         `json:"feature_level"`
	RefreshRate   string         `json:"refresh_rate"`
	RefreshMs     float64        `json:"refresh_period_ms"`
	RefreshHR     string         `json:"refresh_hr"`
	WindowAtChild bool           `json:"window_from_point_is_child"`
	Rest          []restCheck    `json:"rest"`
	CaptureOK     bool           `json:"capture_ok"`
	Frames        int            `json:"frames"`
	Speed         int            `json:"speed_px_per_frame"`
	Sampled       int            `json:"sampled"`
	Offsets       map[string]int `json:"offsets"`
	Trailing      map[string]int `json:"trailing"`
	ChildTrails   int            `json:"child_trails"`
	BandTrails    int            `json:"band_trails"`
	PresentErrors int            `json:"present_errors"`
	Errors        []string       `json:"errors,omitempty"`
}

var (
	frames  = flag.Int("frames", 120, "frames of the scroll measured")
	speed   = flag.Int("speed", 4, "pixels the list scrolls per frame")
	timeout = flag.Duration("timeout", 60*time.Second, "give up (exit 1) after this long")
)

func main() {
	flag.Parse()
	time.AfterFunc(*timeout, func() {
		fmt.Fprintf(os.Stderr, "probe-p2: timed out after %v\n", *timeout)
		os.Exit(1)
	})
	r := report{Offsets: map[string]int{}, Trailing: map[string]int{"0": 0, "1": 0, "2+": 0}, Frames: *frames, Speed: *speed}
	if err := makeWindows(); err != nil {
		fail(err)
	}
	if err := makeSwapChain(); err != nil {
		fail(err)
	}
	if err := makeDIB(); err != nil {
		fail(err)
	}
	r.Driver, r.Adapter, r.SwapEffect = gfx.driver, adapterName(), gfx.effect
	r.FeatureLevel = fmt.Sprintf("%#x", gfx.level)
	num, den, period, hr := refresh()
	r.RefreshHR = fmt.Sprintf("%#x", uint32(hr))
	if hr >= 0 && den != 0 {
		r.RefreshRate = fmt.Sprintf("%d/%d", num, den)
		if f := qpcFrequency(); f > 0 {
			r.RefreshMs = float64(period) * 1000 / float64(f)
		}
	}

	// At rest: the child pokes 10 px above the container.
	for i := 0; i < 10; i++ {
		draw(-1)
		pump()
		dwmFlush()
	}
	r.WindowAtChild = windowAt(sampleChild, childRestInsideY) == wins.child
	for _, read := range []string{"screen", "printwindow"} {
		var ok bool
		if read == "screen" {
			ok = grabScreen()
		} else {
			ok = grabPrint()
		}
		c := restCheck{Read: read, OK: ok && !blank()}
		if c.OK {
			out := colourAt(sampleChild, childRestOutsideY)
			c.ChildVisible = colourAt(sampleChild, childRestInsideY) == cGreen
			c.Clipped = out != cGreen
			c.OutsideColour = out.String()
			c.SwapChainBeside = colourAt(sampleBand, swapChainSampleY) == cBlue
			c.ContainerShown = colourAt(sampleChild, containerSampleY) == cGrey
		}
		r.Rest = append(r.Rest, c)
	}

	// The scroll. The band and the child move down together from the top
	// of the container; frames near a wrap are not sampled.
	const lo, span = clipY + 4, clipH - childH - 8
	yAt := func(i int) int { return lo + (i**speed)%span }
	warm := 10
	r.CaptureOK = r.Rest[0].OK
	for i := 0; i < warm+*frames; i++ {
		y := yAt(i)
		if hr := draw(y); hr < 0 {
			r.PresentErrors++
		}
		moveChild(y)
		pump()
		dwmFlush()
		if i < warm || !r.CaptureOK || (i**speed)%span < 3**speed {
			continue
		}
		if !grabScreen() {
			continue
		}
		band := topRow(sampleBand, clipY-childH, clipY+clipH, cRed)
		child := topRow(sampleChild, clipY, clipY+clipH, cGreen)
		if band < 0 || child < 0 {
			continue
		}
		r.Sampled++
		d := int(math.Round(float64(child-band) / float64(*speed)))
		r.Offsets[fmt.Sprint(d)]++
		switch a := max(d, -d); {
		case a == 0:
			r.Trailing["0"]++
		case a == 1:
			r.Trailing["1"]++
		default:
			r.Trailing["2+"]++
		}
		if d < 0 {
			r.ChildTrails++
		} else if d > 0 {
			r.BandTrails++
		}
	}
	if r.CaptureOK && r.Sampled == 0 {
		r.Errors = append(r.Errors, "no frame showed both the band and the child")
	}
	call(pDestroyWindow, wins.top)
	out, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(out))
}

func fail(err error) {
	out, _ := json.Marshal(map[string]string{"error": err.Error()})
	fmt.Println(string(out))
	os.Exit(1)
}

var qpcFreq int64

func qpcFrequency() int64 {
	call(win32.Proc(k32, "QueryPerformanceFrequency"), uintptr(unsafe.Pointer(&qpcFreq)))
	return qpcFreq
}
