//go:build !gnui_release

package app

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gonativeui/internal/inspectwire"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/script"
)

// The agent bridge (phase 6, T4; DECISIONS D47). With GNUI_INSPECT=<path> set
// in a development build, the app listens on a Unix socket at <path> and
// answers one JSON request per line: the windows, the semantic tree, steps
// played by role and name, and PNG renders of the current frame
// (internal/inspectwire has the protocol and the socket's checks).
//
// Nothing here runs unless the variable is set: no listener, no goroutine, no
// hook in the frame path. Requests arrive on goroutines of the bridge and
// never touch the app from there: each is queued and the main thread takes it
// at the next safe point (appObject.inspDrain, from safePoint), between frames,
// outside Build and outside every event. A "do" installs a Scripter on the
// window for its steps (the player of widget/script, as benchmarks and tests
// use), the window draws frames until the steps are applied and one frame
// more has been built, and only then is the request answered.

const (
	inspectWait     = 30 * time.Second  // how long a request waits for the main thread, and a "do" for its steps, by default
	inspectMaxDo    = 120 * time.Second // the longest a "do" may be given
	inspectTailFrms = 2                 // frames after the last step's before the answer: two built from its result, without input
)

// inspectGrace is how much longer than the steps' own limit the handler waits
// for their answer (a variable for the tests).
var inspectGrace = 2 * time.Second

func noInspect() {}

// inspectStart starts the bridge if GNUI_INSPECT is set, and returns the
// function that stops it (removes the socket). A failure is logged and the app
// runs without the bridge.
func (a *App) inspectStart() func() {
	path := os.Getenv("GNUI_INSPECT")
	if path == "" {
		return noInspect
	}
	in, err := a.startInspector(path)
	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			slog.Warn("inspect: GNUI_INSPECT is set, but the agent bridge is off on this system", "err", err)
		} else {
			slog.Error("inspect: the agent bridge is off", "err", err)
		}
		return noInspect
	}
	slog.Info("inspect: the agent bridge listens", "socket", in.ln.Path())
	return in.stop
}

// inspector is the bridge of one app.
type inspector struct {
	root *App
	ln   *inspectwire.Listener
	wake func() // makes the main thread run its safe point
	done chan struct{}
	quit chan struct{} // closed when the bridge stops

	mu       sync.Mutex
	q        []*inspJob
	stopped  bool
	stopOnce sync.Once

	// main thread only
	draining bool
	active   []*stepper
}

// startInspector listens on path and serves the requests; a's windows are
// the ones it shows. The caller (inspectStart; tests) stops it.
func (a *App) startInspector(path string) (*inspector, error) {
	ln, err := inspectwire.Listen(path)
	if err != nil {
		return nil, err
	}
	in := &inspector{root: a, ln: ln, done: make(chan struct{}), quit: make(chan struct{}),
		wake: platform.SignalDispatch}
	a.inspDrain = in.drain
	go func() { ln.Serve(in); close(in.done) }()
	return in, nil
}

// stop closes the socket and fails the requests waiting.
func (in *inspector) stop() {
	in.stopOnce.Do(in.stopNow)
}

func (in *inspector) stopNow() {
	in.mu.Lock()
	in.stopped = true
	in.mu.Unlock()
	close(in.quit)
	in.ln.Close()
	<-in.done
	in.root.inspDrain = nil
	in.mu.Lock()
	q := in.q
	in.q = nil
	in.mu.Unlock()
	for _, j := range q {
		j.closeShot()
	}
	for _, s := range append([]*stepper(nil), in.active...) {
		s.abort()
	}
}

// inspJob is a request on its way to the main thread.
type inspJob struct {
	req       *inspectwire.Request
	steps     []script.Step
	shot      *inspectwire.ShotDest // where a shot goes: the directory, held open since the request was checked
	wait      time.Duration
	done      chan *inspectwire.Response // buffered: the main thread never waits for a handler
	cancelled atomic.Bool                // the handler gave up: do not start it, stop it
}

// closeShot closes the shot's directory, if the job has one.
func (j *inspJob) closeShot() {
	if j.shot != nil {
		j.shot.Close()
	}
}

