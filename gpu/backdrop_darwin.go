//go:build darwin

package gpu

import (
	"errors"
	"fmt"
	"math"
	"unsafe"

	"gonativeui/internal/objc"
)

// Backdrops (ModeBackdrop, D54). The main pass ends at a backdrop quad;
// three small passes then read the region under it from the target, scaled
// down (bd_down), and blur it horizontally and vertically (bd_blur), the
// last pass applying the recipe; the main pass resumes, loading the target,
// and draws the quad sampling the result (mode 3 of quadMSL). All of it is
// in the frame's command buffer, in order: Metal's hazard tracking orders
// the passes' reads after the writes before them, so one pair of textures
// serves every backdrop of a frame and every frame.
//
// The two textures are the reduced region and the blur's intermediate,
// allocated for the largest region seen (one column and one row more: the
// last pass replicates the edge there, so the bilinear composite never
// samples past the region), in size classes shared across targets. They
// shrink after sustained low demand. Nothing is allocated on the Go heap per
// frame. The pipelines are compiled with the first frame that has a backdrop.

const backdropMSL = `
#include <metal_stdlib>
using namespace metal;

struct BU {
	float4 src;     // the region in the source, pixels: x0, y0, x1, y1
	float4 tint;    // rgb, mix
	float2 srcSize; // the source texture's size
	float2 n;       // the region's size at the reduced scale, texels
	float2 vp;      // the attachment's size
	float2 dir;     // the blur's direction
	float sigma;    // reduced texels
	float radius;   // taps on each side
	float down;
	float sat;
	float add;
	float last;     // 1: the last pass, which applies the recipe
	float under;    // 1: the tint is mixed in before the colour adjustments
	float fades;    // 1: the backdrop fades out (Backdrop.Fade)
	float2 fade;    // where: from y = fade.x (the full recipe) to fade.y (none), target pixels
	float2 pad;
};

// strength is how much of the recipe applies on the reduced region's row
// i: 1, or along a fade, 1 less the smoothstep of the way through it (at
// the row's centre in the target).
static float strength(constant BU& u, int i) {
	if (u.fades < 0.5) {
		return 1.0;
	}
	float y = u.src.y + (float(i) + 0.5) * u.down;
	return 1.0 - smoothstep(0.0, 1.0, (y - u.fade.x) / (u.fade.y - u.fade.x));
}

// A quad over the region and one more column and row, which repeat its edge.
vertex float4 bd_vs(uint vid [[vertex_id]], constant BU& u [[buffer(0)]]) {
	float2 c = float2(float(vid & 1u), float(vid >> 1));
	float2 p = c * (u.n + 1.0);
	return float4(p / u.vp * float2(2.0, -2.0) + float2(-1.0, 1.0), 0.0, 1.0);
}

// The mean of each down×down block of the region: bilinear taps on the
// corners between pixels, each the mean of four (clamped to the region's
// pixel centres, so a partial block at its far edge repeats its last pixels).
fragment float4 bd_down(float4 pos [[position]], constant BU& u [[buffer(0)]],
                        texture2d<float> t [[texture(0)]], sampler s [[sampler(0)]]) {
	float2 i = min(floor(pos.xy), u.n - 1.0);
	float2 o = u.src.xy + i * u.down;
	float2 lo = u.src.xy + 0.5, hi = u.src.zw - 0.5;
	int n = max(int(u.down) / 2, 1);
	float4 acc = 0.0;
	for (int b = 0; b < n; b++) {
		for (int a = 0; a < n; a++) {
			float2 p = u.down < 2.0 ? o + 0.5 : o + float2(float(2 * a + 1), float(2 * b + 1));
			acc += t.sample(s, clamp(p, lo, hi) / u.srcSize);
		}
	}
	return acc / float(n * n);
}

// One direction of the Gaussian, its edges normalised: taps outside the
// region are dropped and the others' weights renormalised. Along a fade its
// sigma is the row's strength of the recipe's (both directions take the
// output row's: a variable blur, as AppKit's ThinFilm edge has).
fragment float4 bd_blur(float4 pos [[position]], constant BU& u [[buffer(0)]],
                        texture2d<float> t [[texture(0)]]) {
	int2 n = int2(u.n);
	int2 i = min(int2(pos.xy), n - 1);
	int2 d = int2(u.dir);
	float st = strength(u, i.y);
	float sigma = u.sigma * st;
	int r = min(int(ceil(3.0 * sigma)), int(u.radius));
	float k = -0.5 / max(sigma * sigma, 1e-6);
	float4 acc = 0.0;
	float ws = 0.0;
	for (int j = -r; j <= r; j++) {
		int2 q = i + d * j;
		if (any(q < 0) || any(q >= n)) {
			continue;
		}
		float w = exp(k * float(j * j));
		acc += w * t.read(uint2(q));
		ws += w;
	}
	float4 c = acc / ws;
	if (u.last < 0.5) {
		return c;
	}
	float3 m = c.rgb;
	float mx = u.tint.a * st, sat = 1.0 + (u.sat - 1.0) * st, add = u.add * st;
	if (u.under > 0.5) {
		m = mix(m, u.tint.rgb, mx);
	}
	float l = dot(m, float3(0.2126, 0.7152, 0.0722));
	m = clamp(l + sat * (m - l) + add, 0.0, 1.0);
	if (u.under < 0.5) {
		m = mix(m, clamp(u.tint.rgb + add, 0.0, 1.0), mx);
	}
	return float4(m, 1.0);
}
`

