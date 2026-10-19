// measure.m: pixel tools for the GnuiShot captures (see README.md). All
// coordinates are in points from the window's top-left; captures are 2x.
//
//	AppKitScenes -crop IN.png X Y W H ZOOM OUT.png    enlarge a region (nearest neighbour)
//	AppKitScenes -probe IN.png X Y [X Y ...]          colour (sRGB #RRGGBBAA) at points
//	AppKitScenes -profile IN.png X0 Y0 X1 Y1 [STEP]   colours along a line, every STEP pt (0.5)
//	AppKitScenes -radius IN.png X Y W H               body edges and top-left corner radius of a rounded rect near X Y W H
//	AppKitScenes -measure DIR                         the window-level measurements -> DIR/measured.json
#import <AppKit/AppKit.h>
#include <math.h>

typedef struct {
	NSBitmapImageRep *rep;
	const uint8_t *p;
	NSInteger w, h, bpr;
} Img;

static Img load(NSString *path) {
	Img m = {0};
	NSData *d = [NSData dataWithContentsOfFile:path];
	NSBitmapImageRep *r = d ? [NSBitmapImageRep imageRepWithData:d] : nil;
	if (!r) return m;
	// normalise to 8-bit RGBA, non-planar, sRGB, premultiplied
	NSBitmapImageRep *n = [[NSBitmapImageRep alloc] initWithBitmapDataPlanes:NULL
	                                                              pixelsWide:r.pixelsWide
	                                                              pixelsHigh:r.pixelsHigh
	                                                           bitsPerSample:8
	                                                         samplesPerPixel:4
	                                                                hasAlpha:YES
	                                                                isPlanar:NO
	                                                          colorSpaceName:NSDeviceRGBColorSpace
	                                                             bytesPerRow:0
	                                                            bitsPerPixel:32];
	n = [n bitmapImageRepByRetaggingWithColorSpace:NSColorSpace.sRGBColorSpace];
	memset(n.bitmapData, 0, n.bytesPerRow * n.pixelsHigh);
	[NSGraphicsContext saveGraphicsState];
	NSGraphicsContext.currentContext = [NSGraphicsContext graphicsContextWithBitmapImageRep:n];
	NSGraphicsContext.currentContext.imageInterpolation = NSImageInterpolationNone;
	[r drawInRect:NSMakeRect(0, 0, r.pixelsWide, r.pixelsHigh) fromRect:NSZeroRect operation:NSCompositingOperationCopy fraction:1 respectFlipped:NO hints:nil];
	[NSGraphicsContext restoreGraphicsState];
	m.rep = n, m.p = n.bitmapData, m.w = n.pixelsWide, m.h = n.pixelsHigh, m.bpr = n.bytesPerRow;
	return m;
}

typedef struct {
	double r, g, b, a; // straight (not premultiplied), 0..255
} RGBA;

static RGBA px(Img m, NSInteger x, NSInteger y) {
	if (x < 0 || y < 0 || x >= m.w || y >= m.h) return (RGBA){0, 0, 0, 0};
	const uint8_t *q = m.p + y * m.bpr + x * 4;
	double a = q[3];
	if (a <= 0) return (RGBA){0, 0, 0, 0};
	return (RGBA){fmin(q[0] * 255.0 / a, 255), fmin(q[1] * 255.0 / a, 255), fmin(q[2] * 255.0 / a, 255), a};
}

// at averages the 2x2 pixels of the point (x, y)
static RGBA at(Img m, double x, double y) {
	NSInteger X = (NSInteger)floor(x * 2), Y = (NSInteger)floor(y * 2);
	RGBA s = {0};
	double wa = 0;
	for (int dy = 0; dy < 2; dy++)
		for (int dx = 0; dx < 2; dx++) {
			RGBA c = px(m, X + dx, Y + dy);
			s.r += c.r * c.a, s.g += c.g * c.a, s.b += c.b * c.a, s.a += c.a, wa += c.a;
		}
	if (wa > 0) s.r /= wa, s.g /= wa, s.b /= wa;
	s.a /= 4;
	return s;
}

