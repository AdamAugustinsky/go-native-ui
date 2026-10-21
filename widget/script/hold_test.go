package script_test

import (
	"strings"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/script"
)

// play runs frames of p into in, clearing the per-frame input between them, and
// returns what each frame's Text held.
func playText(t *testing.T, p *script.Player, in *ui.Input, max int) []string {
	t.Helper()
	var out []string
	for f := 0; f < max; f++ {
		more := p.Script(f, in)
		out = append(out, string(in.Text[:in.NText]))
		in.ClearFrame()
		if !more {
			return out
		}
		if p.Done() {
			return out
		}
	}
	t.Fatalf("the steps did not end in %d frames", max)
	return nil
}

// Text longer than a frame's buffer (ui.MaxText) is delivered over the frames
// that follow, whole and in order, never cut inside a character, and the step
// ends only when it has all been typed.
func TestTypeLongerThanOneFrameArrivesInFull(t *testing.T) {
	long := strings.Repeat("0123456789", 250) // 2500 bytes
	// a multi-byte character astride the buffer's end
	astride := strings.Repeat("a", ui.MaxText-1) + "日本語" + strings.Repeat("é", 700)
	for name, text := range map[string]string{"ascii": long, "astride": astride, "empty": ""} {
		t.Run(name, func(t *testing.T) {
			var in ui.Input
			p := script.NewPlayer(nil, []script.Step{script.Type(text)}, script.Options{Synthetic: true})
			frames := playText(t, p, &in, 50)
			if got := strings.Join(frames, ""); got != text {
				t.Fatalf("typed %d bytes of %d (%d frames)", len(got), len(text), len(frames))
			}
			for i, f := range frames {
				if len(f) > ui.MaxText {
					t.Errorf("frame %d has %d bytes", i, len(f))
				}
				if f != "" && !strings.HasPrefix(f, string([]rune(f)[:1])) {
					t.Errorf("frame %d starts inside a character", i)
				}
			}
			if len(text) > ui.MaxText && len(frames) < 2 {
				t.Errorf("%d bytes in %d frame", len(text), len(frames))
			}
			if p.Err() != nil {
				t.Error(p.Err())
			}
		})
	}
	// a buffer already part full from other input leaves less room
	var in ui.Input
	in.AppendText(strings.Repeat("x", ui.MaxText-2))
	p := script.NewPlayer(nil, []script.Step{script.Type("日本")}, script.Options{Synthetic: true})
	p.Script(0, &in)
	if got := string(in.Text[:in.NText]); strings.Contains(got, "\xe6") && !strings.HasSuffix(got, "日") && !strings.HasSuffix(got, "日本") {
		t.Errorf("a character was cut in a part full buffer: %q", got[len(got)-4:])
	}
	if p.Done() {
		t.Error("the step ended with text left to type")
	}
	in.ClearFrame()
	p.Script(1, &in)
	if got := string(in.Text[:in.NText]); !strings.HasSuffix(got, "本") || !p.Done() {
		t.Errorf("the rest: %q done %v", got, p.Done())
	}
}

// A caller that stops calling Script once the steps are done (the agent bridge)
// leaves what the last step held for a frame held: Unwind lets go of it, and the
// modifiers go back to what they were unless something else changed them since.
func TestUnwindReleasesTheModifiersOfALastStep(t *testing.T) {
	mods := ui.ModSuper | ui.ModShift
	var in ui.Input
	p := script.NewPlayer(nil, []script.Step{script.Key('N', mods)}, script.Options{Synthetic: true})
	p.Script(0, &in)
	if in.Mods != mods || !p.Done() {
		t.Fatalf("after the step: mods %v, done %v", in.Mods, p.Done())
	}
	in.ClearFrame()
	p.Unwind(&in, ui.ModAlt)
	if in.Mods != ui.ModAlt {
		t.Errorf("after Unwind: %v, want the base modifiers", in.Mods)
	}
	p.Unwind(&in, ui.ModCtrl) // again: nothing is held now
	if in.Mods != ui.ModAlt {
		t.Errorf("a second Unwind changed them: %v", in.Mods)
	}

	// modifiers somebody else set since are not touched
	in = ui.Input{}
	p = script.NewPlayer(nil, []script.Step{script.Key('N', mods)}, script.Options{Synthetic: true})
	p.Script(0, &in)
	in.Mods = ui.ModCtrl // a real key event
	p.Unwind(&in, 0)
	if in.Mods != ui.ModCtrl {
		t.Errorf("Unwind overwrote the system's modifiers: %v", in.Mods)
	}
}
