// Package img decodes images on worker goroutines, at the size they are
// shown, and keeps them in a cache with GPU and CPU byte budgets (DECISIONS
// D44). Chat avatars, thumbnails and media previews draw from it.
//
//	cache := img.NewCache(img.Options{Renderer: a.R, GPUBytes: 96 << 20})  // once, in Init
//
//	if cache.Poll() {                                                      // each frame, at the start of Build
//		c.RequestFrame()                                                   // uploads are left for the next frame
//	}
//	key := img.Key{Hash: msg.MediaHash, W: w, H: h, Flags: img.Cover}      // display size in pixels
//	h, _ := cache.Request(key, img.Bytes(msg.Thumb))                       // the same key gives the same handle
//	k.Image(1, h, ui.FitFill, radius, "Photo")                             // widget/base: a skeleton until it is Ready
//
// # The cache
//
// An image is named by its Key: a content hash the app chooses (a message's
// media hash, a file's identity), the decode size in pixels and Flags.
// Request returns a Handle, generation-tagged so that a handle kept past its
// image's eviction never names another image, and a State: Pending while it
// decodes or waits for its upload, Ready once it is on the GPU, Failed when it
// cannot be decoded (Err says why). Requests are cheap and allocate nothing
// for images already known: call Request every frame for what is in view.
//
// A Source says where the encoded bytes are: a file, inline bytes (copied at
// the first request that needs them, for small images such as WhatsApp's
// inline JPEG thumbnails), or a Lease of bytes the app owns, which the cache
// releases once the decoder has read them. A Source can also be a system icon
// (FileIcon, ExtensionIcon, TypeIcon, FolderIcon): what the file manager shows
// for a file, folder, volume or type, decoded at the size shown; see
// sysicon.go for how to key them. VideoPoster(path) reads a local video's first
// frame on a cache worker, with its preferred rotation and the same decode box
// and Cover rules as a still image. It creates no player. On Windows and Linux
// it fails normally (Cache.Err wraps errors.ErrUnsupported), so apps can show
// their fallback.
//
// # Decoding
//
// Workers (a task.Pool) decode at display size: the image is scaled to fit
// inside Key.W × Key.H, or to cover it with the Cover flag, and is never
// enlarged. The result is premultiplied RGBA in sRGB, in buffers the cache
// pools (C memory on macOS).
//
//   - macOS: ImageIO (CGImageSourceCreateThumbnailAtIndex with
//     kCGImageSourceThumbnailMaxPixelSize), drawn into an sRGB bitmap
//     context, on each worker's own ffi.Frame, then turned to its EXIF
//     orientation. JPEG, PNG, GIF, HEIC, WebP and whatever else the system
//     decodes. The frameworks are loaded by the first worker that decodes.
//   - Windows and Linux: the standard library's PNG, JPEG and GIF decoders
//     and golang.org/x/image/webp (lossy, lossless and alpha), scaled by
//     area averaging. JPEG's EXIF orientation is applied (a scan of the APP1
//     segment). HEIC and other formats fail with ErrUnsupported. Animated
//     GIFs, APNGs (frame chunks rebuilt into one-frame PNGs for the PNG
//     decoder) and WebPs (each ANMF frame rebuilt into a one-frame WebP)
//     play with the Animate flag; without it an animated WebP shows its
//     first frame on its canvas.
//
// Decode decodes one image the same way outside the cache, into an
// *image.RGBA the app owns (a cropper's source, say).
//
// An image whose header gives it more than 100 megapixels fails with
// ErrTooLarge before any pixel is decoded. A GIF without the Animate flag
// decodes its first frame, on the image's logical screen; Info reports the
// frame count and the first frame's delay. With the flag, GIFs, APNGs,
// animated WebPs and (macOS) animated HEIC and HEICS play: see Animate and the animation
// section below.
//
// # GPU memory
//
// Poll uploads finished decodes within Options.UploadBytes per frame, so a
// burst of decodes never makes a slow frame. Images up to AtlasMax pixels on
// both sides share RGBA atlas pages (ARCHITECTURE §6), larger ones get their
// own textures. When the GPU budget is short, the least recently used images
// are evicted (gpu.Renderer.FreeTexture): least recently drawn or requested,
// and never one drawn or requested in this frame or the last, so an image in
// view is never evicted. When everything resident is in view the budget is
// exceeded rather than leaving an image blank (Stats.OverBudget counts it).
// Decoded pixels stay in CPU memory within Options.CPUBytes, so an image
// evicted from the GPU and needed again is uploaded without decoding again.
//
// # Animated images
//
// Key.Flags Animate makes a GIF, an APNG or a WebP an animation (animated
// HEIC and HEICS too on macOS). Kit.Image draws the frame showing and wakes
// the app at each frame's deadline; Cache.Animation gives play, pause and
// seek. Frames are decoded only while the image is in view, a few ahead, at
// display size; a hidden window or a row scrolled out decodes and wakes
// nothing. A file's loop count is honoured; delays of 10 ms or less show
// for 100 ms. The system's "Auto-play animated images" setting and Reduce
// Motion hold the first frame until Animation.Play. Details: anim.go.
//
// # Threads and allocation
//
// A Cache belongs to the goroutine that draws its window (its renderer's
// thread): Request, Use, Poll and Close are called there. Request, Use and
// Poll allocate nothing once the images in view are cached; decoding
// allocates, on the workers only (C memory on macOS).
package img

