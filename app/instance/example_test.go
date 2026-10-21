package instance_test

import (
	"log"
	"os"

	"gonativeui/app/instance"
)

func openFiles(dir string, args []string) {}

// One running copy per user: the first launch is the primary and listens, a
// later launch is the secondary, hands its command line to the first and
// exits. Call it first thing in main.
func Example() {
	const appID = "dev.example.notes"
	primary, secondary, err := instance.Acquire(appID)
	if err != nil {
		log.Print(err) // cannot tell: run anyway, or report
	}
	if secondary != nil {
		// Another copy is running: ask it to open our arguments, and leave.
		if err := secondary.Send(os.Args[1:]); err != nil {
			log.Printf("the running copy did not take the request: %v", err)
		}
		return
	}
	if primary == nil {
		return
	}
	defer primary.Close()

	// in the loop (a wake arrives when a request has), or from the handler of
	// an event the app posts from Options.Notify:
	var buf []instance.Request
	for _, r := range primary.Poll(buf[:0]) {
		openFiles(r.Dir, r.Args) // r.Dir resolves relative paths in r.Args
	}
}
