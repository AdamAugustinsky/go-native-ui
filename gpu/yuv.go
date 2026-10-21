package gpu

import "errors"

// YUV textures hold video frames as their planes (luma and chroma) and are
// converted to RGB as they are sampled. A ModeRGBA quad draws one like any
// RGBA texture (UV in luma texels, Radii, Clip and the alpha of Color
// apply), and the result is opaque. Two kinds exist:
//
//   - Textures the renderer owns (NewYUVTexture), filled from memory with
//     StageYUV: a call's I420 frames, or NV12.
//   - Imports of a CVPixelBuffer (macOS, ImportPixelBuffer), which alias its
//     memory through CVMetalTextureCache with no copy: AVFoundation's video
//     frames. An import names the pixel buffer for the frame being built
//     only, and the renderer keeps it until the GPU has finished that frame.
//
// On Windows and Linux NewYUVTexture reports ErrUnsupported.

// YUVFormat is the plane layout of a YUV texture. Both are 4:2:0: the
// chroma planes are half the luma's width and height, rounded up.
type YUVFormat uint8

const (
	// YUVNV12 is a luma plane and one plane of interleaved Cb, Cr pairs
	// (biplanar; what Core Video decodes to).
	YUVNV12 YUVFormat = iota + 1
	// YUVI420 is a luma plane, then a Cb and a Cr plane (triplanar; what
	// WebRTC and openh264 produce).
	YUVI420
)

// YUVSpace says how a YUV texture's values map to RGB: the matrix and the
// range of the values.
type YUVSpace uint8

const (
	// BT601Video is BT.601 in video range (luma 16–235, chroma 16–240):
	// standard-definition video. The zero value.
	BT601Video YUVSpace = iota
	// BT601Full is BT.601 in full range (0–255; JPEG, many webcams).
	BT601Full
	// BT709Video is BT.709 in video range: high-definition video.
	BT709Video
	// BT709Full is BT.709 in full range.
	BT709Full
)

// YUVImage is one YUV image in memory: its planes and their row strides in
// bytes. I420 has Y, U (Cb) and V (Cr); NV12 has Y and the interleaved
// Cb, Cr pairs in U, and no V.
type YUVImage struct {
	// Y, U and V are the planes (V unused for NV12).
	Y, U, V []byte
	// YStride, UStride and VStride are their rows' distance in bytes.
	YStride, UStride, VStride int
	// Turns rotates the image clockwise by quarter turns (0 to 3) as it is
	// drawn, for frames whose camera was turned: with an odd number the
	// texture is drawn as an H×W image (its UV rectangle is in those
	// texels).
	Turns int
}

// MaxImports is the most pixel buffers one frame imports (ImportPixelBuffer).
// With the renderer's three frames in flight, at most 3 × MaxImports are
// leased at once.
const MaxImports = 16

// ErrBusy is returned by ImportPixelBuffer when the frame has MaxImports
// imports already: the caller draws its placeholder instead.
var ErrBusy = errors.New("gpu: the frame has MaxImports pixel buffers imported")

// yuvMatrix is the rows of the conversion rgb = M·(y, cb, cr, 1) of space sp,
// on values normalised to 0..1.
func yuvMatrix(sp YUVSpace) [12]float32 {
	kr, kb := 0.299, 0.114 // BT.601
	if sp == BT709Video || sp == BT709Full {
		kr, kb = 0.2126, 0.0722
	}
	kg := 1 - kr - kb
	ys, cs, yo := 1.0, 1.0, 0.0 // luma scale, chroma scale, luma offset
	if sp == BT601Video || sp == BT709Video {
		ys, cs, yo = 255.0/219, 255.0/224, 16.0/255
	}
	const co = 128.0 / 255
	crR := 2 * (1 - kr) * cs
	cbB := 2 * (1 - kb) * cs
	cbG := -2 * kb * (1 - kb) / kg * cs
	crG := -2 * kr * (1 - kr) / kg * cs
	off := -ys * yo
	return [12]float32{
		float32(ys), 0, float32(crR), float32(off - crR*co),
		float32(ys), float32(cbG), float32(crG), float32(off - (cbG+crG)*co),
		float32(ys), float32(cbB), 0, float32(off - cbB*co),
	}
}

// chromaSize is the size of a 4:2:0 chroma plane of a w×h image.
func chromaSize(w, h int) (int, int) { return (w + 1) / 2, (h + 1) / 2 }
