package capture

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gonativeui/core"
	"gonativeui/platform"
)

// A session is a recording made of segments (macOS): the state machine
// between the system's callbacks and the Recording. It runs on the main
// thread; the system's handlers only post messages to its inbox and wake
// the main thread (D36), which pumps it. It knows the OS only through
// native, so it is tested everywhere with a fake.

// native is the OS's half of a session. Every method but join and duration
// is called on the main thread.
type native interface {
	// choose shows the picker, or looks the target up; mChosen, mCancelled
	// or mFailed follows.
	choose() error
	// chosen takes the choice after mChosen (ErrTargetGone: Start's target
	// is not there).
	chosen() error
	// unchoose ends the choosing without a choice (Stop in the picker).
	unchoose()
	// start makes the stream with a first output writing path (segment 0)
	// and starts it; mStarted follows.
	start(path string) error
	// attach adds an output writing segment seg to path (Resume).
	attach(seg int, path string) error
	// detach removes the current output (Pause); its segment's
	// mSegFinished follows.
	detach() error
	// stopCapture stops the stream; mCaptureStopped follows, and each
	// writing segment's mSegFinished.
	stopCapture()
	// errorOf maps and releases the system error a message carries.
	errorOf(obj uintptr) error
	// drop releases a message's system error unread. Any thread.
	drop(obj uintptr)
	// release drops every native object (idempotent).
	release()
	// join writes the segments at paths, in order, to out without
	// re-encoding and returns its duration. A worker goroutine.
	join(paths []string, out string) (time.Duration, error)
	// duration reads a file's duration (0 when unreadable). A worker.
	duration(path string) time.Duration
}

type msgKind uint8

const (
	mChosen         msgKind = iota + 1 // the picker's choice, or Start's target is known
	mCancelled                         // the picker was closed without a choice
	mFailed                            // choosing failed
	mStarted                           // startCapture finished (err: it failed)
	mSegFinished                       // segment seg's file is complete
	mSegFailed                         // segment seg failed
	mStreamStopped                     // the system stopped the stream (errStoppedByUser: from its menu)
	mCaptureStopped                    // stopCapture finished
	mDelayed                           // Options.Delay is over
	mTimeout                           // the outputs did not finish in time after the stop
	mJoined                            // the worker finished the file (session.joined)
)

// msg is one message to a session. obj is a retained system error
// (native.errorOf maps it on the main thread); err is one already mapped.
type msg struct {
	kind msgKind
	seg  int32
	obj  uintptr
	err  error
}

type phase uint8

const (
	phChoosing  phase = iota + 1 // the picker is up, or the target being looked up
	phWaiting                    // Options.Delay
	phStarting                   // startCapture sent
	phRecording                  // an output is attached
	phPaused                     // no output attached
	phStopping                   // stopCapture sent; waiting for it and the outputs
	phFinishing                  // the worker joins or moves the file
	phEnded
)

type segState uint8

const (
	segWriting segState = iota // attached, or detached and not finished yet
	segDone
	segFailed
)

type segment struct {
	path  string
	state segState
}

// errStoppedByUser is the system's own "stop sharing" (the menu bar's
// control): a stop, not a failure.
var errStoppedByUser = errors.New("capture: stopped from the system's menu")

// stopTimeout bounds the wait for the outputs to finish after a stop.
var stopTimeout = 15 * time.Second

const inboxSize = 64

type session struct {
	r    *Recording
	n    native
	out  string // the final MP4
	wake func() // has the main thread pump (any thread)
	done func() // after Ended (main thread)

	mu      sync.Mutex
	inbox   [inboxSize]msg
	nIn     int
	dropped uint64
	over    bool // ended: later messages are dropped
	joined  Result

	ph             phase
	segs           []segment
	discard        bool
	stopAsked      bool // Stop while starting
	captureStopped bool
	streamGone     bool
	err            error // the first failure
	timer          *core.Timer
	tk             [3]platform.Ticket // Started, Ended, Chosen (EventKind-1)
}

var tickets = sync.OnceValue(func() *platform.Tickets {
	return platform.NewTickets(platform.SourceCapture, 32, 0)
})

func newSession(r *Recording, n native, out string, wake func()) *session {
	return &session{r: r, n: n, out: out, wake: wake}
}

// reserve takes the records of the recording's three events, so their
// delivery never needs room (D37, class 2).
func (s *session) reserve(win uint32) error {
	for i := range s.tk {
		tk, err := tickets().Reserve(win)
		if err != nil {
			s.unreserve()
			return ErrBusy
		}
		s.tk[i] = tk
	}
	return nil
}

func (s *session) unreserve() {
	for i, tk := range s.tk {
		if tk != 0 {
			tickets().Cancel(tk)
			s.tk[i] = 0
		}
	}
}

