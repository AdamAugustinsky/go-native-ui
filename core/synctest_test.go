package core

import (
	"testing"
	"testing/synctest"
	"time"
)

// In a bubble, Now moves with the bubble's clock and a parked goroutine counts
// as blocked, so the clock jumps past it: an hour passes at once.
func TestSynctestClockAndParker(t *testing.T) {
	UseSynctestForTesting(t)
	synctest.Test(t, func(t *testing.T) {
		p := NewParker()
		defer p.Close()
		woke := make(chan int64)
		go func() {
			p.Park()
			woke <- Now()
		}()
		start := Now()
		time.Sleep(time.Hour) // the parked goroutine must not hold the clock back
		synctest.Wait()
		select {
		case <-woke:
			t.Fatal("Park returned without an Unpark")
		default:
		}
		p.Unpark()
		if at := <-woke; at-start != int64(time.Hour) {
			t.Fatalf("woke %v after the start, want 1h", time.Duration(at-start))
		}
	})
}
