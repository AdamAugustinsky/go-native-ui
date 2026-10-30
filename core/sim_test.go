package core

import (
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestSimClockTimers(t *testing.T) {
	c, err := InstallSimClockForTesting()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Restore()
	start := Now()
	var got []int
	a := AfterFunc(time.Second, func() { got = append(got, 1); AfterFunc(0, func() { got = append(got, 4) }) })
	AfterFunc(time.Second, func() { got = append(got, 2) })
	stopped := AfterFunc(time.Second, func() { t.Error("stopped timer fired") })
	if !stopped.Stop() || stopped.Stop() {
		t.Fatal("Stop did not report activity")
	}
	AfterFunc(2*time.Second, func() { got = append(got, 3) })
	if _, err := c.AdvanceTo(start + int64(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int{1, 2, 4, 3}) {
		t.Fatal(got)
	}
	if a.Reset(time.Second) {
		t.Fatal("expired Reset reported active")
	}
	if _, err := c.AdvanceTo(Now() + int64(time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatal(got)
	}
	if _, err := c.AdvanceTo(Now() - 1); err == nil {
		t.Fatal("clock reversed")
	}
	if _, err := InstallSimClockForTesting(); err == nil {
		t.Fatal("nested simulation installed")
	}
}

func TestSimWorkersParkBetweenDecisions(t *testing.T) {
	c, err := InstallSimClockForTesting()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Restore()
	p := NewParker()
	p.BindSimWorkerForTesting()
	defer p.Close()
	var count atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		if !p.StartSimWorker() {
			return
		}
		for {
			count.Add(1)
			if !p.Park() {
				return
			}
		}
	}()
	if count.Load() != 0 {
		t.Fatal("worker ran before barrier")
	}
	if err := c.Quiesce(time.Second); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 1 {
		t.Fatal(count.Load())
	}
	p.Unpark()
	p.Unpark()
	if count.Load() != 1 {
		t.Fatal("wake ran worker during a frame")
	}
	if err := c.Quiesce(time.Second); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 2 {
		t.Fatal(count.Load())
	}
	p.Close()
	<-done
}

func TestSimWorkerTimeoutIsReal(t *testing.T) {
	c, err := InstallSimClockForTesting()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Restore()
	p := NewParker()
	p.BindSimWorkerForTesting()
	defer p.Close()
	release, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		if !p.StartSimWorker() {
			return
		}
		<-release
		p.Park()
	}()
	start := Now()
	if err := c.Quiesce(10 * time.Millisecond); err == nil {
		t.Fatal("stuck worker accepted as idle")
	}
	if Now() != start {
		t.Fatal("watchdog advanced virtual time")
	}
	close(release)
	p.Close()
	<-done
}

func TestSimTimerSurvivesClockRestoration(t *testing.T) {
	c, err := InstallSimClockForTesting()
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	timer := AfterFunc(time.Hour, func() { calls++ })
	c.Restore()
	c, err = InstallSimClockForTesting()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Restore()
	timer.Reset(time.Second)
	if _, err = c.AdvanceTo(Now() + int64(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
