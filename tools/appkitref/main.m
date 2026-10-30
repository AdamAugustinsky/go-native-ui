// AppKitRef: render real AppKit controls offscreen at 2x, in the Aqua and
// DarkAqua appearances, and measure what our widget kit should match: sizes,
// fonts, text styles, table and sidebar metrics, scrollers, window chrome,
// semantic colours, corner radii and focus rings (from the pixels).
//
// A dev tool, not framework code (like tools/bouncerec). See README.md.
//
//	AppKitRef -o DIR [-AppleAccentColor 4]   render + measure into DIR
//	AppKitRef -sheet MANIFEST.json OUT.png   compose a side-by-side sheet
//	AppKitRef -live NAMES.txt [-mode dark]   show samples in a window briefly
//	AppKitRef -machine FILE                  write where this runs (alone, or beside the others)
//
// Except with -live it never shows a window: the activation policy is
// Prohibited, and every window it makes is created deferred and never ordered
// on screen. Key-window
// state is simulated (see ARWindow) because an app that is never active has no
// key window; each sample says which state it was drawn in.
#import <AppKit/AppKit.h>
#import <objc/runtime.h>
#import <CoreText/CoreText.h>
#include <math.h>
#import "machine.h"

#pragma mark - Simulated key/main window state

static BOOL gKey = YES;

// ARWindow reports key and main status (and the private "appearance" variants
// AppKit's controls consult) from gKey, so that controls draw their active
// look in a window that is never on screen.
@interface ARWindow : NSWindow
@end
@implementation ARWindow
- (BOOL)isKeyWindow { return gKey; }
- (BOOL)isMainWindow { return gKey; }
- (BOOL)canBecomeKeyWindow { return YES; }
- (BOOL)hasKeyAppearance { return gKey; }
- (BOOL)hasMainAppearance { return gKey; }
- (BOOL)_hasKeyAppearance { return gKey; }
- (BOOL)_hasMainAppearance { return gKey; }
- (BOOL)_hasActiveAppearance { return gKey; }
- (BOOL)_hasActiveAppearanceIgnoringKeyFocus { return gKey; }
@end

@interface ARApp : NSApplication
@end
@implementation ARApp
- (BOOL)isActive { return gKey; }
- (BOOL)_isActiveApp { return gKey; }
@end

#pragma mark - Small helpers

static NSMutableArray *keep; // data sources and delegates that must outlive their views

static id num(double v) { return isfinite(v) ? (id)@(round(v * 100) / 100) : (id)NSNull.null; }
static NSDictionary *sizeJ(NSSize s) { return @{@"w" : num(s.width), @"h" : num(s.height)}; }
static NSDictionary *rectJ(NSRect r) { return @{@"x" : num(r.origin.x), @"y" : num(r.origin.y), @"w" : num(r.size.width), @"h" : num(r.size.height)}; }

