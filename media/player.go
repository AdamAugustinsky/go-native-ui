package media

import (
	"fmt"
	"math"
	"time"
)

// MaxPlayers is how many Players can be open at once; NewPlayer and
// OpenPlayer return ErrBusy beyond it. Close the Players you are done with.
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

// Player plays one clip: PCM from NewPlayer, or a file from OpenPlayer. Use
// it on the main thread, and Close it when done.
type Player struct {
	id     uint64
	format Format
	length time.Duration
	rate   float32
	volume float32
	closed bool
	err    error // why a file did not open
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

// OpenPlayer makes a paused Player at position 0 for the audio file at path,
// in any format the system decodes (MP3, AAC, M4A, WAV, AIFF, CAF, FLAC,
// system sounds such as /System/Library/Sounds/Tink.aiff). It returns at
// once and never reads the file on the calling thread: the file is opened
// and prepared on a goroutine, and an Opened event arrives when the Player
// is ready, or a Failed event when the file is missing, is not audio, or
// is corrupt (Err says why). Until Ready reports true, Duration is 0,
// Position is 0, Play returns ErrState and Seek does nothing; SetRate and
// SetVolume are kept and apply once it is ready.
//
// The error is for what is known without the file: ErrInvalid for an empty
// path, ErrBusy with MaxPlayers open, ErrUnsupported on Windows and Linux.
// A file Player has no PCM Format (Format returns the zero Format). Close it
// when done, also one that failed.
func OpenPlayer(path string) (*Player, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: no path", ErrInvalid)
	}
	p := &Player{id: nextID(), rate: 1, volume: 1}
	if err := p.n.openFile(p, path); err != nil {
		return nil, err
	}
	return p, nil
}

// Ready reports whether a Player from OpenPlayer has opened its file, and
// is playable: true from the Opened event on (and always for NewPlayer's).
// It is false while opening, after a failure (Err), and once closed. It
// allocates nothing.
func (p *Player) Ready() bool { return !p.closed && p.live() }

// Err is why OpenPlayer's file did not open (the Failed event reports it):
// an error that wraps the file system's (errors.Is fs.ErrNotExist) or
// ErrFormat for a file that is not audio the system can decode. It is nil
// while opening and when the Player is ready.
func (p *Player) Err() error {
	if !p.closed {
		p.live()
	}
	return p.err
}

// live attaches the opened file to p, if it has opened, and reports whether
// p can play. A message or two at most; it allocates nothing.
func (p *Player) live() bool { return p.n.live(p) }

// ID identifies the Player in its events (Event.ID). It never repeats.
func (p *Player) ID() uint64 { return p.id }

// Format is the format of the PCM it plays.
func (p *Player) Format() Format { return p.format }

// Duration is the clip's length at 1×: 0 for a file Player until it is
// Ready.
func (p *Player) Duration() time.Duration {
	if !p.closed {
		p.live()
	}
	return p.length
}

// Play starts or resumes playback from the current position. Playing to
// the end delivers an Ended event (once) and puts the position back at 0;
// Play again starts over.
func (p *Player) Play() error {
	if p.closed {
		return ErrClosed
	}
	if !p.live() {
		if p.err != nil {
			return p.err
		}
		return fmt.Errorf("%w: Play while the file is opening", ErrState)
	}
	return p.n.play(p)
}

// Pause stops playback and keeps the position.
func (p *Player) Pause() {
	if !p.closed && p.live() {
		p.n.pause()
	}
}

// Playing reports whether it is playing now. It allocates nothing.
func (p *Player) Playing() bool { return !p.closed && p.live() && p.n.playing() }

// Position is how far into the clip playback is, in the clip's own time (a
// second of position is a second of the clip at any rate). It allocates
// nothing: read it every frame while playing.
func (p *Player) Position() time.Duration {
	if p.closed || !p.live() {
		return 0
	}
	return min(max(p.n.position(), 0), p.length)
}

// Seek moves the position to t, clamped to the clip; playback continues
// from there if it was playing.
func (p *Player) Seek(t time.Duration) {
	if !p.closed && p.live() {
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
	if !p.closed && p.live() {
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
	if !p.closed && p.live() {
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
