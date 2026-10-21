package fs_test

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gonativeui/fs"
)

// tempDir makes a directory with a few files and a folder.
func tempDir() string {
	dir, err := os.MkdirTemp("", "fsexample")
	if err != nil {
		panic(err)
	}
	os.Mkdir(filepath.Join(dir, "Archive"), 0o755)
	for _, n := range []string{"file10.txt", "file2.txt", "Notes.md"} {
		os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644)
	}
	return dir
}

// List a directory in the natural order, folders first, and filter it by a
// search query. ListDir blocks: it is for tools and tests. An app lists
// through a Service (below).
func Example() {
	dir := tempDir()
	defer os.RemoveAll(dir)

	var l fs.Listing
	if err := fs.ListDir(dir, &l); err != nil {
		fmt.Println(err)
		return
	}
	fs.Sort(&l, fs.SortName, false) // fills l.Order
	for _, i := range l.Order {
		fmt.Println(string(l.Name(int(i))), l.Entries[i].Kind == fs.KindDir)
	}
	fmt.Println("matching \"FILE\":", len(fs.Filter(&l, []byte("FILE"), nil)))
	// Output:
	// Archive true
	// file2.txt false
	// file10.txt false
	// Notes.md false
	// matching "FILE": 2
}

// Listing off the UI thread: List only records the request, a worker lists and
// sorts, and the frame Polls after a wake (platform.Wake in an app). Every
// Listing Polled goes back with Release.
func ExampleService() {
	dir := tempDir()
	defer os.RemoveAll(dir)

	svc := fs.NewService(func() {}) // wake: platform.Wake in an app
	defer svc.Close()
	const pane = 1 // the caller's id for this pane
	svc.List(pane, dir, fs.SortName, false)

	var results []fs.Result
	for deadline := time.Now().Add(5 * time.Second); len(results) == 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		results = svc.Poll(results[:0]) // each frame, after a wake
	}
	for _, r := range results {
		if r.Kind == fs.ResultListing && r.Listing.Err == nil {
			fmt.Println(len(r.Listing.Entries), "entries")
			svc.Release(r.Listing, r.Lease) // when the pane shows another listing
		}
	}
	// Output: 4 entries
}