// Handle implements inspectwire.Handler: it checks what it can without the
// app, hands the request to the main thread and waits for the answer.
func (in *inspector) Handle(req *inspectwire.Request) *inspectwire.Response {
	job := &inspJob{req: req, wait: inspectWait, done: make(chan *inspectwire.Response, 1)}
	switch req.Op {
	case inspectwire.OpWindows:
	case inspectwire.OpTree:
		if _, err := treeFilter(req); err != nil {
			return inspectwire.Fail(req.ID, inspectwire.ErrBadRequest, err.Error())
		}
	case inspectwire.OpDo:
		steps, err := buildSteps(req)
		if err != nil {
			return inspectwire.Fail(req.ID, inspectwire.ErrBadRequest, err.Error())
		}
		job.steps = steps
		if req.TimeoutMs < 0 || time.Duration(req.TimeoutMs)*time.Millisecond > inspectMaxDo {
			return inspectwire.Fail(req.ID, inspectwire.ErrBadRequest, "timeout_ms is at most "+strconv.Itoa(int(inspectMaxDo/time.Millisecond)))
		}
		if req.TimeoutMs > 0 {
			job.wait = time.Duration(req.TimeoutMs) * time.Millisecond
		}
	case inspectwire.OpShot:
		d, err := inspectwire.OpenShotDest(in.ln.Path(), req.Path)
		if err != nil {
			return inspectwire.Fail(req.ID, inspectwire.ErrBadPath, "shot: "+err.Error())
		}
		job.shot = d
	default:
		return inspectwire.Fail(req.ID, inspectwire.ErrUnknownOp, fmt.Sprintf("unknown op %q; use windows, tree, do or shot", req.Op))
	}
	in.mu.Lock()
	if in.stopped {
		in.mu.Unlock()
		job.closeShot()
		return inspectwire.Fail(req.ID, inspectwire.ErrShuttingDown, "the app is ending")
	}
	in.q = append(in.q, job)
	in.mu.Unlock()
	in.wake()
	t := time.NewTimer(job.wait + inspectGrace)
	defer t.Stop()
	select {
	case r := <-job.done:
		return r
	case <-in.quit:
		job.cancelled.Store(true)
		return inspectwire.Fail(req.ID, inspectwire.ErrShuttingDown, "the app is ending")
	case <-t.C:
		job.cancelled.Store(true)
		return inspectwire.Fail(req.ID, inspectwire.ErrTimeout,
			"the app did not answer in "+job.wait.String()+"; a window that is hidden, covered or on a locked screen draws no frames, and a request waits for one")
	}
}

// drain runs the queued requests. Main thread, at a safe point.
func (in *inspector) drain() {
	if in.draining {
		return
	}
	in.draining = true
	defer func() { in.draining = false }()
	in.checkActive()
	in.mu.Lock()
	q := in.q
	in.q = nil
	in.mu.Unlock()
	for _, j := range q {
		if j.cancelled.Load() {
			j.closeShot()
			continue
		}
		if r := in.run(j); r != nil { // a "do" answers later, from its stepper
			j.done <- r
		}
	}
}

// run answers one request, or starts it: a "do" answers later, from its
// stepper, and run returns nil for it (the stepper replies on done).
func (in *inspector) run(j *inspJob) (resp *inspectwire.Response) {
	req := j.req
	defer func() {
		if p := recover(); p != nil {
			slog.Error("inspect: panic", "op", req.Op, "panic", p)
			resp = inspectwire.Fail(req.ID, inspectwire.ErrInternal, fmt.Sprint("panic: ", p))
		}
	}()
	switch req.Op {
	case inspectwire.OpWindows:
		return in.windowsResp()
	case inspectwire.OpTree:
		return in.treeResp(req)
	case inspectwire.OpShot:
		defer j.closeShot()
		return in.shotResp(req, j.shot)
	}
	return in.startDo(j)
}

// wins are the windows the bridge shows: the app's open ones, or, for an App
// that has none of the loop's (offscreen: tests), the App itself.
func (in *inspector) wins() []*App {
	if r := in.root; len(r.wins) == 0 && r.Win == nil && r.UI != nil {
		return []*App{r}
	}
	return in.root.wins
}

func winID(w *App) uint32 {
	if h := w.Window(); h != nil {
		return h.ID()
	}
	return 1
}

func (in *inspector) pick(id uint32) (*App, *inspectwire.Error) {
	ws := in.wins()
	for _, w := range ws {
		if id == 0 || winID(w) == id {
			return w, nil
		}
	}
	if len(ws) == 0 {
		return nil, &inspectwire.Error{Code: inspectwire.ErrNoWindow, Message: "the app has no window open"}
	}
	var ids []uint32
	for _, w := range ws {
		ids = append(ids, winID(w))
	}
	return nil, &inspectwire.Error{Code: inspectwire.ErrNoWindow, Message: fmt.Sprintf("no window %d; the windows are %v", id, ids)}
}

func windowInfo(w *App) inspectwire.Window {
	pw, ph := w.UI.Size()
	return inspectwire.Window{ID: winID(w), Title: w.cfg.Title, Width: pw, Height: ph, Scale: w.EffectiveScale(), Frame: int(w.UI.Frame())}
}

