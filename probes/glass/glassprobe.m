// GlassProbe: the feasibility probe for issue #25 (real glass and blur over
// our own scrolled content). Not framework code; see
// docs/research/glass-over-metal.md for what it measured.
//
// One window, one of two kinds:
//
//   -mode ref    AppKit's own: a unified NSToolbar over an NSScrollView whose
//                document is the content bitmap, so AppKit draws its scroll
//                edge (NSScrollPocket) over known pixels.
//   -mode metal  ours: a full-window CAMetalLayer view drawing the same
//                bitmap, then (-overlay) native views over it in a container
//                that refuses hits, then (-fg) a transparent foreground
//                CAMetalLayer for toolbar glyphs. -drawn blurs the band in
//                Metal instead.
//   -mode bench  offscreen: the drawn blur's GPU time at band sizes.
//
// It walks -phases (comma separated). A static phase (top, scrolled; with
// an active- or inactive- prefix) writes OUT/TAG-PHASE.ready with the window
// number and waits for OUT/TAG-PHASE.done (run.sh captures in between). anim
// scrolls at 120 Hz for -anim seconds and writes OUT/TAG-anim.json; idle
// holds a still frame for -idle seconds; flash alternates the content's
// colour; hit reports hit-testing; dump writes the window's layer tree.
#import <AppKit/AppKit.h>
#import <IOKit/IOKitLib.h>
#import <Metal/Metal.h>
#import <QuartzCore/QuartzCore.h>
#include <mach/mach_time.h>
#include <stdatomic.h>
#include <sys/file.h>
#include <sys/resource.h>
#include <sys/sysctl.h>
#include <unistd.h>

#pragma mark - Options

static NSString *gMode = @"metal", *gAppearance = @"light", *gOut = @"/tmp/glassprobe", *gTag = @"run";
static NSString *gOverlay = @"none", *gToolbar = @"none", *gSync = @"async", *gContent = @"table", *gBlur = @"gauss",
                *gBlurSrc = @"drawable", *gFGSize = @"band", *gTitle = @"";
static NSArray<NSString *> *gPhases;
static CGFloat gW = 900, gH = 560, gBand = 52, gScroll = 100, gMargin = 0;
static BOOL gFG = NO, gDrawn = NO, gHC = NO, gSplit = NO;
static double gAnimSec = 6, gIdleSec = 6, gSpeed = 600, gVevMaterial = 10;
static float gSigma = 6, gSat = 1, gTintA = 0.8245, gOff = 0.03, gKOffset = 1;
static int gKIter = 3;
static int gSepTop = -1, gSepUnder = -1; // NSTitlebarSeparatorStyle to set at the top and with content under the band (-1: leave)
static uint32_t gTint; // 0xRRGGBBAA; 0: the appearance's bar
static int gFlashFrames = 6;
static int gDrawables = 2; // the framework's (gpu/metal_darwin.go)
static double gLinkTarget; // the display link's targetTimestamp for the frame being drawn

static BOOL dark(void) { return [gAppearance isEqual:@"dark"]; }
static BOOL has(NSString *list, NSString *item) { return [[list componentsSeparatedByString:@","] containsObject:item]; }

#pragma mark - Content bitmap

// The content: an inset table drawn by AppKit into an sRGB bitmap at 2x, the
// same pixels in every mode. Rows 24 pt from y 0, a selection run of two
// rows, stripes, coloured icons and secondary text.
static CGImageRef gContentImage;
static NSData *gContentRGBA;
static size_t gContentPxW, gContentPxH;
static uint32_t gBG; // the window's content colour

static void drawRowsInto(CGContextRef ctx, CGFloat w, CGFloat h) {
	NSGraphicsContext *gc = [NSGraphicsContext graphicsContextWithCGContext:ctx flipped:YES];
	[NSGraphicsContext saveGraphicsState];
	NSGraphicsContext.currentContext = gc;
	BOOL d = dark();
	NSColor *bg = d ? [NSColor colorWithSRGBRed:0x1E / 255.0 green:0x1E / 255.0 blue:0x1E / 255.0 alpha:1] : NSColor.whiteColor;
	NSColor *stripe = d ? [NSColor colorWithSRGBRed:1 green:1 blue:1 alpha:0x0C / 255.0] : [NSColor colorWithSRGBRed:0xF4 / 255.0 green:0xF5 / 255.0 blue:0xF5 / 255.0 alpha:1];
	NSColor *sel = d ? [NSColor colorWithSRGBRed:0x00 / 255.0 green:0x59 / 255.0 blue:0xD1 / 255.0 alpha:1] : [NSColor colorWithSRGBRed:0x00 / 255.0 green:0x64 / 255.0 blue:0xE1 / 255.0 alpha:1];
	NSColor *ink = d ? [NSColor colorWithSRGBRed:0xDF / 255.0 green:0xDF / 255.0 blue:0xDF / 255.0 alpha:1] : [NSColor colorWithSRGBRed:0x1A / 255.0 green:0x1A / 255.0 blue:0x1A / 255.0 alpha:1];
	NSColor *ink2 = d ? [NSColor colorWithSRGBRed:0x9A / 255.0 green:0x9A / 255.0 blue:0x9A / 255.0 alpha:1] : [NSColor colorWithSRGBRed:0x80 / 255.0 green:0x80 / 255.0 blue:0x80 / 255.0 alpha:1];
	[bg setFill];
	NSRectFill(NSMakeRect(0, 0, w, h));
	NSArray *names = @[
		@"Annual Report.pdf", @"Budget 2026.numbers", @"Client Brief.pages", @"Design Review.key", @"Invoices", @"Meeting Notes.txt", @"Moodboard.png",
		@"Photos", @"Product Roadmap.pdf", @"Project Plan.pages", @"README.md", @"Receipts", @"Screenshot 2026-10-12.png", @"Team Offsite.key",
		@"Timesheet.numbers", @"Travel.pdf"
	];
	NSArray *kinds = @[ @"PDF document", @"Numbers spreadsheet", @"Pages document", @"Keynote presentation", @"Folder", @"Plain text", @"PNG image" ];
	NSArray *icons = @[ NSColor.systemRedColor, NSColor.systemGreenColor, NSColor.systemOrangeColor, NSColor.systemBlueColor, NSColor.systemTealColor ];
	NSFont *f = [NSFont systemFontOfSize:13];
	int rows = (int)(h / 24);
	for (int i = 0; i < rows; i++) {
		CGFloat y = i * 24;
		BOOL selected = (i % 16 == 5 || i % 16 == 6);
		NSRect band = NSMakeRect(10, y, w - 20, 24);
		if (selected) {
			[sel setFill];
			[[NSBezierPath bezierPathWithRoundedRect:band xRadius:8 yRadius:8] fill];
		} else if (i % 2 == 1) {
			[stripe setFill];
			[[NSBezierPath bezierPathWithRoundedRect:band xRadius:8 yRadius:8] fill];
		}
		NSColor *c1 = selected ? NSColor.whiteColor : ink, *c2 = selected ? NSColor.whiteColor : ink2;
		NSColor *ic = icons[i % icons.count];
		[ic setFill];
		[[NSBezierPath bezierPathWithRoundedRect:NSMakeRect(22, y + 4, 16, 16) xRadius:3 yRadius:3] fill];
		NSString *name = [NSString stringWithFormat:@"%@%@", names[i % names.count], i >= 16 ? [NSString stringWithFormat:@" %d", i / 16 + 1] : @""];
		[name drawAtPoint:NSMakePoint(46, y + 4) withAttributes:@{NSFontAttributeName : f, NSForegroundColorAttributeName : c1}];
		NSString *date = [NSString stringWithFormat:@"%d Oct 2026 at %02d:%02d", 1 + i % 28, 8 + i % 11, (i * 7) % 60];
		[date drawAtPoint:NSMakePoint(w * 0.45, y + 4) withAttributes:@{NSFontAttributeName : f, NSForegroundColorAttributeName : c2}];
		[kinds[i % kinds.count] drawAtPoint:NSMakePoint(w * 0.72, y + 4) withAttributes:@{NSFontAttributeName : f, NSForegroundColorAttributeName : c2}];
	}
	[NSGraphicsContext restoreGraphicsState];
}

