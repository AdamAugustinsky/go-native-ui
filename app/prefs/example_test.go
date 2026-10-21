package prefs_test

import (
	"fmt"
	"os"
	"path/filepath"

	"gonativeui/app/prefs"
)

// Settings are the app's preferences: a plain struct, which is the file's
// JSON. A field the file lacks keeps its default, so adding one needs no
// migration.
type Settings struct {
	Theme  string
	Zoom   float64
	Recent []string
}

// Open once at startup, read anywhere without I/O, change in memory and let
// the store write the file a moment later. In an app the path is
// filepath.Join(dir, "settings.json") with dir from paths.Config(appID).
func Example() {
	dir, err := os.MkdirTemp("", "prefsexample")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "settings.json")

	s, err := prefs.Open(path, Settings{Theme: "system", Zoom: 1}, prefs.Options{})
	if err != nil {
		// the file was unreadable or corrupt: s still works, on the defaults
		fmt.Println("warning:", err)
	}
	fmt.Println(s.Get().Theme, s.Get().Zoom)

	s.Update(func(v *Settings) { v.Zoom = 1.25 }) // schedules a save
	s.Close()                                     // at quit: writes what is still pending

	again, _ := prefs.Open(path, Settings{Theme: "system", Zoom: 1}, prefs.Options{})
	defer again.Close()
	fmt.Println(again.Get().Theme, again.Get().Zoom)
	// Output:
	// system 1
	// system 1.25
}
