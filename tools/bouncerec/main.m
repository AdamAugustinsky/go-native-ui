// BounceRec: dev/measurement tool (not framework code).
//
// Records the main display with ScreenCaptureKit while launching an app through
// LaunchServices (NSWorkspace, same path as `open` / a Dock click) and reports
//   1. when the app's window first shows ANY pixels,
//   2. when it first shows its COMPLETE expected content,
//   3. the timing of the Dock icon's first bounce,
// so that "complete content before the first bounce is half done" (apex) and
// "no empty/partial window frame ever reaches the screen" can be verified.
// See README.md (usage, heuristics, limits) and ARCHITECTURE.md §3.
//
// Run it inside the GUI session (over SSH use `open -W -n`):
//	open -W -n bin/BounceRec.app --args -target /abs/Target.app -expect /abs/exp.png -out /abs/r.json
//	bin/BounceRec.app/Contents/MacOS/BounceRec -selftest      # pure-logic self test
//
// Structure: (1) pure pixel/curve logic on plain buffers (unit-tested by
// -selftest, no OS dependencies), (2) PNG helpers, (3) the self test, (4) the
// Recorder that drives ScreenCaptureKit/NSWorkspace and feeds frames into (1),
// (5) main().
//
// Clock: every frame time is the sample buffer's presentation timestamp, which
// ScreenCaptureKit stamps on the HOST TIME clock (CMClockGetHostTimeClock), the
// same timeline as mach_absolute_time converted to nanoseconds. The launch
// request is stamped with CMClockMakeHostTimeFromSystemUnits(mach_absolute_time())
// so both live on one clock. The first frame reports a sanity delta
// (clockCheckMs) between "now" and its timestamp; it should be small (<~100 ms).
#import <AppKit/AppKit.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <CoreGraphics/CoreGraphics.h>
#import <ImageIO/ImageIO.h>
#include <mach/mach_time.h>
#include <math.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

// ============================================================================
// (1) Pure logic
// ============================================================================

typedef struct {
	// Expected-content match.
	int probeStep;     // grid pitch in points (-step)
	int skipW, skipH;  // top-left skip rectangle (traffic lights), window coords
	int border;        // border band skipped on all sides
	int corner;        // square at each window corner skipped (rounded corners)
	int tol;           // max per-channel diff for a probe to match (-tol)
	int slack;         // a probe also matches if any pixel within +-slack pt matches (-slack)
	double matchFrac;  // fraction of probes that must match for "complete" (-match)
	int offX, offY;    // content origin inside the window bounds; -1 = auto (centred, bottom-aligned)
	// "Window shows pixels" test.
	int diffTol;       // per-channel diff vs baseline that counts as changed (-difftol)
	double firstFrac;  // fraction of sampled pixels changed for "has pixels" (-firstfrac)
	// Dock bounce.
	int bounceTol;     // per-channel diff vs the settled frame that counts as changed (-bouncetol)
	int minCols;       // columns that must agree on the topmost changed row (-mincols)
	int minRun;        // consecutive changed rows that make a "top" (-minrun)
	int dockPad;       // points above the Dock that are stored/analysed (-dockpad)
	int tile;          // Dock icon size in points; 0 = unknown (-tile; read from com.apple.dock tilesize)
	int commonCols;    // columns sharing one top row = Dock-wide change, not an icon; 0 = 2 x icon width (-commoncols)
	double restTop;    // icon resting top row (depth); <0 = estimate (-resttop)
	int shadow;        // margin added around the target window when masking the Dock strip (-shadow)
	int motionPx;      // changed pixels between consecutive Dock strips that count as "Dock moving" (-motionpx)
	int settleMs;      // quiet time after the last Dock motion before the recording may end (-settle)
	double valleyFrac; // two bounce peaks are separate if the valley between is below this x the lower peak
	double minPeak;    // smallest peak (pt) that counts as a bounce
	double startThresh;// displacement (pt) that counts as "moving" (>1 pt per brief)
	double maxInterp;  // ms; only interpolate between samples closer than this (frames are change-driven)
} Params;

static Params defaultParams(void) {
	Params p;
	p.probeStep = 8; p.skipW = 90; p.skipH = 46; p.border = 2; p.corner = 24;
	p.tol = 24; p.slack = 1; p.matchFrac = 0.98; p.offX = -1; p.offY = -1;
	p.diffTol = 12; p.firstFrac = 0.5; // a window appearing changes most of its region; unrelated screen activity (alerts, other apps) under the bounds must not count
	p.bounceTol = 16; p.minCols = 12; p.minRun = 2; p.dockPad = 40; p.tile = 0; p.commonCols = 0;
	p.restTop = -1; p.shadow = 48; p.motionPx = 30; p.settleMs = 1500;
	p.valleyFrac = 0.35; p.minPeak = 4.0; p.startThresh = 1.0; p.maxInterp = 40.0;
	return p;
}

// BGRA image view. Coordinates passed around are display POINTS; `scale` maps
// points to buffer pixels (1.0 when the stream is configured at point size).
typedef struct { const uint8_t *p; size_t stride; int w, h; double scale; } Img;
typedef struct { int x, y, w, h; } IRect;      // points, display-relative, top-left origin
typedef struct { int w, h; uint8_t *p; } Bitmap; // tight BGRA

static inline const uint8_t *pixAt(const Img *im, int xpt, int ypt) {
	int x = (int)floor((xpt + 0.5) * im->scale), y = (int)floor((ypt + 0.5) * im->scale);
	if (x < 0 || y < 0 || x >= im->w || y >= im->h) return NULL;
	return im->p + (size_t)y * im->stride + (size_t)x * 4;
}
static inline int chanDiff(const uint8_t *a, const uint8_t *b) {
	int d0 = abs((int)a[0] - (int)b[0]), d1 = abs((int)a[1] - (int)b[1]), d2 = abs((int)a[2] - (int)b[2]);
	return d0 > d1 ? (d0 > d2 ? d0 : d2) : (d1 > d2 ? d1 : d2);
}

static inline int skipProbe(int rx, int ry, int ww, int wh, const Params *P) {
	if (rx < P->border || ry < P->border || rx >= ww - P->border || ry >= wh - P->border) return 1;
	if (rx < P->skipW && ry < P->skipH) return 1;
	int c = P->corner;
	if ((rx < c || rx >= ww - c) && (ry < c || ry >= wh - c)) return 1;
	return 0;
}

// Fraction of probe points whose captured pixel matches the expected content.
// `ex` is the expected content at 1x. It is placed inside the window bounds
// centred horizontally and bottom-aligned (the title bar sits above it), or at
// (offX, offY) if given. A window smaller than the content (still animating to
// its final size) scores 0.
static double matchFraction(const Img *f, IRect win, const Bitmap *ex, const Params *P, int *probesOut) {
	if (probesOut) *probesOut = 0;
	if (ex->w > win.w + 1 || ex->h > win.h + 1) return 0;
	int cx = P->offX >= 0 ? P->offX : (win.w - ex->w) / 2;
	int cy = P->offY >= 0 ? P->offY : win.h - ex->h;
	if (cx < 0) cx = 0;
	if (cy < 0) cy = 0;
	int total = 0, ok = 0;
	for (int ey = P->probeStep / 2; ey < ex->h; ey += P->probeStep) {
		for (int exx = P->probeStep / 2; exx < ex->w; exx += P->probeStep) {
			int rx = cx + exx, ry = cy + ey;
			if (skipProbe(rx, ry, win.w, win.h, P)) continue;
			total++;
			const uint8_t *e = ex->p + ((size_t)ey * ex->w + exx) * 4;
			int hit = 0;
			for (int dy = -P->slack; dy <= P->slack && !hit; dy++)
				for (int dx = -P->slack; dx <= P->slack; dx++) {
					const uint8_t *c = pixAt(f, win.x + rx + dx, win.y + ry + dy);
					if (c && chanDiff(c, e) <= P->tol) { hit = 1; break; }
				}
			ok += hit;
		}
	}
	if (probesOut) *probesOut = total;
	return total ? (double)ok / total : 0;
}

// Fraction of sampled pixels inside the window bounds that differ from baseline.
static double diffFraction(const Img *f, const Img *base, IRect win, const Params *P) {
	int total = 0, diff = 0;
	for (int y = P->border; y < win.h - P->border; y += 4)
		for (int x = P->border; x < win.w - P->border; x += 4) {
			const uint8_t *a = pixAt(f, win.x + x, win.y + y), *b = pixAt(base, win.x + x, win.y + y);
			if (!a || !b) continue;
			total++;
			if (chanDiff(a, b) > P->diffTol) diff++;
		}
	return total ? (double)diff / total : 0;
}

// ---- Dock geometry --------------------------------------------------------
// The Dock is described in its own axes so bottom/left/right share one code
// path: `along` runs along the Dock, `depth` runs from the screen edge into the
// screen (a bounce increases depth). Rows [0, thickness) are the Dock itself
// (thickness = NSScreen visibleFrame inset), rows above are the dockPad band
// where a high bounce pokes out over the desktop.
typedef struct { int edge; int thickness; int sw, sh; } DockGeom; // edge: 0 bottom, 1 left, 2 right
static void dockXY(const DockGeom *g, int along, int depth, int *x, int *y) {
	switch (g->edge) {
	case 1: *x = depth; *y = along; break;
	case 2: *x = g->sw - 1 - depth; *y = along; break;
	default: *x = along; *y = g->sh - 1 - depth; break;
	}
}
static int dockAlongLen(const DockGeom *g) { return g->edge == 0 ? g->sw : g->sh; }
// Strip depth stored/analysed per frame: the Dock plus dockPad points above it.
static int dockDepth(const DockGeom *g, const Params *P) {
	int d = g->thickness + P->dockPad, lim = g->edge == 0 ? g->sh : g->sw;
	if (d > lim) d = lim;
	return d > 500 ? 500 : d;
}

// One captured frame of the Dock strip only (never the rest of the screen),
// stored in Dock axes, column-major: px[(along*D + depth)*4] (BGRA).
typedef struct {
	double tMs;           // ms after the launch request
	uint8_t *px;
	int hasMask; IRect mask; // target window + shadow (display points): ignored when diffing
} DockFrame;

static void copyDockStrip(const Img *f, const DockGeom *g, int D, uint8_t *dst) {
	int along = dockAlongLen(g);
	for (int a = 0; a < along; a++)
		for (int d = 0; d < D; d++) {
			int x, y;
			dockXY(g, a, d, &x, &y);
			const uint8_t *p = pixAt(f, x, y);
			uint8_t *o = dst + ((size_t)a * D + d) * 4;
			if (p) memcpy(o, p, 4); else memset(o, 0, 4);
		}
}

// Depth range [lo, hi] masked in column `a` by the frame's window rect.
static int maskRange(const DockGeom *g, const DockFrame *fr, int a, int *lo, int *hi) {
	if (!fr->hasMask) return 0;
	IRect r = fr->mask;
	int a0, a1, d0, d1; // along [a0, a1), depth [d0, d1]
	switch (g->edge) {
	case 1: a0 = r.y; a1 = r.y + r.h; d0 = r.x; d1 = r.x + r.w - 1; break;
	case 2: a0 = r.y; a1 = r.y + r.h; d0 = g->sw - (r.x + r.w); d1 = g->sw - 1 - r.x; break;
	default: a0 = r.x; a1 = r.x + r.w; d0 = g->sh - (r.y + r.h); d1 = g->sh - 1 - r.y; break;
	}
	if (a < a0 || a >= a1) return 0;
	*lo = d0; *hi = d1;
	return 1;
}

