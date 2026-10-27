//go:build darwin

package platform

import (
	"errors"
	"sync/atomic"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/internal/sysinfo"
)

// The window registry (DECISIONS D30): every open window, in the order they
// opened, in place of the single window of earlier phases. A window's own
// objects (its GNUIView, GNUIWindowDelegate and GNUIWindow) carry its slot in
// their index instance variable (objc.ClassDef.Index), so a method AppKit
// calls on one of them finds its window from self, without a global and
// without allocating. An object of no class of ours (the window of an
// NSEvent, a panel, the menu bar's) is looked up among the open windows.
//
// One app-wide pump (Pump) serves every window: it hands each event to the
// window it is for (route), each window keeps its own queue, display link and
// frame handler, and an idle window neither ticks nor holds up another.

// MaxWindows is how many windows can be open at once.
const MaxWindows = 64

var reg struct {
	slots   [MaxWindows]*Window // by slot; nil: free
	open    [MaxWindows]*Window // the open windows, in the order they opened
	n       int
	gen     uint32  // the last generation given (register)
	lastKey *Window // the window of ours that was key last
}

// errTooManyWindows is Open's error past MaxWindows.
var errTooManyWindows = errors.New("platform: too many windows open")

// register gives w the first free slot (a closed window's goes to the next
// one) and a new generation, and marks its objects with both: what is left
// of a closed window (a late callback of one of its objects) never finds the
// window that has its slot now. Main thread.
func register(w *Window) error {
	i := 0
	for i < MaxWindows && reg.slots[i] != nil {
		i++
	}
	if i == MaxWindows {
		return errTooManyWindows
	}
	if reg.gen++; reg.gen == 0 {
		reg.gen = 1
	}
	reg.slots[i], w.slot, w.gen = w, i, reg.gen
	reg.open[reg.n] = w
	reg.n++
	mark := w.slotMark()
	viewDef.Index().Set(w.view, mark)
	delegateDef.Index().Set(w.delegate, mark)
	windowDef.Index().Set(w.win, mark)
	w.registered = true
	return nil
}

// slotMark is what w's objects carry in their index ivar: the generation in
// the high 32 bits, the slot plus 1 in the low.
func (w *Window) slotMark() uintptr { return uintptr(w.gen)<<32 | uintptr(w.slot+1) }

// unregister forgets w: its objects stop finding it (a late callback from
// AppKit finds no window), and the windows after it keep their order.
func unregister(w *Window) {
	if !w.registered {
		return
	}
	w.registered = false
	viewDef.Index().Set(w.view, 0)
	delegateDef.Index().Set(w.delegate, 0)
	windowDef.Index().Set(w.win, 0)
	reg.slots[w.slot] = nil
	for i := 0; i < reg.n; i++ {
		if reg.open[i] == w {
			copy(reg.open[i:reg.n], reg.open[i+1:reg.n])
			reg.n--
			reg.open[reg.n] = nil
			break
		}
	}
	if reg.lastKey == w {
		reg.lastKey = nil
	}
}

// openWindows is the open windows, in the order they opened. The slice is
// the registry's: it changes when a window opens or closes. Main thread.
func openWindows() []*Window { return reg.open[:reg.n] }

// OpenCount is how many windows are open. Main thread.
func OpenCount() int { return reg.n }

// slotWindow is the open window an index ivar's value v names (slotMark):
// the window in its slot if that one has its generation, else nil.
func slotWindow(v uintptr) *Window {
	i := int(v&0xFFFFFFFF) - 1
	if i < 0 || i >= MaxWindows {
		return nil
	}
	if w := reg.slots[i]; w != nil && w.gen == uint32(v>>32) {
		return w
	}
	return nil
}

// viewWindow is the window of GNUIView self.
func viewWindow(self objc.ID) *Window {
	if self == 0 {
		return nil
	}
	if w := slotWindow(viewDef.Index().Get(self)); w != nil && w.view == self {
		return w
	}
	return nil
}

// delegateWindow is the window of GNUIWindowDelegate self.
func delegateWindow(self objc.ID) *Window {
	if self == 0 {
		return nil
	}
	if w := slotWindow(delegateDef.Index().Get(self)); w != nil && w.delegate == self {
		return w
	}
	return nil
}

// nsWindowWindow is the window of GNUIWindow self.
func nsWindowWindow(self objc.ID) *Window {
	if self == 0 {
		return nil
	}
	if w := slotWindow(windowDef.Index().Get(self)); w != nil && w.win == self {
		return w
	}
	return nil
}

