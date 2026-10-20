//go:build linux

package platform

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math/rand"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Images, file lists and the concealed hint on the Linux clipboard
// (clipboard_linux.go and the two backends).

func TestPickKind(t *testing.T) {
	only := func(ks ...PasteKind) func(PasteKind) bool {
		return func(k PasteKind) bool { return slices.Contains(ks, k) }
	}
	for _, c := range []struct {
		kinds  PasteKinds
		offers func(PasteKind) bool
		want   PasteKind
		ok     bool
	}{
		{WantText | WantImage | WantFiles, only(PasteText, PasteImage, PasteFiles), PasteFiles, true},
		{WantText | WantImage, only(PasteText, PasteImage, PasteFiles), PasteImage, true},
		{WantText | WantImage | WantFiles, only(PasteText, PasteImage), PasteImage, true},
		{WantText | WantImage | WantFiles, only(PasteText), PasteText, true},
		{WantImage, only(PasteText), 0, false},
		{WantFiles | WantText, only(PasteImage), 0, false},
		{0, only(PasteText), 0, false},
	} {
		if k, ok := pickKind(c.kinds, c.offers); k != c.want || ok != c.ok {
			t.Errorf("kinds %b: got %v %v, want %v %v", c.kinds, k, ok, c.want, c.ok)
		}
	}
}

// kevents is the pastes a window delivered, with their data copied.
type kpaste struct {
	Event
	data []byte
}

func collectPastes(w *Window, evs []Event, into *[]kpaste) {
	for _, e := range evs {
		if e.Kind == EvPaste {
			*into = append(*into, kpaste{e, bytes.Clone(w.Text(e.Text))})
		}
	}
}

// pasteKinds requests a paste of kinds and waits for its answer.
func pasteKinds(t *testing.T, w *Window, tag uint64, kinds PasteKinds, d time.Duration) (kpaste, bool) {
	t.Helper()
	w.RequestPasteKinds(tag, kinds)
	var got []kpaste
	ok := pump(t, w, d, func(e Event) bool {
		if e.Kind == EvPaste && e.Tag == tag {
			got = append(got, kpaste{e, bytes.Clone(w.Text(e.Text))})
			return true
		}
		return false
	})
	if len(got) == 0 {
		return kpaste{}, false
	}
	return got[0], ok
}

// ---- own copies: what the clipboard we own says about itself ----

// Pasting from our own copy needs no display server.
func TestPasteFromOwnCopy(t *testing.T) {
	w := new(Window)
	w.clip.init()
	src, _ := testPNG(t, 20, 10)
	if err := SetClipboardImage(src); err != ErrNoClipboard {
		t.Fatalf("without a window: %v", err)
	}
	w.open = true
	w.wl = true // no display: a Wayland window with no data device only keeps its copy
	w.clipSetData(src, true, true)
	for _, c := range []struct {
		kinds PasteKinds
		fail  bool
	}{{WantImage, false}, {WantImage | WantText | WantFiles, false}, {WantText, true}, {WantFiles, true}} {
		w.q.reset()
		w.clip.inWait = true
		w.RequestPasteKinds(5, c.kinds)
		if len(w.q.ev) != 1 {
			t.Fatalf("kinds %b: events %+v", c.kinds, w.q.ev)
		}
		e := w.q.ev[0]
		if (e.Flags&FlagFailed != 0) != c.fail || (!c.fail && (e.Paste != PasteImage || !bytes.Equal(w.q.textOf(e.Text), src))) {
			t.Errorf("kinds %b: %+v", c.kinds, e)
		}
	}
	// What an image copy offers: the image, not text, and the hint when concealed.
	if d := w.clip.clipData(clipTText); d != nil {
		t.Errorf("an image copy offers text %q", d)
	}
	if got := w.clip.clipData(clipTHint); string(got) != "secret" {
		t.Errorf("hint = %q, want secret", got)
	}
	w.clipSetData(src, true, false)
	if w.clip.clipData(clipTHint) != nil {
		t.Error("the hint stays after a plain copy")
	}
	// A text copy answers text only.
	w.clipSetData([]byte("hello"), false, false)
	w.q.reset()
	w.RequestPasteKinds(6, WantImage|WantFiles)
	if e := w.q.ev[0]; e.Flags&FlagFailed == 0 {
		t.Errorf("text as an image: %+v", e)
	}
	w.q.reset()
	w.RequestPasteKinds(7, WantImage|WantText)
	if e := w.q.ev[0]; e.Flags&FlagFailed != 0 || e.Paste != PasteText || string(w.q.textOf(e.Text)) != "hello" {
		t.Errorf("text: %+v", e)
	}
	if w.clip.clipData(clipTImage) != nil {
		t.Error("a text copy offers an image")
	}
}