// Topmost row (largest depth <= start) of a run of >= minRun rows that differ
// from the reference frame `ff`; -1 if none. Masked rows (either frame) break runs.
static int scanColumn(const DockFrame *fk, const DockFrame *ff, const DockGeom *g, int a, int D, int start, const Params *P) {
	int l1 = 0, h1 = -1, l2 = 0, h2 = -1;
	int m1 = maskRange(g, fk, a, &l1, &h1), m2 = maskRange(g, ff, a, &l2, &h2);
	const uint8_t *pa = fk->px + (size_t)a * D * 4, *pf = ff->px + (size_t)a * D * 4;
	int run = 0;
	for (int d = start; d >= 0; d--) {
		if ((m1 && d >= l1 && d <= h1) || (m2 && d >= l2 && d <= h2)) { run = 0; continue; }
		if (chanDiff(pa + d * 4, pf + d * 4) > P->bounceTol) {
			if (++run >= P->minRun) return d + P->minRun - 1;
		} else run = 0;
	}
	return -1;
}

// Number of pixels that differ between two strips (stops counting at stopAt).
static int dockChangedPixels(const DockFrame *x, const DockFrame *y, const DockGeom *g, int D, int tol, int stopAt) {
	int along = dockAlongLen(g), n = 0;
	for (int a = 0; a < along; a++) {
		int l1 = 0, h1 = -1, l2 = 0, h2 = -1;
		int m1 = maskRange(g, x, a, &l1, &h1), m2 = maskRange(g, y, a, &l2, &h2);
		const uint8_t *pa = x->px + (size_t)a * D * 4, *pb = y->px + (size_t)a * D * 4;
		for (int d = 0; d < D; d++) {
			if ((m1 && d >= l1 && d <= h1) || (m2 && d >= l2 && d <= h2)) continue;
			if (chanDiff(pa + d * 4, pb + d * 4) > tol && ++n >= stopAt) return n;
		}
	}
	return n;
}

// Common-mode rejection. While many columns (>= commonCols, i.e. more than an
// icon's width) share the same topmost changed row, that row is not the new icon:
// it is the Dock re-laying out (every icon sliding, the glass edge/highlight
// shifting). Re-scan those columns below that row. A single icon (narrower than
// commonCols) is never peeled, so the bouncing icon's own top survives.
static void peelTops(int16_t *tops, const DockFrame *fk, const DockFrame *ff, const DockGeom *g, int D, int commonCols, const Params *P) {
	int along = dockAlongLen(g);
	int hist[512];
	for (int iter = 0; iter < 80; iter++) {
		memset(hist, 0, sizeof hist);
		for (int a = 0; a < along; a++) if (tops[a] >= 0) hist[tops[a]]++;
		int big = -1;
		for (int d = D - 1; d >= 0; d--) if (hist[d] >= commonCols) { big = d; break; }
		if (big < 0) return;
		for (int a = 0; a < along; a++)
			if (tops[a] == big) tops[a] = (int16_t)(big > 0 ? scanColumn(fk, ff, g, a, D, big - 1, P) : -1);
	}
}

// Depth reached by at least minCols of the columns inside (or outside) [c0, c1].
static int reachDepth(const int16_t *tops, int along, int c0, int c1, int inside, int D, int minCols) {
	int hist[512];
	memset(hist, 0, sizeof hist);
	for (int a = 0; a < along; a++) {
		int in = a >= c0 && a <= c1;
		if (in != inside || tops[a] < 0) continue;
		hist[tops[a]]++;
	}
	int cum = 0;
	for (int d = D - 1; d >= 0; d--) { cum += hist[d]; if (cum >= minCols) return d; }
	return -1;
}

// Widest/heaviest run of columns with cnt >= need (gaps of up to 3 columns allowed).
static int bestCluster(const int *cnt, int along, int need, int minWidth, int *c0, int *c1) {
	long best = 0;
	int found = 0;
	for (int a = 0; a < along;) {
		if (cnt[a] < need) { a++; continue; }
		int start = a, end = a;
		long w = cnt[a];
		for (int b = a + 1; b < along && b - end <= 4; b++) if (cnt[b] >= need) { end = b; w += cnt[b]; }
		if (end - start + 1 >= minWidth && w > best) { best = w; *c0 = start; *c1 = end; found = 1; }
		a = end + 1;
	}
	return found;
}

typedef struct {
	int ok;                    // a bounce icon was located
	const char *method, *restMethod;
	double restTop;            // row index (depth) of the icons' resting top
	double assumedRest;        // same, from the symmetric-padding assumption (sanity check)
	double maxDisp, slideEndMs;
	int colMin, colMax, wideFrames;
} DockAnalysis;

// Bounce displacement series from stored Dock-strip frames.
//
// Every frame is diffed against the FINAL frame (the Dock after it has settled),
// not against the pre-launch baseline: inserting the new icon re-lays out the
// whole centred Dock, so against the baseline every icon looks changed for the
// whole recording. Against the final frame only the slide (while it lasts) and
// the bouncing icon differ.
//   1. Per frame and column: topmost changed row (scanColumn), then common-mode
//      peeling (peelTops) to drop rows shared by the whole Dock.
//   2. "Wide" frames (changed columns > 3 icon widths) are the insertion slide.
//      After the last wide frame only the bouncing icon differs: its columns are
//      the largest cluster of columns changed in those frames.
//   3. rest top = (lowest icon top seen after the slide) - minRun: a lift is only
//      visible once it spans minRun rows, so the lowest visible top sits about
//      minRun rows above rest. Falls back to symmetric Dock padding.
//   4. displacement(t) = max(0, top reached by >= minCols columns around the icon
//      (icon columns +- half an icon, because it also slides) - restTop). The final
//      frame is appended at 0. Columns far from the icon are deliberately ignored:
//      the glass widening at the Dock's ends is a full-height change that is not a bounce.
// outT/outV must hold n entries.
static void dockAnalyze(const DockFrame *fr, int n, const DockGeom *g, int D, const Params *P,
                        double *outT, double *outV, int *outN, DockAnalysis *R) {
	memset(R, 0, sizeof *R);
	R->restTop = R->slideEndMs = NAN;
	R->colMin = R->colMax = -1;
	R->method = R->restMethod = "none";
	*outN = 0;
	if (n < 3 || D < 8 || D > 500) { R->method = "too few frames"; return; }
	int along = dockAlongLen(g), m = n - 1;
	int iconW = P->tile > 0 ? P->tile : 56, margin = iconW / 2 + 4;
	int commonCols = P->commonCols > 0 ? P->commonCols : 2 * iconW;
	const DockFrame *F = &fr[m];
	R->assumedRest = P->tile > 0 ? g->thickness - (g->thickness - P->tile) / 2 - 1 : g->thickness - 14;
	int16_t *tops = malloc(sizeof(int16_t) * (size_t)m * along), *raw = malloc(sizeof(int16_t) * (size_t)m * along);
	int *rawCols = calloc((size_t)m, sizeof(int)), *cnt = calloc((size_t)along, sizeof(int)), *cnt2 = calloc((size_t)along, sizeof(int));
	if (!tops || !raw || !rawCols || !cnt || !cnt2) {
		R->method = "out of memory";
		free(tops); free(raw); free(rawCols); free(cnt); free(cnt2);
		return;
	}
	for (int k = 0; k < m; k++)
		for (int a = 0; a < along; a++) {
			int t = scanColumn(&fr[k], F, g, a, D, D - 1, P);
			tops[(size_t)k * along + a] = (int16_t)t;
			if (t >= 0) rawCols[k]++;
		}
	memcpy(raw, tops, sizeof(int16_t) * (size_t)m * along);
	int s = -1;
	for (int k = 0; k < m; k++) if (rawCols[k] > 3 * iconW) { s = k; R->wideFrames++; }
	if (s >= 0) R->slideEndMs = fr[s].tMs;
	for (int k = 0; k < m; k++) peelTops(tops + (size_t)k * along, &fr[k], F, g, D, commonCols, P);

	int c0 = -1, c1 = -1, npost = 0;
	for (int k = s + 1; k < m; k++)
		if (rawCols[k] >= P->minCols) {
			npost++;
			for (int a = 0; a < along; a++) if (raw[(size_t)k * along + a] >= 0) cnt[a]++;
		}
	double rest = NAN;
	if (npost > 0 && bestCluster(cnt, along, npost >= 3 ? 2 : 1, P->minCols, &c0, &c1)) {
		R->method = "post-slide-cluster";
		int tmin = 1 << 30, tfloor = P->tile > 0 ? P->tile / 2 : 24;
		for (int k = s + 1; k < m; k++) {
			int T = reachDepth(tops + (size_t)k * along, along, c0 - margin, c1 + margin, 1, D, P->minCols);
			if (T >= tfloor && T < tmin) tmin = T;
		}
		if (P->restTop >= 0) { rest = P->restTop; R->restMethod = "flag -resttop"; }
		else if (tmin < (1 << 30)) { rest = tmin - P->minRun; R->restMethod = "lowest post-slide icon top - minRun"; }
		else { rest = R->assumedRest; R->restMethod = "assumed symmetric padding"; }
		R->ok = 1;
	} else {
		rest = P->restTop >= 0 ? P->restTop : R->assumedRest;
		R->restMethod = P->restTop >= 0 ? "flag -resttop" : "assumed symmetric padding";
		for (int k = 0; k < m; k++)
			for (int a = 0; a < along; a++) if (tops[(size_t)k * along + a] > rest + 2) cnt2[a]++;
		if (bestCluster(cnt2, along, 1, P->minCols, &c0, &c1)) { R->method = "excess-over-assumed-rest"; R->ok = 1; }
	}
	R->restTop = rest;
	R->colMin = R->ok ? c0 : -1;
	R->colMax = R->ok ? c1 : -1;
	for (int k = 0; k < m; k++) {
		double v = 0;
		if (R->ok) {
			const int16_t *row = tops + (size_t)k * along;
			int T = reachDepth(row, along, c0 - margin, c1 + margin, 1, D, P->minCols);
			if (T >= 0 && T > rest) v = T - rest;
		}
		outT[k] = fr[k].tMs; outV[k] = v;
		if (v > R->maxDisp) R->maxDisp = v;
	}
	outT[m] = fr[m].tMs; outV[m] = 0; // the settled frame is at rest by definition
	*outN = n;
	free(tops); free(raw); free(rawCols); free(cnt); free(cnt2);
}

// ---- Bounce-curve analysis ---------------------------------------------------
typedef struct {
	int count;         // bounces (peaks >= minPeak, separated by valleys)
	double start, apex, apexDisp, end, period; // ms; NaN when unknown
	int hasEnd;
} BounceResult;