static NSString *hexOf(NSColor *c) {
	NSColor *s = [c colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
	if (!s) return @"(pattern)";
	int r = (int)lround(fmin(fmax(s.redComponent, 0), 1) * 255), g = (int)lround(fmin(fmax(s.greenComponent, 0), 1) * 255),
	    b = (int)lround(fmin(fmax(s.blueComponent, 0), 1) * 255), a = (int)lround(s.alphaComponent * 255);
	return [NSString stringWithFormat:@"#%02X%02X%02X%02X", r, g, b, a];
}

static NSString *resolveHex(NSColor *c, NSAppearance *ap) {
	__block NSString *h;
	[ap performAsCurrentDrawingAppearance:^{
		h = hexOf(c);
	}];
	return h;
}

static NSDictionary *fontJ(NSFont *f) {
	if (!f) return @{};
	NSDictionary *tr = [f.fontDescriptor objectForKey:NSFontTraitsAttribute];
	NSMutableDictionary *d = [@{
		@"name" : f.fontName ?: @"",
		@"family" : f.familyName ?: @"",
		@"size" : num(f.pointSize),
		@"weight" : num([tr[NSFontWeightTrait] doubleValue]),
		@"ascender" : num(f.ascender),
		@"descender" : num(f.descender),
		@"leading" : num(f.leading),
		@"capHeight" : num(f.capHeight),
		@"xHeight" : num(f.xHeight),
	} mutableCopy];
	NSLayoutManager *lm = [NSLayoutManager new];
	d[@"lineHeight"] = num([lm defaultLineHeightForFont:f]);
	CFDictionaryRef var = CTFontCopyVariation((__bridge CTFontRef)f);
	if (var) {
		NSMutableDictionary *axes = [NSMutableDictionary dictionary];
		NSArray *axList = CFBridgingRelease(CTFontCopyVariationAxes((__bridge CTFontRef)f));
		for (NSDictionary *ax in axList) {
			NSNumber *ident = ax[(__bridge NSString *)kCTFontVariationAxisIdentifierKey];
			NSString *name = ax[(__bridge NSString *)kCTFontVariationAxisNameKey] ?: ident.stringValue;
			NSNumber *v = ((__bridge NSDictionary *)var)[ident];
			if (v) axes[name] = num(v.doubleValue);
		}
		d[@"variation"] = axes;
		CFRelease(var);
	}
	// advance of a fixed string per em: the tracking (and optical size) at work
	NSString *probe = @"Hamburgefonstiv 0123456789";
	NSSize sz = [probe sizeWithAttributes:@{NSFontAttributeName : f}];
	d[@"emAdvanceProbe"] = num(sz.width / f.pointSize);
	return d;
}

#pragma mark - Bitmaps

static NSBitmapImageRep *newRep(NSSize pt) {
	NSBitmapImageRep *r = [[NSBitmapImageRep alloc] initWithBitmapDataPlanes:NULL
	                                                              pixelsWide:(NSInteger)lround(pt.width * 2)
	                                                              pixelsHigh:(NSInteger)lround(pt.height * 2)
	                                                           bitsPerSample:8
	                                                         samplesPerPixel:4
	                                                                hasAlpha:YES
	                                                                isPlanar:NO
	                                                          colorSpaceName:NSDeviceRGBColorSpace
	                                                             bytesPerRow:0
	                                                            bitsPerPixel:32];
	r.size = pt;
	r = [r bitmapImageRepByRetaggingWithColorSpace:NSColorSpace.sRGBColorSpace];
	memset(r.bitmapData, 0, r.bytesPerRow * r.pixelsHigh);
	return r;
}

static void savePNG(NSBitmapImageRep *r, NSString *path) {
	[[NSFileManager defaultManager] createDirectoryAtPath:path.stringByDeletingLastPathComponent withIntermediateDirectories:YES attributes:nil error:nil];
	[[r representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:path atomically:YES];
}

// drawInto runs draw with rep as the current context (points, origin bottom left).
static void drawInto(NSBitmapImageRep *rep, void (^draw)(void)) {
	[NSGraphicsContext saveGraphicsState];
	NSGraphicsContext.currentContext = [NSGraphicsContext graphicsContextWithBitmapImageRep:rep];
	draw();
	[NSGraphicsContext.currentContext flushGraphics];
	[NSGraphicsContext restoreGraphicsState];
}

// pixel access: premultiplied RGBA, row 0 at the top
typedef struct {
	const uint8_t *p;
	NSInteger w, h, bpr;
} Px;
static Px pxOf(NSBitmapImageRep *r) { return (Px){r.bitmapData, r.pixelsWide, r.pixelsHigh, r.bytesPerRow}; }
static double alphaAt(Px p, NSInteger x, NSInteger y) {
	if (x < 0 || y < 0 || x >= p.w || y >= p.h) return 0;
	return p.p[y * p.bpr + x * 4 + 3] / 255.0;
}
// bilinear alpha at continuous coordinates (pixel i's centre is i+0.5)
static double alphaBi(Px p, double x, double y) {
	x -= 0.5, y -= 0.5;
	NSInteger x0 = (NSInteger)floor(x), y0 = (NSInteger)floor(y);
	double fx = x - x0, fy = y - y0;
	return alphaAt(p, x0, y0) * (1 - fx) * (1 - fy) + alphaAt(p, x0 + 1, y0) * fx * (1 - fy) + alphaAt(p, x0, y0 + 1) * (1 - fx) * fy +
	       alphaAt(p, x0 + 1, y0 + 1) * fx * fy;
}
static NSString *pixelHex(Px p, NSInteger x, NSInteger y) {
	const uint8_t *q = p.p + y * p.bpr + x * 4;
	double a = q[3] / 255.0;
	if (a <= 0) return @"#00000000";
	return [NSString stringWithFormat:@"#%02X%02X%02X%02X", (int)lround(fmin(q[0] / a, 255)), (int)lround(fmin(q[1] / a, 255)),
	                                  (int)lround(fmin(q[2] / a, 255)), q[3]];
}

// edgeScan walks from (x,y) by (dx,dy) pixels until the alpha reaches level and
// returns the crossing in continuous coordinates along the walk, or NAN.
static double edgeScan(Px p, double x, double y, double dx, double dy, double level, int steps) {
	double prev = alphaBi(p, x, y);
	for (int i = 1; i <= steps; i++) {
		double a = alphaBi(p, x + dx * i * 0.25, y + dy * i * 0.25);
		if (a >= level && prev < level) {
			double f = (level - prev) / (a - prev);
			return (i - 1 + f) * 0.25;
		}
		prev = a;
	}
	return NAN;
}

// measureShape measures the opaque-ish body of a control drawn over a
// transparent background: its 50 % edges along the centre lines and the
// corner radius along the top-left and top-right diagonals. r is the control
// frame in pixels (top-left origin). Results are in points.
static NSDictionary *measureShape(NSBitmapImageRep *rep, NSRect r, double probeInset) {
	Px p = pxOf(rep);
	double cy = NSMidY(r), cx = NSMidX(r);
	// plateau: the body's alpha just inside the left edge on the centre row
	double best = 0;
	for (double x = r.origin.x; x < r.origin.x + fmin(r.size.width, probeInset * 2); x += 1) best = fmax(best, alphaBi(p, x, cy));
	if (best < 0.03) return @{@"note" : @"no body found"};
	double lvl = best / 2;
	double sx = r.origin.x - 12, sy = r.origin.y - 12;
	double L = edgeScan(p, sx, cy, 1, 0, lvl, (int)((cx - sx) * 4));
	double T = edgeScan(p, cx, sy, 0, 1, lvl, (int)((cy - sy) * 4));
	double R = edgeScan(p, NSMaxX(r) + 12, cy, -1, 0, lvl, (int)((NSMaxX(r) + 12 - cx) * 4));
	double B = edgeScan(p, cx, NSMaxY(r) + 12, 0, -1, lvl, (int)((NSMaxY(r) + 12 - cy) * 4));
	if (isnan(L) || isnan(T)) return @{@"note" : @"edges not found"};
	double left = sx + L, top = sy + T, right = NSMaxX(r) + 12 - R, bottom = NSMaxY(r) + 12 - B;
	double k = 1 - 1 / sqrt(2);
	double dtl = edgeScan(p, left, top, 1, 1, lvl, 400);
	double dtr = edgeScan(p, right, top, -1, 1, lvl, 400);
	NSMutableDictionary *d = [@{
		@"bodyAlpha" : num(best),
		@"bodyColor" : pixelHex(p, (NSInteger)(left + 3), (NSInteger)cy),
		@"edges" : @{
			@"left" : num((left - r.origin.x) / 2),
			@"top" : num((top - r.origin.y) / 2),
			@"right" : num((right - NSMaxX(r)) / 2),
			@"bottom" : num((bottom - NSMaxY(r)) / 2)
		},
		@"bodySize" : @{@"w" : num((right - left) / 2), @"h" : num((bottom - top) / 2)},
	} mutableCopy];
	if (!isnan(dtl)) d[@"radiusTL"] = num(dtl / k / 2);
	if (!isnan(dtr)) d[@"radiusTR"] = num(dtr / k / 2);
	return d;
}

#pragma mark - Samples

typedef NSView * (^Builder)(void);

@interface Sample : NSObject
@property(copy) NSString *name, *group, *note;
@property(copy) Builder build;
@property(copy) void (^after)(NSView *v, NSWindow *w);
@property BOOL focus, inactive, pressed, noShape;
@property NSSize size; // the view's frame size; zero: fitting size
@property double probe; // how far in from the left the body is probed, px
@end
@implementation Sample
@end

static NSMutableArray<Sample *> *samples;

static Sample *add(NSString *group, NSString *name, Builder b) {
	Sample *s = [Sample new];
	s.group = group, s.name = name, s.build = b, s.probe = 8;
	[samples addObject:s];
	return s;
}

static NSButton *pushButton(NSString *title, NSControlSize size) {
	NSButton *b = [NSButton buttonWithTitle:title target:nil action:nil];
	b.controlSize = size;
	b.font = [NSFont systemFontOfSize:[NSFont systemFontSizeForControlSize:size]];
	return b;
}

static NSImage *sym(NSString *n) { return [NSImage imageWithSystemSymbolName:n accessibilityDescription:nil]; }

// --- table data

// ARRowView is a table row view whose selection can be held emphasised: -live
// shows one key window whose first responder is nil, so an "-emphasized"
// sample keeps the look its table has as the first responder.
@interface ARRowView : NSTableRowView
@property BOOL forceEmph;
@end
@implementation ARRowView
- (void)setEmphasized:(BOOL)e { [super setEmphasized:e || self.forceEmph]; }
@end

@interface TableData : NSObject <NSTableViewDataSource, NSTableViewDelegate>
@property BOOL emph; // the rows' selection stays emphasised (ARRowView)
@end
@implementation TableData
- (NSTableRowView *)tableView:(NSTableView *)tv rowViewForRow:(NSInteger)row {
	ARRowView *v = [ARRowView new];
	v.forceEmph = self.emph;
	return v;
}
- (NSInteger)numberOfRowsInTableView:(NSTableView *)tv { return 14; }
- (NSView *)tableView:(NSTableView *)tv viewForTableColumn:(NSTableColumn *)col row:(NSInteger)row {
	NSTableCellView *cv = [NSTableCellView new];
	NSArray *names = @[ @"Annual Report.pdf", @"Budget.numbers", @"Notes.txt", @"Photos", @"Presentation.key", @"README.md", @"Screenshot.png" ];
	NSArray *kinds = @[ @"PDF Document", @"Numbers", @"Plain Text", @"Folder", @"Keynote", @"Markdown", @"PNG image" ];
	NSString *s;
	if ([col.identifier isEqual:@"name"]) s = names[row % names.count];
	else if ([col.identifier isEqual:@"kind"]) s = kinds[row % kinds.count];
	else s = [NSString stringWithFormat:@"%ld KB", (long)(row * 37 + 12)];
	NSTextField *t = [NSTextField labelWithString:s];
	t.lineBreakMode = NSLineBreakByTruncatingTail;
	if ([col.identifier isEqual:@"size"]) t.alignment = NSTextAlignmentRight;
	t.translatesAutoresizingMaskIntoConstraints = NO;
	[cv addSubview:t];
	cv.textField = t;
	[NSLayoutConstraint activateConstraints:@[
		[t.leadingAnchor constraintEqualToAnchor:cv.leadingAnchor constant:2], [t.trailingAnchor constraintEqualToAnchor:cv.trailingAnchor constant:-2],
		[t.centerYAnchor constraintEqualToAnchor:cv.centerYAnchor]
	]];
	return cv;
}
@end

// --- sidebar data
@interface SideItem : NSObject
@property(copy) NSString *title, *symbol;
@property NSArray *kids;
@end
@implementation SideItem
@end

@interface SideData : NSObject <NSOutlineViewDataSource, NSOutlineViewDelegate>
@property NSArray *roots;
@end
@implementation SideData
- (instancetype)init {
	self = [super init];
	SideItem * (^it)(NSString *, NSString *, NSArray *) = ^(NSString *t, NSString *s, NSArray *k) {
		SideItem *i = [SideItem new];
		i.title = t, i.symbol = s, i.kids = k;
		return i;
	};
	self.roots = @[
		it(@"Favorites", nil,
		   @[ it(@"Recents", @"clock", nil), it(@"Applications", @"square.grid.2x2", nil), it(@"Desktop", @"menubar.dock.rectangle", nil),
			  it(@"Documents", @"doc", @[ it(@"Projects", @"folder", nil), it(@"Invoices", @"folder", nil) ]), it(@"Downloads", @"arrow.down.circle", nil) ]),
		it(@"Locations", nil, @[ it(@"Macintosh HD", @"internaldrive", nil), it(@"Network", @"network", nil) ]),
	];
	return self;
}
- (NSInteger)outlineView:(NSOutlineView *)ov numberOfChildrenOfItem:(SideItem *)item { return item ? item.kids.count : self.roots.count; }
- (id)outlineView:(NSOutlineView *)ov child:(NSInteger)i ofItem:(SideItem *)item { return item ? item.kids[i] : self.roots[i]; }
- (BOOL)outlineView:(NSOutlineView *)ov isItemExpandable:(SideItem *)item { return item.kids.count > 0; }
- (BOOL)outlineView:(NSOutlineView *)ov isGroupItem:(SideItem *)item { return item.symbol == nil; }
- (BOOL)outlineView:(NSOutlineView *)ov shouldSelectItem:(SideItem *)item { return item.symbol != nil; }
- (NSView *)outlineView:(NSOutlineView *)ov viewForTableColumn:(NSTableColumn *)col item:(SideItem *)item {
	NSTableCellView *cv = [NSTableCellView new];
	NSTextField *t = [NSTextField labelWithString:item.title];
	t.translatesAutoresizingMaskIntoConstraints = NO;
	[cv addSubview:t];
	cv.textField = t;
	if (item.symbol) {
		NSImageView *iv = [NSImageView imageViewWithImage:sym(item.symbol)];
		iv.translatesAutoresizingMaskIntoConstraints = NO;
		[cv addSubview:iv];
		cv.imageView = iv;
		[NSLayoutConstraint activateConstraints:@[
			[iv.leadingAnchor constraintEqualToAnchor:cv.leadingAnchor constant:3], [iv.centerYAnchor constraintEqualToAnchor:cv.centerYAnchor],
			[t.leadingAnchor constraintEqualToAnchor:iv.trailingAnchor constant:6], [t.centerYAnchor constraintEqualToAnchor:cv.centerYAnchor]
		]];
	} else {
		[NSLayoutConstraint activateConstraints:@[
			[t.leadingAnchor constraintEqualToAnchor:cv.leadingAnchor constant:2], [t.centerYAnchor constraintEqualToAnchor:cv.centerYAnchor]
		]];
	}
	return cv;
}
@end

// --- outline data: a file tree, every item a row (no group items)
@interface TreeData : NSObject <NSOutlineViewDataSource, NSOutlineViewDelegate>
@property NSArray *roots;
@property BOOL emph; // the rows' selection stays emphasised (ARRowView)
@end
@implementation TreeData
- (instancetype)init {
	self = [super init];
	SideItem * (^it)(NSString *, NSString *, NSArray *) = ^(NSString *t, NSString *s, NSArray *k) {
		SideItem *i = [SideItem new];
		i.title = t, i.symbol = s, i.kids = k;
		return i;
	};
	self.roots = @[
		it(@"Documents", @"folder",
		   @[ it(@"Projects", @"folder", @[ it(@"Report.pdf", @"doc", nil), it(@"Budget.numbers", @"doc", nil) ]),
			  it(@"Invoices", @"folder", @[ it(@"March.pdf", @"doc", nil) ]), it(@"Notes.txt", @"doc", nil) ]),
		it(@"Downloads", @"folder", @[ it(@"Installer.dmg", @"doc", nil) ]), it(@"Music", @"folder", @[ it(@"Song.m4a", @"doc", nil) ]),
		it(@"Pictures", @"folder", @[ it(@"Photo.heic", @"doc", nil) ]), it(@"README.md", @"doc", nil), it(@"Screenshot.png", @"doc", nil),
		it(@"Todo.txt", @"doc", nil),
	];
	return self;
}
- (NSTableRowView *)outlineView:(NSOutlineView *)ov rowViewForItem:(id)item {
	ARRowView *v = [ARRowView new];
	v.forceEmph = self.emph;
	return v;
}
- (NSInteger)outlineView:(NSOutlineView *)ov numberOfChildrenOfItem:(SideItem *)item { return item ? item.kids.count : self.roots.count; }
- (id)outlineView:(NSOutlineView *)ov child:(NSInteger)i ofItem:(SideItem *)item { return item ? item.kids[i] : self.roots[i]; }
- (BOOL)outlineView:(NSOutlineView *)ov isItemExpandable:(SideItem *)item { return item.kids.count > 0; }
- (NSView *)outlineView:(NSOutlineView *)ov viewForTableColumn:(NSTableColumn *)col item:(SideItem *)item {
	NSTableCellView *cv = [NSTableCellView new];
	NSTextField *t = [NSTextField labelWithString:item.title];
	t.lineBreakMode = NSLineBreakByTruncatingTail;
	t.translatesAutoresizingMaskIntoConstraints = NO;
	[cv addSubview:t];
	cv.textField = t;
	[NSLayoutConstraint activateConstraints:@[
		[t.leadingAnchor constraintEqualToAnchor:cv.leadingAnchor constant:2], [t.trailingAnchor constraintEqualToAnchor:cv.trailingAnchor constant:-2],
		[t.centerYAnchor constraintEqualToAnchor:cv.centerYAnchor]
	]];
	return cv;
}
@end

// --- toolbar
@interface TBDelegate : NSObject <NSToolbarDelegate>
@end
@implementation TBDelegate
- (NSArray *)toolbarDefaultItemIdentifiers:(NSToolbar *)tb { return @[ @"a", NSToolbarFlexibleSpaceItemIdentifier, @"b" ]; }
- (NSArray *)toolbarAllowedItemIdentifiers:(NSToolbar *)tb { return [self toolbarDefaultItemIdentifiers:tb]; }
- (NSToolbarItem *)toolbar:(NSToolbar *)tb itemForItemIdentifier:(NSString *)ident willBeInsertedIntoToolbar:(BOOL)f {
	NSToolbarItem *i = [[NSToolbarItem alloc] initWithItemIdentifier:ident];
	i.image = sym([ident isEqual:@"a"] ? @"sidebar.left" : @"square.and.arrow.up");
	i.label = ident;
	return i;
}
@end

// listScrollers is the scroller style of the lists' scroll views: overlay in
// the offscreen renders (AppKit's at rest, hidden), the system's in -live.
static BOOL gLiveLists;
static BOOL gFlash; // -live -flash: overlay scrollers are flashed again and again, so that a capture shows their knobs
static void flashScrollViews(NSView *v) {
	if ([v isKindOfClass:NSScrollView.class]) [(NSScrollView *)v flashScrollers];
	for (NSView *sub in v.subviews) flashScrollViews(sub);
}
static NSScrollerStyle listScrollers(void) { return gLiveLists ? NSScroller.preferredScrollerStyle : NSScrollerStyleOverlay; }

static NSScrollView *tableIn(NSTableViewStyle style, BOOL alternating, NSSize size, BOOL emph, BOOL fit) {
	NSScrollView *sv = [[NSScrollView alloc] initWithFrame:(NSRect){NSZeroPoint, size}];
	NSTableView *tv = [[NSTableView alloc] initWithFrame:sv.bounds];
	tv.style = style;
	tv.usesAlternatingRowBackgroundColors = alternating;
	tv.allowsMultipleSelection = YES;
	NSArray *cols = @[ @[ @"name", @"Name", @180 ], @[ @"kind", @"Kind", @120 ], @[ @"size", @"Size", @70 ] ];
	if (fit) { // narrower columns: an inset table as wide as its view
		cols = @[ @[ @"name", @"Name", @160 ], @[ @"kind", @"Kind", @110 ], @[ @"size", @"Size", @60 ] ];
		tv.columnAutoresizingStyle = NSTableViewLastColumnOnlyAutoresizingStyle;
	}
	for (NSArray *c in cols) {
		NSTableColumn *col = [[NSTableColumn alloc] initWithIdentifier:c[0]];
		col.title = c[1];
		col.width = [c[2] doubleValue];
		if ([c[0] isEqual:@"size"]) col.headerCell.alignment = NSTextAlignmentRight;
		[tv addTableColumn:col];
	}
	tv.sortDescriptors = @[];
	[tv setIndicatorImage:[NSImage imageNamed:@"NSAscendingSortIndicator"] inTableColumn:tv.tableColumns[0]];
	TableData *td = [TableData new];
	td.emph = emph;
	[keep addObject:td];
	tv.dataSource = td;
	tv.delegate = td;
	sv.documentView = tv;
	sv.hasVerticalScroller = YES;
	sv.scrollerStyle = listScrollers();
	sv.borderType = NSBezelBorder;
	[tv reloadData];
	[tv selectRowIndexes:[NSIndexSet indexSetWithIndexesInRange:NSMakeRange(2, 2)] byExtendingSelection:NO];
	return sv;
}

static NSScrollView *sidebarIn(NSSize size, NSTableViewRowSizeStyle rs) {
	NSScrollView *sv = [[NSScrollView alloc] initWithFrame:(NSRect){NSZeroPoint, size}];
	NSOutlineView *ov = [[NSOutlineView alloc] initWithFrame:sv.bounds];
	ov.style = NSTableViewStyleSourceList;
	ov.rowSizeStyle = rs;
	ov.headerView = nil;
	ov.floatsGroupRows = NO;
	NSTableColumn *col = [[NSTableColumn alloc] initWithIdentifier:@"c"];
	col.width = size.width;
	[ov addTableColumn:col];
	ov.outlineTableColumn = col;
	SideData *sd = [SideData new];
	[keep addObject:sd];
	ov.dataSource = sd;
	ov.delegate = sd;
	sv.documentView = ov;
	sv.drawsBackground = NO;
	[ov reloadData];
	[ov expandItem:nil expandChildren:YES];
	[ov selectRowIndexes:[NSIndexSet indexSetWithIndex:4] byExtendingSelection:NO]; // Documents
	return sv;
}

// outlineIn is a file tree in an NSOutlineView of the given style, without a
// header: Documents and its Projects expanded, Invoices and the other folders
// collapsed, Projects selected.
static NSScrollView *outlineIn(NSTableViewStyle style, BOOL alternating, NSSize size, BOOL emph) {
	NSScrollView *sv = [[NSScrollView alloc] initWithFrame:(NSRect){NSZeroPoint, size}];
	NSOutlineView *ov = [[NSOutlineView alloc] initWithFrame:sv.bounds];
	ov.style = style;
	ov.usesAlternatingRowBackgroundColors = alternating;
	ov.headerView = nil;
	ov.allowsMultipleSelection = YES;
	NSTableColumn *col = [[NSTableColumn alloc] initWithIdentifier:@"name"];
	// narrow, then grown to as wide as the view leaves it: an inset outline
	// wider than its view draws a full-width selection
	col.width = 120;
	ov.columnAutoresizingStyle = NSTableViewLastColumnOnlyAutoresizingStyle;
	ov.autoresizesOutlineColumn = NO; // else expanding widens it past the view
	[ov addTableColumn:col];
	ov.outlineTableColumn = col;
	TreeData *td = [TreeData new];
	td.emph = emph;
	[keep addObject:td];
	ov.dataSource = td;
	ov.delegate = td;
	sv.documentView = ov;
	sv.hasVerticalScroller = YES;
	sv.scrollerStyle = listScrollers();
	sv.borderType = NSBezelBorder;
	[ov reloadData];
	[ov sizeLastColumnToFit];
	[ov expandItem:td.roots[0]];
	[ov expandItem:[td.roots[0] kids][0]];
	[ov selectRowIndexes:[NSIndexSet indexSetWithIndex:1] byExtendingSelection:NO]; // Projects
	return sv;
}

static NSScrollView *textScroll(NSScrollerStyle st, NSSize size, BOOL lines) {
	NSScrollView *sv = [[NSScrollView alloc] initWithFrame:(NSRect){NSZeroPoint, size}];
	sv.scrollerStyle = st;
	sv.hasVerticalScroller = YES;
	sv.hasHorizontalScroller = st == NSScrollerStyleLegacy;
	sv.borderType = NSBezelBorder;
	NSTextView *tv = [[NSTextView alloc] initWithFrame:(NSRect){NSZeroPoint, {size.width, size.height}}];
	tv.minSize = NSMakeSize(0, size.height);
	tv.maxSize = NSMakeSize(1e7, 1e7);
	tv.verticallyResizable = YES;
	tv.autoresizingMask = NSViewWidthSizable;
	tv.textContainer.widthTracksTextView = YES;
	tv.font = [NSFont systemFontOfSize:NSFont.systemFontSize];
	NSMutableString *s = [NSMutableString string];
	int n = lines ? 30 : 6;
	for (int i = 0; i < n; i++)
		[s appendFormat:@"Line %d of a multi-line text view. Native text editing brings spell check, dictation and the emoji picker.\n", i + 1];
	tv.string = s;
	tv.selectedRange = NSMakeRange(10, 40);
	sv.documentView = tv;
	return sv;
}

#pragma mark - D3b: forms and sheets

// layouts are the frames of a sample's named subviews, in points from the
// sample's top left, written to metrics.json as "layouts"; rexui's form test
// compares our rows with them. sheets holds the sheet-style panel's numbers.
static NSMutableDictionary *d3bLayouts;

static void d3bRecord(NSString *sample, NSView *root, NSDictionary<NSString *, NSView *> *named) {
	if (!d3bLayouts) d3bLayouts = [NSMutableDictionary dictionary];
	[root layoutSubtreeIfNeeded];
	NSMutableDictionary *out = [NSMutableDictionary dictionary];
	for (NSString *k in named) {
		NSView *v = named[k];
		NSRect r = [root convertRect:v.bounds fromView:v];
		CGFloat y = root.isFlipped ? r.origin.y : root.bounds.size.height - NSMaxY(r);
		out[k] = @{@"x" : num(r.origin.x), @"y" : num(y), @"w" : num(r.size.width), @"h" : num(r.size.height)};
	}
	d3bLayouts[sample] = out;
}

// a settings row as tools/appkitscenes lays it out (liquid-glass-reference.md):
// 40 pt at least, the label 10 pt from the leading edge, the control 10 from
// the trailing one, 12 pt at least between them.
static NSView *d3bFormRow(NSString *title, NSView *control, NSTextField **labelOut) {
	NSView *row = [NSView new];
	NSTextField *t = [NSTextField labelWithString:title];
	t.translatesAutoresizingMaskIntoConstraints = NO;
	[row addSubview:t];
	control.translatesAutoresizingMaskIntoConstraints = NO;
	[row addSubview:control];
	[NSLayoutConstraint activateConstraints:@[
		[row.heightAnchor constraintGreaterThanOrEqualToConstant:40], [t.leadingAnchor constraintEqualToAnchor:row.leadingAnchor constant:10],
		[t.centerYAnchor constraintEqualToAnchor:row.centerYAnchor], [control.trailingAnchor constraintEqualToAnchor:row.trailingAnchor constant:-10],
		[control.centerYAnchor constraintEqualToAnchor:row.centerYAnchor], [t.trailingAnchor constraintLessThanOrEqualToAnchor:control.leadingAnchor constant:-12]
	]];
	if (labelOut) *labelOut = t;
	return row;
}

static void defineFormSamples(void) {
	// a grouped form section: an NSBox (primary, no title) of rows with NSBox
	// separators, as System Settings draws it
	Sample *fg = add(@"form", @"form-group", ^{
		NSBox *box = [NSBox new];
		box.boxType = NSBoxPrimary;
		box.titlePosition = NSNoTitle;
		box.contentViewMargins = NSZeroSize;
		NSStackView *st = [NSStackView new];
		st.orientation = NSUserInterfaceLayoutOrientationVertical;
		st.spacing = 0;
		st.alignment = NSLayoutAttributeWidth;
		NSPopUpButton *pop = [[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:NO];
		[pop addItemsWithTitles:@[ @"Auto", @"Light", @"Dark" ]];
		NSSwitch *sw = [NSSwitch new];
		sw.state = NSControlStateValueOn;
		NSSlider *sl = [NSSlider sliderWithValue:0.4 minValue:0 maxValue:1 target:nil action:nil];
		[sl.widthAnchor constraintEqualToConstant:120].active = YES;
		NSTextField *l0, *l1, *l2;
		NSView *r0 = d3bFormRow(@"Appearance", pop, &l0), *r1 = d3bFormRow(@"Wi-Fi", sw, &l1), *r2 = d3bFormRow(@"Brightness", sl, &l2);
		NSMutableArray *seps = [NSMutableArray array];
		NSArray *rows = @[ r0, r1, r2 ];
		for (NSUInteger i = 0; i < rows.count; i++) {
			if (i > 0) {
				NSBox *sep = [NSBox new];
				sep.boxType = NSBoxSeparator;
				[st addArrangedSubview:sep];
				[seps addObject:sep];
			}
			[st addArrangedSubview:rows[i]];
		}
		box.contentView = st;
		[box.widthAnchor constraintEqualToConstant:360].active = YES;
		keep = keep ?: [NSMutableArray array];
		box.identifier = @"form-group";
		// the frames are read once the sample is laid out (renderSample calls after)
		d3bLayouts = d3bLayouts ?: [NSMutableDictionary dictionary];
		objc_setAssociatedObject(box, "d3b", @{@"rows" : rows, @"labels" : @[ l0, l1, l2 ], @"controls" : @[ pop, sw, sl ], @"seps" : seps}, OBJC_ASSOCIATION_RETAIN);
		return box;
	});
	fg.size = NSMakeSize(360, 0);
	fg.after = ^(NSView *v, NSWindow *w) {
		NSDictionary *d = objc_getAssociatedObject(v, "d3b");
		NSMutableDictionary *named = [NSMutableDictionary dictionary];
		[v setFrameSize:NSMakeSize(360, v.fittingSize.height)];
		[v layoutSubtreeIfNeeded];
		for (NSUInteger i = 0; i < 3; i++) {
			named[[NSString stringWithFormat:@"row%lu", (unsigned long)i]] = d[@"rows"][i];
			named[[NSString stringWithFormat:@"label%lu", (unsigned long)i]] = d[@"labels"][i];
			named[[NSString stringWithFormat:@"control%lu", (unsigned long)i]] = d[@"controls"][i];
		}
		NSArray *seps = d[@"seps"];
		for (NSUInteger i = 0; i < seps.count; i++) named[[NSString stringWithFormat:@"separator%lu", (unsigned long)i]] = [seps[i] subviews].firstObject ?: seps[i];
		named[@"box"] = v;
		d3bRecord(@"form-group", v, named);
	};

	// a two-column form as NSGridView lays it out: the labels trailing-aligned
	// in a column of their own, the controls leading-aligned, with the grid's
	// default spacing
	Sample *gf = add(@"form", @"form-grid", ^{
		NSTextField *name = [NSTextField textFieldWithString:@""], *email = [NSTextField textFieldWithString:@""];
		for (NSTextField *f in @[ name, email ]) [f.widthAnchor constraintEqualToConstant:200].active = YES;
		NSButton *chk = [NSButton checkboxWithTitle:@"Send me updates" target:nil action:nil];
		NSTextField *ln = [NSTextField labelWithString:@"Name:"], *le = [NSTextField labelWithString:@"Email address:"];
		NSGridView *gv = [NSGridView gridViewWithViews:@[ @[ ln, name ], @[ le, email ], @[ [NSTextField labelWithString:@"Notifications:"], chk ] ]];
		gv.columnSpacing = gv.columnSpacing; // keep the grid's default
		[gv columnAtIndex:0].xPlacement = NSGridCellPlacementTrailing;
		[gv columnAtIndex:1].xPlacement = NSGridCellPlacementLeading;
		gv.rowAlignment = NSGridRowAlignmentFirstBaseline;
		objc_setAssociatedObject(gv, "d3b", @{@"cells" : @[ ln, name, le, email, [gv cellAtColumnIndex:0 rowIndex:2].contentView, chk ]}, OBJC_ASSOCIATION_RETAIN);
		return gv;
	});
	gf.noShape = YES;
	gf.after = ^(NSView *v, NSWindow *w) {
		NSGridView *gv = (NSGridView *)v;
		[v setFrameSize:v.fittingSize];
		[v layoutSubtreeIfNeeded];
		NSArray *c = objc_getAssociatedObject(v, "d3b")[@"cells"];
		d3bRecord(@"form-grid", v, @{@"label0" : c[0], @"field0" : c[1], @"label1" : c[2], @"field1" : c[3], @"label2" : c[4], @"check2" : c[5], @"grid" : v});
		d3bLayouts[@"form-grid"] = [d3bLayouts[@"form-grid"] mutableCopy];
		((NSMutableDictionary *)d3bLayouts[@"form-grid"])[@"spacing"] = @{@"row" : num(gv.rowSpacing), @"column" : num(gv.columnSpacing)};
	};
}

// sheetTable records what offscreen AppKit says of sheets: the corner radius
// of an alert's panel (a glass content frame) and of a plain titled window
// attached as a sheet, and the alert's frame. Windows are never shown.
static NSDictionary *sheetTable(void) {
	SEL cr = NSSelectorFromString(@"_cornerRadius");
	CGFloat (*rad)(id, SEL) = (void *)[NSWindow instanceMethodForSelector:cr];
	NSMutableDictionary *out = [NSMutableDictionary dictionary];
	NSAlert *a = [NSAlert new];
	a.messageText = @"Sheet";
	[a addButtonWithTitle:@"OK"];
	[a layout];
	out[@"alertPanelCornerRadius"] = num(rad(a.window, cr));
	out[@"alertPanelClass"] = NSStringFromClass(a.window.class);
	out[@"alertPanelContentFrameClass"] = NSStringFromClass(a.window.contentView.superview.class);
	NSPanel *plain = [[NSPanel alloc] initWithContentRect:NSMakeRect(0, 0, 420, 200) styleMask:NSWindowStyleMaskTitled backing:NSBackingStoreBuffered defer:YES];
	out[@"plainTitledPanelCornerRadius"] = num(rad(plain, cr));
	[plain close];
	return out;
}

static void defineSamples(void) {
	samples = [NSMutableArray array];
	NSArray *sizes = @[ @[ @"mini", @(NSControlSizeMini) ], @[ @"small", @(NSControlSizeSmall) ], @[ @"regular", @(NSControlSizeRegular) ], @[ @"large", @(NSControlSizeLarge) ] ];
	NSMutableArray *sz = [sizes mutableCopy];
	if (@available(macOS 26.0, *)) [sz addObject:@[ @"xlarge", @(NSControlSizeExtraLarge) ]];
	sizes = sz;

	// push buttons
	for (NSArray *s in sizes) {
		NSControlSize cs = [s[1] unsignedIntegerValue];
		add(@"button", [@"push-" stringByAppendingString:s[0]], ^{
			return pushButton(@"Button", cs);
		});
	}
	add(@"button", @"push-default", ^{
		NSButton *b = pushButton(@"Default", NSControlSizeRegular);
		b.keyEquivalent = @"\r";
		return b;
	});
	add(@"button", @"push-default-small", ^{
		NSButton *b = pushButton(@"Default", NSControlSizeSmall);
		b.keyEquivalent = @"\r";
		return b;
	});
	add(@"button", @"push-default-inactive", ^{
		NSButton *b = pushButton(@"Default", NSControlSizeRegular);
		b.keyEquivalent = @"\r";
		return b;
	}).inactive = YES;
	add(@"button", @"push-pressed", ^{
		return pushButton(@"Button", NSControlSizeRegular);
	}).pressed = YES;
	add(@"button", @"push-default-pressed", ^{
		NSButton *b = pushButton(@"Default", NSControlSizeRegular);
		b.keyEquivalent = @"\r";
		return b;
	}).pressed = YES;
	add(@"button", @"push-disabled", ^{
		NSButton *b = pushButton(@"Button", NSControlSizeRegular);
		b.enabled = NO;
		return b;
	});
	add(@"button", @"push-default-disabled", ^{
		NSButton *b = pushButton(@"Default", NSControlSizeRegular);
		b.keyEquivalent = @"\r";
		b.enabled = NO;
		return b;
	});
	add(@"button", @"push-focused", ^{
		return pushButton(@"Button", NSControlSizeRegular);
	}).focus = YES;
	add(@"button", @"push-destructive", ^{
		NSButton *b = pushButton(@"Delete", NSControlSizeRegular);
		b.hasDestructiveAction = YES;
		return b;
	});
	if (@available(macOS 26.0, *)) {
		add(@"button", @"push-glass", ^{
			NSButton *b = pushButton(@"Glass", NSControlSizeRegular);
			b.bezelStyle = NSBezelStyleGlass;
			return b;
		}).note = @"Liquid Glass needs window-server compositing; offscreen shows its fallback";
		add(@"button", @"push-capsule", ^{
			NSButton *b = pushButton(@"Capsule", NSControlSizeRegular);
			b.borderShape = NSControlBorderShapeCapsule;
			return b;
		});
	}
	add(@"button", @"icon-rounded", ^{
		NSButton *b = [NSButton buttonWithImage:sym(@"plus") target:nil action:nil];
		return b;
	});
	add(@"button", @"icon-toolbar", ^{
		NSButton *b = [NSButton buttonWithImage:sym(@"sidebar.left") target:nil action:nil];
		b.bezelStyle = NSBezelStyleToolbar;
		return b;
	});
	add(@"button", @"icon-borderless", ^{
		NSButton *b = [NSButton buttonWithImage:sym(@"gearshape") target:nil action:nil];
		b.bordered = NO;
		return b;
	});
	add(@"button", @"icon-smallsquare", ^{
		NSButton *b = [NSButton buttonWithImage:sym(@"plus") target:nil action:nil];
		b.bezelStyle = NSBezelStyleSmallSquare;
		return b;
	});
	add(@"button", @"accessorybar", ^{
		NSButton *b = [NSButton buttonWithTitle:@"All" target:nil action:nil];
		b.bezelStyle = NSBezelStyleAccessoryBar;
		b.buttonType = NSButtonTypePushOnPushOff;
		b.state = NSControlStateValueOn;
		return b;
	});
	add(@"button", @"help", ^{
		NSButton *b = [NSButton buttonWithTitle:@"" target:nil action:nil];
		b.bezelStyle = NSBezelStyleHelpButton;
		return b;
	});
	add(@"button", @"disclosure-closed", ^{
		NSButton *b = [NSButton buttonWithTitle:@"" target:nil action:nil];
		b.bezelStyle = NSBezelStyleDisclosure;
		b.buttonType = NSButtonTypePushOnPushOff;
		b.state = NSControlStateValueOff;
		return b;
	});
	add(@"button", @"disclosure-open", ^{
		NSButton *b = [NSButton buttonWithTitle:@"" target:nil action:nil];
		b.bezelStyle = NSBezelStyleDisclosure;
		b.buttonType = NSButtonTypePushOnPushOff;
		b.state = NSControlStateValueOn;
		return b;
	});
	add(@"button", @"push-disclosure", ^{
		NSButton *b = [NSButton buttonWithTitle:@"" target:nil action:nil];
		b.bezelStyle = NSBezelStylePushDisclosure;
		b.buttonType = NSButtonTypePushOnPushOff;
		return b;
	});

	// choices
	struct {
		NSString *n;
		NSControlStateValue v;
		BOOL en, focus, pressed;
		NSControlSize cs;
	} checks[] = {
		{@"checkbox-off", NSControlStateValueOff, YES, NO, NO, NSControlSizeRegular},
		{@"checkbox-on", NSControlStateValueOn, YES, NO, NO, NSControlSizeRegular},
		{@"checkbox-mixed", NSControlStateValueMixed, YES, NO, NO, NSControlSizeRegular},
		{@"checkbox-on-disabled", NSControlStateValueOn, NO, NO, NO, NSControlSizeRegular},
		{@"checkbox-off-disabled", NSControlStateValueOff, NO, NO, NO, NSControlSizeRegular},
		{@"checkbox-off-pressed", NSControlStateValueOff, YES, NO, YES, NSControlSizeRegular},
		{@"checkbox-on-focused", NSControlStateValueOn, YES, YES, NO, NSControlSizeRegular},
		{@"checkbox-on-small", NSControlStateValueOn, YES, NO, NO, NSControlSizeSmall},
		{@"checkbox-on-large", NSControlStateValueOn, YES, NO, NO, NSControlSizeLarge},
		{@"checkbox-on-inactive", NSControlStateValueOn, YES, NO, NO, NSControlSizeRegular},
		{@"checkbox-mixed-inactive", NSControlStateValueMixed, YES, NO, NO, NSControlSizeRegular},
		{@"checkbox-off-inactive", NSControlStateValueOff, YES, NO, NO, NSControlSizeRegular},
	};
	for (size_t i = 0; i < sizeof checks / sizeof checks[0]; i++) {
		__auto_type c = checks[i];
		Sample *s = add(@"choice", c.n, ^{
			NSButton *b = [NSButton checkboxWithTitle:@"Check" target:nil action:nil];
			b.allowsMixedState = YES;
			b.controlSize = c.cs;
			b.font = [NSFont systemFontOfSize:[NSFont systemFontSizeForControlSize:c.cs]];
			b.state = c.v;
			b.enabled = c.en;
			return b;
		});
		s.focus = c.focus, s.pressed = c.pressed, s.probe = 4;
		s.inactive = [c.n hasSuffix:@"-inactive"];
	}
	struct {
		NSString *n;
		NSControlStateValue v;
		BOOL en, focus;
	} radios[] = {
		{@"radio-off", NSControlStateValueOff, YES, NO},
		{@"radio-on", NSControlStateValueOn, YES, NO},
		{@"radio-on-disabled", NSControlStateValueOn, NO, NO},
		{@"radio-on-focused", NSControlStateValueOn, YES, YES},
		{@"radio-on-inactive", NSControlStateValueOn, YES, NO},
	};
	for (size_t i = 0; i < sizeof radios / sizeof radios[0]; i++) {
		__auto_type c = radios[i];
		Sample *s = add(@"choice", c.n, ^{
			NSButton *b = [NSButton radioButtonWithTitle:@"Radio" target:nil action:nil];
			b.state = c.v;
			b.enabled = c.en;
			return b;
		});
		s.focus = c.focus, s.probe = 4;
		s.inactive = [c.n hasSuffix:@"-inactive"];
	}
	struct {
		NSString *n;
		NSControlStateValue v;
		BOOL en, focus;
		NSControlSize cs;
	} switches[] = {
		{@"switch-off", NSControlStateValueOff, YES, NO, NSControlSizeRegular},
		{@"switch-on", NSControlStateValueOn, YES, NO, NSControlSizeRegular},
		{@"switch-on-disabled", NSControlStateValueOn, NO, NO, NSControlSizeRegular},
		{@"switch-on-focused", NSControlStateValueOn, YES, YES, NSControlSizeRegular},
		{@"switch-on-small", NSControlStateValueOn, YES, NO, NSControlSizeSmall},
		{@"switch-on-mini", NSControlStateValueOn, YES, NO, NSControlSizeMini},
		{@"switch-on-large", NSControlStateValueOn, YES, NO, NSControlSizeLarge},
		{@"switch-on-inactive", NSControlStateValueOn, YES, NO, NSControlSizeRegular},
		{@"switch-off-inactive", NSControlStateValueOff, YES, NO, NSControlSizeRegular},
	};
	for (size_t i = 0; i < sizeof switches / sizeof switches[0]; i++) {
		__auto_type c = switches[i];
		Sample *s = add(@"choice", c.n, ^{
			NSSwitch *w = [NSSwitch new];
			w.controlSize = c.cs;
			w.state = c.v;
			w.enabled = c.en;
			return w;
		});
		s.focus = c.focus;
		s.inactive = [c.n hasSuffix:@"-inactive"];
	}

	// menus and selection
	add(@"select", @"popup", ^{
		NSPopUpButton *p = [[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:NO];
		[p addItemsWithTitles:@[ @"Choice", @"Other option" ]];
		return p;
	});
	add(@"select", @"popup-small", ^{
		NSPopUpButton *p = [[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:NO];
		p.controlSize = NSControlSizeSmall;
		p.font = [NSFont systemFontOfSize:NSFont.smallSystemFontSize];
		[p addItemsWithTitles:@[ @"Choice", @"Other option" ]];
		return p;
	});
	add(@"select", @"popup-disabled", ^{
		NSPopUpButton *p = [[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:NO];
		[p addItemsWithTitles:@[ @"Choice", @"Other option" ]];
		p.enabled = NO;
		return p;
	});
	add(@"select", @"popup-focused", ^{
		NSPopUpButton *p = [[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:NO];
		[p addItemsWithTitles:@[ @"Choice", @"Other option" ]];
		return p;
	}).focus = YES;
	add(@"select", @"pulldown", ^{
		NSPopUpButton *p = [[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:YES];
		[p addItemsWithTitles:@[ @"Actions", @"One", @"Two" ]];
		return p;
	});
	add(@"select", @"popup-borderless", ^{
		NSPopUpButton *p = [[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:NO];
		p.bordered = NO;
		[p addItemsWithTitles:@[ @"Choice", @"Other option" ]];
		return p;
	});
	add(@"select", @"segmented", ^{
		NSSegmentedControl *s = [NSSegmentedControl segmentedControlWithLabels:@[ @"One", @"Two", @"Three" ] trackingMode:NSSegmentSwitchTrackingSelectOne target:nil action:nil];
		s.selectedSegment = 0;
		return s;
	});
	add(@"select", @"segmented-inactive", ^{
		NSSegmentedControl *s = [NSSegmentedControl segmentedControlWithLabels:@[ @"One", @"Two", @"Three" ] trackingMode:NSSegmentSwitchTrackingSelectOne target:nil action:nil];
		s.selectedSegment = 0;
		return s;
	}).inactive = YES;
	add(@"select", @"segmented-small", ^{
		NSSegmentedControl *s = [NSSegmentedControl segmentedControlWithLabels:@[ @"One", @"Two", @"Three" ] trackingMode:NSSegmentSwitchTrackingSelectOne target:nil action:nil];
		s.controlSize = NSControlSizeSmall;
		s.font = [NSFont systemFontOfSize:NSFont.smallSystemFontSize];
		s.selectedSegment = 1;
		return s;
	});
	add(@"select", @"segmented-icons", ^{
		NSSegmentedControl *s = [NSSegmentedControl segmentedControlWithImages:@[ sym(@"list.bullet"), sym(@"square.grid.2x2"), sym(@"rectangle.grid.1x2") ]
		                                                          trackingMode:NSSegmentSwitchTrackingSelectOne
		                                                                target:nil
		                                                                action:nil];
		s.selectedSegment = 0;
		return s;
	});
	add(@"select", @"segmented-separated", ^{
		NSSegmentedControl *s = [NSSegmentedControl segmentedControlWithImages:@[ sym(@"chevron.left"), sym(@"chevron.right") ]
		                                                          trackingMode:NSSegmentSwitchTrackingMomentary
		                                                                target:nil
		                                                                action:nil];
		s.segmentStyle = NSSegmentStyleSeparated;
		return s;
	});
	struct {
		NSString *n;
		NSInteger ticks;
		BOOL en, focus;
		NSControlSize cs;
	} sliders[] = {
		{@"slider", 0, YES, NO, NSControlSizeRegular},
		{@"slider-ticks", 5, YES, NO, NSControlSizeRegular},
		{@"slider-small", 0, YES, NO, NSControlSizeSmall},
		{@"slider-disabled", 0, NO, NO, NSControlSizeRegular},
		{@"slider-focused", 0, YES, YES, NSControlSizeRegular},
		{@"slider-inactive", 0, YES, NO, NSControlSizeRegular},
	};
	for (size_t i = 0; i < sizeof sliders / sizeof sliders[0]; i++) {
		__auto_type c = sliders[i];
		Sample *s = add(@"select", c.n, ^{
			NSSlider *sl = [NSSlider sliderWithValue:40 minValue:0 maxValue:100 target:nil action:nil];
			sl.controlSize = c.cs;
			sl.numberOfTickMarks = c.ticks;
			sl.enabled = c.en;
			return sl;
		});
		s.size = NSMakeSize(200, 0), s.focus = c.focus;
		s.inactive = [c.n hasSuffix:@"-inactive"];
	}
	add(@"select", @"stepper", ^{
		NSStepper *st = [NSStepper new];
		return st;
	});
	add(@"select", @"stepper-inactive", ^{
		NSStepper *st = [NSStepper new];
		return st;
	}).inactive = YES;
	add(@"select", @"color-well", ^{
		NSColorWell *w = [NSColorWell colorWellWithStyle:NSColorWellStyleDefault];
		w.color = NSColor.systemTealColor;
		return w;
	});
	add(@"select", @"color-well-swatch", ^{ // the swatch of "color-well"
		NSColorWell *w = [NSColorWell colorWellWithStyle:NSColorWellStyleDefault];
		w.color = NSColor.systemTealColor;
		return w;
	});
	add(@"select", @"color-well-disabled", ^{
		NSColorWell *w = [NSColorWell colorWellWithStyle:NSColorWellStyleDefault];
		w.color = NSColor.systemTealColor;
		w.enabled = NO;
		return w;
	});
	add(@"select", @"date-picker", ^{
		NSDatePicker *d = [NSDatePicker new];
		d.datePickerElements = NSDatePickerElementFlagYearMonthDay;
		d.dateValue = [NSDate dateWithTimeIntervalSince1970:1792368000];
		d.timeZone = [NSTimeZone timeZoneWithName:@"UTC"];
		return d;
	});

	// text input
	struct {
		NSString *n, *text;
		int kind; // 0 field, 1 secure, 2 search, 3 combo, 4 token
		BOOL en, focus;
		NSControlSize cs;
	} fields[] = {
		{@"field-placeholder", @"", 0, YES, NO, NSControlSizeRegular},
		{@"field-text", @"Hello, world", 0, YES, NO, NSControlSizeRegular},
		{@"field-focused", @"Hello, world", 0, YES, YES, NSControlSizeRegular},
		{@"field-disabled", @"Hello, world", 0, NO, NO, NSControlSizeRegular},
		{@"field-small", @"Hello, world", 0, YES, NO, NSControlSizeSmall},
		{@"field-large", @"Hello, world", 0, YES, NO, NSControlSizeLarge},
		{@"secure", @"secret12", 1, YES, NO, NSControlSizeRegular},
		{@"search-empty", @"", 2, YES, NO, NSControlSizeRegular},
		{@"search-text", @"query", 2, YES, NO, NSControlSizeRegular},
		{@"search-focused", @"query", 2, YES, YES, NSControlSizeRegular},
		{@"combo", @"Combo", 3, YES, NO, NSControlSizeRegular},
		{@"token", @"", 4, YES, NO, NSControlSizeRegular},
	};
	for (size_t i = 0; i < sizeof fields / sizeof fields[0]; i++) {
		__auto_type c = fields[i];
		Sample *s = add(@"field", c.n, ^NSView * {
			NSTextField *f;
			switch (c.kind) {
			case 1: f = [NSSecureTextField new]; break;
			case 2: f = [NSSearchField new]; break;
			case 3: {
				NSComboBox *cb = [NSComboBox new];
				[cb addItemsWithObjectValues:@[ @"Combo", @"Other" ]];
				f = cb;
				break;
			}
			case 4: {
				NSTokenField *t = [NSTokenField new];
				t.objectValue = @[ @"Alpha", @"Beta" ];
				f = t;
				break;
			}
			default: f = [NSTextField new];
			}
			f.controlSize = c.cs;
			f.font = [NSFont systemFontOfSize:[NSFont systemFontSizeForControlSize:c.cs]];
			if (c.kind != 4) f.stringValue = c.text;
			f.placeholderString = c.kind == 2 ? nil : @"Placeholder";
			f.enabled = c.en;
			return f;
		});
		s.size = NSMakeSize(c.kind == 3 ? 160 : 200, 0), s.focus = c.focus;
	}
	add(@"field", @"textview", ^{
		return textScroll(NSScrollerStyleOverlay, NSMakeSize(320, 110), NO);
	}).size = NSMakeSize(320, 110);

	// D7a: the states of the combo box, token field and colour well that
	// rexui's samples (rexui/catalog/d7a.go) are set against, each under its
	// own name; the "-chip", "-button" and "-swatch" ones are the same control
	// as "token", "combo" and "color-well", measured at the part they name.
	struct {
		NSString *n;
		BOOL token, en, focus;
	} d7a[] = {
		{@"combo-focused", NO, YES, YES},
		{@"combo-disabled", NO, NO, NO},
		{@"combo-button", NO, YES, NO},
		{@"token-focused", YES, YES, YES},
		{@"token-disabled", YES, NO, NO},
		{@"token-chip", YES, YES, NO},
	};
	for (size_t i = 0; i < sizeof d7a / sizeof d7a[0]; i++) {
		__auto_type c = d7a[i];
		Sample *s = add(@"field", c.n, ^NSView * {
			NSTextField *f;
			if (c.token) {
				NSTokenField *t = [NSTokenField new];
				t.objectValue = @[ @"Alpha", @"Beta" ];
				f = t;
			} else {
				NSComboBox *cb = [NSComboBox new];
				[cb addItemsWithObjectValues:@[ @"Combo", @"Other" ]];
				cb.stringValue = @"Combo";
				f = cb;
			}
			f.font = [NSFont systemFontOfSize:[NSFont systemFontSizeForControlSize:NSControlSizeRegular]];
			f.placeholderString = @"Placeholder";
			f.enabled = c.en;
			return f;
		});
		s.size = NSMakeSize(c.token ? 200 : 160, 0), s.focus = c.focus;
	}

	// lists
	Sample *t;
	// The "-emphasized" tables and outlines hold their selection emphasised
	// (ARRowView), so that -live, whose window has no first responder, shows
	// it as offscreen does.
	t = add(@"list", @"table-fullwidth-emphasized", ^{
		return tableIn(NSTableViewStyleFullWidth, YES, NSMakeSize(420, 240), YES, NO);
	});
	t.size = NSMakeSize(420, 240), t.noShape = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		[w makeFirstResponder:((NSScrollView *)v).documentView];
	};
	t = add(@"list", @"table-fullwidth-unemphasized", ^{
		return tableIn(NSTableViewStyleFullWidth, YES, NSMakeSize(420, 240), NO, NO);
	});
	t.size = NSMakeSize(420, 240), t.noShape = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		[w makeFirstResponder:nil];
	};
	t = add(@"list", @"table-fullwidth-inactive", ^{
		return tableIn(NSTableViewStyleFullWidth, YES, NSMakeSize(420, 240), NO, NO);
	});
	t.size = NSMakeSize(420, 240), t.noShape = YES, t.inactive = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		[w makeFirstResponder:((NSScrollView *)v).documentView];
	};
	t = add(@"list", @"table-inset-emphasized", ^{
		return tableIn(NSTableViewStyleInset, NO, NSMakeSize(420, 240), YES, NO);
	});
	t.size = NSMakeSize(420, 240), t.noShape = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		[w makeFirstResponder:((NSScrollView *)v).documentView];
	};
	t = add(@"list", @"table-inset-fit-emphasized", ^{
		return tableIn(NSTableViewStyleInset, YES, NSMakeSize(420, 240), YES, YES);
	});
	t.size = NSMakeSize(420, 240), t.noShape = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		[w makeFirstResponder:((NSScrollView *)v).documentView];
	};
	t = add(@"list", @"table-inset-fit-inactive", ^{
		return tableIn(NSTableViewStyleInset, YES, NSMakeSize(420, 240), NO, YES);
	});
	t.size = NSMakeSize(420, 240), t.noShape = YES, t.inactive = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		[w makeFirstResponder:((NSScrollView *)v).documentView];
	};
	t = add(@"list", @"outline-emphasized", ^{
		return outlineIn(NSTableViewStyleFullWidth, NO, NSMakeSize(260, 240), YES);
	});
	t.size = NSMakeSize(260, 240), t.noShape = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		[w makeFirstResponder:((NSScrollView *)v).documentView];
	};
	t = add(@"list", @"outline-unemphasized", ^{
		return outlineIn(NSTableViewStyleFullWidth, NO, NSMakeSize(260, 240), NO);
	});
	t.size = NSMakeSize(260, 240), t.noShape = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		[w makeFirstResponder:nil];
	};
	t = add(@"list", @"outline-inset-emphasized", ^{
		return outlineIn(NSTableViewStyleInset, NO, NSMakeSize(260, 240), YES);
	});
	t.size = NSMakeSize(260, 240), t.noShape = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		[w makeFirstResponder:((NSScrollView *)v).documentView];
	};
	t = add(@"list", @"sidebar-emphasized", ^{
		return sidebarIn(NSMakeSize(220, 300), NSTableViewRowSizeStyleDefault);
	});
	t.size = NSMakeSize(220, 300), t.noShape = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		[w makeFirstResponder:((NSScrollView *)v).documentView];
	};
	t.note = @"vibrancy of a real sidebar needs window-server compositing: offscreen shows no material behind the rows";
	t = add(@"list", @"sidebar-unemphasized", ^{
		return sidebarIn(NSMakeSize(220, 300), NSTableViewRowSizeStyleDefault);
	});
	t.size = NSMakeSize(220, 300), t.noShape = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		[w makeFirstResponder:nil];
	};
	t = add(@"list", @"scroll-legacy", ^{
		return textScroll(NSScrollerStyleLegacy, NSMakeSize(260, 140), YES);
	});
	t.size = NSMakeSize(260, 140), t.noShape = YES;
	t = add(@"list", @"scroll-overlay", ^{
		return textScroll(NSScrollerStyleOverlay, NSMakeSize(260, 140), YES);
	});
	t.size = NSMakeSize(260, 140), t.noShape = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		NSScroller *sc = ((NSScrollView *)v).verticalScroller;
		@try {
			id imp = [sc valueForKey:@"scrollerImp"];
			SEL ka = NSSelectorFromString(@"setKnobAlpha:");
			if ([imp respondsToSelector:ka]) {
				void (*f)(id, SEL, CGFloat) = (void *)[imp methodForSelector:ka];
				f(imp, ka, 1.0);
			}
		} @catch (NSException *e) {
		}
	};
	t.note = @"overlay knob forced visible through the private scroller-imp knob alpha";
	t = add(@"list", @"scroll-overlay-expanded", ^{
		return textScroll(NSScrollerStyleOverlay, NSMakeSize(260, 140), YES);
	});
	t.size = NSMakeSize(260, 140), t.noShape = YES;
	t.after = ^(NSView *v, NSWindow *w) {
		NSScroller *sc = ((NSScrollView *)v).verticalScroller;
		@try {
			id imp = [sc valueForKey:@"scrollerImp"];
			SEL ka = NSSelectorFromString(@"setKnobAlpha:"), ta = NSSelectorFromString(@"setTrackAlpha:"), ex = NSSelectorFromString(@"setExpanded:");
			if ([imp respondsToSelector:ex]) {
				void (*f)(id, SEL, BOOL) = (void *)[imp methodForSelector:ex];
				f(imp, ex, YES);
			}
			for (NSValue *selv in @[ [NSValue valueWithPointer:ka], [NSValue valueWithPointer:ta] ]) {
				SEL s = selv.pointerValue;
				if ([imp respondsToSelector:s]) {
					void (*f)(id, SEL, CGFloat) = (void *)[imp methodForSelector:s];
					f(imp, s, 1.0);
				}
			}
		} @catch (NSException *e) {
		}
	};
	t.note = @"overlay scroller in its hovered (expanded, with track) state, forced through private scroller-imp setters";

	// progress
	add(@"progress", @"progress-bar", ^{
		NSProgressIndicator *p = [NSProgressIndicator new];
		p.style = NSProgressIndicatorStyleBar;
		p.indeterminate = NO;
		p.doubleValue = 40;
		return p;
	}).size = NSMakeSize(200, 0);
	Sample *pbi = add(@"progress", @"progress-bar-inactive", ^{
		NSProgressIndicator *p = [NSProgressIndicator new];
		p.style = NSProgressIndicatorStyleBar;
		p.indeterminate = NO;
		p.doubleValue = 40;
		return p;
	});
	pbi.size = NSMakeSize(200, 0), pbi.inactive = YES;
	Sample *pii = add(@"progress", @"progress-indeterminate-inactive", ^{
		NSProgressIndicator *p = [NSProgressIndicator new];
		p.style = NSProgressIndicatorStyleBar;
		p.indeterminate = YES;
		[p startAnimation:nil];
		return p;
	});
	pii.size = NSMakeSize(200, 0), pii.inactive = YES;
	Sample *lvi = add(@"progress", @"level-inactive", ^{
		NSLevelIndicator *l = [NSLevelIndicator new];
		l.levelIndicatorStyle = NSLevelIndicatorStyleContinuousCapacity;
		l.maxValue = 10, l.doubleValue = 6;
		return l;
	});
	lvi.size = NSMakeSize(160, 0), lvi.inactive = YES;
	add(@"progress", @"progress-indeterminate", ^{
		NSProgressIndicator *p = [NSProgressIndicator new];
		p.style = NSProgressIndicatorStyleBar;
		p.indeterminate = YES;
		[p startAnimation:nil];
		return p;
	}).size = NSMakeSize(200, 0);
	add(@"progress", @"spinner", ^{
		NSProgressIndicator *p = [NSProgressIndicator new];
		p.style = NSProgressIndicatorStyleSpinning;
		p.indeterminate = YES;
		[p startAnimation:nil];
		return p;
	}).noShape = YES;
	add(@"progress", @"spinner-small", ^{
		NSProgressIndicator *p = [NSProgressIndicator new];
		p.style = NSProgressIndicatorStyleSpinning;
		p.controlSize = NSControlSizeSmall;
		p.indeterminate = YES;
		[p startAnimation:nil];
		return p;
	}).noShape = YES;
	add(@"progress", @"level", ^{
		NSLevelIndicator *l = [NSLevelIndicator new];
		l.levelIndicatorStyle = NSLevelIndicatorStyleContinuousCapacity;
		l.maxValue = 10, l.doubleValue = 6;
		return l;
	}).size = NSMakeSize(160, 0);

	// D3b controls: toggle push buttons (NSButtonTypePushOnPushOff) and a link
	add(@"button", @"toggle-off", ^{
		NSButton *b = pushButton(@"Toggle", NSControlSizeRegular);
		b.buttonType = NSButtonTypePushOnPushOff;
		b.state = NSControlStateValueOff;
		return b;
	});
	add(@"button", @"toggle-on", ^{
		NSButton *b = pushButton(@"Toggle", NSControlSizeRegular);
		b.buttonType = NSButtonTypePushOnPushOff;
		b.state = NSControlStateValueOn;
		return b;
	});
	add(@"button", @"toggle-on-inactive", ^{
		NSButton *b = pushButton(@"Toggle", NSControlSizeRegular);
		b.buttonType = NSButtonTypePushOnPushOff;
		b.state = NSControlStateValueOn;
		return b;
	}).inactive = YES;
	add(@"button", @"toggle-on-disabled", ^{
		NSButton *b = pushButton(@"Toggle", NSControlSizeRegular);
		b.buttonType = NSButtonTypePushOnPushOff;
		b.state = NSControlStateValueOn;
		b.enabled = NO;
		return b;
	});
	add(@"button", @"link", ^{
		NSMutableAttributedString *a = [[NSMutableAttributedString alloc] initWithString:@"Learn more"];
		[a addAttributes:@{
			NSLinkAttributeName : [NSURL URLWithString:@"https://example.com"],
			NSForegroundColorAttributeName : NSColor.linkColor,
			NSUnderlineStyleAttributeName : @(NSUnderlineStyleSingle),
			NSFontAttributeName : [NSFont systemFontOfSize:13],
		} range:NSMakeRange(0, a.length)];
		NSTextField *l = [NSTextField labelWithAttributedString:a];
		l.allowsEditingTextAttributes = YES;
		l.selectable = YES;
		return l;
	}).noShape = YES;

	// containers
	add(@"container", @"tabview", ^{
		NSTabView *tv = [[NSTabView alloc] initWithFrame:NSMakeRect(0, 0, 320, 120)];
		for (NSString *n in @[ @"General", @"Advanced", @"About" ]) {
			NSTabViewItem *it = [[NSTabViewItem alloc] initWithIdentifier:n];
			it.label = n;
			[tv addTabViewItem:it];
		}
		return tv;
	}).size = NSMakeSize(320, 120);
	Sample *bx = add(@"container", @"box", ^{
		NSBox *b = [[NSBox alloc] initWithFrame:NSMakeRect(0, 0, 220, 90)];
		b.title = @"Group";
		return b;
	});
	bx.size = NSMakeSize(220, 90), bx.noShape = YES;
	add(@"container", @"separator", ^{
		NSBox *b = [[NSBox alloc] initWithFrame:NSMakeRect(0, 0, 200, 1)];
		b.boxType = NSBoxSeparator;
		return b;
	}).size = NSMakeSize(200, 1);
	add(@"container", @"label-styles", ^{
		NSStackView *st = [NSStackView new];
		st.orientation = NSUserInterfaceLayoutOrientationVertical;
		st.alignment = NSLayoutAttributeLeading;
		st.spacing = 4;
		NSArray *styles = @[ NSFontTextStyleLargeTitle, NSFontTextStyleTitle1, NSFontTextStyleTitle2, NSFontTextStyleTitle3, NSFontTextStyleHeadline, NSFontTextStyleBody, NSFontTextStyleCallout, NSFontTextStyleSubheadline, NSFontTextStyleFootnote, NSFontTextStyleCaption1, NSFontTextStyleCaption2 ];
		NSArray *names = @[ @"Large Title", @"Title 1", @"Title 2", @"Title 3", @"Headline", @"Body", @"Callout", @"Subheadline", @"Footnote", @"Caption 1", @"Caption 2" ];
		for (NSUInteger i = 0; i < styles.count; i++) {
			NSTextField *l = [NSTextField labelWithString:[names[i] stringByAppendingString:@" — The quick brown fox"]];
			l.font = [NSFont preferredFontForTextStyle:styles[i] options:@{}];
			[st addArrangedSubview:l];
		}
		NSArray *colors = @[ NSColor.labelColor, NSColor.secondaryLabelColor, NSColor.tertiaryLabelColor, NSColor.quaternaryLabelColor, NSColor.linkColor ];
		NSArray *cn = @[ @"label", @"secondaryLabel", @"tertiaryLabel", @"quaternaryLabel", @"link" ];
		for (NSUInteger i = 0; i < colors.count; i++) {
			NSTextField *l = [NSTextField labelWithString:[cn[i] stringByAppendingString:@"Color — body text"]];
			l.textColor = colors[i];
			[st addArrangedSubview:l];
		}
		return st;
	}).noShape = YES;
	defineFormSamples();
}