// Copy an image while a write of the previous one is still in flight: the
// write keeps its bytes.
func TestCopyImageDuringWrite(t *testing.T) {
	w := new(Window)
	w.wl = true
	w.clip.init()
	a, _ := testPNG(t, 64, 64)
	b, _ := testPNG(t, 64, 63)
	w.clipSetData(a, true, false)
	held := w.clip.img[:len(a)/2]
	heldCopy := bytes.Clone(held)
	w.clip.sends[0] = clipSend{data: held, deadline: nanotime() + 1e12}
	w.clip.nsend = 1
	w.clipSetData(b, true, false)
	if !bytes.Equal(held, heldCopy) {
		t.Error("the image of the write in flight was overwritten")
	}
	if !bytes.Equal(w.clip.img, b) {
		t.Error("the new image was not kept")
	}
}

// ---- Wayland, against the scripted compositor ----

func TestWaylandCopyImageToOtherApp(t *testing.T) {
	f, w := focused(t, "")
	src, _ := testPNG(t, 40, 30)
	if err := SetClipboardImage(src, ClipOptions{Concealed: true}); err != nil {
		t.Fatal(err)
	}
	f.wait("REQ wl_data_device.set_selection", 5*time.Second)
	check := func(mime string, want []byte) {
		t.Helper()
		f.do("paste %s", mime)
		if !pumpUntil(t, w, 5*time.Second, func() bool { return f.seen("PASTED") }) {
			t.Fatalf("%s: nothing pasted", mime)
		}
		wantLine := fmt.Sprintf("PASTED %d %s", len(want), fnv32(string(want)))
		if l := f.wait("PASTED", time.Second); l != wantLine {
			t.Errorf("%s: %q, want %q", mime, l, wantLine)
		}
	}
	check("image/png", src)
	check("x-kde-passwordManagerHint", []byte("secret"))
	check("text/plain;charset=utf-8", nil) // an image copy has no text: the pipe is closed empty

	// A plain copy drops the hint, and text copies carry it when concealed.
	SetClipboardTextWith([]byte("pw"), ClipOptions{Concealed: true})
	f.wait("REQ wl_data_device.set_selection", 5*time.Second)
	check("x-kde-passwordManagerHint", []byte("secret"))
	check("text/plain;charset=utf-8", []byte("pw"))
	check("image/png", nil)
	SetClipboardText([]byte("plain"))
	f.wait("REQ wl_data_device.set_selection", 5*time.Second)
	check("x-kde-passwordManagerHint", nil)
}

// ---- Sway: a real compositor and wl-clipboard ----

func TestSwayImageAndFiles(t *testing.T) {
	w := swayWindow(t)
	src, _ := testPNG(t, 33, 17)

	cmd := wlTool(t, "wl-copy", "--type", "image/png")
	cmd.Stdin = bytes.NewReader(src)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if !pumpUntil(t, w, 10*time.Second, func() bool { y := &w.wy; return y.sel >= 0 && y.offers[y.sel].mimes&(1<<wlPNG) != 0 }) {
		t.Fatal("no image offer after wl-copy")
	}
	p, ok := pasteKinds(t, w, 1, WantText|WantImage, 10*time.Second)
	if !ok || p.Flags != 0 || p.Paste != PasteImage || !bytes.Equal(p.data, src) {
		t.Fatalf("image from wl-copy: %+v, %d bytes (want %d)", p.Event, len(p.data), len(src))
	}
	if p, _ := pasteKinds(t, w, 2, WantText|WantFiles, 5*time.Second); p.Flags&FlagFailed == 0 {
		t.Errorf("an image is not text or files: %+v", p.Event)
	}

	cmd = wlTool(t, "wl-copy", "--type", "text/uri-list")
	cmd.Stdin = strings.NewReader("# files\r\nfile:///tmp/a%20b.txt\r\nfile:///tmp/c\u00e9\r\n")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if !pumpUntil(t, w, 10*time.Second, func() bool { y := &w.wy; return y.sel >= 0 && y.offers[y.sel].mimes&(1<<wlURI) != 0 }) {
		t.Fatal("no file offer after wl-copy")
	}
	p, ok = pasteKinds(t, w, 3, WantFiles, 10*time.Second)
	if want := []string{"/tmp/a b.txt", "/tmp/c\u00e9"}; !ok || p.Flags != 0 || p.Paste != PasteFiles || p.Code != 2 || !slices.Equal(PasteFilePaths(p.data), want) {
		t.Fatalf("files from wl-copy: %+v %q", p.Event, PasteFilePaths(p.data))
	}
}

