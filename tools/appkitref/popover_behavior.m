// AppKitPopoverBehavior: what closes a real NSPopover of each behavior, and
// whether the click that closes it also reaches the view under it. It is the
// reference rexui.Popover's dismissal policies are matched to (D7a).
//
//	AppKitPopoverBehavior -behavior transient|semi|app -scenario NAME
//
// It opens a window with an anchor button, another button ("Other") and a
// second window with its own button ("Away"), shows a popover relative to
// the anchor whose content holds a button ("Inside"), posts the scenario's
// event into the application's queue as the window server would, waits, and
// prints one JSON object: whether the popover is still shown, the clicks each
// button got, and the key window. Scenarios:
//
//	other      a click on Other, in the popover's parent window
//	empty      a click on the parent window's empty background
//	titlebar   a click on the parent window's title bar
//	anchor     a click on the anchor button
//	inside     a click on Inside, in the popover
//	away       a click on Away, in the app's other window
//	escape     the Escape key
//	return     the Return key
//	deactivate another application (the Finder) comes to the front
#import <AppKit/AppKit.h>

@interface Pop : NSObject <NSApplicationDelegate>
@property NSWindow *window, *other;
@property NSButton *anchor, *btn, *away, *inside;
@property NSPopover *popover;
@property NSPopoverBehavior behavior;
@property NSString *scenario;
@property int nAnchor, nOther, nAway, nInside;
@end

@implementation Pop
- (NSButton *)button:(NSString *)t at:(NSRect)r in:(NSView *)v action:(SEL)a {
	NSButton *b = [NSButton buttonWithTitle:t target:self action:a];
	b.frame = r;
	[v addSubview:b];
	return b;
}
- (void)hitAnchor:(id)s { self.nAnchor++; }
- (void)hitOther:(id)s { self.nOther++; }
- (void)hitAway:(id)s { self.nAway++; }
- (void)hitInside:(id)s { self.nInside++; }

- (void)applicationDidFinishLaunching:(NSNotification *)n {
	NSRect vis = NSScreen.mainScreen.visibleFrame;
	NSRect fr = NSMakeRect(vis.origin.x + 40, NSMaxY(vis) - 400 - 40, 520, 400);
	self.window = [[NSWindow alloc] initWithContentRect:fr styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable backing:NSBackingStoreBuffered defer:NO];
	self.window.releasedWhenClosed = NO;
	NSView *cv = self.window.contentView;
	self.anchor = [self button:@"Anchor" at:NSMakeRect(220, 200, 80, 24) in:cv action:@selector(hitAnchor:)];
	self.btn = [self button:@"Other" at:NSMakeRect(40, 40, 80, 24) in:cv action:@selector(hitOther:)];
	NSRect fr2 = NSMakeRect(NSMaxX(fr) + 40, fr.origin.y, 300, 200);
	self.other = [[NSWindow alloc] initWithContentRect:fr2 styleMask:NSWindowStyleMaskTitled backing:NSBackingStoreBuffered defer:NO];
	self.other.releasedWhenClosed = NO;
	self.away = [self button:@"Away" at:NSMakeRect(40, 80, 80, 24) in:self.other.contentView action:@selector(hitAway:)];
	[self.other orderFront:nil];
	[NSApp activateIgnoringOtherApps:YES];
	[self.window makeKeyAndOrderFront:nil];
	[self performSelector:@selector(show) withObject:nil afterDelay:0.8];
}

- (void)show {
	NSViewController *vc = [NSViewController new];
	NSView *v = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, 260, 140)];
	self.inside = [self button:@"Inside" at:NSMakeRect(20, 20, 80, 24) in:v action:@selector(hitInside:)];
	vc.view = v;
	NSPopover *p = [NSPopover new];
	p.contentViewController = vc;
	p.behavior = self.behavior;
	p.animates = NO;
	self.popover = p;
	[p showRelativeToRect:self.anchor.bounds ofView:self.anchor preferredEdge:NSRectEdgeMaxY];
	[self performSelector:@selector(act) withObject:nil afterDelay:0.8];
}

