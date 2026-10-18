//go:build linux

package platform

import (
	"strings"
	"testing"
	"time"
)

// The scripted compositor reports from several goroutines (its request loop,
// its command loop, the goroutines that read what the client sends). Its
// output must stay whole lines and no acknowledgement may be lost: an
// unsynchronized writer once garbled lines and swallowed "OK" lines, which
// failed tests by timeout, now and then.
func TestFakeCompositorOutputIsNotGarbled(t *testing.T) {
	f := startFakeWL(t, "")
	openFake(t, f) // the command loop starts when a client connects
	const spam, acks = 4000, 300
	f.do("spam %d", spam)
	for i := 0; i < acks; i++ { // each acknowledgement races with the spam
		f.do("quiet")
	}
	last := "SPAM " + "3999 " + strings.Repeat("x", 40)
	f.wait(last, 20*time.Second)
	f.mu.Lock()
	defer f.mu.Unlock()
	spams := 1 // the one just taken
	for _, l := range f.log {
		switch {
		case strings.HasPrefix(l, "SPAM "):
			spams++
			if f := strings.Fields(l); len(f) != 3 || f[2] != strings.Repeat("x", 40) {
				t.Fatalf("garbled line %q", l)
			}
		case l == "OK quiet", l == "OK spam", l == "READY", strings.HasPrefix(l, "REQ "):
			// what opening the window said, whole
		default:
			t.Fatalf("garbled line %q", l)
		}
	}
	if spams != spam {
		t.Errorf("%d of %d spam lines arrived whole", spams, spam)
	}
}