func TestSwayCopyImageToWlPaste(t *testing.T) {
	w := swayWindow(t)
	src, _ := testPNG(t, 50, 40)
	if err := SetClipboardImage(src, ClipOptions{Concealed: true}); err != nil {
		t.Fatal(err)
	}
	read := func(args ...string) []byte {
		t.Helper()
		var out []byte
		var runErr error
		done := make(chan struct{})
		cmd := wlTool(t, "wl-paste", args...)
		go func() {
			time.Sleep(200 * time.Millisecond) // the compositor has the selection by then
			out, runErr = cmd.Output()
			close(done)
		}()
		if !pumpUntil(t, w, 20*time.Second, func() bool {
			select {
			case <-done:
				return true
			default:
				return false
			}
		}) {
			t.Fatal("wl-paste did not finish")
		}
		if runErr != nil {
			t.Fatalf("wl-paste %v: %v", args, runErr)
		}
		return out
	}
	if got := read("--type", "image/png"); !bytes.Equal(got, src) {
		t.Errorf("wl-paste read %d bytes, want the %d of the PNG", len(got), len(src))
	}
	if got := read("--list-types"); !strings.Contains(string(got), "image/png") || !strings.Contains(string(got), "x-kde-passwordManagerHint") || strings.Contains(string(got), "text/plain") {
		t.Errorf("types offered: %q", got)
	}
	if got := read("--type", "x-kde-passwordManagerHint", "--no-newline"); string(got) != "secret" {
		t.Errorf("the hint is %q, want secret", got)
	}
}

// ---- X11 ----

// pasteXclip puts data on the CLIPBOARD under type as xclip, the "other
// application", until the test ends.
func xclipOwn(t *testing.T, typ string, data []byte) *exec.Cmd {
	t.Helper()
	cmd := xclipCmd(t, "-quiet", "-t", typ, "-i")
	cmd.Stdin = bytes.NewReader(data)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGCONT)
		cmd.Process.Kill()
		cmd.Wait()
	})
	return cmd
}