static void makeContent(void) {
	CGFloat w = gW, h = 24 * 80;
	gContentPxW = (size_t)(w * 2);
	gContentPxH = (size_t)(h * 2);
	CGColorSpaceRef cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
	CGContextRef ctx = CGBitmapContextCreate(NULL, gContentPxW, gContentPxH, 8, gContentPxW * 4, cs, kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
	CGContextTranslateCTM(ctx, 0, gContentPxH);
	CGContextScaleCTM(ctx, 2, -2);
	drawRowsInto(ctx, w, h);
	gContentImage = CGBitmapContextCreateImage(ctx);
	gContentRGBA = [NSData dataWithBytes:CGBitmapContextGetData(ctx) length:gContentPxW * gContentPxH * 4];
	CGContextRelease(ctx);
	CGColorSpaceRelease(cs);
	gBG = dark() ? 0x1E1E1EFF : 0xFFFFFFFF;
	// the bitmap, for the analysis
	NSString *p = [gOut stringByAppendingPathComponent:[NSString stringWithFormat:@"content-%@.png", gAppearance]];
	if (![NSFileManager.defaultManager fileExistsAtPath:p]) {
		NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithCGImage:gContentImage];
		[[rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:p atomically:YES];
	}
}

#pragma mark - Results

static void writeJSON(id obj, NSString *name) {
	NSData *js = [NSJSONSerialization dataWithJSONObject:obj options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
	[js writeToFile:[gOut stringByAppendingPathComponent:name] atomically:YES];
}

static NSString *phaseFile(NSString *phase, NSString *ext) { return [gOut stringByAppendingPathComponent:[NSString stringWithFormat:@"%@-%@.%@", gTag, phase, ext]]; }

static NSDictionary *machine(void) {
	char model[64] = {0}, ver[64] = {0}, build[64] = {0};
	size_t n = sizeof model;
	sysctlbyname("hw.model", model, &n, NULL, 0);
	n = sizeof ver;
	sysctlbyname("kern.osproductversion", ver, &n, NULL, 0);
	n = sizeof build;
	sysctlbyname("kern.osversion", build, &n, NULL, 0);
	NSScreen *s = NSScreen.mainScreen;
	return @{
		@"model" : @(model),
		@"macOS" : @(ver),
		@"build" : @(build),
		@"display" : s ? @{@"name" : s.localizedName, @"points" : @[ @(s.frame.size.width), @(s.frame.size.height) ], @"scale" : @(s.backingScaleFactor), @"hz" : @(s.maximumFramesPerSecond)} : @{},
		@"reduceTransparency" : @(NSWorkspace.sharedWorkspace.accessibilityDisplayShouldReduceTransparency),
		@"increaseContrast" : @(NSWorkspace.sharedWorkspace.accessibilityDisplayShouldIncreaseContrast),
	};
}

#pragma mark - System load: WindowServer CPU, GPU utilisation

static pid_t gWSPid;

static double psCPUSeconds(pid_t pid) {
	NSTask *t = [NSTask new];
	t.launchPath = @"/bin/ps";
	t.arguments = @[ @"-o", @"time=", @"-p", [NSString stringWithFormat:@"%d", pid] ];
	NSPipe *p = [NSPipe pipe];
	t.standardOutput = p;
	[t launch];
	NSData *d = [p.fileHandleForReading readDataToEndOfFile];
	[t waitUntilExit];
	NSString *s = [[[NSString alloc] initWithData:d encoding:NSUTF8StringEncoding] stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceAndNewlineCharacterSet];
	double sec = 0;
	for (NSString *part in [s componentsSeparatedByString:@":"]) sec = sec * 60 + part.doubleValue;
	return sec;
}

static pid_t findWS(void) {
	NSTask *t = [NSTask new];
	t.launchPath = @"/usr/bin/pgrep";
	t.arguments = @[ @"-x", @"WindowServer" ];
	NSPipe *p = [NSPipe pipe];
	t.standardOutput = p;
	[t launch];
	NSData *d = [p.fileHandleForReading readDataToEndOfFile];
	[t waitUntilExit];
	return (pid_t)[[[NSString alloc] initWithData:d encoding:NSUTF8StringEncoding] intValue];
}

static double selfCPUSeconds(void) {
	struct rusage ru;
	getrusage(RUSAGE_SELF, &ru);
	return ru.ru_utime.tv_sec + ru.ru_utime.tv_usec / 1e6 + ru.ru_stime.tv_sec + ru.ru_stime.tv_usec / 1e6;
}

static io_registry_entry_t gAccel;

static double gpuUtil(void) {
	if (!gAccel) {
		gAccel = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("IOAccelerator"));
		if (!gAccel) return -1;
	}
	CFTypeRef v = IORegistryEntryCreateCFProperty(gAccel, CFSTR("PerformanceStatistics"), kCFAllocatorDefault, 0);
	if (!v) return -1;
	NSDictionary *d = (__bridge_transfer NSDictionary *)v;
	NSNumber *n = d[@"Device Utilization %"];
	return n ? n.doubleValue : -1;
}

// A sampler of GPU utilisation every 100 ms on a background queue.
static dispatch_source_t gUtilTimer;
static double gUtilSum;
static int gUtilN;

static void utilStart(void) {
	gUtilSum = 0;
	gUtilN = 0;
	gUtilTimer = dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER, 0, 0, dispatch_get_global_queue(QOS_CLASS_UTILITY, 0));
	dispatch_source_set_timer(gUtilTimer, dispatch_time(DISPATCH_TIME_NOW, 0), 100 * NSEC_PER_MSEC, 10 * NSEC_PER_MSEC);
	dispatch_source_set_event_handler(gUtilTimer, ^{
		double u = gpuUtil();
		if (u >= 0) {
			gUtilSum += u;
			gUtilN++;
		}
	});
	dispatch_resume(gUtilTimer);
}

static double utilStop(void) {
	dispatch_source_cancel(gUtilTimer);
	usleep(20000);
	return gUtilN ? gUtilSum / gUtilN : -1;
}

typedef struct {
	double t0, ws0, self0;
} Load;

static Load loadBegin(void) {
	utilStart();
	return (Load){CACurrentMediaTime(), psCPUSeconds(gWSPid), selfCPUSeconds()};
}

static NSDictionary *loadEnd(Load l) {
	double wall = CACurrentMediaTime() - l.t0;
	double ws = psCPUSeconds(gWSPid) - l.ws0, me = selfCPUSeconds() - l.self0;
	double util = utilStop();
	return @{@"wallS" : @(wall), @"windowServerCPUPct" : @(100 * ws / wall), @"selfCPUPct" : @(100 * me / wall), @"gpuDeviceUtilPct" : @(util)};
}

#pragma mark - The activation lock (internal/testlock): one process at a time owns the active app

static int gLockFD = -1;

static BOOL takeActivationLock(void) {
	if (gLockFD >= 0) return YES;
	char tmp[PATH_MAX];
	if (!confstr(_CS_DARWIN_USER_TEMP_DIR, tmp, sizeof tmp)) strcpy(tmp, "/tmp/");
	NSString *p = [@(tmp) stringByAppendingPathComponent:@"gnui-testlock-activation"];
	int fd = open(p.fileSystemRepresentation, O_CREAT | O_RDWR, 0666);
	if (fd < 0) return NO;
	for (int i = 0; i < 1800; i++) { // 180 s
		if (flock(fd, LOCK_EX | LOCK_NB) == 0) {
			gLockFD = fd;
			return YES;
		}
		usleep(100000);
	}
	close(fd);
	return NO;
}

#pragma mark - Views

static NSMutableArray<NSString *> *gHits;

// MetalView hosts the content's CAMetalLayer and records what reaches it.
@interface MetalView : NSView
@end
@implementation MetalView
- (CALayer *)makeBackingLayer { return [CAMetalLayer layer]; }
- (BOOL)wantsUpdateLayer { return YES; }
- (BOOL)isFlipped { return YES; }
- (BOOL)acceptsFirstMouse:(NSEvent *)e { return YES; }
- (BOOL)acceptsFirstResponder { return YES; }
- (BOOL)mouseDownCanMoveWindow { return NO; }
- (void)mouseDown:(NSEvent *)e { [gHits addObject:[NSString stringWithFormat:@"MetalView mouseDown at %@", NSStringFromPoint([self convertPoint:e.locationInWindow fromView:nil])]]; }
- (void)mouseUp:(NSEvent *)e { [gHits addObject:@"MetalView mouseUp"]; }
- (void)scrollWheel:(NSEvent *)e { [gHits addObject:@"MetalView scrollWheel"]; }
@end

// FGView is the transparent foreground layer: drawn glyphs, never hit.
@interface FGView : NSView
@end
@implementation FGView
- (CALayer *)makeBackingLayer { return [CAMetalLayer layer]; }
- (BOOL)wantsUpdateLayer { return YES; }
- (BOOL)isFlipped { return YES; }
- (NSView *)hitTest:(NSPoint)p { return nil; }
@end

// PassView holds the native overlays; it and everything in it refuse hits,
// so the hit test falls through to the MetalView below.
@interface PassView : NSView
@end
@implementation PassView
- (BOOL)isFlipped { return YES; }
- (NSView *)hitTest:(NSPoint)p { return nil; }
@end

@interface FlippedView : NSView
@end
@implementation FlippedView
- (BOOL)isFlipped { return YES; }
@end

// ImageDoc is the reference's document view: the content bitmap as its layer's contents.
@interface ImageDoc : NSView
@end
@implementation ImageDoc
- (BOOL)isFlipped { return YES; }
- (BOOL)wantsUpdateLayer { return YES; }
- (void)updateLayer {
	self.layer.contents = (__bridge id)gContentImage;
	self.layer.contentsScale = 2;
	self.layer.contentsGravity = kCAGravityResize;
}
@end

// Rows is reftable's data source: a plain NSTableView with a header.
@interface Rows : NSObject <NSTableViewDataSource, NSTableViewDelegate>
@end
@implementation Rows
- (NSInteger)numberOfRowsInTableView:(NSTableView *)tv { return 80; }
- (id)tableView:(NSTableView *)tv objectValueForTableColumn:(NSTableColumn *)c row:(NSInteger)r {
	return [NSString stringWithFormat:@"%@ %ld", c.identifier, (long)r];
}
@end

static Rows *gRows;

#pragma mark - Toolbar

@interface TB : NSObject <NSToolbarDelegate>
@property NSArray *ids;
@end
@implementation TB
- (NSArray *)toolbarDefaultItemIdentifiers:(NSToolbar *)tb { return self.ids; }
- (NSArray *)toolbarAllowedItemIdentifiers:(NSToolbar *)tb { return self.ids; }
- (NSToolbarItem *)toolbar:(NSToolbar *)tb itemForItemIdentifier:(NSString *)ident willBeInsertedIntoToolbar:(BOOL)f {
	NSDictionary *sym = @{@"side" : @"sidebar.left", @"share" : @"square.and.arrow.up", @"tag" : @"tag", @"search" : @"magnifyingglass"};
	if ([ident isEqual:@"nav"]) {
		NSToolbarItemGroup *g = [NSToolbarItemGroup groupWithItemIdentifier:ident
		                                                             images:@[ [NSImage imageWithSystemSymbolName:@"chevron.left" accessibilityDescription:@"Back"], [NSImage imageWithSystemSymbolName:@"chevron.right" accessibilityDescription:@"Forward"] ]
		                                                      selectionMode:NSToolbarItemGroupSelectionModeMomentary
		                                                             labels:@[ @"Back", @"Forward" ]
		                                                             target:nil
		                                                             action:@selector(self)];
		g.navigational = YES;
		return g;
	}
	if ([ident isEqual:@"group"]) {
		NSToolbarItemGroup *g = [[NSToolbarItemGroup alloc] initWithItemIdentifier:ident];
		NSToolbarItem *a = [[NSToolbarItem alloc] initWithItemIdentifier:@"share"], *b = [[NSToolbarItem alloc] initWithItemIdentifier:@"tag"];
		a.image = [NSImage imageWithSystemSymbolName:@"square.and.arrow.up" accessibilityDescription:@"Share"];
		b.image = [NSImage imageWithSystemSymbolName:@"tag" accessibilityDescription:@"Tags"];
		a.label = @"Share";
		b.label = @"Tags";
		a.action = b.action = @selector(self);
		g.subitems = @[ a, b ];
		return g;
	}
	NSToolbarItem *i = [[NSToolbarItem alloc] initWithItemIdentifier:ident];
	i.image = [NSImage imageWithSystemSymbolName:sym[ident] ?: @"circle" accessibilityDescription:ident];
	i.label = ident;
	i.action = @selector(self);
	return i;
}
@end