import (
	"errors"
	"unsafe"

	"gonativeui/gpu"
)

// Renderer is the part of gpu.Renderer the cache draws with; every OS
// backend's *gpu.Renderer satisfies it. Images go into atlas pages that
// frames in flight sample with Stage (GPU-ordered, no wait), and into new
// textures of their own with Upload.
type Renderer interface {
	NewTexture(kind gpu.TextureKind, w, h int) gpu.TextureID
	Upload(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int)
	Stage(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int)
	FreeTexture(t gpu.TextureID)
}

var _ Renderer = (*gpu.Renderer)(nil)

// AtlasMax is the largest width and height of an image that shares an atlas
// page; larger images get a texture of their own.
const AtlasMax = 256

// Options configures a Cache. Zero fields take the defaults.
type Options struct {
	// Renderer creates, fills and frees the textures. Required.
	Renderer Renderer
	// GPUBytes is the budget of texture memory: atlas pages and own textures
	// (default 128 MiB).
	GPUBytes int64
	// CPUBytes is the budget of decoded pixels kept in memory, waiting for
	// their upload or kept to upload again after a GPU eviction (default 32
	// MiB). Pixels waiting for their upload are never dropped. What an
	// animation keeps (its file and canvases) counts too: one not in view is
	// dropped whole, to stay within the budget.
	CPUBytes int64
	// UploadBytes is the most Poll uploads in one frame (default 4 MiB); one
	// image is uploaded per frame however large it is.
	UploadBytes int64
	// Workers is the number of decoding goroutines (default min(GOMAXPROCS, 4)).
	Workers int
	// Entries is the most images the cache tracks at once, in any state
	// (default 4096). It sizes the tables, which never grow.
	Entries int
	// Wake is called from a worker when a decode has finished, to wake the
	// UI loop (default platform.Wake; see package task).
	Wake func()
	// AnimRing is the most decoded frames an animated image holds in
	// memory: the one showing and the ones ahead of it (default 3, from 2
	// to 8). See the animation section of the package comment.
	AnimRing int
	// Autoplay reports whether animated images may play without being asked
	// to: the system's "Auto-play animated images" setting. It is read at
	// most once per frame, on the cache's goroutine, when an animated image
	// is shown. The default reads the system (macOS 14 and later; elsewhere
	// animations play); tests and apps with a setting of their own replace it.
	Autoplay func() bool
}

// Flags change how an image is decoded. They are part of its Key.
type Flags uint8

const (
	// Cover decodes the image to cover Key.W × Key.H (the smaller side
	// fills it; draw it with ui.FitFill) instead of fitting inside it.
	Cover Flags = 1 << iota
)

// Key names an image in the cache: the same content at the same decode size
// with the same flags is one image.
type Key struct {
	// Hash identifies the content. The app chooses it (a media hash, a hash of
	// the path and modification time, HashBytes of the bytes); images with
	// equal hashes must have equal content.
	Hash uint64
	// W and H are the decode box in pixels: the size the image is shown at
	// (points × scale). 0 leaves that side free; both 0 decodes at full size.
	W, H int32
	// Flags are the decode flags.
	Flags Flags
}

