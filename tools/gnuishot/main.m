// GnuiShot: dev tool (not framework code). A small macOS screen-capture helper
// so agents can look at live app windows over SSH, including the OS-drawn
// titlebar, traffic lights, menus and native controls.
//
// Run it through LaunchServices (so TCC treats the app as the responsible
// process; `open` swallows stdout, hence -json):
//	open -W -n GnuiShot.app --args <command> -json /abs/r.json [options]
//	GnuiShot.app/Contents/MacOS/GnuiShot selftest        # pure logic, no screen needed
// tools/gnuishot/shot.sh wraps the `open` call. README.md has the details.
//
// Commands: list | window | display | appearance | permission | selftest.
// Exit codes: 0 ok, 2 screen recording permission needed, 3 runtime/capture
// failure, 4 no matching window/display, 5 timeout (watchdog), 64 bad arguments.
//
// Structure: (1) pure logic on plain objects (argument parsing, window
// selection, display geometry, naming, timing; unit-tested by `selftest`),
// (2) image + JSON output helpers, (3) the self test, (4) the ScreenCaptureKit
// driver, (5) main().
#import <AppKit/AppKit.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreGraphics/CoreGraphics.h>
#import <ImageIO/ImageIO.h>
#include <mach/mach_time.h>
#include <fcntl.h>
#include <math.h>
#include <signal.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#define TOOL_VERSION @"1.0"

enum { kExitOK = 0, kExitPermission = 2, kExitFailure = 3, kExitNoMatch = 4, kExitTimeout = 5, kExitUsage = 64 };

// ============================================================================
// (1) Pure logic
// ============================================================================

// ---- clock ------------------------------------------------------------------

static uint64_t gLaunchTicks;
static mach_timebase_info_data_t gTB;

static void gClockInit(void) {
	gLaunchTicks = mach_absolute_time();
	mach_timebase_info(&gTB);
}
static double gTicksToNs(uint64_t t) { return (double)t * (double)gTB.numer / (double)gTB.denom; }
static double gMsSinceLaunch(uint64_t t) { return t >= gLaunchTicks ? gTicksToNs(t - gLaunchTicks) / 1e6 : -gTicksToNs(gLaunchTicks - t) / 1e6; }
static uint64_t gNsToTicks(double ns) { return (uint64_t)(ns * (double)gTB.denom / (double)gTB.numer); }

// ---- models -----------------------------------------------------------------

@interface GWindow : NSObject
@property CGWindowID windowID;
@property (copy) NSString *title, *ownerName, *bundleID;
@property CGRect frame; // points, top-left origin, global display coordinates
@property BOOL frameMismatch; // ScreenCaptureKit's frame differed from the window server's bounds
@property CGRect scFrame;
@property NSInteger layer;
@property BOOL onScreen, active;
@property pid_t pid;
@property NSInteger zRank; // 0 = frontmost
@property (strong) SCWindow *sc;
@end
@implementation GWindow
@end

@interface GDisplay : NSObject
@property CGDirectDisplayID displayID;
@property CGRect frame; // points, top-left origin, global
@property double scale; // pixels per point
@property BOOL isMain;
@property (strong) SCDisplay *sc;
@end
@implementation GDisplay
@end

static GWindow *gMakeWindow(CGWindowID wid, pid_t pid, NSString *owner, NSString *bundle, NSString *title, CGRect frame, NSInteger layer, NSInteger z, BOOL onScreen) {
	GWindow *w = [GWindow new];
	w.windowID = wid; w.pid = pid; w.ownerName = owner; w.bundleID = bundle; w.title = title;
	w.frame = frame; w.layer = layer; w.zRank = z; w.onScreen = onScreen; w.active = NO;
	return w;
}
static GDisplay *gMakeDisplay(CGDirectDisplayID did, CGRect frame, double scale, BOOL main) {
	GDisplay *d = [GDisplay new];
	d.displayID = did; d.frame = frame; d.scale = scale; d.isMain = main;
	return d;
}

// ---- options ----------------------------------------------------------------

@interface GOpts : NSObject
@property (copy) NSString *command, *jsonPath, *outPath;
@property double timeoutBase;           // seconds before the per-command extras (-timeout)
// selectors
@property (strong) NSNumber *windowID, *pid;
@property (copy) NSString *app, *title;
@property (strong) NSNumber *layer;     // nil = any layer (list) ; window defaults to 0
@property BOOL layerGiven, all;
// window/display capture
@property BOOL shadow, cursor, crop, includeApp, menubar, refresh, opaque, unclipped, noPrompt, global, request;
@property int scale;                    // 0 = display backing scale
@property double pad;
@property int waitMs, repeat, intervalMs, pollMs;
@property (copy) NSString *colorspace;  // srgb | display-p3 | native
@property (copy) NSString *displayArg;  // nil | "main" | decimal id
@property BOOL hasRect;
@property CGRect rect;
@end
@implementation GOpts
@end

static NSString *gUsage(void) {
	return @"usage: GnuiShot <command> -json <path> [options]\n"
	       @"  list        -json p [-app s] [-pid n] [-title s] [-id n] [-layer n|any] [-all]\n"
	       @"  window      -json p -out p.png [-id n] [-pid n] [-app name|bundle] [-title s] [-layer n|any] [-all]\n"
	       @"              [-shadow] [-scale 1|2] [-cursor] [-wait-ms n] [-poll-ms n] [-repeat n] [-interval-ms m]\n"
	       @"              [-crop | -include-app [-menubar]] [-pad pt] [-refresh] [-opaque] [-unclipped]\n"
	       @"              [-colorspace srgb|display-p3|native] [-no-prompt] [-timeout s]\n"
	       @"  display     -json p -out p.png [-display main|id] [-rect x,y,w,h [-global]] [-scale 1|2] [-cursor]\n"
	       @"              [-wait-ms n] [-repeat n] [-interval-ms m] [-colorspace ...] [-no-prompt]\n"
	       @"  appearance  -json p\n"
	       @"  permission  -json p [-request]\n"
	       @"  selftest    [-json p]\n"
	       @"  hangtest    -json p [-timeout s]   (diagnostic: hangs on purpose to prove the watchdog exits)\n"
	       @"exit: 0 ok, 2 permission needed, 3 failure, 4 no match, 5 timeout, 64 bad arguments";
}

static NSSet<NSString *> *gCommands(void) { return [NSSet setWithArray:@[@"list", @"window", @"display", @"appearance", @"permission", @"selftest", @"hangtest"]]; }

// name -> @[kind, commands]; kind f=flag i=int d=double s=string. Commands "*" = all.
static NSDictionary<NSString *, NSArray<NSString *> *> *gOptSpec(void) {
	static NSDictionary *spec;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		spec = @{
			@"-json": @[@"s", @"*"], @"-timeout": @[@"i", @"*"],
			@"-out": @[@"s", @"window display"],
			@"-id": @[@"i", @"list window"], @"-pid": @[@"i", @"list window"],
			@"-app": @[@"s", @"list window"], @"-title": @[@"s", @"list window"],
			@"-layer": @[@"s", @"list window"], @"-all": @[@"f", @"list window"],
			@"-shadow": @[@"f", @"window"], @"-scale": @[@"i", @"window display"],
			@"-cursor": @[@"f", @"window display"], @"-wait-ms": @[@"i", @"window display"], @"-poll-ms": @[@"i", @"window"],
			@"-repeat": @[@"i", @"window display"], @"-interval-ms": @[@"i", @"window display"],
			@"-crop": @[@"f", @"window"], @"-include-app": @[@"f", @"window"], @"-menubar": @[@"f", @"window"],
			@"-pad": @[@"d", @"window"], @"-refresh": @[@"f", @"window"], @"-opaque": @[@"f", @"window"],
			@"-unclipped": @[@"f", @"window"], @"-colorspace": @[@"s", @"window display"],
			@"-display": @[@"s", @"display"], @"-rect": @[@"s", @"display"], @"-global": @[@"f", @"display"],
			@"-request": @[@"f", @"permission"], @"-no-prompt": @[@"f", @"list window display"],
		};
	});
	return spec;
}

static BOOL gParseLong(NSString *s, long long lo, long long hi, long long *out) {
	if (s.length == 0) return NO;
	const char *c = s.UTF8String;
	char *end = NULL;
	errno = 0;
	long long v = strtoll(c, &end, 10);
	if (errno || end == c || *end) return NO;
	if (v < lo || v > hi) return NO;
	*out = v;
	return YES;
}
static BOOL gParseDouble(NSString *s, double lo, double hi, double *out) {
	if (s.length == 0) return NO;
	const char *c = s.UTF8String;
	char *end = NULL;
	errno = 0;
	double v = strtod(c, &end);
	if (errno || end == c || *end || !isfinite(v)) return NO;
	if (v < lo || v > hi) return NO;
	*out = v;
	return YES;
}

static BOOL gParseRect(NSString *s, CGRect *out) {
	NSArray *p = [s componentsSeparatedByString:@","];
	if (p.count != 4) return NO;
	double v[4];
	for (int i = 0; i < 4; i++) if (!gParseDouble([p[i] stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceCharacterSet], -1e6, 1e6, &v[i])) return NO;
	if (v[2] <= 0 || v[3] <= 0) return NO;
	*out = CGRectMake(v[0], v[1], v[2], v[3]);
	return YES;
}

static NSString *gAbsPath(NSString *p) {
	BOOL slash = [p hasSuffix:@"/"]; // the Foundation path calls below drop a trailing slash
	p = [p stringByExpandingTildeInPath];
	if (![p hasPrefix:@"/"]) p = [NSFileManager.defaultManager.currentDirectoryPath stringByAppendingPathComponent:p];
	NSString *r = [p stringByStandardizingPath];
	return (slash && ![r hasSuffix:@"/"]) ? [r stringByAppendingString:@"/"] : r;
}

// -out must end in .png (optionally containing one %0Nd), or name a directory
// (trailing slash, or an existing directory).
static BOOL gValidSequencePattern(NSString *p) {
	NSUInteger n = [[p componentsSeparatedByString:@"%"] count] - 1;
	if (n == 0) return YES;
	if (n != 1) return NO;
	NSRegularExpression *re = [NSRegularExpression regularExpressionWithPattern:@"%0[1-9]d" options:0 error:nil];
	return [re numberOfMatchesInString:p options:0 range:NSMakeRange(0, p.length)] == 1;
}
static BOOL gIsDirPath(NSString *p) {
	if ([p hasSuffix:@"/"]) return YES;
	BOOL isDir = NO;
	return [NSFileManager.defaultManager fileExistsAtPath:p isDirectory:&isDir] && isDir;
}

