//go:build darwin

package media

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
	"unsafe"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// A Player is an AVAudioPlayer over an in-memory WAV: the header and a copy
// of the PCM in malloc'd memory, which the NSData frees (freeWhenDone) when
// the player lets go of it. enableRate is set before prepareToPlay, so rate
// changes apply with the pitch kept (the system's time-pitch).
//
// The end of playback comes from the player's delegate, which the system
// calls on the main thread (macOS 27; the handler does not rely on it). Each
// player slot has a delegate for the process lifetime (its index variable is
// the slot), so a late call never finds a freed object; it checks that the
// player calling is still the slot's, and takes the slot's reserved ticket
// with a swap, so an end is sent once, and not at all after Close.

// A file Player (OpenPlayer) is an AVAudioPlayer initWithContentsOfURL:,
// made and prepared on a goroutine (the file's I/O and decoder set-up never
// run on the main thread) and handed to the main thread as an openJob: the
// Player attaches it (its delegate and slot) the first time a method finds
// the job done. The job's state decides who frees the object when the
// Player is closed while the goroutine is still working.

type nativePlayer struct {
	obj  objc.ID // AVAudioPlayer, retained
	slot int
	job  *openJob // a file being opened, until attached
}

// openJob is a file being opened on a goroutine.
type openJob struct {
	state  atomic.Uint32 // jobOpening, then jobDone or jobCancelled
	ticket platform.Ticket
	id     uint64
	obj    uintptr // the AVAudioPlayer, retained; set before jobDone
	dur    time.Duration
	err    error
}

const (
	jobOpening   = iota
	jobDone      // the goroutine finished: obj or err is set, Opened or Failed sent
	jobCancelled // Close came first: the goroutine frees what it made
)

var players struct {
	used [MaxPlayers]bool    // main thread
	del  [MaxPlayers]objc.ID // the slots' delegates, kept
	slot [MaxPlayers]struct {
		obj    atomic.Uintptr // the AVAudioPlayer the slot plays, 0 when free
		id     atomic.Uint64  // its Player's ID
		ticket atomic.Uint64  // the Ended event's reserved ticket, 0 for none
	}
}

func (n *nativePlayer) open(p *Player, pcm []byte) error {
	if err := loadAV(); err != nil {
		return err
	}
	i, err := freeSlot()
	if err != nil {
		return err
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)

	hl := wavHeaderLen(p.format)
	total := hl + len(pcm)
	mem := objc.F.Call1(objc.Malloc, uintptr(total))
	if mem == 0 {
		return fmt.Errorf("%w: no memory for %d bytes", ErrSystem, total)
	}
	buf := cBytes(mem, total)
	appendWAVHeader(buf[:0], p.format, len(pcm))
	copy(buf[hl:], pcm)
	data := objc.ID(objc.F.Reset().Arg(uintptr(objc.Send(av.clsData, sel.alloc))).Arg(uintptr(sel.initNoCopy)).
		Arg(mem).Arg(uintptr(total)).ArgBool(true).Call(objc.MsgSend).R0())
	if data == 0 {
		objc.F.Call1(objc.Free, mem)
		return fmt.Errorf("%w: NSData", ErrSystem)
	}
	*errOut = 0
	obj := objc.Send2(objc.Send(av.clsPlayer, sel.alloc), sel.initWithData, uintptr(data), uintptr(unsafe.Pointer(errOut)))
	objc.Release(data) // the player holds it
	if obj == 0 {
		return fmt.Errorf("%w: AVAudioPlayer: %s", ErrSystem, nsError(*errOut))
	}
	sendBoolArg(obj, sel.setEnableRate, true)
	attach(i, p.id, obj)
	if !objc.SendBool(obj, sel.prepareToPlay) {
		detach(i, obj)
		objc.Release(obj)
		return fmt.Errorf("%w: AVAudioPlayer prepareToPlay (no audio output?)", ErrSystem)
	}
	players.used[i] = true
	n.obj, n.slot = obj, i
	return nil
}