// pasteKindsEventually pastes until the answer is a success that satisfies
// ok (xclip takes a moment to own the selection, and the one before it may
// still own it).
func pasteKindsEventually(t *testing.T, w *Window, kinds PasteKinds, d time.Duration, ok func(kpaste) bool) kpaste {
	t.Helper()
	var p kpaste
	for end, tag := time.Now().Add(d), uint64(1); time.Now().Before(end); tag++ {
		p, _ = pasteKinds(t, w, tag, kinds, 5*time.Second)
		if p.Kind == EvPaste && p.Flags&FlagFailed == 0 && ok(p) {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no matching paste of kinds %b: %+v, %d bytes", kinds, p.Event, len(p.data))
	return p
}

func isKind(k PasteKind) func(kpaste) bool { return func(p kpaste) bool { return p.Paste == k } }

func TestX11ImageAndFilesFromXclip(t *testing.T) {
	w := openEnv(t, "x11")
	src, _ := testPNG(t, 45, 25)
	xclipOwn(t, "image/png", src)
	p := pasteKindsEventually(t, w, WantText|WantImage, 5*time.Second, isKind(PasteImage))
	if p.Paste != PasteImage || !bytes.Equal(p.data, src) {
		t.Errorf("image: kind %v, %d bytes (want %d)", p.Paste, len(p.data), len(src))
	}
	if p, _ := pasteKinds(t, w, 50, WantText|WantFiles, 5*time.Second); p.Flags&FlagFailed == 0 {
		t.Errorf("an image is not text or files: %+v", p.Event)
	}

	// INCR: an image larger than one property.
	big := noisePNG(t, 900, 700)
	if len(big) < 2*x11MaxProp {
		t.Fatalf("the test image is only %d bytes", len(big))
	}
	xclipOwn(t, "image/png", big)
	p = pasteKindsEventually(t, w, WantImage, 10*time.Second, func(p kpaste) bool { return len(p.data) == len(big) })
	if p.Paste != PasteImage || !bytes.Equal(p.data, big) {
		t.Errorf("big image: %d bytes (want %d)", len(p.data), len(big))
	}

	xclipOwn(t, "text/uri-list", []byte("# files\r\nfile:///tmp/a%20b.txt\r\nhttp://example.com/x\r\nfile://localhost/tmp/c\u00e9\r\n"))
	p = pasteKindsEventually(t, w, WantFiles, 5*time.Second, isKind(PasteFiles))
	if want := []string{"/tmp/a b.txt", "/tmp/c\u00e9"}; p.Paste != PasteFiles || p.Code != 2 || !slices.Equal(PasteFilePaths(p.data), want) {
		t.Errorf("files: %v code %d %q", p.Paste, p.Code, PasteFilePaths(p.data))
	}

	// Text still pastes as before, with the new request too.
	xclipOwn(t, "UTF8_STRING", []byte("plain text"))
	p = pasteKindsEventually(t, w, WantText|WantImage|WantFiles, 5*time.Second, isKind(PasteText))
	if p.Paste != PasteText || string(p.data) != "plain text" {
		t.Errorf("text: %v %q", p.Paste, p.data)
	}
}

// noisePNG is a PNG that does not compress: about 3 bytes a pixel.
func noisePNG(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rand.New(rand.NewSource(1)).Read(img.Pix)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestX11CopyImageToXclip(t *testing.T) {
	w := openEnv(t, "x11")
	src, _ := testPNG(t, 38, 22)
	if err := SetClipboardImage(src, ClipOptions{Concealed: true}); err != nil {
		t.Fatal(err)
	}
	read := func(args ...string) []byte {
		t.Helper()
		cmd := xclipCmd(t, append([]string{"-o"}, args...)...)
		var out []byte
		var runErr error
		done := make(chan struct{})
		go func() {
			out, runErr = cmd.Output()
			close(done)
		}()
		if !pumpUntil(t, w, 20*time.Second, func() bool {
			select {
			case <-done:
				return true
			default:
				return false
			}
		}) {
			t.Fatal("xclip -o did not finish")
		}
		if runErr != nil {
			t.Fatalf("xclip -o %v: %v", args, runErr)
		}
		return out
	}
	if got := read("-t", "image/png"); !bytes.Equal(got, src) {
		t.Errorf("xclip read %d bytes, want the %d of the PNG", len(got), len(src))
	}
	targets := string(read("-t", "TARGETS"))
	if !strings.Contains(targets, "image/png") || !strings.Contains(targets, "x-kde-passwordManagerHint") || strings.Contains(targets, "UTF8_STRING") {
		t.Errorf("targets: %q", targets)
	}
	if got := read("-t", "x-kde-passwordManagerHint"); string(got) != "secret" {
		t.Errorf("the hint is %q, want secret", got)
	}

	// A big image goes out in INCR chunks.
	big := noisePNG(t, 900, 700)
	if err := SetClipboardImage(big); err != nil {
		t.Fatal(err)
	}
	if got := read("-t", "image/png"); !bytes.Equal(got, big) {
		t.Errorf("big image: xclip read %d bytes, want %d", len(got), len(big))
	}

	// A plain text copy is text again, without the hint.
	SetClipboardText([]byte("plain"))
	targets = string(read("-t", "TARGETS"))
	if strings.Contains(targets, "image/png") || strings.Contains(targets, "x-kde-passwordManagerHint") || !strings.Contains(targets, "UTF8_STRING") {
		t.Errorf("targets after a text copy: %q", targets)
	}
	SetClipboardTextWith([]byte("pw"), ClipOptions{Concealed: true})
	if got := read("-t", "x-kde-passwordManagerHint"); string(got) != "secret" {
		t.Errorf("a concealed text copy: the hint is %q", got)
	}
}

// ---- X11, against a scripted owner ----

// putAtoms writes a property of 32-bit atoms.
func (p *xpeer) putAtoms(win, prop uintptr, atoms ...uintptr) {
	l := make([]uint64, len(atoms))
	for i, a := range atoms {
		l[i] = uint64(a)
	}
	fr.Reset().Arg(p.dpy).Arg(win).Arg(prop).Arg(xaAtom).Arg(32).Arg(0).Arg(uintptr(unsafe.Pointer(&l[0]))).Arg(uintptr(len(l))).Call(xf.changeProperty)
	fr.Call1(xf.flush, p.dpy)
}

func (s *xsess) kstep(into *[]kpaste) {
	collectPastes(s.w, s.w.Wait(int64(2*time.Millisecond)), into)
	s.p.poll()
}

func (s *xsess) kuntil(what string, into *[]kpaste, match func(e *xev) bool) xev {
	s.t.Helper()
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); s.kstep(into) {
		if e, ok := s.p.take(match); ok {
			return e
		}
	}
	s.t.Fatalf("timed out waiting for %s", what)
	return xev{}
}

// The owner lists its targets, and the requestor asks for the best of the
// requested kinds: files, then image, then text.
func TestX11PasteChoosesFromTargets(t *testing.T) {
	s := newXSess(t)
	s.p.own()
	x := &s.w.x
	src, _ := testPNG(t, 9, 7)
	var got []kpaste

	ask := func(tag uint64, kinds PasteKinds, offers []uintptr, reply map[uintptr][]byte) {
		t.Helper()
		got = got[:0]
		s.w.RequestPasteKinds(tag, kinds)
		req := s.kuntil("the TARGETS request", &got, isType(xSelectionRequest))
		if uintptr(req[7]) != x.aTargets {
			t.Fatalf("asked for target %d first, want TARGETS %d", req[7], x.aTargets)
		}
		s.p.putAtoms(req.reqWin(), req.reqProp(), offers...)
		s.p.notify(req, req.reqProp())
		if len(reply) == 0 { // nothing should be asked of the owner next
			s.kuntil0(&got, func() bool { return len(got) > 0 })
			return
		}
		req = s.kuntil("the data request", &got, isType(xSelectionRequest))
		data, ok := reply[uintptr(req[7])]
		if !ok {
			t.Fatalf("asked for target %d, want one of %v", req[7], reply)
		}
		s.p.putProp(req.reqWin(), req.reqProp(), uintptr(req[7]), data)
		s.p.notify(req, req.reqProp())
		s.kuntil0(&got, func() bool { return len(got) > 0 })
	}

	uris := []byte("file:///tmp/one\r\nfile:///tmp/two%20x\r\n")
	all := []uintptr{x.aTargets, x.aUTF8, x.aPNG, x.aURIList}
	ask(1, WantText|WantImage|WantFiles, all, map[uintptr][]byte{x.aURIList: uris})
	if p := got[0]; p.Tag != 1 || p.Flags != 0 || p.Paste != PasteFiles || p.Code != 2 || string(p.data) != "/tmp/one\x00/tmp/two x\x00" {
		t.Errorf("files: %+v %q", p.Event, p.data)
	}
	ask(2, WantText|WantImage, all, map[uintptr][]byte{x.aPNG: src})
	if p := got[0]; p.Flags != 0 || p.Paste != PasteImage || !bytes.Equal(p.data, src) {
		t.Errorf("image: %+v %d bytes", p.Event, len(p.data))
	}
	ask(3, WantText|WantImage|WantFiles, []uintptr{x.aTargets, x.aUTF8, x.aPlainUTF8}, map[uintptr][]byte{x.aUTF8: []byte("a\r\nb")})
	if p := got[0]; p.Flags != 0 || p.Paste != PasteText || string(p.data) != "a\nb" {
		t.Errorf("text: %+v %q", p.Event, p.data)
	}
	ask(4, WantImage|WantFiles, []uintptr{x.aTargets, x.aUTF8}, nil)
	if p := got[0]; p.Flags != FlagFailed {
		t.Errorf("nothing of the requested kinds: %+v", p.Event)
	}
	// An image that is not a complete PNG fails.
	ask(5, WantImage, all, map[uintptr][]byte{x.aPNG: src[:len(src)-5]})
	if p := got[0]; p.Flags&FlagFailed == 0 {
		t.Errorf("a truncated PNG: %+v", p.Event)
	}
	// Text alone does not ask for TARGETS.
	got = got[:0]
	s.w.RequestPasteKinds(6, WantText)
	req := s.kuntil("the text request", &got, isType(xSelectionRequest))
	if uintptr(req[7]) != x.aUTF8 {
		t.Errorf("a text request asked for target %d, want UTF8_STRING", req[7])
	}
}

func (s *xsess) kuntil0(into *[]kpaste, cond func() bool) {
	s.t.Helper()
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); s.kstep(into) {
		if cond() {
			return
		}
	}
	s.t.Fatal("timed out waiting for the paste")
}

// An image the owner announces as bigger than MaxPaste is refused at once,
// before a byte of it is stored; one that turns out bigger is dropped when the
// limit is passed.
func TestX11PasteImageOverMax(t *testing.T) {
	s := newXSess(t)
	s.p.own()
	x := &s.w.x
	var got []kpaste

	s.w.RequestPasteKinds(1, WantImage)
	req := s.kuntil("TARGETS", &got, isType(xSelectionRequest))
	s.p.putAtoms(req.reqWin(), req.reqProp(), x.aTargets, x.aPNG)
	s.p.notify(req, req.reqProp())
	req = s.kuntil("the image request", &got, isType(xSelectionRequest))
	fr.Call3(xf.selectInput, s.p.dpy, req.reqWin(), xPropertyChangeMask)
	size := uint64(MaxPaste + 1)
	fr.Reset().Arg(s.p.dpy).Arg(req.reqWin()).Arg(req.reqProp()).Arg(x.aIncr).Arg(32).Arg(0).Arg(uintptr(unsafe.Pointer(&size))).Arg(1).Call(xf.changeProperty)
	s.p.notify(req, req.reqProp())
	s.kuntil0(&got, func() bool { return len(got) > 0 })
	if e := got[0]; e.Flags != FlagFailed|FlagTooBig || e.Tag != 1 {
		t.Errorf("announced over MaxPaste: %+v", e.Event)
	}
	if cap(s.w.clip.buf) > 1<<20 {
		t.Errorf("the refused image left a %d-byte buffer", cap(s.w.clip.buf))
	}

	// One that announces little and sends more.
	got = got[:0]
	s.w.RequestPasteKinds(2, WantImage)
	req = s.kuntil("TARGETS", &got, isType(xSelectionRequest))
	s.p.putAtoms(req.reqWin(), req.reqProp(), x.aTargets, x.aPNG)
	s.p.notify(req, req.reqProp())
	req = s.kuntil("the image request", &got, isType(xSelectionRequest))
	s.p.incrAnnounce(req)
	chunk := make([]byte, 1<<20)
	for i := 0; i < MaxPaste/len(chunk)+2 && len(got) == 0; i++ {
		s.kuntil("the requestor to take the chunk", &got, propEvent(req.reqWin(), req.reqProp(), xPropDelete))
		s.p.putProp(req.reqWin(), req.reqProp(), x.aPNG, chunk)
		s.kstep(&got)
	}
	s.kuntil0(&got, func() bool { return len(got) > 0 })
	if e := got[0]; e.Flags != FlagFailed|FlagTooBig || e.Tag != 2 {
		t.Errorf("sent more than MaxPaste: %+v", e.Event)
	}
}

// The paste poll of an image from our own copy allocates nothing in steady
// state; neither does one from another application (xclip).
func TestX11PasteImageNoAlloc(t *testing.T) {
	w := openEnv(t, "x11")
	src, _ := testPNG(t, 64, 64)
	SetClipboardImage(src)
	own := func() {
		w.RequestPasteKinds(1, WantImage|WantText)
		got := false
		for _, e := range w.Wait(0) {
			if e.Kind == EvPaste && e.Paste == PasteImage && int(e.Text.Len) == len(src) {
				got = true
			}
		}
		if !got {
			panic("no paste")
		}
	}
	for i := 0; i < 20; i++ {
		own()
	}
	if n := testing.AllocsPerRun(50, own); n != 0 {
		t.Errorf("own image paste allocates %.1f times per round, want 0", n)
	}
}

func TestX11PasteImageFromOtherAppNoAlloc(t *testing.T) {
	w := openEnv(t, "x11")
	src, _ := testPNG(t, 64, 64)
	xclipOwn(t, "image/png", src)
	pasteKindsEventually(t, w, WantImage, 5*time.Second, isKind(PasteImage))
	step := func() {
		w.RequestPasteKinds(1, WantImage)
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
			for _, e := range w.Wait(int64(time.Millisecond)) {
				if e.Kind == EvPaste {
					if e.Flags != 0 || int(e.Text.Len) != len(src) {
						panic(fmt.Sprintf("paste %+v", e))
					}
					return
				}
			}
		}
		panic("no paste")
	}
	for i := 0; i < 20; i++ {
		step()
	}
	if n := testing.AllocsPerRun(50, step); n != 0 {
		t.Errorf("the paste poll of an image from another application allocates %.1f times per round, want 0", n)
	}
}

