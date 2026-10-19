// AppKitShell: the pure-AppKit counterpart of rexlook's native shell
// (rex.NativeConfig), for side-by-side captures with GnuiShot. It opens one
// real window of the same size with the same chrome: a Liquid Glass backdrop
// (NSGlassEffectView on macOS 26+, else a behind-window NSVisualEffectView),
// a compact unified NSToolbar with the same tabs, ⌘ and +, the same title
// and subtitle, and rexlook's three panes of its first tab as plain rounded
// layers (the focused one white, the others translucent), without content.
//
//	AppKitShell [-light | -dark] [-backdrop glass|clear|window|none] [-unified] [-quit-after S]
//
// -light and -dark set this app's own appearance (NSApp.appearance); nothing
// on the system changes. Without them it follows the system. It quits after
// -quit-after seconds (default 20) or when the window is closed.
#import <AppKit/AppKit.h>

static NSString *const kTabs = @"tabs", *const kCmd = @"cmd", *const kPlus = @"plus";

@interface Shell : NSObject <NSToolbarDelegate, NSWindowDelegate, NSApplicationDelegate>
@property NSWindow *window;
@property NSString *backdrop;
@property BOOL unified;
@property NSTimeInterval quitAfter;
@property NSMutableArray<NSView *> *panes;
@end

@implementation Shell

- (NSArray<NSToolbarItemIdentifier> *)toolbarDefaultItemIdentifiers:(NSToolbar *)tb {
	return @[ kTabs, NSToolbarFlexibleSpaceItemIdentifier, kCmd, kPlus ];
}
- (NSArray<NSToolbarItemIdentifier> *)toolbarAllowedItemIdentifiers:(NSToolbar *)tb {
	return [self toolbarDefaultItemIdentifiers:tb];
}
- (NSToolbarItem *)toolbar:(NSToolbar *)tb itemForItemIdentifier:(NSToolbarItemIdentifier)ident willBeInsertedIntoToolbar:(BOOL)flag {
	NSToolbarItem *item = [[NSToolbarItem alloc] initWithItemIdentifier:ident];
	if ([ident isEqual:kTabs]) {
		NSArray *labels = @[ @"Stress-test Snake demo | rex-snake", @"Bun ~/sites/announcement-draft", @"fish ~/sites/macos-client-update" ];
		NSArray *symbols = @[ @"sparkles", @"face.smiling", @"terminal" ];
		NSSegmentedControl *seg = [[NSSegmentedControl alloc] initWithFrame:NSMakeRect(0, 0, 100, 24)];
		seg.trackingMode = NSSegmentSwitchTrackingSelectOne;
		seg.segmentCount = labels.count;
		for (NSInteger i = 0; i < (NSInteger)labels.count; i++) {
			[seg setLabel:labels[i] forSegment:i];
			[seg setImage:[NSImage imageWithSystemSymbolName:symbols[i] accessibilityDescription:labels[i]] forSegment:i];
			[seg setWidth:205 forSegment:i];
		}
		seg.selectedSegment = 0;
		[seg sizeToFit];
		item.view = seg;
		item.label = @"Tabs";
	} else {
		BOOL cmd = [ident isEqual:kCmd];
		item.image = [NSImage imageWithSystemSymbolName:cmd ? @"command" : @"plus" accessibilityDescription:nil];
		item.label = cmd ? @"Commands" : @"New Tab";
		item.toolTip = item.label;
		item.bordered = YES;
		item.target = self;
		item.action = @selector(noop:);
	}
	return item;
}
- (void)noop:(id)sender {}

