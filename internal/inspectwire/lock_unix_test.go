//go:build !windows

package inspectwire_test

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gonativeui/internal/inspectwire"
)

// Of several bridges starting on one path exactly one gets it; the others are
// told it is in use and touch nothing.
func TestConcurrentListensOnOnePathHaveOneWinner(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s")
	const n = 12
	var wg sync.WaitGroup
	var mu sync.Mutex
	var won []*inspectwire.Listener
	var errs []error
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			l, err := inspectwire.Listen(sock)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			} else {
				won = append(won, l)
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(won) != 1 || len(errs) != n-1 {
		t.Fatalf("%d winners, %d refused (errors %v)", len(won), len(errs), errs)
	}
	for _, err := range errs {
		if !strings.Contains(err.Error(), "in use") {
			t.Errorf("the loser was told: %v", err)
		}
	}
	go won[0].Serve(&echo{})
	defer won[0].Close()
	if c, err := inspectwire.Dial(sock, time.Second); err != nil {
		t.Errorf("the winner does not answer: %v", err)
	} else {
		c.Close()
	}
}

// A bridge whose accept queue is full refuses connections, on macOS like a dead
// one does (ECONNREFUSED): that must not make a second app take its socket.
func TestABridgeWithAFullAcceptQueueIsNotTakenOver(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s")
	ln, err := inspectwire.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() // never served: nothing accepts, the queue fills
	var conns []net.Conn
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	full := false
	for i := 0; i < 1500 && !full; i++ {
		c, err := net.DialTimeout("unix", sock, 50*time.Millisecond)
		if err != nil {
			full = true
			break
		}
		conns = append(conns, c)
	}
	if !full {
		t.Skipf("the accept queue did not fill after %d connections", len(conns))
	}
	t.Logf("a connection was refused after %d", len(conns))
	l2, err := inspectwire.Listen(sock)
	if err == nil {
		l2.Close()
		t.Fatal("a second bridge took over the socket of a live one whose queue was full")
	}
	if !strings.Contains(err.Error(), "in use") {
		t.Errorf("the refusal: %v", err)
	}
	if fi, err := os.Lstat(sock); err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("the live socket is gone: %v", err)
	}
	// and it is the first one's: once the queue drains it answers
	go ln.Serve(&echo{})
	for _, c := range conns {
		c.Close()
	}
	conns = nil
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := inspectwire.Dial(sock, time.Second)
		if err == nil {
			c.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first bridge never answered again: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The process the next test starts and kills: it listens, says so and waits.
func TestHelperBridgeProcess(t *testing.T) {
	sock := os.Getenv("GNUI_TEST_HELPER_SOCKET")
	if sock == "" {
		t.Skip("run by TestSocketOfADeadOwnerIsReclaimed")
	}
	ln, err := inspectwire.Listen(sock)
	if err != nil {
		os.Stdout.WriteString("error " + err.Error() + "\n")
		os.Exit(1)
	}
	go ln.Serve(&echo{})
	os.Stdout.WriteString("ready\n")
	select {}
}

// What a killed bridge leaves is replaced: the system released its lock when it
// died. While it lived, it was not touched.
func TestSocketOfADeadOwnerIsReclaimed(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperBridgeProcess$")
	cmd.Env = append(os.Environ(), "GNUI_TEST_HELPER_SOCKET="+sock)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	line, _ := bufio.NewReader(out).ReadString('\n')
	if strings.TrimSpace(line) != "ready" {
		t.Fatalf("the child said %q", line)
	}
	if l, err := inspectwire.Listen(sock); err == nil {
		l.Close()
		t.Fatal("a bridge took over the socket of a live process")
	} else if !strings.Contains(err.Error(), "in use") {
		t.Errorf("the refusal: %v", err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	if fi, err := os.Lstat(sock); err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("the test needs the dead process's socket to remain: %v", err)
	}
	ln, err := inspectwire.Listen(sock)
	if err != nil {
		t.Fatalf("the dead owner's socket was not reclaimed: %v", err)
	}
	go ln.Serve(&echo{})
	defer ln.Close()
	if c, err := inspectwire.Dial(sock, time.Second); err != nil {
		t.Errorf("the new bridge does not answer: %v", err)
	} else {
		c.Close()
	}
}

// A bridge waiting to take over the path (it is refused while the first lives)
// publishes its socket only after the first has gone, and the first's Close,
// which removes its socket before it lets go of the lock, never removes the
// successor's.
func TestCloseNeverRemovesASuccessorsSocket(t *testing.T) {
	for i := 0; i < 30; i++ {
		sock := filepath.Join(shortDir(t), "s")
		first, err := inspectwire.Listen(sock)
		if err != nil {
			t.Fatal(err)
		}
		next := make(chan *inspectwire.Listener, 1)
		go func() {
			for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
				if l, err := inspectwire.Listen(sock); err == nil {
					next <- l
					return
				}
				time.Sleep(time.Millisecond)
			}
			next <- nil
		}()
		time.Sleep(3 * time.Millisecond)
		first.Close()
		l := <-next
		if l == nil {
			t.Fatal("the successor never got the socket")
		}
		first.Close() // again: a second Close removes nothing of the successor's
		time.Sleep(5 * time.Millisecond)
		fi, err := os.Lstat(sock)
		if err != nil || fi.Mode()&os.ModeSocket == 0 {
			t.Fatalf("round %d: the successor's socket is gone: %v", i, err)
		}
		go l.Serve(&echo{})
		if c, err := inspectwire.Dial(sock, time.Second); err != nil {
			t.Fatalf("round %d: the successor does not answer: %v", i, err)
		} else {
			c.Close()
		}
		l.Close()
	}
}
