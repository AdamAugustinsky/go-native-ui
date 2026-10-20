package pilot

import "sync"

// guard orders a worker's last act against Close. A worker may be inside a
// call that cannot be interrupted (a read of the system's preferences, a stat
// of a folder that does not answer), so Close does not wait for it: it marks
// the guard closed and the worker, when the call returns, finds it so and
// publishes nothing and wakes nobody.
type guard struct {
	mu     sync.Mutex
	closed bool
}

// close ends publishing. It does not wait for a publish in progress longer
// than that publish takes (publish does no more than store and wake).
func (g *guard) close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

// stopped reports whether close was called: a worker checks it between steps.
func (g *guard) stopped() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed
}

// publish runs f unless closed, and reports whether it did.
func (g *guard) publish(f func()) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	f()
	return true
}
