package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gonativeui/internal/inspectwire"
)

// fakeBridge answers like an app with one window and records the requests.
type fakeBridge struct{ got []inspectwire.Request }

func (f *fakeBridge) Handle(r *inspectwire.Request) *inspectwire.Response {
	f.got = append(f.got, *r)
	switch r.Op {
	case "windows":
		return &inspectwire.Response{OK: true, Windows: []inspectwire.Window{{ID: 1, Title: "Notes", Width: 900, Height: 600, Scale: 1, Frame: 12}}}
	case "tree":
		return &inspectwire.Response{OK: true, Trees: []inspectwire.Tree{{Window: 1, Width: 900, Height: 600, Scale: 1, Nodes: []inspectwire.Node{
			{ID: 0, Parent: -1, Role: "Window", Name: "Notes", Bounds: [4]float32{0, 0, 900, 600}},
			{ID: 1, Parent: 0, Role: "TextField", Name: "New note", Value: "milk", States: []string{"editable", "focused"}, Bounds: [4]float32{10, 20, 200, 44}},
			{ID: 2, Parent: 1, Role: "Button", Name: "Add", Bounds: [4]float32{210, 20, 260, 44}},
		}}}}
	case "do":
		if r.Name == "Missing" {
			i := 0
			r := inspectwire.Fail("", inspectwire.ErrStepFailed, `no node with role Button named "Missing"`)
			r.Error.Step = &i
			return r
		}
		return &inspectwire.Response{OK: true, Steps: max(len(r.Steps), 1), Frames: 4}
	case "shot":
		return &inspectwire.Response{OK: true, Shot: &inspectwire.Shot{Path: r.Path, Width: 900, Height: 600, Scale: 1}}
	}
	return inspectwire.Fail("", inspectwire.ErrUnknownOp, r.Op)
}

func startFake(t *testing.T) (string, *fakeBridge) {
	t.Helper()
	d, err := os.MkdirTemp("", "gi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	sock := filepath.Join(d, "s")
	ln, err := inspectwire.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeBridge{}
	done := make(chan struct{})
	go func() { ln.Serve(f); close(done) }()
	t.Cleanup(func() { ln.Close(); <-done })
	return sock, f
}

func inspect(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(append([]string{"inspect"}, args...), &o, &e)
	return o.String(), e.String(), code
}

func TestInspectPrintsTheTreeForAnAgentToRead(t *testing.T) {
	sock, f := startFake(t)
	out, errs, code := inspect(t, sock, "windows")
	if code != 0 || !strings.Contains(out, `window 1 "Notes" 900x600 px at 1x, frame 12`) {
		t.Fatalf("windows: %d %q %q", code, out, errs)
	}
	out, errs, code = inspect(t, sock, "tree", "-role", "button")
	if code != 0 {
		t.Fatalf("tree: %d %q", code, errs)
	}
	want := `window 1 900x600 px at 1x
[0] Window "Notes" (0,0,900,600)
  [1] TextField "New note" value="milk" [editable focused] (10,20,200,44)
    [2] Button "Add" (210,20,260,44)
`
	if out != want {
		t.Errorf("tree output:\n%s\nwant:\n%s", out, want)
	}
	if last := f.got[len(f.got)-1]; last.Op != "tree" || last.Role != "button" {
		t.Errorf("the filter was not sent: %+v", last)
	}
	out, _, _ = inspect(t, "-json", sock, "windows")
	if !strings.HasPrefix(out, `{"id":"1","ok":true,"windows":[{"id":1,`) || strings.Count(out, "\n") != 1 {
		t.Errorf("-json: %q", out)
	}
}

func TestInspectDoBuildsStepsFromTheCommandLine(t *testing.T) {
	sock, f := startFake(t)
	last := func() inspectwire.Request { return f.got[len(f.got)-1] }
	for _, tc := range []struct {
		args []string
		want inspectwire.Step
	}{
		{[]string{"do", "click", "Button", "Add"}, inspectwire.Step{Action: "click", Role: "Button", Name: "Add"}},
		{[]string{"do", "click", "ListItem", "-nth", "2"}, inspectwire.Step{Action: "click", Role: "ListItem", Nth: 2}},
		{[]string{"do", "double-click", "ListItem", "Garden"}, inspectwire.Step{Action: "double_click", Role: "ListItem", Name: "Garden"}},
		{[]string{"do", "type", "Water the plants"}, inspectwire.Step{Action: "type", Text: "Water the plants"}},
		{[]string{"do", "-n", "3", "key", "cmd+shift+n"}, inspectwire.Step{Action: "key", Key: "cmd+shift+n", N: 3}},
		{[]string{"do", "scroll", "List", "", "-120"}, inspectwire.Step{Action: "scroll", Role: "List", Dy: -120}},
		{[]string{"do", "wait_for", "Dialog", "-ms", "500"}, inspectwire.Step{Action: "wait_for", Role: "Dialog", Ms: 500}},
		{[]string{"do", "settle"}, inspectwire.Step{Action: "settle"}},
		{[]string{"do", "focus", "TextField", "New note"}, inspectwire.Step{Action: "focus", Role: "TextField", Name: "New note"}},
	} {
		out, errs, code := inspect(t, append([]string{sock}, tc.args...)...)
		if code != 0 || !strings.HasPrefix(out, "ok: 1 step(s) applied in 4 frame(s)") {
			t.Fatalf("%v: %d %q %q", tc.args, code, out, errs)
		}
		if got := last().Step; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: step %+v, want %+v", tc.args, got, tc.want)
		}
	}
	out, errs, code := inspect(t, sock, "do", "-steps", `[{"action":"focus","role":"TextField","name":"New note"},{"action":"type","text":"x"}]`)
	if code != 0 || !strings.Contains(out, "2 step(s)") || len(last().Steps) != 2 || last().Step.Action != "" {
		t.Errorf("-steps: %d %q %q %+v", code, out, errs, last())
	}
	if last().TimeoutMs != 30000 {
		t.Errorf("timeout_ms %d", last().TimeoutMs)
	}
}

