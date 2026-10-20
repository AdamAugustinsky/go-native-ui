//go:build windows

package platform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

// Images and files on the Windows clipboard (paste_windows.go,
// clip_worker_windows.go). The clipboard is filled by this process's worker
// (SetClipboardImage) or by another process: a child of the test binary, or
// PowerShell.

// pasteKindsOf requests a paste of kinds and waits for its answer; the data is
// copied (an event's text lives until the next Wait).
func pasteKindsOf(t *testing.T, w *Window, tag uint64, kinds PasteKinds, wait time.Duration) (Event, []byte) {
	t.Helper()
	w.RequestPasteKinds(tag, kinds)
	end := time.Now().Add(wait)
	for {
		for _, e := range w.Wait(int64(time.Until(end))) {
			if e.Kind == EvPaste {
				if e.Tag != tag {
					t.Fatalf("paste answered tag %d, want %d", e.Tag, tag)
				}
				return e, bytes.Clone(w.Text(e.Text))
			}
		}
		if !time.Now().Before(end) {
			t.Fatalf("no EvPaste within %v", wait)
		}
	}
}

// readFormat copies the memory of a clipboard format (nil if it is not there),
// on this thread, inside a session like the worker's.
func readFormat(t *testing.T, format uint32) []byte {
	t.Helper()
	if !openClipboard() {
		t.Fatal("the clipboard stays busy")
	}
	defer closeClipboard()
	h := win32.Call(pGetClipboardData, uintptr(format))
	if h == 0 {
		return nil
	}
	p := win32.Call(pGlobalLock, h)
	if p == 0 {
		return nil
	}
	defer win32.Call(pGlobalUnlock, h)
	return bytes.Clone(unsafe.Slice((*byte)(win32.Ptr(p)), int(win32.Call(pGlobalSize, h))))
}

func formatPresent(format uint32) bool {
	return format != 0 && win32.Bool(win32.Call(pIsFormatAvailable, uintptr(format)))
}

// The test image the other program puts on the clipboard: 24 x 16 pixels.
const imgW, imgH = 24, 16

func TestSetClipboardImageRoundTripAndFormats(t *testing.T) {
	w := openTestWindow(t)
	src, want := testPNG(t, imgW, imgH)
	if err := SetClipboardImage(src); err != nil {
		t.Fatal(err)
	}
	settleCopy(w)
	e, got := pasteKindsOf(t, w, 1, WantImage, 2*time.Second)
	if e.Flags != 0 || e.Paste != PasteImage || !bytes.Equal(got, src) {
		t.Errorf("image: flags %v kind %v, %d bytes (want %d, the same)", e.Flags, e.Paste, len(got), len(src))
	}
	f := clipFormats()
	for name, id := range map[string]uint32{"PNG": f.png, "CF_DIBV5": cfDIBV5, "CF_DIB": cfDIB} {
		if !formatPresent(id) {
			t.Errorf("%s is not on the clipboard after SetClipboardImage", name)
		}
	}
	for name, id := range map[string]uint32{"exclude": f.exclude, "history": f.history, "cloud": f.cloud} {
		if f.png == 0 || id == 0 {
			t.Fatalf("a format was not registered: %+v", f)
		}
		if formatPresent(id) {
			t.Errorf("a plain copy has the %s format", name)
		}
	}
	// Another program that reads only the DIB gets the same pixels.
	dib := readFormat(t, cfDIBV5)
	out, err := dibToPNG(nil, dib, MaxPaste)
	if err != nil {
		t.Fatal(err)
	}
	if px := decodeNRGBA(t, out); !slices.Equal(px.Pix, want.Pix) {
		t.Error("the CF_DIBV5 on the clipboard has other pixels than the PNG")
	}
	if e, _ := pasteKindsOf(t, w, 2, WantText|WantFiles, 3*time.Second); e.Flags&FlagFailed == 0 {
		t.Errorf("an image is not text or files: %+v", e)
	}
	// A copy that is not a PNG is refused and leaves the clipboard alone.
	if err := SetClipboardImage([]byte("not a png")); !errors.Is(err, ErrNotPNG) {
		t.Errorf("text as an image: %v", err)
	}
	if err := SetClipboardImage(make([]byte, MaxPaste+1<<20)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a huge image: %v", err)
	}
	if e, got := pasteKindsOf(t, w, 3, WantImage, 2*time.Second); e.Flags != 0 || !bytes.Equal(got, src) {
		t.Errorf("a refused copy changed the clipboard: %+v", e)
	}
}

