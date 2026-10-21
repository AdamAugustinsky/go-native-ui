package selfupdate

import (
	"fmt"
	"strconv"
	"strings"
)

// checkNewer returns nil when the staged version (short, build) is newer than
// the running one (curShort, curBuild), and otherwise an error matching
// ErrNotNewer, or ErrRejected when the versions cannot be compared.
//
// The rule: a version is one or more period-separated decimal integers, the
// form Apple documents for CFBundleShortVersionString and CFBundleVersion
// ("1.2.3"); a missing trailing component counts as 0, so 1.2 and 1.2.0 are
// the same. CFBundleShortVersionString decides; when the two are the same,
// CFBundleVersion decides, and the update is newer only if both builds are
// numbers and the staged one is greater. A short version that is not in that
// form ("1.2b3", "") cannot be compared, and the update is rejected.
func checkNewer(short, build, curShort, curBuild string) error {
	s, err := parseVersion(short)
	if err != nil {
		return fmt.Errorf("%w: the update's version %q cannot be compared: %v", ErrRejected, short, err)
	}
	c, err := parseVersion(curShort)
	if err != nil {
		return fmt.Errorf("%w: the running version %q cannot be compared: %v", ErrRejected, curShort, err)
	}
	switch compareVersions(s, c) {
	case 1:
		return nil
	case -1:
		return fmt.Errorf("%w: %s is older than the running %s", ErrNotNewer, short, curShort)
	}
	sb, err1 := parseVersion(build)
	cb, err2 := parseVersion(curBuild)
	if err1 == nil && err2 == nil && compareVersions(sb, cb) > 0 {
		return nil
	}
	return fmt.Errorf("%w: %s (build %q) is not newer than the running %s (build %q)", ErrNotNewer, short, build, curShort, curBuild)
}

func parseVersion(v string) ([]uint64, error) {
	if v == "" {
		return nil, fmt.Errorf("empty")
	}
	parts := strings.Split(v, ".")
	out := make([]uint64, len(parts))
	for i, p := range parts {
		if p == "" || len(p) > 18 || strings.TrimLeft(p, "0123456789") != "" {
			return nil, fmt.Errorf("%q is not a number", p)
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nil, err
		}
		out[i] = n
	}
	return out, nil
}

// compareVersions returns -1, 0 or 1 as a is older than, the same as or
// newer than b.
func compareVersions(a, b []uint64) int {
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y uint64
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		switch {
		case x > y:
			return 1
		case x < y:
			return -1
		}
	}
	return 0
}
