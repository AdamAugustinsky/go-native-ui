package app

import (
	"bytes"
	"testing"

	"gonativeui/text"
	"gonativeui/ui"
)

// still draws the same text and box every frame.
type still struct {
	a     *App
	font  text.FontKey
	scale float32
}

func (b *still) Init(a *App) error { b.a = a; return nil }

func (b *still) Build(c *ui.Context) {
	if b.scale != c.Scale() {
		b.font = b.a.Text.Font(text.FontDesc{SizePt: 15, Weight: 400, Scale: c.Scale()})
		b.scale = c.Scale()
	}
	c.PushFont(b.font)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextPad(10)
	c.NextBG(blue)
	c.Open(ui.DrawBackground, "bg")
	c.NextSize(ui.TextSize(0), ui.TextSize(0))
	c.Box(ui.DrawText, "Device lost and found")
	c.Close()
	c.PopFont()
}

// A lost device is rebuilt at the next frame: the glyph atlas, whose pages the
// shaper had already marked clean, goes back to the new textures in full, and
// the frame is the one drawn before the loss.
func TestOffscreenFrameSurvivesADeviceLoss(t *testing.T) {
	o, err := NewOffscreen(Config{}, 240, 60, 1, &still{})
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	o.Frame()
	img, _ := o.Frame()
	before := append([]byte(nil), img.Pix...)
	text := 0
	for i := 0; i < len(before); i += 4 { // the label's ink: not the background's blue
		if before[i] != 0 || before[i+1] != 0 || before[i+2] == 0 {
			text++
		}
	}
	if text == 0 {
		t.Fatal("the label draws nothing before the loss")
	}

	o.R.SimulateDeviceLoss()
	if !o.deviceLost() {
		t.Fatal("the app does not see the lost device")
	}
	img, _ = o.Frame()
	if o.deviceLost() || o.R.Epoch() != 1 {
		t.Errorf("after the next frame: lost %v, epoch %d, want recovered and 1", o.deviceLost(), o.R.Epoch())
	}
	if !bytes.Equal(before, img.Pix) {
		diff := 0
		for i := range before {
			if before[i] != img.Pix[i] {
				diff++
			}
		}
		t.Errorf("the frame differs after the device was rebuilt: %d of %d bytes", diff, len(before))
	}
}
