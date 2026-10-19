package paths

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestResolve(t *testing.T) {
	const id = "dev.gonativeui.zapfast"
	type row struct {
		goos string
		env  map[string]string
		want [4]string // config, data, cache, logs
	}
	rows := []row{
		{"darwin", map[string]string{"HOME": "/Users/a"}, [4]string{
			"/Users/a/Library/Application Support/" + id, "/Users/a/Library/Application Support/" + id,
			"/Users/a/Library/Caches/" + id, "/Users/a/Library/Logs/" + id}},
		{"windows", map[string]string{"APPDATA": `C:\Users\a\AppData\Roaming`, "LOCALAPPDATA": `C:\Users\a\AppData\Local\`}, [4]string{
			`C:\Users\a\AppData\Roaming\` + id, `C:\Users\a\AppData\Local\` + id,
			`C:\Users\a\AppData\Local\` + id + `\Cache`, `C:\Users\a\AppData\Local\` + id + `\Logs`}},
		{"windows", map[string]string{"USERPROFILE": `C:\Users\b`}, [4]string{
			`C:\Users\b\AppData\Roaming\` + id, `C:\Users\b\AppData\Local\` + id,
			`C:\Users\b\AppData\Local\` + id + `\Cache`, `C:\Users\b\AppData\Local\` + id + `\Logs`}},
		{"linux", map[string]string{"HOME": "/home/a"}, [4]string{
			"/home/a/.config/" + id, "/home/a/.local/share/" + id, "/home/a/.cache/" + id, "/home/a/.local/state/" + id + "/log"}},
		{"linux", map[string]string{"HOME": "/home/a", "XDG_CONFIG_HOME": "/x/c", "XDG_DATA_HOME": "/x/d",
			"XDG_CACHE_HOME": "/x/k", "XDG_STATE_HOME": "/x/s"}, [4]string{
			"/x/c/" + id, "/x/d/" + id, "/x/k/" + id, "/x/s/" + id + "/log"}},
		// A relative XDG variable is ignored, as the specification says.
		{"linux", map[string]string{"HOME": "/home/a", "XDG_CACHE_HOME": "relative/cache"}, [4]string{
			"/home/a/.config/" + id, "/home/a/.local/share/" + id, "/home/a/.cache/" + id, "/home/a/.local/state/" + id + "/log"}},
		{"linux", map[string]string{"XDG_CONFIG_HOME": "/x/c", "XDG_DATA_HOME": "/x/d", "XDG_CACHE_HOME": "/x/k", "XDG_STATE_HOME": "/x/s"}, [4]string{
			"/x/c/" + id, "/x/d/" + id, "/x/k/" + id, "/x/s/" + id + "/log"}},
	}
	for _, r := range rows {
		for k := kindConfig; k <= kindLogs; k++ {
			got, err := resolve(r.goos, envOf(r.env), k, id)
			if err != nil {
				t.Errorf("%s %v kind %d: %v", r.goos, r.env, k, err)
				continue
			}
			want := r.want[k]
			if got != want {
				t.Errorf("%s kind %d: %q, want %q", r.goos, k, got, want)
			}
		}
	}
}

func TestNoHome(t *testing.T) {
	for _, goos := range []string{"darwin", "windows", "linux"} {
		for k := kindConfig; k <= kindLogs; k++ {
			if _, err := resolve(goos, envOf(nil), k, "a.b"); !errors.Is(err, ErrNoHome) {
				t.Errorf("%s kind %d without a home: %v", goos, k, err)
			}
		}
	}
}

func TestValidID(t *testing.T) {
	good := []string{"dev.gonativeui.zapfast", "a", "My App", "app-1_x", "com.example.app.dev", "console.app"}
	bad := []string{"", ".", "..", ".hidden", "a/b", `a\b`, "a:b", "a*", "a?", `a"b`, "a<b", "a|b", "a\x00b", "a\nb", "trailing.", "trailing ",
		" leading", "CON", "nul", "Com1", "lpt9.txt", "../x", string(make([]byte, 129))}
	for _, id := range good {
		if !ValidID(id) {
			t.Errorf("%q rejected", id)
		}
	}
	for _, id := range bad {
		if ValidID(id) {
			t.Errorf("%q accepted", id)
		}
		for _, goos := range []string{"darwin", "windows", "linux"} {
			if _, err := resolve(goos, envOf(map[string]string{"HOME": "/h", "APPDATA": `C:\a`, "LOCALAPPDATA": `C:\l`}), kindData, id); !errors.Is(err, ErrInvalidID) {
				t.Errorf("%s resolve(%q): %v", goos, id, err)
			}
		}
	}
}

func TestHostFunctionsAndEnsure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "Local"))
	for _, d := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		t.Setenv(d, "")
	}
	seen := map[string]bool{}
	for name, f := range map[string]func(string) (string, error){"Config": Config, "Data": Data, "Cache": Cache, "Logs": Logs} {
		dir, err := f("dev.test.app")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !filepath.IsAbs(dir) {
			t.Errorf("%s: %q is not absolute", name, dir)
		}
		if name == "Cache" || name == "Logs" {
			if seen[dir] {
				t.Errorf("%s shares %q", name, dir)
			}
			seen[dir] = true
		}
		got, err := Ensure(dir)
		if err != nil || got != dir {
			t.Fatalf("Ensure(%q): %q %v", dir, got, err)
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Errorf("%s was not created: %v", dir, err)
		}
		if _, err := Ensure(dir); err != nil {
			t.Errorf("Ensure twice: %v", err)
		}
	}
}