#pragma mark - Rendering one sample

static NSMutableDictionary *metricsOut;

static NSDictionary *viewMetrics(NSView *v) {
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	d[@"class"] = NSStringFromClass(v.class);
	d[@"frame"] = sizeJ(v.frame.size);
	NSSize ic = v.intrinsicContentSize;
	d[@"intrinsic"] = @{@"w" : ic.width == NSViewNoIntrinsicMetric ? @(-1) : num(ic.width), @"h" : ic.height == NSViewNoIntrinsicMetric ? @(-1) : num(ic.height)};
	d[@"fitting"] = sizeJ(v.fittingSize);
	NSEdgeInsets ai = v.alignmentRectInsets;
	if (ai.top || ai.left || ai.bottom || ai.right) d[@"alignmentInsets"] = @{@"t" : num(ai.top), @"l" : num(ai.left), @"b" : num(ai.bottom), @"r" : num(ai.right)};
	if (v.firstBaselineOffsetFromTop > 0) d[@"firstBaselineFromTop"] = num(v.firstBaselineOffsetFromTop);
	if ([v isKindOfClass:NSControl.class]) {
		NSControl *c = (NSControl *)v;
		d[@"font"] = fontJ(c.font);
		d[@"controlSize"] = @(c.controlSize);
		if ([c isKindOfClass:NSButton.class]) d[@"bezelStyle"] = @(((NSButton *)c).bezelStyle);
	}
	return d;
}