// Quadratic through three samples -> refined peak time (falls back to t1).
static double refinePeak(const double *t, const double *v, int i, int n, double *val) {
	*val = v[i];
	if (i <= 0 || i >= n - 1) return t[i];
	double x0 = t[i - 1], x1 = t[i], x2 = t[i + 1], y0 = v[i - 1], y1 = v[i], y2 = v[i + 1];
	double d01 = (y1 - y0) / (x1 - x0), d12 = (y2 - y1) / (x2 - x1);
	double a = (d12 - d01) / (x2 - x0);
	if (a >= 0) return t[i];
	double b = d01 - a * (x0 + x1);
	double xv = -b / (2 * a);
	if (xv < x0 || xv > x2) return t[i];
	*val = a * xv * xv + b * xv + (y1 - a * x1 * x1 - b * x1);
	if (*val < y1) *val = y1;
	return xv;
}

// Crossing of `thr` between samples j-1 and j, linearly interpolated only if
// the samples are close in time (frames are change-driven: across a long gap
// nothing moved, so the change happened at the later frame).
static double crossTime(const double *t, const double *v, int j, double thr, double maxInterp) {
	if (j <= 0) return t[j];
	if (t[j] - t[j - 1] > maxInterp || v[j] == v[j - 1]) return t[j];
	return t[j - 1] + (thr - v[j - 1]) / (v[j] - v[j - 1]) * (t[j] - t[j - 1]);
}

// Time of the peak at sample i. Displacements are quantised to about a point and
// frames are change-driven (a state is held until the next frame), so the apex is
// the middle of the contiguous plateau of samples within 1 pt of the maximum; the
// last plateau sample counts until the next frame (a few ms of bias on dense data). A single dense sample is refined with a
// parabola through its neighbours.
static double peakTime(const double *t, const double *v, int i, int n, const Params *P, double *val) {
	double lim = v[i] - 1.0;
	int l = i, r = i;
	while (l > 0 && v[l - 1] >= lim) l--;
	while (r + 1 < n && v[r + 1] >= lim) r++;
	int hold = r + 1 < n && t[r + 1] - t[r] > P->maxInterp;
	if (r > l || hold) { *val = v[i]; return 0.5 * (t[l] + (r + 1 < n ? t[r + 1] : t[r])); }
	return refinePeak(t, v, i, n, val);
}

static void analyzeBounce(const double *t, const double *v, int n, const Params *P, BounceResult *r) {
	r->count = 0; r->hasEnd = 0;
	r->start = r->apex = r->apexDisp = r->end = r->period = NAN;
	if (n < 2) return;
	enum { MAXPK = 256 };
	int pk[MAXPK], np = 0;
	// A bounce rises from rest: a series that starts above the threshold and only
	// decays (e.g. the Dock glass finishing its widening) is not a bounce.
	int firstRest = -1;
	for (int i = 0; i < n; i++) if (v[i] <= P->startThresh) { firstRest = i; break; }
	if (firstRest < 0) return;
	for (int i = firstRest; i < n; i++) {
		double left = i > 0 ? v[i - 1] : 0, right = i < n - 1 ? v[i + 1] : -1;
		if (v[i] < P->minPeak || !(v[i] > left) || !(v[i] >= right)) continue;
		if (np > 0) { // merge peaks that are not separated by a deep valley
			int p = pk[np - 1];
			double valley = v[p];
			for (int k = p; k <= i; k++) if (v[k] < valley) valley = v[k];
			double lower = v[p] < v[i] ? v[p] : v[i];
			if (valley > P->valleyFrac * lower) { if (v[i] > v[p]) pk[np - 1] = i; continue; }
		}
		if (np < MAXPK) pk[np++] = i;
	}
	r->count = np;
	if (np == 0) return;
	int a = pk[0];
	r->apex = peakTime(t, v, a, n, P, &r->apexDisp);
	// start: first sample above the threshold on the way up to the first peak
	int s = a;
	while (s > 0 && v[s - 1] > P->startThresh) s--;
	r->start = (s > 0) ? crossTime(t, v, s, P->startThresh, P->maxInterp) : t[0];
	// end of the first bounce
	int limit = np >= 2 ? pk[1] : n - 1;
	int j = -1;
	for (int k = a + 1; k <= limit; k++) if (v[k] <= P->startThresh) { j = k; break; }
	if (j >= 0) {
		r->end = crossTime(t, v, j, P->startThresh, P->maxInterp);
		r->hasEnd = 1;
	} else if (np >= 2) { // never returned to rest: use the valley minimum
		int m = a;
		for (int k = a; k <= pk[1]; k++) if (v[k] < v[m]) m = k;
		r->end = t[m];
		r->hasEnd = 1;
	}
	if (np >= 2) {
		double first = r->apex, last = first, tmp;
		last = peakTime(t, v, pk[np - 1], n, P, &tmp);
		r->period = (last - first) / (np - 1);
	}
}

typedef struct { double contentVsStart, fraction; int pass; } HalfBounce; // pass: 1 yes, 0 no, -1 unknown
static HalfBounce halfBounce(double tComplete, const BounceResult *b, int bounceAvailable) {
	HalfBounce h = {NAN, NAN, -1};
	if (isnan(tComplete)) { h.pass = 0; return h; }
	if (!bounceAvailable || b->count == 0) return h;
	h.contentVsStart = tComplete - b->start;
	if (b->hasEnd && b->end > b->start) h.fraction = (tComplete - b->start) / (b->end - b->start);
	h.pass = tComplete <= b->apex ? 1 : 0;
	return h;
}

// ============================================================================
// (2) Bitmap helpers
// ============================================================================

static CGColorSpaceRef sRGB(void) {
	static CGColorSpaceRef cs;
	if (!cs) cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
	return cs;
}
static const CGBitmapInfo kBGRA = kCGBitmapByteOrder32Little | kCGImageAlphaPremultipliedFirst;

static void bmFree(Bitmap *b) { free(b->p); b->p = NULL; }

// Load a PNG, convert to sRGB BGRA and box-downsample by `div` (2 -> 2x PNG to 1x).
static int loadExpected(const char *path, int div, Bitmap *out) {
	CFURLRef url = CFURLCreateFromFileSystemRepresentation(NULL, (const UInt8 *)path, (CFIndex)strlen(path), false);
	CGImageSourceRef src = url ? CGImageSourceCreateWithURL(url, NULL) : NULL;
	if (url) CFRelease(url);
	if (!src) return 0;
	CGImageRef img = CGImageSourceCreateImageAtIndex(src, 0, NULL);
	CFRelease(src);
	if (!img) return 0;
	int fw = (int)CGImageGetWidth(img), fh = (int)CGImageGetHeight(img);
	uint8_t *full = calloc((size_t)fw * fh, 4);
	CGContextRef cg = CGBitmapContextCreate(full, (size_t)fw, (size_t)fh, 8, (size_t)fw * 4, sRGB(), kBGRA);
	CGContextDrawImage(cg, CGRectMake(0, 0, fw, fh), img);
	CGContextRelease(cg);
	CGImageRelease(img);
	out->w = fw / div; out->h = fh / div;
	out->p = malloc((size_t)out->w * out->h * 4);
	for (int y = 0; y < out->h; y++)
		for (int x = 0; x < out->w; x++)
			for (int c = 0; c < 4; c++) {
				int sum = 0;
				for (int dy = 0; dy < div; dy++)
					for (int dx = 0; dx < div; dx++)
						sum += full[((size_t)(y * div + dy) * fw + x * div + dx) * 4 + c];
				out->p[((size_t)y * out->w + x) * 4 + c] = (uint8_t)((sum + div * div / 2) / (div * div));
			}
	free(full);
	return 1;
}

// Copy a window-sized region (points) out of a frame into an owned CGImage.
static CGImageRef regionImage(const Img *f, IRect r) {
	int x0 = (int)floor(r.x * f->scale), y0 = (int)floor(r.y * f->scale);
	int w = (int)floor(r.w * f->scale), h = (int)floor(r.h * f->scale);
	if (x0 < 0) { w += x0; x0 = 0; }
	if (y0 < 0) { h += y0; y0 = 0; }
	if (x0 + w > f->w) w = f->w - x0;
	if (y0 + h > f->h) h = f->h - y0;
	if (w <= 0 || h <= 0) return NULL;
	CGContextRef cg = CGBitmapContextCreate(NULL, (size_t)w, (size_t)h, 8, (size_t)w * 4, sRGB(),
	                                        kCGBitmapByteOrder32Little | kCGImageAlphaNoneSkipFirst);
	uint8_t *dst = CGBitmapContextGetData(cg);
	size_t dstStride = CGBitmapContextGetBytesPerRow(cg);
	for (int y = 0; y < h; y++) memcpy(dst + y * dstStride, f->p + (size_t)(y0 + y) * f->stride + (size_t)x0 * 4, (size_t)w * 4);
	CGImageRef img = CGBitmapContextCreateImage(cg);
	CGContextRelease(cg);
	return img;
}

static void writePNG(CGImageRef img, NSString *path) {
	CFURLRef url = (__bridge CFURLRef)[NSURL fileURLWithPath:path];
	CGImageDestinationRef d = CGImageDestinationCreateWithURL(url, CFSTR("public.png"), 1, NULL);
	if (!d) return;
	CGImageDestinationAddImage(d, img, NULL);
	CGImageDestinationFinalize(d);
	CFRelease(d);
}

// ============================================================================
// (3) Self test (synthetic data; no screen access)
// ============================================================================

static int gFail = 0;
#define CHECK(cond, ...) do { if (!(cond)) { gFail++; printf("  FAIL %s:%d: ", __FILE__, __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

typedef struct { uint8_t *buf; int w, h; } Canvas;
static Canvas cvNew(int w, int h, uint8_t b, uint8_t g, uint8_t r) {
	Canvas c = {malloc((size_t)w * h * 4), w, h};
	for (int i = 0; i < w * h; i++) { c.buf[i * 4] = b; c.buf[i * 4 + 1] = g; c.buf[i * 4 + 2] = r; c.buf[i * 4 + 3] = 255; }
	return c;
}
static Canvas cvCopy(const Canvas *s) { Canvas c = {malloc((size_t)s->w * s->h * 4), s->w, s->h}; memcpy(c.buf, s->buf, (size_t)s->w * s->h * 4); return c; }
static void cvRect(Canvas *c, int x0, int y0, int w, int h, uint8_t b, uint8_t g, uint8_t r) {
	for (int y = y0; y < y0 + h; y++)
		for (int x = x0; x < x0 + w; x++)
			if (x >= 0 && y >= 0 && x < c->w && y < c->h) {
				uint8_t *p = c->buf + ((size_t)y * c->w + x) * 4; p[0] = b; p[1] = g; p[2] = r; p[3] = 255;
			}
}
static Img cvImg(const Canvas *c) { Img i = {c->buf, (size_t)c->w * 4, c->w, c->h, 1.0}; return i; }