// a click at the centre of view v, as the window server delivers it: the
// press, then the release 80 ms later (posted to the queue, not run in a
// nested loop)
- (void)click:(NSView *)v {
	NSPoint p = [v convertPoint:NSMakePoint(NSMidX(v.bounds), NSMidY(v.bounds)) toView:nil];
	[self clickWindow:v.window at:p];
}
- (void)clickWindow:(NSWindow *)w at:(NSPoint)p {
	NSInteger num = w.windowNumber;
	for (int i = 0; i < 2; i++) {
		dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(i * 80 * NSEC_PER_MSEC)), dispatch_get_main_queue(), ^{
			NSEvent *e = [NSEvent mouseEventWithType:i == 0 ? NSEventTypeLeftMouseDown : NSEventTypeLeftMouseUp
			                                location:p modifierFlags:0 timestamp:NSProcessInfo.processInfo.systemUptime
			                            windowNumber:num context:nil eventNumber:0 clickCount:1 pressure:i == 0 ? 1 : 0];
			[NSApp postEvent:e atStart:NO];
		});
	}
}
- (void)key:(unsigned short)code chars:(NSString *)s {
	NSInteger num = (NSApp.keyWindow ?: self.window).windowNumber;
	for (int i = 0; i < 2; i++) {
		dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(i * 80 * NSEC_PER_MSEC)), dispatch_get_main_queue(), ^{
			NSEvent *e = [NSEvent keyEventWithType:i == 0 ? NSEventTypeKeyDown : NSEventTypeKeyUp location:NSZeroPoint modifierFlags:0
			                             timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:num context:nil
			                            characters:s charactersIgnoringModifiers:s isARepeat:NO keyCode:code];
			[NSApp postEvent:e atStart:NO];
		});
	}
}

- (void)act {
	NSString *s = self.scenario;
	NSWindow *kw0 = NSApp.keyWindow;
	NSString *kwBefore = kw0 ? NSStringFromClass([kw0 class]) : @"none";
	if ([s isEqual:@"other"]) [self click:self.btn];
	else if ([s isEqual:@"empty"]) [self clickWindow:self.window at:NSMakePoint(400, 300)];
	else if ([s isEqual:@"titlebar"]) [self clickWindow:self.window at:NSMakePoint(260, self.window.frame.size.height - 10)];
	else if ([s isEqual:@"anchor"]) [self click:self.anchor];
	else if ([s isEqual:@"inside"]) [self click:self.inside];
	else if ([s isEqual:@"away"]) [self click:self.away];
	else if ([s isEqual:@"escape"]) [self key:53 chars:@"\033"];
	else if ([s isEqual:@"return"]) [self key:36 chars:@"\r"];
	else if ([s isEqual:@"deactivate"]) {
		// another application comes to the front: this one resigns
		NSRunningApplication *f = [NSRunningApplication runningApplicationsWithBundleIdentifier:@"com.apple.finder"].firstObject;
		[f activateWithOptions:0];
	}
	[self performSelector:@selector(report:) withObject:kwBefore afterDelay:0.5];
}

- (void)report:(NSString *)kwBefore {
	NSDictionary *d = @{
		@"behavior" : @(self.behavior), @"scenario" : self.scenario, @"shownAfter" : @(self.popover.shown),
		@"anchor" : @(self.nAnchor), @"other" : @(self.nOther), @"away" : @(self.nAway), @"inside" : @(self.nInside),
		@"keyWindowBefore" : kwBefore, @"active" : @(NSApp.active), @"winKey" : @(self.window.keyWindow), @"winVisible" : @(self.window.visible), @"popKey" : @(self.popover.contentViewController.view.window.keyWindow), @"keyWindowAfter" : NSApp.keyWindow ? NSStringFromClass([NSApp.keyWindow class]) : @"none",
	};
	NSData *j = [NSJSONSerialization dataWithJSONObject:d options:NSJSONWritingSortedKeys error:nil];
	printf("%s\n", [[NSString alloc] initWithData:j encoding:NSUTF8StringEncoding].UTF8String);
	fflush(stdout);
	[NSApp terminate:nil];
}
@end

int main(int argc, char **argv) {
	@autoreleasepool {
		Pop *p = [Pop new];
		p.behavior = NSPopoverBehaviorTransient;
		p.scenario = @"other";
		for (int i = 1; i + 1 < argc; i += 2) {
			NSString *a = @(argv[i]), *v = @(argv[i + 1]);
			if ([a isEqual:@"-behavior"]) p.behavior = [v isEqual:@"transient"] ? NSPopoverBehaviorTransient : [v isEqual:@"semi"] ? NSPopoverBehaviorSemitransient : NSPopoverBehaviorApplicationDefined;
			else if ([a isEqual:@"-scenario"]) p.scenario = v;
		}
		NSApplication *app = NSApplication.sharedApplication;
		app.activationPolicy = NSApplicationActivationPolicyRegular;
		app.delegate = p;
		[app run];
	}
	return 0;
}
