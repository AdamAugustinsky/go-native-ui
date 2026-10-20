// AppKitPopover: a real NSPopover on a real window, for live captures with
// GnuiShot (an NSPopover is a window of its own that the window server
// composites, so AppKitRef's offscreen renders cannot show it). It is the
// reference rexui.Popover is measured against.
//
//	AppKitPopover [-light | -dark] [-edge below|above|right|left] [-behavior transient|app|semi]
//	              [-size WxH] [-seconds S]
//
// It opens a 520 × 400 pt window at the top left of the main display with a
// small button, shows a popover below (or on -edge) the button after half a
// second, and quits after -seconds (default 12). The popover's content is a
// plain view of -size (default 260 × 140) with one label, so the capture
// shows the popover's own shape, material and shadow. -light and -dark set
// this app's appearance only; nothing on the system changes.
//
// It also prints the popover's metrics (the content view's frame in the
// popover window, the window's frame and the arrow's room) as JSON on stdout
// when it has shown, for the measurement: tools/appkitref/popover.sh.
#import <AppKit/AppKit.h>

@interface Pop : NSObject <NSApplicationDelegate>
@property NSWindow *window;
@property NSButton *button;
@property NSPopover *popover;
@property NSRectEdge edge;
@property NSPopoverBehavior behavior;
@property NSSize size;
@property NSTimeInterval seconds;
@end

@implementation Pop

- (void)applicationDidFinishLaunching:(NSNotification *)n {
	NSScreen *scr = NSScreen.mainScreen;
	NSRect vis = scr.visibleFrame;
	NSRect fr = NSMakeRect(vis.origin.x + 40, NSMaxY(vis) - 400 - 40, 520, 400);
	self.window = [[NSWindow alloc] initWithContentRect:fr
	                                          styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable
	                                            backing:NSBackingStoreBuffered
	                                              defer:NO];
	self.window.title = @"AppKitPopover";
	self.window.releasedWhenClosed = NO;
	NSView *cv = self.window.contentView;
	NSButton *b = [NSButton buttonWithTitle:@"Anchor" target:nil action:nil];
	b.frame = NSMakeRect(220, 200, 80, 24);
	[cv addSubview:b];
	self.button = b;
	[NSApp activate];
	[self.window makeKeyAndOrderFront:nil];
	[self performSelector:@selector(show) withObject:nil afterDelay:0.6];
	[NSTimer scheduledTimerWithTimeInterval:self.seconds
	                                 target:NSApp
	                               selector:@selector(terminate:)
	                               userInfo:nil
	                                repeats:NO];
}

- (void)show {
	NSViewController *vc = [NSViewController new];
	NSView *v = [[NSView alloc] initWithFrame:(NSRect){NSZeroPoint, self.size}];
	NSTextField *l = [NSTextField labelWithString:@"Popover content"];
	l.frame = NSMakeRect(16, self.size.height - 16 - 17, 200, 17);
	[v addSubview:l];
	vc.view = v;
	NSPopover *p = [NSPopover new];
	p.contentViewController = vc;
	p.behavior = self.behavior;
	p.animates = NO;
	self.popover = p;
	[p showRelativeToRect:self.button.bounds ofView:self.button preferredEdge:self.edge];
	[self performSelector:@selector(report) withObject:nil afterDelay:0.4];
}

- (void)report {
	NSWindow *pw = self.popover.contentViewController.view.window;
	NSRect wf = pw.frame, cf = [self.popover.contentViewController.view convertRect:self.popover.contentViewController.view.bounds toView:nil];
	NSRect cs = [pw convertRectToScreen:cf];
	NSRect bf = [self.button convertRect:self.button.bounds toView:nil];
	NSRect bs = [self.window convertRectToScreen:bf];
	NSScreen *scr = NSScreen.mainScreen;
	double H = scr.frame.size.height;
	// top-left origin screen points
#define R(r) @{@"x" : @(r.origin.x), @"y" : @(H - NSMaxY(r)), @"w" : @(r.size.width), @"h" : @(r.size.height)}
	NSDictionary *d = @{
		@"popoverWindow" : R(wf),
		@"content" : R(cs),
		@"anchor" : R(bs),
		@"window" : R(self.window.frame),
		@"edge" : @(self.edge),
		@"shown" : @(self.popover.shown),
		@"appearance" : NSApp.effectiveAppearance.name,
		@"scale" : @(scr.backingScaleFactor),
		@"popoverWindowClass" : NSStringFromClass([pw class]),
	};
	NSData *j = [NSJSONSerialization dataWithJSONObject:d options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
	printf("%s\n", [[NSString alloc] initWithData:j encoding:NSUTF8StringEncoding].UTF8String);
	fflush(stdout);
}

@end

int main(int argc, char **argv) {
	@autoreleasepool {
		Pop *p = [Pop new];
		p.edge = NSRectEdgeMaxY; // the popover below the anchor, its arrow up: the edge of the anchor it points at, in a non-flipped view
		p.behavior = NSPopoverBehaviorApplicationDefined;
		p.size = NSMakeSize(260, 140);
		p.seconds = 12;
		NSAppearance *ap = nil;
		for (int i = 1; i < argc; i++) {
			NSString *a = @(argv[i]), *v = i + 1 < argc ? @(argv[i + 1]) : @"";
			if ([a isEqual:@"-light"]) ap = [NSAppearance appearanceNamed:NSAppearanceNameAqua];
			else if ([a isEqual:@"-dark"]) ap = [NSAppearance appearanceNamed:NSAppearanceNameDarkAqua];
			else if ([a isEqual:@"-edge"]) {
				p.edge = [v isEqual:@"above"] ? NSRectEdgeMinY : [v isEqual:@"right"] ? NSRectEdgeMaxX : [v isEqual:@"left"] ? NSRectEdgeMinX : NSRectEdgeMaxY;
				i++;
			} else if ([a isEqual:@"-behavior"]) {
				p.behavior = [v isEqual:@"transient"] ? NSPopoverBehaviorTransient : [v isEqual:@"semi"] ? NSPopoverBehaviorSemitransient : NSPopoverBehaviorApplicationDefined;
				i++;
			} else if ([a isEqual:@"-size"]) {
				double w = 260, h = 140;
				sscanf(v.UTF8String, "%lfx%lf", &w, &h);
				p.size = NSMakeSize(w, h);
				i++;
			} else if ([a isEqual:@"-seconds"]) {
				p.seconds = v.doubleValue;
				i++;
			}
		}
		NSApplication *app = NSApplication.sharedApplication;
		if (ap) app.appearance = ap;
		app.activationPolicy = NSApplicationActivationPolicyRegular;
		app.delegate = p;
		[app run];
	}
	return 0;
}