// Expected content: 5x5-pt coloured cells, so a 1 px shift changes many probes unless slack covers it.
static Bitmap makeExpected(int w, int h) {
	Bitmap b = {w, h, malloc((size_t)w * h * 4)};
	for (int y = 0; y < h; y++)
		for (int x = 0; x < w; x++) {
			uint8_t *p = b.p + ((size_t)y * w + x) * 4;
			int cell = (x / 5) * 7 + (y / 5) * 13;
			p[0] = (uint8_t)(40 + (cell * 37) % 180); p[1] = (uint8_t)(40 + (cell * 91) % 180);
			p[2] = (uint8_t)(40 + (cell * 53) % 180); p[3] = 255;
		}
	return b;
}
static void blit(Canvas *c, const Bitmap *b, int x0, int y0, int rows, int dx, int dy, int dcol) {
	for (int y = 0; y < rows && y < b->h; y++)
		for (int x = 0; x < b->w; x++) {
			uint8_t *d = c->buf + ((size_t)(y0 + y + dy) * c->w + x0 + x + dx) * 4;
			const uint8_t *s = b->p + ((size_t)y * b->w + x) * 4;
			for (int k = 0; k < 3; k++) { int v = s[k] + dcol; d[k] = (uint8_t)(v < 0 ? 0 : v > 255 ? 255 : v); }
			d[3] = 255;
		}
}

static void testMatch(void) {
	Params P = defaultParams();
	Canvas base = cvNew(500, 400, 160, 120, 90);
	Bitmap ex = makeExpected(240, 160);
	IRect win = {60, 50, 240, 160 + 28}; // 28 pt title bar above the content
	Img b = cvImg(&base);
	int probes = 0;
	Canvas c = cvCopy(&base);
	CHECK(matchFraction(&(Img){c.buf, (size_t)c.w * 4, c.w, c.h, 1.0}, win, &ex, &P, &probes) < 0.2, "desktop must not match");
	CHECK(probes > 300, "probes=%d", probes);
	CHECK(diffFraction(&(Img){c.buf, (size_t)c.w * 4, c.w, c.h, 1.0}, &b, win, &P) == 0, "no diff expected");

	cvRect(&c, win.x, win.y, win.w, win.h, 255, 255, 255); // empty white window frame
	Img ci = cvImg(&c);
	double m = matchFraction(&ci, win, &ex, &P, NULL), d = diffFraction(&ci, &b, win, &P);
	CHECK(m < 0.2 && d > 0.9, "blank window m=%.3f d=%.3f", m, d);

	blit(&c, &ex, win.x, win.y + 28, ex.h / 2, 0, 0, 0); // half drawn
	ci = cvImg(&c);
	m = matchFraction(&ci, win, &ex, &P, NULL);
	CHECK(m > 0.3 && m < 0.7, "half drawn m=%.3f", m);

	blit(&c, &ex, win.x, win.y + 28, ex.h, 0, 0, 0); // complete
	ci = cvImg(&c);
	m = matchFraction(&ci, win, &ex, &P, NULL);
	CHECK(m >= 0.999, "complete m=%.3f", m);

	blit(&c, &ex, win.x, win.y + 28, ex.h, 1, 1, 0); // 1 px offset (fractional window origin)
	ci = cvImg(&c);
	m = matchFraction(&ci, win, &ex, &P, NULL);
	CHECK(m >= P.matchFrac, "1px shift m=%.3f", m);

	blit(&c, &ex, win.x, win.y + 28, ex.h, 0, 0, 12); // small colour error within tolerance
	ci = cvImg(&c);
	CHECK(matchFraction(&ci, win, &ex, &P, NULL) >= P.matchFrac, "small colour error");
	blit(&c, &ex, win.x, win.y + 28, ex.h, 0, 0, 60); // big colour error
	ci = cvImg(&c);
	CHECK(matchFraction(&ci, win, &ex, &P, NULL) < 0.5, "big colour error");

	IRect small = {60, 50, 200, 120}; // window still smaller than content
	CHECK(matchFraction(&ci, small, &ex, &P, NULL) == 0, "undersized window");
	free(base.buf); free(c.buf); bmFree(&ex);
}

// h(t) for `nb` parabolic hops of height `hgt`, period `per`, first start at `t0`.
static double hop(double t, double t0, double per, int nb, double hgt) {
	for (int i = 0; i < nb; i++) {
		double c = t0 + per * (i + 0.5), u = (t - c) / (per / 2);
		if (fabs(u) < 1) return hgt * (1 - u * u);
	}
	return 0;
}

static void testBounce(void) {
	Params P = defaultParams();
	BounceResult r;
	double t[800], v[800];
	int n = 0;
	// Dense sampling every 8 ms, three 400 ms hops starting at 100 ms.
	for (double x = 0; x < 1500; x += 8) { t[n] = x; v[n] = hop(x, 100, 400, 3, 40); n++; }
	analyzeBounce(t, v, n, &P, &r);
	CHECK(r.count == 3, "count %d", r.count);
	CHECK(fabs(r.start - 100) < 8, "start %.1f", r.start);
	CHECK(fabs(r.apex - 300) <= 5, "apex %.1f", r.apex);
	CHECK(r.hasEnd && fabs(r.end - 500) < 8, "end %.1f", r.end);
	CHECK(fabs(r.period - 400) < 5, "period %.1f", r.period);
	HalfBounce h = halfBounce(250, &r, 1);
	CHECK(h.pass == 1 && fabs(h.fraction - 0.375) < 0.05, "half pass=%d frac=%.3f", h.pass, h.fraction);
	h = halfBounce(320, &r, 1);
	CHECK(h.pass == 0, "late content must fail");
	h = halfBounce(50, &r, 1);
	CHECK(h.pass == 1 && h.contentVsStart < 0, "content before bounce start passes");

	// Landing never reaches rest (valley 3 pt): still separate bounces.
	n = 0;
	for (double x = 0; x < 1500; x += 8) { t[n] = x; v[n] = 3 + hop(x, 100, 400, 2, 40); n++; }
	for (int i = 0; i < n; i++) if (t[i] < 100 || t[i] > 900) v[i] = 0;
	analyzeBounce(t, v, n, &P, &r);
	CHECK(r.count == 2, "valley>thresh count %d", r.count);
	CHECK(r.hasEnd && fabs(r.end - 500) < 12, "valley end %.1f", r.end);

	// Change-driven frames: nothing for 600 ms, then dense frames during motion.
	n = 0;
	t[n] = 0; v[n] = 0; n++;
	for (double x = 700; x < 1300; x += 8) { t[n] = x; v[n] = hop(x, 704, 400, 1, 40); n++; }
	t[n] = 2500; v[n] = 0; n++; // long quiet tail
	analyzeBounce(t, v, n, &P, &r);
	CHECK(r.count == 1 && r.hasEnd, "driven count %d", r.count);
	CHECK(r.start >= 700 && r.start < 720, "no interpolation across the gap: start %.1f", r.start);
	CHECK(fabs(r.apex - 904) <= 5 && fabs(r.end - 1104) < 12, "driven apex %.1f end %.1f", r.apex, r.end);
	CHECK(isnan(r.period), "single bounce has no period");

	// Shallow valley (30 of 40 pt) between two overlapping hops: one bounce, not two.
	n = 0;
	for (double x = 0; x < 900; x += 8) {
		double a = hop(x, 100, 400, 1, 40), b = hop(x, 300, 400, 1, 40);
		t[n] = x; v[n] = a > b ? a : b; n++;
	}
	analyzeBounce(t, v, n, &P, &r);
	CHECK(r.count == 1, "shallow valley count %d", r.count);

	// Series cut off while the first bounce is still going.
	n = 0;
	for (double x = 0; x < 330; x += 8) { t[n] = x; v[n] = hop(x, 100, 400, 1, 40); n++; }
	analyzeBounce(t, v, n, &P, &r);
	CHECK(r.count == 1 && !r.hasEnd, "cut count %d hasEnd %d", r.count, r.hasEnd);

	// Starts high and only decays (glass finishing its widening): not a bounce.
	for (int i = 0; i < 40; i++) { t[i] = i * 8; v[i] = i < 30 ? 13 - i * 0.4 : 0; }
	analyzeBounce(t, v, 40, &P, &r);
	CHECK(r.count == 0, "decay-only series count %d", r.count);

	// No bounce at all.
	for (int i = 0; i < 50; i++) { t[i] = i * 8; v[i] = 0; }
	analyzeBounce(t, v, 50, &P, &r);
	CHECK(r.count == 0, "flat count %d", r.count);
	h = halfBounce(120, &r, 1);
	CHECK(h.pass == -1, "no bounce -> unknown");
	h = halfBounce(NAN, &r, 1);
	CHECK(h.pass == 0, "no content -> fail");
}

// Dock axes: strip copy and window-mask ranges must agree with dockXY for all three edges.
static void testDockAxes(void) {
	for (int edge = 0; edge < 3; edge++) {
		DockGeom g = {edge, 70, 800, 600};
		Canvas c = cvNew(800, 600, 100, 100, 100);
		int x, y;
		dockXY(&g, 123, 17, &x, &y);
		cvRect(&c, x, y, 1, 1, 1, 2, 3);
		Img ci = cvImg(&c);
		int D = 40;
		uint8_t *strip = malloc((size_t)dockAlongLen(&g) * D * 4);
		copyDockStrip(&ci, &g, D, strip);
		const uint8_t *p = strip + ((size_t)123 * D + 17) * 4;
		CHECK(p[0] == 1 && p[1] == 2 && p[2] == 3, "edge %d: strip pixel (%d,%d,%d)", edge, p[0], p[1], p[2]);
		const uint8_t *q = strip + ((size_t)123 * D + 18) * 4;
		CHECK(q[0] == 100, "edge %d: neighbour untouched", edge);
		free(strip);
		// Window rect covering along 390..510, depth 60..110.
		int x0, y0, x1, y1;
		dockXY(&g, 390, 60, &x0, &y0); dockXY(&g, 510, 110, &x1, &y1);
		DockFrame fr = {0, NULL, 1, {x0 < x1 ? x0 : x1, y0 < y1 ? y0 : y1, abs(x1 - x0) + 1, abs(y1 - y0) + 1}};
		int lo = -1, hi = -1;
		CHECK(maskRange(&g, &fr, 400, &lo, &hi) && lo == 60 && hi == 110, "edge %d: mask range %d..%d", edge, lo, hi);
		CHECK(!maskRange(&g, &fr, 380, &lo, &hi) && !maskRange(&g, &fr, 520, &lo, &hi), "edge %d: mask outside", edge);
		free(c.buf);
	}
}

