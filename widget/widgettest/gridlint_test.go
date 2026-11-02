package widgettest

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSpacingGridOffenders(t *testing.T) {
	bad, err := SpacingGridOffenders(GridOpts{Dir: filepath.Join("testdata", "gridlint")})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range bad {
		// "testdata/gridlint/screens.go:5:16: 6 in rowGap" → "5: 6 in rowGap"
		_, rest, _ := strings.Cut(b, "screens.go:")
		line, more, _ := strings.Cut(rest, ":")
		_, what, _ := strings.Cut(more, ": ")
		got = append(got, line+": "+what)
	}
	want := []string{
		"5: 6 in rowGap",
		"12: 10 in PadY",
		"16: 5 in NextPadding",
		"19: 5 in NextGap",
		"20: 7 in NextGap",
		"21: 9 in NextGap",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("offenders:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
