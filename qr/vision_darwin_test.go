//go:build darwin

package qr

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// visionResult is what Vision's barcode detector reports for one QR symbol.
type visionResult struct {
	payload []byte // payloadStringValue as UTF-8; nil when Vision gave no string
	data    []byte // the corrected data codewords (the descriptor's errorCorrectedPayload)
	version int
	level   byte // 'L', 'M', 'Q' or 'H'
	mask    int
}

var (
	visionOnce  bool
	symbologyQR objc.ID
	errOut      objc.ID // NSError** target
)

func loadVision(t testing.TB) {
	t.Helper()
	if visionOnce {
		return
	}
	h, err := purego.Dlopen("/System/Library/Frameworks/Vision.framework/Vision", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Skipf("the Vision framework is not available: %v", err)
	}
	// CoreImage holds the CIQRCodeDescriptor class.
	if _, err := purego.Dlopen("/System/Library/Frameworks/CoreImage.framework/CoreImage", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		t.Skipf("CoreImage is not available: %v", err)
	}
	sym, err := purego.Dlsym(h, "VNBarcodeSymbologyQR")
	if err != nil {
		t.Skipf("VNBarcodeSymbologyQR: %v", err)
	}
	symbologyQR = *(*objc.ID)(ffi.PtrFromC(sym))
	visionOnce = true
}