func TestClipboardConcealedFormats(t *testing.T) {
	w := openTestWindow(t)
	f := clipFormats()
	src, _ := testPNG(t, 8, 8)
	for _, tc := range []struct {
		name string
		set  func(hide bool)
		data uint32
	}{
		{"text", func(h bool) { SetClipboardTextWith([]byte("secret"), ClipOptions{Concealed: h}) }, cfUnicodeText},
		{"image", func(h bool) { SetClipboardImage(src, ClipOptions{Concealed: h}) }, f.png},
	} {
		tc.set(true)
		settleCopy(w)
		if !formatPresent(tc.data) {
			t.Fatalf("%s: the data is not on the clipboard", tc.name)
		}
		if !formatPresent(f.exclude) || !formatPresent(f.history) || !formatPresent(f.cloud) {
			t.Errorf("%s concealed: exclude %v history %v cloud %v", tc.name, formatPresent(f.exclude), formatPresent(f.history), formatPresent(f.cloud))
		}
		for name, id := range map[string]uint32{"CanIncludeInClipboardHistory": f.history, "CanUploadToCloudClipboard": f.cloud} {
			if b := readFormat(t, id); len(b) < 4 || binary.LittleEndian.Uint32(b) != 0 {
				t.Errorf("%s concealed: %s = %v, want a DWORD 0", tc.name, name, b)
			}
		}
		if tc.name == "text" {
			if _, got := pasteOf(t, w, 1, 2*time.Second); got != "secret" {
				t.Errorf("the concealed text must still paste, got %q", got)
			}
		}
		tc.set(false)
		settleCopy(w)
		if formatPresent(f.exclude) || formatPresent(f.history) || formatPresent(f.cloud) {
			t.Errorf("%s: the next, plain copy keeps the concealed formats", tc.name)
		}
	}
}