// findWindow is the open window whose NSWindow is nswin (any NSWindow, ours
// or not), nil if none.
func findWindow(nswin objc.ID) *Window {
	if nswin == 0 {
		return nil
	}
	for _, w := range openWindows() {
		if w.win == nswin {
			return w
		}
	}
	return nil
}

// viewOrDelegateWindow is the window of self, a GNUIView or a
// GNUIWindowDelegate (a method both classes implement).
func viewOrDelegateWindow(self objc.ID) *Window {
	for _, w := range openWindows() {
		if w.view == self || w.delegate == self {
			return w
		}
	}
	return nil
}

var (
	selKeyWindow        = objc.Sel("keyWindow")
	selMainWindow       = objc.Sel("mainWindow")
	selSheetParent      = objc.Sel("sheetParent")
	selSetAllowsTabbing = objc.Sel("setAllowsAutomaticWindowTabbing:")
	selSetTabbingMode   = objc.Sel("setTabbingMode:")
)

// eventWindow is the window an NSEvent is for: the window it happened in, or
// the one whose sheet it happened in; for an event of no window of ours (a
// panel's, the menu bar's, none), the key window, else the main window, else
// the one that was key last, else the first one opened (with one window
// open, that one). An event for a window of ours that has closed (one that
// outlived it in the queue) is no window's: nil, and AppKit alone gets it.
func eventWindow(ev objc.ID) *Window {
	nswin := objc.Send(ev, selWindow)
	if w := findWindow(nswin); w != nil {
		return w
	}
	if nswin != 0 {
		if objc.SendBool1(nswin, selIsKindOfClass, uintptr(windowClass)) {
			return nil // a GNUIWindow that is not open
		}
		if w := findWindow(objc.Send(nswin, selSheetParent)); w != nil {
			return w
		}
	} else if n := int(objc.Send(ev, selWindowNumber)); n > 0 && wasClosed(n) {
		return nil // its window is gone: AppKit finds none for the number
	}
	return fallbackWindow()
}

// closedNums are the window server's numbers of the windows of ours that
// closed last (a number is not given again in the login session): an event
// still queued for one of them (eventWindow) is no other window's.
var closedNums struct {
	n    [16]int
	next int
}

func rememberClosed(num int) {
	if num > 0 {
		closedNums.n[closedNums.next] = num
		closedNums.next = (closedNums.next + 1) % len(closedNums.n)
	}
}

func wasClosed(num int) bool {
	for _, n := range closedNums.n {
		if n == num {
			return true
		}
	}
	return false
}

// fallbackWindow is the window an event or a command of no particular window
// goes to: the key window if it is ours, else the main window, else the one
// that was key last, else the first one opened; nil with none open.
func fallbackWindow() *Window {
	if reg.n == 0 {
		return nil
	}
	if reg.n == 1 {
		return reg.open[0]
	}
	if w := findWindow(objc.Send(appObj.app, selKeyWindow)); w != nil {
		return w
	}
	if w := findWindow(objc.Send(appObj.app, selMainWindow)); w != nil {
		return w
	}
	if reg.lastKey != nil {
		return reg.lastKey
	}
	return reg.open[0]
}

var selCascade = objc.Sel("cascadeTopLeftFromPoint:")

// cascade moves a window opening while others are open off the centre every
// window opens at, which would hide the one under it: below and right of the
// newest window, by what keeps both title bars in view, as AppKit's document
// windows cascade. The newest, not the key one: two windows opened from the
// same key window would land on each other. A window in full screen or in
// the Dock is no place to start from. Open calls it before registering w.
// Main thread.
func (w *Window) cascade() {
	var from *Window
	for i := reg.n - 1; i >= 0 && from == nil; i-- {
		if reg.open[i].cascadesFrom() {
			from = reg.open[i]
		}
	}
	if from == nil {
		return
	}
	f := objc.SendRect(from.win, selFrame)
	// The first call puts w's top-left on from's and returns the point a
	// title bar below and right of it, where the second puts it.
	objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selCascade)).ArgPoint(f.X, f.Y+f.H).Call(objc.MsgSend)
	x, y := objc.F.F64(0), objc.F.F64(1)
	objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selCascade)).ArgPoint(x, y).Call(objc.MsgSend)
}

func (w *Window) cascadesFrom() bool { return !w.FullScreen() && !w.Miniaturized() }

