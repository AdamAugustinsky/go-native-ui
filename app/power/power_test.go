package power_test

import (
	"errors"
	"testing"

	"gonativeui/app"
	"gonativeui/app/power"
	"gonativeui/platform"
)

func TestHoldRefusesUnknownKind(t *testing.T) {
	if _, err := power.Hold(0, "x"); !errors.Is(err, power.ErrInvalid) {
		t.Fatalf("Hold(0) = %v, want ErrInvalid", err)
	}
	if _, err := power.Hold(9, "x"); !errors.Is(err, power.ErrInvalid) {
		t.Fatalf("Hold(9) = %v, want ErrInvalid", err)
	}
}

func TestEventOf(t *testing.T) {
	for _, c := range []struct {
		e    app.Event
		want power.Event
		ok   bool
	}{
		{app.Event{Kind: app.EvWillSleep}, power.WillSleep, true},
		{app.Event{Kind: app.EvDidWake}, power.DidWake, true},
		{app.Event{Kind: app.EvService, Source: platform.SourcePower, A: int64(power.ScreensSleep)}, power.ScreensSleep, true},
		{app.Event{Kind: app.EvService, Source: platform.SourcePower, A: int64(power.ScreensWake)}, power.ScreensWake, true},
		{app.Event{Kind: app.EvService, Source: platform.SourcePower, A: 99}, 0, false},
		{app.Event{Kind: app.EvService, Source: platform.SourceNotify, A: int64(power.ScreensWake)}, 0, false},
		{app.Event{Kind: app.EvActivated}, 0, false},
	} {
		got, ok := power.EventOf(&c.e)
		if got != c.want || ok != c.ok {
			t.Errorf("EventOf(%+v) = %v, %v; want %v, %v", c.e, got, ok, c.want, c.ok)
		}
	}
}