// An image the owner sends too much of is passed over for the text it also
// offers.
func TestX11PasteOversizedImageFallsBackToText(t *testing.T) {
	s := newXSess(t)
	s.p.own()
	x := &s.w.x
	var got []kpaste

	s.w.RequestPasteKinds(1, WantImage|WantText)
	req := s.kuntil("TARGETS", &got, isType(xSelectionRequest))
	s.p.putAtoms(req.reqWin(), req.reqProp(), x.aTargets, x.aUTF8, x.aPNG)
	s.p.notify(req, req.reqProp())
	req = s.kuntil("the image request", &got, isType(xSelectionRequest))
	if uintptr(req[7]) != x.aPNG {
		t.Fatalf("asked for target %d, want image/png", req[7])
	}
	fr.Call3(xf.selectInput, s.p.dpy, req.reqWin(), xPropertyChangeMask)
	size := uint64(MaxPaste + 1)
	fr.Reset().Arg(s.p.dpy).Arg(req.reqWin()).Arg(req.reqProp()).Arg(x.aIncr).Arg(32).Arg(0).Arg(uintptr(unsafe.Pointer(&size))).Arg(1).Call(xf.changeProperty)
	s.p.notify(req, req.reqProp())

	// The text is asked for next, without the image getting a failure first.
	req = s.kuntil("the text request", &got, isType(xSelectionRequest))
	if uintptr(req[7]) != x.aUTF8 {
		t.Fatalf("asked for target %d after the image, want UTF8_STRING", req[7])
	}
	if len(got) != 0 {
		t.Fatalf("answered before the text arrived: %+v", got[0].Event)
	}
	s.p.putProp(req.reqWin(), req.reqProp(), x.aUTF8, []byte("alt text"))
	s.p.notify(req, req.reqProp())
	s.kuntil0(&got, func() bool { return len(got) > 0 })
	if e := got[0]; e.Flags != 0 || e.Paste != PasteText || string(e.data) != "alt text" || e.Tag != 1 {
		t.Errorf("answer %+v %q", e.Event, e.data)
	}
}

// The same on Wayland: a files request whose list is over MaxPaste goes on to
// the text, and when that is over MaxPaste too the failure says so.
func TestWaylandPasteOversizedFallsBackToNextKind(t *testing.T) {
	f, w := focused(t, "")
	f.do("ext-copy-big %d", MaxPaste+1024)
	selectionSeen(t, w, true)
	w.RequestPasteKinds(1, WantFiles|WantText)
	var got []kpaste
	for end := time.Now().Add(60 * time.Second); len(got) == 0 && time.Now().Before(end); {
		collectPastes(w, w.Wait(int64(5*time.Millisecond)), &got)
	}
	if !f.seen(`REQ wl_data_offer.receive "text/uri-list"`) || !f.seen(`REQ wl_data_offer.receive "text/plain;charset=utf-8"`) {
		t.Fatalf("the text was not tried after the file list; got %+v", got)
	}
	if len(got) != 1 || got[0].Flags != FlagFailed|FlagTooBig {
		t.Errorf("answer %+v", got)
	}
}
