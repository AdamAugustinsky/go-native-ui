package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gonativeui/internal/inspectwire"
)

const inspectUsage = `usage: gnui inspect [-json] [-timeout 30s] <socket> <command> [arguments]

Drives and inspects a running development build that was started with
GNUI_INSPECT=<socket> (see docs/GUIDE.md, "Driving an app through the bridge").

commands:
  windows                              list the windows
  tree  [-window N] [-role R] [-name S]   the semantic tree of one or every window
  do [-window N] [-nth I] [-n COUNT] [-ms WAIT] <action> [arguments]
                                       play a step and wait for the next frame; actions:
      click|double_click|right_click|move|focus|wait_for|wait_gone <role> [name]
      type <text>
      key <keys>                       "enter", "cmd+shift+n", "primary+z", "f5"
      scroll <role> <name> <dy>        pixels; positive scrolls toward the start
      settle
  do [-window N] -steps '<json>'       a list of steps, from an argument or "-" (standard input)
  shot [-window N] <file.png>          render the window's current frame into a PNG

-json prints the bridge's response as it came; otherwise the output is text.
`

// cmdInspect is gnui inspect: one request to the agent bridge's socket.
func cmdInspect(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("inspect", stderr)
	asJSON := fs.Bool("json", false, "print the response as JSON")
	timeout := fs.Duration("timeout", 30*time.Second, "how long a step or shot may take")
	fs.Usage = func() { fmt.Fprint(stderr, inspectUsage) }
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		fs.Usage()
		return usageError{errors.New("need a socket and a command")}
	}
	sock, cmd, rest := fs.Arg(0), fs.Arg(1), fs.Args()[2:]
	req, err := inspectRequest(cmd, rest, *timeout, stderr)
	if err != nil {
		return err
	}
	c, err := inspectwire.Dial(sock, 3*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	resp, err := c.Call(req, *timeout+10*time.Second)
	if err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.Marshal(resp)
		fmt.Fprintln(stdout, string(b))
	} else if resp.OK {
		printInspect(stdout, req, resp)
	}
	if !resp.OK {
		if resp.Error == nil {
			return errors.New("the bridge failed without saying why")
		}
		return inspectError{resp.Error}
	}
	return nil
}

// inspectError is a failure the bridge reported.
type inspectError struct{ e *inspectwire.Error }

func (e inspectError) Error() string {
	if e.e.Step != nil {
		return fmt.Sprintf("%s (step %d): %s", e.e.Code, *e.e.Step, e.e.Message)
	}
	return e.e.Code + ": " + e.e.Message
}

// inspectRequest builds the request of a command line.
func inspectRequest(cmd string, args []string, timeout time.Duration, stderr io.Writer) (*inspectwire.Request, error) {
	req := &inspectwire.Request{ID: "1", Op: cmd}
	fs := newFlagSet("inspect "+cmd, stderr)
	window := fs.Uint("window", 0, "the window's id (from windows)")
	var role, name, steps string
	var nth, n int
	var ms int64
	switch cmd {
	case "windows":
	case "tree":
		fs.StringVar(&role, "role", "", "only nodes with this role")
		fs.StringVar(&name, "name", "", "only nodes with this name")
	case "do":
		fs.IntVar(&nth, "nth", 0, "the n-th match, from 0")
		fs.IntVar(&n, "n", 0, "repeat count of key and scroll")
		fs.Int64Var(&ms, "ms", 0, "how long a wait lasts at most")
		fs.StringVar(&steps, "steps", "", "a JSON list of steps, or - for standard input")
	case "shot":
	default:
		return nil, usageError{fmt.Errorf("unknown command %q; use windows, tree, do or shot", cmd)}
	}
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return nil, err
	}
	req.Window = uint32(*window)
	switch cmd {
	case "windows":
		if len(pos) != 0 {
			return nil, usageError{errors.New("windows takes no arguments")}
		}
	case "tree":
		req.Role, req.Name = role, name
	case "shot":
		if len(pos) != 1 {
			return nil, usageError{errors.New("shot needs the file to write: gnui inspect <socket> shot out.png")}
		}
		abs, err := filepath.Abs(pos[0])
		if err != nil {
			return nil, err
		}
		req.Path = abs
	case "do":
		req.TimeoutMs = int(timeout / time.Millisecond)
		if steps != "" {
			if len(pos) != 0 {
				return nil, usageError{errors.New("-steps takes the place of the action")}
			}
			var b []byte
			if steps == "-" {
				if b, err = io.ReadAll(io.LimitReader(os.Stdin, inspectwire.MaxLine)); err != nil {
					return nil, err
				}
			} else {
				b = []byte(steps)
			}
			if err := json.Unmarshal(b, &req.Steps); err != nil {
				return nil, usageError{fmt.Errorf("-steps: %w", err)}
			}
			break
		}
		if len(pos) == 0 {
			return nil, usageError{errors.New("do needs an action (click, type, key …) or -steps")}
		}
		st, err := stepOf(pos, nth, n, ms)
		if err != nil {
			return nil, usageError{err}
		}
		req.Step = st
	}
	return req, nil
}

