//go:build windows

package locale

import "testing"

func TestWindowsReadsTheSystem(t *testing.T) {
	info := read()
	t.Logf("%+v", info)
	if len(info.Languages) == 0 {
		t.Error("no preferred UI languages")
	}
	if len(info.Region) != 2 {
		t.Errorf("region %q", info.Region)
	}
	if Serial() != 0 {
		t.Error("Windows reports no changes yet")
	}
}

func TestSplitMulti(t *testing.T) {
	enc := func(s ...string) []uint16 {
		var out []uint16
		for _, x := range s {
			for _, r := range x {
				out = append(out, uint16(r))
			}
			out = append(out, 0)
		}
		return append(out, 0)
	}
	got := splitMulti(enc("en-US", "pt-BR", "de"))
	if len(got) != 3 || got[0] != "en-US" || got[1] != "pt-BR" || got[2] != "de" {
		t.Errorf("%q", got)
	}
}
