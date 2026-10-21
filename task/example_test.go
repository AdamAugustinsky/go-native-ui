package task_test

import (
	"fmt"
	"time"

	"gonativeui/task"
)

// resp is the pool's output: plain data, no pointers.
type resp struct {
	Sum  int
	Done bool // false when the job saw its Token cancelled
}

// Summing on a worker and collecting the answer on the owner's goroutine. In
// an app the owner is the UI thread: it Submits from an event handler and
// Polls once per frame, and Options.Wake (platform.Wake) wakes the loop when
// an answer is waiting.
func Example() {
	p := task.NewPool(task.Options{Workers: 2}, func(t task.Token, n int) resp {
		sum := 0
		for i := 1; i <= n; i++ {
			if t.Cancelled() { // long work polls its Token and gives up early
				return resp{}
			}
			sum += i
		}
		return resp{Sum: sum, Done: true}
	})
	defer p.Close(time.Second)

	if _, err := p.Submit(100, 0); err != nil { // task.ErrFull when saturated
		fmt.Println(err)
		return
	}
	var results []task.Result[resp]
	for deadline := time.Now().Add(5 * time.Second); len(results) == 0 && time.Now().Before(deadline); {
		results = p.Poll(results[:0]) // each frame: the answers that arrived
		time.Sleep(time.Millisecond)
	}
	for _, r := range results {
		fmt.Println(r.Out.Sum, r.Out.Done)
	}
	// Output: 5050 true
}

// A search box: each keystroke submits under one key, so a newer query
// replaces the older one that has not finished (latest wins).
func ExamplePool_Submit() {
	p := task.NewPool(task.Options{}, func(t task.Token, q int) resp {
		return resp{Sum: q * q, Done: true}
	})
	defer p.Close(time.Second)

	const searchKey = 1
	p.Submit(1, searchKey)
	p.Submit(2, searchKey) // supersedes the first if it has not been polled
	p.Submit(3, searchKey)

	var results []task.Result[resp]
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		results = p.Poll(results[:0])
		if len(results) > 0 {
			break
		}
	}
	// the answer to the newest query is always delivered
	fmt.Println(len(results) > 0)
	// Output: true
}

// Declaring work from Build: Keep starts it on the first frame that declares
// it, does nothing on the later ones, and Sweep cancels what a frame stopped
// declaring.
func ExamplePool_Keep() {
	p := task.NewPool(task.Options{}, func(t task.Token, n int) resp { return resp{Sum: n, Done: true} })
	defer p.Close(time.Second)

	const loadKey = 7
	for frame := 0; frame < 3; frame++ {
		// Build:
		if _, err := p.Keep(loadKey, 42); err != nil { // ErrFull: try again next frame
			fmt.Println(err)
		}
		p.Sweep() // after Build
	}
	var results []task.Result[resp]
	for deadline := time.Now().Add(5 * time.Second); len(results) == 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		results = p.Poll(results[:0])
	}
	fmt.Println(len(results), results[0].Out.Sum) // one declaration, one result
	// Output: 1 42
}
