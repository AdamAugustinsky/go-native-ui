//go:build !darwin

package media

import "time"

// Not yet on this OS: NewPlayer and Start fail with ErrUnsupported, so the
// other methods are never reached with a native object behind them.

type nativePlayer struct{}

func (*nativePlayer) open(*Player, []byte) error { return ErrUnsupported }
func (*nativePlayer) play(*Player) error         { return ErrUnsupported }
func (*nativePlayer) pause()                     {}
func (*nativePlayer) playing() bool              { return false }
func (*nativePlayer) position() time.Duration    { return 0 }
func (*nativePlayer) seek(time.Duration)         {}
func (*nativePlayer) setRate(float32)            {}
func (*nativePlayer) setVolume(float32)          {}
func (*nativePlayer) close()                     {}

func newNativeRecorder() (recBackend, error) { return nil, ErrUnsupported }

func osWarm() {}
