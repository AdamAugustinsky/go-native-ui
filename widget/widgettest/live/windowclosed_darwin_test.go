//go:build darwin

package live

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/widget/widgettest"
)

func init() {
	// gaps-windowclosed: three windows; the second is closed with its close
	// button, the third from code, and the app quits with the first open.
	// The handler is told once per window, with the window gone, and the
	// last one before EvWillTerminate.
	widgettest.RegisterScenario("gaps-windowclosed", func() {
		var log []string
		var ws [3]*app.Win
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			for i := range ws {
				w, err := a.OpenWindow(mwConfig(fmt.Sprint("w", i+1)), &closer{})
				if err != nil {
					widgettest.Say("skip=no-window")
					os.Exit(0)
				}
				w.SetFrame(20+float64(i)*30, 60, 360, 268)
				ws[i] = w
			}
			widgettest.After(400*time.Millisecond, func() {
				pressClose(ws[1])
				widgettest.After(300*time.Millisecond, func() {
					ws[2].Close()
					widgettest.After(300*time.Millisecond, func() { a.Quit() })
				})
			})
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			switch e.Kind {
			case app.EvWindowClosed:
				name := "?"
				for i, w := range ws {
					if w.ID() == e.Window {
						name = fmt.Sprintf("w%d:open=%v", i+1, w.Open())
					}
				}
				log = append(log, name)
			case app.EvWillTerminate:
				log = append(log, "terminate")
				widgettest.Say("events=%s", strings.Join(log, ","))
			}
			return false
		}
		app.Main(app.AppConfig{}, h)
	})
}

// EvWindowClosed arrives once per window however it closed (close button,
// Win.Close, the app quitting), with the window already gone, and before
// EvWillTerminate.
func TestWindowClosedEventForEveryWayAWindowCloses(t *testing.T) {
	r := widgettest.Scenario(t, "gaps-windowclosed", 60*time.Second)
	if want := "w2:open=false,w3:open=false,w1:open=true,terminate"; r.Get("events") != want {
		t.Errorf("events %q, want %q\n%s", r.Get("events"), want, r.Out)
	}
}