func (s *session) deliver(k EventKind) {
	if tk := s.tk[k-1]; tk != 0 {
		s.tk[k-1] = 0
		tickets().Complete(tk, int64(k), int64(s.r.id), nil)
	}
}

// begin starts choosing (Pick, Start).
func (s *session) begin() error {
	if err := s.n.choose(); err != nil {
		return err
	}
	s.ph = phChoosing
	s.r.be = s
	return nil
}

// post queues m and wakes the main thread. Any thread; it allocates
// nothing. A message to an ended session, or one past a full inbox, is
// dropped, and its system error released.
func (s *session) post(m msg) {
	s.mu.Lock()
	ok := !s.over && s.nIn < inboxSize
	if ok {
		s.inbox[s.nIn] = m
		s.nIn++
	} else {
		s.dropped++
	}
	s.mu.Unlock()
	if !ok {
		if m.obj != 0 {
			s.n.drop(m.obj)
		}
		return
	}
	s.wake()
}

// pump handles the queued messages (main thread).
func (s *session) pump() {
	for {
		s.mu.Lock()
		if s.nIn == 0 {
			s.mu.Unlock()
			return
		}
		m := s.inbox[0]
		copy(s.inbox[:], s.inbox[1:s.nIn])
		s.nIn--
		s.inbox[s.nIn] = msg{}
		s.mu.Unlock()
		s.handle(m)
	}
}

func (s *session) handle(m msg) {
	if m.obj != 0 {
		m.err = s.n.errorOf(m.obj)
	}
	if s.ph == phEnded {
		return
	}
	switch m.kind {
	case mChosen:
		if s.ph != phChoosing {
			return
		}
		if err := s.n.chosen(); err != nil {
			s.end(Result{Err: err})
			return
		}
		s.deliver(Chosen)
		if d := s.r.opt.Delay; d > 0 {
			s.ph = phWaiting
			s.timer = core.AfterFunc(d, func() { s.post(msg{kind: mDelayed}) })
			return
		}
		s.startCapture()
	case mDelayed:
		if s.ph == phWaiting {
			s.startCapture()
		}
	case mCancelled:
		if s.ph == phChoosing {
			s.n.unchoose()
			s.end(Result{Err: ErrPickerCancelled})
		}
	case mFailed:
		if s.ph == phChoosing {
			s.n.unchoose()
			s.end(Result{Err: orUnknown(m.err)})
		}
	case mStarted:
		if s.ph != phStarting {
			return
		}
		if m.err != nil {
			s.removeSegments()
			s.end(Result{Err: m.err})
			return
		}
		s.ph = phRecording
		s.r.clock.start()
		s.deliver(Started)
		if s.stopAsked {
			s.stopNow()
		}
	case mSegFinished:
		if i := int(m.seg); i < len(s.segs) && s.segs[i].state == segWriting {
			s.segs[i].state = segDone
		}
		s.maybeFinish()
	case mSegFailed:
		if i := int(m.seg); i < len(s.segs) {
			s.segs[i].state = segFailed
		}
		s.fail(orUnknown(m.err))
		s.maybeFinish()
	case mStreamStopped:
		s.streamGone = true
		if errors.Is(m.err, errStoppedByUser) {
			m.err = nil
		}
		switch s.ph {
		case phStarting:
			s.removeSegments()
			s.end(Result{Err: orUnknown(m.err)})
		case phRecording, phPaused:
			if m.err != nil {
				s.fail(m.err)
			} else {
				s.stopNow()
			}
		case phStopping:
			s.captureStopped = true
			s.maybeFinish()
		}
	case mCaptureStopped:
		s.captureStopped = true
		s.maybeFinish()
	case mTimeout:
		if s.ph != phStopping {
			return
		}
		s.captureStopped = true
		for i := range s.segs {
			if s.segs[i].state == segWriting {
				s.segs[i].state = segFailed // tried in the join; skipped if unreadable
			}
		}
		s.finish()
	case mJoined:
		if s.ph != phFinishing {
			return
		}
		s.mu.Lock()
		res := s.joined
		s.mu.Unlock()
		if s.err != nil {
			res.Err = s.err // the failure that stopped it comes first
		}
		s.end(res)
	}
}

func orUnknown(err error) error {
	if err == nil {
		return errors.New("capture: the system reported a failure without an error")
	}
	return err
}

// segPath is segment i's file: next to the final file, so moving the only
// segment there is a rename.
func (s *session) segPath(i int) string {
	base := strings.TrimSuffix(s.out, filepath.Ext(s.out))
	return fmt.Sprintf("%s.part%d.mp4", base, i)
}

func (s *session) startCapture() {
	path := s.segPath(0)
	os.Remove(path)
	s.segs = append(s.segs[:0], segment{path: path})
	if err := s.n.start(path); err != nil {
		s.removeSegments()
		s.end(Result{Err: err})
		return
	}
	s.ph = phStarting
}