// ---- Synthetic wide Dock (the real MacBook case) ------------------------------
// 1512 pt wide Dock, 74 pt thick (visibleFrame inset), 48 pt icons resting at rows
// 13..60. Inserting the new (23rd) icon re-lays the centred Dock out: for 250 ms
// every other icon slides by half an icon (28 pt) and the glass widens, and the
// glass top-edge highlight (row 73) changes along its whole length. Meanwhile the
// new icon hops twice, peak 10 pt: its top reaches row 70, BELOW the boundary.
enum { kSynthAlong = 1512, kSynthD = 114 };
static void synthSet(uint8_t *buf, int a, int d, int b, int g, int r) {
	if (a < 0 || a >= kSynthAlong || d < 0 || d >= kSynthD) return;
	uint8_t *p = buf + ((size_t)a * kSynthD + d) * 4;
	p[0] = (uint8_t)b; p[1] = (uint8_t)g; p[2] = (uint8_t)r; p[3] = 255;
}
static int synthLift(double t, int bounce) { return bounce ? (int)lround(hop(t, 100, 350, 2, 10)) : 0; }
static void synthDock(uint8_t *buf, double t, int bounce) {
	int off = (int)lround(28.0 * (1.0 - fmin(t, 250.0) / 250.0));
	int gx0 = 88 + off, gx1 = 1403;
	for (int a = 0; a < kSynthAlong; a++)
		for (int d = 0; d < kSynthD; d++) {
			int v = 110; // wallpaper
			if (a >= gx0 && a <= gx1 && d < 74) v = d >= 71 ? (off ? 210 : 150) : 70; // glass + 3-row top-edge highlight, brighter while the Dock re-lays out
			synthSet(buf, a, d, v, v, v);
		}
	for (int i = 0; i < 23; i++) {
		int x0 = 100 + i * 57 + (i < 22 ? off : 0), lift = i == 22 ? synthLift(t, bounce) : 0;
		for (int a = x0; a < x0 + 48; a++)
			for (int d = 13 + lift; d < 61 + lift; d++) synthSet(buf, a, d, 160 + (i * 37) % 90, 30 + (i * 91) % 60, 140 + (i * 53) % 100);
	}
}

// Emit frames the way ScreenCaptureKit would: only when the screen changed, at most every 8 ms.
static int synthFrames(DockFrame *out, int bounce) {
	size_t sz = (size_t)kSynthAlong * kSynthD * 4;
	uint8_t *cur = malloc(sz), *last = malloc(sz);
	synthDock(last, 0, 0);
	int n = 0;
	double lastT = -1e9;
	for (int ms = 1; ms <= 1200; ms++) {
		synthDock(cur, ms, bounce);
		int changed = memcmp(cur, last, sz) != 0;
		if (changed && (ms - lastT >= 8 || ms == 1200)) {
			out[n].tMs = ms; out[n].hasMask = 0; out[n].px = malloc(sz); memcpy(out[n].px, cur, sz);
			memcpy(last, cur, sz); lastT = ms; n++;
		}
	}
	free(cur); free(last);
	return n;
}

static void testDockSlide(void) {
	Params P = defaultParams();
	P.tile = 48;
	DockGeom g = {0, 74, 1512, 982};
	DockFrame *fr = calloc(400, sizeof *fr);
	double *t = malloc(sizeof(double) * 400), *v = malloc(sizeof(double) * 400);
	int outN = 0;
	DockAnalysis A;
	BounceResult r;

	// Slide only: no bounce. Everything shifts, nothing hops.
	int n = synthFrames(fr, 0);
	CHECK(n > 20 && n < 60, "slide-only frame count %d", n);
	dockAnalyze(fr, n, &g, kSynthD, &P, t, v, &outN, &A);
	analyzeBounce(t, v, outN, &P, &r);
	// (The glass widening at the Dock's end shows up as a decaying full-height change; it must not become a bounce.)
	CHECK(r.count == 0, "slide alone must not read as a bounce (count %d, max %.1f, method %s)", r.count, A.maxDisp, A.method);
	for (int i = 0; i < n; i++) free(fr[i].px);

	// Slide + bounce.
	memset(fr, 0, 400 * sizeof *fr);
	n = synthFrames(fr, 1);
	dockAnalyze(fr, n, &g, kSynthD, &P, t, v, &outN, &A);
	analyzeBounce(t, v, outN, &P, &r);
	// Ground truth: the same integer lift sampled every millisecond.
	double tt[1300], tv[1300];
	for (int ms = 0; ms < 1300; ms++) { tt[ms] = ms; tv[ms] = synthLift(ms, 1); }
	BounceResult tr;
	analyzeBounce(tt, tv, 1300, &P, &tr);
	printf("  frames %d, method %s, restTop %.0f (%s), icon cols %d..%d, slide end %.0f ms, wide frames %d\n",
	       n, A.method, A.restTop, A.restMethod, A.colMin, A.colMax, A.slideEndMs, A.wideFrames);
	if (getenv("BR_DEBUG")) for (int i = 0; i < outN; i++) printf("    t=%.0f v=%.0f\n", t[i], v[i]);
	printf("  measured start %.1f apex %.1f end %.1f count %d | truth start %.1f apex %.1f end %.1f count %d\n",
	       r.start, r.apex, r.end, r.count, tr.start, tr.apex, tr.end, tr.count);
	CHECK(A.ok && strcmp(A.method, "post-slide-cluster") == 0, "method %s", A.method);
	CHECK(A.colMin >= 1350 && A.colMin <= 1358 && A.colMax >= 1397 && A.colMax <= 1405, "icon columns %d..%d (want ~1354..1401)", A.colMin, A.colMax);
	CHECK(fabs(A.restTop - 60) <= 1, "restTop %.1f (want 60)", A.restTop);
	CHECK(A.slideEndMs > 200 && A.slideEndMs < 260, "slide end %.1f", A.slideEndMs);
	CHECK(r.count == 2 && tr.count == 2, "bounce count %d (truth %d)", r.count, tr.count);
	CHECK(fabs(r.start - tr.start) <= 10, "start %.1f vs %.1f", r.start, tr.start);
	CHECK(fabs(r.apex - tr.apex) <= 10, "apex %.1f vs %.1f", r.apex, tr.apex);
	CHECK(r.hasEnd && fabs(r.end - tr.end) <= 10, "end %.1f vs %.1f", r.end, tr.end);
	CHECK(fabs(r.period - tr.period) <= 12, "period %.1f vs %.1f", r.period, tr.period);
	HalfBounce h = halfBounce(150, &r, 1); // content on screen while the first hop is still rising
	CHECK(h.pass == 1, "content at 150 ms is before the apex");
	h = halfBounce(300, &r, 1);
	CHECK(h.pass == 0, "content at 300 ms is after the apex");

	// A user-supplied rest top is honoured.
	P.restTop = 60;
	dockAnalyze(fr, n, &g, kSynthD, &P, t, v, &outN, &A);
	CHECK(A.ok && A.restTop == 60 && strcmp(A.restMethod, "flag -resttop") == 0, "-resttop override");

	for (int i = 0; i < n; i++) free(fr[i].px);
	free(fr); free(t); free(v);
}

static void testPNG(NSString *tmp) {
	// 2x PNG with 2x2 blocks of known colour must downsample to the exact colours.
	int w = 16, h = 8;
	CGContextRef cg = CGBitmapContextCreate(NULL, (size_t)w, (size_t)h, 8, (size_t)w * 4, sRGB(), kBGRA);
	CGContextSetRGBFillColor(cg, 1, 0, 0, 1); CGContextFillRect(cg, CGRectMake(0, 0, 8, 8));
	CGContextSetRGBFillColor(cg, 0, 0, 1, 1); CGContextFillRect(cg, CGRectMake(8, 0, 8, 8));
	CGImageRef img = CGBitmapContextCreateImage(cg);
	CGContextRelease(cg);
	NSString *path = [tmp stringByAppendingPathComponent:@"bouncerec-selftest.png"];
	writePNG(img, path);
	CGImageRelease(img);
	Bitmap b = {0, 0, NULL};
	CHECK(loadExpected(path.fileSystemRepresentation, 2, &b), "load png");
	if (b.p) {
		CHECK(b.w == 8 && b.h == 4, "size %dx%d", b.w, b.h);
		// BGRA: left half red, right half blue (CG origin is bottom-left but both halves span full height).
		CHECK(b.p[2] == 255 && b.p[0] == 0, "left red");
		CHECK(b.p[((size_t)0 * 8 + 6) * 4] == 255 && b.p[((size_t)0 * 8 + 6) * 4 + 2] == 0, "right blue");
		bmFree(&b);
	}
	[[NSFileManager defaultManager] removeItemAtPath:path error:nil];
}

static int runSelfTest(void) {
	int (^section)(const char *, void (^)(void)) = ^int(const char *name, void (^fn)(void)) {
		int before = gFail;
		fn();
		printf("%s %s\n", gFail == before ? "PASS" : "FAIL", name);
		return gFail == before;
	};
	NSString *tmp = NSTemporaryDirectory();
	section("match/diff fractions (empty, partial, complete, shift, tolerance)", ^{ testMatch(); });
	section("dock axes (strip copy + window mask, bottom/left/right)", ^{ testDockAxes(); });
	section("bounce curve (start/apex/end/period/count, change-driven gaps, half-bounce)", ^{ testBounce(); });
	section("wide Dock: insertion slide + bounce below the visibleFrame boundary", ^{ testDockSlide(); });
	section("expected PNG load + 2x->1x downsample", ^{ testPNG(tmp); });
	printf("%s\n", gFail ? "SELFTEST FAIL" : "SELFTEST PASS");
	return gFail ? 1 : 0;
}

// ============================================================================
// (4) Recorder: ScreenCaptureKit + NSWorkspace
// ============================================================================

static int64_t cmNs(CMTime t) { return CMTimeConvertScale(t, 1000000000, kCMTimeRoundingMethod_Default).value; }
static double rnd(double v, int dec) { double m = pow(10, dec); return round(v * m) / m; }
static id jnum(double v) { return isnan(v) || isinf(v) ? (id)[NSNull null] : (id)@(rnd(v, 3)); }

typedef struct {
	NSString *target, *expect, *out, *videoDir, *dockVideoDir;
	double timeout;      // seconds after launch
	int warmMs;          // wait after first frame before launching
	int keep, fps, expectDiv, verbose;
	Params P;
	DockGeom dock; int dockAvail; int screenMaxFps; // filled on the main thread
} Options;

static NSString *absPath(NSString *p) {
	if (!p) return nil;
	if (p.isAbsolutePath) return p;
	return [[NSFileManager defaultManager].currentDirectoryPath stringByAppendingPathComponent:p];
}

static void writeJSONFile(id obj, NSString *path) {
	NSData *d = [NSJSONSerialization dataWithJSONObject:obj options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
	if (path) [d writeToFile:path atomically:YES];
	fwrite(d.bytes, 1, d.length, stdout);
	fputc('\n', stdout);
	fflush(stdout);
}

// Largest layer-0 on-screen window owned by pid. Bounds exclude the shadow.
static BOOL findWindow(pid_t pid, CGRect *out) {
	CFArrayRef list = CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
	if (!list) return NO;
	BOOL found = NO;
	double best = 0;
	for (NSDictionary *w in (__bridge NSArray *)list) {
		if ([w[(id)kCGWindowOwnerPID] intValue] != pid || [w[(id)kCGWindowLayer] intValue] != 0) continue;
		if ([w[(id)kCGWindowAlpha] doubleValue] <= 0) continue;
		CGRect b;
		if (!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)w[(id)kCGWindowBounds], &b)) continue;
		if (b.size.width < 32 || b.size.height < 32 || b.size.width * b.size.height <= best) continue;
		best = b.size.width * b.size.height;
		*out = b;
		found = YES;
	}
	CFRelease(list);
	return found;
}

enum { kRing = 3 };