// renderSample renders s in appearance ap into dir/<apName>/<name>.png and returns its metrics.
static NSDictionary *renderSample(Sample *s, NSString *apName, NSString *dir) {
	NSAppearance *ap = [NSAppearance appearanceNamed:apName];
	gKey = !s.inactive;
	ARWindow *w = [[ARWindow alloc] initWithContentRect:NSMakeRect(0, 0, 800, 600) styleMask:NSWindowStyleMaskTitled backing:NSBackingStoreBuffered defer:YES];
	w.releasedWhenClosed = NO;
	w.appearance = ap;
	NSView *v = s.build();
	v.appearance = ap;
	if (s.size.width > 0 || s.size.height > 0) {
		NSSize fit = v.fittingSize;
		[v setFrameSize:NSMakeSize(s.size.width > 0 ? s.size.width : fit.width, s.size.height > 0 ? s.size.height : fit.height)];
	} else if ([v respondsToSelector:@selector(sizeToFit)]) {
		[(id)v sizeToFit];
		if (v.frame.size.width <= 0) [v setFrameSize:v.fittingSize];
	} else {
		[v setFrameSize:v.fittingSize];
	}
	const CGFloat pad = 14;
	NSSize vs = v.frame.size;
	NSView *box = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, ceil(vs.width + 2 * pad), ceil(vs.height + 2 * pad))];
	box.appearance = ap;
	[v setFrameOrigin:NSMakePoint(pad, pad)];
	[box addSubview:v];
	[w setContentSize:box.frame.size];
	[w.contentView addSubview:box];
	if (s.after) s.after(v, w);
	if (s.focus) [w makeFirstResponder:v];
	if (s.pressed && [v isKindOfClass:NSButton.class]) [(NSButton *)v highlight:YES];
	[box layoutSubtreeIfNeeded];
	[box displayIfNeeded];
	// let animations (spinners, indeterminate bars) take a first step
	[[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];
	[box layoutSubtreeIfNeeded];

	NSBitmapImageRep *rep = newRep(box.bounds.size);
	[box cacheDisplayInRect:box.bounds toBitmapImageRep:rep];

	NSMutableDictionary *m = [viewMetrics(v) mutableCopy];
	m[@"window"] = gKey ? @"key (simulated)" : @"inactive";
	if (s.note) m[@"note"] = s.note;
	if (s.pressed) m[@"pressed"] = @"cell highlighted (-highlight:YES)";
	// frame in pixels, top-left origin
	NSRect fpx = NSMakeRect(pad * 2, (box.bounds.size.height - pad - vs.height) * 2, vs.width * 2, vs.height * 2);
	if (!s.noShape) m[@"shape"] = measureShape(rep, fpx, s.probe);

	NSBitmapImageRep *out = rep;
	if (s.focus) {
		// The focus ring is drawn by a separate system layer that cacheDisplay
		// does not capture. Draw it with the documented focus-ring API from
		// the view's own mask (the ring AppKit would draw), first alone to
		// measure it, then over the control.
		NSRect vf = v.frame;
		void (^ring)(void) = ^{
			[ap performAsCurrentDrawingAppearance:^{
				NSAffineTransform *tr = [NSAffineTransform transform];
				[tr translateXBy:vf.origin.x yBy:vf.origin.y];
				if (v.isFlipped) {
					[tr translateXBy:0 yBy:vf.size.height];
					[tr scaleXBy:1 yBy:-1];
				}
				[tr concat];
				NSSetFocusRingStyle(NSFocusRingOnly);
				[v drawFocusRingMask];
			}];
		};
		NSBitmapImageRep *only = newRep(box.bounds.size);
		drawInto(only, ring);
		Px p = pxOf(only);
		// the ring along the centre row: outer and inner 50 % crossings at the left side
		double cy = NSMidY(fpx), peak = 0;
		NSInteger peakX = 0;
		for (NSInteger x = 0; x < (NSInteger)NSMidX(fpx); x++) {
			double a = alphaAt(p, x, (NSInteger)cy);
			if (a > peak) peak = a, peakX = x;
		}
		if (peak > 0.05) {
			double outer = edgeScan(p, 0, cy, 1, 0, peak / 2, (int)(peakX * 4 + 4));
			double inner = edgeScan(p, NSMidX(fpx), cy, -1, 0, peak / 2, (int)((NSMidX(fpx) - peakX) * 4));
			double ty = edgeScan(p, NSMidX(fpx), 0, 0, 1, peak / 2, (int)(cy * 4));
			NSMutableDictionary *fr = [@{
				@"peakAlpha" : num(peak),
				@"color" : pixelHex(p, peakX, (NSInteger)cy),
				@"outerLeftFromFrame" : num((outer - fpx.origin.x) / 2),
				@"outerTopFromFrame" : num((ty - fpx.origin.y) / 2),
				@"width" : num((NSMidX(fpx) - inner - outer) / 2),
				@"method" : @"NSSetFocusRingStyle(NSFocusRingOnly) + drawFocusRingMask; measured at the left side's centre row",
			} mutableCopy];
			NSDictionary *sh = m[@"shape"];
			if (sh[@"edges"]) fr[@"outsetFromBody"] = num([sh[@"edges"][@"left"] doubleValue] - (outer - fpx.origin.x) / 2);
			m[@"focusRing"] = fr;
		}
		out = [rep copy];
		drawInto(out, ring);
	}

	// composite over the window background of the appearance
	NSBitmapImageRep *comp = newRep(box.bounds.size);
	drawInto(comp, ^{
		[ap performAsCurrentDrawingAppearance:^{
			[NSColor.windowBackgroundColor setFill];
			NSRectFill(box.bounds);
		}];
		[out drawInRect:box.bounds fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1 respectFlipped:NO hints:nil];
	});
	NSString *apShort = [apName isEqual:NSAppearanceNameDarkAqua] ? @"dark" : @"light";
	NSString *path = [NSString stringWithFormat:@"%@/%@/%@.png", dir, apShort, s.name];
	savePNG(comp, path);
	m[@"png"] = [NSString stringWithFormat:@"%@/%@.png", apShort, s.name];
	m[@"framePt"] = rectJ(NSMakeRect(pad, pad, vs.width, vs.height));

	// extra per-kind metrics
	if ([v isKindOfClass:NSScrollView.class]) {
		NSScrollView *sv = (NSScrollView *)v;
		NSView *dv = sv.documentView;
		if ([dv isKindOfClass:NSTableView.class]) {
			NSTableView *tv = (NSTableView *)dv;
			NSMutableDictionary *td = [@{
				@"rowHeight" : num(tv.rowHeight),
				@"row0Rect" : rectJ([tv rectOfRow:0]),
				@"row2Rect" : rectJ([tv rectOfRow:2]),
				@"intercellSpacing" : sizeJ(tv.intercellSpacing),
				@"headerHeight" : num(tv.headerView ? tv.headerView.frame.size.height : 0),
				@"style" : @(tv.style),
				@"effectiveStyle" : @(tv.effectiveStyle),
				@"rowSizeStyle" : @(tv.rowSizeStyle),
				@"effectiveRowSizeStyle" : @(tv.effectiveRowSizeStyle),
				@"gridStyleMask" : @(tv.gridStyleMask),
			} mutableCopy];
			if ([tv isKindOfClass:NSOutlineView.class]) {
				NSOutlineView *ov = (NSOutlineView *)tv;
				td[@"indentationPerLevel"] = num(ov.indentationPerLevel);
				NSMutableArray *rows = [NSMutableArray array];
				for (NSInteger r = 0; r < ov.numberOfRows; r++) {
					NSTableCellView *cv = [ov viewAtColumn:0 row:r makeIfNecessary:YES];
					SideItem *it = [ov itemAtRow:r];
					NSMutableDictionary *rd = [@{
						@"title" : it.title,
						@"rect" : rectJ([ov rectOfRow:r]),
						@"level" : @([ov levelForRow:r]),
						@"cellFrame" : rectJ([ov frameOfCellAtColumn:0 row:r]),
					} mutableCopy];
					if (cv.textField) {
						rd[@"font"] = fontJ(cv.textField.font);
						rd[@"textColor"] = resolveHex(cv.textField.textColor, ap);
					}
					if (cv.imageView) rd[@"imageFrame"] = rectJ(cv.imageView.frame);
					if (cv.textField) rd[@"textFrame"] = rectJ(cv.textField.frame);
					if ([ov isExpandable:it]) {
						rd[@"disclosureFrame"] = rectJ([ov frameOfOutlineCellAtRow:r]);
						rd[@"expanded"] = @([ov isItemExpanded:it]);
					}
					[rows addObject:rd];
				}
				td[@"rows"] = rows;
			} else {
				NSTableCellView *cv = [tv viewAtColumn:0 row:0 makeIfNecessary:YES];
				if (cv.textField) td[@"cellFont"] = fontJ(cv.textField.font);
				NSTableHeaderCell *hc = tv.tableColumns[0].headerCell;
				td[@"headerFont"] = fontJ(hc.font);
				NSTableRowView *rv = [tv rowViewAtRow:2 makeIfNecessary:NO];
				if (rv) td[@"selectedRowView"] = @{@"emphasized" : @(rv.emphasized), @"frame" : rectJ(rv.frame)};
				NSMutableArray *cells = [NSMutableArray array], *heads = [NSMutableArray array];
				for (NSInteger c = 0; c < tv.numberOfColumns; c++) {
					[cells addObject:rectJ([tv frameOfCellAtColumn:c row:0])];
					if (tv.headerView) [heads addObject:rectJ([tv.headerView headerRectOfColumn:c])];
				}
				td[@"row0Cells"] = cells;
				if (cv.textField) td[@"row0TextFrame"] = rectJ(cv.textField.frame);
				if (tv.headerView) td[@"headerRects"] = heads;
				td[@"contentWidth"] = num(tv.frame.size.width);
			}
			m[@"table"] = td;
		}
		m[@"scrollers"] = @{
			@"style" : @(sv.scrollerStyle),
			@"verticalFrame" : sv.verticalScroller ? rectJ(sv.verticalScroller.frame) : @{},
			@"knobProportion" : num(sv.verticalScroller.knobProportion),
		};
	}
	if ([v isKindOfClass:NSStackView.class]) m[@"subviews"] = @(v.subviews.count);

	[box removeFromSuperview];
	[w close];
	return m;
}