// Parse args (without argv[0]). Returns nil and sets *err on failure.
static GOpts *gParseArgs(NSArray<NSString *> *args, NSString **err) {
	GOpts *o = [GOpts new];
	o.timeoutBase = 15; o.repeat = 1; o.colorspace = @"srgb";
	NSMutableArray<NSString *> *a = [NSMutableArray array];
	for (NSUInteger i = 0; i < args.count; i++) { // drop LaunchServices/AppKit pass-through arguments
		NSString *t = args[i];
		if ([t hasPrefix:@"-psn_"]) continue;
		if ([t hasPrefix:@"-NS"] || [t hasPrefix:@"-Apple"]) { i++; continue; }
		[a addObject:t];
	}
	if (a.count == 0) { *err = @"missing command"; return nil; }
	NSString *cmd = a[0];
	if (![gCommands() containsObject:cmd]) { *err = [NSString stringWithFormat:@"unknown command '%@'", cmd]; return nil; }
	o.command = cmd;
	NSDictionary *spec = gOptSpec();
	for (NSUInteger i = 1; i < a.count; i++) {
		NSString *k = a[i];
		NSArray *sp = spec[k];
		if (!sp) { *err = [NSString stringWithFormat:@"unknown option '%@'", k]; return nil; }
		NSString *kind = sp[0], *cmds = sp[1];
		if (![cmds isEqualToString:@"*"] && ![[cmds componentsSeparatedByString:@" "] containsObject:cmd]) {
			*err = [NSString stringWithFormat:@"option %@ does not apply to '%@'", k, cmd]; return nil;
		}
		NSString *val = nil;
		if (![kind isEqualToString:@"f"]) {
			if (i + 1 >= a.count) { *err = [NSString stringWithFormat:@"option %@ needs a value", k]; return nil; }
			val = a[++i];
		}
		long long iv = 0; double dv = 0;
		#define BADVAL(what) do { *err = [NSString stringWithFormat:@"bad value '%@' for %@ (%@)", val, k, what]; return nil; } while (0)
		if ([k isEqualToString:@"-json"]) o.jsonPath = gAbsPath(val);
		else if ([k isEqualToString:@"-out"]) o.outPath = gAbsPath(val);
		else if ([k isEqualToString:@"-timeout"]) { if (!gParseLong(val, 1, 3600, &iv)) BADVAL(@"seconds 1..3600"); o.timeoutBase = (double)iv; }
		else if ([k isEqualToString:@"-id"]) { if (!gParseLong(val, 0, UINT32_MAX, &iv)) BADVAL(@"window id"); o.windowID = @(iv); }
		else if ([k isEqualToString:@"-pid"]) { if (!gParseLong(val, 1, INT32_MAX, &iv)) BADVAL(@"process id"); o.pid = @(iv); }
		else if ([k isEqualToString:@"-app"]) { if (val.length == 0) BADVAL(@"non-empty"); o.app = val; }
		else if ([k isEqualToString:@"-title"]) { if (val.length == 0) BADVAL(@"non-empty"); o.title = val; }
		else if ([k isEqualToString:@"-layer"]) {
			o.layerGiven = YES;
			if ([val isEqualToString:@"any"]) o.layer = nil;
			else { if (!gParseLong(val, -100000, 100000, &iv)) BADVAL(@"integer or 'any'"); o.layer = @(iv); }
		}
		else if ([k isEqualToString:@"-all"]) o.all = YES;
		else if ([k isEqualToString:@"-shadow"]) o.shadow = YES;
		else if ([k isEqualToString:@"-scale"]) { if (!gParseLong(val, 1, 2, &iv)) BADVAL(@"1 or 2"); o.scale = (int)iv; }
		else if ([k isEqualToString:@"-cursor"]) o.cursor = YES;
		else if ([k isEqualToString:@"-wait-ms"]) { if (!gParseLong(val, 0, 600000, &iv)) BADVAL(@"ms 0..600000"); o.waitMs = (int)iv; }
		else if ([k isEqualToString:@"-poll-ms"]) { if (!gParseLong(val, 0, 600000, &iv)) BADVAL(@"ms 0..600000"); o.pollMs = (int)iv; }
		else if ([k isEqualToString:@"-repeat"]) { if (!gParseLong(val, 1, 1000, &iv)) BADVAL(@"1..1000"); o.repeat = (int)iv; }
		else if ([k isEqualToString:@"-interval-ms"]) { if (!gParseLong(val, 0, 600000, &iv)) BADVAL(@"ms 0..600000"); o.intervalMs = (int)iv; }
		else if ([k isEqualToString:@"-crop"]) o.crop = YES;
		else if ([k isEqualToString:@"-include-app"]) o.includeApp = YES;
		else if ([k isEqualToString:@"-menubar"]) o.menubar = YES;
		else if ([k isEqualToString:@"-pad"]) { if (!gParseDouble(val, 0, 2000, &dv)) BADVAL(@"points 0..2000"); o.pad = dv; }
		else if ([k isEqualToString:@"-refresh"]) o.refresh = YES;
		else if ([k isEqualToString:@"-opaque"]) o.opaque = YES;
		else if ([k isEqualToString:@"-unclipped"]) o.unclipped = YES;
		else if ([k isEqualToString:@"-colorspace"]) {
			if (![@[@"srgb", @"display-p3", @"native"] containsObject:val]) BADVAL(@"srgb, display-p3 or native");
			o.colorspace = val;
		}
		else if ([k isEqualToString:@"-display"]) {
			if (![val isEqualToString:@"main"] && !gParseLong(val, 1, UINT32_MAX, &iv)) BADVAL(@"'main' or a display id");
			o.displayArg = val;
		}
		else if ([k isEqualToString:@"-rect"]) { CGRect r; if (!gParseRect(val, &r)) BADVAL(@"x,y,w,h with w,h > 0"); o.rect = r; o.hasRect = YES; }
		else if ([k isEqualToString:@"-global"]) o.global = YES;
		else if ([k isEqualToString:@"-request"]) o.request = YES;
		else if ([k isEqualToString:@"-no-prompt"]) o.noPrompt = YES;
	}
	// Per-command requirements and combinations.
	BOOL isCapture = [cmd isEqualToString:@"window"] || [cmd isEqualToString:@"display"];
	if (!o.jsonPath && ![cmd isEqualToString:@"selftest"]) { *err = @"-json <path> is required (open swallows stdout)"; return nil; }
	if (isCapture) {
		if (!o.outPath) { *err = [NSString stringWithFormat:@"%@ needs -out <path.png>", cmd]; return nil; }
		if (!gValidSequencePattern(o.outPath)) { *err = @"-out may contain at most one %0Nd (N = 1..9)"; return nil; }
		if (!gIsDirPath(o.outPath) && ![o.outPath.lowercaseString hasSuffix:@".png"]) { *err = @"-out must end in .png (or be a directory)"; return nil; }
		if (o.repeat == 1 && o.intervalMs != 0) { *err = @"-interval-ms needs -repeat > 1"; return nil; }
	}
	if ([cmd isEqualToString:@"window"]) {
		if (o.crop && o.includeApp) { *err = @"-crop and -include-app are mutually exclusive"; return nil; }
		BOOL region = o.crop || o.includeApp;
		if (o.shadow && region) { *err = @"-shadow only applies to single-window capture; with -crop/-include-app use -pad to include the shadow area"; return nil; }
		if (o.unclipped && region) { *err = @"-unclipped only applies to single-window capture"; return nil; }
		if (o.pad > 0 && !region) { *err = @"-pad needs -crop or -include-app"; return nil; }
		if (o.menubar && !o.includeApp) { *err = @"-menubar needs -include-app"; return nil; }
		if (!o.layerGiven) o.layer = @0;
	}
	if ([cmd isEqualToString:@"display"]) {
		if (o.global && !o.hasRect) { *err = @"-global needs -rect"; return nil; }
		if (o.global && o.displayArg == nil) { /* display chosen from the rect */ }
	}
	return o;
}

// ---- window selection (pure) --------------------------------------------------

static BOOL gContainsCI(NSString *hay, NSString *needle) { return hay && [hay rangeOfString:needle options:NSCaseInsensitiveSearch].location != NSNotFound; }
static BOOL gEqualCI(NSString *a, NSString *b) { return a && [a caseInsensitiveCompare:b] == NSOrderedSame; }
static BOOL gDegenerate(GWindow *w) { return w.frame.size.width * w.frame.size.height < 16; }

// Windows matching the selectors, frontmost first. `forSelection`: pick-one
// semantics (degenerate/invisible windows ignored unless asked for by id, the
// layer filter is bypassed by id). Otherwise list semantics (nothing hidden).
static NSArray<GWindow *> *gMatchWindows(NSArray<GWindow *> *wins, GOpts *s, BOOL forSelection) {
	NSMutableArray<GWindow *> *c = [NSMutableArray array];
	for (GWindow *w in wins) {
		if (s.windowID && w.windowID != s.windowID.unsignedIntValue) continue;
		if (s.pid && w.pid != s.pid.intValue) continue;
		if (s.title && !gContainsCI(w.title, s.title)) continue;
		if (!s.windowID) {
			if (s.layer && w.layer != s.layer.integerValue) continue;
			if (forSelection && gDegenerate(w)) continue;
		}
		[c addObject:w];
	}
	if (s.app) { // exact name/bundle id wins; else substring of name or bundle id
		NSMutableArray *exact = [NSMutableArray array], *sub = [NSMutableArray array];
		for (GWindow *w in c) {
			if (gEqualCI(w.ownerName, s.app) || gEqualCI(w.bundleID, s.app)) [exact addObject:w];
			else if (gContainsCI(w.ownerName, s.app) || gContainsCI(w.bundleID, s.app)) [sub addObject:w];
		}
		c = exact.count ? exact : sub;
	}
	[c sortUsingComparator:^NSComparisonResult(GWindow *x, GWindow *y) {
		if (x.zRank != y.zRank) return x.zRank < y.zRank ? NSOrderedAscending : NSOrderedDescending;
		if (x.windowID != y.windowID) return x.windowID < y.windowID ? NSOrderedAscending : NSOrderedDescending;
		return NSOrderedSame;
	}];
	return c;
}

// ---- geometry (pure) ----------------------------------------------------------

// AppKit (NSScreen) rects are bottom-left origin, y up, relative to the primary
// display's bottom-left. Global display (CG / ScreenCaptureKit) rects are
// top-left origin, y down, relative to the primary display's top-left.
static CGRect gTopLeftFromAppKit(CGRect r, double primaryHeight) { return CGRectMake(r.origin.x, primaryHeight - r.origin.y - r.size.height, r.size.width, r.size.height); }
static CGRect gAppKitFromTopLeft(CGRect r, double primaryHeight) { return gTopLeftFromAppKit(r, primaryHeight); } // the flip is its own inverse

static double gArea(CGRect r) { return r.size.width > 0 && r.size.height > 0 ? r.size.width * r.size.height : 0; }
static CGRect gIntersect(CGRect a, CGRect b) {
	double x0 = fmax(CGRectGetMinX(a), CGRectGetMinX(b)), y0 = fmax(CGRectGetMinY(a), CGRectGetMinY(b));
	double x1 = fmin(CGRectGetMaxX(a), CGRectGetMaxX(b)), y1 = fmin(CGRectGetMaxY(a), CGRectGetMaxY(b));
	if (x1 <= x0 || y1 <= y0) return CGRectZero;
	return CGRectMake(x0, y0, x1 - x0, y1 - y0);
}
// Display holding most of `f`; nil if `f` is on none. Ties go to the lower display id.
static GDisplay *gDisplayForFrame(NSArray<GDisplay *> *ds, CGRect f) {
	GDisplay *best = nil; double ba = 0;
	for (GDisplay *d in ds) {
		double a = gArea(gIntersect(f, d.frame));
		if (a > ba || (a == ba && a > 0 && best && d.displayID < best.displayID)) { best = d; ba = a; }
	}
	return best;
}
static CGRect gGlobalToLocal(CGRect g, GDisplay *d) { return CGRectOffset(g, -d.frame.origin.x, -d.frame.origin.y); }
static CGRect gLocalToGlobal(CGRect l, GDisplay *d) { return CGRectOffset(l, d.frame.origin.x, d.frame.origin.y); }
static CGRect gInflate(CGRect r, double pad) { return CGRectMake(r.origin.x - pad, r.origin.y - pad, r.size.width + 2 * pad, r.size.height + 2 * pad); }

// Window frame: the window server's bounds (CGWindowList, top-left points) are the reference; ScreenCaptureKit's
// own frame is used when the window server did not list the window. *mismatch is set if the two differ by > 1 pt.
static CGRect gPickFrame(CGRect sc, const CGRect *cg, BOOL *mismatch) {
	*mismatch = NO;
	if (!cg) return sc;
	*mismatch = fabs(cg->origin.x - sc.origin.x) > 1 || fabs(cg->origin.y - sc.origin.y) > 1 || fabs(cg->size.width - sc.size.width) > 1 || fabs(cg->size.height - sc.size.height) > 1;
	return *cg;
}

// Point size of a single-window capture. Without the shadow the image is exactly the window frame. With the
// shadow the filter's contentRect is used when it is plausibly the frame plus shadow (>= the frame, <= frame + 200 pt per axis).
static CGSize gWindowCaptureSize(CGRect frame, CGRect contentRect, BOOL shadow) {
	if (!shadow) return frame.size;
	BOOL ok = contentRect.size.width >= frame.size.width - 0.5 && contentRect.size.height >= frame.size.height - 0.5 &&
	          contentRect.size.width <= frame.size.width + 200 && contentRect.size.height <= frame.size.height + 200;
	return ok ? contentRect.size : frame.size;
}

static void gPixelSize(CGSize pts, double scale, size_t *w, size_t *h) {
	long pw = lround(pts.width * scale), ph = lround(pts.height * scale);
	*w = pw < 1 ? 1 : (size_t)pw; *h = ph < 1 ? 1 : (size_t)ph;
}
static double gScaleFromModeSizes(size_t pixelW, size_t pointW) { return (pixelW > 0 && pointW > 0) ? (double)pixelW / (double)pointW : 1.0; }

// ---- naming and timing (pure) --------------------------------------------------

// Output path of frame i. A %0Nd pattern is always filled in; otherwise repeat == 1 is
// the path itself (a directory gets out.png) and a sequence gets "-%03d" before ".png".
static NSString *gFramePath(NSString *out, BOOL isDir, int repeat, int i) {
	NSString *p = out;
	if (isDir) p = [p stringByAppendingPathComponent:@"out.png"];
	if ([p containsString:@"%"]) return [NSString stringWithFormat:p, i]; // validated: exactly one %0Nd
	if (repeat <= 1) return p;
	NSString *ext = p.pathExtension.length ? p.pathExtension : @"png";
	return [NSString stringWithFormat:@"%@-%03d.%@", [p stringByDeletingPathExtension], i, ext];
}

// Watchdog: 15 s base + wait + the repeat sequence (+ 0.5 s per frame for capture and encode).
static double gTimeoutSeconds(GOpts *o) {
	double t = o.timeoutBase + o.waitMs / 1000.0 + o.pollMs / 1000.0;
	if ([o.command isEqualToString:@"window"] || [o.command isEqualToString:@"display"])
		t += (double)(o.repeat - 1) * o.intervalMs / 1000.0 + 0.5 * o.repeat;
	return t;
}

// ---- appearance (pure parts) ---------------------------------------------------

