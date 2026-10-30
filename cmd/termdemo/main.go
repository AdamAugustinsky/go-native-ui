//go:build darwin || linux

// Command termdemo is a terminal window (phase 8 C1): rexui.Terminal over
// libghostty-vt, running a local program on a pseudo-terminal, in one Rex
// pane (rexui.BeginCard, CardRex) with the program's title and the grid's
// size, on Rex's field over the window's glass (docs/guide/rex.md).
//
//	termdemo                      your login shell
//	termdemo -- vim file.go       a program
//	termdemo -mode dark           light, dark or system (the default)
//	termdemo -transparency=false  macOS: no desktop through the window's field
//	termdemo -shot dir -name vim -- vim -u DEFAULTS file.go
//	                              no window: run the program for -wait, draw the
//	                              view offscreen in light and dark and write
//	                              dir/<name>-light.png and dir/<name>-dark.png
//
// The program runs in a term/session session, in this process unless a flag
// asks for the session daemon, which termdemo launches from its own
// executable (-sessiond) when none is running:
//
//	termdemo -daemon              the program runs in the daemon
//	termdemo -persist             …and outlives the window; it prints the session's ID
//	termdemo -attach ID           a window on a daemon session again (its output replays)
//	termdemo -sessions            list the daemon's sessions
//	termdemo -kill ID             end one
//	termdemo -shutdown            end them all, and the daemon
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
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/term"
	"gonativeui/term/session"
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
	title string
	size  string // the grid's size, for the header

	transparency bool
	win          *app.Win // nil offscreen
	focus        bool

	sess session.Service
	att  session.Attachment
	out  *rexui.TerminalInbox // the program's output: Write blocks while the view is behind
	buf  []byte

	daemon  bool   // the session runs in the daemon
	persist bool   // and outlives the window
	attach  string // attach to this daemon session instead of starting one
	dir     string // the daemon's directory

	// -shot
	shot  bool
	keys  []byte
	keyAt int
	total int
}

func main() {
	mode := flag.String("mode", "system", "system, light or dark")
	transparency := flag.Bool("transparency", true, "macOS: show the desktop softly through the window's field")
	shot := flag.String("shot", "", "write <dir>/<name>-light.png and -dark.png of the program offscreen, no window")
	name := flag.String("name", "term", "with -shot: the file name's stem")
	wait := flag.Duration("wait", 2500*time.Millisecond, "with -shot: how long the program runs before the picture")
	keys := flag.String("keys", "", "with -shot: text typed into the program halfway through (Go escapes: \\r, \\x1b)")
	size := flag.String("size", "1440x900", "with -shot: the picture's size in pixels (at scale 2)")
	daemon := flag.Bool("daemon", false, "run the program in the session daemon, launched if it is not running")
	persist := flag.Bool("persist", false, "the program outlives the window (implies -daemon)")
	attach := flag.String("attach", "", "a window on this daemon session")
	list := flag.Bool("sessions", false, "list the daemon's sessions")
	kill := flag.String("kill", "", "end this daemon session")
	shutdown := flag.Bool("shutdown", false, "end the daemon and its sessions")
	sessiond := flag.Bool("sessiond", false, "be the session daemon (what -daemon launches)")
	dir := flag.String("session-dir", session.DefaultDir("termdemo"), "the session daemon's directory")
	flag.Parse()
	if *sessiond {
		if err := session.RunDaemon(*dir, session.ServeOptions{IdleExit: time.Minute}); err != nil {
			fmt.Fprintln(os.Stderr, "termdemo:", err)
			os.Exit(1)
		}
		return
	}
	if *list || *kill != "" || *shutdown {
		if err := control(*dir, *list, *kill, *shutdown); err != nil {
			fmt.Fprintln(os.Stderr, "termdemo:", err)
			os.Exit(1)
		}
		return
	}
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
	d := &demo{argv: argv, transparency: *transparency, persist: *persist, attach: *attach, dir: *dir}
	d.daemon = *daemon || *persist || *attach != ""
	switch *mode {
	case "light":
		d.mode = theme.ForceLight
	case "dark":
		d.mode = theme.ForceDark
	}
	err := app.Run(app.Config{Title: "Terminal", Width: 820, Height: 520, MinWidth: 320, Backdrop: platform.BackdropGlass}, d)
	d.close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "termdemo:", err)
		os.Exit(1)
	}
}

// control runs -sessions, -kill and -shutdown against a running daemon.
func control(dir string, list bool, kill string, shutdown bool) error {
	c, err := session.Dial(dir)
	if err != nil {
		return fmt.Errorf("no session daemon at %s: %w", dir, err)
	}
	defer c.Close()
	switch {
	case list:
		l, err := c.List()
		if err != nil {
			return err
		}
		for _, in := range l {
			state := "detached"
			switch {
			case in.Exited:
				state = fmt.Sprintf("exited %d", in.ExitCode)
			case in.Attached:
				state = "attached"
			}
			fmt.Printf("%s\t%d\t%s\t%s\n", in.ID, in.Pid, state, strings.Join(in.Argv, " "))
		}
		return nil
	case kill != "":
		return c.Kill(kill)
	}
	return c.Shutdown()
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
	return d.start()
}

