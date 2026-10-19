// AppKitScroll: the AppKit reference for trackpad and wheel scrolling (F6).
// It opens one real window holding an NSScrollView, replays a scripted
// sequence of scroll events into it and records the scroll offset over time,
// so ui's physics can be fitted to NSScrollView's and checked against it
// (ui/testdata/appkit-scroll, ui/scrollfit_test.go).
//
//	AppKitScroll SCENARIO OUT [-show]
//
// SCENARIO is a text file of the format below; OUT receives the offsets.
// Each event is a CGEvent built by CGEventCreateScrollWheelEvent2 with the
// continuous, phase and momentum fields set, turned into an NSEvent
// (eventWithCGEvent:) and sent to the app's own window (-[NSWindow
// sendEvent:]): nothing is posted to the system's event stream, and the
// pointer does not move. -show leaves the window open for a second after
// the run.
//
// The scenario format, one directive per line ('#' starts a comment):
//
//	view W H          the scroll view's size in points
//	doc W H           the document's size in points
//	start X Y         the starting offset (points, from the document's top-left)
//	nested            the scroll view sits inside an outer vertical one
//	ev T KIND PHASE MOMENTUM DX DY
//	                  an event at T ms: KIND p (pixel deltas, a trackpad) or
//	                  l (line deltas, a wheel); PHASE none|maybegin|began|
//	                  changed|ended|cancelled|stationary; MOMENTUM
//	                  none|began|changed|ended; DX DY the deltas (points or
//	                  lines, positive moves the content right and down, as
//	                  scrollingDeltaX/Y)
//	end T             record until T ms
//
// OUT gets "t x y" lines (with nested, "t x y outerY"), the offset (points,
// the clip view's bounds origin: negative past the top or left edge) at every
// millisecond tick it changed, and "e T t" at each event: its scheduled time
// and when it was sent. Times are ms from the start of the run.
//
// What it found (macOS 27): NSScrollView rubber-bands a finger pull past an
// edge as d·(1 − 1/(c·x/d + 1)) with c ≈ 0.074 and d the view's height,
// springs back from rest as x0·e^(−12.5 t), stops a spring on MayBegin and
// resumes it on Cancelled, locks the predominant axis of the first events,
// latches nested views, and does not move on wheel lines past an edge (10
// points a line). It ignores synthetic momentum events altogether (they are
// not routed to the view), so momentum cannot be measured this way.
//
// The display must be awake (caffeinate -u): NSScrollView only moves on
// display frames.
#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>

// CGEventSetWindowLocation (CoreGraphics, undocumented) sets the point in the
// event's window (top-left origin) that -[NSEvent locationInWindow] reports.
extern void CGEventSetWindowLocation(CGEventRef, CGPoint);

typedef struct {
	double t;
	int line;
	int phase, momentum; // CGScrollPhase and CGMomentumScrollPhase values
	double dx, dy;
} Ev;

@interface Flipped : NSView
@end
@implementation Flipped
- (BOOL)isFlipped { return YES; }
// Responsive scrolling tracks a gesture on a thread of its own, from the
// window server's events: the synthetic ones after the first would be lost.
// The physics is the same on the main thread.
+ (BOOL)isCompatibleWithResponsiveScrolling { return NO; }
- (void)drawRect:(NSRect)r {
	[NSColor.textBackgroundColor setFill];
	NSRectFill(r);
	[NSColor.separatorColor setFill];
	for (double y = floor(NSMinY(r) / 40) * 40; y < NSMaxY(r); y += 40) NSRectFill(NSMakeRect(NSMinX(r), y, NSWidth(r), 1));
}
@end

@interface Runner : NSObject <NSApplicationDelegate>
@property NSWindow *window;
@property NSScrollView *sv, *outer;
@property NSString *scenario, *outPath;
@property BOOL show, nested;
@property NSSize view, doc;
@property NSPoint start;
@property double endT;
@property NSMutableData *evs;
@property NSMutableString *out;
@property double t0;
@property NSUInteger next;
@property NSPoint last, lastOuter;
@property dispatch_source_t src;
@end

static int phaseOf(NSString *s) {
	NSDictionary *m = @{@"none" : @0, @"began" : @1, @"changed" : @2, @"ended" : @4, @"cancelled" : @8, @"maybegin" : @128, @"stationary" : @2};
	NSNumber *n = m[s];
	if (!n) {
		fprintf(stderr, "bad phase %s\n", s.UTF8String);
		exit(65);
	}
	return n.intValue;
}

