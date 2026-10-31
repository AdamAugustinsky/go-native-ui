package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

// A real window draws partial frames (D92) through the frame loop, and each
// one, once drawn, has the pixels of the same frame drawn whole
// (GNUI_DAMAGE_CHECK). The frames change a label, a progress bar and a
// switch's animation now and then, and are otherwise unchanged: those are
// ended without a present.
func TestPartialFramesInAWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a window")
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), nestedChild+"=damage")
	out, err := cmd.CombinedOutput()
	s := string(out)
	if i := strings.Index(s, "SKIP:"); i >= 0 {
		t.Skip(strings.TrimSpace(s[i+5:]))
	}
	var line string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "damage: ") {
			line = l
		}
		if strings.HasPrefix(l, "gnui damage:") {
			t.Error(l)
		}
	}
	if err != nil || line == "" {
		if len(s) > 3000 {
			s = s[len(s)-3000:]
		}
		t.Fatalf("the child failed (%v):\n%s", err, s)
	}
	t.Log(line)
	var frames, partial, skipped, mismatches int
	if _, err := fmt.Sscanf(line, "damage: frames %d, partial %d, skipped %d, mismatches %d", &frames, &partial, &skipped, &mismatches); err != nil {
		t.Fatalf("%q: %v", line, err)
	}
	if mismatches != 0 {
		t.Errorf("%d partial frames differ from whole ones", mismatches)
	}
	if partial < frames/2 || skipped == 0 || partial == skipped {
		t.Errorf("frames %d, partial %d, skipped %d: the run tested too little", frames, partial, skipped)
	}
}

type damageBuilder struct {
	a    *App
	kit  *widget.Kit
	n    int
	on   bool
	text [8]string
}

func (b *damageBuilder) Init(a *App) error {
	b.a = a
	b.kit = widget.NewKit(a.UI, a.Text, rex.Theme())
	a.dmg.read, a.dmg.check = true, true
	return nil
}

func (b *damageBuilder) Build(c *ui.Context) {
	b.n++
	k := b.kit
	k.Begin(c)
	k.BeginCard("card")
	k.Label("Partial frames")
	for i := range b.text {
		if b.text[i] == "" || b.n%13 == i {
			b.text[i] = fmt.Sprintf("row %d, frame %d", i, b.n/13)
		}
		k.Label(b.text[i])
	}
	k.ProgressBar("progress", float32(b.n/7%10)/10)
	if b.n%40 == 0 {
		b.on = !b.on
	}
	k.Switch("Switch", &b.on)
	k.Button("Button")
	k.EndCard()
	k.End()
}

func runDamageChild() {
	b := &damageBuilder{}
	_, err := RunBench(Config{Title: "damage", Width: 640, Height: 420}, b, BenchOptions{Warm: 30, Frames: 180})
	if err != nil {
		fmt.Println("SKIP: cannot run a window here:", err)
		return
	}
	d := &b.a.dmg
	fmt.Printf("damage: frames %d, partial %d, skipped %d, mismatches %d\n", d.Frames, d.Partial, d.Skipped, d.Mismatches)
}