@interface Recorder : NSObject <SCStreamOutput, SCStreamDelegate> {
	Options O;
	dispatch_queue_t cq;            // capture queue; all state below is owned by it
	dispatch_semaphore_t firstFrameSem;
	// geometry
	int dispW, dispH, dispOX, dispOY;
	DockGeom dock; BOOL dockAvail; NSString *dockWhy;
	double scale;
	// baseline + launch
	uint8_t *baseBuf; size_t baseStride; int baseW, baseH; BOOL locked;
	int64_t launchNs; uint64_t launchMach;
	pid_t pid;
	// frames
	int frames, framesAfter, serial;
	CMSampleBufferRef ring[kRing]; int64_t ringNs[kRing];
	Bitmap ex;
	// window results (ms relative to launch)
	BOOL winSeen; IRect lastWin; BOOL haveLastWin;
	double tWindowListed, tFirstPixels, tComplete;
	int serFirst, serComplete, partial;
	NSMutableArray *partialTimes, *winFrames, *notes;
	// Dock strip frames, analysed after the recording against the settled frame
	DockFrame *dfr; int dn, dcap, dmax, dockD; DockFrame baseStrip;
	int64_t lastMotionNs; BOOL sawMotion; int lastMotionIdx; BOOL dockCapped;
	double clockCheckMs, displayVsPtsMs;
	dispatch_group_t videoGroup; int videoCount, dockVideoCount;
	SCStream *stream; NSRunningApplication *app;
	NSString *streamError; BOOL dockSettled;
}
- (instancetype)initWithOptions:(Options)o;
- (int)run; // blocking; returns exit code
@end

@implementation Recorder

- (instancetype)initWithOptions:(Options)o {
	if ((self = [super init])) {
		O = o;
		cq = dispatch_queue_create("bouncerec.capture", DISPATCH_QUEUE_SERIAL);
		firstFrameSem = dispatch_semaphore_create(0);
		tWindowListed = tFirstPixels = tComplete = NAN;
		partialTimes = [NSMutableArray array]; winFrames = [NSMutableArray array]; notes = [NSMutableArray array];
		dcap = 256; dfr = calloc((size_t)dcap, sizeof(DockFrame));
		lastMotionIdx = -1;
		clockCheckMs = displayVsPtsMs = NAN;
		videoGroup = dispatch_group_create();
		scale = 1;
	}
	return self;
}

- (void)note:(NSString *)s { [notes addObject:s]; if (O.verbose) fprintf(stderr, "note: %s\n", s.UTF8String); }

- (int)failWith:(NSString *)msg code:(int)code extra:(NSDictionary *)extra {
	NSMutableDictionary *d = [@{@"error": msg} mutableCopy];
	if (extra) [d addEntriesFromDictionary:extra];
	writeJSONFile(d, O.out);
	return code;
}

- (int)permissionDenied {
	CGRequestScreenCaptureAccess(); // shows the system prompt / adds BounceRec to the Screen Recording list
	return [self failWith:@"screen recording permission needed" code:2 extra:@{
		@"detail": @"Enable BounceRec in System Settings > Privacy & Security > Screen & System Audio Recording, then re-run."}];
}

// ---- stream callbacks -------------------------------------------------------

- (void)stream:(SCStream *)s didStopWithError:(NSError *)error {
	dispatch_async(cq, ^{ self->streamError = error.localizedDescription; });
}

- (void)stream:(SCStream *)s didOutputSampleBuffer:(CMSampleBufferRef)sb ofType:(SCStreamOutputType)type {
	if (type != SCStreamOutputTypeScreen || !CMSampleBufferIsValid(sb)) return;
	CFArrayRef att = CMSampleBufferGetSampleAttachmentsArray(sb, false);
	NSDictionary *info = att && CFArrayGetCount(att) ? (__bridge NSDictionary *)CFArrayGetValueAtIndex(att, 0) : nil;
	NSNumber *st = info[SCStreamFrameInfoStatus];
	if (st && st.integerValue != SCFrameStatusComplete && st.integerValue != SCFrameStatusStarted) return; // idle/blank/suspended
	if (!CMSampleBufferGetImageBuffer(sb)) return;
	[self handleFrame:sb info:info];
}

- (void)lockBaselineAndMarkLaunch {
	dispatch_sync(cq, ^{
		self->launchMach = mach_absolute_time();
		self->launchNs = cmNs(CMClockMakeHostTimeFromSystemUnits(self->launchMach));
		self->locked = YES;
	});
}

- (void)setPid:(pid_t)p { dispatch_async(cq, ^{ self->pid = p; }); }

// Evaluate one frame against the target window bounds. Called for the frame
// that first lists the window, for up to kRing frames before it (the window
// may have been on glass slightly before the window list showed it), and then
// for every later frame.
- (void)evalWindowFrame:(const Img *)f base:(const Img *)base tMs:(double)tMs win:(IRect)win {
	int probes;
	double m = matchFraction(f, win, &ex, &O.P, &probes);
	double d = diffFraction(f, base, win, &O.P);
	BOOL present = d >= O.P.firstFrac || m >= O.P.matchFrac;
	BOOL complete = m >= O.P.matchFrac;
	[winFrames addObject:@[jnum(tMs), jnum(m), jnum(d)]];
	serial++;
	const char *state = "blank";
	if (isnan(tComplete)) {
		if (present && isnan(tFirstPixels)) { tFirstPixels = tMs; serFirst = serial; }
		if (complete) { tComplete = tMs; serComplete = serial; state = "complete"; }
		else if (present) { partial++; [partialTimes addObject:jnum(tMs)]; state = "partial"; }
	} else {
		state = complete ? "complete" : "later";
	}
	if (O.videoDir) {
		CGImageRef img = regionImage(f, win);
		if (img) {
			NSString *p = [O.videoDir stringByAppendingPathComponent:[NSString stringWithFormat:@"f%05d_t%+010.2fms_%s.png", serial, tMs, state]];
			videoCount++;
			dispatch_group_async(videoGroup, dispatch_get_global_queue(QOS_CLASS_UTILITY, 0), ^{ writePNG(img, p); CGImageRelease(img); });
		}
	}
}

static size_t dockBytesFor(const DockGeom *g, int D) { return (size_t)dockAlongLen(g) * (size_t)D * 4; }

static IRect toIRect(CGRect b, int ox, int oy) {
	return (IRect){(int)lrint(b.origin.x) - ox, (int)lrint(b.origin.y) - oy, (int)lrint(b.size.width), (int)lrint(b.size.height)};
}

- (void)handleFrame:(CMSampleBufferRef)sb info:(NSDictionary *)info {
	CVPixelBufferRef pb = (CVPixelBufferRef)CMSampleBufferGetImageBuffer(sb);
	int64_t ptsNs = cmNs(CMSampleBufferGetPresentationTimeStamp(sb));
	frames++;
	CVPixelBufferLockBaseAddress(pb, kCVPixelBufferLock_ReadOnly);
	Img f = {CVPixelBufferGetBaseAddress(pb), CVPixelBufferGetBytesPerRow(pb), (int)CVPixelBufferGetWidth(pb), (int)CVPixelBufferGetHeight(pb), 1.0};
	if (frames == 1) {
		scale = dispW > 0 ? (double)f.w / dispW : 1.0;
		clockCheckMs = (cmNs(CMClockGetTime(CMClockGetHostTimeClock())) - ptsNs) / 1e6;
		NSNumber *dt = info[SCStreamFrameInfoDisplayTime];
		if (dt) {
			mach_timebase_info_data_t tb; mach_timebase_info(&tb);
			displayVsPtsMs = ((double)dt.unsignedLongLongValue * tb.numer / tb.denom - (double)ptsNs) / 1e6;
		}
		if (fabs(scale - 1.0) > 0.01) [self note:[NSString stringWithFormat:@"frame is %dx%d px for %dx%d pt display (scale %.2f); coordinates are scaled", f.w, f.h, dispW, dispH, scale]];
		baseW = f.w; baseH = f.h; baseStride = (size_t)f.w * 4;
		baseBuf = malloc(baseStride * f.h);
	}
	f.scale = scale;
	if (f.w != baseW || f.h != baseH || !baseBuf) { CVPixelBufferUnlockBaseAddress(pb, kCVPixelBufferLock_ReadOnly); return; }

	if (!locked || ptsNs < launchNs) { // pre-launch: keep refreshing the baseline
		if (!locked) {
			for (int y = 0; y < f.h; y++) memcpy(baseBuf + (size_t)y * baseStride, f.p + (size_t)y * f.stride, baseStride);
			if (frames == 1) dispatch_semaphore_signal(firstFrameSem);
		}
		CVPixelBufferUnlockBaseAddress(pb, kCVPixelBufferLock_ReadOnly);
		return;
	}

	Img base = {baseBuf, baseStride, baseW, baseH, scale};
	double tMs = (ptsNs - launchNs) / 1e6;
	framesAfter++;

	// ---- target window ----
	IRect winRect = {0, 0, 0, 0};
	BOOL haveWin = NO;
	CGRect wb;
	if (pid > 0 && findWindow(pid, &wb)) { winRect = toIRect(wb, dispOX, dispOY); haveWin = YES; }
	if (haveWin) {
		if (!winSeen) {
			winSeen = YES;
			tWindowListed = tMs;
			for (int i = 0; i < kRing; i++) { // oldest first; ring[0] is the oldest
				if (!ring[i] || ringNs[i] < launchNs) continue;
				CVPixelBufferRef rp = (CVPixelBufferRef)CMSampleBufferGetImageBuffer(ring[i]);
				CVPixelBufferLockBaseAddress(rp, kCVPixelBufferLock_ReadOnly);
				Img rf = {CVPixelBufferGetBaseAddress(rp), CVPixelBufferGetBytesPerRow(rp), (int)CVPixelBufferGetWidth(rp), (int)CVPixelBufferGetHeight(rp), scale};
				[self evalWindowFrame:&rf base:&base tMs:(ringNs[i] - launchNs) / 1e6 win:winRect];
				CVPixelBufferUnlockBaseAddress(rp, kCVPixelBufferLock_ReadOnly);
			}
		}
		lastWin = winRect; haveLastWin = YES;
		[self evalWindowFrame:&f base:&base tMs:tMs win:winRect];
	}

	// ---- Dock strip (analysed after the recording; see dockAnalyze) ----
	if (dockAvail) {
		if (!baseStrip.px) { // pre-launch Dock, only used as the first reference for the "Dock moving" test
			baseStrip.px = malloc(dockBytesFor(&dock, dockD));
			copyDockStrip(&base, &dock, dockD, baseStrip.px);
		}
		if (dn >= dmax) {
			if (!dockCapped) { dockCapped = YES; [self note:[NSString stringWithFormat:@"Dock strip store full (%d frames); later frames were not analysed", dmax]]; }
		} else {
			DockFrame cur = {tMs, malloc(dockBytesFor(&dock, dockD)), 0, {0, 0, 0, 0}};
			copyDockStrip(&f, &dock, dockD, cur.px);
			if (haveLastWin) { // mask the window and its shadow so its pixels don't read as an icon
				int sh = O.P.shadow;
				cur.hasMask = 1;
				cur.mask = (IRect){lastWin.x - sh, lastWin.y - sh, lastWin.w + 2 * sh, lastWin.h + 2 * sh};
			}
			const DockFrame *prev = dn ? &dfr[dn - 1] : &baseStrip;
			if (dockChangedPixels(prev, &cur, &dock, dockD, O.P.bounceTol, O.P.motionPx) >= O.P.motionPx) {
				lastMotionNs = ptsNs; sawMotion = YES; lastMotionIdx = dn;
			}
			if (dn == dcap) { dcap *= 2; dfr = realloc(dfr, sizeof(DockFrame) * (size_t)dcap); }
			dfr[dn++] = cur;
		}
	}
	CVPixelBufferUnlockBaseAddress(pb, kCVPixelBufferLock_ReadOnly);

	// ---- ring of recent frames (for the window-listed lookback) ----
	if (ring[0]) CFRelease(ring[0]);
	for (int i = 0; i + 1 < kRing; i++) { ring[i] = ring[i + 1]; ringNs[i] = ringNs[i + 1]; }
	ring[kRing - 1] = (CMSampleBufferRef)CFRetain(sb); ringNs[kRing - 1] = ptsNs;
}