// stepOf reads a step from the words of a command line.
func stepOf(w []string, nth, n int, ms int64) (inspectwire.Step, error) {
	s := inspectwire.Step{Action: strings.ReplaceAll(w[0], "-", "_"), Nth: nth, N: n, Ms: ms}
	args := w[1:]
	need := func(k int, what string) error {
		if len(args) < k {
			return fmt.Errorf("%s needs %s", s.Action, what)
		}
		if len(args) > k {
			return fmt.Errorf("%s: unexpected %q after %s (quote a name with spaces)", s.Action, args[k], what)
		}
		return nil
	}
	switch s.Action {
	case inspectwire.ActClick, inspectwire.ActDoubleClick, inspectwire.ActRightClick, inspectwire.ActMove,
		inspectwire.ActFocus, inspectwire.ActWaitFor, inspectwire.ActWaitGone:
		if len(args) == 1 {
			args = append(args, "")
		}
		if err := need(2, "a role and a name"); err != nil {
			return s, err
		}
		s.Role, s.Name = args[0], args[1]
	case inspectwire.ActType:
		if err := need(1, "the text to type"); err != nil {
			return s, err
		}
		s.Text = args[0]
	case inspectwire.ActKey:
		if err := need(1, "a key, like enter or cmd+shift+n"); err != nil {
			return s, err
		}
		s.Key = args[0]
	case inspectwire.ActScroll:
		if err := need(3, "a role, a name and dy"); err != nil {
			return s, err
		}
		dy, err := strconv.ParseFloat(args[2], 32)
		if err != nil {
			return s, fmt.Errorf("scroll: dy %q is not a number", args[2])
		}
		s.Role, s.Name, s.Dy = args[0], args[1], float32(dy)
	case inspectwire.ActSettle:
		if err := need(0, "nothing"); err != nil {
			return s, err
		}
	default:
		return s, fmt.Errorf("unknown action %q; use click, double_click, right_click, move, focus, type, key, scroll, wait_for, wait_gone or settle", w[0])
	}
	return s, nil
}

// parseInterleaved parses fs's flags wherever they stand among the arguments
// and returns the others, so that "do click Button Add -nth 1" works. A
// negative number is an argument, not a flag ("scroll List "" -120"), and so is
// everything after "--".
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 {
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		if _, err := strconv.ParseFloat(args[0], 32); err == nil {
			pos, args = append(pos, args[0]), args[1:]
			continue
		}
		if err := parseFlags(fs, args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) > 0 && args[0] != "--" && !isNumber(args[0]) {
			pos, args = append(pos, args[0]), args[1:]
		}
	}
	return pos, nil
}

func isNumber(s string) bool { _, err := strconv.ParseFloat(s, 32); return err == nil }

// printInspect writes a response as text an agent can read.
func printInspect(w io.Writer, req *inspectwire.Request, r *inspectwire.Response) {
	switch req.Op {
	case inspectwire.OpWindows:
		for _, x := range r.Windows {
			fmt.Fprintf(w, "window %d %q %dx%d px at %gx, frame %d\n", x.ID, x.Title, x.Width, x.Height, x.Scale, x.Frame)
		}
		if len(r.Windows) == 0 {
			fmt.Fprintln(w, "no windows")
		}
	case inspectwire.OpTree:
		for _, t := range r.Trees {
			fmt.Fprintf(w, "window %d %dx%d px at %gx\n", t.Window, t.Width, t.Height, t.Scale)
			printTree(w, &t)
			if t.Dropped > 0 {
				fmt.Fprintf(w, "(%d more nodes did not fit)\n", t.Dropped)
			}
		}
	case inspectwire.OpDo:
		fmt.Fprintf(w, "ok: %d step(s) applied in %d frame(s)\n", r.Steps, r.Frames)
	case inspectwire.OpShot:
		fmt.Fprintf(w, "wrote %s (%dx%d px at %gx)\n", r.Shot.Path, r.Shot.Width, r.Shot.Height, r.Shot.Scale)
	}
}

// printTree writes the nodes indented by depth, one per line:
// "[id] Role "name" value="…" [states] (x0,y0,x1,y1)".
func printTree(w io.Writer, t *inspectwire.Tree) {
	depth := map[int]int{}
	for _, n := range t.Nodes {
		d := 0
		if pd, ok := depth[n.Parent]; ok {
			d = pd + 1
		}
		depth[n.ID] = d
		var b strings.Builder
		fmt.Fprintf(&b, "%s[%d] %s", strings.Repeat("  ", d), n.ID, n.Role)
		if n.Name != "" {
			fmt.Fprintf(&b, " %q", n.Name)
		}
		if n.Value != "" {
			fmt.Fprintf(&b, " value=%q", n.Value)
		}
		if len(n.States) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(n.States, " "))
		}
		if n.Range != nil {
			fmt.Fprintf(&b, " range=%g..%g at %.3g", n.Range[0], n.Range[1], n.Range[2])
		}
		if n.Pos != nil {
			fmt.Fprintf(&b, " pos=%d/%d", n.Pos[0], n.Pos[1])
		}
		fmt.Fprintf(&b, " (%.0f,%.0f,%.0f,%.0f)", n.Bounds[0], n.Bounds[1], n.Bounds[2], n.Bounds[3])
		fmt.Fprintln(w, b.String())
	}
}
