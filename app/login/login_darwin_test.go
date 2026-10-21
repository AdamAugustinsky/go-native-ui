//go:build darwin

package login_test

import (
	"errors"
	"testing"

	"gonativeui/app/login"
)

// A test binary is not a bundle: the system cannot register it, and says so.
func TestNotBundled(t *testing.T) {
	if _, err := login.Status(); !errors.Is(err, login.ErrUnavailable) {
		t.Errorf("Status() = %v, want ErrUnavailable", err)
	}
	if _, err := login.Enable(); !errors.Is(err, login.ErrUnavailable) {
		t.Errorf("Enable() = %v, want ErrUnavailable", err)
	}
	if st, err := login.Disable(); !errors.Is(err, login.ErrUnavailable) || st != login.NotFound {
		t.Errorf("Disable() = %v, %v, want NotFound, ErrUnavailable", st, err)
	}
	if _, err := login.Agent("hidden").Enable(); !errors.Is(err, login.ErrUnavailable) {
		t.Errorf("Agent.Enable() = %v, want ErrUnavailable", err)
	}
}
