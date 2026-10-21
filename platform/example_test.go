package platform_test

import (
	"fmt"
	"log"

	"gonativeui/platform"
)

// The window and its event loop, the lowest layer an app can use (app.Run is
// this loop plus the renderer, the text system and ui). The process's main
// thread runs it: platform locks the main goroutine to it, so Open and Wait
// are called from main. Wait blocks until there are events, a wake, a frame
// slot or the timeout; draw on EvFrame.
func Example() {
	w, err := platform.Open(platform.Options{Title: "Hello", Width: 640, Height: 400})
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()
	w.RequestFrame()
	for {
		for _, ev := range w.Wait(-1) {
			switch ev.Kind {
			case platform.EvClose:
				return
			case platform.EvResize:
				log.Println("content", ev.W, "x", ev.H, "pixels at", ev.Scale)
			case platform.EvFrame:
				// draw the frame (the gpu package), then ask for the next one
				// only if something is still animating
			case platform.EvWake:
				// a goroutine called platform.Wake: collect its results
			}
		}
	}
}

// Any goroutine can wake the loop, cheaply, when it has a result for it to
// collect: the task pool and the services call it for you.
func ExampleWake() {
	go func() {
		// ... finish some work, then:
		platform.Wake()
	}()
}

// The paths of a file paste: NUL-separated in the event's data.
func ExamplePasteFilePaths() {
	for _, p := range platform.PasteFilePaths([]byte("/tmp/a.png\x00/tmp/b.png\x00")) {
		fmt.Println(p)
	}
	// Output:
	// /tmp/a.png
	// /tmp/b.png
}
