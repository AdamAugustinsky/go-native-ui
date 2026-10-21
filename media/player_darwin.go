//go:build darwin

package media

import (
	"fmt"
	"sync/atomic"
	"time"
	"unsafe"

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

type nativePlayer struct {
	obj  objc.ID // AVAudioPlayer, retained
	slot int
}

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
	i := -1
	for k, u := range players.used {
		if !u {
			i = k
			break
		}
	}
	if i < 0 {
		return fmt.Errorf("%w: %d players are open", ErrBusy, MaxPlayers)
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
	if players.del[i] == 0 {
		players.del[i] = objc.Send(objc.Send(av.clsDelegate, sel.alloc), sel.init)
		delIndex.Set(players.del[i], uintptr(i))
	}
	s := &players.slot[i]
	s.id.Store(p.id)
	s.ticket.Store(0)
	s.obj.Store(uintptr(obj))
	objc.Send1(obj, sel.setDelegate, uintptr(players.del[i]))
	if !objc.SendBool(obj, sel.prepareToPlay) {
		s.obj.Store(0)
		objc.Send1(obj, sel.setDelegate, 0)
		objc.Release(obj)
		return fmt.Errorf("%w: AVAudioPlayer prepareToPlay (no audio output?)", ErrSystem)
	}
	players.used[i] = true
	n.obj, n.slot = obj, i
	return nil
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

func (n *nativePlayer) pause() { objc.Send(n.obj, sel.pause) }

func (n *nativePlayer) playing() bool { return objc.SendBool(n.obj, sel.isPlaying) }

func (n *nativePlayer) position() time.Duration {
	return time.Duration(objc.SendF64(n.obj, sel.currentTime) * float64(time.Second))
}

func (n *nativePlayer) seek(t time.Duration) { sendF64Arg(n.obj, sel.setCurrentTime, t.Seconds()) }

func (n *nativePlayer) setRate(r float32) { sendF32Arg(n.obj, sel.setRate, r) }

func (n *nativePlayer) setVolume(v float32) { sendF32Arg(n.obj, sel.setVolume, v) }

func (n *nativePlayer) close() {
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
	players.used[n.slot] = false
	n.obj = 0
}