// Pump is the app-wide event pump with windows open (D5, D30): it blocks
// until an event, a wake, a frame slot of any window or the timeout (ns; 0
// polls, negative waits without one), hands every event to the window it is
// for, and returns. Each window's events are then taken with Events. The
// events Events or Wait handed out before are released first, so a window
// with events still queued (pushed between two pumps) does not block it.
// Main thread.
func Pump(timeoutNs int64) {
	reap()
	pending := false
	for _, w := range openWindows() {
		w.release()
		if len(w.q.ev) > 0 {
			pending = true
		}
	}
	if pending {
		timeoutNs = 0
	}
	pump(timeoutNs)
}

// pump runs nextEventMatchingMask until the timeout and routes the events.
func pump(timeoutNs int64) {
	pumpStats.pumps++
	pumpDepth++
	defer func() { pumpDepth-- }()
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	until := appObj.distantFuture
	switch {
	case timeoutNs == 0:
		until = appObj.distantPast
	case timeoutNs > 0:
		until = objc.ID(F.Reset().Arg(uintptr(clsNSDate)).Arg(uintptr(selDateSinceNow)).
			ArgF64(float64(timeoutNs) / 1e9).Call(objc.MsgSend).R0())
	}
	for i := 0; i < reg.n; i++ {
		if w := reg.open[i]; w.live && !objc.SendBool(w.win, selInLiveResize) {
			w.exitLiveResize() // windowDidEndLiveResize: never arrived (or the resize ended inside a nested loop)
		}
	}
	var pc0 int64
	if ProbeOn {
		pc0 = sysinfo.ThreadCPUNs()
	}
	first := true
	for ev := appNextEvent(until); ev != 0; ev = appNextEvent(appObj.distantPast) {
		if ProbeOn {
			pc1 := sysinfo.ThreadCPUNs()
			if first {
				ProbeNextFirstNs += pc1 - pc0
				ProbeNextFirstN++
			} else {
				ProbeNextDrainNs += pc1 - pc0
				ProbeNextDrainN++
			}
			first = false
			route(ev)
			pc0 = sysinfo.ThreadCPUNs()
			ProbeRouteNs += pc0 - pc1
			continue
		}
		route(ev)
	}
	if ProbeOn {
		pc1 := sysinfo.ThreadCPUNs()
		if first {
			ProbeNextFirstNs += pc1 - pc0
			ProbeNextFirstN++
		} else {
			ProbeNextDrainNs += pc1 - pc0
			ProbeNextDrainN++
		}
	}
	for i := 0; i < reg.n; i++ {
		w := reg.open[i]
		w.reapDialogs()
		w.reapDrags() // images converted on workers, promised files asked for (drag_darwin.go)
		w.reapPaste() // a pasted TIFF converted on a worker (paste_darwin.go)
	}
	F.Call1(objc.PoolPop, pool)
}

// route hands one event to its window (eventWindow). The wake event only
// unblocks the pump: the wakes it stands for are given out here, if the
// run-loop source has not done it yet.
func route(ev objc.ID) {
	pumpStats.routed++
	if uintptr(objc.Send(ev, selType)) == 15 && uintptr(objc.Send(ev, selSubtype))&0xFFFF == wakeSubtype {
		pumpStats.wakeEvents++
		giveWakes()
		return // also used to unblock the loop after a display-link tick
	}
	if w := eventWindow(ev); w != nil {
		w.handle(ev)
		return
	}
	objc.Send1(appObj.app, selSendEvent, uintptr(ev))
}

// release drops the events the last Events or Wait handed out.
func (w *Window) release() {
	if w.returned {
		w.q.dropFirst(w.nOut)
		w.returned = false
	}
}

// Events hands out the events Pump queued for the window (and any pushed
// since the last call). The slice is valid until the next Pump, Wait, Events
// or Pending. Main thread.
func (w *Window) Events() []Event {
	w.release()
	w.returned, w.nOut = true, len(w.q.ev)
	w.frameOut, w.nested = false, false // its frame reached the app's loop
	return w.q.ev
}

// WakeLoop makes the loop turn once, from any goroutine, without telling any
// window that something changed (Wake does): for work that waits for the
// loop's safe point (a window to close) and draws nothing by itself.
func WakeLoop() {
	if loopWake.CompareAndSwap(false, true) {
		osWake()
	}
}

var loopWake atomic.Bool

// Wake marks this window as having something new from another goroutine (a
// worker's result) and wakes the loop: the window gets an EvWake, the others
// do not. Any goroutine; cheap when one is pending. A closed window's mark is
// dropped.
func (w *Window) Wake() {
	if w.wakeMark.CompareAndSwap(false, true) {
		osWake()
	}
}

