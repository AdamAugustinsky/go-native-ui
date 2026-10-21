package paths_test

import (
	"fmt"
	"path/filepath"

	"gonativeui/app/paths"
)

// Where the app keeps its files, per OS and keyed by the application ID
// (AppConfig.ID). The functions only compute names; Ensure creates the
// directory, once at startup or from a worker, never on the frame path.
func Example() {
	const id = "dev.example.notes"
	cfg, err := paths.Config(id) // ~/Library/Application Support/<id>, %APPDATA%\<id>, ~/.config/<id>
	if err != nil {
		return
	}
	if _, err := paths.Ensure(cfg); err != nil {
		return
	}
	settings := filepath.Join(cfg, "settings.json")
	cache, _ := paths.Cache(id) // regenerable files
	logs, _ := paths.Logs(id)
	_, _, _ = settings, cache, logs
}

// An ID is a reverse-DNS name; one that could climb out of the directory is
// refused.
func ExampleValidID() {
	fmt.Println(paths.ValidID("dev.example.notes"), paths.ValidID("../etc"), paths.ValidID(""))
	// Output: true false false
}
