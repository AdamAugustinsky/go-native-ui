//go:build darwin || linux

// Command termdemo is a terminal window (phase 8 C1): rexui.Terminal over
// libghostty-vt, running a local program on a pseudo-terminal.
//
//	termdemo                      your login shell
//	termdemo -- vim file.go       a program
//	termdemo -mode dark           light, dark or system (the default)
//	termdemo -shot dir -name vim -- vim -u DEFAULTS file.go
//	                              no window: run the program for -wait, draw the
//	                              view offscreen in light and dark and write
//	                              dir/<name>-light.png and dir/<name>-dark.png
//
// The library is looked up beside the executable (Contents/Frameworks in the
// bundle `gnui package` makes from gnui.json), then in GNUI_LIBGHOSTTY_VT;
// `go run ./tools/libghostty` builds it into .cache/libghostty-vt.
package main

import (
	"errors"
	"flag"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"gonativeui/app"
	"gonativeui/rexui"
	"gonativeui/term"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

type demo struct {
	a     *app.App
	k     *widget.Kit
	argv  []string
	mode  theme.Preference
	s     *rexui.TerminalState
	p     *pty
	title string
	win   *app.Win // nil offscreen
	focus bool

	mu     sync.Mutex
	in     []byte // read from the program, for the UI thread
	exited bool
	buf    []byte

	// -shot
	shot  bool
	keys  []byte
	keyAt int
	total int
}

func main() {
	mode := flag.String("mode", "system", "system, light or dark")
	shot := flag.String("shot", "", "write <dir>/<name>-light.png and -dark.png of the program offscreen, no window")
	name := flag.String("name", "term", "with -shot: the file name's stem")
	wait := flag.Duration("wait", 2500*time.Millisecond, "with -shot: how long the program runs before the picture")
	keys := flag.String("keys", "", "with -shot: text typed into the program halfway through (Go escapes: \\r, \\x1b)")
	size := flag.String("size", "1440x900", "with -shot: the picture's size in pixels (at scale 2)")
	flag.Parse()
	argv := flag.Args()
	if len(argv) == 0 {
		sh := os.Getenv("SHELL")
		if sh == "" {
			sh = "/bin/sh"
		}
		argv = []string{sh, "-l"}
	}
	if err := term.Available(); err != nil {
		fmt.Fprintln(os.Stderr, "termdemo:", err)
		fmt.Fprintln(os.Stderr, "termdemo: build the library with `go run ./tools/libghostty` and set", term.EnvLibrary)
		os.Exit(1)
	}
	if *shot != "" {
		var w, h int
		if _, err := fmt.Sscanf(*size, "%dx%d", &w, &h); err != nil {
			fmt.Fprintln(os.Stderr, "termdemo: -size:", err)
			os.Exit(2)
		}
		k, err := unquote(*keys)
		if err != nil {
			fmt.Fprintln(os.Stderr, "termdemo: -keys:", err)
			os.Exit(2)
		}
		for _, m := range [...]struct {
			name string
			pref theme.Preference
		}{{"light", theme.ForceLight}, {"dark", theme.ForceDark}} {
			if err := shoot(argv, m.pref, filepath.Join(*shot, *name+"-"+m.name+".png"), w, h, *wait, k); err != nil {
				fmt.Fprintln(os.Stderr, "termdemo:", err)
				os.Exit(1)
			}
		}
		return
	}
	d := &demo{argv: argv}
	switch *mode {
	case "light":
		d.mode = theme.ForceLight
	case "dark":
		d.mode = theme.ForceDark
	}
	if err := app.Run(app.Config{Title: "Terminal", Width: 820, Height: 520, MinWidth: 200}, d); err != nil {
		fmt.Fprintln(os.Stderr, "termdemo:", err)
		os.Exit(1)
	}
}

// Init implements app.Initer: it makes the terminal and starts the program.
func (d *demo) Init(a *app.App) error {
	d.a = a
	d.win = a.Window()
	d.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	d.k.SetPreference(d.mode)
	s, err := rexui.NewTerminal(term.Options{Scrollback: 5000, OptionAsAlt: true})
	if err != nil {
		return err
	}
	d.s = s
	cols, rows := s.T.Size()
	p, err := startPTY(d.argv, cols, rows)
	if err != nil {
		return err
	}
	d.p = p
	go d.read()
	return nil
}

// read copies the program's output for the UI thread and wakes it.
func (d *demo) read() {
	b := make([]byte, 64<<10)
	for {
		n, err := d.p.f.Read(b)
		d.mu.Lock()
		d.in = append(d.in, b[:n]...)
		if err != nil {
			d.exited = true
		}
		d.mu.Unlock()
		if d.win != nil {
			d.win.Wake()
		}
		if err != nil {
			return
		}
	}
}

// Build implements app.Builder.
func (d *demo) Build(c *ui.Context) {
	k := d.k
	exited := d.take()
	k.Begin(c)
	k.NextPad(0)
	k.BeginWindow()
	o := rexui.TerminalOpts{AllowClipboard: true, OptionAsAlt: true}
	if d.shot {
		o.FeedBudget = time.Second // a picture: parse everything each frame
	}
	sig := rexui.Terminal(k.Kit, "terminal", d.s, o)
	if !d.focus {
		k.Focus(sig.Key)
		d.focus = true
	}
	k.EndWindow()
	k.End()
	if sig.Resized {
		cw, ch := d.s.CellSize()
		d.p.resize(sig.Cols, sig.Rows, int(cw)*sig.Cols, int(ch)*sig.Rows)
	}
	if len(sig.Link) > 0 {
		openLink(string(sig.Link))
	}
	if sig.Input {
		d.buf = d.s.TakeInput(d.buf[:0])
		d.p.f.Write(d.buf)
	}
	if t := d.s.T.Title(); t != d.title && d.win != nil {
		d.title = t
		d.win.SetTitle(t)
	}
	if exited && !d.shot && d.s.Pending() == 0 {
		d.a.Quit()
	}
}

// take feeds what the program wrote since the last frame.
func (d *demo) take() (exited bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.in) > 0 {
		d.s.Feed(d.in)
		d.in = d.in[:0]
	}
	return d.exited
}

