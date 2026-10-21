package selfupdate

import (
	"errors"
	"testing"
)

func TestCheckNewer(t *testing.T) {
	const (
		newer = iota
		notNewer
		incomparable
	)
	for _, c := range []struct {
		short, build, curShort, curBuild string
		want                             int
	}{
		{"2.0", "1", "1.9", "99", newer},
		{"1.10", "", "1.9", "", newer}, // numbers, not text
		{"1.2.1", "", "1.2", "", newer},
		{"1.2", "", "1.2.0", "", notNewer}, // the same version
		{"1.9", "99", "2.0", "1", notNewer},
		{"0.9", "", "1.0", "", notNewer},
		{"1.0", "6", "1.0", "5", newer}, // the build decides
		{"1.0", "1.0.10", "1.0", "1.0.9", newer},
		{"1.0", "5", "1.0", "5", notNewer},
		{"1.0", "4", "1.0", "5", notNewer},
		{"1.0", "", "1.0", "5", notNewer},
		{"1.0", "5a", "1.0", "4", notNewer}, // builds that are not numbers never make it newer
		{"1.0b2", "", "1.0", "", incomparable},
		{"2.0", "", "1.0-dev", "", incomparable},
		{"", "", "1.0", "", incomparable},
		{"1..0", "", "1.0", "", incomparable},
		{"+1.0", "", "1.0", "", incomparable},
		{" 1.0", "", "1.0", "", incomparable},
		{"99999999999999999999", "", "1.0", "", incomparable},
	} {
		err := checkNewer(c.short, c.build, c.curShort, c.curBuild)
		var ok bool
		switch c.want {
		case newer:
			ok = err == nil
		case notNewer:
			ok = errors.Is(err, ErrNotNewer) && errors.Is(err, ErrRejected)
		case incomparable:
			ok = errors.Is(err, ErrRejected) && !errors.Is(err, ErrNotNewer)
		}
		if !ok {
			t.Errorf("checkNewer(%q, %q, %q, %q) = %v, want case %d", c.short, c.build, c.curShort, c.curBuild, err, c.want)
		}
	}
}
