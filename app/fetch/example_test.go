package fetch_test

import (
	"context"
	"errors"
	"os"
	"time"

	"gonativeui/app/fetch"
)

// Downloading a file, streaming the body to a writer, and reading a small
// JSON document into memory. Both block until the transfer ends: call them
// from a task worker, never from the UI thread. Cancelling the context stops
// the transfer promptly.
func Example() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f, err := os.CreateTemp("", "update-*.zip")
	if err != nil {
		return
	}
	defer f.Close()
	_, err = fetch.Get(ctx, "https://example.com/app-1.2.zip", f, fetch.Options{
		Timeout:  5 * time.Minute,
		MaxBytes: 200 << 20,
		Progress: func(got, total int64) {}, // total is -1 when unknown
	})
	var se *fetch.StatusError
	if errors.As(err, &se) && se.Code == 404 {
		// no such release
	}

	manifest, res, err := fetch.Bytes(ctx, "https://example.com/latest.json", 1<<20, fetch.Options{})
	_, _, _ = manifest, res, err
}