// Script implements app.Scripter for -shot: frames are spaced in real time
// so the program runs, and the keys go in halfway.
func (d *demo) Script(frame int, in *ui.Input) bool {
	in.Inside, in.Focused = true, true
	time.Sleep(16 * time.Millisecond)
	if frame == d.total/2 && len(d.keys) > 0 {
		d.p.f.Write(d.keys)
	}
	return frame < d.total
}

// shoot runs argv for wait and writes the view offscreen as a PNG.
func shoot(argv []string, pref theme.Preference, path string, w, h int, wait time.Duration, keys []byte) error {
	d := &demo{argv: argv, mode: pref, shot: true, keys: keys, total: int(wait / (16 * time.Millisecond))}
	defer func() {
		if d.p != nil {
			d.p.close()
		}
	}()
	img, err := app.RenderOffscreen(app.Config{}, w, h, 2, d, d.total+1)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

func openLink(u string) {
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	exec.Command(opener, u).Start()
}

// unquote reads Go escapes in s.
func unquote(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'r':
			b.WriteByte('\r')
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'e':
			b.WriteByte(0x1b)
		case '\\':
			b.WriteByte('\\')
		case 'x':
			if i+2 >= len(s) {
				return nil, errors.New("short \\x escape")
			}
			var v byte
			if _, err := fmt.Sscanf(s[i+1:i+3], "%02x", &v); err != nil {
				return nil, err
			}
			b.WriteByte(v)
			i += 2
		default:
			return nil, fmt.Errorf("unknown escape \\%c", s[i])
		}
	}
	return []byte(b.String()), nil
}