func (in *inspector) windowsResp() *inspectwire.Response {
	r := &inspectwire.Response{OK: true, Windows: []inspectwire.Window{}}
	for _, w := range in.wins() {
		r.Windows = append(r.Windows, windowInfo(w))
	}
	return r
}

// treeSel is the filter of a tree request.
type treeSel struct {
	role   ui.Role
	hasRol bool
	name   string
}

func treeFilter(req *inspectwire.Request) (f treeSel, err error) {
	f.name = req.Name
	if req.Role != "" {
		var ok bool
		if f.role, ok = script.ParseRole(req.Role); !ok {
			return f, fmt.Errorf("unknown role %q; use the names the tree prints (Button, TextField, ListItem …)", req.Role)
		}
		f.hasRol = true
	}
	return f, nil
}

var stateNames = [...]struct {
	bit  ui.SemState
	name string
}{
	{ui.SemChecked, "checked"}, {ui.SemMixed, "mixed"}, {ui.SemSelected, "selected"}, {ui.SemExpanded, "expanded"},
	{ui.SemDisabled, "disabled"}, {ui.SemFocused, "focused"}, {ui.SemEditable, "editable"}, {ui.SemModal, "modal"},
	{ui.SemSortAscending, "sort_ascending"}, {ui.SemSortDescending, "sort_descending"}, {ui.SemLive, "live"}, {ui.SemSecure, "secure"},
}

func (in *inspector) treeResp(req *inspectwire.Request) *inspectwire.Response {
	sel, _ := treeFilter(req)
	ws := in.wins()
	if req.Window != 0 {
		w, e := in.pick(req.Window)
		if e != nil {
			return &inspectwire.Response{Error: e}
		}
		ws = []*App{w}
	}
	if len(ws) == 0 {
		return &inspectwire.Response{Error: &inspectwire.Error{Code: inspectwire.ErrNoWindow, Message: "the app has no window open"}}
	}
	r := &inspectwire.Response{OK: true}
	for _, w := range ws {
		r.Trees = append(r.Trees, windowTree(w, sel))
	}
	return r
}

func windowTree(w *App, sel treeSel) inspectwire.Tree {
	var st ui.SemTree
	w.UI.Semantics(&st)
	wi := windowInfo(w)
	t := inspectwire.Tree{Window: wi.ID, Width: wi.Width, Height: wi.Height, Scale: wi.Scale, Dropped: st.Dropped, Nodes: []inspectwire.Node{}}
	for i := range st.Nodes {
		n := &st.Nodes[i]
		if (sel.hasRol && n.Role != sel.role) || (sel.name != "" && string(st.Text(n.Name)) != sel.name) {
			continue
		}
		node := inspectwire.Node{ID: i, Parent: int(n.Parent), Role: script.RoleName(n.Role), Name: string(st.Text(n.Name)),
			Value: string(st.Text(n.Value)), Bounds: n.Bounds, Key: strconv.FormatUint(uint64(n.Key), 16)}
		for _, s := range stateNames {
			if n.State&s.bit != 0 {
				node.States = append(node.States, s.name)
			}
		}
		if n.Range != [4]float64{} {
			rg := n.Range
			node.Range = &rg
		}
		if n.Pos[1] > 0 {
			p := n.Pos
			node.Pos = &p
		}
		t.Nodes = append(t.Nodes, node)
	}
	return t
}

// shotResp renders the window's last frame offscreen into a PNG at path.
func (in *inspector) shotResp(req *inspectwire.Request, dest *inspectwire.ShotDest) *inspectwire.Response {
	w, e := in.pick(req.Window)
	if e != nil {
		return &inspectwire.Response{Error: e}
	}
	pw, ph := w.UI.Size()
	if pw <= 0 || ph <= 0 || w.R == nil || w.Text == nil {
		return inspectwire.Fail(req.ID, inspectwire.ErrShotFailed, "the window has not drawn a frame yet")
	}
	if runtime.GOOS == "linux" && w.Win != nil {
		// The shared GL context is current on the thread of the frame, bound to
		// the window's surface; a render from here is not verified on it.
		return inspectwire.Fail(req.ID, inspectwire.ErrShotFailed, "a shot of a live window is not supported on Linux yet; render the app offscreen instead (its -o flag)")
	}
	img := image.NewRGBA(image.Rect(0, 0, pw, ph))
	w.R.RenderToRGBA(pw, ph, w.cfg.Clear, &w.list, img.Pix)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return inspectwire.Fail(req.ID, inspectwire.ErrShotFailed, err.Error())
	}
	if err := dest.Write(buf.Bytes()); err != nil {
		return inspectwire.Fail(req.ID, inspectwire.ErrShotFailed, err.Error())
	}
	return &inspectwire.Response{OK: true, Shot: &inspectwire.Shot{Path: dest.Path, Width: pw, Height: ph, Scale: w.EffectiveScale()}}
}

