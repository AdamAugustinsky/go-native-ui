package tray_test

import (
	"errors"
	"testing"

	"gonativeui/app"
	"gonativeui/app/tray"
	"gonativeui/platform"
)

func TestIcons(t *testing.T) {
	if s := tray.SymbolIcon("bell"); s.Symbol != "bell" || !s.Template {
		t.Errorf("SymbolIcon = %+v", s)
	}
	if p := tray.PNGIcon([]byte{1}, false); p.Template || len(p.PNG) != 1 {
		t.Errorf("PNGIcon = %+v", p)
	}
}

func TestNewBeforeTheAppStarts(t *testing.T) {
	// No application object in this process: no status item, whatever the OS.
	if _, err := tray.New(tray.Options{Icon: tray.SymbolIcon("bell")}); !errors.Is(err, tray.ErrNotStarted) && !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("New = %v, want ErrNotStarted (or unsupported)", err)
	}
}

func TestClickedIgnoresOtherEvents(t *testing.T) {
	var it tray.Item
	for _, e := range []app.Event{
		{Kind: app.EvActivated},
		{Kind: app.EvService, Source: platform.SourceNotify},
		{Kind: app.EvCommand, ID: 1},
	} {
		if _, ok := it.Clicked(&e); ok {
			t.Errorf("Clicked(%+v) is a click", e)
		}
	}
}

func TestBalloonContract(t *testing.T) {
	var it tray.Item
	if err := it.Balloon("Recording saved", "Uploading…"); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Balloon = %v, want unsupported (macOS has none; Windows until W5)", err)
	}
}
