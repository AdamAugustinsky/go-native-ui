package shell_test

import (
	"log"

	"gonativeui/app/shell"
	"gonativeui/task"
)

// Asking the desktop to open a link, open or show a file, or move it to the
// trash. The calls may touch the disk or the network, so they run on a task
// worker, never on the UI thread. OpenURL allows http, https and mailto;
// OpenPath runs whatever the system associates with the path, so pass only
// paths the user chose or the app wrote.
func Example() {
	type done struct{ OK bool } // a result holds no pointers
	pool := task.NewPool(task.Options{}, func(t task.Token, path string) done {
		if err := shell.OpenURL("https://example.com/help"); err != nil {
			log.Print(err)
		}
		if err := shell.Reveal([]string{path}); err != nil { // Finder / Explorer, selected
			log.Print(err)
		}
		return done{OK: shell.Trash(path) == nil} // recoverable, unlike os.Remove
	})
	defer pool.Close(0)
	pool.Submit("/Users/ana/Downloads/old.zip", 0)
}

// An app that hands out its own links lets their scheme through.
func ExampleOpenURLWith() {
	err := shell.OpenURLWith("zapfast://chat/42", shell.URLOptions{Schemes: []string{"zapfast"}})
	if err != nil {
		log.Print(err)
	}
}
