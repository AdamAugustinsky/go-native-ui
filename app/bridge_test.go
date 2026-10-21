//go:build !gnui_release

package app_test

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/examples/notes/notes"
	"gonativeui/internal/inspectwire"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

// bridgeRig is the notes example in an offscreen window with the agent bridge
// on a socket in a directory of its own. The test's goroutine owns the window:
// call serves the bridge's requests from it, a frame at a time, as the loop of
// a running app does between frames.
type bridgeRig struct {
	t    *testing.T
	o    *app.Offscreen
	sock string
	dir  string
	cl   *inspectwire.Client
}

func newBridgeRig(t *testing.T) *bridgeRig {
	t.Helper()
	n, err := notes.New()
	if err != nil {
		t.Fatal(err)
	}
	n.Traffic = true // an offscreen window has no system traffic lights
	cfg := notes.Config()
	return newBridgeRigFor(t, n, cfg)
}

// newBridgeRigFor is newBridgeRig for another application.
func newBridgeRigFor(t *testing.T, b app.Builder, cfg app.Config) *bridgeRig {
	t.Helper()
	o, err := app.NewOffscreen(cfg, int(cfg.Width), int(cfg.Height), 1, b)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("offscreen rendering on CI: %v", err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	t.Cleanup(o.Close)
	dir, err := os.MkdirTemp("", "gi")
	if err != nil {
		t.Fatal(err)
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	r := &bridgeRig{t: t, o: o, dir: dir, sock: filepath.Join(dir, "s")}
	for i := 0; i < 4; i++ {
		o.Frame()
	}
	stop, err := o.StartBridge(r.sock)
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skipf("no agent bridge here: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if r.cl, err = inspectwire.Dial(r.sock, time.Second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.cl.Close() })
	return r
}

// call sends the request and serves the bridge until the answer is in.
func (r *bridgeRig) call(req inspectwire.Request) *inspectwire.Response {
	r.t.Helper()
	type res struct {
		r   *inspectwire.Response
		err error
	}
	ch := make(chan res, 1)
	go func() { resp, err := r.cl.Call(&req, 20*time.Second); ch <- res{resp, err} }()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case x := <-ch:
			if x.err != nil {
				r.t.Fatal(x.err)
			}
			return x.r
		case <-deadline:
			r.t.Fatalf("no answer to %+v", req)
		default:
		}
		r.o.PumpBridge()
		r.o.Frame()
		time.Sleep(time.Millisecond)
	}
}

func (r *bridgeRig) ok(req inspectwire.Request) *inspectwire.Response {
	r.t.Helper()
	resp := r.call(req)
	if !resp.OK || resp.Error != nil {
		r.t.Fatalf("%+v: %+v", req, resp.Error)
	}
	return resp
}

// find returns the nodes of the window's tree with the role and name.
func (r *bridgeRig) find(role, name string) []inspectwire.Node {
	r.t.Helper()
	resp := r.ok(inspectwire.Request{Op: "tree", Step: inspectwire.Step{Role: role, Name: name}})
	if len(resp.Trees) != 1 {
		r.t.Fatalf("%d trees", len(resp.Trees))
	}
	return resp.Trees[0].Nodes
}

func TestAgentDrivesTheNotesExampleThroughTheSocket(t *testing.T) {
	r := newBridgeRig(t)

	// the windows
	w := r.ok(inspectwire.Request{ID: "w", Op: "windows"})
	if w.ID != "w" || len(w.Windows) != 1 || w.Windows[0].Title != "Notes" || w.Windows[0].Width <= 0 || w.Windows[0].ID == 0 {
		t.Fatalf("windows: %+v", w)
	}

	// the tree: roles, names, values, states and bounds
	tree := r.ok(inspectwire.Request{Op: "tree"}).Trees
	if len(tree) != 1 || tree[0].Window != w.Windows[0].ID || tree[0].Width != w.Windows[0].Width {
		t.Fatalf("tree: %+v", tree)
	}
	nodes := tree[0].Nodes
	if len(nodes) < 10 || nodes[0].Role != "Window" || nodes[0].Parent != -1 {
		t.Fatalf("the tree has %d nodes, root %+v", len(nodes), nodes[0])
	}
	var add *inspectwire.Node
	for i := range nodes {
		n := &nodes[i]
		if n.Bounds[2] <= n.Bounds[0] && n.Role != "Group" && n.Role != "Label" {
			t.Errorf("%s %q has empty bounds %v", n.Role, n.Name, n.Bounds)
		}
		if n.Role == "Button" && n.Name == "Add" {
			add = n
		}
	}
	if add == nil {
		t.Fatal("no Add button in the tree")
	}
	if got := r.find("button", "Add"); len(got) != 1 || got[0].Key != add.Key { // a filter, role in any case
		t.Errorf("filtered tree: %+v", got)
	}
	before := len(r.find("ListItem", ""))

	// type into the field by focusing it by name, check the value in the tree
	resp := r.ok(inspectwire.Request{Op: "do", Steps: []inspectwire.Step{
		{Action: "focus", Role: "TextField", Name: "New note"},
		{Action: "type", Text: "Water the plants"},
	}})
	if resp.Steps != 2 || resp.Frames < 3 {
		t.Errorf("do: %+v", resp)
	}
	f := r.find("TextField", "New note")
	if len(f) != 1 || f[0].Value != "Water the plants" || !contains(f[0].States, "focused") {
		t.Fatalf("the field after typing: %+v", f)
	}

	// click a button by name; the note is added to the list
	r.ok(inspectwire.Request{Op: "do", Step: inspectwire.Step{Action: "click", Role: "Button", Name: "Add"}})
	if got := r.find("ListItem", "Water the plants"); len(got) != 1 {
		t.Fatalf("no row for the new note: %+v", got)
	}
	if after := len(r.find("ListItem", "")); after != before+1 {
		t.Errorf("rows %d, then %d", before, after)
	}
	if f := r.find("TextField", "New note"); len(f) != 1 || f[0].Value != "" {
		t.Errorf("the field was not cleared: %+v", f)
	}

	// a key with modifiers and the second match of a name
	r.ok(inspectwire.Request{Op: "do", Steps: []inspectwire.Step{
		{Action: "click", Role: "ListItem", Nth: 1},
		{Action: "key", Key: "down"},
		{Action: "settle"},
	}})

	// a shot of the current frame
	tmp, err := filepath.EvalSymlinks(t.TempDir()) // the response gives the path with links resolved
	if err != nil {
		t.Fatal(err)
	}
	png1 := filepath.Join(tmp, "shot.png")
	sh := r.ok(inspectwire.Request{Op: "shot", Path: png1}).Shot
	if sh == nil || sh.Path != png1 || sh.Width != w.Windows[0].Width || sh.Height != w.Windows[0].Height {
		t.Fatalf("shot: %+v", sh)
	}
	f1, err := os.Open(png1)
	if err != nil {
		t.Fatal(err)
	}
	defer f1.Close()
	img, err := png.Decode(f1)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != sh.Width || b.Dy() != sh.Height {
		t.Errorf("the PNG is %v, the response says %dx%d", b, sh.Width, sh.Height)
	}
	colours := map[[4]uint32]int{}
	for y := 0; y < img.Bounds().Dy(); y += 7 {
		for x := 0; x < img.Bounds().Dx(); x += 7 {
			cr, cg, cb, ca := img.At(x, y).RGBA()
			colours[[4]uint32{cr, cg, cb, ca}]++
		}
	}
	if len(colours) < 6 {
		t.Errorf("the shot has %d colours: it is blank", len(colours))
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestBridgeErrorsAreStructured(t *testing.T) {
	r := newBridgeRig(t)
	code := func(req inspectwire.Request) (string, *inspectwire.Error) {
		t.Helper()
		resp := r.call(req)
		if resp.OK || resp.Error == nil || resp.Error.Message == "" {
			t.Fatalf("%+v: %+v", req, resp)
		}
		return resp.Error.Code, resp.Error
	}
	step := func(s inspectwire.Step) inspectwire.Request { return inspectwire.Request{Op: "do", Step: s} }

	if c, e := code(step(inspectwire.Step{Action: "click", Role: "Button", Name: "No such button"})); c != inspectwire.ErrStepFailed || e.Step == nil || *e.Step != 0 || !strings.Contains(e.Message, "No such button") || !strings.Contains(e.Message, "Button") {
		t.Errorf("a missing target: %s %+v", c, e)
	}
	e2 := inspectwire.Request{Op: "do", Steps: []inspectwire.Step{{Action: "settle"}, {Action: "focus", Role: "TextField", Name: "nope"}}}
	if c, e := code(e2); c != inspectwire.ErrStepFailed || e.Step == nil || *e.Step != 1 {
		t.Errorf("the second step failed: %s %+v", c, e)
	}
	// the window is usable after a failed do
	r.ok(step(inspectwire.Step{Action: "settle"}))

	for name, req := range map[string]inspectwire.Request{
		"unknown role":    step(inspectwire.Step{Action: "click", Role: "Buton", Name: "Add"}),
		"no role":         step(inspectwire.Step{Action: "click", Name: "Add"}),
		"unknown action":  step(inspectwire.Step{Action: "hover"}),
		"no action":       {Op: "do"},
		"bad key":         step(inspectwire.Step{Action: "key", Key: "cmd+banana"}),
		"empty type":      step(inspectwire.Step{Action: "type"}),
		"scroll no dy":    step(inspectwire.Step{Action: "scroll", Role: "List"}),
		"both forms":      {Op: "do", Step: inspectwire.Step{Action: "settle"}, Steps: []inspectwire.Step{{Action: "settle"}}},
		"tree bad role":   {Op: "tree", Step: inspectwire.Step{Role: "Buton"}},
		"negative nth":    step(inspectwire.Step{Action: "click", Role: "Button", Nth: -1}),
		"too long a wait": {Op: "do", Step: inspectwire.Step{Action: "settle"}, TimeoutMs: 10 * 60 * 1000},
	} {
		if c, _ := code(req); c != inspectwire.ErrBadRequest {
			t.Errorf("%s: %s, want bad_request", name, c)
		}
	}
	if c, _ := code(inspectwire.Request{Op: "frobnicate"}); c != inspectwire.ErrUnknownOp {
		t.Errorf("unknown op: %s", c)
	}
	if c, _ := code(inspectwire.Request{Op: "tree", Window: 99}); c != inspectwire.ErrNoWindow {
		t.Errorf("unknown window: %s", c)
	}
	if c, _ := code(inspectwire.Request{Op: "shot", Window: 99, Path: filepath.Join(t.TempDir(), "x.png")}); c != inspectwire.ErrNoWindow {
		t.Errorf("shot of an unknown window: %s", c)
	}

	// shot paths: relative, and inside the socket's directory tree
	out := t.TempDir()
	for name, p := range map[string]string{
		"relative":          "shot.png",
		"in the socket dir": filepath.Join(r.dir, "shot.png"),
		"below it":          filepath.Join(r.dir, "x", "..", "shot.png"),
		"missing dir":       filepath.Join(out, "nope", "shot.png"),
	} {
		if c, _ := code(inspectwire.Request{Op: "shot", Path: p}); c != inspectwire.ErrBadPath {
			t.Errorf("shot path %s: %s, want bad_path", name, c)
		}
	}
	if _, err := os.Stat(filepath.Join(r.dir, "shot.png")); err == nil {
		t.Error("a shot was written into the socket's directory")
	}
	// the socket's directory under another spelling of its name (macOS's volumes
	// do not tell the cases apart), and through a link
	if alias := filepath.Join(filepath.Dir(r.dir), strings.ToUpper(filepath.Base(r.dir))); alias != r.dir {
		if _, err := os.Stat(alias); err == nil {
			if c, _ := code(inspectwire.Request{Op: "shot", Path: filepath.Join(alias, "shot.png")}); c != inspectwire.ErrBadPath {
				t.Errorf("a case alias of the socket's directory: %s, want bad_path", c)
			}
		} else {
			t.Log("the volume is case-sensitive: no alias to try")
		}
	}
	link := filepath.Join(out, "link")
	if err := os.Symlink(r.dir, link); err == nil {
		if c, _ := code(inspectwire.Request{Op: "shot", Path: filepath.Join(link, "shot.png")}); c != inspectwire.ErrBadPath {
			t.Errorf("a link to the socket's directory: %s, want bad_path", c)
		}
	}
}

// A shot replaces a file, or a link at its path, without following the link.
func TestShotDoesNotFollowALink(t *testing.T) {
	r := newBridgeRig(t)
	out := t.TempDir()
	victim := filepath.Join(out, "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(out, "shot.png")
	if err := os.Symlink(victim, path); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	r.ok(inspectwire.Request{Op: "shot", Path: path})
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Errorf("the shot was written through the link: %q", b)
	}
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("the link was not replaced by the file: %v %v", fi, err)
	}
}

// With the bridge on and no client, an idle window draws nothing and a steady
// frame allocates nothing.
func TestIdleBridgeDrawsNothingAndAllocatesNothing(t *testing.T) {
	n, err := notes.New()
	if err != nil {
		t.Fatal(err)
	}
	cfg := notes.Config()
	o, err := app.NewOffscreen(cfg, int(cfg.Width), int(cfg.Height), 1, n)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("offscreen rendering on CI: %v", err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	defer o.Close()
	dir, err := os.MkdirTemp("", "gi")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	stop, err := o.StartBridge(filepath.Join(dir, "s"))
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skipf("no agent bridge here: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	idle := 0
	for i := 0; i < 600 && idle < 5; i++ {
		o.PumpBridge()
		if o.Step(16e6) {
			idle = 0
		} else {
			idle++
		}
	}
	if idle < 5 {
		t.Fatal("the window never went idle")
	}
	for i := 0; i < 200; i++ {
		o.PumpBridge()
		if o.Step(16e6) {
			t.Fatalf("an idle window with the bridge on drew a frame (turn %d)", i)
		}
	}
	for i := 0; i < 5; i++ {
		o.Frame() // warm the steady frame
	}
	if a := testing.AllocsPerRun(100, func() { o.PumpBridge(); o.Frame() }); a != 0 {
		t.Errorf("a steady frame with the bridge on allocates %v times", a)
	}
}

// bigField is an application with one text field that holds a lot.
type bigField struct {
	k   *widget.Kit
	buf ui.TextBuffer
}

func (b *bigField) Init(a *app.App) error {
	b.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	b.buf.Reserve(16384)
	return nil
}

func (b *bigField) Build(c *ui.Context) {
	b.k.Begin(c)
	b.k.BeginWindow()
	b.k.TextField("Big", &b.buf, "")
	b.k.EndWindow()
	b.k.End()
}

// Typing is not cut at one frame's text buffer (ui.MaxText, 1024 bytes) and
// reported as done: all of it arrives, over as many frames as it takes, and
// never cut inside a character.
func TestTypingLongerThanOneFrameArrivesInFull(t *testing.T) {
	for name, text := range map[string]string{
		"ascii":   strings.Repeat("0123456789", 200),
		"astride": strings.Repeat("a", ui.MaxText-1) + "日本語" + strings.Repeat("é", 600),
	} {
		t.Run(name, func(t *testing.T) {
			r := newBridgeRigFor(t, &bigField{}, app.Config{Title: "big", Width: 400, Height: 200, Clear: [4]float32{1, 1, 1, 1}})
			resp := r.ok(inspectwire.Request{Op: "do", Steps: []inspectwire.Step{
				{Action: "focus", Role: "TextField", Name: "Big"},
				{Action: "type", Text: text},
			}})
			if resp.Frames < 5 {
				t.Errorf("%d bytes typed in %d frames", len(text), resp.Frames)
			}
			f := r.find("TextField", "Big")
			if len(f) != 1 || f[0].Value != text {
				got := ""
				if len(f) == 1 {
					got = f[0].Value
				}
				t.Fatalf("the field holds %d bytes of the %d typed", len(got), len(text))
			}
		})
	}
}