static NSString *gAppearanceFromInterfaceStyle(NSString *style) { return [style isKindOfClass:NSString.class] && [style caseInsensitiveCompare:@"Dark"] == NSOrderedSame ? @"DarkAqua" : @"Aqua"; }
static NSString *gAccentName(NSNumber *idx) { // AppleAccentColor; absent = multicolor
	if (!idx) return @"multicolor";
	switch (idx.intValue) {
	case -1: return @"graphite";
	case 0: return @"red";
	case 1: return @"orange";
	case 2: return @"yellow";
	case 3: return @"green";
	case 4: return @"blue";
	case 5: return @"purple";
	case 6: return @"pink";
	default: return @"unknown";
	}
}
static NSString *gHex(double r, double g, double b) {
	#define C8(x) ((int)lround(fmin(1.0, fmax(0.0, (x))) * 255.0))
	return [NSString stringWithFormat:@"#%02X%02X%02X", C8(r), C8(g), C8(b)];
}

// True if every pixel (4 bytes) equals the first (a blank/black/transparent capture).
static BOOL gBufferUniform(const uint8_t *p, size_t w, size_t h, size_t stride) {
	if (!p || w == 0 || h == 0) return YES;
	uint32_t first;
	memcpy(&first, p, 4);
	for (size_t y = 0; y < h; y++) {
		const uint8_t *row = p + y * stride;
		for (size_t x = 0; x < w; x++) {
			uint32_t v;
			memcpy(&v, row + 4 * x, 4);
			if (v != first) return NO;
		}
	}
	return YES;
}

// ============================================================================
// (2) Output helpers
// ============================================================================

static NSDictionary *gRectDict(CGRect r) { return @{@"x": @(r.origin.x), @"y": @(r.origin.y), @"w": @(r.size.width), @"h": @(r.size.height)}; }

static BOOL gEnsureParent(NSString *path) {
	return [NSFileManager.defaultManager createDirectoryAtPath:[path stringByDeletingLastPathComponent] withIntermediateDirectories:YES attributes:nil error:nil];
}

// Write data to path atomically (temp file in the same directory, then rename).
static BOOL gWriteAtomic(NSData *data, NSString *path) {
	if (!gEnsureParent(path)) return NO;
	NSString *tmp = [path stringByAppendingFormat:@".tmp%d", getpid()];
	if (![data writeToFile:tmp options:0 error:nil]) return NO;
	if (rename(tmp.fileSystemRepresentation, path.fileSystemRepresentation) != 0) { unlink(tmp.fileSystemRepresentation); return NO; }
	return YES;
}

static NSData *gJSONData(NSDictionary *d) {
	@try {
		return [NSJSONSerialization dataWithJSONObject:d options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys | NSJSONWritingWithoutEscapingSlashes error:nil];
	} @catch (NSException *e) {
		return nil;
	}
}

// PNG via ImageIO, atomically. Returns an error string or nil.
static NSString *gWritePNG(CGImageRef img, NSString *path) {
	if (!img) return @"no image";
	if (!gEnsureParent(path)) return [NSString stringWithFormat:@"cannot create directory for %@", path];
	NSString *tmp = [path stringByAppendingFormat:@".tmp%d", getpid()];
	CGImageDestinationRef dst = CGImageDestinationCreateWithURL((__bridge CFURLRef)[NSURL fileURLWithPath:tmp], CFSTR("public.png"), 1, NULL);
	if (!dst) return [NSString stringWithFormat:@"cannot create %@", path];
	CGImageDestinationAddImage(dst, img, NULL);
	BOOL ok = CGImageDestinationFinalize(dst);
	CFRelease(dst);
	if (!ok) { unlink(tmp.fileSystemRepresentation); return [NSString stringWithFormat:@"PNG encode failed for %@", path]; }
	if (rename(tmp.fileSystemRepresentation, path.fileSystemRepresentation) != 0) { unlink(tmp.fileSystemRepresentation); return [NSString stringWithFormat:@"cannot move PNG into place: %@", path]; }
	return nil;
}