static TB *gTBD;

static void addToolbar(NSWindow *w, BOOL items) {
	gTBD = [TB new];
	gTBD.ids = items ? @[ @"side", @"nav", NSToolbarFlexibleSpaceItemIdentifier, @"group", @"search" ] : @[];
	NSToolbar *tb = [[NSToolbar alloc] initWithIdentifier:@"glassprobe"];
	tb.delegate = gTBD;
	tb.displayMode = NSToolbarDisplayModeIconOnly;
	tb.allowsUserCustomization = NO;
	w.toolbar = tb;
	w.toolbarStyle = NSWindowToolbarStyleUnified;
}

#pragma mark - Metal

static id<MTLDevice> gDev;
static id<MTLCommandQueue> gQueue;
static id<MTLRenderPipelineState> pSolid, pTex, pTexOpaque, pGauss, pKDown, pKUp, pComp;
static id<MTLSamplerState> gLinear, gNearest;
static id<MTLTexture> gContentTex;
// the drawn blur's textures, preallocated for the window's size
static id<MTLTexture> tL1, tL2, tL2b, tK[4], tKU[4];

typedef struct {
	float dst[4];   // target rect, pixels
	float src[4];   // source rect, texels
	float color[4]; // premultiplied
	float vp[2];    // target size
	float ts[2];    // source size
	float p[4];     // per-shader parameters
} U;

static NSString *kShaders = @"#include <metal_stdlib>\n"
                             "using namespace metal;\n"
                             "struct U { float4 dst; float4 src; float4 color; float2 vp; float2 ts; float4 p; };\n"
                             "struct V { float4 pos [[position]]; float2 uv; };\n"
                             "vertex V vq(uint vid [[vertex_id]], constant U &u [[buffer(0)]]) {\n"
                             "  float2 c = float2(float(vid & 1), float(vid >> 1));\n"
                             "  float2 p = mix(u.dst.xy, u.dst.zw, c);\n"
                             "  V o; o.pos = float4(p.x / u.vp.x * 2 - 1, 1 - p.y / u.vp.y * 2, 0, 1);\n"
                             "  o.uv = mix(u.src.xy, u.src.zw, c) / u.ts; return o; }\n"
                             "fragment float4 fsolid(V in [[stage_in]], constant U &u [[buffer(0)]]) { return u.color; }\n"
                             "fragment float4 ftex(V in [[stage_in]], texture2d<float> t [[texture(0)]], sampler s [[sampler(0)]]) { return t.sample(s, in.uv); }\n"
                             "fragment float4 fgauss(V in [[stage_in]], constant U &u [[buffer(0)]], texture2d<float> t [[texture(0)]], sampler s [[sampler(0)]]) {\n"
                             "  float2 d = u.p.xy / u.ts; float sg = u.p.z; int r = int(u.p.w);\n"
                             "  float4 acc = 0; float ws = 0;\n"
                             "  for (int i = -r; i <= r; i++) { float w = exp(-0.5 * float(i * i) / (sg * sg)); acc += w * t.sample(s, in.uv + d * float(i)); ws += w; }\n"
                             "  return acc / ws; }\n"
                             "fragment float4 fkdown(V in [[stage_in]], constant U &u [[buffer(0)]], texture2d<float> t [[texture(0)]], sampler s [[sampler(0)]]) {\n"
                             "  float2 h = u.p.x / u.ts;\n"
                             "  float4 c = t.sample(s, in.uv) * 4;\n"
                             "  c += t.sample(s, in.uv - h); c += t.sample(s, in.uv + h);\n"
                             "  c += t.sample(s, in.uv + float2(h.x, -h.y)); c += t.sample(s, in.uv - float2(h.x, -h.y));\n"
                             "  return c / 8; }\n"
                             "fragment float4 fkup(V in [[stage_in]], constant U &u [[buffer(0)]], texture2d<float> t [[texture(0)]], sampler s [[sampler(0)]]) {\n"
                             "  float2 h = u.p.x / u.ts;\n"
                             "  float4 c = t.sample(s, in.uv + float2(-h.x * 2, 0));\n"
                             "  c += t.sample(s, in.uv + float2(-h.x, h.y)) * 2; c += t.sample(s, in.uv + float2(0, h.y * 2));\n"
                             "  c += t.sample(s, in.uv + float2(h.x, h.y)) * 2; c += t.sample(s, in.uv + float2(h.x * 2, 0));\n"
                             "  c += t.sample(s, in.uv + float2(h.x, -h.y)) * 2; c += t.sample(s, in.uv + float2(0, -h.y * 2));\n"
                             "  c += t.sample(s, in.uv + float2(-h.x, -h.y)) * 2;\n"
                             "  return c / 12; }\n"
                             "fragment float4 fcomp(V in [[stage_in]], constant U &u [[buffer(0)]], texture2d<float> t [[texture(0)]], sampler s [[sampler(0)]]) {\n"
                             "  float4 c = t.sample(s, in.uv);\n"
                             "  float l = dot(c.rgb, float3(0.2126, 0.7152, 0.0722));\n"
                             "  float3 k = clamp(mix(float3(l), c.rgb, u.p.x), 0.0, 1.0);\n"
                             "  return float4(mix(k, u.color.rgb, u.p.y) + u.p.z, 1); }\n";

static id<MTLRenderPipelineState> mkpipe(id<MTLLibrary> lib, NSString *frag, BOOL blend) {
	MTLRenderPipelineDescriptor *d = [MTLRenderPipelineDescriptor new];
	d.vertexFunction = [lib newFunctionWithName:@"vq"];
	d.fragmentFunction = [lib newFunctionWithName:frag];
	d.colorAttachments[0].pixelFormat = MTLPixelFormatBGRA8Unorm;
	if (blend) {
		d.colorAttachments[0].blendingEnabled = YES;
		d.colorAttachments[0].sourceRGBBlendFactor = d.colorAttachments[0].sourceAlphaBlendFactor = MTLBlendFactorOne;
		d.colorAttachments[0].destinationRGBBlendFactor = d.colorAttachments[0].destinationAlphaBlendFactor = MTLBlendFactorOneMinusSourceAlpha;
	}
	NSError *err;
	id<MTLRenderPipelineState> p = [gDev newRenderPipelineStateWithDescriptor:d error:&err];
	if (!p) NSLog(@"pipeline %@: %@", frag, err);
	return p;
}

static void metalInit(void) {
	gDev = MTLCreateSystemDefaultDevice();
	gQueue = [gDev newCommandQueue];
	NSError *err;
	id<MTLLibrary> lib = [gDev newLibraryWithSource:kShaders options:nil error:&err];
	if (!lib) {
		NSLog(@"shaders: %@", err);
		exit(3);
	}
	pSolid = mkpipe(lib, @"fsolid", YES);
	pTex = mkpipe(lib, @"ftex", YES);
	pTexOpaque = mkpipe(lib, @"ftex", NO);
	pGauss = mkpipe(lib, @"fgauss", NO);
	pKDown = mkpipe(lib, @"fkdown", NO);
	pKUp = mkpipe(lib, @"fkup", NO);
	pComp = mkpipe(lib, @"fcomp", NO);
	MTLSamplerDescriptor *sd = [MTLSamplerDescriptor new];
	sd.minFilter = sd.magFilter = MTLSamplerMinMagFilterLinear;
	sd.sAddressMode = sd.tAddressMode = MTLSamplerAddressModeClampToEdge;
	gLinear = [gDev newSamplerStateWithDescriptor:sd];
	sd.minFilter = sd.magFilter = MTLSamplerMinMagFilterNearest;
	gNearest = [gDev newSamplerStateWithDescriptor:sd];
	MTLTextureDescriptor *td = [MTLTextureDescriptor texture2DDescriptorWithPixelFormat:MTLPixelFormatRGBA8Unorm width:gContentPxW height:gContentPxH mipmapped:NO];
	td.usage = MTLTextureUsageShaderRead;
	gContentTex = [gDev newTextureWithDescriptor:td];
	[gContentTex replaceRegion:MTLRegionMake2D(0, 0, gContentPxW, gContentPxH) mipmapLevel:0 withBytes:gContentRGBA.bytes bytesPerRow:gContentPxW * 4];
}

static id<MTLTexture> newRT(NSUInteger w, NSUInteger h) {
	MTLTextureDescriptor *td = [MTLTextureDescriptor texture2DDescriptorWithPixelFormat:MTLPixelFormatBGRA8Unorm width:MAX(w, 1) height:MAX(h, 1) mipmapped:NO];
	td.usage = MTLTextureUsageShaderRead | MTLTextureUsageRenderTarget;
	td.storageMode = MTLStorageModePrivate;
	return [gDev newTextureWithDescriptor:td];
}

// The blur region: the band plus a margin below it, in pixels.
static NSUInteger blurW(void) { return (NSUInteger)(gW * 2); }
static NSUInteger blurH(void) { return (NSUInteger)((gBand + gMargin) * 2); }

static void allocBlur(void) {
	NSUInteger w = blurW(), h = blurH();
	tL1 = newRT(w / 2, h / 2);
	tL2 = newRT(w / 4, h / 4);
	tL2b = newRT(w / 4, h / 4);
	for (int i = 0; i < 4; i++) {
		tK[i] = newRT(w >> (i + 1), h >> (i + 1));
		tKU[i] = newRT(w >> (i + 1), h >> (i + 1));
	}
}