static int momentumOf(NSString *s) {
	NSDictionary *m = @{@"none" : @0, @"began" : @1, @"changed" : @2, @"ended" : @3};
	NSNumber *n = m[s];
	if (!n) {
		fprintf(stderr, "bad momentum %s\n", s.UTF8String);
		exit(65);
	}
	return n.intValue;
}

@implementation Runner

- (void)parse {
	NSString *txt = [NSString stringWithContentsOfFile:self.scenario encoding:NSUTF8StringEncoding error:nil];
	if (!txt) {
		fprintf(stderr, "cannot read %s\n", self.scenario.UTF8String);
		exit(66);
	}
	self.view = NSMakeSize(400, 400);
	self.doc = NSMakeSize(400, 2000);
	self.evs = [NSMutableData data];
	for (NSString *raw in [txt componentsSeparatedByString:@"\n"]) {
		NSString *l = raw;
		NSRange h = [l rangeOfString:@"#"];
		if (h.location != NSNotFound) l = [l substringToIndex:h.location];
		NSArray *f = [[l stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceCharacterSet] componentsSeparatedByCharactersInSet:NSCharacterSet.whitespaceCharacterSet];
		f = [f filteredArrayUsingPredicate:[NSPredicate predicateWithFormat:@"length > 0"]];
		if (f.count == 0) continue;
		NSString *k = f[0];
		if ([k isEqual:@"view"]) self.view = NSMakeSize([f[1] doubleValue], [f[2] doubleValue]);
		else if ([k isEqual:@"doc"]) self.doc = NSMakeSize([f[1] doubleValue], [f[2] doubleValue]);
		else if ([k isEqual:@"start"]) self.start = NSMakePoint([f[1] doubleValue], [f[2] doubleValue]);
		else if ([k isEqual:@"nested"]) self.nested = YES;
		else if ([k isEqual:@"end"]) self.endT = [f[1] doubleValue];
		else if ([k isEqual:@"ev"]) {
			Ev e = {.t = [f[1] doubleValue], .line = [f[2] isEqual:@"l"], .phase = phaseOf(f[3]), .momentum = momentumOf(f[4]),
			        .dx = [f[5] doubleValue], .dy = [f[6] doubleValue]};
			[self.evs appendBytes:&e length:sizeof e];
		}
	}
}

- (NSScrollView *)scrollViewWithFrame:(NSRect)r doc:(NSSize)doc {
	NSScrollView *sv = [[NSScrollView alloc] initWithFrame:r];
	sv.hasVerticalScroller = YES;
	sv.hasHorizontalScroller = doc.width > r.size.width;
	sv.drawsBackground = YES;
	Flipped *d = [[Flipped alloc] initWithFrame:NSMakeRect(0, 0, doc.width, doc.height)];
	sv.documentView = d;
	return sv;
}

- (void)applicationDidFinishLaunching:(NSNotification *)n {
	[self parse];
	NSRect wr = NSMakeRect(200, 200, self.view.width, self.view.height);
	if (self.nested) wr.size.height += 200;
	self.window = [[NSWindow alloc] initWithContentRect:wr styleMask:NSWindowStyleMaskTitled backing:NSBackingStoreBuffered defer:NO];
	self.window.releasedWhenClosed = NO;
	self.window.title = @"AppKitScroll";
	NSView *host = self.window.contentView;
	if (self.nested) {
		// the outer view scrolls a column: 100 pt above the inner view, 100 below,
		// and more below that, so it has room on both sides
		self.outer = [self scrollViewWithFrame:host.bounds doc:NSMakeSize(self.view.width, self.view.height + 800)];
		[host addSubview:self.outer];
		host = self.outer.documentView;
		[self.outer.contentView scrollToPoint:NSMakePoint(0, 100)];
		[self.outer reflectScrolledClipView:self.outer.contentView];
	}
	self.sv = [self scrollViewWithFrame:NSMakeRect(0, self.nested ? 200 : 0, self.view.width, self.view.height) doc:self.doc];
	[host addSubview:self.sv];
	[self.sv.contentView scrollToPoint:self.start];
	[self.sv reflectScrolledClipView:self.sv.contentView];
	[self.window makeKeyAndOrderFront:nil];
	[NSApp activateIgnoringOtherApps:YES];
	self.out = [NSMutableString string];
	// Let the window come up before the run starts.
	dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 500 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
		self.t0 = CACurrentMediaTime() * 1000;
		self.last = NSMakePoint(NAN, NAN);
		self.src = dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER, 0, DISPATCH_TIMER_STRICT, dispatch_get_main_queue());
		dispatch_source_set_timer(self.src, DISPATCH_TIME_NOW, NSEC_PER_MSEC, 0);
		dispatch_source_set_event_handler(self.src, ^{ [self tick:nil]; });
		dispatch_resume(self.src);
	});
}