// freeSlot finds a free player slot (main thread). The slot is not taken
// until the caller sets players.used.
func freeSlot() (int, error) {
	for k, u := range players.used {
		if !u {
			return k, nil
		}
	}
	return 0, fmt.Errorf("%w: %d players are open", ErrBusy, MaxPlayers)
}

// attach makes obj the player of slot i, with the slot's delegate (main
// thread).
func attach(i int, id uint64, obj objc.ID) {
	if players.del[i] == 0 {
		players.del[i] = objc.Send(objc.Send(av.clsDelegate, sel.alloc), sel.init)
		delIndex.Set(players.del[i], uintptr(i))
	}
	s := &players.slot[i]
	s.id.Store(id)
	s.ticket.Store(0)
	s.obj.Store(uintptr(obj))
	objc.Send1(obj, sel.setDelegate, uintptr(players.del[i]))
}

// detach undoes attach.
func detach(i int, obj objc.ID) {
	players.slot[i].obj.Store(0)
	objc.Send1(obj, sel.setDelegate, 0)
}

// openFile starts opening the file at path (main thread): it takes a slot
// and an event record, and the rest happens on a goroutine.
func (n *nativePlayer) openFile(p *Player, path string) error {
	if err := loadAV(); err != nil {
		return err
	}
	i, err := freeSlot()
	if err != nil {
		return err
	}
	tk, err := events().Reserve(0)
	if err != nil {
		return ErrBusy
	}
	players.used[i] = true
	n.slot = i
	n.job = &openJob{ticket: tk, id: p.id}
	go n.job.run(path)
	return nil
}

// run opens the file (a goroutine of its own, through internal/cf).
func (j *openJob) run(path string) {
	obj, dur, err := openFileObj(path)
	j.obj, j.dur, j.err = obj, dur, err
	kind := Opened
	if err != nil {
		kind = Failed
	}
	if j.state.CompareAndSwap(jobOpening, jobDone) {
		complete(j.ticket, kind, j.id)
		return
	}
	if obj != 0 { // closed meanwhile: nobody else has it
		fr := cf.Acquire()
		defer cf.Release(fr)
		defer cf.Pool(fr)()
		cf.Msg(fr, obj, cf.Sel(fr, "release"))
	}
}