- (void)applicationDidFinishLaunching:(NSNotification *)n {
	NSRect r = NSMakeRect(0, 0, 1057, 639);
	NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable |
	                          NSWindowStyleMaskResizable | NSWindowStyleMaskFullSizeContentView;
	NSWindow *w = [[NSWindow alloc] initWithContentRect:r styleMask:style backing:NSBackingStoreBuffered defer:NO];
	self.window = w;
	w.releasedWhenClosed = NO;
	w.delegate = self;
	w.title = @"rex-snake";
	w.subtitle = @"Mac Studio";
	w.titlebarAppearsTransparent = YES;
	NSToolbar *tb = [[NSToolbar alloc] initWithIdentifier:@"AppKitShell"];
	tb.delegate = self;
	tb.displayMode = NSToolbarDisplayModeIconOnly;
	w.toolbarStyle = self.unified ? NSWindowToolbarStyleUnified : NSWindowToolbarStyleUnifiedCompact;
	w.toolbar = tb;

	NSView *container = [[NSView alloc] initWithFrame:r];
	container.wantsLayer = YES;
	NSView *effect = nil;
	Class glass = NSClassFromString(@"NSGlassEffectView");
	if (glass && ([self.backdrop isEqual:@"glass"] || [self.backdrop isEqual:@"clear"])) {
		effect = [[glass alloc] initWithFrame:r];
		[effect setValue:@([self.backdrop isEqual:@"clear"] ? 1 : 0) forKey:@"style"];
	} else if (![self.backdrop isEqual:@"none"]) {
		NSVisualEffectView *v = [[NSVisualEffectView alloc] initWithFrame:r];
		v.material = NSVisualEffectMaterialUnderWindowBackground;
		v.blendingMode = NSVisualEffectBlendingModeBehindWindow;
		v.state = NSVisualEffectStateFollowsWindowActiveState;
		effect = v;
	}
	if (effect) {
		effect.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
		[container addSubview:effect];
		w.opaque = NO;
		w.backgroundColor = [NSColor colorWithWhite:1 alpha:0.001];
	}
	w.contentView = container;
	if (effect && glass && [effect isKindOfClass:glass] && [w respondsToSelector:NSSelectorFromString(@"_cornerRadius")])
		[effect setValue:[w valueForKey:@"_cornerRadius"] forKey:@"cornerRadius"];

	// rexlook's first tab (rex.Config, 1057×639 pt): work area 7.5 pt in, 45.5 pt
	// from the top; split 0.54638 across with a 7 pt gutter, the left half 0.43302
	// down with 7.5 pt. Top-left coordinates, flipped below.
	CGFloat H = r.size.height;
	NSRect rects[3] = {
	    {{7.5, 45.5}, {573 - 7.5, 296 - 45.5}},   // agent: focused, white
	    {{7.5, 303.5}, {573 - 7.5, 631.5 - 303.5}}, // git
	    {{580, 45.5}, {1049.5 - 580, 631.5 - 45.5}}, // snake
	};
	self.panes = [NSMutableArray array];
	for (int i = 0; i < 3; i++) {
		NSRect p = rects[i];
		p.origin.y = H - p.origin.y - p.size.height;
		NSView *v = [[NSView alloc] initWithFrame:p];
		v.wantsLayer = YES;
		v.layer.cornerRadius = 10.5;
		v.layer.cornerCurve = kCACornerCurveContinuous;
		v.layer.borderWidth = 0.5;
		v.autoresizingMask = NSViewMaxXMargin | NSViewMinYMargin;
		[container addSubview:v];
		[self.panes addObject:v];
	}
	[self restyle];
	[w center];
	[w makeKeyAndOrderFront:nil];
	[NSApp activate];
	[NSApp addObserver:self forKeyPath:@"effectiveAppearance" options:0 context:nil];
	[NSTimer scheduledTimerWithTimeInterval:self.quitAfter repeats:NO block:^(NSTimer *t) { [NSApp terminate:nil]; }];
}

// restyle colours the panes for the window's appearance: rexlook's Surface,
// opaque when focused and at its unfocused alpha (0.575 light, 0.5 dark)
// otherwise, with its hairline.
- (void)restyle {
	BOOL dark = [[self.window.effectiveAppearance bestMatchFromAppearancesWithNames:@[ NSAppearanceNameAqua, NSAppearanceNameDarkAqua ]]
	    isEqual:NSAppearanceNameDarkAqua];
	NSColor *surf = dark ? [NSColor colorWithSRGBRed:0x2A / 255.0 green:0x2C / 255.0 blue:0x3A / 255.0 alpha:1] : NSColor.whiteColor;
	CGFloat dim = dark ? 0.5 : 0.575;
	for (NSUInteger i = 0; i < self.panes.count; i++) {
		CALayer *l = self.panes[i].layer;
		l.backgroundColor = [surf colorWithAlphaComponent:i == 0 ? 1 : dim].CGColor;
		l.borderColor = (dark ? [NSColor colorWithWhite:1 alpha:0.11] : [NSColor colorWithSRGBRed:0x5C / 255.0 green:0x5E / 255.0 blue:0x60 / 255.0 alpha:0.25]).CGColor;
	}
}
- (void)observeValueForKeyPath:(NSString *)k ofObject:(id)o change:(NSDictionary *)c context:(void *)x {
	[self restyle];
}
- (void)windowWillClose:(NSNotification *)n { [NSApp terminate:nil]; }
@end

int main(int argc, const char **argv) {
	@autoreleasepool {
		NSApplication *app = NSApplication.sharedApplication;
		app.activationPolicy = NSApplicationActivationPolicyRegular;
		Shell *s = [Shell new];
		s.backdrop = @"glass";
		s.quitAfter = 20;
		for (int i = 1; i < argc; i++) {
			if (!strcmp(argv[i], "-light")) app.appearance = [NSAppearance appearanceNamed:NSAppearanceNameAqua];
			else if (!strcmp(argv[i], "-dark")) app.appearance = [NSAppearance appearanceNamed:NSAppearanceNameDarkAqua];
			else if (!strcmp(argv[i], "-unified")) s.unified = YES;
			else if (!strcmp(argv[i], "-backdrop") && i + 1 < argc) s.backdrop = [NSString stringWithUTF8String:argv[++i]];
			else if (!strcmp(argv[i], "-quit-after") && i + 1 < argc) s.quitAfter = atof(argv[++i]);
			else if (!strncmp(argv[i], "-psn", 4)) continue; // LaunchServices' process serial number
			else {
				fprintf(stderr, "usage: AppKitShell [-light | -dark] [-backdrop glass|clear|window|none] [-unified] [-quit-after S]\n");
				return 64;
			}
		}
		app.delegate = s;
		[app run];
	}
	return 0;
}
