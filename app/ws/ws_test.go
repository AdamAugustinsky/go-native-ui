package ws_test

import (
	"context"
	"errors"
	"testing"

	"gonativeui/app/ws"
)

func TestDialContract(t *testing.T) {
	ctx := context.Background()
	if _, err := ws.Dial(ctx, "https://example.com", ws.Options{}); !errors.Is(err, ws.ErrBadURL) {
		t.Fatalf("https URL: %v", err)
	}
	if _, err := ws.Dial(ctx, "ws://127.0.0.1:1/x", ws.Options{}); err == nil {
		t.Fatal("Dial to a closed port succeeded")
	} else if !errors.Is(err, errors.ErrUnsupported) {
		t.Logf("Dial: %v (a backend exists here)", err)
	}
	var ce error = &ws.CloseError{Code: ws.CloseGoingAway, Reason: "bye"}
	var target *ws.CloseError
	if !errors.As(ce, &target) || target.Code != 1001 {
		t.Fatal("CloseError")
	}
}