static id<MTLRenderCommandEncoder> pass(id<MTLCommandBuffer> cb, id<MTLTexture> t, BOOL clear, uint32_t rgba) {
	MTLRenderPassDescriptor *rp = [MTLRenderPassDescriptor renderPassDescriptor];
	rp.colorAttachments[0].texture = t;
	rp.colorAttachments[0].loadAction = clear ? MTLLoadActionClear : MTLLoadActionLoad;
	rp.colorAttachments[0].storeAction = MTLStoreActionStore;
	float a = (rgba & 0xFF) / 255.0f;
	rp.colorAttachments[0].clearColor = MTLClearColorMake((rgba >> 24) / 255.0 * a, (rgba >> 16 & 0xFF) / 255.0 * a, (rgba >> 8 & 0xFF) / 255.0 * a, a);
	return [cb renderCommandEncoderWithDescriptor:rp];
}

// passDC begins a pass on an intermediate texture that the pass overwrites whole.
static id<MTLRenderCommandEncoder> passDC(id<MTLCommandBuffer> cb, id<MTLTexture> t) {
	MTLRenderPassDescriptor *rp = [MTLRenderPassDescriptor renderPassDescriptor];
	rp.colorAttachments[0].texture = t;
	rp.colorAttachments[0].loadAction = MTLLoadActionDontCare;
	rp.colorAttachments[0].storeAction = MTLStoreActionStore;
	return [cb renderCommandEncoderWithDescriptor:rp];
}

static void quad(id<MTLRenderCommandEncoder> e, id<MTLRenderPipelineState> p, U *u, id<MTLTexture> t, id<MTLSamplerState> s) {
	[e setRenderPipelineState:p];
	[e setVertexBytes:u length:sizeof *u atIndex:0];
	[e setFragmentBytes:u length:sizeof *u atIndex:0];
	if (t) {
		[e setFragmentTexture:t atIndex:0];
		[e setFragmentSamplerState:s atIndex:0];
	}
	[e drawPrimitives:MTLPrimitiveTypeTriangleStrip vertexStart:0 vertexCount:4];
}

static void setColor(U *u, uint32_t rgba) {
	float a = (rgba & 0xFF) / 255.0f;
	u->color[0] = (rgba >> 24) / 255.0f * a;
	u->color[1] = (rgba >> 16 & 0xFF) / 255.0f * a;
	u->color[2] = (rgba >> 8 & 0xFF) / 255.0f * a;
	u->color[3] = a;
}

static void rect(id<MTLRenderCommandEncoder> e, float vw, float vh, float x0, float y0, float x1, float y1, uint32_t rgba) {
	U u = {{x0, y0, x1, y1}, {0, 0, 1, 1}, {0}, {vw, vh}, {1, 1}, {0}};
	setColor(&u, rgba);
	quad(e, pSolid, &u, nil, nil);
}

// content draws the content scrolled by scrollPt into target (pixels w x h).
static int gFrame;
static uint32_t flashColor(int frame) { return (frame / gFlashFrames) % 2 ? 0xFF8000FF : 0x0060FFFF; }

static void drawContent(id<MTLRenderCommandEncoder> e, float w, float h, float scrollPt, float scale) {
	if ([gContent isEqual:@"flash"]) {
		rect(e, w, h, 0, 0, w, h, flashColor(gFrame));
		return;
	}
	float y0 = (float)((gBand - scrollPt) * 2) * scale;
	U u = {{0, y0, (float)gContentPxW * scale, y0 + (float)gContentPxH * scale}, {0, 0, (float)gContentPxW, (float)gContentPxH}, {0}, {w, h}, {(float)gContentPxW, (float)gContentPxH}, {0}};
	quad(e, scale == 1 ? pTexOpaque : pTexOpaque, &u, gContentTex, scale == 1 ? gNearest : gLinear);
}

// barTint is the colour replayed over the blur: AppKit's hard pocket replays
// the window's content background (#FFFFFF, #1E1E1E) at 82.45 %.
static uint32_t barTint(void) {
	if (gTint) return gTint;
	return gBG;
}

// The blurred band (encodeBlurPasses' output) and its size.
static id<MTLTexture> gBlurOut;
static float gBlurOutW, gBlurOutH;

// encodeBlurPasses blurs the band's content into gBlurOut: read from src (the
// frame drawn so far) or, with -blursrc rerender, the content drawn again at
// half size, as our renderer would re-encode the band's quads.
static void encodeBlurPasses(id<MTLCommandBuffer> cb, id<MTLTexture> src, float scrollPt) {
	float W = (float)blurW(), H = (float)blurH();
	BOOL rerender = [gBlurSrc isEqual:@"rerender"];
	if ([gBlur isEqual:@"kawase"]) {
		// dual Kawase: down to 1/2, 1/4, ... then back up to 1/2
		id<MTLTexture> prev = src;
		float pw = W, ph = H;
		for (int i = 0; i < gKIter; i++) {
			id<MTLTexture> t = tK[i];
			id<MTLRenderCommandEncoder> e = passDC(cb, t);
			float tw = (float)t.width, th = (float)t.height;
			if (i == 0 && rerender) {
				drawContent(e, tw, th, scrollPt, 0.5f);
			} else {
				U u = {{0, 0, tw, th}, {0, 0, pw, ph}, {0}, {tw, th}, {(float)prev.width, (float)prev.height}, {gKOffset, 0, 0, 0}};
				quad(e, pKDown, &u, prev, gLinear);
			}
			[e endEncoding];
			prev = t;
			pw = tw;
			ph = th;
		}
		for (int i = gKIter - 2; i >= 0; i--) {
			id<MTLTexture> t = tKU[i];
			id<MTLRenderCommandEncoder> e = passDC(cb, t);
			float tw = (float)t.width, th = (float)t.height;
			U u = {{0, 0, tw, th}, {0, 0, (float)prev.width, (float)prev.height}, {0}, {tw, th}, {(float)prev.width, (float)prev.height}, {gKOffset, 0, 0, 0}};
			quad(e, pKUp, &u, prev, gLinear);
			[e endEncoding];
			prev = t;
		}
		gBlurOut = prev;
	} else {
		// downsample to 1/4 (two bilinear halvings), then a separable Gaussian there
		id<MTLRenderCommandEncoder> e = passDC(cb, tL1);
		float w1 = (float)tL1.width, h1 = (float)tL1.height;
		if (rerender) {
			drawContent(e, w1, h1, scrollPt, 0.5f);
		} else {
			U u = {{0, 0, w1, h1}, {0, 0, W, H}, {0}, {w1, h1}, {(float)src.width, (float)src.height}, {0}};
			quad(e, pTexOpaque, &u, src, gLinear);
		}
		[e endEncoding];
		float w2 = (float)tL2.width, h2 = (float)tL2.height;
		e = passDC(cb, tL2);
		U u2 = {{0, 0, w2, h2}, {0, 0, w1, h1}, {0}, {w2, h2}, {w1, h1}, {0}};
		quad(e, pTexOpaque, &u2, tL1, gLinear);
		[e endEncoding];
		float sg = gSigma * 2 / 4; // points -> quarter-resolution pixels
		float r = ceilf(sg * 3);
		e = passDC(cb, tL2b);
		U uh = {{0, 0, w2, h2}, {0, 0, w2, h2}, {0}, {w2, h2}, {w2, h2}, {1, 0, sg, r}};
		quad(e, pGauss, &uh, tL2, gLinear);
		[e endEncoding];
		e = passDC(cb, tL2);
		U uv = {{0, 0, w2, h2}, {0, 0, w2, h2}, {0}, {w2, h2}, {w2, h2}, {0, 1, sg, r}};
		quad(e, pGauss, &uv, tL2b, gLinear);
		[e endEncoding];
		gBlurOut = tL2;
	}
	gBlurOutW = (float)gBlurOut.width;
	gBlurOutH = (float)gBlurOut.height;
}

// drawBandComposite draws the blurred band, tinted, and the hairline into an
// open pass on a dw x dh target.
static void drawBandComposite(id<MTLRenderCommandEncoder> e, float dw, float dh) {
	float W = (float)blurW(), H = (float)blurH(), bandPx = (float)(gBand * 2);
	U uc = {{0, 0, W, bandPx}, {0, 0, gBlurOutW, gBlurOutH * bandPx / H}, {0}, {dw, dh}, {gBlurOutW, gBlurOutH}, {gSat, gTintA, gOff, 0}};
	setColor(&uc, barTint());
	quad(e, pComp, &uc, gBlurOut, gLinear);
	rect(e, dw, dh, 0, bandPx, W, bandPx + 1, dark() ? 0xFFFFFF12 : 0x0000001A);
}

// encodeBlur is the drawn scroll edge read back from the frame (src): the
// blur passes, then a second pass over the whole of dst for the composite.
static void encodeBlur(id<MTLCommandBuffer> cb, id<MTLTexture> src, id<MTLTexture> dst, float scrollPt) {
	encodeBlurPasses(cb, src, scrollPt);
	id<MTLRenderCommandEncoder> e = pass(cb, dst, NO, 0);
	drawBandComposite(e, (float)dst.width, (float)dst.height);
	[e endEncoding];
}

#pragma mark - The window

static NSWindow *gWin;
static MetalView *gMetal;
static FGView *gFGView;
static PassView *gPass;
static CAMetalLayer *gLayer, *gFGLayer;
static NSScrollView *gPocket; // the transparent scroll view that makes AppKit draw its pocket
static NSScrollView *gRefScroll;
static NSView *gPlainDoc; // refplain's content
static float gScrollNow;

static NSRect topRect(CGFloat x, CGFloat y, CGFloat w, CGFloat h) { return NSMakeRect(x, y, w, h); } // in the flipped root

static void setupLayer(CAMetalLayer *l, BOOL opaque, BOOL fbOnly) {
	l.device = gDev;
	l.pixelFormat = MTLPixelFormatBGRA8Unorm;
	l.framebufferOnly = fbOnly;
	l.opaque = opaque;
	l.maximumDrawableCount = gDrawables;
	l.displaySyncEnabled = YES;
	l.contentsScale = 2;
	CGColorSpaceRef cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
	l.colorspace = cs;
	CGColorSpaceRelease(cs);
}

