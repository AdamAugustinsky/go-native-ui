//go:build darwin

package menulive

import (
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

// ctxApp is a chat row that pops up a native menu described per click when it
// is right-clicked; the hook acts on the open menu as the user would.
type ctxApp struct {
	a *app.App
	k *widget.Kit

	act   string // what the user does with the open menu: choose, disabled, delete, dismiss
	frame int
	built int
	items []ui.MenuItem

	clicked bool
	opened  int
	got     int
	chosen  bool
	out     ui.MenuOutcome
	done    atomic.Bool
}

func (s *ctxApp) Init(a *app.App) error {
	s.a = a
	s.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}

func (s *ctxApp) Build(c *ui.Context) {
	s.frame++
	if s.done.Load() || s.frame > 600 {
		s.a.Quit()
		return
	}
	k := s.k
	k.Begin(c)
	row := rexui.ChatListRow(k.Kit, "r1", "Ada", "Lunch?", "10:42", rexui.ChatRowOpts{})
	i, ok := rexui.OnContextMenu(k.Kit, row.ID, func(m *[]ui.MenuItem) {
		s.built++
		*m = append(*m,
			ui.MenuItem{ID: 7, Title: "Pin", Symbol: "pin"},
			ui.MenuItem{ID: 8, Title: "Archive", Disabled: true},
			ui.MenuSeparator,
			ui.MenuItem{Title: "Labels", Sub: []ui.MenuItem{{ID: 20, Title: "Family", Mark: ui.MarkOn}, {ID: 21, Title: "Work"}}},
			ui.MenuItem{ID: 9, Title: "Delete", Destructive: true})
	})
	if ok {
		s.got, s.chosen, s.out = i, true, ui.MenuChosen
		s.done.Store(true)
	} else if _, out := c.ContextMenuResult(row.ID); out == ui.MenuDismissed {
		s.out = out
		s.done.Store(true)
	}
	k.End()
	if !s.clicked && s.frame == 8 {
		if x, y, found := centreOf(c, ui.RoleListItem, "Ada"); found {
			postMouse(s.a, 3, x, y) // right mouse down, up
			postMouse(s.a, 4, x, y)
			s.clicked = true
		}
	}
	c.RequestFrame()
}

func (s *ctxApp) onMenuOpen(m platform.DebugMenu) {
	s.opened = s.frame
	switch s.act {
	case "choose":
		m.Choose(0) // Pin
	case "disabled":
		m.Choose(1) // Archive, disabled: nothing happens, the menu closes
	case "delete":
		m.Choose(4)
	case "dismiss":
		m.Close()
	}
}

func runCtx(t *testing.T, act string) *ctxApp {
	t.Helper()
	s := &ctxApp{act: act}
	widgettest.OnMain(func() { platform.DebugOnMenuOpen(s.onMenuOpen) })
	defer widgettest.OnMain(func() { platform.DebugOnMenuOpen(nil) })
	dog := time.AfterFunc(20*time.Second, func() { s.done.Store(true); s.a.Quit(); platform.Wake() })
	run(t, s)
	dog.Stop()
	if s.opened == 0 {
		t.Fatalf("%s: the menu did not open (right-click at frame %v, %d frames, built %d)", act, s.clicked, s.frame, s.built)
	}
	return s
}

// A right-click on a row pops up a native menu built for that click, and the
// outcome comes back to the row in a later frame: the chosen item's id, or
// dismissed; a disabled item cannot be chosen.
func TestContextMenuRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		act    string
		chosen bool
		id     int
	}{{"choose", true, 7}, {"delete", true, 9}, {"disabled", false, 0}, {"dismiss", false, 0}} {
		t.Run(tc.act, func(t *testing.T) {
			s := runCtx(t, tc.act)
			if s.chosen != tc.chosen || s.got != tc.id {
				t.Errorf("chosen %v item %d, want %v %d (outcome %v)", s.chosen, s.got, tc.chosen, tc.id, s.out)
			}
			if !tc.chosen && s.out != ui.MenuDismissed {
				t.Errorf("outcome %v, want dismissed", s.out)
			}
			if s.built != 1 {
				t.Errorf("items built %d times for one click", s.built)
			}
		})
	}
}