var (
	rsSetFBytes    = objc.Sel("setFragmentBytes:length:atIndex:")
	rsDrawSimple   = objc.Sel("drawPrimitives:vertexStart:vertexCount:")
	rsUsage        = objc.Sel("usage")
	bdUniform      bdUniforms // the passes' constants (a global: C reads it)
	errBackdropMSL = errors.New("gpu: backdrop shader entry points missing")
)

// bdUniforms mirrors backdropMSL's BU (112 bytes).
type bdUniforms struct {
	src     [4]float32
	tint    [4]float32
	srcSize [2]float32
	n       [2]float32
	vp      [2]float32
	dir     [2]float32
	sigma   float32
	radius  float32
	down    float32
	sat     float32
	add     float32
	last    float32
	under   float32
	fades   float32
	fade    [2]float32
	_       [2]float32
}

var _ = [1]struct{}{}[unsafe.Sizeof(bdUniforms{})-112]

// backdropKit is the renderer's backdrop state.
type backdropKit struct {
	tried, ok      bool    // the pipelines were compiled (tried), successfully (ok)
	err            error   // why not
	down, blur     objc.ID // pipelines
	tex            [2]objc.ID
	tw, th         int // the textures' size
	peakW, peakH   int // largest demand during calm frames
	calm           int
	rpd, color0    objc.ID
	load, loadCol0 objc.ID // the main pass resumed: load, store
}

// Backdrops reports whether the renderer blurs ModeBackdrop quads (Metal
// does, into a target with shader-read usage; elsewhere it draws their
// fallback colour).
func (r *Renderer) Backdrops() bool { return true }

// initBackdrops compiles the backdrop pipelines once; on failure backdrops
// draw their fallback.
func (r *Renderer) initBackdrops() bool {
	bd := &r.bd
	if bd.tried {
		return bd.ok
	}
	bd.tried = true
	if bd.err = r.buildBackdrops(); bd.err != nil {
		return false // the backdrops draw their fallback colour
	}
	bd.ok = true
	return true
}

func (r *Renderer) buildBackdrops() error {
	bd := &r.bd
	// the pipelines: Prewarm's, or built here
	if bd.down, bd.blur = warmBackdrops(r.device); bd.down == 0 {
		var fail buildFail
		switch bd.down, bd.blur, fail = backdropPipelines(objc.F, r.device, &nsErrorOut); fail {
		case buildCompile:
			return fmt.Errorf("gpu: compiling the backdrop shaders: %s", nsErrorText())
		case buildEntry:
			return errBackdropMSL
		case buildPipeline:
			return fmt.Errorf("gpu: creating a backdrop pipeline: %s", nsErrorText())
		}
	}
	bd.rpd = objc.Retain(objc.Send(clsRPD, rsRPD))
	bd.color0 = objc.Send1(objc.Send(bd.rpd, rsColorAttach), rsObjectAt, 0)
	objc.Send1(bd.color0, rsSetLoad, 0)  // DontCare: each pass writes what it reads
	objc.Send1(bd.color0, rsSetStore, 1) // Store
	bd.load = objc.Retain(objc.Send(clsRPD, rsRPD))
	bd.loadCol0 = objc.Send1(objc.Send(bd.load, rsColorAttach), rsObjectAt, 0)
	objc.Send1(bd.loadCol0, rsSetLoad, 1)  // Load
	objc.Send1(bd.loadCol0, rsSetStore, 1) // Store
	return nil
}