func TestInspectShotMakesThePathAbsolute(t *testing.T) {
	sock, f := startFake(t)
	out, _, code := inspect(t, sock, "shot", "out.png")
	if code != 0 {
		t.Fatalf("%d %q", code, out)
	}
	wd, _ := os.Getwd()
	if p := f.got[len(f.got)-1].Path; p != filepath.Join(wd, "out.png") {
		t.Errorf("path %q", p)
	}
	if !strings.Contains(out, "wrote "+filepath.Join(wd, "out.png")+" (900x600 px at 1x)") {
		t.Errorf("output %q", out)
	}
}

func TestInspectReportsFailuresAndUsageErrors(t *testing.T) {
	sock, _ := startFake(t)
	_, errs, code := inspect(t, sock, "do", "click", "Button", "Missing")
	if code != 1 || !strings.Contains(errs, "step_failed (step 0): no node with role Button") {
		t.Errorf("a failed step: %d %q", code, errs)
	}
	for name, args := range map[string][]string{
		"no command":       {sock},
		"unknown command":  {sock, "poke"},
		"click, no role":   {sock, "do", "click"},
		"extra word":       {sock, "do", "click", "Button", "Add", "now"},
		"type, no text":    {sock, "do", "type"},
		"scroll, bad dy":   {sock, "do", "scroll", "List", "x", "fast"},
		"unknown action":   {sock, "do", "hover", "Button"},
		"do alone":         {sock, "do"},
		"steps and action": {sock, "do", "-steps", "[]", "settle"},
		"bad steps":        {sock, "do", "-steps", "{"},
		"shot, no file":    {sock, "shot"},
		"windows, extra":   {sock, "windows", "now"},
		"unknown flag":     {sock, "tree", "-colour", "red"},
	} {
		if _, errs, code := inspect(t, args...); code != 2 {
			t.Errorf("%s: exit %d, want 2 (%q)", name, code, errs)
		}
	}
	if _, errs, code := inspect(t, filepath.Join(filepath.Dir(sock), "missing"), "windows"); code != 1 || !strings.Contains(errs, "GNUI_INSPECT") {
		t.Errorf("no app listening: %d %q", code, errs)
	}
}
