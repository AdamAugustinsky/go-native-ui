//go:build linux

package gpu

// GLSL for the one instanced quad pipeline (see drawlist.go for the Quad
// contract and shaders_darwin.go for the reference MSL this is ported from;
// the two must stay in step).
//
// Every Quad field is a per-instance vertex attribute (divisor 1) read
// straight from the 108-byte instance record, at the offsets in attribs in
// renderer_linux.go. The vertex stage emits a 4-vertex triangle strip per
// instance from gl_VertexID, unpacks and premultiplies the colours once, and
// hands everything to the fragment stage as flat varyings. The fragment
// stage evaluates the rounded-rect SDF, border, shadow, clip and fade
// analytically and returns premultiplied alpha for One / OneMinusSrcAlpha
// blending.
//
// Coordinates are pixels, origin top-left, y down, like the Metal shader.
// OpenGL's window origin is bottom-left, so the vertex stage flips y into
// clip space (the same formula as MSL) and the fragment stage flips
// gl_FragCoord.y back with the viewport height; fragment positions are pixel
// centres (x+0.5, y+0.5) exactly as on Metal. The image therefore appears
// the right way up in a window and in an FBO; only glReadPixels, which
// returns the bottom row first, needs a row flip (RenderToRGBA does it).
//
// The #version line (and for GLES the precision statements) is prepended by
// the renderer.

const glslHeader330 = "#version 330 core\n"

const glslHeaderES300 = "#version 300 es\nprecision highp float;\nprecision highp int;\nprecision highp sampler2D;\n"

const quadVS = `
layout(location = 0) in vec4 aRect;
layout(location = 1) in vec4 aClip;
layout(location = 2) in vec4 aUV;
layout(location = 3) in uvec4 aColor;   // TL, TR, BL, BR
layout(location = 4) in vec4 aRadii;    // TL, TR, BR, BL
layout(location = 5) in float aBorder;
layout(location = 6) in uint aBorderColor;
layout(location = 7) in float aSoft;
layout(location = 8) in vec2 aFadeX;
layout(location = 9) in uint aMode;     // 0 shape, 1 mask R8, 2 RGBA

uniform vec2 uViewport;

flat out vec4 vRect;
flat out vec4 vClip;
flat out vec4 vUV;
flat out vec4 vRadii;
flat out vec4 vC0;
flat out vec4 vC1;
flat out vec4 vC2;
flat out vec4 vC3;
flat out vec4 vBorderColor;
flat out vec4 vParams;   // border, soft, fadeX.x, fadeX.y
flat out uint vMode;
flat out uint vUniformColor;

vec4 unpackRGBA(uint c) {
	return vec4(float((c >> 24) & 255u), float((c >> 16) & 255u),
	            float((c >> 8) & 255u), float(c & 255u)) * (1.0 / 255.0);
}

vec4 premul(vec4 c) { return vec4(c.rgb * c.a, c.a); }

void main() {
	// Geometry grows so the AA fringe / shadow tail has pixels to land on.
	float pad = 0.0;
	if (aMode == 0u) {
		pad = aSoft > 0.0 ? ceil(3.0 * aSoft) + 1.0 : 1.0;
	} else if (any(greaterThan(aRadii, vec4(0.0)))) {
		pad = 1.0;
	}
	// Outside this half-pixel fringe, clip/fade coverage is exactly zero.
	vec2 lo = aRect.xy - pad, hi = aRect.zw + pad;
	if (any(notEqual(aClip, vec4(0.0)))) { lo = max(lo, aClip.xy - 0.5); hi = min(hi, aClip.zw + 0.5); }
	if (any(notEqual(aFadeX, vec2(0.0)))) { hi.x = min(hi.x, aFadeX.y + 0.5); }
	vec2 c = vec2(float(gl_VertexID & 1), float(gl_VertexID >> 1));
	vec2 p = mix(lo, max(hi, lo), c);
	gl_Position = vec4(p / uViewport * vec2(2.0, -2.0) + vec2(-1.0, 1.0), 0.0, 1.0);

	vRect = aRect;
	vClip = aClip;
	vUV = aUV;
	vRadii = aRadii;
	vC0 = premul(unpackRGBA(aColor.x));
	vC1 = premul(unpackRGBA(aColor.y));
	vC2 = premul(unpackRGBA(aColor.z));
	vC3 = premul(unpackRGBA(aColor.w));
	vBorderColor = premul(unpackRGBA(aBorderColor));
	vParams = vec4(aBorder, aSoft, aFadeX.x, aFadeX.y);
	vMode = aMode;
	vUniformColor = all(equal(aColor, aColor.xxxx)) ? 1u : 0u;
}
`