// ---- driver ---------------------------------------------------------------------

static void waitOn(dispatch_semaphore_t s, double secs) { dispatch_semaphore_wait(s, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(secs * NSEC_PER_SEC))); }

- (int)run {
	dock = O.dock;
	dockAvail = O.dockAvail;
	dockWhy = dockAvail ? nil : @"Dock is hidden/auto-hide (visibleFrame equals frame on every edge); bounce timing unavailable";
	int fps = O.fps > 0 ? O.fps : O.screenMaxFps;
	if (fps < 30) fps = 60;
	if (fps > 120) fps = 120;

	if (!CGPreflightScreenCaptureAccess()) return [self permissionDenied];

	// Expected content.
	if (!loadExpected(O.expect.fileSystemRepresentation, O.expectDiv, &ex))
		return [self failWith:[NSString stringWithFormat:@"cannot load -expect PNG %@", O.expect] code:64 extra:nil];
	if (O.videoDir) [[NSFileManager defaultManager] createDirectoryAtPath:O.videoDir withIntermediateDirectories:YES attributes:nil error:nil];

	// Shareable content -> main display.
	__block SCShareableContent *content = nil;
	__block NSError *err = nil;
	dispatch_semaphore_t sem = dispatch_semaphore_create(0);
	[SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *c, NSError *e) { content = c; err = e; dispatch_semaphore_signal(sem); }];
	waitOn(sem, 10);
	if (!content) {
		if (err.code == -3801) return [self permissionDenied];
		return [self failWith:[NSString stringWithFormat:@"SCShareableContent failed: %@", err.localizedDescription] code:3 extra:nil];
	}
	SCDisplay *disp = nil;
	for (SCDisplay *d in content.displays) if (d.displayID == CGMainDisplayID()) disp = d;
	if (!disp) disp = content.displays.firstObject;
	if (!disp) return [self failWith:@"no display" code:3 extra:nil];
	dispW = (int)disp.width; dispH = (int)disp.height;
	dispOX = (int)lrint(disp.frame.origin.x); dispOY = (int)lrint(disp.frame.origin.y);
	dock.sw = dispW; dock.sh = dispH;
	dockD = dockDepth(&dock, &O.P);
	dmax = dockAvail ? (int)fmin(2400.0, 1.5e9 / (double)dockBytesFor(&dock, dockD)) : 0; // ~1.5 GB cap
	if (O.dockVideoDir) [[NSFileManager defaultManager] createDirectoryAtPath:O.dockVideoDir withIntermediateDirectories:YES attributes:nil error:nil];

	// Stream: 1x (point-sized) BGRA, change-driven frames at up to the display rate.
	SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:disp excludingWindows:@[]];
	SCStreamConfiguration *cfg = [SCStreamConfiguration new];
	cfg.width = (size_t)dispW; cfg.height = (size_t)dispH;
	cfg.pixelFormat = kCVPixelFormatType_32BGRA;
	cfg.minimumFrameInterval = CMTimeMake(1, fps);
	cfg.queueDepth = 8;
	cfg.showsCursor = NO;
	cfg.colorSpaceName = kCGColorSpaceSRGB; // so values compare with an sRGB-rendered expected PNG
	stream = [[SCStream alloc] initWithFilter:filter configuration:cfg delegate:self];
	NSError *addErr = nil;
	if (![stream addStreamOutput:self type:SCStreamOutputTypeScreen sampleHandlerQueue:cq error:&addErr])
		return [self failWith:[NSString stringWithFormat:@"addStreamOutput: %@", addErr.localizedDescription] code:3 extra:nil];
	__block NSError *startErr = nil;
	[stream startCaptureWithCompletionHandler:^(NSError *e) { startErr = e; dispatch_semaphore_signal(sem); }];
	waitOn(sem, 10);
	if (startErr) {
		if (startErr.code == -3801) return [self permissionDenied];
		return [self failWith:[NSString stringWithFormat:@"startCapture: %@", startErr.localizedDescription] code:3 extra:nil];
	}
	waitOn(firstFrameSem, 3);
	__block int got = 0;
	dispatch_sync(cq, ^{ got = self->frames; });
	if (!got) {
		[stream stopCaptureWithCompletionHandler:^(NSError *e) {}];
		return [self failWith:@"no frames delivered (display asleep or session locked?)" code:3 extra:nil];
	}
	usleep((useconds_t)O.warmMs * 1000); // let the stream settle; baseline keeps refreshing

	// Launch. Baseline is frozen and the launch request stamped on the capture queue.
	NSURL *url = [NSURL fileURLWithPath:O.target];
	NSWorkspaceOpenConfiguration *oc = [NSWorkspaceOpenConfiguration configuration];
	oc.activates = YES;
	oc.createsNewApplicationInstance = YES;
	dispatch_semaphore_t launched = dispatch_semaphore_create(0);
	__block NSError *launchErr = nil;
	[self lockBaselineAndMarkLaunch];
	[NSWorkspace.sharedWorkspace openApplicationAtURL:url configuration:oc completionHandler:^(NSRunningApplication *a, NSError *e) {
		self->app = a; launchErr = e;
		if (a) [self setPid:a.processIdentifier];
		dispatch_semaphore_signal(launched);
	}];
	waitOn(launched, 5);
	if (!app) {
		[stream stopCaptureWithCompletionHandler:^(NSError *e) {}];
		return [self failWith:[NSString stringWithFormat:@"launch failed: %@", launchErr.localizedDescription ?: @"timeout"] code:3 extra:nil];
	}

	// Record until the Dock has settled, or until the timeout (both counted from
	// the launch request). "Settled" = the Dock strip has been still for -settle ms
	// after it last moved; the last frame is then the reference for the bounce
	// analysis. While the Dock keeps moving the recording is extended up to -timeout.
	mach_timebase_info_data_t tb; mach_timebase_info(&tb);
	BOOL settled = NO;
	for (;;) {
		usleep(50000);
		double elapsed = (double)(mach_absolute_time() - launchMach) * tb.numer / tb.denom / 1e9;
		if (elapsed >= O.timeout) break;
		if (!dockAvail) continue;
		__block BOOL saw = NO;
		__block int64_t lastNs = 0;
		dispatch_sync(cq, ^{ saw = self->sawMotion; lastNs = self->lastMotionNs; });
		if (saw && (cmNs(CMClockGetTime(CMClockGetHostTimeClock())) - lastNs) / 1e6 >= O.P.settleMs) { settled = YES; break; }
	}
	dockSettled = settled;

	dispatch_semaphore_t stopped = dispatch_semaphore_create(0);
	[stream stopCaptureWithCompletionHandler:^(NSError *e) { dispatch_semaphore_signal(stopped); }];
	waitOn(stopped, 5);
	dispatch_sync(cq, ^{}); // drain: no more callbacks after this
	pid_t apid = app.processIdentifier;
	if (!O.keep) {
		[app terminate];
		for (int i = 0; i < 30 && !app.isTerminated; i++) usleep(50000);
		if (!app.isTerminated) [app forceTerminate];
	}
	dispatch_group_wait(videoGroup, dispatch_time(DISPATCH_TIME_NOW, 20 * NSEC_PER_SEC));

	return [self finishWithPid:apid mach:launchMach tb:tb fps:fps];
}

// -dockvideo: the Dock strip (only that strip) of every frame from launch until the
// Dock settled, oriented with the screen edge at the bottom and depth growing upward.
- (void)writeDockVideo {
	if (!O.dockVideoDir || !dn) return;
	int along = dockAlongLen(&dock), last = lastMotionIdx + 1 < dn ? lastMotionIdx + 1 : dn - 1;
	for (int k = 0; k <= last; k++) {
		CGContextRef cg = CGBitmapContextCreate(NULL, (size_t)along, (size_t)dockD, 8, (size_t)along * 4, sRGB(), kCGBitmapByteOrder32Little | kCGImageAlphaNoneSkipFirst);
		uint8_t *dst = CGBitmapContextGetData(cg);
		size_t stride = CGBitmapContextGetBytesPerRow(cg);
		for (int y = 0; y < dockD; y++)
			for (int a = 0; a < along; a++) memcpy(dst + (size_t)y * stride + (size_t)a * 4, dfr[k].px + ((size_t)a * dockD + (dockD - 1 - y)) * 4, 4);
		CGImageRef img = CGBitmapContextCreateImage(cg);
		CGContextRelease(cg);
		NSString *p = [O.dockVideoDir stringByAppendingPathComponent:[NSString stringWithFormat:@"d%05d_t%+010.2fms.png", k, dfr[k].tMs]];
		dockVideoCount++;
		dispatch_group_async(videoGroup, dispatch_get_global_queue(QOS_CLASS_UTILITY, 0), ^{ writePNG(img, p); CGImageRelease(img); });
	}
	dispatch_group_wait(videoGroup, dispatch_time(DISPATCH_TIME_NOW, 60 * NSEC_PER_SEC));
}

