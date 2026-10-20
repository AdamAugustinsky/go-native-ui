// Command composerdemo is the composer's demo (N4): a conversation whose
// messages are drawn by ui and a native editor (widget/base.NativeEditor, an
// NSTextView on macOS) to write the next one, with a mention completion for
// "@". Enter sends, Shift+Enter adds a line; the editor grows to six lines,
// then scrolls.
//
//	composerdemo                 run it
//	composerdemo -o out.png      render offscreen (a placeholder where the editor is)
//	composerdemo -bench -gate    frames with the editor focused; exit 3 on any allocation
//	composerdemo -idle 10s       the editor focused, then nothing: frames built and CPU time
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"
)

const winW, winH = 520, 420

const fieldBG = 0x8080802A

var names = [...]string{"Paolo", "Pamela", "Petra", "Quentin", "Rosa"}

type demo struct {
	a       *app.App
	k       *widget.Kit
	ed      *base.NativeEditor
	msgs    [][]byte
	popup   bool
	trigger platform.TextRange
	matches [len(names)]int
	nMatch  int
	row     int
	spans   []platform.TextSpan
	focus   bool // -bench, -idle: give the editor the keyboard at the first frame
	focused bool
	key     atomic.Bool // the window was key at the last frame
	builds  atomic.Int64
}

func main() {
	out := flag.String("o", "", "render offscreen at 2× into this PNG and exit")
	bench := flag.Bool("bench", false, "draw frames with the editor focused and print the bench report as JSON")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless the measured frames allocated nothing")
	idle := flag.Duration("idle", 0, "focus the editor, wait -settle, then print the frames built and the CPU time used over this long, and exit")
	settle := flag.Duration("settle", 1500*time.Millisecond, "with -idle: how long to wait before measuring")
	unfocused := flag.Bool("unfocused", false, "with -idle: leave the editor without the keyboard")
	flag.Parse()
	d := newDemo()
	var err error
	switch {
	case *out != "":
		err = d.render(*out)
	case *bench:
		err = runBench(d, *gate)
	case *idle > 0:
		d.focus = !*unfocused
		go func() {
			time.Sleep(*settle)
			n, cpu := d.builds.Load(), cpuTime()
			time.Sleep(*idle)
			fmt.Printf("{\"IdleSeconds\": %g, \"IdleFrames\": %d, \"CPUms\": %.1f, \"EditorFocused\": %v, \"KeyWindow\": %v}\n",
				idle.Seconds(), d.builds.Load()-n, float64(cpuTime()-cpu)/1e6, d.focused, d.key.Load())
			os.Exit(0)
		}()
		err = app.Run(config(), d)
	default:
		err = app.Run(config(), d)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "composerdemo:", err)
		os.Exit(1)
	}
}

func newDemo() *demo {
	d := &demo{}
	for _, s := range []string{"Is the build green?", "@Paolo the editor is in: try a mention."} {
		d.msgs = append(d.msgs, []byte(s))
	}
	return d
}

func config() app.Config {
	return app.Config{Title: "Composer", Width: winW, Height: winH, MinWidth: 360, MinHeight: 260}
}

func (d *demo) Init(a *app.App) error {
	d.a = a
	d.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	a.UI.Reserve(256)
	ed, err := base.NewNativeEditor(a.Win, platform.EditorOptions{Placeholder: "Message", MaxLen: 4000, InsetX: 4, InsetY: 6})
	if err != nil {
		return err
	}
	if ed.Editor != nil {
		ed.Reserve(4000)
	}
	d.ed = ed
	d.spans = make([]platform.TextSpan, 0, 64)
	return nil
}

func (d *demo) Build(c *ui.Context) {
	d.builds.Add(1)
	k := d.k
	k.Begin(c)
	k.BeginWindow()
	k.NextHeight(widget.Fill)
	k.NextGap(6)
	k.Column("messages")
	for i, m := range d.msgs {
		k.Row(rowLabel(i))
		k.LabelBytes(m)
		k.EndRow()
	}
	k.EndColumn()
	if d.popup {
		k.NextGap(2)
		k.Column("completion")
		for i := 0; i < d.nMatch; i++ {
			name := names[d.matches[i]]
			if i == d.row {
				k.Label("▸ " + name)
			} else {
				k.Label("   " + name)
			}
		}
		k.EndColumn()
	}
	k.NextCross(ui.CrossEnd)
	k.Row("composer")
	// The field under the editor: the text view draws no background (D7b
	// styles the composer).
	c.NextBG(fieldBG)
	c.NextRadius(8 * c.Scale())
	k.NextPad(2)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "field", widget.Fill, ui.Children(1), ui.DrawBackground)
	s := k.NativeEditor(d.ed, "Message")
	k.EndContainer()
	send := k.Button("Send").Clicked
	k.EndRow()
	k.EndWindow()
	k.End()
	if d.ed.Editor != nil {
		d.take(s, send)
	}
}