// start runs the program in a session, or finds the session to attach to,
// and attaches the terminal to it.
func (d *demo) start() error {
	cols, rows := d.s.T.Size()
	sz := session.Size{Cols: cols, Rows: rows}
	if d.daemon {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		d.sess, err = session.DialOrLaunch(d.dir, []string{exe, "-sessiond", "-session-dir", d.dir}, 5*time.Second)
		if err != nil {
			return err
		}
	} else {
		d.sess = session.NewLocal(session.Config{})
	}
	id := d.attach
	if id == "" {
		in, err := d.sess.Create(session.Options{Argv: d.argv, Size: sz, Persist: d.persist,
			Env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=termdemo"}})
		if err != nil {
			return err
		}
		id = in.ID
		if d.persist {
			fmt.Fprintf(os.Stderr, "termdemo: session %s; termdemo -attach %s opens it again\n", id, id)
		}
	} else if l, err := d.sess.List(); err == nil {
		for _, in := range l {
			if in.ID == id {
				d.argv = in.Argv
			}
		}
	}
	var wake func()
	if d.win != nil {
		wake = d.win.Wake
	}
	d.out = d.s.OpenInbox(rexui.TerminalInboxOpts{Wake: wake})
	att, err := d.sess.Attach(id, d.out, sz)
	if err != nil {
		return err
	}
	d.att = att
	if d.win != nil {
		go func() { <-att.Done(); d.win.Wake() }()
	}
	return nil
}

// close ends the window's hold on the session: a persistent one goes on in
// the daemon, any other is hung up.
func (d *demo) close() {
	if d.out != nil {
		d.out.Close() // first: a Write blocked on a full inbox returns, so the attachment can stop
	}
	if d.att != nil {
		d.att.Close()
	}
	if d.sess != nil {
		d.sess.Close()
	}
}

// Build implements app.Builder.
func (d *demo) Build(c *ui.Context) {
	k := d.k
	exited := d.take()
	k.Begin(c)
	// Rex's field shows the desktop through the window's material unless
	// the user turned transparency off (or the system's Reduce Transparency is on).
	k.Theme().SetBackdrop(d.transparency && d.a.Win != nil && d.a.Win.Backdrop() != platform.BackdropNone)
	k.BeginWindow()

	// One Rex pane: a header with the program's title and the grid's size,
	// and the terminal in the room left.
	focus := c.AnimateValue(ui.HashString(0, "pane focus"), boolTo1(c.Input().Focused), 20)
	cd := rexui.BeginCard(k.Kit, "pane", rexui.CardOpts{Variant: rexui.CardRex, Focus: focus})
	k.Row("header")
	k.NextWidth(ui.Grow(1)) // the title takes what the size leaves, and fades if longer
	rexui.Label(k.Kit, d.heading(), rexui.LabelOpts{Style: rexui.TextHeadline})
	rexui.Label(k.Kit, d.size, rexui.LabelOpts{Color: rexui.Tint{Role: theme.Label2}, MonoPt: 11})
	k.EndRow()
	o := rexui.TerminalOpts{AllowClipboard: true, OptionAsAlt: true, Focus: focus}
	if d.shot {
		o.FeedBudget = time.Second // a picture: parse everything each frame
	}
	k.NextSize(ui.Pct(1), ui.Grow(1))
	sig := rexui.Terminal(k.Kit, "terminal", d.s, o)
	rexui.EndCard(k.Kit, &cd)
	if !d.focus {
		k.Focus(sig.Key)
		d.focus = true
	}
	k.EndWindow()
	k.End()
	if sig.Resized {
		cw, ch := d.s.CellSize()
		d.att.Resize(session.Size{Cols: sig.Cols, Rows: sig.Rows, Width: int(cw) * sig.Cols, Height: int(ch) * sig.Rows})
		d.size = fmt.Sprintf("%d × %d", sig.Cols, sig.Rows)
	}
	if len(sig.Link) > 0 {
		openLink(string(sig.Link))
	}
	if sig.Input {
		d.buf = d.s.TakeInput(d.buf[:0])
		d.att.Write(d.buf)
	}
	if t := d.s.T.Title(); t != d.title && d.win != nil {
		d.title = t
		d.win.SetTitle(t)
	}
	if exited && !d.shot && d.s.Pending() == 0 {
		d.a.Quit()
	}
}

// heading is the pane's title: the program's (OSC 0/2), else its name.
func (d *demo) heading() string {
	if t := d.s.T.Title(); t != "" {
		return t
	}
	return filepath.Base(d.argv[0])
}

func boolTo1(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// take reports whether the program has exited (its last output is in the
// inbox by then; the terminal parses it from there).
func (d *demo) take() (exited bool) {
	select {
	case <-d.att.Done():
		return true
	default:
		return false
	}
}

// Script implements app.Scripter for -shot: frames are spaced in real time
// so the program runs, and the keys go in halfway.
func (d *demo) Script(frame int, in *ui.Input) bool {
	in.Inside, in.Focused = true, true
	time.Sleep(16 * time.Millisecond)
	if frame == d.total/2 && len(d.keys) > 0 {
		d.att.Write(d.keys)
	}
	return frame < d.total
}

// shoot runs argv for wait and writes the view offscreen as a PNG.
func shoot(argv []string, pref theme.Preference, path string, w, h int, wait time.Duration, keys []byte) error {
	d := &demo{argv: argv, mode: pref, shot: true, keys: keys, total: int(wait / (16 * time.Millisecond))}
	defer d.close()
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
