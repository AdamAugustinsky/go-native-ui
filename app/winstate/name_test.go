package winstate

import (
	"regexp"
	"strings"
	"testing"
)

// validName replaced the pattern ^[A-Za-z0-9_-]{1,64}$ (so that no application
// links regexp for one check). The table is what the pattern accepted and
// rejected; the second half checks every short string over a spread of bytes
// against the pattern itself.
func TestValidName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"main", true},
		{"inspector", true},
		{"a", true},
		{"Z", true},
		{"0", true},
		{"_", true},
		{"-", true},
		{"My-Window_2", true},
		{"-leading", true},
		{"_trailing_", true},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("-", 64), true},
		{"", false},
		{strings.Repeat("a", 65), false},
		{strings.Repeat("a", 200), false},
		{"has space", false},
		{" ", false},
		{"a.b", false},
		{"a/b", false},
		{"a\\b", false},
		{"..", false},
		{".", false},
		{"a\n", false},
		{"\na", false},
		{"a\x00", false},
		{"a\tb", false},
		{"ä", false},
		{"é", false},
		{"日本", false},
		{"a\xff", false},
		{"\xc3", false},
		{"a:b", false},
		{"a@b", false},
		{"[", false},
		{"`", false},
		{"{", false},
		{"/", false},
		{":", false},
	}
	for _, c := range cases {
		if got := validName(c.name); got != c.ok {
			t.Errorf("validName(%q) = %v, want %v", c.name, got, c.ok)
		}
	}

	re := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	alphabet := []byte("aZ09_-. /\\\n\x00\x7f\x80\xc3\xa9\xff:@[`{")
	var walk func(s []byte, depth int)
	walk = func(s []byte, depth int) {
		if got, want := validName(string(s)), re.MatchString(string(s)); got != want {
			t.Fatalf("validName(%q) = %v, the pattern says %v", s, got, want)
		}
		if depth == 0 {
			return
		}
		for _, b := range alphabet {
			walk(append(s[:len(s):len(s)], b), depth-1)
		}
	}
	walk(nil, 3)
	for _, n := range []int{63, 64, 65} {
		s := strings.Repeat("x", n)
		if validName(s) != re.MatchString(s) {
			t.Errorf("length %d differs from the pattern", n)
		}
	}
}