#pragma mark - Non-visual metrics

static NSDictionary *colorTable(void) {
	NSDictionary *named = @{
		@"labelColor" : NSColor.labelColor,
		@"secondaryLabelColor" : NSColor.secondaryLabelColor,
		@"tertiaryLabelColor" : NSColor.tertiaryLabelColor,
		@"quaternaryLabelColor" : NSColor.quaternaryLabelColor,
		@"textColor" : NSColor.textColor,
		@"placeholderTextColor" : NSColor.placeholderTextColor,
		@"selectedTextColor" : NSColor.selectedTextColor,
		@"textBackgroundColor" : NSColor.textBackgroundColor,
		@"selectedTextBackgroundColor" : NSColor.selectedTextBackgroundColor,
		@"keyboardFocusIndicatorColor" : NSColor.keyboardFocusIndicatorColor,
		@"unemphasizedSelectedTextColor" : NSColor.unemphasizedSelectedTextColor,
		@"unemphasizedSelectedTextBackgroundColor" : NSColor.unemphasizedSelectedTextBackgroundColor,
		@"linkColor" : NSColor.linkColor,
		@"separatorColor" : NSColor.separatorColor,
		@"selectedContentBackgroundColor" : NSColor.selectedContentBackgroundColor,
		@"unemphasizedSelectedContentBackgroundColor" : NSColor.unemphasizedSelectedContentBackgroundColor,
		@"selectedMenuItemTextColor" : NSColor.selectedMenuItemTextColor,
		@"gridColor" : NSColor.gridColor,
		@"headerTextColor" : NSColor.headerTextColor,
		@"alternateSelectedControlTextColor" : NSColor.alternateSelectedControlTextColor,
		@"controlAccentColor" : NSColor.controlAccentColor,
		@"controlColor" : NSColor.controlColor,
		@"controlBackgroundColor" : NSColor.controlBackgroundColor,
		@"controlTextColor" : NSColor.controlTextColor,
		@"disabledControlTextColor" : NSColor.disabledControlTextColor,
		@"selectedControlColor" : NSColor.selectedControlColor,
		@"selectedControlTextColor" : NSColor.selectedControlTextColor,
		@"findHighlightColor" : NSColor.findHighlightColor,
		@"windowBackgroundColor" : NSColor.windowBackgroundColor,
		@"underPageBackgroundColor" : NSColor.underPageBackgroundColor,
		@"windowFrameTextColor" : NSColor.windowFrameTextColor,
		@"textInsertionPointColor" : NSColor.textInsertionPointColor,
		@"highlightColor" : NSColor.highlightColor,
		@"shadowColor" : NSColor.shadowColor,
		@"systemFillColor" : NSColor.systemFillColor,
		@"secondarySystemFillColor" : NSColor.secondarySystemFillColor,
		@"tertiarySystemFillColor" : NSColor.tertiarySystemFillColor,
		@"quaternarySystemFillColor" : NSColor.quaternarySystemFillColor,
		@"quinarySystemFillColor" : NSColor.quinarySystemFillColor,
		@"quinaryLabelColor" : NSColor.quinaryLabelColor,
		@"systemRedColor" : NSColor.systemRedColor,
		@"systemOrangeColor" : NSColor.systemOrangeColor,
		@"systemYellowColor" : NSColor.systemYellowColor,
		@"systemGreenColor" : NSColor.systemGreenColor,
		@"systemMintColor" : NSColor.systemMintColor,
		@"systemTealColor" : NSColor.systemTealColor,
		@"systemCyanColor" : NSColor.systemCyanColor,
		@"systemBlueColor" : NSColor.systemBlueColor,
		@"systemIndigoColor" : NSColor.systemIndigoColor,
		@"systemPurpleColor" : NSColor.systemPurpleColor,
		@"systemPinkColor" : NSColor.systemPinkColor,
		@"systemBrownColor" : NSColor.systemBrownColor,
		@"systemGrayColor" : NSColor.systemGrayColor,
	};
	NSMutableDictionary *out = [NSMutableDictionary dictionary];
	for (NSString *apn in @[ NSAppearanceNameAqua, NSAppearanceNameDarkAqua, NSAppearanceNameAccessibilityHighContrastAqua, NSAppearanceNameAccessibilityHighContrastDarkAqua ]) {
		NSAppearance *ap = [NSAppearance appearanceNamed:apn];
		NSMutableDictionary *d = [NSMutableDictionary dictionary];
		for (NSString *k in named) d[k] = resolveHex(named[k], ap);
		NSMutableArray *alt = [NSMutableArray array];
		for (NSColor *c in NSColor.alternatingContentBackgroundColors) [alt addObject:resolveHex(c, ap)];
		d[@"alternatingContentBackgroundColors"] = alt;
		out[apn] = d;
	}
	return out;
}