// buildSteps turns the steps of a "do" request into the player's.
func buildSteps(req *inspectwire.Request) ([]script.Step, error) {
	list := req.Steps
	if req.Step.Action != "" {
		if len(list) > 0 {
			return nil, errors.New("send one step (action …) or a list (steps […]), not both")
		}
		list = []inspectwire.Step{req.Step}
	}
	if len(list) == 0 {
		return nil, errors.New("a do needs an action or a list of steps")
	}
	if len(list) > 256 {
		return nil, errors.New("a do takes at most 256 steps")
	}
	out := make([]script.Step, len(list))
	for i := range list {
		s, err := buildStep(&list[i])
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i, err)
		}
		out[i] = s
	}
	return out, nil
}

func buildStep(s *inspectwire.Step) (script.Step, error) {
	target := func() (ui.Role, error) {
		if s.Role == "" {
			return 0, fmt.Errorf("%s needs a role (and usually a name): the ones the tree prints", s.Action)
		}
		r, ok := script.ParseRole(s.Role)
		if !ok {
			return 0, fmt.Errorf("unknown role %q; use the names the tree prints (Button, TextField, ListItem …)", s.Role)
		}
		if s.Nth < 0 {
			return 0, errors.New("nth is counted from 0")
		}
		return r, nil
	}
	pointer := func(mk func(ui.Role, string) script.Step) (script.Step, error) {
		r, err := target()
		if err != nil {
			return script.Step{}, err
		}
		return mk(r, s.Name).Nth(s.Nth), nil
	}
	switch s.Action {
	case inspectwire.ActClick:
		return pointer(script.Click)
	case inspectwire.ActDoubleClick:
		return pointer(script.DoubleClick)
	case inspectwire.ActRightClick:
		return pointer(script.RClick)
	case inspectwire.ActMove:
		return pointer(script.Move)
	case inspectwire.ActFocus:
		return pointer(script.Focus)
	case inspectwire.ActWaitFor:
		st, err := pointer(script.WaitFor)
		return withinMs(st, s.Ms), err
	case inspectwire.ActWaitGone:
		st, err := pointer(script.WaitGone)
		return withinMs(st, s.Ms), err
	case inspectwire.ActSettle:
		return withinMs(script.Settle(), s.Ms), nil
	case inspectwire.ActType:
		if s.Text == "" {
			return script.Step{}, errors.New("type needs text")
		}
		return script.Type(s.Text), nil
	case inspectwire.ActKey:
		code, mods, err := parseKey(s.Key)
		if err != nil {
			return script.Step{}, err
		}
		if s.N > 1 {
			return script.Keys(code, mods, s.N), nil
		}
		return script.Key(code, mods), nil
	case inspectwire.ActScroll:
		r, err := target()
		if err != nil {
			return script.Step{}, err
		}
		if s.Dy == 0 {
			return script.Step{}, errors.New("scroll needs dy (pixels; positive scrolls toward the start of the content)")
		}
		return script.Wheel(r, s.Name, s.Dy, max(s.N, 1)).Nth(s.Nth), nil
	case "":
		return script.Step{}, errors.New("a step needs an action")
	}
	return script.Step{}, fmt.Errorf("unknown action %q; use click, double_click, right_click, move, focus, type, key, scroll, wait_for, wait_gone or settle", s.Action)
}

func withinMs(s script.Step, ms int64) script.Step {
	if ms > 0 {
		return s.Within(ms)
	}
	return s
}

// ctxHost lets a player find its targets in a window's ui context.
type ctxHost struct{ c *ui.Context }

func (h ctxHost) Context() *ui.Context { return h.c }

// stepper is a "do" in progress on a window: the Scripter the window runs
// until its steps are applied and a frame built from their result has been
// seen. Main thread.
type stepper struct {
	in    *inspector
	w     *App
	job   *inspJob
	p     *script.Player
	n     int // steps
	start int // the player's first frame
	frame int // frames played
	tail  int // frames left after the steps are done
	end   int64
	begun bool

	focused, inside bool    // the window's input state before the steps, restored after
	mods            ui.Mods // the modifiers held before them
	finished        bool
}