// Render into an RGBA8 sRGB buffer and test for a single colour.
static BOOL gImageUniform(CGImageRef img) {
	size_t w = CGImageGetWidth(img), h = CGImageGetHeight(img);
	if (w == 0 || h == 0 || w * h > 100000000) return NO;
	CGColorSpaceRef cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
	size_t stride = w * 4;
	uint8_t *buf = calloc(h, stride);
	CGContextRef cx = CGBitmapContextCreate(buf, w, h, 8, stride, cs, kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
	CGColorSpaceRelease(cs);
	if (!cx) { free(buf); return NO; }
	CGContextDrawImage(cx, CGRectMake(0, 0, w, h), img);
	BOOL u = gBufferUniform(buf, w, h, stride);
	CGContextRelease(cx);
	free(buf);
	return u;
}

// ============================================================================
// (3) Self test
// ============================================================================

static int gChecks, gFailures;
static NSMutableArray<NSString *> *gFailureLog;
static NSMutableArray<NSDictionary *> *gSections;
static int gSectionFailuresAtStart;
static NSString *gSectionName;

static void gSectionBegin(NSString *n) { gSectionName = n; gSectionFailuresAtStart = gFailures; }
static void gSectionEnd(void) {
	BOOL ok = gFailures == gSectionFailuresAtStart;
	fprintf(stderr, "%s %s\n", ok ? "PASS" : "FAIL", gSectionName.UTF8String);
	[gSections addObject:@{@"section": gSectionName, @"pass": @(ok)}];
}
static void gCheckImpl(BOOL ok, const char *expr, int line, NSString *msg) {
	gChecks++;
	if (ok) return;
	gFailures++;
	NSString *s = [NSString stringWithFormat:@"line %d: %s%@%@", line, expr, msg.length ? @" : " : @"", msg ?: @""];
	[gFailureLog addObject:s];
	fprintf(stderr, "  FAIL %s\n", s.UTF8String);
}
#define CHECK(...) gCheckImpl((__VA_ARGS__), #__VA_ARGS__, __LINE__, nil)
#define CHECKF(cond, ...) gCheckImpl((cond), #cond, __LINE__, [NSString stringWithFormat:__VA_ARGS__])

static BOOL gNear(double a, double b) { return fabs(a - b) < 1e-9; }

// Parse helper: returns options or error text.
static GOpts *P(NSString *cmdline, NSString **err) {
	NSArray *a = [cmdline componentsSeparatedByString:@" "];
	*err = nil;
	return gParseArgs(a, err);
}

static void testParsing(void) {
	gSectionBegin(@"argument parsing");
	NSString *e;
	GOpts *o = P(@"list -json /tmp/x.json", &e);
	CHECKF(o && !e, @"%@", e);
	CHECK([o.command isEqualToString:@"list"] && [o.jsonPath isEqualToString:@"/tmp/x.json"]);
	CHECK(o.layer == nil && !o.all); // list shows every layer by default

	o = P(@"window -json /tmp/j.json -out /tmp/w.png -id 42 -pid 77 -app Gnui -title Demo -shadow -scale 2 -cursor -wait-ms 250 -repeat 5 -interval-ms 40", &e);
	CHECKF(o && !e, @"%@", e);
	CHECK(o.windowID.intValue == 42 && o.pid.intValue == 77);
	CHECK([o.app isEqualToString:@"Gnui"] && [o.title isEqualToString:@"Demo"]);
	CHECK(o.shadow && o.cursor && o.scale == 2 && o.waitMs == 250 && o.repeat == 5 && o.intervalMs == 40);
	CHECK(o.layer && o.layer.integerValue == 0); // window defaults to layer 0
	CHECK([o.colorspace isEqualToString:@"srgb"]);
	CHECK(gNear(o.timeoutBase, 15));

	o = P(@"window -json /tmp/j.json -out /tmp/w.png -poll-ms 3000", &e);
	CHECK(o && o.pollMs == 3000);
	o = P(@"window -json /tmp/j.json -out /tmp/w.png -layer any", &e);
	CHECK(o && o.layer == nil && o.layerGiven);
	o = P(@"window -json /tmp/j.json -out /tmp/w.png -layer 101", &e);
	CHECK(o && o.layer.integerValue == 101);
	o = P(@"window -json /tmp/j.json -out /tmp/w.png -crop -pad 12.5 -refresh -opaque -colorspace display-p3 -no-prompt -timeout 40", &e);
	CHECKF(o && !e, @"%@", e);
	CHECK(o.crop && gNear(o.pad, 12.5) && o.refresh && o.opaque && o.noPrompt && gNear(o.timeoutBase, 40));
	CHECK([o.colorspace isEqualToString:@"display-p3"]);
	o = P(@"window -json /tmp/j.json -out /tmp/w.png -include-app -menubar -pad 4", &e);
	CHECKF(o && !e, @"%@", e);
	o = P(@"window -json /tmp/j.json -out /tmp/w.png -unclipped -shadow", &e);
	CHECKF(o && !e, @"%@", e);

	o = P(@"display -json /tmp/j.json -out /tmp/d.png -display 3 -rect 10,20,300.5,200 -scale 1", &e);
	CHECKF(o && !e, @"%@", e);
	CHECK([o.displayArg isEqualToString:@"3"] && o.hasRect && CGRectEqualToRect(o.rect, CGRectMake(10, 20, 300.5, 200)) && !o.global);
	o = P(@"display -json /tmp/j.json -out /tmp/d.png -display main -rect -5,-6,10,10 -global", &e);
	CHECKF(o && !e && o.global && o.rect.origin.x == -5, @"%@", e);
	o = P(@"display -json /tmp/j.json -out /tmp/d.png", &e);
	CHECK(o && !o.hasRect && o.displayArg == nil);

	CHECK(P(@"appearance -json /tmp/a.json", &e) != nil);
	o = P(@"permission -json /tmp/a.json -request", &e);
	CHECK(o && o.request);
	o = P(@"selftest", &e);
	CHECK(o && o.jsonPath == nil); // selftest runs without -json
	o = P(@"selftest -json /tmp/s.json", &e);
	CHECK(o && o.jsonPath != nil);

	// Pass-through junk from LaunchServices/AppKit is ignored.
	o = P(@"list -psn_0_12345 -json /tmp/x.json -NSDocumentRevisionsDebugMode YES -AppleLanguages (en)", &e);
	CHECKF(o && !e, @"%@", e);

	// Relative paths become absolute; "~" expands.
	o = P(@"list -json rel/out.json", &e);
	CHECK(o && [o.jsonPath hasPrefix:@"/"] && [o.jsonPath hasSuffix:@"rel/out.json"]);
	CHECK([gAbsPath(@"/tmp/shots/") hasSuffix:@"/shots/"] && [gAbsPath(@"/tmp/a.png") hasSuffix:@"/a.png"] && ![gAbsPath(@"/tmp/a/../b.png") containsString:@".."]);
	CHECK([gAbsPath(@"~/shots/") hasSuffix:@"/shots/"] && [gAbsPath(@"~/shots/") hasPrefix:@"/"]);
	o = P(@"list -json ~/x.json", &e);
	CHECK(o && [o.jsonPath hasPrefix:@"/"] && ![o.jsonPath containsString:@"~"]);

	// Errors (each must name the problem).
	struct { const char *line; const char *needle; } bad[] = {
		{"", "missing command"},
		{"frobnicate -json /tmp/x", "unknown command"},
		{"list", "-json"},
		{"list -json", "needs a value"},
		{"list -json /tmp/x -bogus", "unknown option"},
		{"list -json /tmp/x -shadow", "does not apply"},
		{"display -json /tmp/x -out /tmp/a.png -app Foo", "does not apply"},
		{"window -json /tmp/x", "-out"},
		{"window -json /tmp/x -out /tmp/a.jpg", ".png"},
		{"window -json /tmp/x -out /tmp/a.png -scale 3", "-scale"},
		{"window -json /tmp/x -out /tmp/a.png -scale 0", "-scale"},
		{"window -json /tmp/x -out /tmp/a.png -scale abc", "-scale"},
		{"window -json /tmp/x -out /tmp/a.png -id -4", "-id"},
		{"window -json /tmp/x -out /tmp/a.png -id 4x", "-id"},
		{"window -json /tmp/x -out /tmp/a.png -pid 0", "-pid"},
		{"window -json /tmp/x -out /tmp/a.png -repeat 0", "-repeat"},
		{"window -json /tmp/x -out /tmp/a.png -repeat 1001", "-repeat"},
		{"window -json /tmp/x -out /tmp/a.png -wait-ms -1", "-wait-ms"},
		{"window -json /tmp/x -out /tmp/a.png -interval-ms 10", "-repeat"},
		{"window -json /tmp/x -out /tmp/a.png -layer foo", "-layer"},
		{"window -json /tmp/x -out /tmp/a.png -colorspace cmyk", "-colorspace"},
		{"window -json /tmp/x -out /tmp/a.png -crop -include-app", "mutually exclusive"},
		{"window -json /tmp/x -out /tmp/a.png -crop -shadow", "-shadow"},
		{"window -json /tmp/x -out /tmp/a.png -pad 3", "-pad"},
		{"window -json /tmp/x -out /tmp/a.png -pad -3 -crop", "-pad"},
		{"window -json /tmp/x -out /tmp/a.png -menubar", "-menubar"},
		{"window -json /tmp/x -out /tmp/a.png -crop -unclipped", "-unclipped"},
		{"window -json /tmp/x -out /tmp/a%d%d.png -repeat 2", "%0Nd"},
		{"window -json /tmp/x -out /tmp/a%s.png", "%0Nd"},
		{"display -json /tmp/x -out /tmp/a.png -rect 1,2,3", "-rect"},
		{"display -json /tmp/x -out /tmp/a.png -rect 1,2,0,3", "-rect"},
		{"display -json /tmp/x -out /tmp/a.png -rect a,b,c,d", "-rect"},
		{"display -json /tmp/x -out /tmp/a.png -global", "-global"},
		{"display -json /tmp/x -out /tmp/a.png -display zero", "-display"},
		{"display -json /tmp/x -out /tmp/a.png -display 0", "-display"},
		{"display -json /tmp/x -out /tmp/a.png -timeout 0", "-timeout"},
		{"display -json /tmp/x -out /tmp/a.png -poll-ms 5", "does not apply"},
		{"window -json /tmp/x -out /tmp/a.png -poll-ms -5", "-poll-ms"},
	};
	for (size_t i = 0; i < sizeof bad / sizeof bad[0]; i++) {
		NSString *err = nil;
		NSString *line = @(bad[i].line);
		NSArray *args = line.length ? [line componentsSeparatedByString:@" "] : @[];
		GOpts *r = gParseArgs(args, &err);
		CHECKF(r == nil && err && [err containsString:@(bad[i].needle)], @"'%s' -> %@", bad[i].line, err);
	}

	// Output path validity.
	CHECK(gValidSequencePattern(@"/tmp/x.png") && gValidSequencePattern(@"/tmp/x-%03d.png") && gValidSequencePattern(@"/tmp/x-%d9.png") == NO);
	CHECK(gIsDirPath(@"/tmp/") && gIsDirPath(NSTemporaryDirectory()) && !gIsDirPath(@"/nonexistent-dir-xyz/a.png"));
	o = P(@"window -json /tmp/x -out /tmp/shots/", &e);
	CHECKF(o && !e, @"directory -out accepted: %@", e);
	CHECK(gParseArgs(@[@"window", @"-json", @"/tmp/x", @"-out", @"/tmp/s-%03d.PNG", @"-repeat", @"3"], &e) != nil);
	gSectionEnd();
}

static NSArray<GWindow *> *fakeWindows(void) {
	return @[
		gMakeWindow(10, 100, @"Safari", @"com.apple.Safari", @"Apple", CGRectMake(0, 30, 1000, 700), 0, 5, YES),
		gMakeWindow(11, 100, @"Safari", @"com.apple.Safari", @"Reading List", CGRectMake(50, 60, 400, 500), 0, 2, YES),
		gMakeWindow(12, 100, @"Safari", @"com.apple.Safari", @"", CGRectMake(60, 70, 200, 300), 101, 0, YES),
		gMakeWindow(20, 200, @"GnuiDemo", @"dev.gonativeui.demo", @"Demo window", CGRectMake(300, 100, 640, 480), 0, 3, YES),
		gMakeWindow(21, 200, @"GnuiDemo", @"dev.gonativeui.demo", @"", CGRectMake(0, 0, 1, 1), 0, 1, YES),
		gMakeWindow(22, 300, @"Safari Technology Preview", @"com.apple.SafariTechnologyPreview", @"Preview", CGRectMake(10, 10, 800, 600), 0, 4, YES),
		gMakeWindow(30, 200, @"GnuiDemo", @"dev.gonativeui.demo", @"Offscreen", CGRectMake(5000, 5000, 300, 300), 0, 1000, NO),
	];
}
static NSArray<NSNumber *> *ids(NSArray<GWindow *> *ws) {
	NSMutableArray *r = [NSMutableArray array];
	for (GWindow *w in ws) [r addObject:@(w.windowID)];
	return r;
}
static NSArray<NSNumber *> *sel(NSString *line) { // pick-one semantics
	NSString *e;
	NSMutableArray *a = [NSMutableArray arrayWithObject:@"window"];
	[a addObjectsFromArray:[line componentsSeparatedByString:@" "]];
	[a addObjectsFromArray:@[@"-json", @"/tmp/j", @"-out", @"/tmp/o.png"]];
	if ([line isEqualToString:@""]) [a removeObject:@""];
	GOpts *o = gParseArgs(a, &e);
	if (!o) return @[@-1];
	return ids(gMatchWindows(fakeWindows(), o, YES));
}
static NSArray<NSNumber *> *lst(NSString *line) { // list semantics
	NSString *e;
	NSMutableArray *a = [NSMutableArray arrayWithObject:@"list"];
	if (line.length) [a addObjectsFromArray:[line componentsSeparatedByString:@" "]];
	[a addObjectsFromArray:@[@"-json", @"/tmp/j"]];
	GOpts *o = gParseArgs(a, &e);
	if (!o) return @[@-1];
	return ids(gMatchWindows(fakeWindows(), o, NO));
}

static void gCheckIDs(NSArray *got, NSArray *want, int line) {
	gCheckImpl([got isEqualToArray:want], "window ids", line, [NSString stringWithFormat:@"got %@ want %@", got, want]);
}
#define CHECKIDS(got, ...) gCheckIDs((got), @[__VA_ARGS__], __LINE__)

static void testSelection(void) {
	gSectionBegin(@"window selection");
	// No selector: frontmost layer-0 window; the layer-101 popup (z 0) and the 1x1 helper (z 1) are skipped.
	CHECKIDS(sel(@""), @11, @20, @22, @10, @30);
	CHECKIDS(sel(@"-app Safari"), @11, @10);
	CHECKIDS(sel(@"-app safari"), @11, @10);          // case-insensitive
	CHECKIDS(sel(@"-app com.apple.Safari"), @11, @10); // bundle id
	CHECKIDS(sel(@"-app technology"), @22);           // substring fallback
	CHECKIDS(sel(@"-app SafariTechnologyPreview"), @22); // bundle-id substring
	CHECKIDS(sel(@"-app GnuiDemo"), @20, @30);        // the 1x1 helper is skipped; the off-screen window sorts last
	CHECKIDS(sel(@"-app Nope"));
	CHECKIDS(sel(@"-title read"), @11);
	CHECKIDS(sel(@"-title DEMO"), @20);
	CHECKIDS(sel(@"-app Safari -title apple"), @10);  // selectors combine
	CHECKIDS(sel(@"-app Safari -title demo"));
	CHECKIDS(sel(@"-pid 100"), @11, @10);             // layer 0 only
	CHECKIDS(sel(@"-pid 100 -layer 101"), @12);
	CHECKIDS(sel(@"-pid 100 -layer any"), @12, @11, @10);
	CHECKIDS(sel(@"-pid 200"), @20, @30);             // the 1x1 helper is never auto-selected
	CHECKIDS(sel(@"-id 12"), @12);                    // id bypasses the layer filter
	CHECKIDS(sel(@"-id 21"), @21);                    // ...and the degenerate filter
	CHECKIDS(sel(@"-id 999"));
	CHECKIDS(sel(@"-id 12 -pid 200"));                      // id must still agree with other selectors
	CHECKIDS(sel(@"-id 20 -app GnuiDemo"), @20);
	// List semantics: everything, front to back, nothing hidden.
	CHECKIDS(lst(@""), @12, @21, @11, @20, @22, @10, @30);
	CHECKIDS(lst(@"-layer 0 -app Safari"), @11, @10);
	CHECKIDS(lst(@"-pid 200"), @21, @20, @30);
	CHECKIDS(lst(@"-id 21"), @21);
	// Ordering is by z-rank, with window id breaking ties.
	NSArray *ws = @[gMakeWindow(9, 1, @"A", @"a", @"t", CGRectMake(0, 0, 10, 10), 0, 7, YES), gMakeWindow(8, 1, @"A", @"a", @"t", CGRectMake(0, 0, 10, 10), 0, 7, YES), gMakeWindow(7, 1, @"A", @"a", @"t", CGRectMake(0, 0, 10, 10), 0, 9, YES)];
	NSString *e;
	GOpts *o = gParseArgs(@[@"window", @"-json", @"/tmp/j", @"-out", @"/tmp/o.png", @"-app", @"A"], &e);
	CHECKIDS(ids(gMatchWindows(ws, o, YES)), @8, @9, @7);
	// Missing titles/owners (windows without an owning application) never crash the matcher.
	NSArray *odd = @[gMakeWindow(1, 0, nil, nil, nil, CGRectMake(0, 0, 100, 100), 0, 0, YES)];
	o = gParseArgs(@[@"window", @"-json", @"/tmp/j", @"-out", @"/tmp/o.png", @"-app", @"x", @"-title", @"y"], &e);
	CHECK([gMatchWindows(odd, o, YES) count] == 0);
	gSectionEnd();
}

static void testGeometry(void) {
	gSectionBegin(@"frame conversion and displays");
	// Main 1512x982 at the origin; an external 2560x1440 to its right whose top sits 200 pt above the main
	// display's top; a 1920x1080 display to the left, top 100 pt below the main display's top.
	GDisplay *main_ = gMakeDisplay(1, CGRectMake(0, 0, 1512, 982), 2, YES);
	GDisplay *ext = gMakeDisplay(2, CGRectMake(1512, -200, 2560, 1440), 1, NO);
	GDisplay *left = gMakeDisplay(3, CGRectMake(-1920, 100, 1920, 1080), 1, NO);
	NSArray *ds = @[main_, ext, left];
	double H = 982; // primary height
	// AppKit frames of these displays are bottom-left based.
	CGRect akMain = CGRectMake(0, 0, 1512, 982);
	CGRect akExt = CGRectMake(1512, 982 - (-200) - 1440, 2560, 1440);   // y = -258
	CGRect akLeft = CGRectMake(-1920, 982 - 100 - 1080, 1920, 1080);    // y = -198
	CHECK(CGRectEqualToRect(gTopLeftFromAppKit(akMain, H), main_.frame));
	CHECK(CGRectEqualToRect(gTopLeftFromAppKit(akExt, H), ext.frame));
	CHECK(CGRectEqualToRect(gTopLeftFromAppKit(akLeft, H), left.frame));
	CHECK(akExt.origin.y == -258 && akLeft.origin.y == -198);
	for (GDisplay *d in ds) CHECK(CGRectEqualToRect(gAppKitFromTopLeft(gTopLeftFromAppKit(d.frame, H), H), d.frame));
	// A window in AppKit coordinates (bottom-left) -> top-left: 100 pt tall window whose bottom is at y=50.
	CGRect w = gTopLeftFromAppKit(CGRectMake(20, 50, 300, 100), H);
	CHECK(CGRectEqualToRect(w, CGRectMake(20, 832, 300, 100)));

	// Which display a window is on.
	CHECK(gDisplayForFrame(ds, CGRectMake(100, 100, 800, 600)) == main_);
	CHECK(gDisplayForFrame(ds, CGRectMake(1600, -150, 800, 600)) == ext);
	CHECK(gDisplayForFrame(ds, CGRectMake(-1000, 200, 500, 400)) == left);
	CHECK(gDisplayForFrame(ds, CGRectMake(-100, 150, 400, 300)) == main_);  // straddles main/left, mostly main
	CHECK(gDisplayForFrame(ds, CGRectMake(-300, 150, 400, 300)) == left);   // mostly left
	CHECK(gDisplayForFrame(ds, CGRectMake(1400, 0, 200, 100)) == main_);    // 112 pt on main vs 88 pt on the external
	CHECK(gDisplayForFrame(ds, CGRectMake(1450, 0, 200, 100)) == ext);      // 62 pt on main vs 138 pt on the external
	CHECK(gDisplayForFrame(ds, CGRectMake(90000, 90000, 10, 10)) == nil);   // on no display
	CHECK(gDisplayForFrame(@[], CGRectMake(0, 0, 10, 10)) == nil);
	CHECK(gDisplayForFrame(ds, CGRectMake(0, 0, 0, 0)) == nil);             // empty frame
	// Equal overlap: the lower display id wins (deterministic).
	GDisplay *a = gMakeDisplay(5, CGRectMake(0, 0, 100, 100), 1, NO), *b = gMakeDisplay(4, CGRectMake(100, 0, 100, 100), 1, NO);
	CHECK(gDisplayForFrame(@[a, b], CGRectMake(50, 0, 100, 100)) == b);

	// Local/global conversion.
	CGRect g = CGRectMake(1600, -100, 200, 100);
	CGRect l = gGlobalToLocal(g, ext);
	CHECK(CGRectEqualToRect(l, CGRectMake(88, 100, 200, 100)));
	CHECK(CGRectEqualToRect(gLocalToGlobal(l, ext), g));
	CHECK(CGRectEqualToRect(gGlobalToLocal(CGRectMake(-1000, 200, 500, 400), left), CGRectMake(920, 100, 500, 400)));
	// Clipping to a display.
	CGRect clipped = gIntersect(gInflate(CGRectMake(10, 10, 100, 100), 30), main_.frame);
	CHECK(CGRectEqualToRect(clipped, CGRectMake(0, 0, 140, 140)));
	CHECK(CGRectIsEmpty(gIntersect(CGRectMake(2000, 2000, 10, 10), main_.frame)));
	CHECK(CGRectIsEmpty(gIntersect(CGRectMake(1512, 0, 10, 10), main_.frame))); // touching edges do not overlap
	CHECK(CGRectEqualToRect(gInflate(CGRectMake(5, 5, 10, 10), 2.5), CGRectMake(2.5, 2.5, 15, 15)));

	// Frame source and capture size.
	BOOL mm;
	CGRect cgr = CGRectMake(10, 20, 300, 200);
	CHECK(CGRectEqualToRect(gPickFrame(CGRectMake(10.4, 19.6, 300, 200.5), &cgr, &mm), cgr) && !mm);
	CHECK(CGRectEqualToRect(gPickFrame(CGRectMake(10, -180, 300, 200), &cgr, &mm), cgr) && mm); // flipped y: reported, window server wins
	CHECK(CGRectEqualToRect(gPickFrame(CGRectMake(1, 2, 3, 4), NULL, &mm), CGRectMake(1, 2, 3, 4)) && !mm);
	CGSize cs_ = gWindowCaptureSize(CGRectMake(0, 0, 300, 200), CGRectMake(-20, -10, 340, 240), NO);
	CHECK(cs_.width == 300 && cs_.height == 200);
	cs_ = gWindowCaptureSize(CGRectMake(0, 0, 300, 200), CGRectMake(-20, -10, 340, 240), YES);
	CHECK(cs_.width == 340 && cs_.height == 240);
	cs_ = gWindowCaptureSize(CGRectMake(0, 0, 300, 200), CGRectMake(0, 0, 100, 100), YES);   // implausible contentRect
	CHECK(cs_.width == 300 && cs_.height == 200);
	cs_ = gWindowCaptureSize(CGRectMake(0, 0, 300, 200), CGRectMake(0, 0, 5000, 5000), YES);
	CHECK(cs_.width == 300 && cs_.height == 200);

	// Pixel sizes.
	size_t pw, ph;
	gPixelSize(CGSizeMake(800, 600), 2, &pw, &ph);
	CHECK(pw == 1600 && ph == 1200);
	gPixelSize(CGSizeMake(800.4, 600.5), 1, &pw, &ph);
	CHECK(pw == 800 && ph == 601);
	gPixelSize(CGSizeMake(0.1, 0), 1, &pw, &ph);
	CHECK(pw == 1 && ph == 1); // never zero
	CHECK(gNear(gScaleFromModeSizes(3024, 1512), 2) && gNear(gScaleFromModeSizes(2560, 2560), 1) && gNear(gScaleFromModeSizes(0, 100), 1));
	gSectionEnd();
}

static void testNamingAndTiming(void) {
	gSectionBegin(@"output naming and timeouts");
	CHECK([gFramePath(@"/t/shot.png", NO, 1, 0) isEqualToString:@"/t/shot.png"]);
	CHECK([gFramePath(@"/t/shot.png", NO, 3, 0) isEqualToString:@"/t/shot-000.png"]);
	CHECK([gFramePath(@"/t/shot.png", NO, 3, 12) isEqualToString:@"/t/shot-012.png"]);
	CHECK([gFramePath(@"/t/shot.png", NO, 1000, 999) isEqualToString:@"/t/shot-999.png"]);
	CHECK([gFramePath(@"/t/f-%04d.png", NO, 3, 7) isEqualToString:@"/t/f-0007.png"]);
	CHECK([gFramePath(@"/t/f-%04d.png", NO, 1, 0) isEqualToString:@"/t/f-0000.png"]);
	CHECK([gFramePath(@"/t/dir/", YES, 1, 0) isEqualToString:@"/t/dir/out.png"]);
	CHECK([gFramePath(@"/t/dir/", YES, 4, 2) isEqualToString:@"/t/dir/out-002.png"]);
	CHECK([gFramePath(@"/t/dir", YES, 4, 2) isEqualToString:@"/t/dir/out-002.png"]);
	CHECK([gFramePath(@"/t/my.shots/a.PNG", NO, 2, 1) isEqualToString:@"/t/my.shots/a-001.PNG"]);
	CHECK([gFramePath(@"/t/p ath/é.png", NO, 2, 1) isEqualToString:@"/t/p ath/é-001.png"]);

	NSString *e;
	GOpts *o = gParseArgs(@[@"list", @"-json", @"/t/j"], &e);
	CHECK(gNear(gTimeoutSeconds(o), 15));
	o = gParseArgs(@[@"window", @"-json", @"/t/j", @"-out", @"/t/o.png"], &e);
	CHECK(gNear(gTimeoutSeconds(o), 15.5));
	o = gParseArgs(@[@"window", @"-json", @"/t/j", @"-out", @"/t/o.png", @"-wait-ms", @"2000", @"-repeat", @"11", @"-interval-ms", @"100"], &e);
	CHECK(gNear(gTimeoutSeconds(o), 15 + 2 + 1.0 + 5.5));
	o = gParseArgs(@[@"window", @"-json", @"/t/j", @"-out", @"/t/o.png", @"-poll-ms", @"4000"], &e);
	CHECK(gNear(gTimeoutSeconds(o), 15 + 4 + 0.5));
	o = gParseArgs(@[@"display", @"-json", @"/t/j", @"-out", @"/t/o.png", @"-timeout", @"30"], &e);
	CHECK(gNear(gTimeoutSeconds(o), 30.5));
	// Clock helpers are monotonic and consistent.
	uint64_t t0 = mach_absolute_time();
	CHECK(gMsSinceLaunch(t0) >= 0);
	CHECK(fabs(gTicksToNs(gNsToTicks(1e9)) - 1e9) < 1e6);
	gSectionEnd();
}

static void testAppearance(void) {
	gSectionBegin(@"appearance helpers");
	CHECK([gAppearanceFromInterfaceStyle(@"Dark") isEqualToString:@"DarkAqua"]);
	CHECK([gAppearanceFromInterfaceStyle(@"dark") isEqualToString:@"DarkAqua"]);
	CHECK([gAppearanceFromInterfaceStyle(nil) isEqualToString:@"Aqua"]);
	CHECK([gAppearanceFromInterfaceStyle(@"Light") isEqualToString:@"Aqua"]);
	CHECK([gAppearanceFromInterfaceStyle((NSString *)@42) isEqualToString:@"Aqua"]);
	CHECK([gAccentName(nil) isEqualToString:@"multicolor"] && [gAccentName(@-1) isEqualToString:@"graphite"] && [gAccentName(@0) isEqualToString:@"red"]);
	CHECK([gAccentName(@4) isEqualToString:@"blue"] && [gAccentName(@5) isEqualToString:@"purple"] && [gAccentName(@6) isEqualToString:@"pink"] && [gAccentName(@99) isEqualToString:@"unknown"]);
	CHECK([gHex(0, 0.5, 1) isEqualToString:@"#0080FF"] && [gHex(1.2, -1, 0.2) isEqualToString:@"#FF0033"]);
	gSectionEnd();
}

static void testImages(void) {
	gSectionBegin(@"PNG writing and blank detection");
	// Buffer-level uniform test.
	uint8_t px[4 * 4 * 3];
	memset(px, 0x7F, sizeof px);
	CHECK(gBufferUniform(px, 4, 3, 16));
	px[16 + 4 * 2 + 1] ^= 1; // one differing byte in row 1
	CHECK(!gBufferUniform(px, 4, 3, 16));
	px[16 + 4 * 2 + 1] ^= 1;
	px[0] ^= 1; // first pixel differs from the rest
	CHECK(!gBufferUniform(px, 4, 3, 16));
	px[0] ^= 1;
	px[4 * 3] = 0; // row 0, last pixel
	CHECK(!gBufferUniform(px, 4, 3, 16));
	CHECK(gBufferUniform(NULL, 0, 0, 0));

	// A 40x30 RGBA gradient with a transparent corner, written and read back.
	size_t W = 40, Hh = 30;
	uint8_t *buf = calloc(Hh, W * 4);
	for (size_t y = 0; y < Hh; y++) for (size_t x = 0; x < W; x++) {
		uint8_t *p = buf + (y * W + x) * 4;
		BOOL corner = x < 3 && y < 3;
		p[0] = corner ? 0 : (uint8_t)(x * 6); p[1] = corner ? 0 : (uint8_t)(y * 8); p[2] = corner ? 0 : 200; p[3] = corner ? 0 : 255;
	}
	CGColorSpaceRef cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
	CGContextRef cx = CGBitmapContextCreate(buf, W, Hh, 8, W * 4, cs, kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
	CGImageRef img = CGBitmapContextCreateImage(cx);
	CHECK(img != NULL && !gImageUniform(img));
	NSString *dir = [NSTemporaryDirectory() stringByAppendingPathComponent:[NSString stringWithFormat:@"gnuishot-selftest-%d", getpid()]];
	NSString *path = [dir stringByAppendingPathComponent:@"sub dir/é shot.png"]; // nested, non-ASCII, space
	NSString *werr = gWritePNG(img, path);
	CHECKF(werr == nil, @"%@", werr);
	NSArray *left = [NSFileManager.defaultManager contentsOfDirectoryAtPath:[path stringByDeletingLastPathComponent] error:nil];
	CHECKF(left.count == 1, @"no temp file left behind: %@", left); // atomic: only the final file
	CGImageSourceRef src = CGImageSourceCreateWithURL((__bridge CFURLRef)[NSURL fileURLWithPath:path], NULL);
	CHECK(src != NULL);
	if (src) {
		CGImageRef back = CGImageSourceCreateImageAtIndex(src, 0, NULL);
		CHECK(back && CGImageGetWidth(back) == W && CGImageGetHeight(back) == Hh);
		if (back) {
			uint8_t *b2 = calloc(Hh, W * 4);
			CGContextRef c2 = CGBitmapContextCreate(b2, W, Hh, 8, W * 4, cs, kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
			CGContextDrawImage(c2, CGRectMake(0, 0, W, Hh), back);
			CHECK(memcmp(buf, b2, W * Hh * 4) == 0); // lossless round trip incl. alpha
			CGContextRelease(c2);
			free(b2);
			CFRelease(back);
		}
		CFRelease(src);
	}
	CHECK(gWritePNG(img, path) == nil); // overwrite works
	CHECK(gWritePNG(NULL, path) != nil);
	CHECK(gWritePNG(img, @"/dev/null/nope/x.png") != nil); // unwritable location reports an error
	// Uniform image detection on a real CGImage.
	memset(buf, 0, W * Hh * 4);
	CGImageRef blank = CGBitmapContextCreateImage(cx);
	CHECK(gImageUniform(blank));
	CGImageRelease(blank);

	// JSON writing: atomic, sorted, error-free for ordinary values; NaN must not crash.
	NSString *jp = [dir stringByAppendingPathComponent:@"r.json"];
	NSData *jd = gJSONData(@{@"b": @1, @"a": @"x/y", @"r": gRectDict(CGRectMake(1, 2, 3, 4)), @"t": @"é\"\n"});
	CHECK(jd && gWriteAtomic(jd, jp));
	id back = [NSJSONSerialization JSONObjectWithData:[NSData dataWithContentsOfFile:jp] options:0 error:nil];
	CHECK([back isKindOfClass:NSDictionary.class] && [back[@"a"] isEqualToString:@"x/y"] && [back[@"t"] isEqualToString:@"é\"\n"] && [back[@"r"][@"h"] intValue] == 4);
	CHECK(gJSONData(@{@"x": @(NAN)}) == nil);

	CGImageRelease(img);
	CGContextRelease(cx);
	CGColorSpaceRelease(cs);
	free(buf);
	[NSFileManager.defaultManager removeItemAtPath:dir error:nil];
	gSectionEnd();
}

static NSDictionary *gRunSelfTest(void) {
	gChecks = 0; gFailures = 0;
	gFailureLog = [NSMutableArray array];
	gSections = [NSMutableArray array];
	testParsing();
	testSelection();
	testGeometry();
	testNamingAndTiming();
	testAppearance();
	testImages();
	fprintf(stderr, "%s: %d checks, %d failures\n", gFailures ? "SELFTEST FAILED" : "selftest ok", gChecks, gFailures);
	NSMutableDictionary *d = [@{@"command": @"selftest", @"checks": @(gChecks), @"failures": @(gFailures), @"sections": gSections, @"tool": @{@"name": @"GnuiShot", @"version": TOOL_VERSION}} mutableCopy];
	if (gFailures) { d[@"error"] = @"selftest failed"; d[@"failureLog"] = gFailureLog; }
	return d;
}

// ============================================================================
// (4) ScreenCaptureKit driver
// ============================================================================

static NSLock *gFinishLock;
static NSMutableArray<NSDictionary *> *gProgress; // frame snapshots recorded so far (reported on timeout)
static NSLock *gProgressLock;
static NSString *gJSONPath;

// Write the result once and exit. Safe to call from any thread; only the first call wins.
__attribute__((noreturn)) static void gFinish(NSDictionary *d, int code) {
	[gFinishLock lock]; // never unlocked: the process ends here
	NSMutableDictionary *m = [d mutableCopy];
	m[@"exitCode"] = @(code);
	NSData *data = gJSONData(m) ?: [@"{\"error\":\"internal: result not serialisable\"}" dataUsingEncoding:NSUTF8StringEncoding];
	if (gJSONPath && !gWriteAtomic(data, gJSONPath)) fprintf(stderr, "cannot write %s\n", gJSONPath.UTF8String);
	fwrite(data.bytes, 1, data.length, stdout);
	fputc('\n', stdout);
	fflush(stdout);
	_exit(code);
}
__attribute__((noreturn)) static void gFail(NSString *msg, int code, NSDictionary *extra) {
	NSMutableDictionary *d = [@{@"error": msg} mutableCopy];
	if (extra) [d addEntriesFromDictionary:extra];
	gFinish(d, code);
}
__attribute__((noreturn)) static void gUsageFail(NSString *msg) { gFail(msg, kExitUsage, @{@"usage": gUsage()}); }

__attribute__((noreturn)) static void gPermissionNeeded(GOpts *o) {
	if (!o.noPrompt) CGRequestScreenCaptureAccess(); // adds GnuiShot to the Screen Recording list / shows the prompt
	gFail(@"screen recording permission needed", kExitPermission, @{
		@"detail": @"Enable GnuiShot in System Settings > Privacy & Security > Screen & System Audio Recording, then re-run. After a rebuild the grant is void: remove GnuiShot with '-' and add it again.",
		@"app": NSBundle.mainBundle.bundlePath ?: @"",
	});
}

static dispatch_semaphore_t gSem(void) { return dispatch_semaphore_create(0); }
static BOOL gWaitSem(dispatch_semaphore_t s, double secs) { return dispatch_semaphore_wait(s, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(secs * NSEC_PER_SEC))) == 0; }

static id gOnMain(id (^blk)(void)) {
	if ([NSThread isMainThread]) return blk();
	__block id r;
	dispatch_sync(dispatch_get_main_queue(), ^{ r = blk(); });
	return r;
}

// ---- crash reporting -----------------------------------------------------------
// An uncaught exception or fatal signal still leaves an error JSON behind (best effort), so the caller never
// has to guess why there is no result.

static char *gJSONPathC;

static void gSignalHandler(int sig) {
	if (gJSONPathC) {
		char msg[128];
		int n = snprintf(msg, sizeof msg, "{\"error\":\"GnuiShot crashed (signal %d)\",\"exitCode\":3}\n", sig);
		int fd = open(gJSONPathC, O_WRONLY | O_CREAT | O_TRUNC, 0644);
		if (fd >= 0 && n > 0) { ssize_t w = write(fd, msg, (size_t)n); (void)w; close(fd); }
	}
	_exit(kExitFailure);
}
static void gExceptionHandler(NSException *e) {
	gFail([NSString stringWithFormat:@"uncaught exception %@: %@", e.name, e.reason], kExitFailure, nil);
}
static void gInstallCrashHandlers(void) {
	NSSetUncaughtExceptionHandler(gExceptionHandler);
	int sigs[] = {SIGSEGV, SIGBUS, SIGABRT, SIGILL, SIGTRAP, SIGFPE};
	for (size_t i = 0; i < sizeof sigs / sizeof sigs[0]; i++) signal(sigs[i], gSignalHandler);
}

// ---- appearance --------------------------------------------------------------

static id gPrefGlobal(NSString *key) {
	id v = CFBridgingRelease(CFPreferencesCopyValue((__bridge CFStringRef)key, kCFPreferencesAnyApplication, kCFPreferencesCurrentUser, kCFPreferencesAnyHost));
	return v;
}

static NSDictionary *gAppearanceInfo(void) {
	return gOnMain(^id{
		NSAppearance *ap = NSApp.effectiveAppearance ?: NSAppearance.currentDrawingAppearance;
		NSAppearanceName nm = [ap bestMatchFromAppearancesWithNames:@[NSAppearanceNameAqua, NSAppearanceNameDarkAqua]];
		NSString *style = gPrefGlobal(@"AppleInterfaceStyle");
		NSString *fromPrefs = gAppearanceFromInterfaceStyle(style);
		NSString *name = [nm isEqualToString:NSAppearanceNameDarkAqua] ? @"DarkAqua" : @"Aqua";
		__block double r = 0, g = 0, b = 0; __block BOOL got = NO;
		[ap performAsCurrentDrawingAppearance:^{
			NSColor *c = [NSColor.controlAccentColor colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
			if (c) { r = c.redComponent; g = c.greenComponent; b = c.blueComponent; got = YES; }
		}];
		id accentPref = gPrefGlobal(@"AppleAccentColor");
		NSNumber *accentIdx = [accentPref isKindOfClass:NSNumber.class] ? accentPref : nil;
		NSMutableDictionary *accent = [@{@"name": gAccentName(accentIdx), @"source": @"NSColor.controlAccentColor"} mutableCopy];
		if (got) { accent[@"hex"] = gHex(r, g, b); accent[@"rgb"] = @[@(r), @(g), @(b)]; }
		if (accentIdx) accent[@"prefIndex"] = accentIdx;
		id hl = gPrefGlobal(@"AppleHighlightColor");
		id variant = gPrefGlobal(@"AppleAquaColorVariant");
		NSWorkspace *ws = NSWorkspace.sharedWorkspace;
		NSMutableDictionary *d = [@{
			@"appearance": name,
			@"isDark": @([name isEqualToString:@"DarkAqua"]),
			@"appearanceFromPrefs": fromPrefs,
			@"interfaceStyle": [style isKindOfClass:NSString.class] ? style : @"Light",
			@"autoSwitches": @([gPrefGlobal(@"AppleInterfaceStyleSwitchesAutomatically") boolValue]),
			@"accent": accent,
			@"reduceTransparency": @(ws.accessibilityDisplayShouldReduceTransparency),
			@"reduceMotion": @(ws.accessibilityDisplayShouldReduceMotion),
			@"increaseContrast": @(ws.accessibilityDisplayShouldIncreaseContrast),
			@"differentiateWithoutColor": @(ws.accessibilityDisplayShouldDifferentiateWithoutColor),
			@"invertColors": @(ws.accessibilityDisplayShouldInvertColors),
			@"macOS": NSProcessInfo.processInfo.operatingSystemVersionString,
		} mutableCopy];
		if ([hl isKindOfClass:NSString.class]) d[@"highlightColorPref"] = hl;
		if ([variant isKindOfClass:NSNumber.class]) d[@"aquaColorVariantPref"] = variant;
		return d;
	});
}
static NSDictionary *gAppearanceSummary(void) {
	NSDictionary *a = gAppearanceInfo();
	NSMutableDictionary *s = [@{@"name": a[@"appearance"], @"accentName": a[@"accent"][@"name"]} mutableCopy];
	if (a[@"accent"][@"hex"]) s[@"accentHex"] = a[@"accent"][@"hex"];
	return s;
}

// ---- screen / window inventory ---------------------------------------------------

static NSDictionary *gWindowDict(GWindow *w, GDisplay *d) {
	NSMutableDictionary *m = [@{
		@"windowID": @(w.windowID), @"title": w.title ?: @"", @"frame": gRectDict(w.frame),
		@"layer": @(w.layer), @"onScreen": @(w.onScreen), @"active": @(w.active), @"zOrder": @(w.zRank),
		@"ownerName": w.ownerName ?: @"", @"bundleID": w.bundleID ?: @"", @"pid": @(w.pid),
	} mutableCopy];
	if (d) m[@"display"] = @{@"id": @(d.displayID), @"scale": @(d.scale), @"isMain": @(d.isMain)};
	if (w.frameMismatch) m[@"scFrame"] = gRectDict(w.scFrame); // diagnostic: ScreenCaptureKit disagreed with the window server
	return m;
}

static double gDisplayScale(CGDirectDisplayID did) {
	CGDisplayModeRef m = CGDisplayCopyDisplayMode(did);
	double s = 0;
	if (m) { s = gScaleFromModeSizes(CGDisplayModeGetPixelWidth(m), CGDisplayModeGetWidth(m)); CGDisplayModeRelease(m); }
	if (s <= 0) s = 1;
	return s;
}

// AppKit frames by display id (read on the main thread).
static NSDictionary<NSNumber *, NSValue *> *gAppKitFrames(void) {
	return gOnMain(^id{
		NSMutableDictionary *m = [NSMutableDictionary dictionary];
		for (NSScreen *s in NSScreen.screens) {
			NSNumber *n = s.deviceDescription[@"NSScreenNumber"];
			if (n) m[n] = [NSValue valueWithRect:s.frame];
		}
		return m;
	});
}
static double gPrimaryHeight(void) {
	return [(NSNumber *)gOnMain(^id{ return @(NSScreen.screens.firstObject.frame.size.height); }) doubleValue];
}

static NSArray<GDisplay *> *gDisplaysFrom(SCShareableContent *c) {
	NSMutableArray *r = [NSMutableArray array];
	for (SCDisplay *d in c.displays) {
		GDisplay *g = gMakeDisplay(d.displayID, d.frame, gDisplayScale(d.displayID), d.displayID == CGMainDisplayID());
		g.sc = d;
		[r addObject:g];
	}
	return r;
}

static NSArray<GWindow *> *gWindowsFrom(SCShareableContent *c) {
	// Front-to-back order comes from the window server's on-screen list.
	NSMutableDictionary<NSNumber *, NSNumber *> *z = [NSMutableDictionary dictionary];
	NSMutableDictionary<NSNumber *, NSValue *> *bounds = [NSMutableDictionary dictionary];
	CFArrayRef info = CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
	NSArray *infoA = CFBridgingRelease(info);
	NSInteger rank = 0;
	for (NSDictionary *e in infoA) {
		NSNumber *n = e[(__bridge NSString *)kCGWindowNumber];
		if (n && !z[n]) z[n] = @(rank++);
		NSDictionary *b = e[(__bridge NSString *)kCGWindowBounds];
		CGRect br;
		if (n && b && CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)b, &br)) bounds[n] = [NSValue valueWithRect:br];
	}
	NSMutableArray *r = [NSMutableArray array];
	NSInteger idx = 0;
	for (SCWindow *w in c.windows) {
		GWindow *g = [GWindow new];
		g.windowID = w.windowID; g.title = w.title ?: @""; g.layer = w.windowLayer;
		NSValue *bv = bounds[@(w.windowID)];
		CGRect cgb = bv ? bv.rectValue : CGRectZero;
		BOOL mm;
		g.frame = gPickFrame(w.frame, bv ? &cgb : NULL, &mm);
		g.scFrame = w.frame; g.frameMismatch = mm;
		g.onScreen = w.onScreen; g.active = w.active;
		g.ownerName = w.owningApplication.applicationName ?: @""; g.bundleID = w.owningApplication.bundleIdentifier ?: @"";
		g.pid = w.owningApplication.processID;
		g.zRank = z[@(w.windowID)] ? z[@(w.windowID)].integerValue : 1000000 + idx;
		g.sc = w;
		idx++;
		[r addObject:g];
	}
	return r;
}

static SCShareableContent *gFetchContent(BOOL onScreenOnly, NSString **err, BOOL *permission) {
	__block SCShareableContent *content = nil;
	__block NSError *e = nil;
	dispatch_semaphore_t s = gSem();
	[SCShareableContent getShareableContentExcludingDesktopWindows:YES onScreenWindowsOnly:onScreenOnly completionHandler:^(SCShareableContent *c, NSError *er) {
		content = c; e = er; dispatch_semaphore_signal(s);
	}];
	if (!gWaitSem(s, 10)) { *err = @"timed out waiting for ScreenCaptureKit (SCShareableContent)"; return nil; }
	if (!content) {
		if (e.code == SCStreamErrorUserDeclined) *permission = YES;
		*err = [NSString stringWithFormat:@"SCShareableContent failed: %@", e.localizedDescription];
	}
	return content;
}

static SCShareableContent *gContentOrFail(GOpts *o, BOOL onScreenOnly) {
	NSString *err = nil; BOOL perm = NO;
	SCShareableContent *c = gFetchContent(onScreenOnly, &err, &perm);
	if (!c) {
		if (perm) gPermissionNeeded(o);
		gFail(err, kExitFailure, nil);
	}
	return c;
}

static NSArray<NSDictionary *> *gCandidates(NSArray<GWindow *> *wins, NSArray<GDisplay *> *ds, NSUInteger limit) {
	NSMutableArray *r = [NSMutableArray array];
	NSArray *sorted = [wins sortedArrayUsingComparator:^NSComparisonResult(GWindow *x, GWindow *y) { return x.zRank < y.zRank ? NSOrderedAscending : x.zRank > y.zRank ? NSOrderedDescending : NSOrderedSame; }];
	for (GWindow *w in sorted) {
		if (r.count >= limit) break;
		if (w.layer != 0 || gDegenerate(w)) continue;
		[r addObject:gWindowDict(w, gDisplayForFrame(ds, w.frame))];
	}
	return r;
}

// ---- commands: list / appearance / permission ----------------------------------------

static NSDictionary *gDisplayDict(GDisplay *d, NSDictionary<NSNumber *, NSValue *> *ak, double primaryH) {
	NSMutableDictionary *m = [@{@"id": @(d.displayID), @"frame": gRectDict(d.frame), @"scale": @(d.scale), @"isMain": @(d.isMain),
	                             @"pixelSize": @{@"w": @(lround(d.frame.size.width * d.scale)), @"h": @(lround(d.frame.size.height * d.scale))}} mutableCopy];
	NSValue *v = ak[@(d.displayID)];
	if (v) { // cross-check the top-left frame against AppKit's bottom-left one
		CGRect akr = v.rectValue;
		CGRect conv = gTopLeftFromAppKit(akr, primaryH);
		m[@"appKitFrame"] = gRectDict(akr);
		m[@"frameMatchesAppKit"] = @(fabs(conv.origin.x - d.frame.origin.x) < 1 && fabs(conv.origin.y - d.frame.origin.y) < 1 && fabs(conv.size.width - d.frame.size.width) < 1 && fabs(conv.size.height - d.frame.size.height) < 1);
	}
	return m;
}

static void cmdList(GOpts *o) {
	if (!CGPreflightScreenCaptureAccess()) gPermissionNeeded(o);
	SCShareableContent *c = gContentOrFail(o, !o.all);
	NSArray<GDisplay *> *ds = gDisplaysFrom(c);
	NSArray<GWindow *> *wins = gMatchWindows(gWindowsFrom(c), o, NO);
	NSMutableArray *out = [NSMutableArray array];
	for (GWindow *w in wins) [out addObject:gWindowDict(w, gDisplayForFrame(ds, w.frame))];
	NSMutableArray *dd = [NSMutableArray array];
	NSDictionary *ak = gAppKitFrames();
	double ph = gPrimaryHeight();
	for (GDisplay *d in ds) [dd addObject:gDisplayDict(d, ak, ph)];
	uint64_t now = mach_absolute_time();
	gFinish(@{@"command": @"list", @"windows": out, @"windowCount": @(out.count), @"displays": dd,
	          @"coordinates": @"points, top-left origin, global display coordinates (main display at 0,0)",
	          @"machAbsTime": @(now), @"msSinceLaunch": @(gMsSinceLaunch(now)), @"appearance": gAppearanceSummary(),
	          @"tool": @{@"name": @"GnuiShot", @"version": TOOL_VERSION}}, kExitOK);
}

static void cmdAppearance(void) {
	NSMutableDictionary *d = [gAppearanceInfo() mutableCopy];
	uint64_t now = mach_absolute_time();
	d[@"command"] = @"appearance";
	d[@"machAbsTime"] = @(now);
	d[@"msSinceLaunch"] = @(gMsSinceLaunch(now));
	d[@"tool"] = @{@"name": @"GnuiShot", @"version": TOOL_VERSION};
	gFinish(d, kExitOK);
}

static void cmdPermission(GOpts *o) {
	BOOL granted = CGPreflightScreenCaptureAccess();
	BOOL requested = NO;
	if (!granted && o.request) { CGRequestScreenCaptureAccess(); requested = YES; }
	NSMutableDictionary *d = [@{@"command": @"permission", @"granted": @(granted), @"requested": @(requested),
	                             @"app": NSBundle.mainBundle.bundlePath ?: @"", @"bundleID": NSBundle.mainBundle.bundleIdentifier ?: @""} mutableCopy];
	if (granted) gFinish(d, kExitOK);
	d[@"error"] = @"screen recording permission needed";
	d[@"detail"] = @"Enable GnuiShot in System Settings > Privacy & Security > Screen & System Audio Recording, then re-run.";
	gFinish(d, kExitPermission);
}

// ---- capture ---------------------------------------------------------------------

// Returns a +1 CGImage or NULL (with *err).
static CGImageRef gCaptureImage(SCContentFilter *f, SCStreamConfiguration *c, NSString **err, BOOL *permission) {
	__block CGImageRef img = NULL;
	__block NSError *e = nil;
	dispatch_semaphore_t s = gSem();
	[SCScreenshotManager captureImageWithFilter:f configuration:c completionHandler:^(CGImageRef image, NSError *er) {
		if (image) img = CGImageRetain(image);
		e = er;
		dispatch_semaphore_signal(s);
	}];
	if (!gWaitSem(s, 10)) { *err = @"timed out waiting for the screenshot"; return NULL; }
	if (!img) {
		if (e.code == SCStreamErrorUserDeclined) *permission = YES;
		*err = [NSString stringWithFormat:@"capture failed: %@", e.localizedDescription ?: @"no image"];
	}
	return img;
}

static void gApplyCommonConfig(SCStreamConfiguration *cfg, GOpts *o) {
	cfg.showsCursor = o.cursor;
	cfg.pixelFormat = kCVPixelFormatType_32BGRA;
	cfg.scalesToFit = YES;
	if ([o.colorspace isEqualToString:@"srgb"]) cfg.colorSpaceName = kCGColorSpaceSRGB;
	else if ([o.colorspace isEqualToString:@"display-p3"]) cfg.colorSpaceName = kCGColorSpaceDisplayP3;
}

// One planned capture: filter + configuration + the geometry it represents.
@interface GShot : NSObject
@property (strong) SCContentFilter *filter;
@property (strong) SCStreamConfiguration *cfg;
@property CGRect regionGlobal;      // captured region, points, global top-left (window frame / padded crop / display rect)
@property CGRect contentRect;       // single-window: filter.contentRect
@property double scale, filterPointPixelScale;
@property size_t pw, ph;
@end
@implementation GShot
@end

static GShot *gPlanWindowShot(GOpts *o, GWindow *w, GDisplay *d) {
	GShot *s = [GShot new];
	double scale = o.scale ? o.scale : d.scale;
	s.scale = scale;
	SCStreamConfiguration *cfg = [SCStreamConfiguration new];
	gApplyCommonConfig(cfg, o);
	cfg.shouldBeOpaque = o.opaque;
	if (!o.crop && !o.includeApp) {
		SCContentFilter *f = [[SCContentFilter alloc] initWithDesktopIndependentWindow:w.sc];
		CGSize pts = gWindowCaptureSize(w.frame, f.contentRect, o.shadow);
		s.filter = f; s.contentRect = f.contentRect; s.filterPointPixelScale = f.pointPixelScale;
		s.regionGlobal = w.frame;
		cfg.ignoreShadowsSingleWindow = !o.shadow;
		cfg.ignoreGlobalClipSingleWindow = o.unclipped;
		size_t pw, ph;
		gPixelSize(pts, scale, &pw, &ph);
		s.pw = pw; s.ph = ph;
	} else {
		CGRect region = gIntersect(gInflate(w.frame, o.pad), d.frame);
		if (CGRectIsEmpty(region)) gFail(@"window region is outside its display", kExitFailure, nil);
		SCContentFilter *f;
		if (o.includeApp) {
			SCRunningApplication *app = w.sc.owningApplication;
			if (!app) gFail(@"window has no owning application; cannot use -include-app", kExitFailure, nil);
			f = [[SCContentFilter alloc] initWithDisplay:d.sc includingApplications:@[app] exceptingWindows:@[]];
			if (o.menubar) { if (@available(macOS 14.2, *)) f.includeMenuBar = YES; }
		} else {
			f = [[SCContentFilter alloc] initWithDisplay:d.sc excludingWindows:@[]];
		}
		s.filter = f; s.regionGlobal = region; s.filterPointPixelScale = f.pointPixelScale;
		cfg.sourceRect = gGlobalToLocal(region, d);
		size_t pw, ph;
		gPixelSize(region.size, scale, &pw, &ph);
		s.pw = pw; s.ph = ph;
	}
	cfg.width = s.pw; cfg.height = s.ph;
	s.cfg = cfg;
	return s;
}

static GShot *gPlanDisplayShot(GOpts *o, GDisplay *d, CGRect localRect) {
	GShot *s = [GShot new];
	double scale = o.scale ? o.scale : d.scale;
	s.scale = scale;
	SCStreamConfiguration *cfg = [SCStreamConfiguration new];
	gApplyCommonConfig(cfg, o);
	s.filter = [[SCContentFilter alloc] initWithDisplay:d.sc excludingWindows:@[]];
	s.filterPointPixelScale = s.filter.pointPixelScale;
	cfg.sourceRect = localRect;
	size_t pw, ph;
	gPixelSize(localRect.size, scale, &pw, &ph);
	s.pw = pw; s.ph = ph;
	cfg.width = s.pw; cfg.height = s.ph;
	s.regionGlobal = gLocalToGlobal(localRect, d);
	s.cfg = cfg;
	return s;
}

// Shared sequence runner. `refresh` (may be nil) re-plans before frame i > 0 and returns an error string on failure.
static void gRunSequence(GOpts *o, GShot *(^plan)(NSString **err), NSMutableDictionary *result, NSDictionary *(^frameExtras)(void)) {
	// (-wait-ms has already elapsed in the caller, before the window/display was resolved.)
	NSString *outPath = o.outPath;
	BOOL isDir = gIsDirPath(outPath);
	NSString *err = nil;
	GShot *shot = plan(&err);
	if (!shot) gFail(err ?: @"cannot plan capture", kExitNoMatch, nil);
	NSMutableArray<NSDictionary *> *frames = [NSMutableArray array];
	NSMutableArray<NSString *> *writeErrors = [NSMutableArray array];
	NSLock *wl = [NSLock new];
	dispatch_queue_t wq = dispatch_queue_create("gnuishot.write", DISPATCH_QUEUE_CONCURRENT);
	dispatch_group_t grp = dispatch_group_create();
	dispatch_semaphore_t inflight = dispatch_semaphore_create(6);
	uint64_t first = mach_absolute_time();
	uint64_t interval = gNsToTicks((double)o.intervalMs * 1e6);
	NSString *fatal = nil;
	int fatalCode = kExitFailure;
	for (int i = 0; i < o.repeat; i++) {
		uint64_t target = first + (uint64_t)i * interval;
		if (i > 0 && mach_absolute_time() < target) mach_wait_until(target);
		if (i > 0 && o.refresh) {
			NSString *perr = nil;
			GShot *ns = plan(&perr);
			if (!ns) { fatal = [NSString stringWithFormat:@"frame %d: %@", i, perr]; fatalCode = kExitNoMatch; break; }
			shot = ns;
		}
		uint64_t t0 = mach_absolute_time();
		NSString *cerr = nil; BOOL perm = NO;
		CGImageRef img = gCaptureImage(shot.filter, shot.cfg, &cerr, &perm);
		uint64_t t1 = mach_absolute_time();
		if (!img) { fatal = [NSString stringWithFormat:@"frame %d: %@", i, cerr]; fatalCode = perm ? kExitPermission : kExitFailure; break; }
		NSString *path = gFramePath(outPath, isDir, o.repeat, i);
		NSMutableDictionary *fd = [@{
			@"index": @(i), @"path": path,
			@"scheduledMs": @(gMsSinceLaunch(target)), @"lateMs": @(gTicksToNs(t0 > target ? t0 - target : 0) / 1e6),
			@"requestMachAbsTime": @(t0), @"requestMs": @(gMsSinceLaunch(t0)),
			@"completeMachAbsTime": @(t1), @"completeMs": @(gMsSinceLaunch(t1)), @"captureMs": @(gTicksToNs(t1 - t0) / 1e6),
			@"pixelSize": @{@"w": @(CGImageGetWidth(img)), @"h": @(CGImageGetHeight(img))},
			@"wallClock": [NSISO8601DateFormatter stringFromDate:NSDate.date timeZone:NSTimeZone.localTimeZone formatOptions:NSISO8601DateFormatWithInternetDateTime | NSISO8601DateFormatWithFractionalSeconds],
		} mutableCopy];
		if (CGImageGetWidth(img) != shot.pw || CGImageGetHeight(img) != shot.ph) fd[@"requestedPixelSize"] = @{@"w": @(shot.pw), @"h": @(shot.ph)};
		if (o.refresh) { fd[@"region"] = gRectDict(shot.regionGlobal); fd[@"scale"] = @(shot.scale); }
		NSDictionary *extra = frameExtras ? frameExtras() : nil;
		if (extra) [fd addEntriesFromDictionary:extra];
		[wl lock]; [frames addObject:fd]; [wl unlock];
		[gProgressLock lock]; [gProgress addObject:[fd copy]]; [gProgressLock unlock];
		dispatch_semaphore_wait(inflight, DISPATCH_TIME_FOREVER);
		dispatch_group_async(grp, wq, ^{
			NSString *werr = gWritePNG(img, path);
			BOOL uni = !werr && gImageUniform(img);
			CGImageRelease(img);
			[wl lock];
			if (werr) [writeErrors addObject:werr];
			if (uni) fd[@"uniform"] = @YES;
			[wl unlock];
			dispatch_semaphore_signal(inflight);
		});
	}
	dispatch_group_wait(grp, dispatch_time(DISPATCH_TIME_NOW, 20 * NSEC_PER_SEC));
	[wl lock];
	NSMutableArray *done = [NSMutableArray array];
	for (NSDictionary *f in frames) [done addObject:[f copy]];
	NSArray *werrs = [writeErrors copy];
	[wl unlock];
	NSMutableArray<NSString *> *warnings = [NSMutableArray array];
	for (NSDictionary *f in done) if ([f[@"uniform"] boolValue]) [warnings addObject:[NSString stringWithFormat:@"frame %@ is a single flat colour (blank capture? stale permission?)", f[@"index"]]];
	for (NSDictionary *f in done) if (f[@"requestedPixelSize"]) { [warnings addObject:@"returned pixel size differs from the requested size (see frames[].requestedPixelSize)"]; break; }
	NSDictionary *first_ = done.firstObject;
	result[@"frames"] = done;
	result[@"frameCount"] = @(done.count);
	result[@"scale"] = @(shot.scale);
	result[@"timestamps"] = @{@"unit": @"machAbsTime = mach_absolute_time ticks; ms are since GnuiShot launched",
	                          @"launchMachAbsTime": @(gLaunchTicks), @"timebase": @{@"numer": @(gTB.numer), @"denom": @(gTB.denom)}};
	if (first_) {
		result[@"out"] = first_[@"path"];
		result[@"pixelSize"] = first_[@"pixelSize"];
		result[@"captureMachAbsTime"] = first_[@"requestMachAbsTime"];
		result[@"captureMs"] = first_[@"requestMs"];
	}
	if (o.repeat > 1) { result[@"repeat"] = @(o.repeat); result[@"intervalMs"] = @(o.intervalMs); }
	result[@"regionPoints"] = gRectDict(shot.regionGlobal);
	result[@"filter"] = @{@"contentRect": gRectDict(shot.contentRect), @"pointPixelScale": @(shot.filterPointPixelScale)}; // diagnostics
	if (warnings.count) result[@"warnings"] = warnings;
	if (werrs.count) gFail([NSString stringWithFormat:@"cannot write image: %@", werrs.firstObject], kExitFailure, @{@"frames": done});
	if (fatal) {
		if (fatalCode == kExitPermission) { if (!o.noPrompt) CGRequestScreenCaptureAccess(); }
		gFail(fatalCode == kExitPermission ? @"screen recording permission needed" : fatal, fatalCode, @{@"frames": done, @"detail": fatal});
	}
}

static NSDictionary *gCaptureCommon(GOpts *o) {
	return @{@"command": o.command, @"colorspace": o.colorspace, @"cursor": @(o.cursor), @"waitMs": @(o.waitMs),
	         @"appearance": gAppearanceSummary(), @"tool": @{@"name": @"GnuiShot", @"version": TOOL_VERSION}};
}

static GWindow *gFindByID(NSArray<GWindow *> *wins, CGWindowID wid) {
	for (GWindow *w in wins) if (w.windowID == wid) return w;
	return nil;
}

static void cmdWindow(GOpts *o) {
	if (!CGPreflightScreenCaptureAccess()) gPermissionNeeded(o);
	if (o.waitMs > 0) usleep((useconds_t)o.waitMs * 1000); // wait first: the selection reflects the state we want to capture
	// -poll-ms: keep looking for the window (e.g. right after launching the app under test).
	uint64_t pollEnd = mach_absolute_time() + gNsToTicks((double)o.pollMs * 1e6);
	__block SCShareableContent *content;
	__block NSArray<GDisplay *> *ds;
	NSArray<GWindow *> *all, *m;
	for (;;) {
		content = gContentOrFail(o, !o.all);
		ds = gDisplaysFrom(content);
		all = gWindowsFrom(content);
		m = gMatchWindows(all, o, YES);
		if (m.count || mach_absolute_time() >= pollEnd) break;
		usleep(100 * 1000);
	}
	if (m.count == 0) {
		NSMutableDictionary *sel = [NSMutableDictionary dictionary];
		if (o.windowID) sel[@"id"] = o.windowID;
		if (o.pid) sel[@"pid"] = o.pid;
		if (o.app) sel[@"app"] = o.app;
		if (o.title) sel[@"title"] = o.title;
		sel[@"layer"] = o.layer ?: @"any";
		gFail(@"no matching window", kExitNoMatch, @{@"selector": sel, @"candidates": gCandidates(all, ds, 40),
		      @"hint": o.all ? @"" : @"only on-screen windows are considered; add -all for windows on other Spaces/minimized, or -layer any for non-zero layers"});
	}
	GWindow *chosen = m.firstObject;
	CGWindowID wid = chosen.windowID;
	NSMutableDictionary *result = [gCaptureCommon(o) mutableCopy];
	result[@"mode"] = o.crop ? @"crop" : o.includeApp ? @"include-app" : @"window";
	result[@"shadow"] = @(o.shadow);
	if (o.pad > 0) result[@"pad"] = @(o.pad);
	if (m.count > 1) {
		NSMutableArray *also = [NSMutableArray array];
		for (GWindow *w in [m subarrayWithRange:NSMakeRange(1, MIN(m.count - 1, 10))]) [also addObject:gWindowDict(w, gDisplayForFrame(ds, w.frame))];
		result[@"alsoMatched"] = also;
		result[@"matchCount"] = @(m.count);
	}
	__block GWindow *cur = chosen;
	__block GDisplay *curDisplay = gDisplayForFrame(ds, chosen.frame);
	__block BOOL firstPlan = YES;
	GShot *(^plan)(NSString **) = ^GShot *(NSString **err) {
		if (!firstPlan) { // -refresh: look the window up again
			NSString *e2 = nil; BOOL perm = NO;
			SCShareableContent *c2 = gFetchContent(!o.all, &e2, &perm);
			if (!c2) { *err = e2; return nil; }
			NSArray<GDisplay *> *ds2 = gDisplaysFrom(c2);
			GWindow *w2 = gFindByID(gWindowsFrom(c2), wid);
			if (!w2) { *err = [NSString stringWithFormat:@"window %u is gone", wid]; return nil; }
			cur = w2; curDisplay = gDisplayForFrame(ds2, w2.frame);
			content = c2; ds = ds2;
		}
		firstPlan = NO;
		if (!curDisplay) { *err = @"window is not on any display (off-screen); use -all to see where it is"; return nil; }
		return gPlanWindowShot(o, cur, curDisplay);
	};
	gRunSequence(o, plan, result, ^NSDictionary *{ return o.refresh ? @{@"window": gWindowDict(cur, curDisplay)} : nil; });
	// gRunSequence returns after filling `result` (it exits itself on failure).
	result[@"window"] = gWindowDict(cur, curDisplay);
	result[@"windowFrame"] = gRectDict(cur.frame);
	if (curDisplay) result[@"display"] = @{@"id": @(curDisplay.displayID), @"frame": gRectDict(curDisplay.frame), @"scale": @(curDisplay.scale)};
	gFinish(result, kExitOK);
}

static void cmdDisplay(GOpts *o) {
	if (!CGPreflightScreenCaptureAccess()) gPermissionNeeded(o);
	if (o.waitMs > 0) usleep((useconds_t)o.waitMs * 1000);
	SCShareableContent *content = gContentOrFail(o, YES);
	NSArray<GDisplay *> *ds = gDisplaysFrom(content);
	GDisplay *d = nil;
	if (o.displayArg && ![o.displayArg isEqualToString:@"main"]) {
		CGDirectDisplayID want = (CGDirectDisplayID)o.displayArg.longLongValue;
		for (GDisplay *x in ds) if (x.displayID == want) d = x;
		if (!d) {
			NSMutableArray *dd = [NSMutableArray array];
			NSDictionary *ak = gAppKitFrames(); double ph = gPrimaryHeight();
			for (GDisplay *x in ds) [dd addObject:gDisplayDict(x, ak, ph)];
			gFail([NSString stringWithFormat:@"no display with id %@", o.displayArg], kExitNoMatch, @{@"displays": dd});
		}
	} else if (o.global && o.hasRect && !o.displayArg) {
		d = gDisplayForFrame(ds, o.rect);
		if (!d) gFail(@"-rect (global) is not on any display", kExitUsage, nil);
	} else {
		for (GDisplay *x in ds) if (x.isMain) d = x;
		if (!d) d = ds.firstObject;
	}
	if (!d) gFail(@"no display", kExitFailure, nil);
	CGRect local = CGRectMake(0, 0, d.frame.size.width, d.frame.size.height);
	BOOL clippedRect = NO;
	if (o.hasRect) {
		CGRect want = o.global ? gGlobalToLocal(o.rect, d) : o.rect;
		local = gIntersect(want, CGRectMake(0, 0, d.frame.size.width, d.frame.size.height));
		if (CGRectIsEmpty(local)) gFail(@"-rect is outside the display", kExitUsage, @{@"display": gDisplayDict(d, @{}, 0)});
		clippedRect = !CGRectEqualToRect(local, want);
	}
	NSMutableDictionary *result = [gCaptureCommon(o) mutableCopy];
	result[@"display"] = @{@"id": @(d.displayID), @"frame": gRectDict(d.frame), @"scale": @(d.scale), @"isMain": @(d.isMain)};
	result[@"localRect"] = gRectDict(local);
	if (o.hasRect) { result[@"rectClipped"] = @(clippedRect); result[@"rectGlobal"] = @(o.global); }
	gRunSequence(o, ^GShot *(NSString **__unused err) { return gPlanDisplayShot(o, d, local); }, result, nil);
	gFinish(result, kExitOK);
}

// ---- dispatch ----------------------------------------------------------------------

static void gRunCommand(GOpts *o) {
	if ([o.command isEqualToString:@"list"]) cmdList(o);
	else if ([o.command isEqualToString:@"window"]) cmdWindow(o);
	else if ([o.command isEqualToString:@"display"]) cmdDisplay(o);
	else if ([o.command isEqualToString:@"appearance"]) cmdAppearance();
	else if ([o.command isEqualToString:@"permission"]) cmdPermission(o);
	else if ([o.command isEqualToString:@"hangtest"]) { for (;;) sleep(3600); } // diagnostic: proves the watchdog ends a hung run
	gFail(@"internal: unhandled command", kExitFailure, nil);
}

@interface GAppDelegate : NSObject <NSApplicationDelegate>
@property (strong) GOpts *opts;
@end
@implementation GAppDelegate
- (void)applicationDidFinishLaunching:(NSNotification *)n {
	GOpts *o = self.opts;
	dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{ gRunCommand(o); });
}
@end

