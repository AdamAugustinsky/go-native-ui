package media

import (
	"fmt"
	"math"
	"time"
)

// MaxPlayers is how many Players can be open at once; NewPlayer returns
// ErrBusy beyond it. Close the Players you are done with.
const MaxPlayers = 32

// The playback rates SetRate takes.
const (
	MinRate = 0.5
	MaxRate = 2
)

// Warm starts the system's audio stack on a goroutine of its own and
// returns at once, so that the first NewPlayer does not pay for it on the
// main thread (about 60 ms on a Mac mini, against about 8 ms for a later
// one). Call it after the first frame once audio is likely (a conversation
// with voice notes is open). Calling it again does nothing.
func Warm() { osWarm() }

// Player plays one clip of PCM. Make it with NewPlayer, use it on the main
// thread, and Close it when done.
type Player struct {
	id     uint64
	format Format
	length time.Duration
	rate   float32
	volume float32
	closed bool
	n      nativePlayer
}

// NewPlayer makes a paused Player at position 0 for pcm, in format f: whole
// frames, at least one. It copies pcm, so the app may reuse it at once.
func NewPlayer(pcm []byte, f Format) (*Player, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	switch fb := f.FrameBytes(); {
	case len(pcm) == 0:
		return nil, fmt.Errorf("%w: no PCM", ErrInvalid)
	case len(pcm)%fb != 0:
		return nil, fmt.Errorf("%w: %d bytes are not whole frames of %d", ErrInvalid, len(pcm), fb)
	case len(pcm) > maxWAVData:
		return nil, fmt.Errorf("%w: %d bytes of PCM (at most %d)", ErrInvalid, len(pcm), maxWAVData)
	}
	p := &Player{id: nextID(), format: f, length: f.Duration(len(pcm)), rate: 1, volume: 1}
	if err := p.n.open(p, pcm); err != nil {
		return nil, err
	}
	return p, nil
}

// ID identifies the Player in its events (Event.ID). It never repeats.
func (p *Player) ID() uint64 { return p.id }

// Format is the format of the PCM it plays.
func (p *Player) Format() Format { return p.format }

// Duration is the clip's length at 1×.
func (p *Player) Duration() time.Duration { return p.length }

// Play starts or resumes playback from the current position. Playing to
// the end delivers an Ended event (once) and puts the position back at 0;
// Play again starts over.
func (p *Player) Play() error {
	if p.closed {
		return ErrClosed
	}
	return p.n.play(p)
}

// Pause stops playback and keeps the position.
func (p *Player) Pause() {
	if !p.closed {
		p.n.pause()
	}
}

// Playing reports whether it is playing now. It allocates nothing.
func (p *Player) Playing() bool { return !p.closed && p.n.playing() }

// Position is how far into the clip playback is, in the clip's own time (a
// second of position is a second of the clip at any rate). It allocates
// nothing: read it every frame while playing.
func (p *Player) Position() time.Duration {
	if p.closed {
		return 0
	}
	return min(max(p.n.position(), 0), p.length)
}

// Seek moves the position to t, clamped to the clip; playback continues
// from there if it was playing.
func (p *Player) Seek(t time.Duration) {
	if !p.closed {
		p.n.seek(min(max(t, 0), p.length))
	}
}

// SetRate sets the playback rate, from MinRate to MaxRate (1.5 and 2 for
// voice notes), keeping the pitch. It applies at once, also while playing.
func (p *Player) SetRate(r float32) {
	if math.IsNaN(float64(r)) {
		r = 1
	}
	p.rate = min(max(r, MinRate), MaxRate)
	if !p.closed {
		p.n.setRate(p.rate)
	}
}

// Rate is the playback rate SetRate set (1 at first).
func (p *Player) Rate() float32 { return p.rate }

// SetVolume sets the gain, from 0 to 1 (1 at first).
func (p *Player) SetVolume(v float32) {
	if math.IsNaN(float64(v)) {
		v = 1
	}
	p.volume = min(max(v, 0), 1)
	if !p.closed {
		p.n.setVolume(p.volume)
	}
}

// Volume is the gain SetVolume set.
func (p *Player) Volume() float32 { return p.volume }

// Close stops playback and frees the Player; an Ended event it had not sent
// yet is not sent. Closing twice does nothing.
func (p *Player) Close() {
	if !p.closed {
		p.closed = true
		p.n.close()
	}
}
