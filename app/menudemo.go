package app

import (
	"os"
	"strconv"
	"strings"

	"gonativeui/platform"
	"gonativeui/ui"
)

// GNUI_MENU_DEMO opens a native menu once the window has settled, for
// screenshots of the menus (Run only; macOS):
//
//	bar:N              menu N of the menu bar (0: the application menu)
//	select:NAME        clicks the select named NAME (its native list menu)
//	pulldown:NAME      clicks the button named NAME (a MenuButton's pull-down)
//	context:N          right-clicks the N-th table row
//
// The clicks are synthetic pointer input, so the widget asks for its menu as
// it does for the user's click.
type menuDemo struct {
	kind, arg string
	step      int
	frame     int   // a.frames when the last step ran
	at        int64 // platform.Now when the demo starts
}

func newMenuDemo() *menuDemo {
	v := os.Getenv("GNUI_MENU_DEMO")
	kind, arg, ok := strings.Cut(v, ":")
	if !ok {
		return nil
	}
	return &menuDemo{kind: kind, arg: arg, at: platform.Now() + demoSettleNs}
}

// demoSettleNs is how long the window shows before the demo clicks.
const demoSettleNs = 1.5e9

// run advances the demo by one step per frame drawn.
func (d *menuDemo) run(a *App) {
	if d.step > 3 {
		return
	}
	if now := platform.Now(); now < d.at || a.menuDirty {
		if a.wakeAt == 0 || a.wakeAt > d.at {
			a.wakeAt = d.at // come round again then, even when idle
		}
		return
	}
	if a.frames == d.frame {
		return
	}
	d.frame = a.frames
	in := &a.tr.in
	button := ui.ButtonLeft
	if d.kind == "context" {
		button = ui.ButtonRight
	}
	switch d.step {
	case 0:
		if d.kind == "bar" {
			n, _ := strconv.Atoi(d.arg)
			a.Win.DebugOpenBarMenu(n)
			d.step = 4
			return
		}
		x, y, ok := d.target(a)
		if !ok {
			d.step = 4
			return
		}
		in.MouseX, in.MouseY, in.Inside = x, y, true
	case 1:
		in.SetButton(button, true, 1)
	case 2:
		in.SetButton(button, false, 0)
	}
	d.step++
	a.tr.dirty = true
	a.p.invalidate()
}

// target finds where to click: the centre of the semantic node the demo names.
func (d *menuDemo) target(a *App) (x, y float32, ok bool) {
	var t ui.SemTree
	a.UI.Semantics(&t)
	nth, _ := strconv.Atoi(d.arg)
	seen := 0
	for _, n := range t.Nodes {
		var hit bool
		switch d.kind {
		case "select":
			hit = n.Role == ui.RoleComboBox && string(t.Text(n.Name)) == d.arg
		case "pulldown":
			hit = n.Role == ui.RoleButton && string(t.Text(n.Name)) == d.arg
		case "context":
			if n.Role == ui.RoleRow {
				hit = seen == nth
				seen++
			}
		}
		if hit {
			return (n.Bounds[0] + n.Bounds[2]) / 2, (n.Bounds[1] + n.Bounds[3]) / 2, true
		}
	}
	return 0, 0, false
}