// ============================================================================
// (5) main
// ============================================================================

// Best-effort -json lookup so that even argument errors can be reported through the file.
static NSString *gFindJSONArg(NSArray<NSString *> *args) {
	for (NSUInteger i = 0; i + 1 < args.count; i++) if ([args[i] isEqualToString:@"-json"]) return gAbsPath(args[i + 1]);
	return nil;
}

int main(int argc, const char **argv) {
	@autoreleasepool {
		gClockInit();
		gFinishLock = [NSLock new];
		gProgressLock = [NSLock new];
		gProgress = [NSMutableArray array];
		NSMutableArray<NSString *> *args = [NSMutableArray array];
		for (int i = 1; i < argc; i++) [args addObject:@(argv[i])];
		NSString *err = nil;
		GOpts *o = gParseArgs(args, &err);
		if (!o) {
			gJSONPath = gFindJSONArg(args);
			fprintf(stderr, "GnuiShot: %s\n%s\n", err.UTF8String, gUsage().UTF8String);
			gUsageFail(err);
		}
		gJSONPath = o.jsonPath;
		if (gJSONPath) gJSONPathC = strdup(gJSONPath.fileSystemRepresentation);
		gInstallCrashHandlers();
		if ([o.command isEqualToString:@"selftest"]) {
			NSDictionary *d = gRunSelfTest();
			gFinish(d, gFailures ? 1 : kExitOK);
		}
		// Watchdog: never let `open -W` hang. Reports whatever frames were captured so far.
		double limit = gTimeoutSeconds(o);
		dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(limit * NSEC_PER_SEC)), dispatch_get_global_queue(QOS_CLASS_UTILITY, 0), ^{
			[gProgressLock lock];
			NSArray *partial = [gProgress copy];
			[gProgressLock unlock];
			gFail([NSString stringWithFormat:@"timeout after %.1f s", limit], kExitTimeout, @{@"frames": partial});
		});
		NSApplication *app = [NSApplication sharedApplication];
		[app setActivationPolicy:NSApplicationActivationPolicyAccessory];
		GAppDelegate *del = [GAppDelegate new];
		del.opts = o;
		app.delegate = del;
		[app run];
	}
	return 0;
}
