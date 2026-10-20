//go:build darwin

package img

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// meanColour is the mean colour (not premultiplied) and the mean spread
// (largest minus smallest channel) of the pixels that are fully opaque.
func meanColour(d decoded) (rgb [3]int, spread int) {
	var sum [3]int
	n := 0
	for i := 0; i < len(d.pix); i += 4 {
		if d.pix[i+3] != 255 {
			continue
		}
		hi, lo := 0, 255
		for k := 0; k < 3; k++ {
			v := int(d.pix[i+k])
			sum[k] += v
			hi, lo = max(hi, v), min(lo, v)
		}
		spread += hi - lo
		n++
	}
	if n == 0 {
		return rgb, 0
	}
	return [3]int{sum[0] / n, sum[1] / n, sum[2] / n}, spread / n
}

// macOSMajor is the major version of macOS (0 if unknown).
func macOSMajor() int {
	v, err := syscall.Sysctl("kern.osproductversion")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	return n
}

// TestSystemIconsDarwin decodes a folder, a PDF and a text file, an
// application bundle and types with no file, each at 32 and 64 pixels: real
// icons with something to see, premultiplied, of the size asked for, and the
// 64 pixel one is its own art, not the 32 pixel one stretched.
func TestSystemIconsDarwin(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	dir := t.TempDir()
	pdf, txt := filepath.Join(dir, "doc.pdf"), filepath.Join(dir, "notes.txt")
	for _, f := range []string{pdf, txt} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srcs := []struct {
		name string
		src  Source
	}{
		{"folder", FileIcon(dir)},
		{"pdf file", FileIcon(pdf)},
		{"txt file", FileIcon(txt)},
		{"application", FileIcon("/System/Applications/Calculator.app")},
		{"volume", FileIcon("/")},
		{"pdf extension", ExtensionIcon(".pdf")},
		{"txt extension", ExtensionIcon("txt")},
		{"pdf uti", TypeIcon("com.adobe.pdf")},
		{"folder uti", TypeIcon("public.folder")},
		{"folder", FolderIcon()},
	}
	got := map[string][2]decoded{}
	for _, s := range srcs {
		var pair [2]decoded
		for i, side := range []int32{32, 64} {
			d := iconNow(t, c, s.src, side, side, 0)
			validIcon(t, s.name, d, int(side))
			if d.w != int(side) || d.h != int(side) {
				t.Errorf("%s: %d×%d at %d", s.name, d.w, d.h, side)
			}
			pair[i] = d
		}
		got[s.name] = pair
		if pair[0].pix == nil || pair[1].pix == nil {
			continue
		}
		up := scaleNearest(pair[0], 64, 64)
		mean, _ := diff(pair[1], up)
		d32, d64 := distinct(pair[0]), distinct(pair[1])
		t.Logf("%-14s 32: %4d colours; 64: %4d colours, mean difference from the 32 enlarged %.1f", s.name, d32, d64, mean)
		if d64 <= d32 || mean < 1.5 {
			t.Errorf("%s: the 64 pixel icon is the 32 pixel one stretched (%d and %d colours, difference %.2f)", s.name, d32, d64, mean)
		}
	}
	// The same type by two names is the same art.
	if a, b := got["folder uti"][1], got["folder"][1]; string(a.pix) != string(b.pix) {
		t.Error("TypeIcon(public.folder) and FolderIcon differ")
	}
	// Folders are the one coloured icon among the documents: a saturated
	// colour, where the text and PDF icons are paper (and blue on macOS 15
	// and 26, whose folders follow no setting here: measured green on 27.2).
	rgb, spread := meanColour(got["folder"][1])
	_, docSpread := meanColour(got["txt file"][1])
	t.Logf("folder: mean opaque colour %v, spread %d; text file spread %d", rgb, spread, docSpread)
	if spread < 80 || spread < docSpread+50 {
		t.Errorf("folder spread %d (want ≥ 80 and 50 over the document's), document spread %d", spread, docSpread)
	}
	if v := macOSMajor(); v != 0 && v < 27 && rgb[2] < rgb[0] {
		t.Errorf("the folder is not blue on macOS %d: mean colour %v", v, rgb)
	}
	// An application's icon is its own.
	if string(got["application"][1].pix) == string(got["txt file"][1].pix) {
		t.Error("the application has the document's icon")
	}
}

// TestSystemIconsDarwinErrors: what is not there fails the way the other
// sources fail.
func TestSystemIconsDarwinErrors(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	for _, tc := range []struct {
		what string
		src  Source
		w, h int32
		want error
	}{
		{"missing file", FileIcon("/no/such/file"), 32, 32, ErrRead},
		{"unknown type", TypeIcon("com.example.no-such-type"), 32, 32, ErrUnsupported},
		{"empty type", TypeIcon(""), 32, 32, ErrUnsupported},
		{"empty extension", ExtensionIcon("."), 32, 32, ErrUnsupported},
		{"too large", FolderIcon(), 11000, 11000, ErrTooLarge},
		{"side over the texture limit", FolderIcon(), maxSide + 1, 0, ErrTooLarge},
	} {
		if d := iconNow(t, c, tc.src, tc.w, tc.h, 0); d.r.buf >= 0 || errOf(d.r.err) != tc.want {
			t.Errorf("%s: buffer %d, error %v; want %v", tc.what, d.r.buf, errOf(d.r.err), tc.want)
		}
	}
	// An extension the system knows nothing of still has the generic icon.
	validIcon(t, "unknown extension", iconNow(t, c, ExtensionIcon("gnuiunknownext"), 32, 32, 0), 32)
	// Neither errors nor icons leak pooled buffers.
	if inUse := c.bufs.inUse; inUse != 0 {
		t.Errorf("%d bytes of buffers still in use", inUse)
	}
}

