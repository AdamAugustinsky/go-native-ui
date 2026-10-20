//go:build !darwin && !windows

package locale

import (
	"reflect"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestReadEnv(t *testing.T) {
	for _, c := range []struct {
		name string
		env  map[string]string
		fmt  string
		want Info
	}{
		{"US", map[string]string{"LANG": "en_US.UTF-8"}, "%r", Info{[]string{"en-US"}, "US", false}},
		{"Brazil, 24 hour", map[string]string{"LANG": "pt_BR.UTF-8"}, "%T", Info{[]string{"pt-BR"}, "BR", true}},
		{"LANGUAGE first, deduplicated", map[string]string{"LANGUAGE": "de:en_GB:de", "LANG": "de_DE.UTF-8"}, "%H:%M:%S",
			Info{[]string{"de", "en-GB", "de-DE"}, "DE", true}},
		{"LC_ALL wins", map[string]string{"LC_ALL": "fr_CA.UTF-8", "LANG": "en_US.UTF-8"}, "%r", Info{[]string{"fr-CA"}, "CA", false}},
		{"region from LC_TIME", map[string]string{"LANG": "en_US.UTF-8", "LC_TIME": "en_GB.UTF-8"}, "%T", Info{[]string{"en-US"}, "GB", true}},
		{"the C locale", map[string]string{"LANG": "C"}, "", Info{}},
		{"nothing set", nil, "", Info{}},
	} {
		got := readEnv(envOf(c.env), func() string { return c.fmt })
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