static NSDictionary *fontTable(void) {
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	d[@"systemFontSize"] = num(NSFont.systemFontSize);
	d[@"smallSystemFontSize"] = num(NSFont.smallSystemFontSize);
	d[@"labelFontSize"] = num(NSFont.labelFontSize);
	NSMutableDictionary *cs = [NSMutableDictionary dictionary];
	NSArray *sizes = @[ @[ @"mini", @(NSControlSizeMini) ], @[ @"small", @(NSControlSizeSmall) ], @[ @"regular", @(NSControlSizeRegular) ], @[ @"large", @(NSControlSizeLarge) ] ];
	if (@available(macOS 26.0, *)) sizes = [sizes arrayByAddingObject:@[ @"xlarge", @(NSControlSizeExtraLarge) ]];
	for (NSArray *s in sizes) {
		NSControlSize c = [s[1] unsignedIntegerValue];
		NSButton *b = [NSButton buttonWithTitle:@"X" target:nil action:nil];
		b.controlSize = c;
		cs[s[0]] = @{@"systemFontSizeForControlSize" : num([NSFont systemFontSizeForControlSize:c]), @"buttonDefaultFontAfterSetControlSize" : fontJ(b.font)};
	}
	d[@"controlSizes"] = cs;
	d[@"menuFont"] = fontJ([NSFont menuFontOfSize:0]);
	d[@"menuBarFont"] = fontJ([NSFont menuBarFontOfSize:0]);
	d[@"titleBarFont"] = fontJ([NSFont titleBarFontOfSize:0]);
	d[@"toolTipsFont"] = fontJ([NSFont toolTipsFontOfSize:0]);
	d[@"messageFont"] = fontJ([NSFont messageFontOfSize:0]);
	d[@"controlContentFont"] = fontJ([NSFont controlContentFontOfSize:0]);
	d[@"labelFont"] = fontJ([NSFont labelFontOfSize:0]);
	d[@"paletteFont"] = fontJ([NSFont paletteFontOfSize:0]);
	d[@"userFixedPitchFont"] = fontJ([NSFont userFixedPitchFontOfSize:0]);
	d[@"monospacedSystemFont13"] = fontJ([NSFont monospacedSystemFontOfSize:13 weight:NSFontWeightRegular]);
	d[@"monospacedDigitSystemFont13"] = fontJ([NSFont monospacedDigitSystemFontOfSize:13 weight:NSFontWeightRegular]);
	NSDictionary *styles = @{
		@"largeTitle" : NSFontTextStyleLargeTitle,
		@"title1" : NSFontTextStyleTitle1,
		@"title2" : NSFontTextStyleTitle2,
		@"title3" : NSFontTextStyleTitle3,
		@"headline" : NSFontTextStyleHeadline,
		@"subheadline" : NSFontTextStyleSubheadline,
		@"body" : NSFontTextStyleBody,
		@"callout" : NSFontTextStyleCallout,
		@"footnote" : NSFontTextStyleFootnote,
		@"caption1" : NSFontTextStyleCaption1,
		@"caption2" : NSFontTextStyleCaption2,
	};
	NSMutableDictionary *ts = [NSMutableDictionary dictionary];
	for (NSString *k in styles) {
		NSFont *f = [NSFont preferredFontForTextStyle:styles[k] options:@{}];
		NSMutableDictionary *e = [fontJ(f) mutableCopy];
		// a single-line label (NSTextField labelWithString:): its intrinsic height, the
		// cell's rounded up to a half point, and the width of "Check" (theme.TextSpec.Cell)
		NSTextField *l = [NSTextField labelWithString:@"Check"];
		l.font = f;
		e[@"labelCell"] = num(l.intrinsicContentSize.height);
		e[@"labelCheckWidth"] = num(l.intrinsicContentSize.width);
		ts[k] = e;
	}
	d[@"textStyles"] = ts;
	// the system font across sizes: the optical-size axis and the tracking
	NSMutableArray *curve = [NSMutableArray array];
	for (NSNumber *n in @[ @9, @10, @11, @12, @13, @14, @15, @16, @17, @19, @20, @22, @24, @26, @28, @34 ]) {
		NSFont *f = [NSFont systemFontOfSize:n.doubleValue];
		NSMutableDictionary *e = [fontJ(f) mutableCopy];
		e[@"requested"] = n;
		[curve addObject:e];
	}
	d[@"systemFontBySize"] = curve;
	NSMutableArray *weights = [NSMutableArray array];
	NSArray *wn = @[ @"ultraLight", @"thin", @"light", @"regular", @"medium", @"semibold", @"bold", @"heavy", @"black" ];
	CGFloat wv[] = {NSFontWeightUltraLight, NSFontWeightThin, NSFontWeightLight, NSFontWeightRegular, NSFontWeightMedium, NSFontWeightSemibold, NSFontWeightBold, NSFontWeightHeavy, NSFontWeightBlack};
	for (NSUInteger i = 0; i < wn.count; i++) {
		NSMutableDictionary *e = [fontJ([NSFont systemFontOfSize:13 weight:wv[i]]) mutableCopy];
		e[@"weightName"] = wn[i];
		e[@"nsFontWeight"] = num(wv[i]);
		[weights addObject:e];
	}
	d[@"systemFont13ByWeight"] = weights;
	return d;
}

