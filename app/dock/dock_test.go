package dock_test

import (
	"errors"
	"math"
	"testing"

	"gonativeui/app/dock"
)

func TestBadgeLabel(t *testing.T) {
	for _, c := range []struct {
		n    int
		want string
	}{{-5, ""}, {0, ""}, {1, "1"}, {42, "42"}, {999, "999"}, {1000, "999+"}, {1 << 40, "999+"}} {
		if got := dock.BadgeLabel(c.n); got != c.want {
			t.Errorf("BadgeLabel(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestPolicyRefusesProhibited(t *testing.T) {
	// Not the app's choice to make at run time: refused before the OS is asked.
	if err := dock.SetPolicy(2); !errors.Is(err, dock.ErrPolicy) {
		t.Fatalf("SetPolicy(PolicyProhibited) = %v, want ErrPolicy", err)
	}
}

func TestProgressRefusesNaNAndNegatives(t *testing.T) {
	for _, f := range []float64{math.NaN(), -0.5, -3} {
		if err := dock.SetProgress(f); !errors.Is(err, dock.ErrProgress) {
			t.Errorf("SetProgress(%v) = %v, want ErrProgress", f, err)
		}
	}
	if dock.Progress() != dock.NoProgress {
		t.Errorf("Progress before any = %v, want NoProgress", dock.Progress())
	}
}

func TestCancelNothing(t *testing.T) {
	dock.CancelBounce(0) // no request: nothing to do, on any OS
}