// HashBytes is a 64-bit FNV-1a hash of b, for Key.Hash.
func HashBytes(b []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

// HashString is HashBytes of s.
func HashString(s string) uint64 {
	return HashBytes(unsafe.Slice(unsafe.StringData(s), len(s)))
}

// Lease is encoded bytes the app owns and lends to the cache: Bytes is read
// on a worker while the image decodes, and Release is called once, when the
// cache no longer needs them: on the cache's goroutine, except for a decode
// still running when Close returns, whose worker releases its lease once the
// decoder has stopped reading it.
type Lease interface {
	Bytes() []byte
	Release()
}

// Source names encoded image bytes (File, Bytes, Leased), a system icon, or a
// local video's first frame (VideoPoster). The cache decodes it on its workers.
type Source struct {
	path  string
	bytes []byte
	lease Lease
	kind  uint8
}

const (
	srcNone uint8 = iota
	srcFile
	srcBytes
	srcLease
	srcVideoPoster
)

// File is the image file at path.
func File(path string) Source { return Source{path: path, kind: srcFile} }

// Bytes is an image held in b. The cache copies b at the first Request that
// has to decode it, so b may change afterwards; meant for small images
// (inline thumbnails). Larger ones are cheaper as a File or a Lease.
func Bytes(b []byte) Source { return Source{bytes: b, kind: srcBytes} }

// Leased is an image held in l's bytes. Request takes ownership of l: the
// cache calls l.Release once, when the decode has read the bytes, or at once
// when the image needs no decode (already cached or decoding) or cannot be
// accepted. Use Get to learn whether an image needs its bytes before taking
// a lease.
func Leased(l Lease) Source { return Source{lease: l, kind: srcLease} }

// State is where an image is in the cache.
type State uint8

const (
	// Pending: the image is decoding or waits for its upload.
	Pending State = iota
	// Ready: the image is on the GPU and draws.
	Ready
	// Failed: the image could not be decoded (see Cache.Err).
	Failed
)

// String names the state.
func (s State) String() string {
	switch s {
	case Pending:
		return "pending"
	case Ready:
		return "ready"
	case Failed:
		return "failed"
	}
	return "?"
}

// Handle names an image of a cache while it stays there. The zero Handle
// names nothing. A handle whose image was evicted no longer resolves, even
// if the same key is requested again later (it then gets a new handle).
type Handle uint64

func makeHandle(i int32, gen uint32) Handle { return Handle(uint64(gen)<<32 | uint64(uint32(i))) }
func (h Handle) index() int32               { return int32(uint32(h)) }
func (h Handle) gen() uint32                { return uint32(h >> 32) }

// Errors reported by Err.
var (
	// ErrUnsupported: the format cannot be decoded on this system (HEIC off
	// macOS).
	ErrUnsupported = errors.New("img: unsupported image format")
	// ErrDecode: the bytes are not a valid image.
	ErrDecode = errors.New("img: cannot decode the image")
	// ErrRead: the file cannot be read.
	ErrRead = errors.New("img: cannot read the image file")
	// ErrTooLarge: the image has more than 100 megapixels, or the decoded
	// image would not fit a texture.
	ErrTooLarge = errors.New("img: image too large")
)

// Error codes carried by a decode result (pointer-free).
const (
	errNone uint8 = iota
	errUnsupported
	errDecode
	errRead
	errTooLarge
	errVideo
)

func errOf(code uint8) error {
	switch code {
	case errUnsupported:
		return ErrUnsupported
	case errDecode:
		return ErrDecode
	case errRead:
		return ErrRead
	case errTooLarge:
		return ErrTooLarge
	case errVideo:
		return errVideoUnsupported
	}
	return nil
}

// Info describes a decoded image.
type Info struct {
	// W and H are the decoded size in pixels (after orientation).
	W, H int
	// Frames is the number of frames of an animated image (1 otherwise).
	Frames int
	// DelayMs is the first frame's display time in milliseconds (0 for a
	// still image).
	DelayMs int
	// Plays is how many times an animation plays (0: for ever); 1 for a
	// still image.
	Plays int
}

// Stats are counters of a cache.
type Stats struct {
	// Entries is the images the cache tracks; Ready, Pending and Failed
	// split them by state.
	Entries, Ready, Pending, Failed int
	// GPUBytes is the texture memory in use: atlas pages and own textures.
	GPUBytes int64
	// CPUBytes is the decoded pixels held in memory, and an estimate of what
	// each animation keeps between frames (its file and its canvases).
	CPUBytes int64
	// SpareBytes is the free decode buffers kept for reuse. They count
	// against the CPU budget with CPUBytes, and the cache keeps at most a
	// quarter of the budget (16 MiB at most) of them.
	SpareBytes int64
	// Pages is the number of atlas pages.
	Pages int
	// Decodes and DecodeNs count the finished decodes and their worker time;
	// Uploads and UploadedBytes the uploads.
	Decodes, Uploads        int
	DecodeNs, UploadedBytes int64
	// EvictedGPU and EvictedCPU count images evicted from each budget.
	EvictedGPU, EvictedCPU int
	// OverBudget counts the uploads made over the GPU budget because every
	// resident image was in view.
	OverBudget int
	// Animations is the animated images the cache has decoded the first
	// frame of; AnimDecodes the frames decoded after the first and
	// AnimUploads the frames put on the GPU; AnimErrors the animations that
	// stopped at a frame that did not decode.
	Animations, AnimDecodes, AnimUploads, AnimErrors int
	// Refused counts requests turned away because the decode queue or the
	// entry table was full of work in view (they are retried by the next
	// Request).
	Refused int
}