static void addOverlays(NSView *root) {
	gPass = [[PassView alloc] initWithFrame:root.bounds];
	gPass.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
	[root addSubview:gPass];
	if (has(gOverlay, @"vev")) {
		NSVisualEffectView *v = [[NSVisualEffectView alloc] initWithFrame:topRect(0, 0, gW, gBand)];
		v.material = (NSVisualEffectMaterial)gVevMaterial;
		v.blendingMode = NSVisualEffectBlendingModeWithinWindow;
		v.state = NSVisualEffectStateActive;
		v.autoresizingMask = NSViewWidthSizable;
		[gPass addSubview:v];
	}
	if (has(gOverlay, @"glassband")) {
		NSGlassEffectView *g = [[NSGlassEffectView alloc] initWithFrame:topRect(0, 0, gW, gBand)];
		g.style = NSGlassEffectViewStyleRegular;
		g.cornerRadius = 0;
		g.autoresizingMask = NSViewWidthSizable;
		[gPass addSubview:g];
	}
	if (has(gOverlay, @"pocket")) {
		// a transparent scroll view the window's size: AppKit gives it the
		// scroll edge of the titlebar; scrolling it shows the pocket
		gPocket = [[NSScrollView alloc] initWithFrame:root.bounds];
		gPocket.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
		gPocket.drawsBackground = NO;
		gPocket.hasVerticalScroller = NO;
		gPocket.hasHorizontalScroller = NO;
		if (has(gOverlay, @"pocketinset")) {
			gPocket.automaticallyAdjustsContentInsets = NO;
			gPocket.contentInsets = NSEdgeInsetsMake(gBand, 0, 0, 0);
		}
		FlippedView *doc = [[FlippedView alloc] initWithFrame:NSMakeRect(0, 0, gW, gH + 4000)];
		gPocket.documentView = doc;
		gPocket.contentView.drawsBackground = NO;
		[gPass addSubview:gPocket];
	}
	if (has(gOverlay, @"glass")) {
		// capsules as AppKit's toolbar has them: one container, spacing 4
		NSGlassEffectContainerView *gc = [[NSGlassEffectContainerView alloc] initWithFrame:root.bounds];
		gc.spacing = 4;
		gc.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
		FlippedView *inner = [[FlippedView alloc] initWithFrame:root.bounds];
		inner.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
		gc.contentView = inner;
		CGFloat xs[][2] = {{96, 36}, {140, 86}, {gW - 230, 84}, {gW - 142, 36}, {gW - 52, 36}};
		for (int i = 0; i < 5; i++) {
			NSGlassEffectView *g = [[NSGlassEffectView alloc] initWithFrame:NSMakeRect(xs[i][0], 8, xs[i][1], 36)];
			g.cornerRadius = 18;
			if (@available(macOS 27.0, *)) g.effectIsInteractive = YES;
			[inner addSubview:g];
		}
		[gPass addSubview:gc];
	}
}

// pocketTo shows AppKit's pocket (scrolled) or not: the overlay scroll
// view's, and the titlebar separator style asked for.
static void pocketTo(BOOL under) {
	int sep = under ? gSepUnder : gSepTop;
	if (sep >= 0 && gWin.titlebarSeparatorStyle != (NSTitlebarSeparatorStyle)sep) gWin.titlebarSeparatorStyle = (NSTitlebarSeparatorStyle)sep;
	if (!gPocket) return;
	NSClipView *c = gPocket.contentView;
	CGFloat top = -gPocket.contentInsets.top;
	[c scrollToPoint:NSMakePoint(0, top + (under ? 40 : 0))];
	[gPocket reflectScrolledClipView:c];
}

static void makeWindow(void) {
	NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable |
	                          NSWindowStyleMaskFullSizeContentView;
	gWin = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, gW, gH) styleMask:style backing:NSBackingStoreBuffered defer:NO];
	gWin.releasedWhenClosed = NO;
	gWin.title = gTitle.length ? gTitle : @"GlassProbe";
	if ([gToolbar isEqual:@"none"]) {
		gWin.titlebarAppearsTransparent = YES;
		gWin.titleVisibility = NSWindowTitleHidden;
	} else {
		addToolbar(gWin, [gToolbar isEqual:@"items"]);
		if (!gTitle.length) gWin.titleVisibility = NSWindowTitleHidden;
	}
	if ([gMode isEqual:@"refplain"]) {
		FlippedView *root = [[FlippedView alloc] initWithFrame:NSMakeRect(0, 0, gW, gH)];
		root.wantsLayer = YES;
		root.layer.masksToBounds = YES;
		gWin.contentView = root;
		gPlainDoc = [[ImageDoc alloc] initWithFrame:NSMakeRect(0, gBand, gW, gContentPxH / 2)];
		gPlainDoc.wantsLayer = YES;
		[root addSubview:gPlainDoc];
	} else if ([gMode isEqual:@"reftable"]) {
		gRefScroll = [[NSScrollView alloc] initWithFrame:NSMakeRect(0, 0, gW, gH)];
		NSTableView *tv = [NSTableView new];
		tv.style = NSTableViewStyleInset;
		tv.usesAlternatingRowBackgroundColors = YES;
		for (NSString *c in @[ @"Name", @"Date", @"Kind" ]) {
			NSTableColumn *col = [[NSTableColumn alloc] initWithIdentifier:c];
			col.title = c;
			col.width = 260;
			[tv addTableColumn:col];
		}
		gRows = [Rows new];
		tv.dataSource = gRows;
		tv.delegate = gRows;
		gRefScroll.documentView = tv;
		[tv reloadData];
		[tv selectRowIndexes:[NSIndexSet indexSetWithIndexesInRange:NSMakeRange(5, 2)] byExtendingSelection:NO];
		gWin.contentView = gRefScroll;
	} else if ([gMode isEqual:@"ref"]) {
		gRefScroll = [[NSScrollView alloc] initWithFrame:NSMakeRect(0, 0, gW, gH)];
		gRefScroll.hasVerticalScroller = NO;
		gRefScroll.hasHorizontalScroller = NO;
		ImageDoc *doc = [[ImageDoc alloc] initWithFrame:NSMakeRect(0, 0, gW, gContentPxH / 2)];
		doc.wantsLayer = YES;
		gRefScroll.documentView = doc;
		gWin.contentView = gRefScroll;
	} else {
		FlippedView *root = [[FlippedView alloc] initWithFrame:NSMakeRect(0, 0, gW, gH)];
		root.wantsLayer = YES;
		gWin.contentView = root;
		gMetal = [[MetalView alloc] initWithFrame:root.bounds];
		gMetal.wantsLayer = YES;
		gMetal.layerContentsRedrawPolicy = NSViewLayerContentsRedrawNever;
		gMetal.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
		[root addSubview:gMetal];
		gLayer = (CAMetalLayer *)gMetal.layer;
		setupLayer(gLayer, YES, !(gDrawn && [gBlurSrc isEqual:@"drawable"]));
		gLayer.drawableSize = CGSizeMake(gW * 2, gH * 2);
		addOverlays(root);
		if (gFG) {
			CGFloat fh = [gFGSize isEqual:@"full"] ? gH : gBand + 8;
			gFGView = [[FGView alloc] initWithFrame:NSMakeRect(0, 0, gW, fh)];
			gFGView.wantsLayer = YES;
			gFGView.layerContentsRedrawPolicy = NSViewLayerContentsRedrawNever;
			gFGView.autoresizingMask = NSViewWidthSizable;
			[root addSubview:gFGView];
			gFGLayer = (CAMetalLayer *)gFGView.layer;
			setupLayer(gFGLayer, NO, YES);
			gFGLayer.drawableSize = CGSizeMake(gW * 2, fh * 2);
		}
		if (gDrawn) allocBlur();
		[gWin makeFirstResponder:gMetal];
	}
	[gWin center];
}

#pragma mark - Frames and statistics

#define MAXF 4096
static double sTarget[MAXF], sCommit[MAXF], sPresented[MAXF], sFGPresented[MAXF], sGPU[MAXF], sGPUBlur[MAXF], sMain[MAXF], sDrawableWait[MAXF];
static int sN;

// encodeFG draws the foreground: glyph mocks on the capsules and the sync
// marker just below the band (the content draws its own marker below it).
static void encodeFG(id<MTLCommandBuffer> cb, id<MTLTexture> t, int frame) {
	id<MTLRenderCommandEncoder> e = pass(cb, t, YES, 0x00000000);
	float w = (float)t.width, h = (float)t.height;
	uint32_t glyph = dark() ? 0xE6E6E6FF : 0x4D4D4DFF;
	float xs[] = {96 + 18, 140 + 22, 140 + 64, gW - 230 + 22, gW - 230 + 62, gW - 142 + 18, gW - 52 + 18};
	for (int i = 0; i < 7; i++) rect(e, w, h, (xs[i] - 7) * 2, (26 - 7) * 2, (xs[i] + 7) * 2, (26 + 7) * 2, glyph);
	float mx = (float)(200 + (frame % 100) * 5) * 2;
	rect(e, w, h, mx, (gBand + 2) * 2, mx + 8, (gBand + 8) * 2, 0xFF0000FF);
	[e endEncoding];
}

static void encodeContentMarker(id<MTLRenderCommandEncoder> e, float w, float h, int frame) {
	float mx = (float)(200 + (frame % 100) * 5) * 2;
	rect(e, w, h, mx, (gBand + 8) * 2, mx + 8, (gBand + 14) * 2, 0x00A000FF);
}

