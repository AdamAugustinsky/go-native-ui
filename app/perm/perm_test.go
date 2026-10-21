package perm

import (
	"errors"
	"testing"

	"gonativeui/app"
	"gonativeui/platform"
)

func TestResultRoundTrip(t *testing.T) {
	for _, r := range []Result{
		{Ticket: 7, Kind: Microphone, Granted: true, State: Authorized},
		{Ticket: 1 << 50, Kind: Camera, Granted: false, State: Denied},
		{Ticket: 3, Kind: Microphone, Granted: false, State: Restricted},
	} {
		a, b := encode(r.Kind, r.Granted, r.State)
		e := &app.Event{Kind: app.EvService, Source: platform.SourcePerm, ID: uint64(r.Ticket), A: a, B: b}
		got, ok := ResultOf(e)
		if !ok || got != r {
			t.Errorf("ResultOf(encode(%+v)) = %+v, %v", r, got, ok)
		}
	}
	for _, e := range []*app.Event{
		nil,
		{Kind: app.EvService, Source: platform.SourceMedia, A: 1},
		{Kind: app.EvPost, Source: platform.SourcePerm, A: 1},
	} {
		if _, ok := ResultOf(e); ok {
			t.Errorf("ResultOf(%+v) decoded an event that is not a permission answer", e)
		}
	}
}

func TestInvalidKind(t *testing.T) {
	for _, k := range []Kind{0, Camera + 1, 255} {
		if _, err := Status(k); !errors.Is(err, ErrInvalid) {
			t.Errorf("Status(%v) = %v, want ErrInvalid", k, err)
		}
		if _, err := Request(k); !errors.Is(err, ErrInvalid) {
			t.Errorf("Request(%v) = %v, want ErrInvalid", k, err)
		}
		if err := OpenSettings(k); !errors.Is(err, ErrInvalid) {
			t.Errorf("OpenSettings(%v) = %v, want ErrInvalid", k, err)
		}
	}
}