// readable reports whether target (an id<MTLTexture>) can be sampled: a
// drawable of a framebufferOnly layer cannot.
func readable(target uintptr) bool {
	return uintptr(objc.Send(objc.ID(target), rsUsage))&1 != 0 // MTLTextureUsageShaderRead
}

// prepareBackdrops readies the frame's backdrops in inst, the ring slot's
// copy of quads. Those it blurs stay backdrops: it returns true if there are
// any, sizes the textures for them and writes the fills of their regions
// with their Base into pre (at most len(pre)), returning how many. Any
// other it turns into a shape (backdropShape): without the blur, without a
// recipe, over nothing with a Base, or with an empty region.
func (r *Renderer) prepareBackdrops(target uintptr, w, h int, list *DrawList, inst, pre []Quad) (bool, int) {
	canRead := readable(target)
	needW, needH := 0, 0
	for i := range inst {
		q := &inst[i]
		if q.Mode != ModeBackdrop {
			continue
		}
		b, ok := list.backdropOf(q)
		x0, y0, x1, y1, inside := backdropRegion(q, w, h)
		emptyBase := ok && b.Base&0xFF != 0 && backdropOverNothing(list.Quads, i)
		if !canRead || !ok || !inside || emptyBase {
			backdropFallback(q, b, emptyBase) // outside the target and clip, the shape draws nothing
			continue
		}
		d := backdropDown(&b)
		needW = max(needW, (x1-x0+d-1)/d+1)
		needH = max(needH, (y1-y0+d-1)/d+1)
	}
	if needW == 0 {
		r.sizeBackdrops(0, 0, w, h)
		return false, 0
	}
	if !r.initBackdrops() {
		backdropsAsShapes(inst, list)
		return false, 0
	}
	r.sizeBackdrops(needW, needH, w, h)
	n := 0
	for i := range inst {
		q := &inst[i]
		if q.Mode != ModeBackdrop || n == len(pre) {
			continue
		}
		if b, _ := list.backdropOf(q); b.Base&0xFF != 0 {
			x0, y0, x1, y1, _ := backdropRegion(q, w, h)
			c := b.Base | 0xFF
			pre[n] = Quad{Rect: [4]float32{float32(x0), float32(y0), float32(x1), float32(y1)}, Color: [4]uint32{c, c, c, c}}
			if q.Radii != ([4]float32{}) {
				// a rounded backdrop (a sheet) fills its own shape, so that
				// nothing of the fill shows past its corners where what is
				// over it is translucent (a window's material)
				pre[n].Rect, pre[n].Clip, pre[n].Radii = q.Rect, q.Clip, q.Radii
			}
			if from, to, ok := fadeSpan(q, &b); ok {
				fadeShape(&pre[n], from, to) // no edge where it ends and nothing is drawn
			}
			n++
		}
	}
	return true, n
}

// sizeBackdrops shares sufficient capacity across targets. After 60 frames
// using at most a quarter of its area it shrinks, keeping growth headroom.
func (r *Renderer) sizeBackdrops(w, h, _, _ int) {
	bd := &r.bd
	if bd.tex[0] == 0 && (w == 0 || h == 0) {
		return
	}
	if w > bd.tw || h > bd.th {
		w, h = max(w, bd.tw), max(h, bd.th)
		bd.peakW, bd.peakH, bd.calm = 0, 0, 0
	} else {
		if w*h > bd.tw*bd.th/4 {
			bd.peakW, bd.peakH, bd.calm = 0, 0, 0
			return
		}
		bd.peakW, bd.peakH = max(w, bd.peakW), max(h, bd.peakH)
		bd.calm++
		if bd.calm < instanceCalmFrames {
			return
		}
		w, h = min(bd.tw, 2*bd.peakW), min(bd.th, 2*bd.peakH)
		bd.peakW, bd.peakH, bd.calm = 0, 0, 0
	}
	w, h = sizeClass(w, 32), sizeClass(h, 32)
	if bd.tex[0] != 0 && w == bd.tw && h == bd.th {
		return
	}
	for i, t := range bd.tex {
		if t != 0 {
			r.retiring = append(r.retiring, retiredTex{t, r.serial, 0})
		}
		d := objc.Send4(clsTexDesc, rsTexDesc, mtlBGRA8Unorm, uintptr(w), uintptr(h), 0)
		objc.Send1(d, rsSetUsage, 1|4) // ShaderRead | RenderTarget
		objc.Send1(d, rsSetStorage, 2) // Private
		bd.tex[i] = objc.Send1(r.device, rsNewTexture, uintptr(d))
		if bd.tex[i] == 0 {
			panic("gpu: backdrop texture creation failed")
		}
	}
	bd.tw, bd.th = w, h
}