static void drawFrame(float scrollPt, BOOL record) {
	@autoreleasepool {
		double t0 = CACurrentMediaTime();
		int fi = gFrame;
		id<CAMetalDrawable> d = [gLayer nextDrawable];
		double tw = CACurrentMediaTime() - t0;
		if (!d) return;
		id<CAMetalDrawable> fd = nil;
		if (gFGLayer) fd = [gFGLayer nextDrawable];
		BOOL tx = [gSync isEqual:@"tx"], onecb = [gSync isEqual:@"onecb"];
		id<MTLCommandBuffer> cb = [gQueue commandBuffer];
		BOOL inPass = gDrawn && [gBlurSrc isEqual:@"rerender"];
		if (inPass) encodeBlurPasses(cb, nil, scrollPt); // the band first; composited in the main pass
		id<MTLRenderCommandEncoder> e = pass(cb, d.texture, YES, gBG);
		float w = (float)d.texture.width, h = (float)d.texture.height;
		drawContent(e, w, h, scrollPt, 1);
		if (inPass) drawBandComposite(e, w, h);
		if (gFGLayer) encodeContentMarker(e, w, h, fi);
		[e endEncoding];
		id<MTLCommandBuffer> cbb = cb;
		if (gDrawn && !inPass) {
			if (gSplit) {
				if (record && fi < MAXF) {
					int k = fi;
					[cb addCompletedHandler:^(id<MTLCommandBuffer> b) {
						sGPU[k] = b.GPUEndTime - b.GPUStartTime;
					}];
				}
				[cb commit];
				cbb = [gQueue commandBuffer];
			}
			encodeBlur(cbb, d.texture, d.texture, scrollPt);
		}
		id<MTLCommandBuffer> fcb = nil;
		if (fd) {
			fcb = onecb ? cbb : [gQueue commandBuffer];
			encodeFG(fcb, fd.texture, fi);
		}
		if (record && fi < MAXF) {
			int k = fi;
			[d addPresentedHandler:^(id<MTLDrawable> dd) {
				sPresented[k] = dd.presentedTime;
			}];
			if (fd)
				[fd addPresentedHandler:^(id<MTLDrawable> dd) {
					sFGPresented[k] = dd.presentedTime;
				}];
			if (gSplit && gDrawn) {
				[cbb addCompletedHandler:^(id<MTLCommandBuffer> b) {
					sGPUBlur[k] = b.GPUEndTime - b.GPUStartTime;
				}];
			} else {
				[cbb addCompletedHandler:^(id<MTLCommandBuffer> b) {
					sGPU[k] = b.GPUEndTime - b.GPUStartTime;
				}];
			}
		}
		if (tx) {
			gLayer.presentsWithTransaction = YES;
			if (gFGLayer) gFGLayer.presentsWithTransaction = YES;
			[cbb commit];
			if (fcb && fcb != cbb) [fcb commit];
			[cbb waitUntilScheduled];
			if (fcb && fcb != cbb) [fcb waitUntilScheduled];
			[CATransaction begin];
			[CATransaction setDisableActions:YES];
			pocketTo(scrollPt > 0.25);
			[d present];
			if (fd) [fd present];
			[CATransaction commit];
			[CATransaction flush];
		} else {
			[cbb presentDrawable:d];
			if (fd) [fcb presentDrawable:fd];
			[cbb commit];
			if (fcb && fcb != cbb) [fcb commit];
			pocketTo(scrollPt > 0.25);
		}
		if (record && fi < MAXF) {
			sCommit[fi] = CACurrentMediaTime();
			sTarget[fi] = gLinkTarget;
			sMain[fi] = sCommit[fi] - t0;
			sDrawableWait[fi] = tw;
			sN = fi + 1;
		}
		gFrame++;
	}
}

static int cmpd(const void *a, const void *b) {
	double x = *(const double *)a, y = *(const double *)b;
	return x < y ? -1 : x > y;
}

static NSDictionary *dist(double *v, int n, double scale) {
	if (n <= 0) return @{};
	double *c = malloc(sizeof(double) * n);
	int m = 0;
	for (int i = 0; i < n; i++)
		if (v[i] > 0) c[m++] = v[i] * scale;
	if (!m) {
		free(c);
		return @{@"n" : @0};
	}
	qsort(c, m, sizeof(double), cmpd);
	double sum = 0;
	for (int i = 0; i < m; i++) sum += c[i];
	NSDictionary *r = @{@"n" : @(m), @"p50" : @(c[m / 2]), @"p95" : @(c[(int)(m * 0.95)]), @"p99" : @(c[(int)(m * 0.99)]), @"max" : @(c[m - 1]), @"mean" : @(sum / m)};
	free(c);
	return r;
}

#pragma mark - Phases

static int gPhase;
static void runPhase(void);
static void after(double s, void (^b)(void)) { dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(s * NSEC_PER_SEC)), dispatch_get_main_queue(), b); }

static void nextPhase(void) {
	gPhase++;
	runPhase();
}

static void waitDone(NSString *phase, NSDate *start, double limit) {
	if ([NSFileManager.defaultManager fileExistsAtPath:phaseFile(phase, @"done")] || -[start timeIntervalSinceNow] > limit) {
		nextPhase();
		return;
	}
	after(0.05, ^{
		waitDone(phase, start, limit);
	});
}

static void ready(NSString *phase, NSDictionary *extra) {
	NSMutableDictionary *d = [@{@"windowNumber" : @(gWin.windowNumber), @"active" : @(NSApp.isActive), @"key" : @(gWin.isKeyWindow), @"scroll" : @(gScrollNow), @"lock" : @(gLockFD >= 0)} mutableCopy];
	if (extra) [d addEntriesFromDictionary:extra];
	NSData *js = [NSJSONSerialization dataWithJSONObject:d options:0 error:nil];
	[js writeToFile:phaseFile(phase, @"ready") atomically:YES];
}

static void setScroll(float s) {
	gScrollNow = s;
	if (gRefScroll) {
		NSClipView *c = gRefScroll.contentView;
		[c scrollToPoint:NSMakePoint(0, -gRefScroll.contentInsets.top + s)];
		[gRefScroll reflectScrolledClipView:c];
		pocketTo(s > 0.25);
	} else if (gPlainDoc) {
		[gPlainDoc setFrameOrigin:NSMakePoint(0, gBand - s)];
		pocketTo(s > 0.25);
	} else {
		drawFrame(s, NO);
	}
}

// Animation: a triangle wave from -20 to 300 pt at gSpeed pt/s, snapped to
// device pixels, one frame per display-link tick.
@interface Anim : NSObject
@property CADisplayLink *link;
@property double start;
@property (copy) void (^done)(void);
@end
@implementation Anim
- (void)tick:(CADisplayLink *)l {
	double t = CACurrentMediaTime() - self.start;
	if (t > gAnimSec || gFrame >= MAXF - 2) {
		[l invalidate];
		self.link = nil;
		self.done();
		return;
	}
	double span = 320, p = fmod(t * gSpeed, 2 * span);
	double s = (p < span ? p : 2 * span - p) - 20;
	s = round(s * 2) / 2;
	gScrollNow = (float)s;
	gLinkTarget = l.targetTimestamp;
	drawFrame((float)s, YES);
}
@end

static Anim *gAnim;

static void runAnim(NSString *phase) {
	memset(sCommit, 0, sizeof sCommit);
	memset(sPresented, 0, sizeof sPresented);
	memset(sFGPresented, 0, sizeof sFGPresented);
	memset(sGPU, 0, sizeof sGPU);
	memset(sGPUBlur, 0, sizeof sGPUBlur);
	sN = 0;
	gFrame = 0;
	gAnim = [Anim new];
	ready(phase, @{@"anim" : @YES});
	Load ld = loadBegin();
	gAnim.start = CACurrentMediaTime();
	gAnim.link = [gMetal displayLinkWithTarget:gAnim selector:@selector(tick:)];
	gAnim.link.preferredFrameRateRange = CAFrameRateRangeMake(120, 120, 120);
	[gAnim.link addToRunLoop:NSRunLoop.mainRunLoop forMode:NSRunLoopCommonModes];
	__block NSString *ph = phase;
	gAnim.done = ^{
		NSDictionary *load = loadEnd(ld);
		after(0.3, ^{ // the last presented handlers
			int n = sN, presented = 0, drops = 0, fgMismatch = 0, fgBoth = 0;
			double *iv = calloc(n, sizeof(double)), *lat = calloc(n, sizeof(double)), *late = calloc(n, sizeof(double));
			double prev = 0;
			for (int i = 0; i < n; i++) {
				if (sPresented[i] > 0) {
					presented++;
					lat[i] = sPresented[i] - sCommit[i];
					if (sTarget[i] > 0) late[i] = sPresented[i] - sTarget[i] + 1e-6; // after the frame's target vsync
					if (prev > 0) {
						iv[i] = sPresented[i] - prev;
						if (iv[i] > 1.5 / 120) drops++;
					}
					prev = sPresented[i];
				}
				if (sPresented[i] > 0 && sFGPresented[i] > 0) {
					fgBoth++;
					if (fabs(sPresented[i] - sFGPresented[i]) > 0.002) fgMismatch++;
				}
			}
			NSMutableDictionary *r = [@{
				@"tag" : gTag,
				@"phase" : ph,
				@"overlay" : gOverlay,
				@"toolbar" : gToolbar,
				@"sync" : gSync,
				@"fg" : @(gFG),
				@"fgSize" : gFGSize,
				@"drawn" : @(gDrawn),
				@"blur" : gBlur,
				@"blurSrc" : gBlurSrc,
				@"appearance" : gAppearance,
				@"frames" : @(n),
				@"presented" : @(presented),
				@"intervalsOver1.5Frames" : @(drops),
				@"intervalMs" : dist(iv, n, 1000),
				@"latencyMs" : dist(lat, n, 1000),
				@"presentedAfterTargetMs" : dist(late, n, 1000),
				@"drawables" : @(gDrawables),
				@"gpuMs" : dist(sGPU, n, 1000),
				@"mainThreadMs" : dist(sMain, n, 1000),
				@"nextDrawableMs" : dist(sDrawableWait, n, 1000),
				@"load" : load,
				@"active" : @(NSApp.isActive),
				@"machine" : machine(),
			} mutableCopy];
			if (gSplit && gDrawn) r[@"gpuBlurMs"] = dist(sGPUBlur, n, 1000);
			if (gFG) r[@"fgPresentedApart"] = @{@"both" : @(fgBoth), @"apart" : @(fgMismatch)};
			free(iv);
			free(lat);
			free(late);
			writeJSON(r, [NSString stringWithFormat:@"%@-%@.json", gTag, ph]);
			waitDone(ph, [NSDate date], 30);
		});
	};
}

