//go:build windows

package secret

import "testing"

func TestTargetIsUnambiguous(t *testing.T) {
	seen := map[string][2]string{}
	for _, c := range [][2]string{{"a", "b/c"}, {"a/b", "c"}, {"a%2Fb", "c"}, {"a", "b"}, {"a%", "b"}} {
		n := target(c[0], c[1])
		if prev, dup := seen[n]; dup {
			t.Errorf("%q and %q both name %q", prev, c, n)
		}
		seen[n] = c
	}
}
