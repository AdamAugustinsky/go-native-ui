package appkitref

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Sets says which machine each committed reference was measured on; the
// files say it too ("machine"). They must agree, or Select would pick a set
// for the wrong host.
func TestSetsMatchTheCommittedFiles(t *testing.T) {
	for _, s := range Sets {
		for _, f := range []string{"rexui/testdata/appkit-27.2.json", "theme/testdata/appkit-scroller-27.2.json", "ui/testdata/appkit-scroll/machine.json"} {
			path := filepath.Join("..", "..", File(s.Name, f))
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("%s: %v", s.Name, err)
				continue
			}
			var m struct {
				Machine struct {
					Model string `json:"model"`
					MacOS struct {
						Build string `json:"build"`
					} `json:"macOS"`
				} `json:"machine"`
			}
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Errorf("%s: %v", path, err)
				continue
			}
			if m.Machine.Model != s.Model || m.Machine.MacOS.Build != s.Build {
				t.Errorf("%s: machine %q %q, Sets says %q %q", path, m.Machine.Model, m.Machine.MacOS.Build, s.Model, s.Build)
			}
		}
	}
}

func TestFileAndEnv(t *testing.T) {
	if got := File("macbook", "testdata/appkit-27.2.json"); got != "testdata/appkit-27.2.json" {
		t.Errorf("macbook file renamed: %s", got)
	}
	if got := File("macmini", "testdata/appkit-27.2.json"); got != "testdata/appkit-27.2-macmini.json" {
		t.Errorf("macmini file: %s", got)
	}
	if got := File("macmini", "wheel-mid.appkit"); got != "wheel-mid-macmini.appkit" {
		t.Errorf("macmini file: %s", got)
	}
	t.Setenv(EnvVar, "macmini")
	if s, err := Select(); s != "macmini" || err != nil {
		t.Errorf("Select with the variable: %q, %v", s, err)
	}
	t.Setenv(EnvVar, "bogus")
	if _, err := Select(); err == nil {
		t.Error("Select accepted an unknown set")
	}
	t.Setenv(EnvVar, "")
	if s, err := Select(); err != nil || (s != "macbook" && s != "macmini") {
		t.Errorf("Select on this host: %q, %v", s, err)
	}
}