static void runIdle(NSString *phase) {
	setScroll((float)gScroll);
	after(1.0, ^{
		Load ld = loadBegin();
		int f0 = gFrame;
		after(gIdleSec, ^{
			NSDictionary *load = loadEnd(ld);
			writeJSON(@{@"tag" : gTag, @"phase" : phase, @"overlay" : gOverlay, @"presents" : @(gFrame - f0), @"load" : load, @"active" : @(NSApp.isActive), @"machine" : machine()},
			          [NSString stringWithFormat:@"%@-%@.json", gTag, phase]);
			nextPhase();
		});
	});
}

// flash: the content alternates colour every gFlashFrames frames at 120 Hz
// while run.sh captures a sequence: the band follows it or lags.
static void runFlash(NSString *phase) {
	gContent = @"flash";
	gAnimSec = 8;
	runAnim(phase);
}

static NSString *viewName(NSView *v) { return v ? NSStringFromClass(v.class) : @"(nil)"; }

static void runHit(NSString *phase) {
	setScroll((float)gScroll);
	gHits = [NSMutableArray array];
	NSView *frameView = gWin.contentView.superview;
	CGFloat H = gWin.frame.size.height;
	NSMutableDictionary *r = [NSMutableDictionary dictionary];
	NSDictionary *pts = @{
		@"band-empty" : [NSValue valueWithPoint:NSMakePoint(gW / 2, 26)],
		@"band-capsule" : [NSValue valueWithPoint:NSMakePoint(114, 26)],
		@"band-right-capsule" : [NSValue valueWithPoint:NSMakePoint(gW - 34, 26)],
		@"content" : [NSValue valueWithPoint:NSMakePoint(gW / 2, 300)],
	};
	for (NSString *k in pts) {
		NSPoint p = [pts[k] pointValue];
		NSPoint wp = NSMakePoint(p.x, H - p.y); // window coordinates, y up
		NSView *hit = [frameView hitTest:wp];
		NSMutableArray *chain = [NSMutableArray array];
		for (NSView *v = hit; v; v = v.superview) [chain addObject:viewName(v)];
		NSEvent *down = [NSEvent mouseEventWithType:NSEventTypeLeftMouseDown location:wp modifierFlags:0 timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:gWin.windowNumber context:nil eventNumber:0 clickCount:1 pressure:1];
		NSEvent *up = [NSEvent mouseEventWithType:NSEventTypeLeftMouseUp location:wp modifierFlags:0 timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:gWin.windowNumber context:nil eventNumber:0 clickCount:1 pressure:0];
		[gHits removeAllObjects];
		NSRect before = gWin.frame;
		[gWin sendEvent:down];
		[gWin sendEvent:up];
		r[k] = @{@"hitTest" : chain, @"delivered" : [gHits copy], @"windowMoved" : @(!NSEqualRects(before, gWin.frame))};
	}
	writeJSON(@{@"tag" : gTag, @"overlay" : gOverlay, @"toolbar" : gToolbar, @"points" : r}, [NSString stringWithFormat:@"%@-%@.json", gTag, phase]);
	nextPhase();
}

#pragma mark - Layer dump

static NSArray *rgbaOf(CGColorRef c) {
	if (!c) return nil;
	CGColorSpaceRef cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
	CGColorRef s = CGColorCreateCopyByMatchingToColorSpace(cs, kCGRenderingIntentDefault, c, NULL);
	CGColorSpaceRelease(cs);
	if (!s) return nil;
	const CGFloat *k = CGColorGetComponents(s);
	size_t n = CGColorGetNumberOfComponents(s);
	NSMutableArray *a = [NSMutableArray array];
	for (size_t i = 0; i < n; i++) [a addObject:@(round(k[i] * 1000) / 1000)];
	CGColorRelease(s);
	return a;
}

static id filterDesc(id f) {
	if (![f isKindOfClass:NSObject.class]) return [f description];
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	d[@"desc"] = [f description];
	for (NSString *k in @[ @"name", @"type", @"inputRadius", @"inputAmount", @"inputNormalizeEdges", @"inputQuality", @"inputColor", @"inputBias", @"inputHardEdges", @"inputDither", @"inputReversed", @"inputIntermediateBitDepth" ]) {
		@try {
			id v = [f valueForKey:k];
			if (v) d[k] = [v isKindOfClass:NSNumber.class] || [v isKindOfClass:NSString.class] ? v : [v description];
		} @catch (NSException *e) {
		}
	}
	@try {
		id m = [f valueForKey:@"inputColorMatrix"];
		if ([m isKindOfClass:NSValue.class]) {
			float k[20] = {0};
			[(NSValue *)m getValue:k size:sizeof k];
			NSMutableArray *a = [NSMutableArray array];
			for (int i = 0; i < 20; i++) [a addObject:@(round(k[i] * 10000) / 10000)];
			d[@"inputColorMatrix"] = a;
		}
	} @catch (NSException *e) {
	}
	for (NSString *k in @[ @"inputMaskImage", @"inputSourceSublayerName", @"inputEdgeMode", @"inputScale" ]) {
		@try {
			id v = [f valueForKey:k];
			if (v) {
				if (CFGetTypeID((__bridge CFTypeRef)v) == CGImageGetTypeID()) {
					CGImageRef im = (__bridge CGImageRef)v;
					static int nmask;
					NSString *p = [gOut stringByAppendingPathComponent:[NSString stringWithFormat:@"%@-mask%d.png", gTag, nmask++]];
					NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithCGImage:im];
					[[rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:p atomically:YES];
					d[k] = [NSString stringWithFormat:@"CGImage %zux%zu -> %@", CGImageGetWidth(im), CGImageGetHeight(im), p.lastPathComponent];
				} else
					d[k] = [v description];
			}
		} @catch (NSException *e) {
		}
	}
	return d;
}

static NSDictionary *dumpLayer(CALayer *l, CALayer *root, int depth) {
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	d[@"class"] = NSStringFromClass(l.class);
	if (l.name) d[@"name"] = l.name;
	if (l.delegate) d[@"delegate"] = NSStringFromClass([(id)l.delegate class]);
	NSRect f = [l convertRect:l.bounds toLayer:root];
	d[@"frame"] = @[ @(f.origin.x), @(f.origin.y), @(f.size.width), @(f.size.height) ];
	if (l.opacity != 1) d[@"opacity"] = @(l.opacity);
	if (l.hidden) d[@"hidden"] = @YES;
	if (l.backgroundColor) d[@"bg"] = rgbaOf(l.backgroundColor) ?: @"?";
	if (l.cornerRadius) d[@"cornerRadius"] = @(l.cornerRadius);
	if (l.contents) d[@"contents"] = NSStringFromClass([l.contents class]);
	if (l.compositingFilter) d[@"compositingFilter"] = filterDesc(l.compositingFilter);
	NSMutableArray *fs = [NSMutableArray array];
	for (id x in l.filters) [fs addObject:filterDesc(x)];
	if (fs.count) d[@"filters"] = fs;
	NSMutableArray *bfs = [NSMutableArray array];
	for (id x in l.backgroundFilters) [bfs addObject:filterDesc(x)];
	if (bfs.count) d[@"backgroundFilters"] = bfs;
	if ([d[@"class"] containsString:@"Backdrop"]) {
		for (NSString *k in @[ @"scale", @"groupName", @"enabled", @"marginWidth", @"windowServerAware", @"captureOnly", @"reducesCaptureBitDepth", @"statisticsType", @"backdropRect", @"zoom", @"allowsInPlaceFiltering" ]) {
			@try {
				id v = [l valueForKey:k];
				if (v) d[k] = [v isKindOfClass:NSNumber.class] || [v isKindOfClass:NSString.class] ? v : [v description];
			} @catch (NSException *e) {
			}
		}
	}
	if (depth < 40 && l.sublayers.count) {
		NSMutableArray *subs = [NSMutableArray array];
		for (CALayer *s in l.sublayers) [subs addObject:dumpLayer(s, root, depth + 1)];
		d[@"sublayers"] = subs;
	}
	return d;
}

static NSDictionary *dumpView(NSView *v, NSView *root, int depth) {
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	d[@"class"] = NSStringFromClass(v.class);
	NSRect f = [v convertRect:v.bounds toView:root];
	d[@"frame"] = @[ @(f.origin.x), @(f.origin.y), @(f.size.width), @(f.size.height) ];
	if (v.hidden) d[@"hidden"] = @YES;
	if (v.alphaValue != 1) d[@"alpha"] = @(v.alphaValue);
	if ([v isKindOfClass:NSVisualEffectView.class]) {
		NSVisualEffectView *e = (NSVisualEffectView *)v;
		d[@"material"] = @(e.material);
		d[@"blending"] = @(e.blendingMode);
		d[@"state"] = @(e.state);
	}
	if (depth < 40 && v.subviews.count) {
		NSMutableArray *subs = [NSMutableArray array];
		for (NSView *s in v.subviews) [subs addObject:dumpView(s, root, depth + 1)];
		d[@"subviews"] = subs;
	}
	return d;
}

static void runDump(NSString *phase) {
	setScroll((float)gScroll);
	after(0.8, ^{
		NSView *frameView = gWin.contentView.superview;
		writeJSON(@{@"views" : dumpView(frameView, frameView, 0), @"layers" : dumpLayer(frameView.layer, frameView.layer, 0)},
		          [NSString stringWithFormat:@"%@-%@.json", gTag, phase]);
		nextPhase();
	});
}

#pragma mark - Activation