func (s *session) pause() error {
	switch s.ph {
	case phPaused:
		return nil
	case phRecording:
	default:
		return ErrNotRecording
	}
	if err := s.n.detach(); err != nil {
		return err
	}
	s.r.clock.stop()
	s.r.paused.Store(true)
	s.ph = phPaused
	return nil
}

func (s *session) resume() error {
	switch s.ph {
	case phRecording:
		return nil
	case phPaused:
	default:
		return ErrNotRecording
	}
	i := len(s.segs)
	path := s.segPath(i)
	os.Remove(path)
	if err := s.n.attach(i, path); err != nil {
		return err
	}
	s.segs = append(s.segs, segment{path: path})
	s.r.clock.start()
	s.r.paused.Store(false)
	s.ph = phRecording
	return nil
}

func (s *session) stop(discard bool) {
	switch s.ph {
	case phChoosing:
		s.n.unchoose()
		s.end(Result{Discarded: true})
	case phWaiting:
		s.end(Result{Discarded: true})
	case phStarting:
		s.stopAsked, s.discard = true, discard
	case phRecording, phPaused:
		s.discard = discard
		s.stopNow()
	}
}

// fail records the first failure and stops the recording, keeping what it
// wrote.
func (s *session) fail(err error) {
	if s.err == nil {
		s.err = err
	}
	if s.ph == phRecording || s.ph == phPaused {
		s.stopNow()
	}
}

func (s *session) stopNow() {
	s.ph = phStopping
	s.r.clock.stop()
	s.r.paused.Store(false)
	if s.streamGone {
		s.captureStopped = true
	} else {
		s.n.stopCapture()
	}
	s.timer = core.AfterFunc(stopTimeout, func() { s.post(msg{kind: mTimeout}) })
	s.maybeFinish()
}

func (s *session) maybeFinish() {
	if s.ph != phStopping || !s.captureStopped {
		return
	}
	for _, g := range s.segs {
		if g.state == segWriting {
			return
		}
	}
	s.finish()
}

// finish hands the segments to a worker, which moves or joins them into
// the final file; mJoined follows.
func (s *session) finish() {
	if s.timer != nil {
		s.timer.Stop()
	}
	s.ph = phFinishing
	s.n.release()
	var paths []string
	for _, g := range s.segs {
		if _, err := os.Stat(g.path); err == nil && (g.state == segDone || g.state == segFailed) {
			paths = append(paths, g.path)
		}
	}
	if s.discard || len(paths) == 0 {
		s.removeSegments()
		s.end(Result{Discarded: s.discard, Err: s.err})
		return
	}
	clock := s.r.clock.read()
	go func() {
		res := s.finishFile(paths, clock)
		s.mu.Lock()
		s.joined = res
		s.mu.Unlock()
		s.post(msg{kind: mJoined})
	}()
}

// finishFile makes the final file from the segments (a worker).
func (s *session) finishFile(paths []string, clock time.Duration) Result {
	res := Result{Path: s.out}
	os.Remove(s.out)
	if len(paths) == 1 {
		if err := os.Rename(paths[0], s.out); err != nil {
			res.Path = paths[0]
		}
		res.Duration = s.n.duration(res.Path)
	} else if d, err := s.n.join(paths, s.out); err != nil {
		os.Remove(s.out)
		res.Path, res.Segments = "", paths
		res.Err = fmt.Errorf("capture: joining the segments (kept in %s): %w", filepath.Dir(paths[0]), err)
	} else {
		res.Duration = d
		for _, p := range paths {
			os.Remove(p)
		}
	}
	if res.Duration <= 0 {
		res.Duration = clock
	}
	return res
}

func (s *session) removeSegments() {
	for _, g := range s.segs {
		os.Remove(g.path)
	}
}

// end delivers the result: Ended, exactly once.
func (s *session) end(res Result) {
	if s.timer != nil {
		s.timer.Stop()
	}
	s.n.release()
	s.ph = phEnded
	s.mu.Lock()
	s.over = true
	for i := 0; i < s.nIn; i++ {
		if o := s.inbox[i].obj; o != 0 {
			s.n.drop(o)
		}
		s.inbox[i] = msg{}
	}
	s.nIn = 0
	s.mu.Unlock()
	r := s.r
	r.clock.stop()
	r.paused.Store(false)
	r.result, r.ended = res, true
	for _, k := range [...]EventKind{Chosen, Started} {
		if tk := s.tk[k-1]; tk != 0 {
			tickets().Cancel(tk)
			s.tk[k-1] = 0
		}
	}
	s.deliver(Ended)
	if s.done != nil {
		s.done()
	}
}