// openFileObj makes and prepares the AVAudioPlayer for path, with rate
// enabled; obj is retained.
func openFileObj(path string) (obj uintptr, dur time.Duration, err error) {
	if _, err := os.Stat(path); err != nil {
		return 0, 0, err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()
	url := cf.FileURL(fr, path, false)
	if url == 0 {
		return 0, 0, fmt.Errorf("%w: %q is not a file path", ErrInvalid, path)
	}
	defer cf.CFRelease(fr, url)
	out := make([]uintptr, 1) // heap: the NSError out parameter
	o := cf.Msg(fr, cf.Cls(fr, "AVAudioPlayer"), cf.Sel(fr, "alloc"))
	o = cf.Msg(fr, o, cf.Sel(fr, "initWithContentsOfURL:error:"), url, uintptr(unsafe.Pointer(&out[0])))
	if o == 0 {
		why := "no error given"
		if out[0] != 0 {
			why = cf.GoString(fr, cf.Msg(fr, out[0], cf.Sel(fr, "localizedDescription")))
		}
		return 0, 0, fmt.Errorf("%w: %s: %s", ErrFormat, filepath.Base(path), why)
	}
	cf.Msg(fr, o, cf.Sel(fr, "setEnableRate:"), 1)
	if !cf.MsgBool(fr, o, cf.Sel(fr, "prepareToPlay")) {
		cf.Msg(fr, o, cf.Sel(fr, "release"))
		return 0, 0, fmt.Errorf("%w: AVAudioPlayer prepareToPlay (no audio output?)", ErrSystem)
	}
	sd := cf.Sel(fr, "duration") // before the chain: Sel uses the frame too
	d := fr.Reset().Arg(o).Arg(sd).Call(cf.MsgAddr()).F64(0)
	return o, time.Duration(d * float64(time.Second)), nil
}

// live attaches a file whose goroutine has finished and reports whether the
// player is usable (main thread). A failed open gives its slot back and sets
// p.err.
func (n *nativePlayer) live(p *Player) bool {
	if n.obj != 0 {
		return true
	}
	j := n.job
	if j == nil || j.state.Load() != jobDone {
		return false
	}
	n.job = nil
	if j.err != nil {
		p.err = j.err
		players.used[n.slot] = false
		n.slot = -1
		return false
	}
	obj := objc.ID(j.obj)
	attach(n.slot, p.id, obj)
	n.obj = obj
	p.length = j.dur
	if p.rate != 1 {
		n.setRate(p.rate)
	}
	if p.volume != 1 {
		n.setVolume(p.volume)
	}
	return true
}

// finished is the delegate's audioPlayerDidFinishPlaying:successfully: and
// audioPlayerDecodeErrorDidOccur:error: (the callback's frame; no
// allocation).
func finished(c *ffi.CB, ok bool) {
	i := int(delIndex.Get(objc.ID(c.Int(0))))
	if i >= MaxPlayers {
		return
	}
	s := &players.slot[i]
	if s.obj.Load() != c.Int(2) {
		return // a player that is not the slot's any more
	}
	tk := s.ticket.Swap(0)
	if tk == 0 {
		return
	}
	kind := Ended
	if !ok {
		kind = Failed
	}
	complete(platform.Ticket(tk), kind, s.id.Load())
}

func (n *nativePlayer) play(*Player) error {
	s := &players.slot[n.slot]
	if s.ticket.Load() == 0 {
		tk, err := events().Reserve(0)
		if err != nil {
			return ErrBusy
		}
		s.ticket.Store(uint64(tk))
	}
	if !objc.SendBool(n.obj, sel.play) {
		if tk := s.ticket.Swap(0); tk != 0 {
			events().Cancel(platform.Ticket(tk))
		}
		return fmt.Errorf("%w: AVAudioPlayer play", ErrSystem)
	}
	return nil
}

// Near the end, AVAudioPlayer.pause can reset the position after returning
// and leave a resumed player without an end. stop preserves currentTime
// but tears down playback resources, avoiding that transition.
func (n *nativePlayer) pause() { objc.Send(n.obj, sel.stop) }

func (n *nativePlayer) playing() bool { return objc.SendBool(n.obj, sel.isPlaying) }

func (n *nativePlayer) position() time.Duration {
	return time.Duration(objc.SendF64(n.obj, sel.currentTime) * float64(time.Second))
}

func (n *nativePlayer) seek(t time.Duration) { sendF64Arg(n.obj, sel.setCurrentTime, t.Seconds()) }

func (n *nativePlayer) setRate(r float32) { sendF32Arg(n.obj, sel.setRate, r) }

func (n *nativePlayer) setVolume(v float32) { sendF32Arg(n.obj, sel.setVolume, v) }

func (n *nativePlayer) close() {
	if j := n.job; j != nil { // a file still opening, or opened and not attached
		n.job = nil
		events().Cancel(j.ticket)
		if !j.state.CompareAndSwap(jobOpening, jobCancelled) && j.obj != 0 {
			objc.Release(objc.ID(j.obj))
		}
	}
	if n.slot < 0 { // a failed open already gave its slot back
		return
	}
	if n.obj != 0 {
		s := &players.slot[n.slot]
		s.obj.Store(0)
		if tk := s.ticket.Swap(0); tk != 0 {
			events().Cancel(platform.Ticket(tk))
		}
		pool := objc.F.Call0(objc.PoolPush)
		objc.Send(n.obj, sel.stop)
		objc.Send1(n.obj, sel.setDelegate, 0)
		objc.Release(n.obj)
		objc.F.Call1(objc.PoolPop, pool)
		n.obj = 0
	}
	players.used[n.slot] = false
	n.slot = -1
}
