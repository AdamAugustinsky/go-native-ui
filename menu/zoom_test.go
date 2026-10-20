package menu

import (
	"strings"
	"testing"

	"gonativeui/ui"
)

const (
	cmdIn ID = iota + 40
	cmdOut
	cmdActual
	cmdPlain
)

func zoomModel(t *testing.T) *Model {
	t.Helper()
	m, err := NewModel([]Command{
		StandardCommand(cmdIn, RoleZoomIn), StandardCommand(cmdOut, RoleZoomOut), StandardCommand(cmdActual, RoleActualSize),
		{ID: cmdPlain, Title: "Plain", Shortcut: Shortcut{Key: 'P', Primary: true}},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestStandardZoomCommands(t *testing.T) {
	in, out, act := StandardCommand(1, RoleZoomIn), StandardCommand(2, RoleZoomOut), StandardCommand(3, RoleActualSize)
	if in.Title != "Zoom In" || out.Title != "Zoom Out" || act.Title != "Actual Size" {
		t.Errorf("titles %q %q %q", in.Title, out.Title, act.Title)
	}
	if c := StandardCommand(4, RoleCopy); c.Title != "" || c.Role != RoleCopy || !c.Shortcut.IsZero() {
		t.Errorf("another role: %+v", c)
	}
	if ui.PrimaryMod() == ui.ModSuper {
		if got := string(in.Shortcut.AppendLabel(nil)); got != "⌘+" {
			t.Errorf("Zoom In shows %q", got)
		}
		if in.Alias != (Shortcut{Key: '=', Primary: true}) {
			t.Errorf("Zoom In alias %+v, want ⌘=", in.Alias)
		}
	}
	if got := string(out.Shortcut.AppendLabel(nil)); !strings.HasSuffix(got, "-") {
		t.Errorf("Zoom Out shows %q", got)
	}
	if got := string(act.Shortcut.AppendLabel(nil)); !strings.HasSuffix(got, "0") {
		t.Errorf("Actual Size shows %q", got)
	}
	m := zoomModel(t)
	if id, ok := m.RoleID(RoleZoomOut); !ok || id != cmdOut {
		t.Errorf("RoleID(RoleZoomOut) = %v %v", id, ok)
	}
	if _, ok := m.RoleID(RoleCopy); ok {
		t.Error("RoleID found a role the model does not have")
	}
}

func TestAliasValidates(t *testing.T) {
	_, err := NewModel([]Command{
		{ID: 1, Title: "a", Shortcut: Shortcut{Key: 'A', Primary: true}, Alias: Shortcut{Key: 'B', Primary: true}},
		{ID: 2, Title: "b", Shortcut: Shortcut{Key: 'B', Primary: true}},
	}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "same shortcut") {
		t.Errorf("an alias shared with another command's shortcut: %v", err)
	}
	if _, err = NewModel([]Command{{ID: 1, Title: "a", Alias: Shortcut{Primary: true}}}, nil, nil); err == nil || !strings.Contains(err.Error(), "no key") {
		t.Errorf("an alias without a key: %v", err)
	}
}

func TestZoomCommandsAreTakenNotQueued(t *testing.T) {
	r := newRig(t)
	r.m = zoomModel(t)
	pm := ui.PrimaryMod()
	var got []ZoomAction
	var queued bool
	frame := func() {
		got, queued = nil, false
		r.frame(func(c *ui.Context) {
			r.m.Match(c)
			queued = r.m.Fired(cmdIn) || r.m.Fired(cmdOut) || r.m.Fired(cmdActual)
			for z, ok := r.m.TakeZoom(); ok; z, ok = r.m.TakeZoom() {
				got = append(got, z)
			}
		})
	}
	same := func(want ...ZoomAction) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("zoom actions %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("zoom actions %v, want %v", got, want)
			}
		}
	}

	// A native item (a command) comes first, then the chords in order: the
	// shortcuts and ⌘= (Ctrl+=, which is the shortcut elsewhere) for Zoom In.
	r.in.PushCommand(uint16(cmdOut))
	r.chord('-', pm)
	r.chord('0', pm)
	r.chord('P', pm) // another command: not a zoom
	r.chord('=', pm)
	frame()
	same(ZoomOut, ZoomOut, ZoomReset, ZoomIn)
	if queued {
		t.Error("a zoom command was queued for Fired")
	}

	// A disabled command does nothing, and actions do not carry over.
	r.m.SetEnabled(cmdOut, false)
	r.chord('-', pm)
	r.in.PushCommand(uint16(cmdOut))
	frame()
	same()
	r.in.PushCommand(uint16(cmdIn))
	r.frame(func(c *ui.Context) { r.m.Match(c) }) // not taken
	frame()
	same()
}

func TestZoomMatchAllocatesNothing(t *testing.T) {
	r := newRig(t)
	r.m = zoomModel(t)
	pm := ui.PrimaryMod()
	frame := func() {
		r.chord('-', pm)
		r.in.PushCommand(uint16(cmdIn))
		r.frame(func(c *ui.Context) {
			r.m.Match(c)
			for _, ok := r.m.TakeZoom(); ok; _, ok = r.m.TakeZoom() {
			}
		})
	}
	for i := 0; i < 5; i++ {
		frame()
	}
	if n := testing.AllocsPerRun(50, frame); n != 0 {
		t.Fatalf("%v allocations per frame", n)
	}
}

// A command at its limit shows disabled but is still delivered, in order: the
// limit is checked where the command acts, after the ones before it. A
// command that is disabled for good stays held back.
func TestLimitedCommandsAreStillDelivered(t *testing.T) {
	r := newRig(t)
	r.m = zoomModel(t)
	r.m.SetLimited(cmdActual, true)
	if r.m.Enabled(cmdActual) || !r.m.Enabled(cmdIn) {
		t.Fatal("a limited command shows enabled, or an unlimited one disabled")
	}
	if en, _ := r.m.State(r.c, cmdActual); en {
		t.Error("State of a limited command is enabled")
	}
	pm := ui.PrimaryMod()
	var got []ZoomAction
	frame := func() {
		got = nil
		r.frame(func(c *ui.Context) {
			r.m.Match(c)
			for z, ok := r.m.TakeZoom(); ok; z, ok = r.m.TakeZoom() {
				got = append(got, z)
			}
		})
	}
	r.in.PushCommand(uint16(cmdIn))
	r.in.PushCommand(uint16(cmdActual))
	frame()
	if len(got) != 2 || got[0] != ZoomIn || got[1] != ZoomReset {
		t.Errorf("Zoom In then Actual Size at its limit: %v", got)
	}
	r.chord('0', pm)
	frame()
	if len(got) != 1 || got[0] != ZoomReset {
		t.Errorf("the limited shortcut: %v", got)
	}
	r.m.SetLimited(cmdActual, false)
	r.m.SetEnabled(cmdActual, false)
	r.in.PushCommand(uint16(cmdActual))
	r.chord('0', pm)
	frame()
	if len(got) != 0 {
		t.Errorf("a disabled command was delivered: %v", got)
	}
}