- (void)post:(const Ev *)e {
	CGEventRef cg = CGEventCreateScrollWheelEvent2(NULL, e->line ? kCGScrollEventUnitLine : kCGScrollEventUnitPixel, 2,
	                                               (int32_t)lround(e->dy), (int32_t)lround(e->dx), 0);
	if (!e->line) {
		CGEventSetIntegerValueField(cg, kCGScrollWheelEventIsContinuous, 1);
		CGEventSetDoubleValueField(cg, kCGScrollWheelEventFixedPtDeltaAxis1, e->dy);
		CGEventSetDoubleValueField(cg, kCGScrollWheelEventFixedPtDeltaAxis2, e->dx);
		CGEventSetDoubleValueField(cg, kCGScrollWheelEventPointDeltaAxis1, e->dy);
		CGEventSetDoubleValueField(cg, kCGScrollWheelEventPointDeltaAxis2, e->dx);
	}
	CGEventSetIntegerValueField(cg, kCGScrollWheelEventScrollPhase, e->phase);
	CGEventSetIntegerValueField(cg, kCGScrollWheelEventMomentumPhase, e->momentum);
	// at the inner view's centre, in global (top-left) coordinates
	NSRect vr = [self.sv convertRect:self.sv.bounds toView:nil];
	NSPoint wp = NSMakePoint(NSMidX(vr), NSMidY(vr));
	NSPoint sp = [self.window convertPointToScreen:wp];
	CGFloat top = NSMaxY(NSScreen.screens[0].frame);
	CGEventSetLocation(cg, CGPointMake(sp.x, top - sp.y));
	CGEventSetIntegerValueField(cg, kCGMouseEventWindowUnderMousePointer, self.window.windowNumber);
	CGEventSetIntegerValueField(cg, kCGMouseEventWindowUnderMousePointerThatCanHandleThisEvent, self.window.windowNumber);
	// The NSEvent's window (field 51, undocumented) and its point in the window:
	// without them -[NSWindow sendEvent:] has no view to give the event to.
	CGEventSetIntegerValueField(cg, (CGEventField)51, self.window.windowNumber);
	CGEventSetWindowLocation(cg, CGPointMake(wp.x, NSHeight(self.window.frame) - wp.y));
	NSEvent *ev = [NSEvent eventWithCGEvent:cg];
	CFRelease(cg);
	[self.window sendEvent:ev];
}

- (void)tick:(id)unused {
	double now = CACurrentMediaTime() * 1000 - self.t0;
	const Ev *evs = self.evs.bytes;
	NSUInteger n = self.evs.length / sizeof(Ev);
	while (self.next < n && evs[self.next].t <= now) {
		[self.out appendFormat:@"e %.0f %.1f\n", evs[self.next].t, now];
		[self post:&evs[self.next]];
		self.next++;
	}
	NSPoint p = self.sv.contentView.bounds.origin;
	NSPoint po = self.outer ? self.outer.contentView.bounds.origin : NSZeroPoint;
	if (!NSEqualPoints(p, self.last) || !NSEqualPoints(po, self.lastOuter)) {
		if (self.outer) [self.out appendFormat:@"%.1f %.3f %.3f %.3f\n", now, p.x, p.y, po.y];
		else [self.out appendFormat:@"%.1f %.3f %.3f\n", now, p.x, p.y];
		self.last = p;
		self.lastOuter = po;
	}
	if (now >= self.endT && self.next >= n) {
		dispatch_source_cancel(self.src);
		[self.out writeToFile:self.outPath atomically:YES encoding:NSUTF8StringEncoding error:nil];
		dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (self.show ? 1000 : 0) * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
			[NSApp terminate:nil];
		});
	}
}
@end

int main(int argc, const char **argv) {
	@autoreleasepool {
		if (argc < 3) {
			fprintf(stderr, "usage: AppKitScroll SCENARIO OUT [-show]\n");
			return 64;
		}
		NSApplication *app = NSApplication.sharedApplication;
		app.activationPolicy = NSApplicationActivationPolicyRegular;
		Runner *r = [Runner new];
		r.scenario = [NSString stringWithUTF8String:argv[1]];
		r.outPath = [NSString stringWithUTF8String:argv[2]];
		for (int i = 3; i < argc; i++) {
			if (!strcmp(argv[i], "-show")) r.show = YES;
		}
		app.delegate = r;
		[app run];
	}
	return 0;
}
