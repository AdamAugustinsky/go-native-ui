package fetch_test

import (
	"bytes"
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

// Uploading a file with a known length and progress, and reading an API's
// JSON answer whatever its status.
func ExampleDo() {
	f, err := os.Open("recording.mp4")
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	var answer bytes.Buffer
	res, err := fetch.Do(context.Background(), fetch.Request{
		Method:     "PUT",
		URL:        "https://kb.example/api/v1/recordings/a1/parts/1",
		Body:       f, // read in pieces as it is sent: constant memory
		BodyLength: st.Size(),
	}, &answer, fetch.Options{
		Headers:        map[string]string{"Authorization": "Bearer token", "Content-Type": "application/octet-stream"},
		UploadProgress: func(sent, total int64) {}, // post to the UI, never block here
		AnyStatus:      true,                       // a 4xx's JSON body is ours to read
		Timeout:        120 * time.Second,
	})
	if err == nil && res.Status >= 400 {
		// answer holds {"message": ...}
	}
}
