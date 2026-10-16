//go:build darwin

// Throwaway phase-0 probe: can a cgo-free Go binary open an AppKit window,
// clear it with Metal and present, within the startup / RSS / size budgets?
// Uses purego's convenience objc API (allocating; fine for a probe).
//
//	go build -trimpath -ldflags="-s -w" -o /tmp/macwin ./probes/macwin
//	GNUI_T0=$(date +%s%N) /tmp/macwin -exit   # prints startup timings
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

func init() { runtime.LockOSThread() }

type NSRect struct{ X, Y, W, H float64 }
type CGSize struct{ W, H float64 }
type MTLClearColor struct{ R, G, B, A float64 }

var mainStart = time.Now()

func sel(s string) objc.SEL { return objc.RegisterName(s) }
func cls(s string) objc.ID  { return objc.ID(objc.GetClass(s)) }

func nsString(s string) objc.ID {
	b := append([]byte(s), 0)
	return cls("NSString").Send(sel("stringWithUTF8String:"), unsafe.Pointer(&b[0]))
}

func main() {
	exit := flag.Bool("exit", false, "exit after first frame and print timings")
	flag.Parse()

	t := func() float64 { return float64(time.Since(mainStart).Microseconds()) / 1000 }
	for _, fw := range []string{
		"/System/Library/Frameworks/AppKit.framework/AppKit",
		"/System/Library/Frameworks/QuartzCore.framework/QuartzCore",
		"/System/Library/Frameworks/Metal.framework/Metal",
	} {
		if _, err := purego.Dlopen(fw, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			panic(err)
		}
	}
	tLoad := t()
	metal, _ := purego.Dlopen("/System/Library/Frameworks/Metal.framework/Metal", purego.RTLD_NOW)
	var mtlCreate func() objc.ID
	purego.RegisterLibFunc(&mtlCreate, metal, "MTLCreateSystemDefaultDevice")

	pool := cls("NSAutoreleasePool").Send(sel("new"))
	app := cls("NSApplication").Send(sel("sharedApplication"))
	app.Send(sel("setActivationPolicy:"), 0) // NSApplicationActivationPolicyRegular
	tApp := t()

	style := uintptr(1 | 2 | 4 | 8) // titled|closable|miniaturizable|resizable
	win := cls("NSWindow").Send(sel("alloc")).Send(sel("initWithContentRect:styleMask:backing:defer:"),
		NSRect{200, 200, 960, 600}, style, uintptr(2), false)
	win.Send(sel("setTitle:"), nsString("gnui probe"))
	tWinCreate := t()
	view := win.Send(sel("contentView"))

	tDev0 := t()
	dev := mtlCreate()
	tDev := t() - tDev0
	queue := dev.Send(sel("newCommandQueue"))
	layer := cls("CAMetalLayer").Send(sel("layer"))
	layer.Send(sel("setDevice:"), dev)
	layer.Send(sel("setPixelFormat:"), uintptr(80)) // MTLPixelFormatBGRA8Unorm
	layer.Send(sel("setFramebufferOnly:"), true)
	scale := objc.Send[float64](win, sel("backingScaleFactor"))
	layer.Send(sel("setContentsScale:"), scale)
	layer.Send(sel("setDrawableSize:"), CGSize{960 * scale, 600 * scale})
	view.Send(sel("setLayer:"), layer)
	view.Send(sel("setWantsLayer:"), true)

	tLayer := t()
	win.Send(sel("makeKeyAndOrderFront:"), objc.ID(0))
	tOrder := t()
	app.Send(sel("finishLaunching"))
	tFinish := t()
	app.Send(sel("activateIgnoringOtherApps:"), true)
	tWin := t()

	drawable := layer.Send(sel("nextDrawable"))
	rpd := cls("MTLRenderPassDescriptor").Send(sel("renderPassDescriptor"))
	ca0 := rpd.Send(sel("colorAttachments")).Send(sel("objectAtIndexedSubscript:"), uintptr(0))
	ca0.Send(sel("setTexture:"), drawable.Send(sel("texture")))
	ca0.Send(sel("setLoadAction:"), uintptr(2))  // Clear
	ca0.Send(sel("setStoreAction:"), uintptr(1)) // Store
	ca0.Send(sel("setClearColor:"), MTLClearColor{0.11, 0.12, 0.14, 1})
	cb := queue.Send(sel("commandBuffer"))
	enc := cb.Send(sel("renderCommandEncoderWithDescriptor:"), rpd)
	enc.Send(sel("endEncoding"))
	cb.Send(sel("presentDrawable:"), drawable)
	cb.Send(sel("commit"))
	cb.Send(sel("waitUntilCompleted"))
	tFrame := t()
	pool.Send(sel("drain"))

	if *exit {
		var procStart float64 = -1
		if s := os.Getenv("GNUI_T0"); s != "" {
			ns, _ := strconv.ParseInt(s, 10, 64)
			procStart = float64(time.Now().UnixNano()-ns)/1e6 - t()
		}
		fmt.Printf("exec->main %.1fms | dlopen %.1f | NSApp %.1f | NSWindow %.1f | MTLDevice %.2f | layer %.1f | orderFront %.1f | finishLaunching %.1f | activate %.1f | frame %.1f (cumulative ms since main)\n",
			procStart, tLoad, tApp, tWinCreate, tDev, tLayer, tOrder, tFinish, tWin, tFrame)
		return
	}
	// Idle: block in the AppKit event loop; nothing renders after the first frame.
	distantFuture := cls("NSDate").Send(sel("distantFuture"))
	mode := nsString("kCFRunLoopDefaultMode")
	for {
		p := cls("NSAutoreleasePool").Send(sel("new"))
		ev := app.Send(sel("nextEventMatchingMask:untilDate:inMode:dequeue:"), ^uintptr(0), distantFuture, mode, true)
		if ev != 0 {
			app.Send(sel("sendEvent:"), ev)
		}
		p.Send(sel("drain"))
		if win.Send(sel("isVisible")) == 0 {
			return
		}
	}
}