// giveWakes turns the pending wakes into EvWake events: the app-wide Wake
// for every window, a window's own Wake for it. The woken windows ask for a
// frame (ArmOnWake): inside a loop of AppKit's the pump that would take the
// wake is suspended, and the frame's tick draws it through the frame handler.
// It reports whether any wake was pending.
func giveWakes() bool {
	all := takeWake()
	any := loopWake.Swap(false) || all
	for _, w := range openWindows() {
		if w.wakeMark.Swap(false) || all {
			any = true
			w.q.push(Event{Kind: EvWake, Time: Now()})
			w.RequestFrame()
		}
	}
	return any
}

// keyWindowOurs is the key window if it is one of ours.
func keyWindowOurs() *Window {
	if !appObj.started {
		return nil
	}
	return findWindow(objc.Send(appObj.app, selKeyWindow))
}

// WindowNumber is the window's number in the window server (tests, tools).
func (w *Window) WindowNumber() int { return int(objc.Send(w.win, selWindowNumber)) }

var (
	selWindowNumber = objc.Sel("windowNumber")
	selDealloc      = objc.Sel("dealloc")
)

// WindowObjects counts the objects of Open's windows alive in the process,
// for the open/close leak gate: once a closed window's objects are reaped
// (the next Pump) the counts are back where they were before it opened.
type WindowObjects struct {
	Windows, Views, Delegates int64 // GNUIWindow, GNUIView, GNUIWindowDelegate
	Pending                   int   // released by the next Pump
}

// LiveWindowObjects returns the current counts. Any goroutine for the
// counts; Pending is the main thread's.
func LiveWindowObjects() WindowObjects {
	return WindowObjects{Windows: live.windows.Load(), Views: live.views.Load(), Delegates: live.delegates.Load(), Pending: len(graveyard)}
}

var live struct {
	windows, views, delegates          atomic.Int64
	windowMark, viewMark, delegateMark *objc.Ivar // set on the objects Open counted
}

// countInstances gives def a mark ivar and a dealloc that, on a marked
// object, takes one off n; Open marks what it makes (mark). Dealloc may run
// on any thread.
func countInstances(def *objc.ClassDef, n *atomic.Int64) *objc.Ivar {
	mark := def.Ivar("gnuiCounted")
	def.Method(selDealloc, "v@:", func(c *ffi.CB) {
		self := objc.ID(c.Int(0))
		if mark.Get(self) != 0 {
			mark.Set(self, 0)
			n.Add(-1)
		}
		objc.SendSuper(c.Frame(), def.Superclass(), self, selDealloc)
	})
	return mark
}

// mark counts obj, made by Open, as alive until its dealloc.
func mark(iv *objc.Ivar, obj objc.ID, n *atomic.Int64) {
	if obj != 0 && iv.Get(obj) == 0 {
		iv.Set(obj, 1)
		n.Add(1)
	}
}

// graveyard is the objects of the windows closed since the last Pump: Close
// hands them over and Pump releases them at the top of the loop, so none
// goes from under an AppKit callback or nested loop that asked for the
// close while one of them is on its stack.
var graveyard []objc.ID

// bury hands obj (one reference Open owns) to the next Pump.
func bury(obj objc.ID) {
	if obj != 0 {
		graveyard = append(graveyard, obj)
	}
}

// Throwaway perf probe.
var (
	ProbeOn                                                  bool
	ProbeNextFirstNs, ProbeNextDrainNs, ProbeRouteNs         int64
	ProbeNextFirstN, ProbeNextDrainN                         int64
)

// pumpStats count the pump's work, for a test's look at a loop that stopped
// (DebugPumpState). Main thread.
var pumpStats struct{ performs, pushes, routed, wakeEvents, pumps uint64 }

// pumpDepth is how many pumps are running: more than none is a pump nested
// in a callout of another (a window opened from a performed selector waits
// for its first frame on glass).
var pumpDepth int

// reap releases the closed windows' objects, in the order Close buried them
// (the view and the delegate before the window that may still refer to
// them), inside a pool: what their deallocation autoreleases goes with it.
// Only from the top of the loop: not from a pump nested in a callout, nor
// while AppKit runs a loop of its own (a window closed inside its own live
// resize waits for the loop's end).
func reap() {
	if len(graveyard) == 0 || pumpDepth > 0 || NativeLoop() {
		return
	}
	pool := objc.F.Call0(objc.PoolPush)
	for i, obj := range graveyard {
		objc.Send(obj, selReleaseObj)
		graveyard[i] = 0
	}
	graveyard = graveyard[:0]
	objc.F.Call1(objc.PoolPop, pool)
}