// TestSystemIconsBoxAndCover: the icon is square in the largest square of
// the box, or the smallest that covers it.
func TestSystemIconsBoxAndCover(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	for _, tc := range []struct {
		w, h  int32
		flags Flags
		side  int
	}{{64, 40, 0, 40}, {64, 40, Cover, 64}, {48, 0, 0, 48}, {0, 48, 0, 48}, {0, 0, 0, 128}} {
		if d := iconNow(t, c, FolderIcon(), tc.w, tc.h, tc.flags); d.w != tc.side || d.h != tc.side {
			t.Errorf("box %d×%d flags %v: %d×%d, want %d square", tc.w, tc.h, tc.flags, d.w, d.h, tc.side)
		}
	}
}

// TestSystemIconsThroughCache: icons are requested and keyed like images:
// one key is one decode however often it is requested, another size is
// another image, and the icon ends up on the GPU.
func TestSystemIconsThroughCache(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	k64 := Key{Hash: HashTypeIcon("pdf"), W: 64, H: 64}
	k32 := Key{Hash: HashTypeIcon("pdf"), W: 32, H: 32}
	var h64, h32 Handle
	settle(t, c, func() {
		h64, _ = c.Request(k64, ExtensionIcon("pdf"))
		h32, _ = c.Request(k32, ExtensionIcon("pdf"))
		c.Request(k64, ExtensionIcon("pdf"))
	})
	if h64 == h32 || c.State(h64) != Ready || c.State(h32) != Ready {
		t.Fatalf("handles %#x %#x, states %v %v", h64, h32, c.State(h64), c.State(h32))
	}
	if i64, _ := c.Info(h64); i64.W != 64 || i64.H != 64 || i64.Frames != 1 {
		t.Errorf("64 pixel icon info %+v", i64)
	}
	if i32, _ := c.Info(h32); i32.W != 32 {
		t.Errorf("32 pixel icon info %+v", i32)
	}
	if s := c.Stats(); s.Decodes != 2 {
		t.Errorf("%d decodes for two keys", s.Decodes)
	}
	// A missing file is a failed image.
	var h Handle
	settle(t, c, func() {
		h, _ = c.Request(Key{Hash: HashFileIcon("/no/such", mod0, 0), W: 32, H: 32}, FileIcon("/no/such"))
	})
	if c.State(h) != Failed || c.Err(h) != ErrRead {
		t.Errorf("a missing file: %v, %v", c.State(h), c.Err(h))
	}
}

// TestSystemIconsConcurrent is the evidence that NSWorkspace and NSImage
// work off the main thread (sysicon_darwin.go): eight goroutines, each on its
// own OS thread, decode the same icons over and over while the cache's own
// workers decode others; nothing crashes or throws, and every icon equals
// the one decoded alone, within a tolerance: the art of an application icon
// settles by a level or two in the system's icon cache over the first
// moments (measured: the first decode of a size differs from every later one
// in 1 run of 60; under load, from decodes made a few hundred milliseconds
// later in 1 run of 12, with or without a lock around the AppKit calls). A
// race that corrupted an image would differ by far more.
func TestSystemIconsConcurrent(t *testing.T) {
	c, _ := newTestCache(t, Options{Workers: 4})
	srcs := []Source{
		FolderIcon(), ExtensionIcon("pdf"), ExtensionIcon("txt"), TypeIcon("public.jpeg"),
		FileIcon("/System/Applications/Calculator.app"), FileIcon("/System/Applications/Utilities/Terminal.app"), FileIcon("/"),
	}
	sides := []int32{16, 32, 64, 128, 256}
	want := map[[2]int]decoded{}
	for i, s := range srcs {
		for _, side := range sides {
			want[[2]int{i, int(side)}] = iconNow(t, c, s, side, side, 0)
		}
	}
	// The cache's workers busy at the same time.
	go func() {
		for k := 0; k < 40; k++ {
			ext := "busy" + strconv.Itoa(k)
			c.Request(Key{Hash: HashTypeIcon(ext), W: 64, H: 64}, ExtensionIcon(ext))
		}
	}()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 30; k++ {
				i := (g + k) % len(srcs)
				side := sides[(g*3+k)%len(sides)]
				d := iconNow(t, c, srcs[i], side, side, 0)
				if d.r.buf < 0 {
					t.Errorf("goroutine %d: icon %d at %d failed: %v", g, i, side, errOf(d.r.err))
					return
				}
				if mean, worst := diff(want[[2]int{i, int(side)}], d); mean > 0.5 || worst > 8 {
					t.Errorf("goroutine %d: icon %d at %d differs from the one decoded alone: mean %.2f, worst %d", g, i, side, mean, worst)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
