//go:build darwin

package gpu

// quadMSL is the one instanced quad pipeline (see drawlist.go for the Quad
// contract). The Quad struct below mirrors the Go struct byte for byte:
// packed_* types have 4-byte alignment, so sizeof(Quad) is 108.
//
// The vertex stage emits a triangle strip per instance; the fragment stage
// evaluates everything analytically (rounded-rect SDF, border, shadow, clip,
// fade) and returns premultiplied alpha for One / OneMinusSrcAlpha blending.
// Fragment positions are pixel centres (x+0.5, y+0.5), so a pixel-aligned
// edge at integer x has an SDF of ∓0.5 on the two pixels beside it and
// renders with exactly 0 or 1 coverage.
const quadMSL = `
#include <metal_stdlib>
using namespace metal;

struct Quad {
	packed_float4 rect;
	packed_float4 clip;
	packed_float4 uv;
	packed_uint4  color;   // TL, TR, BL, BR
	packed_float4 radii;   // TL, TR, BR, BL
	float         border;
	uint          borderColor;
	float         soft;
	packed_float2 fadeX;
	uint          mode;    // 0 shape, 1 mask R8, 2 RGBA, 3 backdrop
	uint          tex;
};

struct VOut {
	float4 pos [[position]];
	uint   inst [[flat]];
};

// The YUV conversion of the run's texture (external_darwin.go, yuvRun):
// rgb = (dot(r, v), dot(g, v), dot(b, v)) for v = (y, cb, cr, 1); planes is
// 0 for a plain texture, 2 for NV12 (CbCr in tex1), 3 for I420 (Cb in tex1,
// Cr in tex2); turns rotates the image clockwise by quarter turns.
struct YUVRun {
	packed_float4 r;
	packed_float4 g;
	packed_float4 b;
	uint planes;
	uint turns;
	uint pad0;
	uint pad1;
};

vertex VOut vs_main(uint vid [[vertex_id]],
                    uint iid [[instance_id]],
                    device const Quad* quads [[buffer(0)]],
                    constant float2& viewport [[buffer(1)]]) {
	float4 rect = quads[iid].rect;
	float4 radii = quads[iid].radii;
	uint mode = quads[iid].mode;
	float soft = quads[iid].soft;
	// Geometry grows so the AA fringe / shadow tail has pixels to land on.
	float pad = 0.0;
	if (mode == 0u) {
		pad = soft > 0.0 ? ceil(3.0 * soft) + 1.0 : 1.0;
	} else if (any(radii > 0.0)) {
		pad = 1.0;
	}
	// Outside this half-pixel fringe, clip/fade coverage is exactly zero.
	float2 lo = rect.xy - pad, hi = rect.zw + pad;
	float4 clip = quads[iid].clip;
	float2 fadeX = quads[iid].fadeX;
	if (any(clip != 0.0)) { lo = max(lo, clip.xy - 0.5); hi = min(hi, clip.zw + 0.5); }
	if (any(fadeX != 0.0)) { hi.x = min(hi.x, fadeX.y + 0.5); }
	float2 c = float2(float(vid & 1u), float(vid >> 1));
	float2 p = mix(lo, max(hi, lo), c);
	VOut o;
	o.pos = float4(p / viewport * float2(2.0, -2.0) + float2(-1.0, 1.0), 0.0, 1.0);
	o.inst = iid;
	return o;
}

static float4 unpack_rgba(uint c) {
	return float4(float((c >> 24) & 255u), float((c >> 16) & 255u),
	              float((c >> 8) & 255u), float(c & 255u)) * (1.0 / 255.0);
}

static float4 premul(float4 c) { return float4(c.rgb * c.a, c.a); }

// Signed distance to a rounded rect with per-corner radii (Inigo Quilez,
// "distance functions", sdRoundedBox). Negative inside.
static float rrect_sdf(float2 p, float4 rect, float4 radii) {
	float2 c = (rect.xy + rect.zw) * 0.5;
	float2 h = (rect.zw - rect.xy) * 0.5;
	float2 q = p - c;
	float r = q.x < 0.0 ? (q.y < 0.0 ? radii.x : radii.w)
	                    : (q.y < 0.0 ? radii.y : radii.z);
	r = min(r, min(h.x, h.y));
	float2 d = abs(q) - h + r;
	return length(max(d, 0.0)) + min(max(d.x, d.y), 0.0) - r;
}

// The continuous (squircle) corner of macOS, ShapeContinuous: the corner of
// radius r reaches CORNER_REACH * r along both edges and follows a
// superellipse |x|^n + |y|^n = 1 of exponent CORNER_N (drawlist.go; the
// constants must match). The distance is the norm's first-order estimate,
// (f - 1) / |grad f|, exact on the straight edges and good to a fraction of
// a pixel at the edge of the shape, where it is used.
#define CORNER_REACH 1.52
#define CORNER_N 3.3

static float crect_sdf(float2 p, float4 rect, float4 radii) {
	float2 c = (rect.xy + rect.zw) * 0.5;
	float2 h = (rect.zw - rect.xy) * 0.5;
	float2 q = p - c;
	float r = q.x < 0.0 ? (q.y < 0.0 ? radii.x : radii.w)
	                    : (q.y < 0.0 ? radii.y : radii.z);
	float e = min(CORNER_REACH * r, min(h.x, h.y));
	if (e <= 0.0) {
		return rrect_sdf(p, rect, radii);
	}
	float2 d = abs(q) - (h - e);
	if (d.x <= 0.0 || d.y <= 0.0) {
		return max(d.x, d.y) - e;
	}
	float2 a = d / e;
	float f = pow(pow(a.x, CORNER_N) + pow(a.y, CORNER_N), 1.0 / CORNER_N);
	float2 g = pow(a, float2(CORNER_N - 1.0)) / pow(f, CORNER_N - 1.0);
	return e * (f - 1.0) / max(length(g), 1e-3);
}

// Gaussian-blurred rounded rectangle, after Evan Wallace, "Fast Rounded
// Rectangle Shadows" (madebyevan.com/shaders/fast-rounded-rectangle-shadows/):
// the x integral of the gaussian over the rounded box is closed-form (erf,
// polynomial approximation); the y integral is a 4-sample sum over +-3 sigma.
// A single corner radius is used (the one of the point's quadrant).
static float2 erf2(float2 x) {
	float2 s = sign(x), a = abs(x);
	x = 1.0 + (0.278393 + (0.230389 + 0.078108 * (a * a)) * a) * a;
	x *= x;
	return s - s / (x * x);
}

static float gaussian(float x, float sigma) {
	return exp(-(x * x) / (2.0 * sigma * sigma)) / (2.50662827463 * sigma);
}

static float shadow_x(float x, float y, float sigma, float corner, float2 h) {
	float delta = min(h.y - corner - abs(y), 0.0);
	float curved = h.x - corner + sqrt(max(0.0, corner * corner - delta * delta));
	float2 integral = 0.5 + 0.5 * erf2((x + float2(-curved, curved)) * (0.70710678 / sigma));
	return integral.y - integral.x;
}

static float rrect_shadow(float2 p, float4 rect, float4 radii, float sigma) {
	float2 c = (rect.xy + rect.zw) * 0.5;
	float2 h = (rect.zw - rect.xy) * 0.5;
	p -= c;
	float r = p.x < 0.0 ? (p.y < 0.0 ? radii.x : radii.w)
	                    : (p.y < 0.0 ? radii.y : radii.z);
	r = min(r, min(h.x, h.y));
	if (all(abs(p) < h - (3.0 * sigma + r))) {
		return 1.0;
	}
	float lo = p.y - h.y, hi = p.y + h.y;
	float start = clamp(-3.0 * sigma, lo, hi);
	float end = clamp(3.0 * sigma, lo, hi);
	float step = (end - start) * 0.25;
	float y = start + step * 0.5;
	float v = 0.0;
	for (int i = 0; i < 4; i++) {
		v += shadow_x(p.x, p.y - y, sigma, r, h) * gaussian(y, sigma) * step;
		y += step;
	}
	return clamp(v, 0.0, 1.0);
}

// yuv_sample samples the YUV texture of the run at texel (in the turned
// image's luma texels) and converts it to opaque RGB.
static float4 yuv_sample(float2 texel, constant YUVRun& yuv, texture2d<float> tex,
                         texture2d<float> tex1, texture2d<float> tex2, sampler smp) {
	float2 dsz = float2(tex.get_width(), tex.get_height());
	if ((yuv.turns & 1u) != 0u) {
		dsz = dsz.yx;
	}
	float2 n = texel / dsz;
	if (yuv.turns == 1u) {
		n = float2(n.y, 1.0 - n.x);
	} else if (yuv.turns == 2u) {
		n = 1.0 - n;
	} else if (yuv.turns == 3u) {
		n = float2(1.0 - n.y, n.x);
	}
	float4 v = float4(tex.sample(smp, n).r, 0.0, 0.0, 1.0);
	if (yuv.planes == 2u) {
		v.yz = tex1.sample(smp, n).rg;
	} else {
		v.y = tex1.sample(smp, n).r;
		v.z = tex2.sample(smp, n).r;
	}
	float3 rgb = float3(dot(float4(yuv.r), v), dot(float4(yuv.g), v), dot(float4(yuv.b), v));
	return float4(clamp(rgb, 0.0, 1.0), 1.0);
}

fragment float4 fs_main(VOut in [[stage_in]],
                        device const Quad* quads [[buffer(0)]],
                        constant YUVRun& yuv [[buffer(1)]],
                        texture2d<float> tex [[texture(0)]],
                        texture2d<float> tex1 [[texture(1)]],
                        texture2d<float> tex2 [[texture(2)]],
                        array<texture2d<float>, 8> texs [[texture(3)]],
                        sampler smp [[sampler(0)]]) {
	float4 rect = quads[in.inst].rect;
	float4 clip = quads[in.inst].clip;
	float4 uv = quads[in.inst].uv;
	float4 radii = quads[in.inst].radii;
	float2 fadeX = quads[in.inst].fadeX;
	float border = quads[in.inst].border;
	float soft = quads[in.inst].soft;
	uint mode = quads[in.inst].mode;
	bool continuous = mode == 0u && (quads[in.inst].tex & 1u) != 0u; // ShapeContinuous
	float2 p = in.pos.xy;

	// Four-corner colour, interpolated premultiplied.
	float2 size = max(rect.zw - rect.xy, 1e-3);
	float2 t = clamp((p - rect.xy) / size, 0.0, 1.0);
	uint4 cw = uint4(quads[in.inst].color);
	float4 col;
	if (all(cw == cw.xxxx)) {
		col = premul(unpack_rgba(cw.x));
	} else {
		float4 cTL = premul(unpack_rgba(cw.x));
		float4 cTR = premul(unpack_rgba(cw.y));
		float4 cBL = premul(unpack_rgba(cw.z));
		float4 cBR = premul(unpack_rgba(cw.w));
		col = mix(mix(cTL, cTR, t.x), mix(cBL, cBR, t.x), t.y);
	}

	float4 res;
	if (mode == 0u) {
		if (soft > 0.0) {
			res = col * rrect_shadow(p, rect, radii, soft);
		} else {
			float sd = continuous ? crect_sdf(p, rect, radii) : rrect_sdf(p, rect, radii);
			float outer = clamp(0.5 - sd, 0.0, 1.0);
			if (border > 0.0) {
				float inner = clamp(0.5 - (sd + border), 0.0, 1.0);
				float4 bc = premul(unpack_rgba(quads[in.inst].borderColor));
				col = mix(bc, col, inner);
			}
			res = col * outer;
		}
	} else if (mode == 4u) {
		float2 a = p - rect.xy - uv.xy, d = uv.zw - uv.xy;
		float along = clamp(dot(a, d) / max(dot(d, d), 1e-6), 0.0, 1.0);
		float sd = length(a - d * along) - radii.x;
		res = col * clamp(0.5 - sd, 0.0, 1.0);
	} else {
		float cov = 1.0;
		if (any(radii > 0.0)) {
			cov = clamp(0.5 - rrect_sdf(p, rect, radii), 0.0, 1.0);
		}
		float2 texel = uv.xy + (p - rect.xy) * ((uv.zw - uv.xy) / size);
		float4 s;
		if (mode == 2u && yuv.planes != 0u) {
			s = yuv_sample(texel, yuv, tex, tex1, tex2, smp);
		} else if (mode == 3u) {
			s = tex.sample(smp, texel / float2(tex.get_width(), tex.get_height()));
		} else {
			uint slot = quads[in.inst].tex;
			s = texs[slot].sample(smp, texel / float2(texs[slot].get_width(), texs[slot].get_height()));
		}
		// mode 3: a backdrop, its blurred region (uv: the texels the
		// renderer put it at; backdrop_darwin.go), opaque; but along a fade
		// from y = border to y = soft (set by the renderer), where the
		// recipe's strength falls from 1 to 0 along a smoothstep, it mixes
		// into what is under it over the fade's outer half
		float a = 1.0;
		if (mode == 3u && border != soft) {
			float st = 1.0 - smoothstep(0.0, 1.0, (p.y - border) / (soft - border));
			a = smoothstep(0.0, 0.5, st);
		}
		res = (mode == 1u ? col * s.r : mode == 3u ? float4(s.rgb, 1.0) * a : s * col.a) * cov;
	}

	if (any(clip != 0.0)) {
		float2 a = clamp(p - clip.xy + 0.5, 0.0, 1.0);
		float2 b = clamp(clip.zw - p + 0.5, 0.0, 1.0);
		res *= a.x * a.y * b.x * b.y;
	}
	if (fadeX.x != 0.0 || fadeX.y != 0.0) {
		res *= clamp((fadeX.y - p.x) / max(fadeX.y - fadeX.x, 1e-4), 0.0, 1.0);
	}
	return res;
}
`