static NSDictionary *windowTable(void) {
	NSMutableDictionary *out = [NSMutableDictionary dictionary];
	TBDelegate *tbd = [TBDelegate new];
	[keep addObject:tbd];
	struct {
		NSString *n;
		NSWindowStyleMask mask;
		BOOL toolbar, transparent;
		NSWindowToolbarStyle ts;
	} kinds[] = {
		{@"titled", NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable, NO, NO, NSWindowToolbarStyleAutomatic},
		{@"fullSizeTransparent", NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable | NSWindowStyleMaskFullSizeContentView, NO, YES,
		 NSWindowToolbarStyleAutomatic},
		{@"toolbarUnified", NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable | NSWindowStyleMaskFullSizeContentView, YES, NO,
		 NSWindowToolbarStyleUnified},
		{@"toolbarUnifiedCompact", NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable | NSWindowStyleMaskFullSizeContentView, YES, NO,
		 NSWindowToolbarStyleUnifiedCompact},
		{@"toolbarExpanded", NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable | NSWindowStyleMaskFullSizeContentView, YES, NO,
		 NSWindowToolbarStyleExpanded},
	};
	for (size_t i = 0; i < sizeof kinds / sizeof kinds[0]; i++) {
		__auto_type k = kinds[i];
		ARWindow *w = [[ARWindow alloc] initWithContentRect:NSMakeRect(0, 0, 700, 400) styleMask:k.mask backing:NSBackingStoreBuffered defer:YES];
		w.releasedWhenClosed = NO;
		w.title = @"Window";
		w.titlebarAppearsTransparent = k.transparent;
		if (k.toolbar) {
			NSToolbar *tb = [[NSToolbar alloc] initWithIdentifier:k.n];
			tb.delegate = tbd;
			tb.displayMode = NSToolbarDisplayModeIconOnly;
			w.toolbar = tb;
			w.toolbarStyle = k.ts;
		}
		[w.contentView layoutSubtreeIfNeeded];
		NSRect fr = w.frame, cl = w.contentLayoutRect;
		NSMutableDictionary *d = [NSMutableDictionary dictionary];
		d[@"frame"] = sizeJ(fr.size);
		d[@"contentLayoutRect"] = rectJ(cl);
		d[@"titlebarPlusToolbarHeight"] = num(fr.size.height - NSMaxY(cl));
		NSMutableDictionary *btn = [NSMutableDictionary dictionary];
		NSDictionary *names = @{@"close" : @(NSWindowCloseButton), @"miniaturize" : @(NSWindowMiniaturizeButton), @"zoom" : @(NSWindowZoomButton)};
		for (NSString *bn in names) {
			NSButton *b = [w standardWindowButton:[names[bn] unsignedIntegerValue]];
			if (!b) continue;
			NSRect r = [b convertRect:b.bounds toView:nil];
			// from the window's top left
			btn[bn] = @{@"x" : num(r.origin.x), @"yFromTop" : num(fr.size.height - NSMaxY(r)), @"w" : num(r.size.width), @"h" : num(r.size.height), @"centerYFromTop" : num(fr.size.height - NSMidY(r))};
		}
		d[@"trafficLights"] = btn;
		SEL cr = NSSelectorFromString(@"_cornerRadius");
		if ([w respondsToSelector:cr]) {
			CGFloat (*f)(id, SEL) = (void *)[w methodForSelector:cr];
			d[@"cornerRadius(private)"] = num(f(w, cr));
		}
		out[k.n] = d;
		[w close];
	}
	return out;
}

static NSDictionary *menuTable(void) {
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	NSMenu * (^mk)(int, BOOL) = ^(int n, BOOL sep) {
		NSMenu *m = [[NSMenu alloc] initWithTitle:@"M"];
		for (int i = 0; i < n; i++) [m addItemWithTitle:[NSString stringWithFormat:@"Item %d", i] action:nil keyEquivalent:@""];
		if (sep) [m addItem:NSMenuItem.separatorItem];
		return m;
	};
	NSSize s1 = mk(1, NO).size, s5 = mk(5, NO).size, s5s = mk(5, YES).size;
	d[@"oneItemMenuSize"] = sizeJ(s1);
	d[@"fiveItemMenuSize"] = sizeJ(s5);
	d[@"itemHeight"] = num((s5.height - s1.height) / 4);
	d[@"verticalPadding"] = num((s1.height - (s5.height - s1.height) / 4) / 2);
	d[@"separatorHeight"] = num(s5s.height - s5.height);
	NSMenu *ks = [[NSMenu alloc] initWithTitle:@"K"];
	[ks addItemWithTitle:@"Copy" action:nil keyEquivalent:@"c"];
	NSMenuItem *img = [ks addItemWithTitle:@"With image" action:nil keyEquivalent:@""];
	img.image = sym(@"doc");
	d[@"menuWithShortcutAndImageSize"] = sizeJ(ks.size);
	d[@"menuFont"] = fontJ(ks.font);
	d[@"statusBarThickness"] = num(NSStatusBar.systemStatusBar.thickness);
	d[@"note"] = @"menus are drawn by the system in their own windows; offscreen cannot render them, so only their metrics are reported";
	return d;
}

static NSDictionary *scrollerTable(void) {
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	d[@"preferredScrollerStyle"] = NSScroller.preferredScrollerStyle == NSScrollerStyleOverlay ? @"overlay" : @"legacy";
	for (NSArray *cs in @[ @[ @"regular", @(NSControlSizeRegular) ], @[ @"small", @(NSControlSizeSmall) ], @[ @"mini", @(NSControlSizeMini) ] ]) {
		for (NSArray *st in @[ @[ @"legacy", @(NSScrollerStyleLegacy) ], @[ @"overlay", @(NSScrollerStyleOverlay) ] ]) {
			d[[NSString stringWithFormat:@"%@-%@", cs[0], st[0]]] = num([NSScroller scrollerWidthForControlSize:[cs[1] unsignedIntegerValue]
			                                                                                     scrollerStyle:[st[1] integerValue]]);
		}
	}
	return d;
}

static NSDictionary *behaviourTable(void) {
	NSUserDefaults *ud = NSUserDefaults.standardUserDefaults;
	NSMutableDictionary *d = [@{
		@"doubleClickInterval" : num(NSEvent.doubleClickInterval),
		@"keyRepeatDelay" : num(NSEvent.keyRepeatDelay),
		@"keyRepeatInterval" : num(NSEvent.keyRepeatInterval),
		@"defaultAnimationDuration" : num([NSAnimationContext currentContext].duration),
		@"reduceMotion" : @(NSWorkspace.sharedWorkspace.accessibilityDisplayShouldReduceMotion),
		@"reduceTransparency" : @(NSWorkspace.sharedWorkspace.accessibilityDisplayShouldReduceTransparency),
		@"increaseContrast" : @(NSWorkspace.sharedWorkspace.accessibilityDisplayShouldIncreaseContrast),
		@"fullKeyboardAccess" : @(NSApp.fullKeyboardAccessEnabled),
	} mutableCopy];
	for (NSString *k in @[ @"AppleAccentColor", @"AppleHighlightColor", @"AppleShowScrollBars", @"AppleInterfaceStyle", @"NSTextInsertionPointBlinkPeriodOn", @"NSTextInsertionPointBlinkPeriodOff", @"AppleScrollerPagingBehavior", @"NSTableViewDefaultSizeMode", @"AppleReduceDesktopTinting" ]) {
		id v = [ud objectForKey:k];
		d[k] = v ? [v description] : @"(unset)";
	}
	return d;
}

static NSDictionary *alertSample(NSString *apName, NSString *dir) {
	NSAppearance *ap = [NSAppearance appearanceNamed:apName];
	gKey = YES;
	NSAlert *a = [NSAlert new];
	a.messageText = @"Delete “Annual Report.pdf”?";
	a.informativeText = @"This item will be deleted immediately. You can’t undo this action.";
	[a addButtonWithTitle:@"Delete"];
	[a addButtonWithTitle:@"Cancel"];
	a.buttons[0].hasDestructiveAction = YES;
	a.window.appearance = ap;
	[a layout];
	NSView *cv = a.window.contentView;
	NSView *frameView = cv.superview ?: cv;
	[frameView layoutSubtreeIfNeeded];
	NSBitmapImageRep *rep = newRep(frameView.bounds.size);
	[frameView cacheDisplayInRect:frameView.bounds toBitmapImageRep:rep];
	NSBitmapImageRep *comp = newRep(frameView.bounds.size);
	drawInto(comp, ^{
		[ap performAsCurrentDrawingAppearance:^{
			[NSColor.windowBackgroundColor setFill];
			NSRectFill(frameView.bounds);
		}];
		[rep drawInRect:frameView.bounds fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1 respectFlipped:NO hints:nil];
	});
	NSString *apShort = [apName isEqual:NSAppearanceNameDarkAqua] ? @"dark" : @"light";
	savePNG(comp, [NSString stringWithFormat:@"%@/%@/alert.png", dir, apShort]);
	NSMutableArray *btns = [NSMutableArray array];
	for (NSButton *b in a.buttons) [btns addObject:@{@"title" : b.title, @"frame" : rectJ([b convertRect:b.bounds toView:nil]), @"font" : fontJ(b.font)}];
	NSMutableArray *labels = [NSMutableArray array];
	NSMutableArray *stack = [NSMutableArray arrayWithObject:cv];
	while (stack.count) {
		NSView *x = stack.lastObject;
		[stack removeLastObject];
		[stack addObjectsFromArray:x.subviews];
		if ([x isKindOfClass:NSTextField.class] && ((NSTextField *)x).stringValue.length)
			[labels addObject:@{@"text" : ((NSTextField *)x).stringValue, @"frame" : rectJ([x convertRect:x.bounds toView:nil]), @"font" : fontJ(((NSTextField *)x).font)}];
	}
	return @{
		@"png" : [NSString stringWithFormat:@"%@/alert.png", apShort],
		@"windowFrame" : sizeJ(a.window.frame.size),
		@"buttons" : btns,
		@"labels" : labels,
		@"note" : @"NSAlert laid out and rendered without being shown; the panel's material/vibrancy and shadow need the window server"
	};
}

// windowChrome renders the frame view (titlebar, traffic lights, toolbar) of
// a few window styles, never shown.
static void windowChromeSamples(NSString *apName, NSString *dir, NSMutableDictionary *into) {
	NSAppearance *ap = [NSAppearance appearanceNamed:apName];
	NSString *apShort = [apName isEqual:NSAppearanceNameDarkAqua] ? @"dark" : @"light";
	TBDelegate *tbd = [TBDelegate new];
	[keep addObject:tbd];
	for (NSString *kind in @[ @"titled", @"toolbarUnified", @"toolbarUnifiedCompact" ]) {
		for (NSNumber *active in @[ @YES, @NO ]) {
			gKey = active.boolValue;
			NSWindowStyleMask mask = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable;
			if (![kind isEqual:@"titled"]) mask |= NSWindowStyleMaskFullSizeContentView;
			ARWindow *w = [[ARWindow alloc] initWithContentRect:NSMakeRect(0, 0, 480, 120) styleMask:mask backing:NSBackingStoreBuffered defer:YES];
			w.releasedWhenClosed = NO;
			w.appearance = ap;
			w.title = @"Window Title";
			if (![kind isEqual:@"titled"]) {
				NSToolbar *tb = [[NSToolbar alloc] initWithIdentifier:kind];
				tb.delegate = tbd;
				tb.displayMode = NSToolbarDisplayModeIconOnly;
				w.toolbar = tb;
				w.toolbarStyle = [kind isEqual:@"toolbarUnified"] ? NSWindowToolbarStyleUnified : NSWindowToolbarStyleUnifiedCompact;
			}
			NSView *fv = w.contentView.superview;
			[fv layoutSubtreeIfNeeded];
			[fv displayIfNeeded];
			NSBitmapImageRep *rep = newRep(fv.bounds.size);
			[fv cacheDisplayInRect:fv.bounds toBitmapImageRep:rep];
			NSString *name = [NSString stringWithFormat:@"window-%@-%@", kind, active.boolValue ? @"active" : @"inactive"];
			NSBitmapImageRep *comp = newRep(fv.bounds.size);
			drawInto(comp, ^{
				[ap performAsCurrentDrawingAppearance:^{
					[NSColor.windowBackgroundColor setFill];
					NSRectFill(fv.bounds);
				}];
				[rep drawInRect:fv.bounds fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1 respectFlipped:NO hints:nil];
			});
			savePNG(comp, [NSString stringWithFormat:@"%@/%@/%@.png", dir, apShort, name]);
			into[name] = @{
				@"png" : [NSString stringWithFormat:@"%@/%@.png", apShort, name],
				@"note" : @"the window's frame view drawn offscreen: titlebar/toolbar materials, the window shadow and the rounded window mask are composited by the window server and are missing"
			};
			[w close];
		}
	}
	gKey = YES;
}

#pragma mark - Sheets

typedef struct {
	NSString *label;
	NSImage *img;
	NSImage *img2;
} Cell;

// flowSheet lays cells out left to right, wrapping at maxW points, each with a
// caption under it, and returns the sheet at 2x.
static NSBitmapImageRep *flowSheet(NSString *title, NSArray<NSDictionary *> *cells, NSColor *bg, NSColor *fg, NSColor *fg2, CGFloat maxW) {
	const CGFloat gap = 16, capH = 16, margin = 20, titleH = 30;
	// measure
	CGFloat x = margin, y = margin + titleH, rowH = 0, W = 0;
	NSMutableArray *pos = [NSMutableArray array];
	for (NSDictionary *c in cells) {
		NSImage *im = c[@"img"];
		NSSize s = im.size;
		if (x > margin && x + s.width > maxW - margin) {
			x = margin;
			y += rowH + gap;
			rowH = 0;
		}
		[pos addObject:[NSValue valueWithPoint:NSMakePoint(x, y)]];
		x += s.width + gap;
		rowH = fmax(rowH, s.height + capH);
		W = fmax(W, x);
	}
	CGFloat H = y + rowH + margin;
	W = fmax(W + margin - gap, 400);
	NSBitmapImageRep *rep = newRep(NSMakeSize(ceil(W), ceil(H)));
	drawInto(rep, ^{
		[bg setFill];
		NSRectFill(NSMakeRect(0, 0, W, H));
		NSDictionary *ta = @{NSFontAttributeName : [NSFont boldSystemFontOfSize:15], NSForegroundColorAttributeName : fg};
		[title drawAtPoint:NSMakePoint(margin, H - margin - 20) withAttributes:ta];
		NSDictionary *ca = @{NSFontAttributeName : [NSFont systemFontOfSize:10], NSForegroundColorAttributeName : fg2};
		for (NSUInteger i = 0; i < cells.count; i++) {
			NSDictionary *c = cells[i];
			NSImage *im = c[@"img"];
			NSPoint p = [pos[i] pointValue];
			CGFloat top = H - p.y; // flip
			[im drawInRect:NSMakeRect(p.x, top - im.size.height, im.size.width, im.size.height)];
			[c[@"label"] drawAtPoint:NSMakePoint(p.x, top - im.size.height - capH + 2) withAttributes:ca];
		}
	});
	return rep;
}

static NSImage *imageAt2x(NSString *path) {
	NSImage *im = [[NSImage alloc] initWithContentsOfFile:path];
	if (!im) return nil;
	NSImageRep *r = im.representations.firstObject;
	im.size = NSMakeSize(r.pixelsWide / 2.0, r.pixelsHigh / 2.0);
	return im;
}