static void becomeActive(void (^then)(void)) {
	if (NSApp.isActive && gWin.isKeyWindow) {
		then();
		return;
	}
	if (!takeActivationLock()) {
		NSLog(@"glassprobe: the activation lock was busy for 180 s; staying inactive");
		then();
		return;
	}
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
	[NSApp activateIgnoringOtherApps:YES];
#pragma clang diagnostic pop
	[gWin makeKeyAndOrderFront:nil];
	__block int tries = 0;
	__block void (^wait)(void);
	wait = ^{
		if (NSApp.isActive && !gWin.isKeyWindow) [gWin makeKeyAndOrderFront:nil];
		if ((NSApp.isActive && gWin.isKeyWindow) || ++tries > 200) {
			[NSFileManager.defaultManager removeItemAtPath:[gOut stringByAppendingPathComponent:[gTag stringByAppendingString:@".need-activate"]] error:nil];
			then();
			return;
		}
		// macOS 14+ may refuse activateIgnoringOtherApps (cooperative
		// activation): run.sh asks LaunchServices to activate us instead
		if (tries == 10) [@"" writeToFile:[gOut stringByAppendingPathComponent:[gTag stringByAppendingString:@".need-activate"]] atomically:YES encoding:NSUTF8StringEncoding error:nil];
		after(0.05, wait);
	};
	after(0.05, wait);
}

static void runPhase(void) {
	if (gPhase >= (int)gPhases.count) {
		[gWin close];
		[NSApp terminate:nil];
		return;
	}
	NSString *phase = gPhases[gPhase];
	NSString *base = phase;
	BOOL wantActive = [phase hasPrefix:@"active-"];
	if (wantActive) base = [phase substringFromIndex:7];
	if ([phase hasPrefix:@"inactive-"]) base = [phase substringFromIndex:9];
	void (^go)(void) = ^{
		if ([base hasPrefix:@"anim"]) {
			runAnim(phase);
		} else if ([base hasPrefix:@"idle"]) {
			runIdle(phase);
		} else if ([base hasPrefix:@"flash"]) {
			runFlash(phase);
		} else if ([base hasPrefix:@"hit"]) {
			runHit(phase);
		} else if ([base hasPrefix:@"dump"]) {
			runDump(phase);
		} else if ([base isEqual:@"fullscreen"]) {
			setScroll((float)gScroll);
			[gWin toggleFullScreen:nil];
			after(2.5, ^{
				ready(phase, @{@"fullscreen" : @((gWin.styleMask & NSWindowStyleMaskFullScreen) != 0)});
				NSDate *st = [NSDate date];
				__block void (^w)(void);
				w = ^{
					if ([NSFileManager.defaultManager fileExistsAtPath:phaseFile(phase, @"done")] || -[st timeIntervalSinceNow] > 30) {
						[gWin toggleFullScreen:nil];
						after(2.0, ^{
							nextPhase();
						});
						return;
					}
					after(0.05, w);
				};
				w();
			});
		} else { // top, scrolled, or scrolled:N
			float s = 0;
			if ([base hasPrefix:@"scrolled"]) s = (float)gScroll;
			if ([base hasPrefix:@"scrolled:"]) s = [[base substringFromIndex:9] floatValue];
			setScroll(s);
			after(0.8, ^{
				if (!gRefScroll && !gPlainDoc) setScroll(s); // a second frame once the window is on screen
				after(0.4, ^{
					ready(phase, nil);
					waitDone(phase, [NSDate date], 40);
				});
			});
		}
	};
	if (wantActive)
		becomeActive(go);
	else
		go();
}

#pragma mark - Bench

// benchOne times, per frame of a wpt x hpt window, three designs of the
// band: D4b's flat bar in the content pass (baseline); the band re-rendered
// at half size, blurred, and composited in the content pass (rerender); and
// the band read back from the frame with a second pass over the whole target
// (readback). GPU time of one command buffer per frame, after a warm-up.
static NSDictionary *benchOne(CGFloat wpt, CGFloat hpt, CGFloat band, int frames) {
	gW = wpt;
	gBand = band;
	allocBlur();
	id<MTLTexture> target = newRT((NSUInteger)(wpt * 2), (NSUInteger)(hpt * 2));
	float w = (float)target.width, h = (float)target.height;
	NSArray *names = @[ @"flatBar", @"rerender", @"readback" ];
	NSMutableDictionary *r = [@{@"widthPt" : @(wpt), @"heightPt" : @(hpt), @"bandPt" : @(band), @"bandPx" : [NSString stringWithFormat:@"%.0fx%.0f", wpt * 2, band * 2]} mutableCopy];
	double *t = calloc(frames, sizeof(double));
	for (int v = 0; v < 3; v++) {
		for (int i = -20; i < frames; i++) {
			@autoreleasepool {
				id<MTLCommandBuffer> cb = [gQueue commandBuffer];
				if (v == 1) {
					gBlurSrc = @"rerender";
					encodeBlurPasses(cb, nil, 100);
				}
				id<MTLRenderCommandEncoder> e = pass(cb, target, YES, gBG);
				drawContent(e, w, h, 100, 1);
				if (v == 0) rect(e, w, h, 0, 0, w, (float)(band * 2), dark() ? 0x292929C7 : 0xFFFFFFD1);
				if (v == 1) drawBandComposite(e, w, h);
				[e endEncoding];
				if (v == 2) {
					gBlurSrc = @"drawable";
					encodeBlur(cb, target, target, 100);
				}
				[cb commit];
				[cb waitUntilCompleted];
				if (i >= 0) t[i] = cb.GPUEndTime - cb.GPUStartTime;
			}
		}
		r[names[v]] = dist(t, frames, 1000);
	}
	free(t);
	return r;
}

static void runBench(void) {
	NSMutableArray *rs = [NSMutableArray array];
	CGFloat w0 = gW;
	CGFloat sizes[][2] = {{900, 560}, {1440, 900}, {2560, 1440}};
	for (NSString *b in @[ @"gauss", @"kawase" ]) {
		gBlur = b;
		for (int k = 0; k < 3; k++)
			for (NSNumber *band in @[ @52, @80 ]) {
				NSMutableDictionary *r = [benchOne(sizes[k][0], sizes[k][1], band.doubleValue, 300) mutableCopy];
				r[@"blur"] = b;
				[rs addObject:r];
			}
	}
	gW = w0;
	writeJSON(@{@"bench" : rs, @"sigmaPt" : @(gSigma), @"kawaseIter" : @(gKIter), @"machine" : machine(), @"gpu" : gDev.name}, [NSString stringWithFormat:@"%@-bench.json", gTag]);
}

#pragma mark - main

int main(int argc, const char *argv[]) {
	@autoreleasepool {
		gPhases = @[ @"scrolled" ];
		for (int i = 1; i + 1 < argc; i += 2) {
			NSString *k = @(argv[i]), *v = @(argv[i + 1]);
			if ([k isEqual:@"-mode"]) gMode = v;
			else if ([k isEqual:@"-appearance"]) gAppearance = v;
			else if ([k isEqual:@"-out"]) gOut = v;
			else if ([k isEqual:@"-tag"]) gTag = v;
			else if ([k isEqual:@"-overlay"]) gOverlay = v;
			else if ([k isEqual:@"-toolbar"]) gToolbar = v;
			else if ([k isEqual:@"-title"]) gTitle = v;
			else if ([k isEqual:@"-sync"]) gSync = v;
			else if ([k isEqual:@"-content"]) gContent = v;
			else if ([k isEqual:@"-blur"]) gBlur = v;
			else if ([k isEqual:@"-blursrc"]) gBlurSrc = v;
			else if ([k isEqual:@"-fgsize"]) gFGSize = v;
			else if ([k isEqual:@"-phases"]) gPhases = [v componentsSeparatedByString:@","];
			else if ([k isEqual:@"-w"]) gW = v.doubleValue;
			else if ([k isEqual:@"-h"]) gH = v.doubleValue;
			else if ([k isEqual:@"-band"]) gBand = v.doubleValue;
			else if ([k isEqual:@"-scroll"]) gScroll = v.doubleValue;
			else if ([k isEqual:@"-margin"]) gMargin = v.doubleValue;
			else if ([k isEqual:@"-fg"]) gFG = v.boolValue;
			else if ([k isEqual:@"-drawn"]) gDrawn = v.boolValue;
			else if ([k isEqual:@"-split"]) gSplit = v.boolValue;
			else if ([k isEqual:@"-hc"]) gHC = v.boolValue;
			else if ([k isEqual:@"-anim"]) gAnimSec = v.doubleValue;
			else if ([k isEqual:@"-idle"]) gIdleSec = v.doubleValue;
			else if ([k isEqual:@"-speed"]) gSpeed = v.doubleValue;
			else if ([k isEqual:@"-vevmaterial"]) gVevMaterial = v.doubleValue;
			else if ([k isEqual:@"-sigma"]) gSigma = v.floatValue;
			else if ([k isEqual:@"-sat"]) gSat = v.floatValue;
			else if ([k isEqual:@"-tinta"]) gTintA = v.floatValue;
			else if ([k isEqual:@"-off"]) gOff = v.floatValue;
			else if ([k isEqual:@"-tint"]) gTint = (uint32_t)strtoul(v.UTF8String, NULL, 16);
			else if ([k isEqual:@"-koffset"]) gKOffset = v.floatValue;
			else if ([k isEqual:@"-kiter"]) gKIter = v.intValue;
			else if ([k isEqual:@"-flashframes"]) gFlashFrames = v.intValue;
			else if ([k isEqual:@"-septop"]) gSepTop = v.intValue;
			else if ([k isEqual:@"-drawables"]) gDrawables = v.intValue;
			else if ([k isEqual:@"-sepunder"]) gSepUnder = v.intValue;
		}
		[NSFileManager.defaultManager createDirectoryAtPath:gOut withIntermediateDirectories:YES attributes:nil error:nil];
		[NSApplication sharedApplication];
		NSApp.activationPolicy = NSApplicationActivationPolicyRegular;
		NSString *apName = dark() ? NSAppearanceNameDarkAqua : NSAppearanceNameAqua;
		if (gHC) apName = dark() ? NSAppearanceNameAccessibilityHighContrastDarkAqua : NSAppearanceNameAccessibilityHighContrastAqua;
		NSApp.appearance = [NSAppearance appearanceNamed:apName];
		gWSPid = findWS();
		makeContent();
		metalInit();
		if ([gMode isEqual:@"bench"]) {
			runBench();
			return 0;
		}
		[NSApp finishLaunching];
		makeWindow();
		[gWin orderFront:nil];
		after(0.3, ^{
			runPhase();
		});
		[NSApp run];
	}
	return 0;
}