- (int)finishWithPid:(pid_t)apid mach:(uint64_t)mach tb:(mach_timebase_info_data_t)tb fps:(int)fps {
	BounceResult br;
	DockAnalysis da;
	memset(&da, 0, sizeof da);
	da.restTop = da.slideEndMs = NAN; da.colMin = da.colMax = -1; da.method = da.restMethod = "none";
	double *sT = malloc(sizeof(double) * (size_t)(dn + 1)), *sV = malloc(sizeof(double) * (size_t)(dn + 1));
	int sN = 0;
	if (dockAvail) dockAnalyze(dfr, dn, &dock, dockD, &O.P, sT, sV, &sN, &da);
	analyzeBounce(sT, sV, sN, &O.P, &br);
	[self writeDockVideo];
	HalfBounce hb = halfBounce(tComplete, &br, dockAvail);
	BOOL contentFirst = !isnan(tComplete) && tComplete == tFirstPixels && partial == 0;

	if (isnan(tWindowListed)) [self note:@"target window never appeared in the on-screen window list"];
	else if (isnan(tComplete)) [self note:@"window appeared but its content never matched -expect (check offset/size/colours; see framesInWindow)"];
	if (!isnan(tFirstPixels) && !isnan(tWindowListed) && tFirstPixels < tWindowListed - 0.001)
		[self note:@"pixels were already on glass in a frame before the window list showed the window (recovered via lookback)"];
	if (dockAvail && br.count == 0) [self note:[NSString stringWithFormat:@"no Dock bounce observed (method %s, max displacement %.1f pt); the app may have launched before the Dock bounced, or the thresholds need tuning (inspect with -dockvideo)", da.method, da.maxDisp]];
	if (dockAvail && !dockSettled) [self note:@"the Dock had not settled when -timeout expired: the final frame is not at rest, so displacements are unreliable (raise -timeout)"];
	if (dockAvail && da.ok && fabs(da.restTop - da.assumedRest) > 8) [self note:[NSString stringWithFormat:@"estimated icon rest top (%.0f) differs from the symmetric-padding assumption (%.0f) by more than 8 pt", da.restTop, da.assumedRest]];
	if (dockAvail && br.count > 0 && !br.hasEnd) [self note:@"first bounce had not ended when recording stopped; fractionOfFirstBounce unavailable"];
	if (streamError) [self note:[NSString stringWithFormat:@"stream stopped with error: %@", streamError]];
	[self note:@"frames are change-driven: no frame is delivered while the screen is static; tFirstPixels/tComplete are the first frames that SHOW the state, not when it began"];

	NSMutableArray *series = [NSMutableArray array];
	for (int i = 0; i < sN; i++) [series addObject:@[jnum(sT[i]), jnum(sV[i])]];
	Params p = O.P;
	NSDictionary *r = @{
		@"launchRequest": @{@"machNs": @((uint64_t)((double)mach * tb.numer / tb.denom)), @"hostClockNs": @(launchNs)},
		@"pid": @(apid),
		@"tWindowListed": jnum(tWindowListed),
		@"tFirstPixels": jnum(tFirstPixels),
		@"tComplete": jnum(tComplete),
		@"contentFirst": isnan(tComplete) ? (id)[NSNull null] : @(contentFirst),
		@"partialFrames": @(partial),
		@"partialFrameTimes": partialTimes,
		@"bounce": @{
			@"available": @(dockAvail),
			@"unavailableReason": dockWhy ?: (id)[NSNull null],
			@"start": jnum(br.start), @"apex": jnum(br.apex), @"apexDisplacementPt": jnum(br.apexDisp),
			@"end": jnum(br.end), @"periodMs": jnum(br.period), @"count": @(br.count),
			@"method": @(da.method), @"restTopMethod": @(da.restMethod),
			@"restTopPt": jnum(da.restTop), @"restTopAssumedPt": jnum(da.assumedRest),
			@"iconColumnsPt": da.colMin >= 0 ? @[@(da.colMin), @(da.colMax)] : (id)[NSNull null],
			@"maxDisplacementPt": jnum(da.maxDisp),
			@"slideEndMs": jnum(da.slideEndMs), @"slideFrames": @(da.wideFrames),
			@"settled": @(dockSettled), @"dockFrames": @(dn),
			@"finalFrameMs": dn ? jnum(dfr[dn - 1].tMs) : (id)[NSNull null],
		},
		@"halfBounce": @{
			@"contentVsBounceStartMs": jnum(hb.contentVsStart),
			@"fractionOfFirstBounce": jnum(hb.fraction),
			@"pass": hb.pass < 0 ? (id)[NSNull null] : @(hb.pass == 1),
		},
		@"frames": @(frames),
		@"framesAfterLaunch": @(framesAfter),
		@"framesInWindow": winFrames, // [t, matchedFraction, changedFraction]
		@"dockSeries": series,        // [t, displacementPt] per Dock-strip frame after launch
		@"screen": @{@"widthPt": @(dispW), @"heightPt": @(dispH), @"captureScale": jnum(scale), @"maxFps": @(fps),
		             @"dockEdge": @[@"bottom", @"left", @"right"][dock.edge], @"dockThicknessPt": @(dock.thickness)},
		@"clock": @{@"clockCheckMs": jnum(clockCheckMs), @"displayTimeMinusPtsMs": jnum(displayVsPtsMs)},
		@"params": @{@"tol": @(p.tol), @"slack": @(p.slack), @"matchFrac": @(p.matchFrac), @"step": @(p.probeStep),
		             @"diffTol": @(p.diffTol), @"firstFrac": @(p.firstFrac), @"bounceTol": @(p.bounceTol),
		             @"minCols": @(p.minCols), @"minRun": @(p.minRun), @"dockPad": @(p.dockPad), @"tile": @(p.tile),
		             @"commonCols": @(p.commonCols), @"settleMs": @(p.settleMs),
		             @"expectW": @(ex.w), @"expectH": @(ex.h)},
		@"videoFrames": @(videoCount), @"dockVideoFrames": @(dockVideoCount),
		@"notes": notes,
	};
	writeJSONFile(r, O.out);
	return 0;
}

@end

// ============================================================================
// (5) main
// ============================================================================

@interface AppDelegate : NSObject <NSApplicationDelegate> { Options o; }
- (instancetype)initWithOptions:(Options)opts;
@end
@implementation AppDelegate
- (instancetype)initWithOptions:(Options)opts { if ((self = [super init])) o = opts; return self; }
- (void)applicationDidFinishLaunching:(NSNotification *)n {
	Options opts = o;
	// Dock geometry from AppKit: the Dock edge is where visibleFrame is inset from frame.
	NSScreen *scr = NSScreen.screens.firstObject; // the menu-bar screen == CGMainDisplayID
	if (scr) {
		NSRect fr = scr.frame, vf = scr.visibleFrame;
		double dB = vf.origin.y - fr.origin.y, dL = vf.origin.x - fr.origin.x;
		double dR = (fr.origin.x + fr.size.width) - (vf.origin.x + vf.size.width);
		opts.dock.sw = (int)fr.size.width; opts.dock.sh = (int)fr.size.height;
		if (dB >= dL && dB >= dR) { opts.dock.edge = 0; opts.dock.thickness = (int)lrint(dB); }
		else if (dL >= dR) { opts.dock.edge = 1; opts.dock.thickness = (int)lrint(dL); }
		else { opts.dock.edge = 2; opts.dock.thickness = (int)lrint(dR); }
		opts.dockAvail = opts.dock.thickness >= 8;
		opts.screenMaxFps = (int)scr.maximumFramesPerSecond;
	}
	if (opts.P.tile <= 0) { // icon size from the Dock's preferences (used for icon width / rest-top sanity check)
		CFPropertyListRef v = CFPreferencesCopyAppValue(CFSTR("tilesize"), CFSTR("com.apple.dock"));
		if (v && CFGetTypeID(v) == CFNumberGetTypeID()) { int ts = 0; CFNumberGetValue(v, kCFNumberIntType, &ts); opts.P.tile = ts; }
		if (v) CFRelease(v);
	}
	dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
		Recorder *rec = [[Recorder alloc] initWithOptions:opts];
		int code = [rec run];
		exit(code);
	});
}
@end

static void usage(void) {
	fprintf(stderr,
	        "usage: BounceRec -target App.app -expect expected.png -out result.json [-video dir] [-timeout 5]\n"
	        "       [-keep] [-warm 500] [-expectscale 2] [-fps N] [-tol 24] [-slack 1] [-match 0.98] [-step 8]\n"
	        "       [-difftol 12] [-firstfrac 0.5] [-dockvideo dir] [-settle 1500] [-bouncetol 16] [-mincols 12]\n"
	        "       [-minrun 2] [-dockpad 40] [-tile N] [-commoncols N] [-resttop N] [-motionpx 30]\n"
	        "       [-shadow 48] [-corner 24] [-offset x,y] [-v]\n"
	        "       BounceRec -selftest\n");
}

int main(int argc, const char **argv) {
	@autoreleasepool {
		Options o = {0};
		o.P = defaultParams();
		o.timeout = 5; o.warmMs = 500; o.expectDiv = 2;
		for (int i = 1; i < argc; i++) {
			const char *a = argv[i];
			#define ARG(name) (strcmp(a, name) == 0 && i + 1 < argc)
			if (strcmp(a, "-selftest") == 0) return runSelfTest();
			else if (ARG("-target")) o.target = absPath(@(argv[++i]));
			else if (ARG("-expect")) o.expect = absPath(@(argv[++i]));
			else if (ARG("-out")) o.out = absPath(@(argv[++i]));
			else if (ARG("-video")) o.videoDir = absPath(@(argv[++i]));
			else if (ARG("-timeout")) o.timeout = atof(argv[++i]);
			else if (strcmp(a, "-keep") == 0) o.keep = 1;
			else if (strcmp(a, "-v") == 0) o.verbose = 1;
			else if (ARG("-warm")) o.warmMs = atoi(argv[++i]);
			else if (ARG("-expectscale")) o.expectDiv = atoi(argv[++i]);
			else if (ARG("-fps")) o.fps = atoi(argv[++i]);
			else if (ARG("-tol")) o.P.tol = atoi(argv[++i]);
			else if (ARG("-slack")) o.P.slack = atoi(argv[++i]);
			else if (ARG("-match")) o.P.matchFrac = atof(argv[++i]);
			else if (ARG("-step")) o.P.probeStep = atoi(argv[++i]);
			else if (ARG("-difftol")) o.P.diffTol = atoi(argv[++i]);
			else if (ARG("-firstfrac")) o.P.firstFrac = atof(argv[++i]);
			else if (ARG("-bouncetol")) o.P.bounceTol = atoi(argv[++i]);
			else if (ARG("-mincols")) o.P.minCols = atoi(argv[++i]);
			else if (ARG("-dockvideo")) o.dockVideoDir = absPath(@(argv[++i]));
			else if (ARG("-settle")) o.P.settleMs = atoi(argv[++i]);
			else if (ARG("-minrun")) o.P.minRun = atoi(argv[++i]);
			else if (ARG("-dockpad")) o.P.dockPad = atoi(argv[++i]);
			else if (ARG("-tile")) o.P.tile = atoi(argv[++i]);
			else if (ARG("-commoncols")) o.P.commonCols = atoi(argv[++i]);
			else if (ARG("-resttop")) o.P.restTop = atof(argv[++i]);
			else if (ARG("-motionpx")) o.P.motionPx = atoi(argv[++i]);
			else if (ARG("-shadow")) o.P.shadow = atoi(argv[++i]);
			else if (ARG("-corner")) o.P.corner = atoi(argv[++i]);
			else if (ARG("-offset")) sscanf(argv[++i], "%d,%d", &o.P.offX, &o.P.offY);
			// anything else (e.g. -NSDocumentRevisionsDebugMode, -ApplePersistence...) is AppKit's
		}
		if (o.P.probeStep < 1) o.P.probeStep = 8;
		if (o.P.minRun < 1) o.P.minRun = 1;
		if (o.P.dockPad < 0) o.P.dockPad = 0;
		if (o.P.slack < 0) o.P.slack = 0;
		if (o.expectDiv < 1) o.expectDiv = 2;
		if (!o.target || !o.expect || !o.out) { usage(); return 64; }
		NSApplication *app = [NSApplication sharedApplication];
		[app setActivationPolicy:NSApplicationActivationPolicyAccessory];
		AppDelegate *del = [[AppDelegate alloc] initWithOptions:o];
		app.delegate = del;
		[app run];
	}
	return 0;
}