static NSString *hex(RGBA c) { return [NSString stringWithFormat:@"#%02X%02X%02X%02X", (int)lround(c.r), (int)lround(c.g), (int)lround(c.b), (int)lround(c.a)]; }
static double lum(RGBA c) { return 0.2126 * c.r + 0.7152 * c.g + 0.0722 * c.b; }
static double dist(RGBA a, RGBA b) { return sqrt((a.r - b.r) * (a.r - b.r) + (a.g - b.g) * (a.g - b.g) + (a.b - b.b) * (a.b - b.b)); }
static id num(double v) { return isfinite(v) ? (id)@(round(v * 100) / 100) : (id)NSNull.null; }

static int crop(NSString *in, double x, double y, double w, double h, int zoom, NSString *out) {
	Img m = load(in);
	if (!m.rep) return fprintf(stderr, "cannot read %s\n", in.UTF8String), 1;
	NSInteger X = lround(x * 2), Y = lround(y * 2), W = lround(w * 2), H = lround(h * 2);
	NSBitmapImageRep *o = [[NSBitmapImageRep alloc] initWithBitmapDataPlanes:NULL
	                                                              pixelsWide:W * zoom
	                                                              pixelsHigh:H * zoom
	                                                           bitsPerSample:8
	                                                         samplesPerPixel:4
	                                                                hasAlpha:YES
	                                                                isPlanar:NO
	                                                          colorSpaceName:NSDeviceRGBColorSpace
	                                                             bytesPerRow:0
	                                                            bitsPerPixel:32];
	for (NSInteger yy = 0; yy < H * zoom; yy++)
		for (NSInteger xx = 0; xx < W * zoom; xx++) {
			NSInteger sx = X + xx / zoom, sy = Y + yy / zoom;
			uint8_t *d = o.bitmapData + yy * o.bytesPerRow + xx * 4;
			if (sx < 0 || sy < 0 || sx >= m.w || sy >= m.h) {
				memset(d, 0, 4);
				continue;
			}
			memcpy(d, m.p + sy * m.bpr + sx * 4, 4);
		}
	o = [o bitmapImageRepByRetaggingWithColorSpace:NSColorSpace.sRGBColorSpace];
	[[o representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:out atomically:YES];
	return 0;
}

#pragma mark - Measurements

// alphaEdge: the window mask. From the window's top-left corner, the first opaque point along the diagonal gives the corner radius.
static NSDictionary *windowCorner(Img m) {
	double k = 1 - 1 / sqrt(2);
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	struct {
		NSString *n;
		double x, y, dx, dy;
	} c[] = {{@"tl", 0, 0, 1, 1}, {@"tr", m.w / 2.0, 0, -1, 1}, {@"bl", 0, m.h / 2.0, 1, -1}, {@"br", m.w / 2.0, m.h / 2.0, -1, -1}};
	for (int i = 0; i < 4; i++) {
		// sub-pixel: walk in pixel units along the diagonal and find alpha 50 %
		double prev = 0, found = NAN;
		for (double t = 0; t < 80; t += 0.25) {
			double X = c[i].x * 2 + c[i].dx * t, Y = c[i].y * 2 + c[i].dy * t;
			if (c[i].dx < 0) X -= 1;
			if (c[i].dy < 0) Y -= 1;
			double a = px(m, (NSInteger)floor(X), (NSInteger)floor(Y)).a / 255.0;
			if (a >= 0.5 && prev < 0.5) {
				found = t;
				break;
			}
			prev = a;
		}
		// t is per-axis pixels along the diagonal; distance from corner along the diagonal = t*sqrt2 px; r*(sqrt2-1) = that/2 pt
		d[c[i].n] = num(found / k / 2);
	}
	return d;
}


// frac: where c sits between out (0) and in (1)
static double frac(RGBA c, RGBA out, RGBA in) {
	double d = dist(in, out);
	if (d < 1) return NAN;
	double a = dist(c, out), b = dist(c, in);
	return fmin(fmax((a * a - b * b + d * d) / (2 * d * d), 0), 1);
}

// crossing walks pixels from (X, Y) by (dx, dy) and returns the 50 % crossing in points, sub-pixel.
static double crossing(Img m, double x, double y, int dx, int dy, RGBA out, RGBA in, int maxPx) {
	NSInteger X = (NSInteger)floor(x * 2), Y = (NSInteger)floor(y * 2);
	double prev = frac(px(m, X, Y), out, in);
	for (int i = 1; i <= maxPx; i++) {
		double f = frac(px(m, X + dx * i, Y + dy * i), out, in);
		if (f >= 0.5 && prev < 0.5) {
			double t = (i - 1) + (0.5 - prev) / (f - prev);
			// pixel centres are at +0.5; the crossing between centre i-1 and i
			return ((dx ? X : Y) + 0.5 + (dx ? dx : dy) * t) / 2;
		}
		prev = f;
	}
	return NAN;
}

// cornerFit finds the body edges of a rounded rectangle near rect r (points) and fits the top-left
// corner radius from the edge offset on each pixel row of the corner: off = R - sqrt(R^2 - (R-d)^2).
static NSDictionary *cornerFit(Img m, NSRect r) {
	// the inside colour just inside the left edge (clear of icons and text in rows and capsules)
	RGBA in = at(m, NSMinX(r) + fmin(6, r.size.width / 4), NSMidY(r));
	double my = NSMidY(r), mx = NSMinX(r) + fmin(r.size.width / 2, 12);
	RGBA outL = at(m, NSMinX(r) - 4, my), outT = at(m, mx, NSMinY(r) - 4);
	double left = crossing(m, NSMinX(r) - 4, my, 1, 0, outL, in, 24);
	double top = crossing(m, mx, NSMinY(r) - 4, 0, 1, outT, in, 24);
	double right = crossing(m, NSMaxX(r) + 4, my, -1, 0, at(m, NSMaxX(r) + 4, my), in, 24);
	double bottom = crossing(m, mx, NSMaxY(r) + 4, 0, -1, at(m, mx, NSMaxY(r) + 4), in, 24);
	NSMutableArray *rs = [NSMutableArray array];
	if (!isnan(left) && !isnan(top))
		for (double d = 0.25; d < fmin(r.size.height / 2, 24); d += 0.5) {
			double x = crossing(m, left - 2, top + d, 1, 0, outL, in, 80);
			if (isnan(x)) continue;
			double off = x - left;
			if (off < 0.3) break;
			// solve off = R - sqrt(R^2 - (R-d)^2) for R:  R^2 - 2(off+d)R + (off^2 + d^2) = 0
			double b = off + d, disc = b * b - (off * off + d * d);
			if (disc < 0) continue;
			[rs addObject:@(b + sqrt(disc))];
		}
	[rs sortUsingSelector:@selector(compare:)];
	NSMutableDictionary *o = [@{@"left" : num(left), @"top" : num(top), @"right" : num(right), @"bottom" : num(bottom), @"inside" : hex(in), @"outside" : hex(outL)} mutableCopy];
	if (rs.count) {
		o[@"radius"] = num([rs[rs.count / 2] doubleValue]);
		o[@"radiusSamples"] = @(rs.count);
	}
	return o;
}

static NSArray *profile(Img m, double x0, double y0, double x1, double y1, double step) {
	NSMutableArray *a = [NSMutableArray array];
	double L = hypot(x1 - x0, y1 - y0);
	for (double t = 0; t <= L + 1e-9; t += step) {
		double x = x0 + (x1 - x0) * t / L, y = y0 + (y1 - y0) * t / L;
		RGBA c = at(m, x, y);
		[a addObject:@{@"x" : num(x), @"y" : num(y), @"c" : hex(c), @"L" : num(lum(c))}];
	}
	return a;
}

static NSDictionary *J(NSString *path) {
	NSData *d = [NSData dataWithContentsOfFile:path];
	return d ? [NSJSONSerialization JSONObjectWithData:d options:0 error:nil] : nil;
}

static void walk(NSDictionary *v, void (^f)(NSDictionary *)) {
	f(v);
	for (NSDictionary *c in v[@"subviews"]) walk(c, f);
}

static NSRect R(NSDictionary *r) { return NSMakeRect([r[@"x"] doubleValue], [r[@"y"] doubleValue], [r[@"w"] doubleValue], [r[@"h"] doubleValue]); }

// pxColumn samples the pixel column at point x from y0 to y1 at 2x resolution (every pixel row).
static NSArray *pxColumn(Img m, double x, double y0, double y1) {
	NSMutableArray *a = [NSMutableArray array];
	NSInteger X = (NSInteger)floor(x * 2);
	for (NSInteger Y = (NSInteger)floor(y0 * 2); Y < (NSInteger)ceil(y1 * 2); Y++) {
		RGBA c = px(m, X, Y);
		[a addObject:@[ num(Y / 2.0), hex(c) ]];
	}
	return a;
}

static RGBA median(Img m, double x, double y0, double y1) {
	NSMutableArray *v = [NSMutableArray array];
	for (double y = y0; y <= y1; y += 0.5) {
		RGBA c = at(m, x, y);
		[v addObject:@[ @(lum(c)), [NSValue valueWithBytes:&c objCType:@encode(RGBA)] ]];
	}
	[v sortUsingComparator:^NSComparisonResult(NSArray *a, NSArray *b) { return [a[0] compare:b[0]]; }];
	RGBA c;
	[v[v.count / 2][1] getValue:&c];
	return c;
}

static NSDictionary *bandStats(Img m, double x, double y0, double y1) {
	double lo = 1e9, hi = -1e9;
	for (double y = y0; y <= y1; y += 0.5) {
		double l = lum(at(m, x, y));
		lo = fmin(lo, l), hi = fmax(hi, l);
	}
	return @{@"median" : hex(median(m, x, y0, y1)), @"lumMin" : num(lo), @"lumMax" : num(hi)};
}

// freeColumn: the middle of the widest stretch of [x0, x1] that no toolbar item, title or traffic light covers.
static double freeColumn(NSDictionary *dump, double x0, double x1) {
	NSMutableArray *occ = [NSMutableArray array];
	walk(dump[@"tree"], ^(NSDictionary *v) {
		NSString *c = v[@"class"];
		NSRect f = R(v[@"frame"]);
		if (([c isEqual:@"NSToolbarPlatterView"] || [c isEqual:@"_NSToolbarTitleField"] || [c hasPrefix:@"_NSTheme"]) && NSMaxY(f) < 60)
			[occ addObject:[NSValue valueWithRange:NSMakeRange((NSUInteger)floor(fmax(NSMinX(f) - 4, 0)), (NSUInteger)ceil(f.size.width + 8))]];
	});
	double best = -1, bx = (x0 + x1) / 2;
	double run = 0, start = x0;
	for (double x = x0 + 4; x <= x1 - 4; x += 1) {
		BOOL hit = NO;
		for (NSValue *r in occ)
			if (NSLocationInRange((NSUInteger)x, r.rangeValue)) hit = YES;
		if (hit) {
			run = 0;
			start = x + 1;
			continue;
		}
		run += 1;
		if (run > best) best = run, bx = start + run / 2;
	}
	return bx;
}

static NSDictionary *measureOne(NSString *png, NSDictionary *dump) {
	Img m = load(png);
	if (!m.rep) return @{@"error" : @"no png"};
	NSMutableDictionary *o = [NSMutableDictionary dictionary];
	double H = m.h / 2.0;
	double tb = [dump[@"window"][@"titlebarPlusToolbarHeight"] doubleValue];
	o[@"pixelSize"] = @[ @(m.w), @(m.h) ];
	o[@"windowCornerRadiusFromDiagonal"] = windowCorner(m);
	o[@"note"] = @"colours are sRGB #RRGGBBAA from the 2x capture, averaged over the 2x2 pixels of a point; y in points from the window top";

	NSMutableDictionary *tl = [NSMutableDictionary dictionary];
	NSDictionary *lights = dump[@"window"][@"trafficLights"];
	for (NSString *k in lights) {
		NSRect f = R(lights[k]);
		tl[k] = @{@"centre" : hex(at(m, NSMidX(f) - 0.5, NSMidY(f) - 0.5)), @"rim" : hex(at(m, NSMinX(f) + 0.5, NSMidY(f) - 0.5))};
	}
	o[@"trafficLightColours"] = tl;

	NSMutableArray *areas = [NSMutableArray array];
	for (NSDictionary *it in dump[@"splitItems"]) {
		NSRect f = R(it[@"viewFrame"]);
		NSMutableDictionary *a = [@{@"behavior" : it[@"behavior"], @"frame" : it[@"viewFrame"]} mutableCopy];
		if ([it[@"behavior"] isEqual:@"sidebar"]) {
			// the sidebar's own fill: its rightmost 3 pt (inside the selection inset), below the toolbar
			a[@"fill"] = bandStats(m, NSMaxX(f) - 2, tb + 10, H - 10);
			a[@"fillInToolbarBand"] = hex(at(m, NSMaxX(f) - 2, tb / 2));
			a[@"dividerColumn"] = @{@"x-1" : hex(at(m, NSMaxX(f) - 1, H * 0.7)), @"x-0.5" : hex(at(m, NSMaxX(f) - 0.5, H * 0.7)), @"x" : hex(at(m, NSMaxX(f), H * 0.7)), @"x+0.5" : hex(at(m, NSMaxX(f) + 0.5, H * 0.7))};
			a[@"pxRowAcrossDivider"] = ({
				NSMutableArray *r = [NSMutableArray array];
				NSInteger Y = (NSInteger)(H * 0.7 * 2);
				for (NSInteger X = (NSInteger)(NSMaxX(f) * 2) - 3; X <= (NSInteger)(NSMaxX(f) * 2) + 2; X++) [r addObject:@[ num(X / 2.0), hex(px(m, X, Y)) ]];
				r;
			});
		} else if ([it[@"behavior"] isEqual:@"inspector"]) {
			a[@"fill"] = bandStats(m, NSMinX(f) + 3, tb + 10, H - 10);
			a[@"fillInToolbarBand"] = hex(at(m, NSMinX(f) + 3, tb / 2));
		} else {
			double x = freeColumn(dump, NSMinX(f), NSMaxX(f));
			a[@"probeX"] = num(x);
			a[@"toolbarBand"] = bandStats(m, x, 2, tb - 4);
			a[@"belowToolbar"] = hex(at(m, x, tb + 4));
			a[@"pxColumnAtToolbarEdge"] = pxColumn(m, x, tb - 3, tb + 3);
			for (NSDictionary *acc in it[@"bottomAccessories"] ?: @[]) {
				NSRect af = R(acc);
				a[@"bottomAccessory"] = @{@"frame" : acc, @"pxColumnAtTopEdge" : pxColumn(m, NSMinX(af) + 6, NSMinY(af) - 3, NSMinY(af) + 3),
				                          @"band" : bandStats(m, NSMinX(af) + 6, NSMinY(af) + 2, NSMaxY(af) - 2)};
			}
		}
		[areas addObject:a];
	}
	o[@"areas"] = areas;

	NSMutableArray *caps = [NSMutableArray array];
	walk(dump[@"tree"], ^(NSDictionary *v) {
		if (!v[@"glass"]) return;
		NSRect f = R(v[@"frame"]);
		if (f.size.height > 80) return; // the sidebar and inspector panes
		double my = NSMidY(f);
		NSMutableArray *edge = [NSMutableArray array];
		for (NSInteger X = (NSInteger)(NSMinX(f) * 2) - 4; X <= (NSInteger)(NSMinX(f) * 2) + 6; X++) [edge addObject:@[ num(X / 2.0), hex(px(m, X, (NSInteger)(my * 2))) ]];
		[caps addObject:@{
			@"frame" : v[@"frame"],
			@"cornerRadius" : v[@"glass"][@"cornerRadius"],
			@"fillLeftPadding" : hex(at(m, NSMinX(f) + 5, my)),
			@"outsideLeft" : hex(at(m, NSMinX(f) - 3, my)),
			@"pxRowAcrossLeftEdge" : edge,
			@"pxColumnBelow" : pxColumn(m, NSMidX(f), NSMaxY(f) - 2, NSMaxY(f) + 4),
			@"pxColumnAbove" : pxColumn(m, NSMidX(f), NSMinY(f) - 2, NSMinY(f) + 2),
			@"fit" : cornerFit(m, f),
		}];
	});
	o[@"glassCapsules"] = caps;

	NSMutableArray *boxes = [NSMutableArray array];
	walk(dump[@"tree"], ^(NSDictionary *v) {
		if (![v[@"class"] isEqual:@"NSBox"] || [v[@"box"][@"type"] integerValue] != 0) return;
		NSRect f = R(v[@"frame"]);
		if (NSMinY(f) < tb + 4 || NSMaxY(f) > H - 4) return; // only whole, unscrolled-under boxes
		[boxes addObject:@{@"frame" : v[@"frame"], @"fit" : cornerFit(m, f)}];
	});
	if (boxes.count) o[@"groupBoxes"] = boxes;

	NSMutableArray *sel = [NSMutableArray array];
	for (NSDictionary *t in dump[@"tables"])
		for (NSDictionary *r in t[@"rows"]) {
			if (![r[@"selected"] boolValue]) continue;
			NSRect rr = R(r[@"rect"]);
			double my = NSMidY(rr);
			RGBA ref = at(m, NSMinX(rr) + 0.25, my);
			NSMutableDictionary *s = [@{@"table" : t[@"class"], @"style" : t[@"style"], @"row" : r[@"row"], @"rowRect" : r[@"rect"], @"outside" : hex(ref)} mutableCopy];
			double x0 = NAN, x1 = NAN, y0 = NAN, y1 = NAN;
			for (double x = NSMinX(rr); x < NSMinX(rr) + 40; x += 0.5)
				if (dist(at(m, x, my), ref) > 8) {
					x0 = x;
					break;
				}
			if (!isnan(x0)) {
				RGBA fill = at(m, x0 + 2, my);
				s[@"fill"] = hex(fill);
				for (double x = NSMaxX(rr) - 0.5; x > NSMaxX(rr) - 40; x -= 0.5)
					if (dist(at(m, x, my), fill) < 6) {
						x1 = x + 0.5;
						break;
					}
				double cx = x0 + 5; // clear of the rounded corner, short of the icon
				for (double y = my; y > NSMinY(rr) - 6; y -= 0.5)
					if (dist(at(m, cx, y), fill) > 8) {
						y0 = y + 0.5;
						break;
					}
				for (double y = my; y < NSMaxY(rr) + 6; y += 0.5)
					if (dist(at(m, cx, y), fill) > 8) {
						y1 = y;
						break;
					}
				s[@"visible"] = @{@"x0" : num(x0), @"x1" : num(x1), @"y0" : num(y0), @"y1" : num(y1),
				                  @"insetLeft" : num(x0 - NSMinX(rr)), @"insetRight" : num(NSMaxX(rr) - x1), @"insetTop" : num(y0 - NSMinY(rr)), @"insetBottom" : num(NSMaxY(rr) - y1)};
				if (!isnan(y0)) {
					// radius: how far along the 45° diagonal from the visible corner the fill begins
					double k = 1 - 1 / sqrt(2);
					for (double t2 = 0; t2 < 14; t2 += 0.25)
						if (dist(at(m, x0 + t2, y0 + t2), fill) < 10) {
							s[@"radiusFromDiagonal"] = num(t2 / k);
							break;
						}
				}
			}
			if (!isnan(x0)) s[@"fit"] = cornerFit(m, NSMakeRect(x0, NSMinY(rr), NSMaxX(rr) - 10 - x0, rr.size.height));
			if (r[@"text"]) s[@"textColorDeclared"] = r[@"text"][@"color"];
			[sel addObject:s];
		}
	o[@"selections"] = sel;
	return o;
}

static int measureDir(NSString *dir) {
	NSArray *files = [[NSFileManager.defaultManager contentsOfDirectoryAtPath:dir error:nil] sortedArrayUsingSelector:@selector(compare:)];
	NSMutableDictionary *all = [NSMutableDictionary dictionary];
	for (NSString *f in files) {
		if (![f hasSuffix:@".png"] || [f hasPrefix:@"sheet"]) continue;
		NSString *base = f.stringByDeletingPathExtension;
		NSDictionary *dump = J([dir stringByAppendingPathComponent:[base stringByAppendingPathExtension:@"json"]]);
		if (!dump[@"window"]) continue;
		@autoreleasepool {
			all[base] = measureOne([dir stringByAppendingPathComponent:f], dump);
		}
	}
	NSData *js = [NSJSONSerialization dataWithJSONObject:all options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
	[js writeToFile:[dir stringByAppendingPathComponent:@"measured.json"] atomically:YES];
	printf("%lu captures -> %s/measured.json\n", (unsigned long)all.count, dir.UTF8String);
	return 0;
}

int measureMain(int argc, const char **argv, int i) {
	NSString *(^S)(int) = ^(int k) {
		return [NSString stringWithUTF8String:argv[k]];
	};
	NSString *cmd = S(i);
	if ([cmd isEqual:@"-measure"] && i + 1 < argc) return measureDir(S(i + 1));
	if ([cmd isEqual:@"-crop"] && i + 7 < argc)
		return crop(S(i + 1), atof(argv[i + 2]), atof(argv[i + 3]), atof(argv[i + 4]), atof(argv[i + 5]), atoi(argv[i + 6]), S(i + 7));
	if ([cmd isEqual:@"-radius"] && i + 5 < argc) {
		Img m = load(S(i + 1));
		NSDictionary *d = cornerFit(m, NSMakeRect(atof(argv[i + 2]), atof(argv[i + 3]), atof(argv[i + 4]), atof(argv[i + 5])));
		printf("%s\n", [[NSString alloc] initWithData:[NSJSONSerialization dataWithJSONObject:d options:NSJSONWritingSortedKeys error:nil] encoding:NSUTF8StringEncoding].UTF8String);
		return 0;
	}
	if ([cmd isEqual:@"-probe"] && i + 3 < argc) {
		Img m = load(S(i + 1));
		for (int k = i + 2; k + 1 < argc; k += 2) printf("%s,%s %s\n", argv[k], argv[k + 1], hex(at(m, atof(argv[k]), atof(argv[k + 1]))).UTF8String);
		return 0;
	}
	if ([cmd isEqual:@"-profile"] && i + 5 < argc) {
		Img m = load(S(i + 1));
		double step = i + 6 < argc ? atof(argv[i + 6]) : 0.5;
		for (NSDictionary *s in profile(m, atof(argv[i + 2]), atof(argv[i + 3]), atof(argv[i + 4]), atof(argv[i + 5]), step))
			printf("%6.1f %6.1f %s L=%.0f\n", [s[@"x"] doubleValue], [s[@"y"] doubleValue], [s[@"c"] UTF8String], [s[@"L"] doubleValue]);
		return 0;
	}
	fprintf(stderr, "bad pixel-tool arguments\n");
	return 64;
}