const quadFS = `
flat in vec4 vRect;
flat in vec4 vClip;
flat in vec4 vUV;
flat in vec4 vRadii;
flat in vec4 vC0;
flat in vec4 vC1;
flat in vec4 vC2;
flat in vec4 vC3;
flat in vec4 vBorderColor;
flat in vec4 vParams;
flat in uint vMode;
flat in uint vUniformColor;

uniform vec2 uViewport;
uniform sampler2D uTex;

out vec4 oColor;

// Signed distance to a rounded rect with per-corner radii (Inigo Quilez,
// "distance functions", sdRoundedBox). Negative inside.
float rrect_sdf(vec2 p, vec4 rect, vec4 radii) {
	vec2 c = (rect.xy + rect.zw) * 0.5;
	vec2 h = (rect.zw - rect.xy) * 0.5;
	vec2 q = p - c;
	float r = q.x < 0.0 ? (q.y < 0.0 ? radii.x : radii.w)
	                    : (q.y < 0.0 ? radii.y : radii.z);
	r = min(r, min(h.x, h.y));
	vec2 d = abs(q) - h + r;
	return length(max(d, 0.0)) + min(max(d.x, d.y), 0.0) - r;
}

// Gaussian-blurred rounded rectangle, after Evan Wallace, "Fast Rounded
// Rectangle Shadows" (madebyevan.com/shaders/fast-rounded-rectangle-shadows/):
// the x integral of the gaussian over the rounded box is closed-form (erf,
// polynomial approximation); the y integral is a 4-sample sum over +-3 sigma.
// A single corner radius is used (the one of the point's quadrant).
vec2 erf2(vec2 x) {
	vec2 s = sign(x), a = abs(x);
	x = 1.0 + (0.278393 + (0.230389 + 0.078108 * (a * a)) * a) * a;
	x *= x;
	return s - s / (x * x);
}

float gaussian(float x, float sigma) {
	return exp(-(x * x) / (2.0 * sigma * sigma)) / (2.50662827463 * sigma);
}

float shadow_x(float x, float y, float sigma, float corner, vec2 h) {
	float delta = min(h.y - corner - abs(y), 0.0);
	float curved = h.x - corner + sqrt(max(0.0, corner * corner - delta * delta));
	vec2 integral = 0.5 + 0.5 * erf2((x + vec2(-curved, curved)) * (0.70710678 / sigma));
	return integral.y - integral.x;
}

float rrect_shadow(vec2 p, vec4 rect, vec4 radii, float sigma) {
	vec2 c = (rect.xy + rect.zw) * 0.5;
	vec2 h = (rect.zw - rect.xy) * 0.5;
	p -= c;
	float r = p.x < 0.0 ? (p.y < 0.0 ? radii.x : radii.w)
	                    : (p.y < 0.0 ? radii.y : radii.z);
	r = min(r, min(h.x, h.y));
	float lo = p.y - h.y, hi = p.y + h.y;
	float start = clamp(-3.0 * sigma, lo, hi);
	float stop = clamp(3.0 * sigma, lo, hi);
	float dy = (stop - start) * 0.25;
	float y = start + dy * 0.5;
	float v = 0.0;
	for (int i = 0; i < 4; i++) {
		v += shadow_x(p.x, p.y - y, sigma, r, h) * gaussian(y, sigma) * dy;
		y += dy;
	}
	return clamp(v, 0.0, 1.0);
}

void main() {
	vec4 rect = vRect;
	vec4 clip = vClip;
	vec4 uv = vUV;
	vec4 radii = vRadii;
	float border = vParams.x;
	float soft = vParams.y;
	vec2 fadeX = vParams.zw;
	// Pixel centre, y down from the top (gl_FragCoord's origin is bottom-left).
	vec2 p = vec2(gl_FragCoord.x, uViewport.y - gl_FragCoord.y);

	// Four-corner colour, interpolated premultiplied.
	vec2 size = max(rect.zw - rect.xy, vec2(1e-3));
	vec2 t = clamp((p - rect.xy) / size, 0.0, 1.0);
	vec4 col = vC0;
	if (vUniformColor == 0u) {
		col = mix(mix(vC0, vC1, t.x), mix(vC2, vC3, t.x), t.y);
	}

	vec4 res;
	if (vMode == 0u) {
		if (soft > 0.0) {
			res = col * rrect_shadow(p, rect, radii, soft);
		} else {
			float sd = rrect_sdf(p, rect, radii);
			float outer = clamp(0.5 - sd, 0.0, 1.0);
			if (border > 0.0) {
				float inner = clamp(0.5 - (sd + border), 0.0, 1.0);
				col = mix(vBorderColor, col, inner);
			}
			res = col * outer;
		}
	} else {
		float cov = 1.0;
		if (any(greaterThan(radii, vec4(0.0)))) {
			cov = clamp(0.5 - rrect_sdf(p, rect, radii), 0.0, 1.0);
		}
		vec2 texel = uv.xy + (p - rect.xy) * ((uv.zw - uv.xy) / size);
		vec4 s = texture(uTex, texel / vec2(textureSize(uTex, 0)));
		res = (vMode == 1u ? col * s.r : s * col.a) * cov;
	}

	if (any(notEqual(clip, vec4(0.0)))) {
		vec2 a = clamp(p - clip.xy + 0.5, 0.0, 1.0);
		vec2 b = clamp(clip.zw - p + 0.5, 0.0, 1.0);
		res *= a.x * a.y * b.x * b.y;
	}
	if (fadeX.x != 0.0 || fadeX.y != 0.0) {
		res *= clamp((fadeX.y - p.x) / max(fadeX.y - fadeX.x, 1e-4), 0.0, 1.0);
	}
	oColor = res;
}
`