// otherData runs a child of the test binary that puts what on the clipboard
// (takeClipboardData) and waits for it to end. files are for "hdrop".
func otherData(t *testing.T, what string, files ...string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestClipboardOtherData$", "-test.count=1")
	cmd.Env = append(os.Environ(), "GNUI_CLIP_DATA="+what, "GNUI_CLIP_FILES="+strings.Join(files, "|"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the other program (%s): %v\n%s", what, err, out)
	}
}

// TestClipboardOtherData is not a test: it is the other program of otherData.
func TestClipboardOtherData(t *testing.T) {
	what := os.Getenv("GNUI_CLIP_DATA")
	if what == "" {
		t.Skip("the other program of the image and file clipboard tests, run by otherData")
	}
	runtime.LockOSThread()
	if err := takeClipboardData(t, what, strings.Split(os.Getenv("GNUI_CLIP_FILES"), "|")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	os.Exit(0)
}

// dib24 is a 4 x 3 bottom-up 24-bit DIB; dib24Pixel is the pixel it holds.
func dib24Pixel(x, y int) color.NRGBA {
	return color.NRGBA{uint8(x*60 + 1), uint8(y*80 + 2), uint8(x + y*3 + 3), 255}
}

func dib24() []byte {
	const w, h = 4, 3
	stride := (w*24 + 31) / 32 * 4
	px := make([]byte, stride*h)
	for y := 0; y < h; y++ {
		row := px[(h-1-y)*stride:]
		for x := 0; x < w; x++ {
			c := dib24Pixel(x, y)
			row[3*x], row[3*x+1], row[3*x+2] = c.B, c.G, c.R
		}
	}
	return dibOf(w, h, 24, 0, nil, px)
}

// takeClipboardData is what the other program does (see takeClipboard): the
// clipboard emptied with a window of its own, then the formats of what.
func takeClipboardData(t *testing.T, what string, files []string) error {
	hwnd, _, _ := syscall.SyscallN(pCreateWindowExW, 0, uintptr(unsafe.Pointer(mustUTF16("STATIC"))), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
	if hwnd == 0 {
		return errors.New("CreateWindowExW failed")
	}
	defer win32.Call(pDestroyWindow, hwnd)
	opened := false
	for end := time.Now().Add(5 * time.Second); !opened && time.Now().Before(end); {
		if opened = win32.Bool(win32.Call(pOpenClipboard, hwnd)); !opened {
			time.Sleep(2 * time.Millisecond)
		}
	}
	if !opened {
		return errors.New("OpenClipboard failed for 5 s")
	}
	defer win32.Call(pCloseClipboard)
	if !win32.Bool(win32.Call(pEmptyClipboard)) {
		return errors.New("EmptyClipboard failed")
	}
	f := clipFormats()
	src, _ := testPNG(t, imgW, imgH)
	var puts []clipPut
	switch what {
	case "dib24":
		puts = append(puts, clipPut{cfDIB, allocClipboardBytes(dib24())})
	case "dibv5":
		dib, err := pngToDIBV5(nil, src)
		if err != nil {
			return err
		}
		puts = append(puts, clipPut{cfDIBV5, allocClipboardBytes(dib)})
	case "pngfmt": // as Chromium and Office write it: the memory is rounded up past the file
		puts = append(puts, clipPut{f.png, allocClipboardBytes(append(slices.Clone(src), make([]byte, 37)...))})
	case "pngbig":
		puts = append(puts, clipPut{f.png, allocClipboardBytes(pngShaped(t, MaxPaste+1<<20))})
	case "hdrop":
		puts = append(puts, clipPut{cfHDrop, allocClipboardBytes(dropFiles(true, files...))})
	case "all": // a copied file: its path, its name as text and a picture of it
		puts = append(puts, clipPut{cfHDrop, allocClipboardBytes(dropFiles(true, files...))},
			clipPut{f.png, allocClipboardBytes(src)},
			clipPut{cfUnicodeText, allocClipboardText([]byte("a file name"))})
	case "empty":
		return nil
	default:
		return fmt.Errorf("what %q", what)
	}
	for _, p := range puts {
		if p.format == 0 || p.mem == 0 || win32.Call(pSetClipboardData, uintptr(p.format), p.mem) == 0 {
			return fmt.Errorf("SetClipboardData(%d) failed", p.format)
		}
	}
	return nil
}

func TestPasteImageFromOtherProgram(t *testing.T) {
	w := openTestWindow(t)
	_, wantPNG := testPNG(t, imgW, imgH)

	otherData(t, "dib24")
	e, got := pasteKindsOf(t, w, 1, WantImage, 5*time.Second)
	if e.Flags != 0 || e.Paste != PasteImage {
		t.Fatalf("a 24-bit DIB: %+v", e)
	}
	img := decodeNRGBA(t, got)
	if img.Bounds().Dx() != 4 || img.Bounds().Dy() != 3 {
		t.Fatalf("the DIB became a %v image", img.Bounds())
	}
	for _, p := range [][2]int{{0, 0}, {3, 0}, {1, 2}, {3, 2}} {
		if c := img.NRGBAAt(p[0], p[1]); c != dib24Pixel(p[0], p[1]) {
			t.Errorf("pixel %v: %v, want %v", p, c, dib24Pixel(p[0], p[1]))
		}
	}

	otherData(t, "dibv5")
	e, got = pasteKindsOf(t, w, 2, WantImage, 5*time.Second)
	if e.Flags != 0 || !slices.Equal(decodeNRGBA(t, got).Pix, wantPNG.Pix) {
		t.Errorf("a CF_DIBV5 with alpha: %+v", e)
	}

	otherData(t, "pngfmt")
	src, _ := testPNG(t, imgW, imgH)
	e, got = pasteKindsOf(t, w, 3, WantImage, 5*time.Second)
	if e.Flags != 0 || !bytes.Equal(got, src) {
		t.Errorf("the PNG format: %+v, %d bytes, want the file's %d exactly (not the rounded-up memory)", e, len(got), len(src))
	}

	// Over MaxPaste: refused by its size.
	otherData(t, "pngbig")
	e, got = pasteKindsOf(t, w, 4, WantImage, 5*time.Second)
	if e.Flags != FlagFailed|FlagTooBig || len(got) != 0 {
		t.Errorf("a PNG over MaxPaste: flags %v, %d bytes", e.Flags, len(got))
	}
}

func TestPasteFilesFromOtherProgram(t *testing.T) {
	w := openTestWindow(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a file.txt"), filepath.Join(dir, "caf\u00e9 \U0001F600.png")

	otherData(t, "hdrop", a, b)
	e, got := pasteKindsOf(t, w, 1, WantFiles, 5*time.Second)
	if want := []string{a, b}; e.Flags != 0 || e.Paste != PasteFiles || e.Code != 2 || !slices.Equal(PasteFilePaths(got), want) {
		t.Errorf("files: flags %v kind %v code %d: %q, want %q", e.Flags, e.Paste, e.Code, PasteFilePaths(got), want)
	}
	if e, _ := pasteKindsOf(t, w, 2, WantText|WantImage, 3*time.Second); e.Flags&FlagFailed == 0 {
		t.Errorf("files delivered to a request that did not ask for them: %+v", e)
	}

	// A copied file comes with its name as text and a picture: files win, then image, then text.
	otherData(t, "all", a)
	for i, c := range []struct {
		kinds PasteKinds
		kind  PasteKind
	}{
		{WantText | WantImage | WantFiles, PasteFiles},
		{WantText | WantImage, PasteImage},
		{WantText, PasteText},
		{WantFiles, PasteFiles},
	} {
		if e, _ := pasteKindsOf(t, w, uint64(10+i), c.kinds, 5*time.Second); e.Flags != 0 || e.Paste != c.kind {
			t.Errorf("kinds %v: delivered %v (flags %v), want %v", c.kinds, e.Paste, e.Flags, c.kind)
		}
	}
}

// The paste poll allocates nothing in steady state, for an image and for
// files, worker thread included.
func TestPasteKindsZeroAllocs(t *testing.T) {
	w := openTestWindow(t)
	src, _ := testPNG(t, 64, 64)
	dir := t.TempDir()
	cycle := func(kinds PasteKinds, kind PasteKind) func() {
		return func() {
			w.RequestPasteKinds(1, kinds)
			if e := waitPaste(w, 2*time.Second); e.Kind != EvPaste || e.Flags != 0 || e.Paste != kind {
				t.Fatalf("answer %+v", e)
			}
		}
	}
	if err := SetClipboardImage(src); err != nil {
		t.Fatal(err)
	}
	settleCopy(w)
	for _, c := range []struct {
		name string
		put  func()
		f    func()
	}{
		{"png", func() {}, cycle(WantImage, PasteImage)},
		{"files", func() { otherData(t, "hdrop", filepath.Join(dir, "a"), filepath.Join(dir, "b")) }, cycle(WantFiles, PasteFiles)},
	} {
		c.put()
		c.f() // warm: the worker, its buffer, the queue
		c.f()
		c.f()
		if n := testing.AllocsPerRun(50, c.f); n != 0 {
			t.Errorf("%s: %v allocations per paste", c.name, n)
		}
	}
}

// What SetClipboardImage puts on the clipboard another process reads as an
// image, and an image another process puts there is pasted as PNG; files too.
// One PowerShell run, kept waiting between the steps, because starting it is
// slow.
func TestImageAndFilesWithPowerShell(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("no powershell.exe")
	}
	w := openTestWindow(t)
	dir := t.TempDir()
	out, go2, ready1, ready2 := filepath.Join(dir, "out.txt"), filepath.Join(dir, "go2"), filepath.Join(dir, "ready1"), filepath.Join(dir, "ready2")
	f1, f2 := filepath.Join(dir, "one.txt"), filepath.Join(dir, "two words.txt")
	for _, f := range []string{f1, f2} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	src, _ := testPNG(t, 30, 20)
	if err := SetClipboardImage(src); err != nil {
		t.Fatal(err)
	}
	settleCopy(w)
	script := `Add-Type -AssemblyName System.Windows.Forms,System.Drawing;` +
		`$i = Get-Clipboard -Format Image; [IO.File]::WriteAllText($env:GNUI_OUT, "$($i.Width)x$($i.Height)");` +
		`$b = New-Object System.Drawing.Bitmap 32,20; $g = [System.Drawing.Graphics]::FromImage($b); $g.Clear([System.Drawing.Color]::Red);` +
		`[System.Windows.Forms.Clipboard]::SetImage($b); [IO.File]::WriteAllText($env:GNUI_READY1, "x");` +
		`for ($n = 0; $n -lt 1200 -and -not (Test-Path $env:GNUI_GO2); $n++) { Start-Sleep -Milliseconds 50 };` +
		`Set-Clipboard -Path $env:GNUI_F1, $env:GNUI_F2; [IO.File]::WriteAllText($env:GNUI_READY2, "x");` +
		`Start-Sleep -Seconds 1`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), "GNUI_OUT="+out, "GNUI_GO2="+go2, "GNUI_READY1="+ready1, "GNUI_READY2="+ready2, "GNUI_F1="+f1, "GNUI_F2="+f2)
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() { os.WriteFile(go2, nil, 0o600); <-done }()
	waitFile := func(p string) {
		t.Helper()
		for end := time.Now().Add(3 * time.Minute); time.Now().Before(end); w.Wait(int64(20 * time.Millisecond)) {
			if _, err := os.Stat(p); err == nil {
				return
			}
			select {
			case err := <-done:
				done <- err
				t.Fatalf("powershell ended early: %v\n%s", err, log.String())
			default:
			}
		}
		t.Fatalf("powershell did not reach %s\n%s", filepath.Base(p), log.String())
	}

	waitFile(ready1)
	if got, err := os.ReadFile(out); err != nil || string(got) != "30x20" {
		t.Errorf("Get-Clipboard -Format Image read %q (%v), want 30x20\n%s", got, err, log.String())
	}
	e, got := pasteKindsOf(t, w, 1, WantImage, 10*time.Second)
	if e.Flags != 0 || e.Paste != PasteImage {
		t.Fatalf("image from PowerShell: %+v", e)
	}
	img := decodeNRGBA(t, got)
	if img.Bounds().Dx() != 32 || img.Bounds().Dy() != 20 || img.NRGBAAt(5, 5) != (color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("the pasted image is %v with pixel %v, want 32x20 red", img.Bounds(), img.NRGBAAt(5, 5))
	}

	os.WriteFile(go2, nil, 0o600)
	waitFile(ready2)
	e, got = pasteKindsOf(t, w, 2, WantFiles, 10*time.Second)
	if want := []string{f1, f2}; e.Flags != 0 || e.Paste != PasteFiles || !slices.Equal(PasteFilePaths(got), want) {
		t.Errorf("files from Set-Clipboard -Path: flags %v: %q, want %q", e.Flags, PasteFilePaths(got), want)
	}
}