// renderPNG draws the symbol black on white with a four-module quiet zone,
// scale pixels per module.
func renderPNG(c *Code, scale int) []byte {
	n := (c.Size() + 8) * scale
	img := image.NewGray(image.Rect(0, 0, n, n))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for y := 0; y < c.Size(); y++ {
		for x := 0; x < c.Size(); x++ {
			if !c.Dark(x, y) {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetGray((x+4)*scale+dx, (y+4)*scale+dy, color.Gray{0})
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// detect runs VNDetectBarcodesRequest on a PNG and returns what it found.
func detect(t testing.TB, pngData []byte) []visionResult {
	t.Helper()
	loadVision(t)
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)

	nsData := objc.Send2(objc.Cls("NSData"), objc.Sel("dataWithBytes:length:"), uintptr(unsafe.Pointer(&pngData[0])), uintptr(len(pngData)))
	opts := objc.Send(objc.Cls("NSDictionary"), objc.Sel("dictionary"))
	handler := objc.Send2(objc.Send(objc.Cls("VNImageRequestHandler"), objc.Sel("alloc")), objc.Sel("initWithData:options:"), uintptr(nsData), uintptr(opts))
	if handler == 0 {
		t.Fatal("VNImageRequestHandler: initWithData failed")
	}
	defer objc.Release(handler)
	req := objc.Send(objc.Send(objc.Cls("VNDetectBarcodesRequest"), objc.Sel("alloc")), objc.Sel("init"))
	defer objc.Release(req)
	objc.Send1(req, objc.Sel("setSymbologies:"), uintptr(objc.Send1(objc.Cls("NSArray"), objc.Sel("arrayWithObject:"), uintptr(symbologyQR))))

	errOut = 0
	reqs := objc.Send1(objc.Cls("NSArray"), objc.Sel("arrayWithObject:"), uintptr(req))
	ok := byte(objc.Send2(handler, objc.Sel("performRequests:error:"), uintptr(reqs), uintptr(unsafe.Pointer(&errOut)))) != 0
	if !ok {
		var msg []byte
		if errOut != 0 {
			msg = objc.AppendUTF8(nil, objc.Send(errOut, objc.Sel("localizedDescription")))
		}
		t.Fatalf("performRequests failed: %s", msg)
	}
	results := objc.Send(req, objc.Sel("results"))
	count := int(objc.Send(results, objc.Sel("count")))
	var out []visionResult
	for i := 0; i < count; i++ {
		obs := objc.Send1(results, objc.Sel("objectAtIndex:"), uintptr(i))
		var r visionResult
		if s := objc.Send(obs, objc.Sel("payloadStringValue")); s != 0 {
			r.payload = objc.AppendUTF8([]byte{}, s)
		}
		if d := objc.Send(obs, objc.Sel("barcodeDescriptor")); d != 0 {
			r.version = int(objc.Send(d, objc.Sel("symbolVersion")))
			r.level = byte(objc.Send(d, objc.Sel("errorCorrectionLevel")))
			r.mask = int(byte(objc.Send(d, objc.Sel("maskPattern"))))
			if p := objc.Send(d, objc.Sel("errorCorrectedPayload")); p != 0 {
				n := int(objc.Send(p, objc.Sel("length")))
				bp := objc.Send(p, objc.Sel("bytes"))
				r.data = append([]byte{}, unsafe.Slice((*byte)(ffi.PtrFromC(uintptr(bp))), n)...)
			}
		}
		out = append(out, r)
	}
	return out
}

// checkVision encodes data, renders it at scale pixels per module, and checks
// that Vision finds exactly one QR code with our version, level, mask and data
// codewords; text, when non-nil, must also come back as the payload string.
func checkVision(t *testing.T, name string, data []byte, level Level, scale int, text bool) {
	t.Helper()
	var c Code
	if err := Encode(&c, data, level); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	checkDecoded(t, name, &c, detect(t, renderPNG(&c, scale)), data, text)
}

func checkDecoded(t *testing.T, name string, c *Code, got []visionResult, data []byte, text bool) {
	t.Helper()
	if len(got) != 1 {
		t.Errorf("%s: Vision found %d codes, want 1 (version %d, level %v)", name, len(got), c.Version(), c.Level())
		return
	}
	r := got[0]
	if r.version != c.Version() || r.level != "LMQH"[c.Level()] || r.mask != c.Mask() {
		t.Errorf("%s: Vision read version %d level %c mask %d, we wrote %d %v %d", name, r.version, r.level, r.mask, c.Version(), c.Level(), c.Mask())
	}
	if want := c.data[:numDataCodewords(c.Version(), c.Level())]; !bytes.Equal(r.data, want) {
		t.Errorf("%s: Vision's corrected data differs from ours\n got %x\nwant %x", name, r.data, want)
	}
	if text && !bytes.Equal(r.payload, data) {
		t.Errorf("%s: payload %q, want %q", name, r.payload, data)
	}
}

func TestVisionDecodesWhatWeEncode(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	loadVision(t)

	// A link the size of the ones WhatsApp and other multi-device pairing flows
	// put in a QR code.
	wa := "https://wa.me/qr/" + strings.Repeat("ABCDEFGHIJKLM", 3) + "?src=zapfast&ref=pairing&session=" +
		strings.Repeat("0123456789abcdef", 5) + "&k=" + strings.Repeat("Zm9vYmFy", 4)
	if len(wa) < 190 || len(wa) > 215 {
		t.Fatalf("the link is %d bytes, meant to be about 200", len(wa))
	}
	texts := []struct{ name, s string }{
		{"short", "hi"},
		{"one character", "A"},
		{"url", "https://example.com/some/path?query=1&other=two"},
		{"whatsapp-like link", wa},
		{"numeric", "0123456789012345678901234567890123456789"},
		{"alphanumeric", "HELLO WORLD $%*+-./: 0123456789"},
		{"utf-8", "Olá, mundo! 日本語のテキスト — héllo ✓"},
		{"json", `{"id":"abc","server":"wss://example.com/ws","key":"qL3w9P0xZ+/=","ts":1790000000}`},
		{"wifi", "WIFI:T:WPA;S:Cafe Guest;P:coffee-2026;;"},
	}
	for _, level := range []Level{LevelL, LevelM, LevelQ, LevelH} {
		for _, tc := range texts {
			checkVision(t, fmt.Sprintf("%s/%v", tc.name, level), []byte(tc.s), level, 6, true)
		}
	}

	// A spread of lengths, from a single character past several version
	// boundaries, at every level; numeric and alphanumeric payloads too (they
	// use the other two modes). Vision is slow on the CI runners, so the
	// sweep is a spread rather than every length.
	for _, level := range []Level{LevelL, LevelM, LevelQ, LevelH} {
		for _, n := range []int{1, 7, 20, 46, 79, 121, 188, 305} {
			b := fill(n*31+int(level), n)
			for i := range b {
				b[i] = 'a' + b[i]%26
			}
			checkVision(t, fmt.Sprintf("lower/%v/%d", level, n), b, level, 5, true)
			if n > 79 {
				continue
			}
			for i := range b {
				b[i] = '0' + b[i]%10
			}
			checkVision(t, fmt.Sprintf("digits/%v/%d", level, n), b, level, 5, true)
			for i := range b {
				b[i] = alnumChars[int(b[i])*7%45]
			}
			checkVision(t, fmt.Sprintf("alnum/%v/%d", level, n), b, level, 5, true)
		}
	}
}

// Version and level pairs, filled to the last byte with binary data
// (the corrected data codewords are compared, so no text decoding is involved),
// are what Vision reads back: version, level, mask and every codeword.
func TestVisionDecodesEveryVersionAndLevel(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	loadVision(t)
	for v := 1; v <= maxVersion; v++ {
		for level := LevelL; level <= LevelH; level++ {
			// Every version at M and H, and every fourth at L and Q: the
			// exact modules of all 160 are checked against the reference
			// encoder (vectors_test.go), and Vision is slow on the CI runners.
			if (level == LevelL || level == LevelQ) && v%4 != 1 {
				continue
			}
			n := capacity[v-1][level]
			scale := 4
			if v > 10 {
				scale = 3
			}
			checkVision(t, fmt.Sprintf("v%d-%v", v, level), fill(v*5+int(level), n), level, scale, false)
		}
	}
}

// Draw, rasterised from the draw list at 1× and 2× of a window, scans.
func TestVisionDecodesDrawnSymbols(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	loadVision(t)
	wa := "https://wa.me/qr/" + strings.Repeat("0123456789ABCDEFGHIJ", 9)
	for _, payload := range []string{"https://example.com", wa} {
		m := encodeString(t, payload, LevelM)
		n := m.Size() + 2*QuietZone
		for _, scale := range []int{1, 2, 3} {
			for _, extra := range []int{0, 7} {
				side := n*scale + extra
				r := newDrawRig(side, side)
				r.frame(m, float32(side))
				dark, _ := raster(t, &r.list, side, side)
				img := image.NewGray(image.Rect(0, 0, side, side))
				for i, d := range dark {
					if !d {
						img.Pix[i] = 255
					}
				}
				var buf bytes.Buffer
				if err := png.Encode(&buf, img); err != nil {
					t.Fatal(err)
				}
				checkDecoded(t, fmt.Sprintf("v%d/x%d+%d", m.Version(), scale, extra), m, detect(t, buf.Bytes()), []byte(payload), true)
			}
		}
	}
}

// The detector is not vacuous: a blank page and a symbol with its finder
// patterns erased yield no code.
func TestVisionFindsNothingWithoutASymbol(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	loadVision(t)
	var c Code
	if err := Encode(&c, []byte("https://example.com"), LevelM); err != nil {
		t.Fatal(err)
	}
	if got := detect(t, renderPNG(&Code{size: c.Size()}, 6)); len(got) != 0 {
		t.Errorf("a blank image decoded as %d codes", len(got))
	}
	for y := 0; y < 7; y++ { // erase the three finders
		for x := 0; x < 7; x++ {
			for _, p := range [][2]int{{x, y}, {c.Size() - 1 - x, y}, {x, c.Size() - 1 - y}} {
				c.set(p[0], p[1], false)
			}
		}
	}
	if got := detect(t, renderPNG(&c, 6)); len(got) != 0 {
		t.Errorf("a symbol without finders decoded as %d codes", len(got))
	}
}
