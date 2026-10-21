//go:build windows

package gpu

// quadHLSL is the Direct3D 11 port of the Metal shader in shaders_darwin.go:
// one instanced quad pipeline (see drawlist.go for the Quad contract). It is
// compiled at startup with D3DCompile (vs_4_0 / ps_4_0, so Direct3D feature
// level 10_0 hardware is enough) until CI-precompiled DXBC replaces it
// (docs/PHASE2-PLAN.md step 4).
//
// Differences from the MSL, all forced by the API:
//
//   - The Quad record is not read from a buffer in the shaders. It is a
//     per-instance vertex buffer (stride quadSize, one input element per
//     field; the layout is built in renderer_windows.go), which makes
//     StartInstanceLocation work for batches: SV_InstanceID does not include
//     it in Direct3D, so a structured buffer would need a constant per draw.
//     The vertex shader forwards the fields the pixel shader needs as
//     nointerpolation (flat) outputs.
//   - Texture size comes from Texture2D.GetDimensions and the sample is
//     SampleLevel(..., 0): the call is inside a branch, where implicit
//     derivatives are not allowed.
//   - mix → lerp, fragment position → SV_Position (pixel centres at x+0.5,
//     like Metal), the viewport arrives in a constant buffer.
//
// The maths (SDF, AA, border, erf shadow, bilinear premultiplied corner
// colours, clip, FadeX) is line for line the MSL, so both back ends render
// the same pixels.
const quadHLSL = `
cbuffer Globals : register(b0) {
	float2 viewport;
	float2 vpad;
};

Texture2D<float4> atlas : register(t0);
SamplerState smp : register(s0);

struct VIn {
	uint   vid         : SV_VertexID;
	float4 rect        : QRECT;
	float4 clip        : QCLIP;
	float4 uv          : QUV;
	uint4  color       : QCOLOR;   // TL, TR, BL, BR
	float4 radii       : QRADII;   // TL, TR, BR, BL
	float  border      : QBORDER;
	uint   borderColor : QBORDERCOLOR;
	float  soft        : QSOFT;
	float2 fadeX       : QFADEX;
	uint   mode        : QMODE;    // 0 shape, 1 mask R8, 2 RGBA
	uint   tex         : QTEX;
};

struct VOut {
	float4 pos : SV_Position;
	nointerpolation float4 rect   : QRECT;
	nointerpolation float4 clip   : QCLIP;
	nointerpolation float4 uv     : QUV;
	nointerpolation uint4  color  : QCOLOR;
	nointerpolation float4 radii  : QRADII;
	nointerpolation float4 misc   : QMISC;   // border, soft, fadeX.x, fadeX.y
	nointerpolation uint2  flags  : QFLAGS;  // borderColor, mode
};

VOut vs_main(VIn i) {
	// Geometry grows so the AA fringe / shadow tail has pixels to land on.
	float pad = 0.0;
	if (i.mode == 0u) {
		pad = i.soft > 0.0 ? ceil(3.0 * i.soft) + 1.0 : 1.0;
	} else if (any(i.radii > 0.0)) {
		pad = 1.0;
	}
	// Outside this half-pixel fringe, clip/fade coverage is exactly zero.
	float2 lo = i.rect.xy - pad, hi = i.rect.zw + pad;
	if (any(i.clip != 0.0)) { lo = max(lo, i.clip.xy - 0.5); hi = min(hi, i.clip.zw + 0.5); }
	if (any(i.fadeX != 0.0)) { hi.x = min(hi.x, i.fadeX.y + 0.5); }
	float2 c = float2(float(i.vid & 1u), float(i.vid >> 1u));
	float2 p = lerp(lo, max(hi, lo), c);
	VOut o;
	o.pos = float4(p / viewport * float2(2.0, -2.0) + float2(-1.0, 1.0), 0.0, 1.0);
	o.rect = i.rect;
	o.clip = i.clip;
	o.uv = i.uv;
	o.color = i.color;
	o.radii = i.radii;
	o.misc = float4(i.border, i.soft, i.fadeX.x, i.fadeX.y);
	o.flags = uint2(i.borderColor, i.mode);
	return o;
}

float4 unpack_rgba(uint c) {
	return float4(float((c >> 24u) & 255u), float((c >> 16u) & 255u),
	              float((c >> 8u) & 255u), float(c & 255u)) * (1.0 / 255.0);
}

float4 premul(float4 c) { return float4(c.rgb * c.a, c.a); }

// Signed distance to a rounded rect with per-corner radii (Inigo Quilez,
// "distance functions", sdRoundedBox). Negative inside.
float rrect_sdf(float2 p, float4 rect, float4 radii) {
	float2 c = (rect.xy + rect.zw) * 0.5;
	float2 h = (rect.zw - rect.xy) * 0.5;
	float2 q = p - c;
	float r = q.x < 0.0 ? (q.y < 0.0 ? radii.x : radii.w)
	                    : (q.y < 0.0 ? radii.y : radii.z);
	r = min(r, min(h.x, h.y));
	float2 d = abs(q) - h + r;
	return length(max(d, 0.0)) + min(max(d.x, d.y), 0.0) - r;
}

// Gaussian-blurred rounded rectangle, after Evan Wallace, "Fast Rounded
// Rectangle Shadows" (madebyevan.com/shaders/fast-rounded-rectangle-shadows/):
// the x integral of the gaussian over the rounded box is closed-form (erf,
// polynomial approximation); the y integral is a 4-sample sum over +-3 sigma.
// A single corner radius is used (the one of the point's quadrant).
float2 erf2(float2 x) {
	float2 s = sign(x);
	float2 a = abs(x);
	x = 1.0 + (0.278393 + (0.230389 + 0.078108 * (a * a)) * a) * a;
	x *= x;
	return s - s / (x * x);
}

float gaussian(float x, float sigma) {
	return exp(-(x * x) / (2.0 * sigma * sigma)) / (2.50662827463 * sigma);
}

float shadow_x(float x, float y, float sigma, float corner, float2 h) {
	float delta = min(h.y - corner - abs(y), 0.0);
	float curved = h.x - corner + sqrt(max(0.0, corner * corner - delta * delta));
	float2 integral = 0.5 + 0.5 * erf2((x + float2(-curved, curved)) * (0.70710678 / sigma));
	return integral.y - integral.x;
}

float rrect_shadow(float2 p, float4 rect, float4 radii, float sigma) {
	float2 c = (rect.xy + rect.zw) * 0.5;
	float2 h = (rect.zw - rect.xy) * 0.5;
	p -= c;
	float r = p.x < 0.0 ? (p.y < 0.0 ? radii.x : radii.w)
	                    : (p.y < 0.0 ? radii.y : radii.z);
	r = min(r, min(h.x, h.y));
	float lo = p.y - h.y;
	float hi = p.y + h.y;
	float start = clamp(-3.0 * sigma, lo, hi);
	float stop = clamp(3.0 * sigma, lo, hi);
	float dy = (stop - start) * 0.25;
	float y = start + dy * 0.5;
	float v = 0.0;
	[unroll]
	for (int k = 0; k < 4; k++) {
		v += shadow_x(p.x, p.y - y, sigma, r, h) * gaussian(y, sigma) * dy;
		y += dy;
	}
	return clamp(v, 0.0, 1.0);
}

float4 ps_main(VOut i) : SV_Target {
	float4 rect = i.rect;
	float4 clip = i.clip;
	float4 uv = i.uv;
	float4 radii = i.radii;
	float border = i.misc.x;
	float soft = i.misc.y;
	float2 fadeX = i.misc.zw;
	uint mode = i.flags.y;
	float2 p = i.pos.xy;

	// Four-corner colour, interpolated premultiplied.
	float2 size = max(rect.zw - rect.xy, 1e-3);
	float2 t = clamp((p - rect.xy) / size, 0.0, 1.0);
	float4 col;
	if (all(i.color == i.color.xxxx)) {
		col = premul(unpack_rgba(i.color.x));
	} else {
		float4 cTL = premul(unpack_rgba(i.color.x));
		float4 cTR = premul(unpack_rgba(i.color.y));
		float4 cBL = premul(unpack_rgba(i.color.z));
		float4 cBR = premul(unpack_rgba(i.color.w));
		col = lerp(lerp(cTL, cTR, t.x), lerp(cBL, cBR, t.x), t.y);
	}

	float4 res;
	if (mode == 0u) {
		if (soft > 0.0) {
			res = col * rrect_shadow(p, rect, radii, soft);
		} else {
			float sd = rrect_sdf(p, rect, radii);
			float outer = clamp(0.5 - sd, 0.0, 1.0);
			if (border > 0.0) {
				float inner = clamp(0.5 - (sd + border), 0.0, 1.0);
				float4 bc = premul(unpack_rgba(i.flags.x));
				col = lerp(bc, col, inner);
			}
			res = col * outer;
		}
	} else {
		float cov = 1.0;
		if (any(radii > 0.0)) {
			cov = clamp(0.5 - rrect_sdf(p, rect, radii), 0.0, 1.0);
		}
		uint tw, th;
		atlas.GetDimensions(tw, th);
		float2 texel = uv.xy + (p - rect.xy) * ((uv.zw - uv.xy) / size);
		float4 s = atlas.SampleLevel(smp, texel / float2(float(tw), float(th)), 0.0);
		res = (mode == 1u ? col * s.r : s * col.a) * cov;
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