func (in *inspector) startDo(j *inspJob) *inspectwire.Response {
	req := j.req
	w, e := in.pick(req.Window)
	if e != nil {
		return &inspectwire.Response{Error: e}
	}
	if w.script != nil || w.bench != nil {
		return inspectwire.Fail(req.ID, inspectwire.ErrBusy, "the window is playing another do, or a benchmark's script; wait for it")
	}
	s := &stepper{in: in, w: w, job: j, n: len(j.steps), tail: inspectTailFrms, end: platform.Now() + int64(j.wait)}
	s.p = script.NewPlayer(ctxHost{w.UI}, j.steps, script.Options{Synthetic: w.Win == nil}) // an offscreen window has no clock: the frame count is it
	w.script, w.scriptDone = s, false
	in.active = append(in.active, s)
	w.p.invalidate()
	if w.Win != nil {
		w.Win.RequestFrame()
	}
	return nil // the stepper answers
}

// Script implements Scripter.
func (s *stepper) Script(frame int, in *ui.Input) bool {
	if !s.begun {
		s.begun = true
		s.focused, s.inside, s.mods = in.Focused, in.Inside, in.Mods
	}
	switch {
	case s.job.cancelled.Load():
		s.finish(in, inspectwire.ErrTimeout, "the request was given up on")
	case platform.Now() > s.end:
		s.finish(in, inspectwire.ErrTimeout, fmt.Sprintf("the steps did not finish in %v (step %d of %d was running); a hidden or covered window draws no frames", s.job.wait, s.p.Index()+1, s.n))
	case s.p.Done() && s.p.Err() == nil:
		// The player is not called again: what its last step left held (a
		// modifier for the frame that follows it) is let go here.
		s.unwind(in)
		if s.tail--; s.tail < 0 {
			s.finish(in, "", "")
		}
	case s.p.Err() != nil || !s.p.Script(frame, in):
		if err := s.p.Err(); err != nil {
			s.finish(in, inspectwire.ErrStepFailed, strings.TrimPrefix(err.Error(), fmt.Sprintf("script: step %d: ", s.p.FailedStep())))
		}
	}
	s.frame++
	return true
}

// finish ends the do: the window goes back to its own input state, the stepper
// leaves it, and the request is answered. code "" is success.
func (s *stepper) finish(in *ui.Input, code, msg string) {
	if s.finished {
		return
	}
	s.finished = true
	if in != nil {
		s.unwind(in)
	}
	s.detach()
	r := &inspectwire.Response{ID: s.job.req.ID, OK: code == "", Steps: s.n, Frames: s.frame}
	if code != "" {
		e := &inspectwire.Error{Code: code, Message: msg}
		if code == inspectwire.ErrStepFailed {
			i := s.p.FailedStep()
			e.Step = &i
		}
		r.Error, r.Steps = e, 0
	}
	s.job.done <- r
}

// unwind gives the window's input back: a button the steps pressed and have not
// released is cancelled, the modifiers they set go back to what they were, and
// the pointer is no longer inside and the window no longer focused, unless it
// was before (a live window's focus is read from the system again). Input that
// came from elsewhere since is not undone (Player.Unwind).
func (s *stepper) unwind(in *ui.Input) {
	s.p.Unwind(in, s.mods)
	in.Inside = s.inside
	in.Focused = s.focused
	if s.w.Win != nil {
		in.Focused = s.w.Win.Focused()
	}
}

// abort takes the stepper off a window that goes on without it (the bridge
// stops): whatever the steps hold in the window's input is let go, outside a
// frame, and the request is answered.
func (s *stepper) abort() {
	if s.finished {
		return
	}
	s.finished = true
	if s.w.UI != nil {
		s.unwind(&s.w.tr.in)
		s.w.p.invalidate()
	}
	s.detach()
	s.job.done <- inspectwire.Fail(s.job.req.ID, inspectwire.ErrShuttingDown, "the app is ending")
}

// detach takes the stepper off the window.
func (s *stepper) detach() {
	if s.w.script == Scripter(s) {
		s.w.script, s.w.scriptDone = nil, false
	}
	for i, a := range s.in.active {
		if a == s {
			s.in.active = append(s.in.active[:i], s.in.active[i+1:]...)
			break
		}
	}
}

// checkActive fails the steppers whose window has closed: nothing would ever
// call them again.
func (in *inspector) checkActive() {
	open := in.wins()
	for _, s := range append([]*stepper(nil), in.active...) {
		alive := false
		for _, w := range open {
			alive = alive || w == s.w
		}
		if !alive || s.w.UI == nil {
			s.finish(nil, inspectwire.ErrNoWindow, "the window closed before the steps were done")
		}
	}
}
