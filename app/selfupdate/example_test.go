package selfupdate_test

import (
	"log"

	"gonativeui/app/selfupdate"
)

// Replacing the running app with a newer copy of itself, and undoing it if
// the new copy does not start. Downloading the archive and checking the
// publisher's signature on it is the app's job (see app/fetch); this takes
// the archive from there.
func Example() {
	selfupdate.RunHelperIfAsked() // first thing in main: never returns in the helper

	// Settings → Updates
	if err := selfupdate.Supported(); err != nil {
		log.Print(err) // say why, and offer a download instead
		return
	}

	// "Restart to update", with the archive downloaded and checked:
	staged, err := selfupdate.Prepare("/path/to/Notes-1.2.zip") // unpacked and verified
	if err == nil {
		err = staged.InstallAndRelaunch(nil) // the helper takes over
	}
	if err != nil {
		log.Print(err)
		return
	}
	// ... then quit with a.Quit(): the helper waits for this process to end.
}

// After the first frame of every run the new version says it is healthy; the
// old one started again after a rollback learns why.
func ExampleStarted() {
	selfupdate.Started()
	if r := selfupdate.LastResult(); r.Status == selfupdate.RolledBack {
		log.Print("the update did not start: ", r.Reason) // tell the user
	}
}
