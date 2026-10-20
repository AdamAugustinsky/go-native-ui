package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gonativeui/internal/appkitref"
	"gonativeui/platform"
	"gonativeui/theme"
)

// LoadSystem reads the light and dark system colours of an AppKitRef
// metrics.json into SystemValues, for renders that reproduce that run's
// appearance (base.Kit.PinSystem). "auto" is the metrics.json of this host's
// reference set (internal/appkitref: GNUI_APPKIT_REF, else the set measured on
// this model and macOS build).
func LoadSystem(path string) (*theme.SystemValues, error) {
	if path == "auto" {
		set, err := appkitref.Select()
		if err != nil {
			return nil, err
		}
		path = appkitref.Metrics(set)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m struct {
		Colors map[string]map[string]json.RawMessage `json:"colors"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	s := &theme.SystemValues{Known: true}
	for mode, ap := range [...]string{"NSAppearanceNameAqua", "NSAppearanceNameDarkAqua"} {
		cs := m.Colors[ap]
		if cs == nil {
			return nil, fmt.Errorf("%s: no colours for %s", path, ap)
		}
		for i, name := range platform.SystemColorNames {
			raw, ok := cs[name]
			if !ok {
				continue
			}
			var hex string
			if json.Unmarshal(raw, &hex) != nil { // alternatingContentBackgroundColors: a list
				var list []string
				if json.Unmarshal(raw, &list) != nil || len(list) < 2 {
					continue
				}
				hex = list[1]
			}
			v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
			if err != nil {
				return nil, fmt.Errorf("%s: %s = %q", path, name, hex)
			}
			s.Colors[mode][i] = uint32(v)
		}
	}
	return s, nil
}
