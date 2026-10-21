package media_test

import (
	"time"

	"gonativeui/app"
	"gonativeui/media"
)

type voiceNotes struct {
	player   *media.Player
	recorder *media.Recorder
	progress float32
	level    float32
}

// A voice note's playback and recording, as a chat uses them: everything on
// the main thread, the per-frame reads in Build, the ends in Event.
func Example() {
	var v voiceNotes
	pcm := make([]byte, 48000*2) // a second of silence, decoded by the app
	p, err := media.NewPlayer(pcm, media.Format{SampleRate: 48000, Channels: 1, Sample: media.Int16})
	if err != nil {
		return // media.ErrUnsupported outside macOS
	}
	v.player = p
	p.SetRate(1.5)
	p.Play()

	v.recorder = media.NewRecorder(media.RecorderOptions{SampleRate: 48000})
	_ = v.recorder.Start() // media.ErrNotAuthorized until perm.Request(perm.Microphone) is granted
}

// build runs every frame.
func (v *voiceNotes) build() {
	if v.player.Playing() {
		v.progress = float32(v.player.Position()) / float32(v.player.Duration())
	}
	if v.recorder.State() == media.Recording {
		v.level = v.recorder.Level().Amplitude()
		_ = v.recorder.Elapsed() > time.Minute
	}
}

// event is the app's Handler.Event.
func (v *voiceNotes) event(a *app.App, e *app.Event) {
	if m, ok := media.EventOf(e); ok {
		switch {
		case m.Kind == media.Ended && m.ID == v.player.ID():
			// play the next voice note
		case m.Kind == media.Recorded && m.ID == v.recorder.ID():
			pcm, f, err := v.recorder.Take()
			_, _, _ = pcm, f, err // hand the PCM to the encoder
		}
	}
	a.DefaultEvent(e)
}