var rowLabels [64]string

func rowLabel(i int) string {
	if i >= len(rowLabels) {
		i = len(rowLabels) - 1
	}
	if rowLabels[i] == "" {
		rowLabels[i] = fmt.Sprintf("m%d", i)
	}
	return rowLabels[i]
}

// take handles the editor's signals: sending, the mention completion and its
// spans.
func (d *demo) take(s base.NativeEditorSignal, send bool) {
	ed := d.ed
	if d.focus {
		d.focus = false
		activate(d.a)
		ed.Focus()
	}
	d.focused = s.Focused
	d.key.Store(keyWindow(d.a))
	if (s.Submitted || send) && len(bytes.TrimSpace(ed.Text())) > 0 {
		if len(d.msgs) == len(rowLabels) {
			d.msgs = d.msgs[1:]
		}
		d.msgs = append(d.msgs, bytes.Clone(ed.Text()))
		ed.SetText(nil)
		d.close()
	}
	for c := ed.TakeCommand(); c != platform.CmdNone; c = ed.TakeCommand() {
		switch c {
		case platform.CmdUp:
			d.row = (d.row + d.nMatch - 1) % d.nMatch
		case platform.CmdDown, platform.CmdTab:
			d.row = (d.row + 1) % d.nMatch
		case platform.CmdBacktab:
			d.row = (d.row + d.nMatch - 1) % d.nMatch
		case platform.CmdAccept:
			name := names[d.matches[d.row]]
			at := d.trigger.Start
			repl := make([]byte, 0, len(name)+2)
			repl = append(append(append(repl, '@'), name...), ' ')
			ed.Replace(d.trigger, repl, platform.TextRange{Start: at + len(repl), End: at + len(repl)})
			d.close()
		case platform.CmdCancel:
			d.close()
		}
	}
	if s.Cancelled {
		d.popup = false
	}
	if s.Changed && !s.Composing {
		d.mentions()
		d.complete()
	}
}

func (d *demo) close() {
	d.popup = false
	d.ed.SetCompletionActive(false)
}

// mentions styles every "@Name" of a known name.
func (d *demo) mentions() {
	t := d.ed.Text()
	d.spans = d.spans[:0]
	for i := 0; i < len(t); i++ {
		if t[i] != '@' {
			continue
		}
		for _, n := range names {
			if bytes.HasPrefix(t[i+1:], []byte(n)) && len(d.spans) < cap(d.spans) {
				d.spans = append(d.spans, platform.TextSpan{Range: platform.TextRange{Start: i, End: i + 1 + len(n)}, Color: 0x0A84FFFF})
			}
		}
	}
	d.ed.SetSpans(d.spans)
}

// complete opens the completion for "@" and a prefix of a name before the
// caret, and closes it otherwise.
func (d *demo) complete() {
	t, caret := d.ed.Text(), d.ed.Selection().Focus
	at := bytes.LastIndexByte(t[:caret], '@')
	d.nMatch = 0
	if at >= 0 && !bytes.ContainsAny(t[at:caret], " \n") {
		prefix := strings.ToLower(string(t[at+1 : caret]))
		for i, n := range names {
			if strings.HasPrefix(strings.ToLower(n), prefix) {
				d.matches[d.nMatch] = i
				d.nMatch++
			}
		}
	}
	if d.nMatch == 0 {
		d.close()
		return
	}
	if !d.popup {
		d.row = 0
	}
	d.row = min(d.row, d.nMatch-1)
	d.popup, d.trigger = true, platform.TextRange{Start: at, End: caret}
	d.ed.SetCompletionActive(true)
}

func (d *demo) render(path string) error {
	o, err := app.NewOffscreen(config(), winW*2, winH*2, 2, d)
	if err != nil {
		return err
	}
	defer o.Close()
	img, _ := o.Frame()
	for i := 0; i < 4; i++ {
		img, _ = o.Frame()
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// benchDemo draws frames with the editor focused (it takes the keyboard at
// the first frame): the steady state of a window with a composer.
type benchDemo struct{ *demo }

func (b benchDemo) Script(frame int, in *ui.Input) bool { return true }

func runBench(d *demo, gate bool) error {
	d.focus = true
	r, err := app.RunBench(config(), benchDemo{d}, app.BenchOptions{Warm: 300, Frames: 600, Windows: 3, Timeout: 2 * time.Minute})
	if err != nil {
		return err
	}
	j, _ := json.MarshalIndent(struct {
		app.BenchReport
		EditorFocused bool
	}{r, d.focused}, "", "  ")
	fmt.Println(string(j))
	if gate && (r.Mallocs != 0 || !d.focused) {
		fmt.Fprintf(os.Stderr, "composerdemo: gate: %d allocations in %d measured frames (editor focused: %v)\n", r.Mallocs, r.Frames, d.focused)
		os.Exit(3)
	}
	return nil
}
