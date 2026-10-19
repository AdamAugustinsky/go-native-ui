package log

import (
	"io"
	"sync"
	"time"
)

// asyncWriter takes a log line from any goroutine without touching the disk or
// the terminal: Write copies it into a bounded buffer, and one goroutine writes
// the buffer to the sinks. A caller (the UI thread, say) never waits for I/O,
// and a sink that stalls cannot stall the program; when the buffer is full,
// lines are dropped and counted, and the writer says so once it catches up.
type asyncWriter struct {
	sinks  []io.Writer
	closer io.Closer // closed by close (the log file; never os.Stderr)
	limit  int
	json   bool

	mu         sync.Mutex
	cond       *sync.Cond // signalled when written advances
	buf, spare []byte
	dropped    int
	enq        uint64 // lines accepted
	written    uint64 // lines accepted up to which the sinks have them
	quit       bool
	kick       chan struct{}
	exited     chan struct{}
}

func newAsyncWriter(limit int, json bool, closer io.Closer, sinks ...io.Writer) *asyncWriter {
	a := &asyncWriter{sinks: sinks, closer: closer, limit: limit, json: json,
		kick: make(chan struct{}, 1), exited: make(chan struct{})}
	a.cond = sync.NewCond(&a.mu)
	n := min(limit, 64<<10)
	a.buf, a.spare = make([]byte, 0, n), make([]byte, 0, n)
	go a.run()
	return a
}

// Write queues p (a copy of it). It never blocks on I/O and always reports
// success, so a logger never turns a full buffer into an error.
func (a *asyncWriter) Write(p []byte) (int, error) {
	a.mu.Lock()
	if a.quit {
		a.mu.Unlock()
		return len(p), nil
	}
	if len(a.buf)+len(p) > a.limit {
		a.dropped++
		a.mu.Unlock()
		select {
		case a.kick <- struct{}{}:
		default:
		}
		return len(p), nil
	}
	a.buf = append(a.buf, p...)
	a.enq++
	a.mu.Unlock()
	select {
	case a.kick <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (a *asyncWriter) run() {
	defer close(a.exited)
	for {
		a.mu.Lock()
		for len(a.buf) == 0 && a.dropped == 0 {
			if a.quit {
				a.cond.Broadcast()
				a.mu.Unlock()
				return
			}
			a.mu.Unlock()
			<-a.kick
			a.mu.Lock()
		}
		out, seq, drops := a.buf, a.enq, a.dropped
		a.buf, a.dropped = a.spare[:0], 0
		a.mu.Unlock()

		if drops > 0 {
			a.writeSinks(a.dropNotice(drops))
		}
		a.writeSinks(out)

		a.mu.Lock()
		a.spare = out[:0]
		a.written = seq
		a.cond.Broadcast()
		a.mu.Unlock()
	}
}

func (a *asyncWriter) writeSinks(p []byte) {
	if len(p) == 0 {
		return
	}
	for _, s := range a.sinks {
		s.Write(p) // a failing sink must not stop the others
	}
}

func (a *asyncWriter) dropNotice(n int) []byte {
	ts := time.Now().Format(time.RFC3339Nano)
	if a.json {
		return []byte(`{"time":"` + ts + `","level":"WARN","msg":"log lines dropped: the log buffer was full","count":` + itoa(n) + "}\n")
	}
	return []byte("time=" + ts + ` level=WARN msg="log lines dropped: the log buffer was full" count=` + itoa(n) + "\n")
}

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			return string(b[i:])
		}
	}
}

// flush blocks until every line accepted so far has been handed to the sinks.
func (a *asyncWriter) flush() {
	a.mu.Lock()
	target := a.enq
	for a.written < target {
		select {
		case <-a.exited:
			a.mu.Unlock()
			return
		default:
		}
		a.cond.Wait()
	}
	a.mu.Unlock()
}

// close flushes, stops the goroutine and closes the sinks that can be closed.
func (a *asyncWriter) close() {
	a.mu.Lock()
	if a.quit {
		a.mu.Unlock()
		<-a.exited
		return
	}
	a.quit = true
	a.mu.Unlock()
	select {
	case a.kick <- struct{}{}:
	default:
	}
	<-a.exited
	if a.closer != nil {
		a.closer.Close()
	}
}