// pairSheet composes {label, left, right} rows: AppKit on the left, ours on the right.
static int pairSheet(NSString *manifest, NSString *outPath) {
	NSData *data = [NSData dataWithContentsOfFile:manifest];
	if (!data) {
		fprintf(stderr, "no manifest %s\n", manifest.UTF8String);
		return 1;
	}
	NSDictionary *m = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
	BOOL dark = [m[@"dark"] boolValue];
	NSAppearance *ap = [NSAppearance appearanceNamed:dark ? NSAppearanceNameDarkAqua : NSAppearanceNameAqua];
	__block NSColor *bg, *fg, *fg2;
	[ap performAsCurrentDrawingAppearance:^{
		bg = [NSColor.windowBackgroundColor colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
		fg = [NSColor.labelColor colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
		fg2 = [NSColor.secondaryLabelColor colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
	}];
	NSArray *rows = m[@"rows"];
	const CGFloat margin = 20, labelW = 150, gap = 24, rowGap = 14, head = 54;
	CGFloat lw = 0, rw = 0, H = margin + head;
	NSMutableArray *imgs = [NSMutableArray array];
	for (NSDictionary *r in rows) {
		NSImage *a = imageAt2x(r[@"left"]), *b = imageAt2x(r[@"right"]);
		[imgs addObject:@[ a ?: NSNull.null, b ?: NSNull.null ]];
		lw = fmax(lw, a.size.width);
		rw = fmax(rw, b.size.width);
		H += fmax(fmax(a.size.height, b.size.height), 18) + rowGap;
	}
	lw = fmax(lw, 200);
	CGFloat W = margin + labelW + lw + gap + rw + margin;
	H += margin;
	NSBitmapImageRep *rep = newRep(NSMakeSize(ceil(W), ceil(H)));
	drawInto(rep, ^{
		[bg setFill];
		NSRectFill(NSMakeRect(0, 0, W, H));
		NSDictionary *ta = @{NSFontAttributeName : [NSFont boldSystemFontOfSize:15], NSForegroundColorAttributeName : fg};
		NSDictionary *ha = @{NSFontAttributeName : [NSFont boldSystemFontOfSize:11], NSForegroundColorAttributeName : fg2};
		NSDictionary *la = @{NSFontAttributeName : [NSFont systemFontOfSize:11], NSForegroundColorAttributeName : fg};
		[m[@"title"] drawAtPoint:NSMakePoint(margin, H - margin - 18) withAttributes:ta];
		[@"AppKit (real controls)" drawAtPoint:NSMakePoint(margin + labelW, H - margin - head + 8) withAttributes:ha];
		[@"Ours (rexui, Rex theme)" drawAtPoint:NSMakePoint(margin + labelW + lw + gap, H - margin - head + 8) withAttributes:ha];
		CGFloat y = H - margin - head;
		for (NSUInteger i = 0; i < rows.count; i++) {
			NSArray *pair = imgs[i];
			NSImage *a = pair[0] == NSNull.null ? nil : pair[0], *b = pair[1] == NSNull.null ? nil : pair[1];
			CGFloat h = fmax(fmax(a.size.height, b.size.height), 18);
			[rows[i][@"label"] drawAtPoint:NSMakePoint(margin, y - h / 2 - 7) withAttributes:la];
			if (a) [a drawInRect:NSMakeRect(margin + labelW, y - h + (h - a.size.height) / 2, a.size.width, a.size.height)];
			if (b) [b drawInRect:NSMakeRect(margin + labelW + lw + gap, y - h + (h - b.size.height) / 2, b.size.width, b.size.height)];
			y -= h + rowGap;
		}
	});
	savePNG(rep, outPath);
	printf("sheet -> %s\n", outPath.UTF8String);
	return 0;
}

#pragma mark - Live window

// FlippedView is a top-left-origin view that fills itself with a colour.
@interface FlippedView : NSView
@property(strong) NSColor *fill;
@end
@implementation FlippedView
- (BOOL)isFlipped { return YES; }
- (void)drawRect:(NSRect)r {
	if (self.fill) { // the dirty rectangle can reach outside the bounds (clipsToBounds is NO)
		[self.fill setFill];
		NSRectFill(NSIntersectionRect(r, self.bounds));
	}
}
@end

// runLive shows the samples named in namesPath (one per line) in a window, in
// the grid of the contact sheets (flowSheet) wrapped at maxW points, with
// each cell exactly its sample's frame plus 14 pt all round, as package
// rexui/catalog lays out the same names (catalog.Grid), so that captures of
// this window and of the gallery's appkit scene cut into the same cells. The
// window shows for seconds seconds in the appearance named apName, then the
// app quits. Pressed samples are highlighted; focus and inactive samples
// cannot be shown in one key window and draw their normal look.
static int runLive(NSString *namesPath, NSString *apName, double seconds, CGFloat maxW, NSString *dump) {
	NSString *list = [NSString stringWithContentsOfFile:namesPath encoding:NSUTF8StringEncoding error:nil];
	if (!list) {
		fprintf(stderr, "no names file %s\n", namesPath.UTF8String);
		return 1;
	}
	defineSamples();
	NSMutableDictionary<NSString *, Sample *> *byName = [NSMutableDictionary dictionary];
	for (Sample *s in samples) byName[s.name] = s;
	NSAppearance *ap = [NSAppearance appearanceNamed:apName];
	gKey = YES;
	[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];

	const CGFloat gap = 16, capH = 16, margin = 20, titleH = 30, pad = 14;
	NSMutableArray *views = [NSMutableArray array];
	NSMutableArray *names = [NSMutableArray array];
	for (NSString *raw in [list componentsSeparatedByString:@"\n"]) {
		NSString *n = [raw stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceCharacterSet];
		Sample *s = n.length ? byName[n] : nil;
		if (!s) {
			if (n.length) fprintf(stderr, "no sample %s\n", n.UTF8String);
			continue;
		}
		NSView *v = s.build();
		v.appearance = ap;
		if (s.size.width > 0 || s.size.height > 0) {
			NSSize fit = v.fittingSize;
			[v setFrameSize:NSMakeSize(s.size.width > 0 ? s.size.width : fit.width, s.size.height > 0 ? s.size.height : fit.height)];
		} else if ([v respondsToSelector:@selector(sizeToFit)]) {
			[(id)v sizeToFit];
			if (v.frame.size.width <= 0) [v setFrameSize:v.fittingSize];
		} else {
			[v setFrameSize:v.fittingSize];
		}
		[views addObject:v];
		[names addObject:s];
	}
	// lay out as catalog.Grid does
	NSMutableArray *pos = [NSMutableArray array];
	CGFloat x = margin, y = margin + titleH, rowH = 0, W = 0;
	for (NSView *v in views) {
		NSSize c = NSMakeSize(v.frame.size.width + 2 * pad, v.frame.size.height + 2 * pad);
		if (x > margin && x + c.width > maxW - margin) {
			x = margin;
			y += rowH + gap;
			rowH = 0;
		}
		[pos addObject:[NSValue valueWithPoint:NSMakePoint(x, y)]];
		x += c.width + gap;
		rowH = fmax(rowH, c.height + capH);
		W = fmax(W, x);
	}
	CGFloat H = y + rowH + margin;
	W = fmax(W + margin - gap, 400);

	ARWindow *w = [[ARWindow alloc] initWithContentRect:NSMakeRect(0, 0, W, H)
	                                          styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable
	                                            backing:NSBackingStoreBuffered
	                                              defer:NO];
	w.releasedWhenClosed = NO;
	w.appearance = ap;
	w.title = @"AppKitRef — live samples";
	__block NSColor *under, *winBG, *fg2, *fg;
	[ap performAsCurrentDrawingAppearance:^{
		under = NSColor.underPageBackgroundColor;
		winBG = NSColor.windowBackgroundColor;
		fg = NSColor.labelColor;
		fg2 = NSColor.secondaryLabelColor;
	}];
	FlippedView *root = [[FlippedView alloc] initWithFrame:NSMakeRect(0, 0, W, H)];
	root.fill = under;
	w.contentView = root;
	NSTextField *title = [NSTextField labelWithString:@"AppKit — macOS 27.2 samples (tools/appkitref -live)"];
	title.font = [NSFont boldSystemFontOfSize:13];
	title.textColor = fg;
	[title sizeToFit];
	[title setFrameOrigin:NSMakePoint(margin - 2, margin)];
	[root addSubview:title];
	for (NSUInteger i = 0; i < views.count; i++) {
		NSView *v = views[i];
		Sample *s = names[i];
		NSPoint p = [pos[i] pointValue];
		NSSize vs = v.frame.size;
		FlippedView *cell = [[FlippedView alloc] initWithFrame:NSMakeRect(p.x, p.y, vs.width + 2 * pad, vs.height + 2 * pad)];
		cell.fill = winBG;
		[root addSubview:cell];
		[v setFrameOrigin:NSMakePoint(pad, pad)];
		[cell addSubview:v];
		if (s.after) s.after(v, w);
		if (s.pressed && [v isKindOfClass:NSButton.class]) [(NSButton *)v highlight:YES];
		NSTextField *cap = [NSTextField labelWithString:s.name];
		cap.font = [NSFont systemFontOfSize:10];
		cap.textColor = fg2;
		[cap sizeToFit];
		[cap setFrameOrigin:NSMakePoint(p.x - 2, p.y + cell.frame.size.height + 1)]; // a label's text sits 2 pt in
		[root addSubview:cap];
	}
	[w center];
	[w makeKeyAndOrderFront:nil];
	[w makeFirstResponder:nil];
	[NSApp activateIgnoringOtherApps:YES];
	if (dump) { // what the views draw, for checking the window without a screen capture
		[NSTimer scheduledTimerWithTimeInterval:0.5
		                                repeats:NO
		                                  block:^(NSTimer *t) {
			                                  NSBitmapImageRep *rep = newRep(root.bounds.size);
			                                  [root cacheDisplayInRect:root.bounds toBitmapImageRep:rep];
			                                  savePNG(rep, dump);
		                                  }];
	}
	if (gFlash) {
		[NSTimer scheduledTimerWithTimeInterval:0.25
		                                repeats:YES
		                                  block:^(NSTimer *t) {
			                                  flashScrollViews(root);
		                                  }];
	}
	[NSTimer scheduledTimerWithTimeInterval:seconds
	                                repeats:NO
	                                  block:^(NSTimer *t) {
		                                  [NSApp terminate:nil];
	                                  }];
	printf("live: %lu samples, %.0f x %.0f pt, %s, %.0f s\n", (unsigned long)views.count, W, H, apName.UTF8String, seconds);
	fflush(stdout);
	[NSApp run];
	return 0;
}

#pragma mark - main

int main(int argc, const char **argv) {
	@autoreleasepool {
		[ARApp sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyProhibited];
		keep = [NSMutableArray array];
		NSString *outDir = nil, *live = nil, *liveAp = NSAppearanceNameAqua, *dump = nil, *machinePath = nil;
		double seconds = 8, maxW = 1440;
		for (int i = 1; i < argc; i++) {
			if (!strcmp(argv[i], "-o") && i + 1 < argc) outDir = [NSString stringWithUTF8String:argv[++i]];
			else if (!strcmp(argv[i], "-sheet") && i + 2 < argc)
				return pairSheet([NSString stringWithUTF8String:argv[i + 1]], [NSString stringWithUTF8String:argv[i + 2]]);
			else if (!strcmp(argv[i], "-live") && i + 1 < argc) live = [NSString stringWithUTF8String:argv[++i]], gLiveLists = YES;
			else if (!strcmp(argv[i], "-flash")) gFlash = YES;
			else if (!strcmp(argv[i], "-mode") && i + 1 < argc) liveAp = strcmp(argv[++i], "dark") ? NSAppearanceNameAqua : NSAppearanceNameDarkAqua;
			else if (!strcmp(argv[i], "-seconds") && i + 1 < argc) seconds = atof(argv[++i]);
			else if (!strcmp(argv[i], "-width") && i + 1 < argc) maxW = atof(argv[++i]);
			else if (!strcmp(argv[i], "-dump") && i + 1 < argc) dump = [NSString stringWithUTF8String:argv[++i]];
			else if (!strcmp(argv[i], "-machine") && i + 1 < argc) machinePath = [NSString stringWithUTF8String:argv[++i]];
		}
		if (machinePath) { // where this runs, for captures that have no metrics.json of their own (live.sh)
			NSData *m = [NSJSONSerialization dataWithJSONObject:@{@"machine" : machineInfo()} options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
			[m writeToFile:machinePath.stringByStandardizingPath atomically:YES];
			if (!live && !outDir) return 0;
		}
		if (live) return runLive(live, liveAp, seconds, maxW, dump);
		if (!outDir) {
			fprintf(stderr, "usage: AppKitRef -o DIR [-AppleAccentColor N]\n       AppKitRef -sheet MANIFEST.json OUT.png\n"
			                "       AppKitRef -live NAMES.txt [-mode light|dark] [-seconds 8] [-width 1440]\n");
			return 2;
		}
		outDir = outDir.stringByStandardizingPath;
		metricsOut = [NSMutableDictionary dictionary];
		NSOperatingSystemVersion ov = NSProcessInfo.processInfo.operatingSystemVersion;
		NSScreen *scr = NSScreen.mainScreen;
		metricsOut[@"system"] = @{
			@"os" : [NSString stringWithFormat:@"%ld.%ld.%ld", (long)ov.majorVersion, (long)ov.minorVersion, (long)ov.patchVersion],
			@"osBuild" : NSProcessInfo.processInfo.operatingSystemVersionString,
			@"mainScreenScale" : scr ? num(scr.backingScaleFactor) : @0,
			@"renderScale" : @2,
			@"keyWindowSimulated" : @"ARWindow/ARApp report key, main and active; samples marked 'inactive' report not",
		};
		metricsOut[@"behaviour"] = behaviourTable();
		metricsOut[@"fonts"] = fontTable();
		metricsOut[@"colors"] = colorTable();
		metricsOut[@"scrollers"] = scrollerTable();
		metricsOut[@"menus"] = menuTable();
		metricsOut[@"windows"] = windowTable();

		defineSamples();
		NSMutableDictionary *controls = [NSMutableDictionary dictionary];
		for (NSString *apn in @[ NSAppearanceNameAqua, NSAppearanceNameDarkAqua ]) {
			NSMutableDictionary *per = [NSMutableDictionary dictionary];
			for (Sample *s in samples) {
				@autoreleasepool {
					per[s.name] = renderSample(s, apn, outDir);
				}
			}
			per[@"alert"] = alertSample(apn, outDir);
			windowChromeSamples(apn, outDir, per);
			controls[[apn isEqual:NSAppearanceNameAqua] ? @"light" : @"dark"] = per;

			// contact sheet
			NSAppearance *ap = [NSAppearance appearanceNamed:apn];
			NSString *apShort = [apn isEqual:NSAppearanceNameAqua] ? @"light" : @"dark";
			NSMutableArray *cells = [NSMutableArray array];
			NSMutableArray *names = [NSMutableArray array];
			for (Sample *s in samples) [names addObject:s.name];
			[names addObject:@"alert"];
			for (NSString *k in [per.allKeys sortedArrayUsingSelector:@selector(compare:)])
				if ([k hasPrefix:@"window-"]) [names addObject:k];
			for (NSString *n in names) {
				NSImage *im = imageAt2x([NSString stringWithFormat:@"%@/%@/%@.png", outDir, apShort, n]);
				if (im) [cells addObject:@{@"img" : im, @"label" : n}];
			}
			__block NSColor *bg, *fg, *fg2;
			[ap performAsCurrentDrawingAppearance:^{
				bg = [NSColor.underPageBackgroundColor colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
				fg = [NSColor.labelColor colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
				fg2 = [NSColor.secondaryLabelColor colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
			}];
			NSString *title = [NSString stringWithFormat:@"AppKit reference — %@ — macOS %@ — accent %@", apShort, metricsOut[@"system"][@"os"],
			                                             metricsOut[@"behaviour"][@"AppleAccentColor"]];
			savePNG(flowSheet(title, cells, bg, fg, fg2, 1500), [NSString stringWithFormat:@"%@/contact-%@.png", outDir, apShort]);
		}
		metricsOut[@"controls"] = controls;
		metricsOut[@"layouts"] = d3bLayouts ?: @{};
		metricsOut[@"sheets"] = sheetTable();
		metricsOut[@"machine"] = machineInfo(); // after the renders: it spins the run loop (machine.h)
		NSData *js = [NSJSONSerialization dataWithJSONObject:metricsOut options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
		[js writeToFile:[outDir stringByAppendingPathComponent:@"metrics.json"] atomically:YES];
		printf("%lu samples x 2 appearances -> %s\n", (unsigned long)samples.count, outDir.UTF8String);
	}
	return 0;
}