// encodeBackdrop encodes the passes that blur backdrop quad q (inst, its
// ring copy, gets the texel rectangle the composite samples) of a w×h
// target into cmdBuf, between two encoders of the main pass. The result is
// in the first texture.
func (r *Renderer) encodeBackdrop(cmdBuf objc.ID, target uintptr, w, h int, q *Quad, b *Backdrop, inst *Quad) {
	bd := &r.bd
	x0, y0, x1, y1, _ := backdropRegion(q, w, h)
	d := backdropDown(b)
	fd := float32(d)
	nw, nh := (x1-x0+d-1)/d, (y1-y0+d-1)/d
	sigma := max(b.Sigma, 0) / fd
	u := &bdUniform
	*u = bdUniforms{
		src:     [4]float32{float32(x0), float32(y0), float32(x1), float32(y1)},
		tint:    [4]float32{float32(b.Tint>>24) / 255, float32(b.Tint>>16&0xFF) / 255, float32(b.Tint>>8&0xFF) / 255, min(max(b.Mix, 0), 1)},
		srcSize: [2]float32{float32(w), float32(h)},
		n:       [2]float32{float32(nw), float32(nh)},
		vp:      [2]float32{float32(bd.tw), float32(bd.th)},
		sigma:   sigma,
		radius:  float32(math.Ceil(float64(3 * sigma))),
		down:    fd,
		sat:     b.Saturation,
		add:     b.Add,
		under:   b2f(b.TintUnder),
	}
	// a fade: the passes ease the recipe off along it, and the composite
	// (mode 3 of quadMSL) mixes into what is under it, reading where from the
	// ring copy's Border and Soft (unused by a backdrop)
	inst.Border, inst.Soft = 0, 0
	if from, to, ok := fadeSpan(q, b); ok {
		u.fades, u.fade = 1, [2]float32{from, to}
		inst.Border, inst.Soft = from, to
	}
	r.backdropPass(cmdBuf, bd.tex[0], bd.down, objc.ID(target))
	u.dir = [2]float32{1, 0}
	r.backdropPass(cmdBuf, bd.tex[1], bd.blur, bd.tex[0])
	u.dir, u.last = [2]float32{0, 1}, 1
	r.backdropPass(cmdBuf, bd.tex[0], bd.blur, bd.tex[1])
	// the composite samples (p - region origin) / down
	inst.UV = [4]float32{(q.Rect[0] - float32(x0)) / fd, (q.Rect[1] - float32(y0)) / fd,
		(q.Rect[2] - float32(x0)) / fd, (q.Rect[3] - float32(y0)) / fd}
}

// backdropPass draws one of a backdrop's passes into dst, reading src.
func (r *Renderer) backdropPass(cmdBuf, dst, pipe, src objc.ID) {
	bd := &r.bd
	objc.Send1(bd.color0, rsSetTexture, uintptr(dst))
	enc := objc.Send1(cmdBuf, rsEncoder, uintptr(bd.rpd))
	objc.Send1(enc, rsSetPipeline, uintptr(pipe))
	objc.Send3(enc, rsSetVBytes, uintptr(unsafe.Pointer(&bdUniform)), unsafe.Sizeof(bdUniform), 0)
	objc.Send3(enc, rsSetFBytes, uintptr(unsafe.Pointer(&bdUniform)), unsafe.Sizeof(bdUniform), 0)
	objc.Send2(enc, rsSetFTexture, uintptr(src), 0)
	objc.Send2(enc, rsSetFSampler, uintptr(r.sampler), 0)
	objc.Send3(enc, rsDrawSimple, 4, 0, 4) // MTLPrimitiveTypeTriangleStrip
	objc.Send(enc, rsEndEncoding)
	objc.Send1(bd.color0, rsSetTexture, 0)
}

// b2f is 1 for true.
func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// destroyBackdrops releases the backdrop state.
func (r *Renderer) destroyBackdrops() {
	bd := &r.bd
	for _, o := range []objc.ID{bd.tex[0], bd.tex[1], bd.down, bd.blur, bd.rpd, bd.load} {
		if o != 0 {
			objc.Release(o)
		}
	}
	*bd = backdropKit{tried: true}
}
