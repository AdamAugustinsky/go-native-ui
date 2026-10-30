//go:build darwin || linux

package main

import "sync"

// termSink is the seam between a session's output and the terminal: the
// session's goroutine Writes, which queues the bytes and wakes the window,
// and the UI thread's take feeds them to TerminalState.Feed. It never
// blocks, so its queue is as big as the program's output between two
// frames.
type termSink struct {
	mu   sync.Mutex
	in   []byte
	wake func() // nil offscreen
}

func (w *termSink) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.in = append(w.in, p...)
	w.mu.Unlock()
	if w.wake != nil {
		w.wake()
	}
	return len(p), nil
}

// take passes what arrived since the last frame to feed, on the UI thread.
func (w *termSink) take(feed func([]byte)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.in) > 0 {
		feed(w.in)
		w.in = w.in[:0]
	}
}
