// AppKitScenes: show pure-AppKit reference windows in the macOS 26+ (Liquid
// Glass) idioms, one scene at a time, and dump their live geometry as JSON so
// the window-level patterns can be measured: the floating glass sidebar, toolbar
// items grouped in glass capsules, content under the toolbar with the scroll
// edge effect, concentric radii, selection, headers and inspectors.
//
// A dev tool, not framework code (like tools/appkitref and tools/gnuishot).
// See README.md.
//
//	AppKitScenes -scene NAME -appearance light|dark -hs DIR [-offscreen] [-tag T] [-phases a,b]
//	AppKitScenes -sheet MANIFEST.json OUT.png     compose a contact sheet
//	AppKitScenes -measure DIR                     measure the captures in DIR
//	AppKitScenes -crop|-probe|-profile|-radius    pixel tools (measure.m)
//
// Scenes: finder, notes, settings, chat. With -hs the tool walks four phases,
// inactive-top, inactive-scrolled, active-top, active-scrolled. For each it
// writes DIR/<scene>-<appearance>-<phase>.json, then a .ready file, and waits
// for a .done file (written by run.sh after GnuiShot captured the window). It
// starts inactive (launched with open -g, never activated) and activates itself
// only for the last two phases, then quits, so focus goes back to the user's
// app. It never changes a setting; the appearance is NSApp.appearance.

#import <AppKit/AppKit.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#include <math.h>

#pragma mark - Helpers

static NSMutableArray *keep; // data sources, delegates and toolbars that must outlive their views

static id num(double v) {
	double r = round(v * 100) / 100;
	return isfinite(r) && fabs(r) < 1e12 ? (id)@(r) : (id)NSNull.null;
}
static NSDictionary *rectJ(NSRect r) { return @{@"x" : num(r.origin.x), @"y" : num(r.origin.y), @"w" : num(r.size.width), @"h" : num(r.size.height)}; }
static NSDictionary *insetsJ(NSEdgeInsets e) { return @{@"top" : num(e.top), @"left" : num(e.left), @"bottom" : num(e.bottom), @"right" : num(e.right)}; }
static BOOL insetsZero(NSEdgeInsets e) { return e.top == 0 && e.left == 0 && e.bottom == 0 && e.right == 0; }

static NSString *hexOf(NSColor *c) {
	if (!c) return nil;
	NSColor *s = [c colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
	if (!s) return [NSString stringWithFormat:@"(%@)", c.colorNameComponent ?: NSStringFromClass(c.class)];
	int r = (int)lround(fmin(fmax(s.redComponent, 0), 1) * 255), g = (int)lround(fmin(fmax(s.greenComponent, 0), 1) * 255),
	    b = (int)lround(fmin(fmax(s.blueComponent, 0), 1) * 255), a = (int)lround(s.alphaComponent * 255);
	return [NSString stringWithFormat:@"#%02X%02X%02X%02X", r, g, b, a];
}

static NSString *colorJ(NSColor *c, NSAppearance *ap) {
	if (!c) return nil;
	__block NSString *h;
	[ap performAsCurrentDrawingAppearance:^{
		h = hexOf(c);
	}];
	if (c.type == NSColorTypeCatalog) return [NSString stringWithFormat:@"%@ %@", c.colorNameComponent, h];
	return h;
}

static NSDictionary *fontJ(NSFont *f) {
	if (!f) return nil;
	NSDictionary *tr = [f.fontDescriptor objectForKey:NSFontTraitsAttribute];
	return @{@"name" : f.fontName ?: @"", @"size" : num(f.pointSize), @"weight" : num([tr[NSFontWeightTrait] doubleValue])};
}

static NSImage *sym(NSString *n) { return [NSImage imageWithSystemSymbolName:n accessibilityDescription:nil]; }

static NSTextField *label(NSString *s, NSFont *f, NSColor *c) {
	NSTextField *t = [NSTextField labelWithString:s];
	if (f) t.font = f;
	if (c) t.textColor = c;
	t.lineBreakMode = NSLineBreakByTruncatingTail;
	t.translatesAutoresizingMaskIntoConstraints = NO;
	return t;
}

static void pin(NSView *v, NSView *in, CGFloat l, CGFloat t, CGFloat r, CGFloat b) {
	v.translatesAutoresizingMaskIntoConstraints = NO;
	[NSLayoutConstraint activateConstraints:@[
		[v.leadingAnchor constraintEqualToAnchor:in.leadingAnchor constant:l], [v.trailingAnchor constraintEqualToAnchor:in.trailingAnchor constant:-r],
		[v.topAnchor constraintEqualToAnchor:in.topAnchor constant:t], [v.bottomAnchor constraintEqualToAnchor:in.bottomAnchor constant:-b]
	]];
}

// tile: a white symbol on a coloured rounded square, the System Settings icon style (drawn here; not a framework asset).
static NSImage *tile(NSString *symbol, NSColor *fill) {
	return [NSImage imageWithSize:NSMakeSize(20, 20)
	                      flipped:NO
	               drawingHandler:^BOOL(NSRect r) {
		               [fill setFill];
		               [[NSBezierPath bezierPathWithRoundedRect:r xRadius:5 yRadius:5] fill];
		               NSImageSymbolConfiguration *cfg = [NSImageSymbolConfiguration configurationWithPointSize:11 weight:NSFontWeightMedium];
		               cfg = [cfg configurationByApplyingConfiguration:[NSImageSymbolConfiguration configurationWithHierarchicalColor:NSColor.whiteColor]];
		               NSImage *s = [sym(symbol) imageWithSymbolConfiguration:cfg];
		               NSSize z = s.size;
		               [s drawInRect:NSMakeRect((20 - z.width) / 2, (20 - z.height) / 2, z.width, z.height)];
		               return YES;
	               }];
}

// avatar: a coloured circle with initials (drawn here).
static NSImage *avatar(NSString *initials, NSColor *fill, CGFloat d) {
	return [NSImage imageWithSize:NSMakeSize(d, d)
	                      flipped:NO
	               drawingHandler:^BOOL(NSRect r) {
		               [fill setFill];
		               [[NSBezierPath bezierPathWithOvalInRect:r] fill];
		               NSDictionary *a = @{NSFontAttributeName : [NSFont systemFontOfSize:d * 0.4 weight:NSFontWeightSemibold], NSForegroundColorAttributeName : NSColor.whiteColor};
		               NSSize z = [initials sizeWithAttributes:a];
		               [initials drawAtPoint:NSMakePoint((d - z.width) / 2, (d - z.height) / 2) withAttributes:a];
		               return YES;
	               }];
}

#pragma mark - Sidebar outline (source list)

typedef NS_ENUM(NSInteger, RowKind) { RowPlain, RowTwoLine, RowChat, RowTile };

@interface Node : NSObject
@property(copy) NSString *title, *symbol, *detail, *preview, *initials;
@property(strong) NSColor *tint;
@property(strong) NSImage *image;
@property NSArray *kids;
@property BOOL group;
@end
@implementation Node
@end

static Node *G(NSString *t, NSArray *kids) {
	Node *n = [Node new];
	n.title = t, n.kids = kids, n.group = YES;
	return n;
}
static Node *I(NSString *t, NSString *s) {
	Node *n = [Node new];
	n.title = t, n.symbol = s;
	return n;
}

@interface Outline : NSObject <NSOutlineViewDataSource, NSOutlineViewDelegate>
@property NSArray *roots;
@property RowKind kind;
@end
@implementation Outline
- (NSInteger)outlineView:(NSOutlineView *)ov numberOfChildrenOfItem:(Node *)item { return item ? item.kids.count : self.roots.count; }
- (id)outlineView:(NSOutlineView *)ov child:(NSInteger)i ofItem:(Node *)item { return item ? item.kids[i] : self.roots[i]; }
- (BOOL)outlineView:(NSOutlineView *)ov isItemExpandable:(Node *)item { return item.kids.count > 0; }
- (BOOL)outlineView:(NSOutlineView *)ov isGroupItem:(Node *)item { return item.group; }
- (BOOL)outlineView:(NSOutlineView *)ov shouldSelectItem:(Node *)item { return !item.group; }
- (NSView *)outlineView:(NSOutlineView *)ov viewForTableColumn:(NSTableColumn *)col item:(Node *)item {
	NSTableCellView *cv = [NSTableCellView new];
	if (item.group) {
		NSTextField *t = label(item.title, nil, nil);
		[cv addSubview:t];
		cv.textField = t;
		[NSLayoutConstraint activateConstraints:@[
			[t.leadingAnchor constraintEqualToAnchor:cv.leadingAnchor constant:2], [t.centerYAnchor constraintEqualToAnchor:cv.centerYAnchor]
		]];
		return cv;
	}
	if (self.kind == RowPlain || self.kind == RowTile) {
		NSTextField *t = label(item.title, nil, nil);
		NSImageView *iv = [NSImageView imageViewWithImage:item.image ?: sym(item.symbol)];
		iv.translatesAutoresizingMaskIntoConstraints = NO;
		if (item.tint) iv.contentTintColor = item.tint;
		[cv addSubview:iv];
		[cv addSubview:t];
		cv.textField = t;
		cv.imageView = iv;
		[NSLayoutConstraint activateConstraints:@[
			[iv.leadingAnchor constraintEqualToAnchor:cv.leadingAnchor constant:3], [iv.centerYAnchor constraintEqualToAnchor:cv.centerYAnchor],
			[t.leadingAnchor constraintEqualToAnchor:iv.trailingAnchor constant:6], [t.centerYAnchor constraintEqualToAnchor:cv.centerYAnchor],
			[t.trailingAnchor constraintLessThanOrEqualToAnchor:cv.trailingAnchor constant:-4]
		]];
		if (self.kind == RowTile) {
			[NSLayoutConstraint activateConstraints:@[ [iv.widthAnchor constraintEqualToConstant:20], [iv.heightAnchor constraintEqualToConstant:20] ]];
		}
		return cv;
	}
	// two-line rows (notes) and chat rows (avatar, name, time, preview)
	NSTextField *t = label(item.title, [NSFont systemFontOfSize:13 weight:NSFontWeightSemibold], nil);
	NSTextField *d = label(item.detail, [NSFont systemFontOfSize:11], NSColor.secondaryLabelColor);
	NSTextField *p = label(item.preview, [NSFont systemFontOfSize:11], NSColor.secondaryLabelColor);
	[cv addSubview:t];
	[cv addSubview:d];
	[cv addSubview:p];
	cv.textField = t;
	if (self.kind == RowTwoLine) {
		[d setContentCompressionResistancePriority:NSLayoutPriorityRequired forOrientation:NSLayoutConstraintOrientationHorizontal];
		[NSLayoutConstraint activateConstraints:@[
			[t.leadingAnchor constraintEqualToAnchor:cv.leadingAnchor constant:6], [t.trailingAnchor constraintLessThanOrEqualToAnchor:cv.trailingAnchor constant:-6],
			[t.topAnchor constraintEqualToAnchor:cv.topAnchor constant:8], [d.leadingAnchor constraintEqualToAnchor:t.leadingAnchor],
			[d.topAnchor constraintEqualToAnchor:t.bottomAnchor constant:2], [p.leadingAnchor constraintEqualToAnchor:d.trailingAnchor constant:6],
			[p.firstBaselineAnchor constraintEqualToAnchor:d.firstBaselineAnchor], [p.trailingAnchor constraintLessThanOrEqualToAnchor:cv.trailingAnchor constant:-6]
		]];
	} else {
		NSImageView *iv = [NSImageView imageViewWithImage:avatar(item.initials, item.tint, 36)];
		iv.translatesAutoresizingMaskIntoConstraints = NO;
		[cv addSubview:iv];
		cv.imageView = iv;
		p.maximumNumberOfLines = 2;
		p.lineBreakMode = NSLineBreakByWordWrapping;
		p.cell.truncatesLastVisibleLine = YES;
		[p setContentCompressionResistancePriority:NSLayoutPriorityDefaultLow forOrientation:NSLayoutConstraintOrientationHorizontal];
		[d setContentCompressionResistancePriority:NSLayoutPriorityRequired forOrientation:NSLayoutConstraintOrientationHorizontal];
		[NSLayoutConstraint activateConstraints:@[
			[iv.leadingAnchor constraintEqualToAnchor:cv.leadingAnchor constant:4], [iv.centerYAnchor constraintEqualToAnchor:cv.centerYAnchor],
			[iv.widthAnchor constraintEqualToConstant:36], [iv.heightAnchor constraintEqualToConstant:36],
			[t.leadingAnchor constraintEqualToAnchor:iv.trailingAnchor constant:8], [t.topAnchor constraintEqualToAnchor:cv.topAnchor constant:8],
			[d.trailingAnchor constraintEqualToAnchor:cv.trailingAnchor constant:-6], [d.firstBaselineAnchor constraintEqualToAnchor:t.firstBaselineAnchor],
			[t.trailingAnchor constraintLessThanOrEqualToAnchor:d.leadingAnchor constant:-4], [p.leadingAnchor constraintEqualToAnchor:t.leadingAnchor],
			[p.topAnchor constraintEqualToAnchor:t.bottomAnchor constant:1], [p.trailingAnchor constraintEqualToAnchor:cv.trailingAnchor constant:-6]
		]];
	}
	return cv;
}
@end

// TallOutline gives the custom rows (notes, chat) their own height; plain sidebars keep the
// system's row size (the delegate must not implement heightOfRowByItem, or the style becomes custom).
@interface TallOutline : Outline
@end
@implementation TallOutline
- (CGFloat)outlineView:(NSOutlineView *)ov heightOfRowByItem:(Node *)item {
	if (item.group) return ov.rowHeight;
	return self.kind == RowChat ? 60 : 52;
}
@end

// sourceList builds the sidebar: a source-list outline in a scroll view, the standard sidebar content.
static NSScrollView *sourceList(NSArray *roots, RowKind kind, NSString *selectTitle) {
	NSScrollView *sv = [NSScrollView new];
	NSOutlineView *ov = [NSOutlineView new];
	ov.style = NSTableViewStyleSourceList;
	ov.headerView = nil;
	ov.floatsGroupRows = NO;
	NSTableColumn *col = [[NSTableColumn alloc] initWithIdentifier:@"c"];
	[ov addTableColumn:col];
	ov.outlineTableColumn = col;
	ov.columnAutoresizingStyle = NSTableViewUniformColumnAutoresizingStyle;
	BOOL tall = kind == RowTwoLine || kind == RowChat;
	// plain sidebars follow the user's sidebar size (General settings), like Finder's
	if (!tall) ov.rowSizeStyle = NSTableViewRowSizeStyleDefault;
	Outline *o = tall ? [TallOutline new] : [Outline new];
	o.roots = roots;
	o.kind = kind;
	[keep addObject:o];
	ov.dataSource = o;
	ov.delegate = o;
	sv.documentView = ov;
	sv.drawsBackground = NO;
	sv.hasVerticalScroller = YES;
	sv.autohidesScrollers = YES;
	[ov reloadData];
	[ov expandItem:nil expandChildren:YES];
	for (NSInteger r = 0; r < ov.numberOfRows; r++) {
		Node *n = [ov itemAtRow:r];
		if ([n.title isEqual:selectTitle]) [ov selectRowIndexes:[NSIndexSet indexSetWithIndex:r] byExtendingSelection:NO];
	}
	return sv;
}

#pragma mark - File table (Finder)

static NSArray *fileNames, *fileKinds, *fileTypes;

@interface Files : NSObject <NSTableViewDataSource, NSTableViewDelegate>
@end
@implementation Files
- (NSInteger)numberOfRowsInTableView:(NSTableView *)tv { return 48; }
- (NSView *)tableView:(NSTableView *)tv viewForTableColumn:(NSTableColumn *)col row:(NSInteger)row {
	NSUInteger i = row % fileNames.count;
	NSTableCellView *cv = [NSTableCellView new];
	NSString *s;
	NSString *c = col.identifier;
	NSString *name = row < (NSInteger)fileNames.count ? fileNames[i] : [NSString stringWithFormat:@"%@ %ld.%@", [fileNames[i] stringByDeletingPathExtension], (long)(row / fileNames.count + 1), [fileNames[i] pathExtension]];
	if ([[fileNames[i] pathExtension] length] == 0 && row >= (NSInteger)fileNames.count) name = [NSString stringWithFormat:@"%@ %ld", fileNames[i], (long)(row / fileNames.count + 1)];
	if ([c isEqual:@"name"]) s = name;
	else if ([c isEqual:@"date"]) s = [NSString stringWithFormat:@"%ld Oct 2026 at %02ld:%02ld", (long)(18 - row % 17), (long)(8 + row % 11), (long)(row * 7 % 60)];
	else if ([c isEqual:@"size"]) s = [fileKinds[i] isEqual:@"Folder"] ? @"--" : [NSString stringWithFormat:@"%ld KB", (long)(row * 137 % 900 + 12)];
	else s = fileKinds[i];
	NSTextField *t = label(s, nil, [c isEqual:@"name"] ? nil : NSColor.secondaryLabelColor);
	if ([c isEqual:@"size"]) t.alignment = NSTextAlignmentRight;
	[cv addSubview:t];
	cv.textField = t;
	if ([c isEqual:@"name"]) {
		UTType *ut = [UTType typeWithIdentifier:fileTypes[i]] ?: UTTypeData;
		NSImageView *iv = [NSImageView imageViewWithImage:[NSWorkspace.sharedWorkspace iconForContentType:ut]];
		iv.translatesAutoresizingMaskIntoConstraints = NO;
		[cv addSubview:iv];
		cv.imageView = iv;
		[NSLayoutConstraint activateConstraints:@[
			[iv.leadingAnchor constraintEqualToAnchor:cv.leadingAnchor constant:2], [iv.centerYAnchor constraintEqualToAnchor:cv.centerYAnchor],
			[iv.widthAnchor constraintEqualToConstant:16], [iv.heightAnchor constraintEqualToConstant:16],
			[t.leadingAnchor constraintEqualToAnchor:iv.trailingAnchor constant:5], [t.trailingAnchor constraintEqualToAnchor:cv.trailingAnchor constant:-2],
			[t.centerYAnchor constraintEqualToAnchor:cv.centerYAnchor]
		]];
	} else {
		[NSLayoutConstraint activateConstraints:@[
			[t.leadingAnchor constraintEqualToAnchor:cv.leadingAnchor constant:2], [t.trailingAnchor constraintEqualToAnchor:cv.trailingAnchor constant:-2],
			[t.centerYAnchor constraintEqualToAnchor:cv.centerYAnchor]
		]];
	}
	return cv;
}
@end

#pragma mark - Toolbar

typedef NSToolbarItem * (^ItemMaker)(NSString *ident);

@interface TB : NSObject <NSToolbarDelegate>
@property NSArray *ids;
@property(copy) ItemMaker make;
@end
@implementation TB
- (NSArray *)toolbarDefaultItemIdentifiers:(NSToolbar *)tb { return self.ids; }
- (NSArray *)toolbarAllowedItemIdentifiers:(NSToolbar *)tb { return self.ids; }
- (NSToolbarItem *)toolbar:(NSToolbar *)tb itemForItemIdentifier:(NSString *)ident willBeInsertedIntoToolbar:(BOOL)f { return self.make(ident); }
@end

static NSToolbarItem *button(NSString *ident, NSString *symbol, NSString *lbl) {
	NSToolbarItem *i = [[NSToolbarItem alloc] initWithItemIdentifier:ident];
	i.image = sym(symbol);
	i.label = lbl;
	i.toolTip = lbl;
	i.target = NSApp;
	i.action = @selector(self);
	return i;
}

// group: plain items in one glass capsule (an NSToolbarItemGroup of subitems, no selection).
static NSToolbarItemGroup *group(NSString *ident, NSArray<NSArray<NSString *> *> *items) {
	NSToolbarItemGroup *g = [[NSToolbarItemGroup alloc] initWithItemIdentifier:ident];
	NSMutableArray *subs = [NSMutableArray array];
	for (NSArray *it in items) [subs addObject:button([ident stringByAppendingFormat:@".%@", it[1]], it[0], it[1])];
	g.subitems = subs;
	g.label = ident;
	return g;
}

static NSToolbarItem *navigation(NSString *ident) {
	NSToolbarItemGroup *g = [NSToolbarItemGroup groupWithItemIdentifier:ident
	                                                             images:@[ sym(@"chevron.left"), sym(@"chevron.right") ]
	                                                      selectionMode:NSToolbarItemGroupSelectionModeMomentary
	                                                             labels:@[ @"Back", @"Forward" ]
	                                                             target:NSApp
	                                                             action:@selector(self)];
	g.navigational = YES;
	g.label = @"Back/Forward";
	return g;
}

static NSToolbarItem *search(NSString *ident) {
	NSSearchToolbarItem *s = [[NSSearchToolbarItem alloc] initWithItemIdentifier:ident];
	s.label = @"Search";
	return s;
}

static NSToolbarItem *menuItem(NSString *ident, NSString *symbol, NSString *lbl) {
	NSMenuToolbarItem *m = [[NSMenuToolbarItem alloc] initWithItemIdentifier:ident];
	m.image = sym(symbol);
	m.label = lbl;
	NSMenu *menu = [NSMenu new];
	[menu addItemWithTitle:@"None" action:nil keyEquivalent:@""];
	[menu addItemWithTitle:@"Kind" action:nil keyEquivalent:@""];
	[menu addItemWithTitle:@"Date Modified" action:nil keyEquivalent:@""];
	m.menu = menu;
	return m;
}

#pragma mark - Scenes

@interface Flipped : NSView
@end
@implementation Flipped
- (BOOL)isFlipped { return YES; }
@end

@interface Scene : NSObject
@property NSString *name;
@property NSWindow *window;
@property NSSplitViewController *split;
@property NSScrollView *main, *side; // scrolled in the scrolled phases
@property CGFloat mainScroll, sideScroll;
@property BOOL mainFromBottom; // chat: the natural position is the bottom
@property NSView *focus;       // the first responder: decides which selection is emphasised
@end
@implementation Scene
@end

static CGFloat gWidth; // -width: the window's content width instead of the scene's
static BOOL gSelectAll; // -selectAll: the finder scene selects every row (one solid colour under the scroll edge, to measure it)

static NSViewController *vc(NSView *v) {
	NSViewController *c = [NSViewController new];
	c.view = v;
	return c;
}

static NSWindow *newWindow(NSString *title, NSSize size, NSWindowToolbarStyle style, TB *tbd, NSString *tbIdent) {
	NSWindow *w = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, size.width, size.height)
	                                          styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable |
	                                                    NSWindowStyleMaskResizable | NSWindowStyleMaskFullSizeContentView
	                                            backing:NSBackingStoreBuffered
	                                              defer:NO];
	w.releasedWhenClosed = NO;
	w.title = title;
	w.toolbarStyle = style;
	NSToolbar *tb = [[NSToolbar alloc] initWithIdentifier:tbIdent];
	tb.delegate = tbd;
	tb.displayMode = NSToolbarDisplayModeIconOnly;
	tb.allowsUserCustomization = NO;
	[keep addObject:tbd];
	w.toolbar = tb;
	return w;
}

static void install(Scene *s, NSArray<NSSplitViewItem *> *items, NSSize size) {
	NSSplitViewController *sp = [NSSplitViewController new];
	for (NSSplitViewItem *it in items) [sp addSplitViewItem:it];
	s.split = sp;
	s.window.contentViewController = sp;
	if (gWidth > 0) size.width = gWidth;
	[s.window setContentSize:size];
}

static Scene *finderScene(void) {
	Scene *s = [Scene new];
	s.name = @"finder";
	TB *tbd = [TB new];
	tbd.ids = @[
		NSToolbarToggleSidebarItemIdentifier, NSToolbarSidebarTrackingSeparatorItemIdentifier, @"nav", NSToolbarFlexibleSpaceItemIdentifier, @"view",
		@"groupby", @"share-tag", @"action", @"search", NSToolbarInspectorTrackingSeparatorItemIdentifier, NSToolbarToggleInspectorItemIdentifier
	];
	tbd.make = ^NSToolbarItem *(NSString *ident) {
		if ([ident isEqual:@"nav"]) return navigation(ident);
		if ([ident isEqual:@"view"]) {
			NSToolbarItemGroup *g = [NSToolbarItemGroup groupWithItemIdentifier:ident
			                                                             images:@[ sym(@"square.grid.2x2"), sym(@"list.bullet"), sym(@"rectangle.split.3x1"), sym(@"square.grid.3x1.below.line.grid.1x2") ]
			                                                      selectionMode:NSToolbarItemGroupSelectionModeSelectOne
			                                                             labels:@[ @"Icons", @"List", @"Columns", @"Gallery" ]
			                                                             target:NSApp
			                                                             action:@selector(self)];
			g.selectedIndex = 1;
			g.label = @"View";
			return g;
		}
		if ([ident isEqual:@"groupby"]) return menuItem(ident, @"square.grid.3x1.folder.badge.plus", @"Group");
		if ([ident isEqual:@"share-tag"]) return group(ident, @[ @[ @"square.and.arrow.up", @"Share" ], @[ @"tag", @"Tags" ] ]);
		if ([ident isEqual:@"action"]) return menuItem(ident, @"ellipsis.circle", @"Action");
		if ([ident isEqual:@"search"]) return search(ident);
		return nil;
	};
	s.window = newWindow(@"Documents", NSMakeSize(1120, 640), NSWindowToolbarStyleUnified, tbd, @"finder");

	Node * (^tag)(NSString *, NSColor *) = ^(NSString *t, NSColor *c) {
		Node *n = I(t, @"circle.fill");
		n.tint = c;
		return n;
	};
	NSArray *roots = @[
		G(@"Favorites", @[
			I(@"AirDrop", @"dot.radiowaves.left.and.right"), I(@"Recents", @"clock"), I(@"Applications", @"square.grid.2x2"),
			I(@"Desktop", @"menubar.dock.rectangle"), I(@"Documents", @"doc"), I(@"Downloads", @"arrow.down.circle")
		]),
		G(@"iCloud", @[ I(@"iCloud Drive", @"icloud"), I(@"Shared", @"folder.badge.person.crop") ]),
		G(@"Locations", @[ I(@"Macintosh HD", @"internaldrive"), I(@"Network", @"network") ]),
		G(@"Tags", @[
			tag(@"Red", NSColor.systemRedColor), tag(@"Orange", NSColor.systemOrangeColor), tag(@"Yellow", NSColor.systemYellowColor),
			tag(@"Green", NSColor.systemGreenColor), tag(@"Blue", NSColor.systemBlueColor), tag(@"Purple", NSColor.systemPurpleColor),
			tag(@"Grey", NSColor.systemGrayColor), I(@"All Tags…", @"circle.dashed")
		]),
	];
	s.side = sourceList(roots, RowPlain, @"Documents");
	NSSplitViewItem *side = [NSSplitViewItem sidebarWithViewController:vc(s.side)];
	side.minimumThickness = 180;
	side.maximumThickness = 260;

	fileNames = @[
		@"Annual Report.pdf", @"Budget 2026.numbers", @"Client Brief.pages", @"Design Review.key", @"Invoices", @"Meeting Notes.txt", @"Moodboard.png",
		@"Photos", @"Product Roadmap.pdf", @"Project Plan.pages", @"README.md", @"Receipts", @"Screenshot 2026-10-12.png", @"Team Offsite.key",
		@"Timesheet.numbers", @"Travel.pdf"
	];
	fileKinds = @[
		@"PDF document", @"Numbers spreadsheet", @"Pages document", @"Keynote presentation", @"Folder", @"Plain text", @"PNG image", @"Folder",
		@"PDF document", @"Pages document", @"Markdown document", @"Folder", @"PNG image", @"Keynote presentation", @"Numbers spreadsheet", @"PDF document"
	];
	fileTypes = @[
		@"com.adobe.pdf", @"com.apple.iwork.numbers.sffnumbers", @"com.apple.iwork.pages.sffpages", @"com.apple.iwork.keynote.sffkey", @"public.folder",
		@"public.plain-text", @"public.png", @"public.folder", @"com.adobe.pdf", @"com.apple.iwork.pages.sffpages", @"net.daringfireball.markdown", @"public.folder",
		@"public.png", @"com.apple.iwork.keynote.sffkey", @"com.apple.iwork.numbers.sffnumbers", @"com.adobe.pdf"
	];
	NSScrollView *sv = [NSScrollView new];
	NSTableView *tv = [NSTableView new];
	tv.style = NSTableViewStyleInset;
	tv.usesAlternatingRowBackgroundColors = YES;
	tv.allowsMultipleSelection = YES;
	NSArray *cols = @[ @[ @"name", @"Name", @260 ], @[ @"date", @"Date Modified", @160 ], @[ @"size", @"Size", @64 ], @[ @"kind", @"Kind", @130 ] ];
	tv.columnAutoresizingStyle = NSTableViewLastColumnOnlyAutoresizingStyle;
	for (NSArray *c in cols) {
		NSTableColumn *col = [[NSTableColumn alloc] initWithIdentifier:c[0]];
		col.title = c[1];
		col.width = [c[2] doubleValue];
		if ([c[0] isEqual:@"size"]) col.headerCell.alignment = NSTextAlignmentRight;
		[tv addTableColumn:col];
	}
	[tv setIndicatorImage:[NSImage imageNamed:@"NSAscendingSortIndicator"] inTableColumn:tv.tableColumns[0]];
	Files *fd = [Files new];
	[keep addObject:fd];
	tv.dataSource = fd;
	tv.delegate = fd;
	sv.documentView = tv;
	sv.hasVerticalScroller = YES;
	sv.drawsBackground = YES;
	[tv reloadData];
	[tv selectRowIndexes:[NSIndexSet indexSetWithIndexesInRange:NSMakeRange(3, 2)] byExtendingSelection:NO];
	if (gSelectAll) [tv selectAll:nil];
	s.main = sv;
	NSSplitViewItem *content = [NSSplitViewItem splitViewItemWithViewController:vc(sv)];
	content.minimumThickness = 360;

	// inspector: a Get Info-like preview pane
	NSStackView *insp = [NSStackView new];
	insp.orientation = NSUserInterfaceLayoutOrientationVertical;
	insp.alignment = NSLayoutAttributeLeading;
	insp.spacing = 6;
	insp.edgeInsets = NSEdgeInsetsMake(16, 16, 16, 16);
	NSImageView *big = [NSImageView imageViewWithImage:[NSWorkspace.sharedWorkspace iconForContentType:UTTypePresentation]];
	big.translatesAutoresizingMaskIntoConstraints = NO;
	[big.widthAnchor constraintEqualToConstant:128].active = YES;
	[big.heightAnchor constraintEqualToConstant:128].active = YES;
	[insp addArrangedSubview:big];
	[insp addArrangedSubview:label(@"Design Review.key", [NSFont systemFontOfSize:15 weight:NSFontWeightSemibold], nil)];
	[insp addArrangedSubview:label(@"Keynote presentation – 2.4 MB", [NSFont systemFontOfSize:11], NSColor.secondaryLabelColor)];
	NSTextField *infoHeader = label(@"Information", [NSFont systemFontOfSize:13 weight:NSFontWeightSemibold], nil);
	[insp addArrangedSubview:infoHeader];
	[insp setCustomSpacing:14 afterView:insp.arrangedSubviews[2]];
	for (NSArray *kv in @[ @[ @"Created", @"2 Oct 2026 at 10:12" ], @[ @"Modified", @"15 Oct 2026 at 16:40" ], @[ @"Last opened", @"Today at 09:41" ] ]) {
		NSStackView *row = [NSStackView stackViewWithViews:@[
			label(kv[0], [NSFont systemFontOfSize:11], NSColor.secondaryLabelColor), label(kv[1], [NSFont systemFontOfSize:11], nil)
		]];
		row.spacing = 8;
		[insp addArrangedSubview:row];
	}
	NSView *inspHost = [NSView new];
	[inspHost addSubview:insp];
	insp.translatesAutoresizingMaskIntoConstraints = NO;
	[NSLayoutConstraint activateConstraints:@[
		[insp.leadingAnchor constraintEqualToAnchor:inspHost.leadingAnchor], [insp.trailingAnchor constraintEqualToAnchor:inspHost.trailingAnchor],
		[insp.topAnchor constraintEqualToAnchor:inspHost.safeAreaLayoutGuide.topAnchor]
	]];
	NSSplitViewItem *inspector = [NSSplitViewItem inspectorWithViewController:vc(inspHost)];
	inspector.minimumThickness = 230;
	inspector.maximumThickness = 230;
	install(s, @[ side, content, inspector ], NSMakeSize(1120, 640));
	s.focus = tv; // the file list, as in Finder: the sidebar selection is then unemphasised
	s.mainScroll = 130;
	s.sideScroll = 60;
	return s;
}

static NSString *notesBody(void) {
	return @"Agenda\n"
	       @"• Review the launch checklist and owners\n"
	       @"• Confirm the design freeze date\n"
	       @"• Decide on the beta group size\n\n"
	       @"Decisions\n"
	       @"The beta opens to the waiting list in two waves of five hundred people each. The second wave starts once the crash rate stays under a tenth of a percent for three days.\n\n"
	       @"Pricing stays as proposed. The annual plan keeps its discount, and the monthly plan gets a seven-day trial.\n\n"
	       @"Open questions\n"
	       @"Who owns the support inbox during the first week? Do we need a status page before the beta, or is the in-app banner enough?\n\n"
	       @"Action items\n"
	       @"Sam drafts the announcement and the FAQ by Thursday. Priya prepares the onboarding flow and the empty states. Jordan checks the analytics events against the funnel we agreed on.\n\n"
	       @"Notes from the design review\n"
	       @"The sidebar felt heavy in dark mode. We will try the lighter selection, smaller section headers and more space above each group. The toolbar keeps three groups: formatting, insert and share.\n\n"
	       @"The editor should keep a comfortable measure of about seventy characters. Headings use the title style, and the body stays at the system size so it matches the rest of the app.\n\n"
	       @"Next meeting\n"
	       @"Thursday at 10:00, same room. Bring the updated timeline and the list of risks.\n\n"
	       @"Risks\n"
	       @"The migration of old notes may take longer than planned on large libraries. The sync service needs a load test before the second wave. Translations for three languages are still missing.\n\n"
	       @"Follow-up\n"
	       @"Collect feedback from the first wave through the in-app form and summarise it every Friday.\n";
}

static Scene *notesScene(void) {
	Scene *s = [Scene new];
	s.name = @"notes";
	TB *tbd = [TB new];
	tbd.ids = @[
		NSToolbarToggleSidebarItemIdentifier, @"delete", NSToolbarSidebarTrackingSeparatorItemIdentifier, @"compose", NSToolbarFlexibleSpaceItemIdentifier,
		@"format", @"insert", @"share", @"search"
	];
	tbd.make = ^NSToolbarItem *(NSString *ident) {
		if ([ident isEqual:@"delete"]) return button(ident, @"trash", @"Delete");
		if ([ident isEqual:@"compose"]) return button(ident, @"square.and.pencil", @"New Note");
		if ([ident isEqual:@"format"]) return group(ident, @[ @[ @"textformat", @"Format" ], @[ @"checklist", @"Checklist" ], @[ @"tablecells", @"Table" ] ]);
		if ([ident isEqual:@"insert"]) return button(ident, @"photo.on.rectangle", @"Media");
		if ([ident isEqual:@"share"]) return button(ident, @"square.and.arrow.up", @"Share");
		if ([ident isEqual:@"search"]) return search(ident);
		return nil;
	};
	s.window = newWindow(@"Notes", NSMakeSize(900, 600), NSWindowToolbarStyleUnifiedCompact, tbd, @"notes");
	Node * (^note)(NSString *, NSString *, NSString *) = ^(NSString *t, NSString *d, NSString *p) {
		Node *n = I(t, nil);
		n.detail = d, n.preview = p;
		return n;
	};
	NSArray *roots = @[
		G(@"Today", @[
			note(@"Launch meeting", @"09:41", @"Agenda: review the launch checklist"), note(@"Grocery list", @"08:15", @"Oat milk, lemons, rice, coffee beans"),
		]),
		G(@"Previous 7 Days", @[
			note(@"Trip ideas", @"Friday", @"Lisbon in spring, the coast road"), note(@"Book notes", @"Thursday", @"Chapter 3: the habit loop"),
			note(@"Recipe: lentil soup", @"Tuesday", @"Onion, carrot, celery, red lentils"), note(@"Gift ideas", @"Monday", @"A good notebook, a plant"),
		]),
		G(@"Previous 30 Days", @[
			note(@"Workout plan", @"02/10/2026", @"Mon: run 5 km, Wed: strength"), note(@"Podcast list", @"28/09/2026", @"Design, history and science"),
			note(@"Garden", @"25/09/2026", @"Plant the bulbs before the first frost"), note(@"Quotes", @"21/09/2026", @"Simplicity is a great virtue"),
		]),
	];
	s.side = sourceList(roots, RowTwoLine, @"Launch meeting");
	NSSplitViewItem *side = [NSSplitViewItem sidebarWithViewController:vc(s.side)];
	side.minimumThickness = 240;
	side.maximumThickness = 320;

	NSScrollView *sv = [NSScrollView new];
	sv.hasVerticalScroller = YES;
	sv.autohidesScrollers = YES;
	NSTextView *tv = [[NSTextView alloc] initWithFrame:NSMakeRect(0, 0, 600, 600)];
	tv.minSize = NSMakeSize(0, 0);
	tv.maxSize = NSMakeSize(1e7, 1e7);
	tv.verticallyResizable = YES;
	tv.autoresizingMask = NSViewWidthSizable;
	tv.textContainer.widthTracksTextView = YES;
	tv.textContainerInset = NSMakeSize(24, 12);
	tv.drawsBackground = YES;
	tv.backgroundColor = NSColor.textBackgroundColor;
	NSMutableAttributedString *a = [[NSMutableAttributedString alloc]
	    initWithString:@"Launch meeting\n"
	        attributes:@{NSFontAttributeName : [NSFont preferredFontForTextStyle:NSFontTextStyleTitle1 options:@{}], NSForegroundColorAttributeName : NSColor.labelColor}];
	NSString *body = notesBody();
	NSMutableAttributedString *b = [[NSMutableAttributedString alloc]
	    initWithString:body
	        attributes:@{NSFontAttributeName : [NSFont systemFontOfSize:13], NSForegroundColorAttributeName : NSColor.labelColor}];
	for (NSString *h in @[ @"Agenda", @"Decisions", @"Open questions", @"Action items", @"Notes from the design review", @"Next meeting", @"Risks", @"Follow-up" ]) {
		NSRange r = [body rangeOfString:[h stringByAppendingString:@"\n"]];
		if (r.location != NSNotFound) [b addAttribute:NSFontAttributeName value:[NSFont systemFontOfSize:15 weight:NSFontWeightSemibold] range:r];
	}
	[a appendAttributedString:b];
	[tv.textStorage setAttributedString:a];
	sv.documentView = tv;
	s.main = sv;
	NSSplitViewItem *content = [NSSplitViewItem splitViewItemWithViewController:vc(sv)];
	content.minimumThickness = 360;
	install(s, @[ side, content ], NSMakeSize(900, 600));
	s.focus = s.side.documentView; // the note list: its selection is emphasised
	s.mainScroll = 160;
	return s;
}

static NSView *formRow(NSString *title, NSView *control) {
	NSView *row = [NSView new];
	NSTextField *t = label(title, nil, nil);
	[row addSubview:t];
	control.translatesAutoresizingMaskIntoConstraints = NO;
	[row addSubview:control];
	[NSLayoutConstraint activateConstraints:@[
		[row.heightAnchor constraintGreaterThanOrEqualToConstant:40], [t.leadingAnchor constraintEqualToAnchor:row.leadingAnchor constant:10],
		[t.centerYAnchor constraintEqualToAnchor:row.centerYAnchor], [control.trailingAnchor constraintEqualToAnchor:row.trailingAnchor constant:-10],
		[control.centerYAnchor constraintEqualToAnchor:row.centerYAnchor], [t.trailingAnchor constraintLessThanOrEqualToAnchor:control.leadingAnchor constant:-12]
	]];
	return row;
}

static NSPopUpButton *popup(NSArray *items, NSInteger sel) {
	NSPopUpButton *p = [[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:NO];
	[p addItemsWithTitles:items];
	[p selectItemAtIndex:sel];
	return p;
}

static NSSwitch *toggle(BOOL on) {
	NSSwitch *s = [NSSwitch new];
	s.state = on ? NSControlStateValueOn : NSControlStateValueOff;
	return s;
}

static NSSlider *slider(double v) {
	NSSlider *s = [NSSlider sliderWithValue:v minValue:0 maxValue:1 target:nil action:nil];
	[s.widthAnchor constraintEqualToConstant:200].active = YES;
	return s;
}

// formGroup: rows in an NSBox (primary, untitled), AppKit's group box, separated by hairlines.
static NSView *formGroup(NSArray<NSView *> *rows) {
	NSBox *box = [NSBox new];
	box.boxType = NSBoxPrimary;
	box.titlePosition = NSNoTitle;
	box.contentViewMargins = NSZeroSize;
	NSStackView *st = [NSStackView new];
	st.orientation = NSUserInterfaceLayoutOrientationVertical;
	st.spacing = 0;
	st.alignment = NSLayoutAttributeWidth;
	for (NSUInteger i = 0; i < rows.count; i++) {
		if (i > 0) {
			NSBox *sep = [NSBox new];
			sep.boxType = NSBoxSeparator;
			[st addArrangedSubview:sep];
		}
		[st addArrangedSubview:rows[i]];
	}
	box.contentView = st;
	return box;
}

static Scene *settingsScene(void) {
	Scene *s = [Scene new];
	s.name = @"settings";
	TB *tbd = [TB new];
	tbd.ids = @[ NSToolbarSidebarTrackingSeparatorItemIdentifier, @"nav" ];
	tbd.make = ^NSToolbarItem *(NSString *ident) {
		if ([ident isEqual:@"nav"]) return navigation(ident);
		return nil;
	};
	s.window = newWindow(@"Appearance", NSMakeSize(820, 620), NSWindowToolbarStyleUnified, tbd, @"settings");
	Node * (^t)(NSString *, NSString *, NSColor *) = ^(NSString *title, NSString *symbol, NSColor *c) {
		Node *n = I(title, symbol);
		n.image = tile(symbol, c);
		return n;
	};
	NSArray *roots = @[
		G(@"Connections", @[
			t(@"Wi-Fi", @"wifi", NSColor.systemBlueColor), t(@"Bluetooth", @"wave.3.right", NSColor.systemBlueColor),
			t(@"Network", @"network", NSColor.systemBlueColor)
		]),
		G(@"System", @[
			t(@"Notifications", @"bell.badge.fill", NSColor.systemRedColor), t(@"Sound", @"speaker.wave.2.fill", NSColor.systemPinkColor),
			t(@"Focus", @"moon.fill", NSColor.systemIndigoColor), t(@"Screen Time", @"hourglass", NSColor.systemIndigoColor)
		]),
		G(@"Personalisation", @[
			t(@"General", @"gearshape.fill", NSColor.systemGrayColor), t(@"Appearance", @"circle.lefthalf.filled", NSColor.labelColor),
			t(@"Accessibility", @"accessibility", NSColor.systemBlueColor), t(@"Control Centre", @"switch.2", NSColor.systemGrayColor),
			t(@"Desktop & Dock", @"menubar.dock.rectangle", NSColor.labelColor), t(@"Displays", @"sun.max.fill", NSColor.systemBlueColor),
			t(@"Wallpaper", @"photo", NSColor.systemTealColor), t(@"Screen Saver", @"tv", NSColor.systemTealColor)
		]),
		G(@"Security", @[
			t(@"Battery", @"battery.100", NSColor.systemGreenColor), t(@"Lock Screen", @"lock.fill", NSColor.labelColor),
			t(@"Privacy & Security", @"hand.raised.fill", NSColor.systemBlueColor), t(@"Users & Groups", @"person.2.fill", NSColor.systemBlueColor)
		]),
	];
	NSScrollView *sideScroll = sourceList(roots, RowTile, @"Appearance");
	s.side = sideScroll;
	// the System Settings sidebar has a search field at its top, under the traffic lights
	NSView *sideHost = [NSView new];
	NSSearchField *sf = [NSSearchField new];
	sf.placeholderString = @"Search";
	sf.translatesAutoresizingMaskIntoConstraints = NO;
	sideScroll.translatesAutoresizingMaskIntoConstraints = NO;
	[sideHost addSubview:sideScroll];
	[sideHost addSubview:sf];
	[NSLayoutConstraint activateConstraints:@[
		[sf.leadingAnchor constraintEqualToAnchor:sideHost.safeAreaLayoutGuide.leadingAnchor constant:10],
		[sf.trailingAnchor constraintEqualToAnchor:sideHost.safeAreaLayoutGuide.trailingAnchor constant:-10],
		[sf.topAnchor constraintEqualToAnchor:sideHost.safeAreaLayoutGuide.topAnchor constant:4],
		[sideScroll.leadingAnchor constraintEqualToAnchor:sideHost.leadingAnchor], [sideScroll.trailingAnchor constraintEqualToAnchor:sideHost.trailingAnchor],
		[sideScroll.topAnchor constraintEqualToAnchor:sf.bottomAnchor constant:6], [sideScroll.bottomAnchor constraintEqualToAnchor:sideHost.bottomAnchor]
	]];
	NSSplitViewItem *side = [NSSplitViewItem sidebarWithViewController:vc(sideHost)];
	side.minimumThickness = 215;
	side.maximumThickness = 215;

	NSStackView *form = [NSStackView new];
	form.orientation = NSUserInterfaceLayoutOrientationVertical;
	form.alignment = NSLayoutAttributeWidth;
	form.spacing = 8;
	form.edgeInsets = NSEdgeInsetsMake(20, 20, 24, 20);
	NSView * (^header)(NSString *) = ^(NSString *h) {
		NSView *v = [NSView new];
		NSTextField *t = label(h, [NSFont systemFontOfSize:13 weight:NSFontWeightSemibold], nil);
		[v addSubview:t];
		[NSLayoutConstraint activateConstraints:@[
			[t.leadingAnchor constraintEqualToAnchor:v.leadingAnchor constant:10], [t.topAnchor constraintEqualToAnchor:v.topAnchor],
			[t.bottomAnchor constraintEqualToAnchor:v.bottomAnchor], [t.trailingAnchor constraintLessThanOrEqualToAnchor:v.trailingAnchor]
		]];
		return v;
	};
	[form addArrangedSubview:formGroup(@[
		formRow(@"Appearance", popup(@[ @"Auto", @"Light", @"Dark" ], 0)), formRow(@"Accent colour", popup(@[ @"Multicolour", @"Blue", @"Purple", @"Pink" ], 1)),
		formRow(@"Highlight colour", popup(@[ @"Accent colour", @"Graphite" ], 0))
	])];
	[form addArrangedSubview:formGroup(@[
		formRow(@"Sidebar icon size", popup(@[ @"Small", @"Medium", @"Large" ], 1)),
		formRow(@"Allow wallpaper tinting in windows", toggle(YES))
	])];
	[form addArrangedSubview:header(@"Scroll bars")];
	[form addArrangedSubview:formGroup(@[
		formRow(@"Show scroll bars", popup(@[ @"Automatically based on mouse or trackpad", @"When scrolling", @"Always" ], 0)),
		formRow(@"Click in the scroll bar to", popup(@[ @"Jump to the next page", @"Jump to the spot that’s clicked" ], 0))
	])];
	[form addArrangedSubview:header(@"Display")];
	[form addArrangedSubview:formGroup(@[
		formRow(@"Brightness", slider(0.7)), formRow(@"Automatically adjust brightness", toggle(YES)), formRow(@"True Tone", toggle(NO))
	])];
	[form addArrangedSubview:header(@"Sound")];
	[form addArrangedSubview:formGroup(@[
		formRow(@"Alert volume", slider(0.45)), formRow(@"Play sound on startup", toggle(YES)), formRow(@"Play feedback when volume is changed", toggle(NO))
	])];
	[form addArrangedSubview:header(@"Energy")];
	[form addArrangedSubview:formGroup(@[
		formRow(@"Low power mode", popup(@[ @"Never", @"Always", @"Only on battery" ], 2)), formRow(@"Wake for network access", toggle(YES)),
		formRow(@"Optimise video streaming on battery", toggle(NO))
	])];
	for (NSView *v in form.arrangedSubviews)
		if (![v isKindOfClass:NSBox.class]) [form setCustomSpacing:6 afterView:v];
	for (NSUInteger i = 0; i + 1 < form.arrangedSubviews.count; i++)
		if ([form.arrangedSubviews[i] isKindOfClass:NSBox.class] && ![form.arrangedSubviews[i + 1] isKindOfClass:NSBox.class])
			[form setCustomSpacing:18 afterView:form.arrangedSubviews[i]];
	form.translatesAutoresizingMaskIntoConstraints = NO;
	NSScrollView *sv = [NSScrollView new];
	sv.hasVerticalScroller = YES;
	sv.autohidesScrollers = YES;
	sv.drawsBackground = YES;
	NSClipView *clip = [NSClipView new];
	clip.drawsBackground = YES;
	sv.contentView = clip;
	// a flipped document view so the form starts at the top
	NSView *doc = [Flipped new];
	doc.translatesAutoresizingMaskIntoConstraints = NO;
	[doc addSubview:form];
	sv.documentView = doc;
	pin(form, doc, 0, 0, 0, 0);
	[NSLayoutConstraint activateConstraints:@[
		[doc.leadingAnchor constraintEqualToAnchor:clip.leadingAnchor], [doc.trailingAnchor constraintEqualToAnchor:clip.trailingAnchor],
		[doc.topAnchor constraintEqualToAnchor:clip.topAnchor]
	]];
	s.main = sv;
	NSSplitViewItem *content = [NSSplitViewItem splitViewItemWithViewController:vc(sv)];
	content.minimumThickness = 480;
	install(s, @[ side, content ], NSMakeSize(820, 620));
	s.focus = sideScroll.documentView; // the sidebar, as in System Settings
	s.mainScroll = 120;
	return s;
}

// Chat: messages are drawn bubbles (not an AppKit control); the composer is an NSGlassEffectView
// capsule in a bottom-aligned split-view accessory, as Apple's guidance places floating controls.
@interface Bubbles : NSView
@property NSArray<NSArray *> *msgs; // @[ @(mine), text ]
@end
@implementation Bubbles
- (BOOL)isFlipped { return YES; }
- (NSArray *)layoutFor:(CGFloat)w {
	NSMutableArray *out = [NSMutableArray array];
	CGFloat y = 16;
	NSDictionary *a = @{NSFontAttributeName : [NSFont systemFontOfSize:13]};
	for (NSArray *m in self.msgs) {
		if ([m[0] integerValue] == 2) { // timestamp
			NSSize z = [m[1] sizeWithAttributes:@{NSFontAttributeName : [NSFont systemFontOfSize:11 weight:NSFontWeightMedium]}];
			[out addObject:@[ m, [NSValue valueWithRect:NSMakeRect((w - z.width) / 2, y + 6, z.width, z.height)] ]];
			y += z.height + 18;
			continue;
		}
		CGFloat maxW = fmin(w * 0.62, 380);
		NSRect br = [m[1] boundingRectWithSize:NSMakeSize(maxW - 24, 1e5) options:NSStringDrawingUsesLineFragmentOrigin attributes:a];
		CGFloat bw = ceil(br.size.width) + 24, bh = ceil(br.size.height) + 14;
		CGFloat x = [m[0] boolValue] ? w - 20 - bw : 20;
		[out addObject:@[ m, [NSValue valueWithRect:NSMakeRect(x, y, bw, bh)] ]];
		y += bh + 6;
	}
	[out addObject:@(y + 10)];
	return out;
}
- (void)drawRect:(NSRect)dirty {
	NSArray *L = [self layoutFor:self.bounds.size.width];
	for (NSUInteger i = 0; i + 1 < L.count; i++) {
		NSArray *m = L[i][0];
		NSRect r = [L[i][1] rectValue];
		if ([m[0] integerValue] == 2) {
			[m[1] drawInRect:r withAttributes:@{NSFontAttributeName : [NSFont systemFontOfSize:11 weight:NSFontWeightMedium], NSForegroundColorAttributeName : NSColor.secondaryLabelColor}];
			continue;
		}
		BOOL mine = [m[0] boolValue];
		[(mine ? NSColor.systemBlueColor : NSColor.unemphasizedSelectedContentBackgroundColor) setFill];
		[[NSBezierPath bezierPathWithRoundedRect:r xRadius:fmin(17, r.size.height / 2) yRadius:fmin(17, r.size.height / 2)] fill];
		[m[1] drawInRect:NSInsetRect(r, 12, 7)
		    withAttributes:@{NSFontAttributeName : [NSFont systemFontOfSize:13], NSForegroundColorAttributeName : mine ? NSColor.whiteColor : NSColor.labelColor}];
	}
}
- (NSSize)intrinsicContentSize { return NSMakeSize(NSViewNoIntrinsicMetric, [[[self layoutFor:fmax(self.bounds.size.width, 400)] lastObject] doubleValue]); }
- (void)setFrameSize:(NSSize)s {
	BOOL changed = s.width != self.frame.size.width;
	[super setFrameSize:s];
	if (changed) [self invalidateIntrinsicContentSize];
}
@end

static Scene *chatScene(void) {
	Scene *s = [Scene new];
	s.name = @"chat";
	TB *tbd = [TB new];
	tbd.ids = @[ NSToolbarToggleSidebarItemIdentifier, NSToolbarFlexibleSpaceItemIdentifier, @"compose", NSToolbarSidebarTrackingSeparatorItemIdentifier, NSToolbarFlexibleSpaceItemIdentifier, @"calls", @"info" ];
	tbd.make = ^NSToolbarItem *(NSString *ident) {
		if ([ident isEqual:@"compose"]) return button(ident, @"square.and.pencil", @"New Message");
		if ([ident isEqual:@"calls"]) return group(ident, @[ @[ @"video", @"Video" ], @[ @"phone", @"Call" ] ]);
		if ([ident isEqual:@"info"]) return button(ident, @"info.circle", @"Details");
		return nil;
	};
	s.window = newWindow(@"Sam Chen", NSMakeSize(900, 620), NSWindowToolbarStyleUnified, tbd, @"chat");
	Node * (^c)(NSString *, NSString *, NSString *, NSString *, NSColor *) = ^(NSString *n, NSString *ini, NSString *time, NSString *prev, NSColor *col) {
		Node *x = I(n, nil);
		x.initials = ini, x.detail = time, x.preview = prev, x.tint = col;
		return x;
	};
	NSArray *roots = @[
		c(@"Sam Chen", @"SC", @"09:41", @"Sounds good, see you at ten. I’ll bring the timeline.", NSColor.systemBlueColor),
		c(@"Design Team", @"DT", @"09:12", @"Priya: the new sidebar is in the build", NSColor.systemPurpleColor),
		c(@"Alex Rivera", @"AR", @"Yesterday", @"Thanks for the notes!", NSColor.systemOrangeColor),
		c(@"Jordan Lee", @"JL", @"Yesterday", @"Can we move the call to Thursday?", NSColor.systemGreenColor),
		c(@"Priya Patel", @"PP", @"Saturday", @"Photo", NSColor.systemPinkColor),
		c(@"Book Club", @"BC", @"Friday", @"Next one: The Overstory", NSColor.systemTealColor),
		c(@"Morgan Diaz", @"MD", @"Thursday", @"On my way", NSColor.systemIndigoColor),
		c(@"Taylor Kim", @"TK", @"14/10/2026", @"Haha yes", NSColor.systemRedColor),
		c(@"Chris Novak", @"CN", @"12/10/2026", @"Let me check and get back to you", NSColor.systemBrownColor),
		c(@"Riley Moore", @"RM", @"09/10/2026", @"Great, thanks", NSColor.systemMintColor),
	];
	NSScrollView *sideScroll = sourceList(roots, RowChat, @"Sam Chen");
	s.side = sideScroll;
	NSView *sideHost = [NSView new];
	NSSearchField *sf = [NSSearchField new];
	sf.placeholderString = @"Search";
	sf.translatesAutoresizingMaskIntoConstraints = NO;
	sideScroll.translatesAutoresizingMaskIntoConstraints = NO;
	[sideHost addSubview:sideScroll];
	[sideHost addSubview:sf];
	[NSLayoutConstraint activateConstraints:@[
		[sf.leadingAnchor constraintEqualToAnchor:sideHost.safeAreaLayoutGuide.leadingAnchor constant:10],
		[sf.trailingAnchor constraintEqualToAnchor:sideHost.safeAreaLayoutGuide.trailingAnchor constant:-10],
		[sf.topAnchor constraintEqualToAnchor:sideHost.safeAreaLayoutGuide.topAnchor constant:4],
		[sideScroll.leadingAnchor constraintEqualToAnchor:sideHost.leadingAnchor], [sideScroll.trailingAnchor constraintEqualToAnchor:sideHost.trailingAnchor],
		[sideScroll.topAnchor constraintEqualToAnchor:sf.bottomAnchor constant:6], [sideScroll.bottomAnchor constraintEqualToAnchor:sideHost.bottomAnchor]
	]];
	NSSplitViewItem *side = [NSSplitViewItem sidebarWithViewController:vc(sideHost)];
	side.minimumThickness = 280;
	side.maximumThickness = 280;

	Bubbles *b = [Bubbles new];
	NSMutableArray *msgs = [NSMutableArray array];
	NSArray *script = @[
		@[ @2, @"Yesterday 18:20" ], @[ @0, @"Did you get a chance to look at the launch checklist?" ], @[ @1, @"Yes, most of it looks fine." ],
		@[ @1, @"Two things: the beta group size, and who owns the support inbox in the first week." ], @[ @0, @"Good catch. I think five hundred per wave is enough." ],
		@[ @0, @"For support, maybe Jordan? He knows the onboarding flow best." ], @[ @1, @"Makes sense. I’ll ask him tomorrow." ],
		@[ @0, @"Also, the design review moved to Thursday." ], @[ @1, @"Thursday works for me." ], @[ @2, @"Today 08:55" ],
		@[ @0, @"Morning! Are we still on for ten?" ], @[ @1, @"Yes. I’ll bring the updated timeline and the list of risks." ],
		@[ @0, @"Perfect. Can you add the sync load test to the risks? It worries me a bit for the second wave." ],
		@[ @1, @"Already there. I also added the missing translations." ], @[ @0, @"Great." ],
		@[ @1, @"One more question: do we want a status page before the beta, or is the in-app banner enough?" ],
		@[ @0, @"The banner is enough for the first wave. Let’s revisit before the second one." ], @[ @1, @"Agreed." ],
		@[ @0, @"Sounds good, see you at ten. I’ll bring the timeline." ]
	];
	[msgs addObjectsFromArray:script];
	b.msgs = msgs;
	NSScrollView *sv = [NSScrollView new];
	sv.hasVerticalScroller = YES;
	sv.autohidesScrollers = YES;
	sv.drawsBackground = YES;
	sv.backgroundColor = NSColor.textBackgroundColor;
	b.translatesAutoresizingMaskIntoConstraints = NO;
	sv.documentView = b;
	[NSLayoutConstraint activateConstraints:@[
		[b.leadingAnchor constraintEqualToAnchor:sv.contentView.leadingAnchor], [b.trailingAnchor constraintEqualToAnchor:sv.contentView.trailingAnchor],
		[b.topAnchor constraintEqualToAnchor:sv.contentView.topAnchor]
	]];
	s.main = sv;
	s.mainFromBottom = YES;
	NSSplitViewItem *content = [NSSplitViewItem splitViewItemWithViewController:vc(sv)];
	content.minimumThickness = 420;

	// composer: a glass capsule floating over the bottom of the messages
	NSGlassEffectView *glass = [NSGlassEffectView new];
	glass.cornerRadius = 18;
	if (@available(macOS 27.0, *)) glass.effectIsInteractive = YES;
	NSView *inner = [NSView new];
	NSButton *plus = [NSButton buttonWithImage:sym(@"plus") target:nil action:nil];
	plus.bordered = NO;
	NSTextField *field = [NSTextField textFieldWithString:@""];
	field.placeholderString = @"Message";
	field.bordered = NO;
	field.drawsBackground = NO;
	field.focusRingType = NSFocusRingTypeNone;
	NSButton *mic = [NSButton buttonWithImage:sym(@"waveform") target:nil action:nil];
	mic.bordered = NO;
	for (NSView *v in @[ plus, field, mic ]) {
		v.translatesAutoresizingMaskIntoConstraints = NO;
		[inner addSubview:v];
	}
	[NSLayoutConstraint activateConstraints:@[
		[inner.heightAnchor constraintEqualToConstant:36], [plus.leadingAnchor constraintEqualToAnchor:inner.leadingAnchor constant:12],
		[plus.centerYAnchor constraintEqualToAnchor:inner.centerYAnchor], [field.leadingAnchor constraintEqualToAnchor:plus.trailingAnchor constant:10],
		[field.centerYAnchor constraintEqualToAnchor:inner.centerYAnchor], [mic.leadingAnchor constraintEqualToAnchor:field.trailingAnchor constant:8],
		[mic.trailingAnchor constraintEqualToAnchor:inner.trailingAnchor constant:-12], [mic.centerYAnchor constraintEqualToAnchor:inner.centerYAnchor]
	]];
	glass.contentView = inner;
	NSView *accHost = [NSView new];
	glass.translatesAutoresizingMaskIntoConstraints = NO;
	[accHost addSubview:glass];
	pin(glass, accHost, 16, 8, 16, 12);
	NSSplitViewItemAccessoryViewController *acc = [NSSplitViewItemAccessoryViewController new];
	acc.view = accHost;
	[content addBottomAlignedAccessoryViewController:acc];
	install(s, @[ side, content ], NSMakeSize(900, 620));
	s.focus = field; // the composer: the conversation list selection is unemphasised
	s.mainScroll = 180;
	return s;
}

#pragma mark - Geometry dump

static NSString *gTag; // a variant name appended to the scene name in file names
static CGFloat gWinH;
static NSAppearance *gAp;
static NSInteger gNodes;

static NSRect winRect(NSView *v) {
	NSRect r = [v convertRect:v.bounds toView:nil];
	r.origin.y = gWinH - NSMaxY(r);
	return r;
}

static NSString *materialName(NSVisualEffectMaterial m) {
	switch (m) {
	case NSVisualEffectMaterialTitlebar: return @"titlebar";
	case NSVisualEffectMaterialSelection: return @"selection";
	case NSVisualEffectMaterialMenu: return @"menu";
	case NSVisualEffectMaterialPopover: return @"popover";
	case NSVisualEffectMaterialSidebar: return @"sidebar";
	case NSVisualEffectMaterialHeaderView: return @"headerView";
	case NSVisualEffectMaterialSheet: return @"sheet";
	case NSVisualEffectMaterialWindowBackground: return @"windowBackground";
	case NSVisualEffectMaterialHUDWindow: return @"hudWindow";
	case NSVisualEffectMaterialFullScreenUI: return @"fullScreenUI";
	case NSVisualEffectMaterialToolTip: return @"toolTip";
	case NSVisualEffectMaterialContentBackground: return @"contentBackground";
	case NSVisualEffectMaterialUnderWindowBackground: return @"underWindowBackground";
	case NSVisualEffectMaterialUnderPageBackground: return @"underPageBackground";
	default: return [NSString stringWithFormat:@"%ld", (long)m];
	}
}

static NSDictionary *layerJ(CALayer *l) {
	if (!l) return nil;
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	if (l.cornerRadius > 0) {
		d[@"cornerRadius"] = num(l.cornerRadius);
		d[@"cornerCurve"] = l.cornerCurve;
		if (l.maskedCorners != (kCALayerMinXMinYCorner | kCALayerMaxXMinYCorner | kCALayerMinXMaxYCorner | kCALayerMaxXMaxYCorner)) d[@"maskedCorners"] = @(l.maskedCorners);
	}
	if (l.masksToBounds) d[@"masksToBounds"] = @YES;
	if (l.backgroundColor) d[@"backgroundColor"] = hexOf([NSColor colorWithCGColor:l.backgroundColor]);
	if (l.borderWidth > 0) {
		d[@"borderWidth"] = num(l.borderWidth);
		if (l.borderColor) d[@"borderColor"] = hexOf([NSColor colorWithCGColor:l.borderColor]);
	}
	if (l.opacity < 1) d[@"opacity"] = num(l.opacity);
	if (l.filters.count) d[@"filters"] = [l.filters valueForKey:@"description"];
	if (l.backgroundFilters.count) d[@"backgroundFilters"] = [l.backgroundFilters valueForKey:@"description"];
	if (l.compositingFilter) d[@"compositingFilter"] = [l.compositingFilter description];
	if (l.mask) d[@"mask"] = NSStringFromClass(l.mask.class);
	if (l.shadowOpacity > 0) d[@"shadow"] = @{@"opacity" : num(l.shadowOpacity), @"radius" : num(l.shadowRadius), @"offset" : @[ num(l.shadowOffset.width), num(l.shadowOffset.height) ]};
	if (l.sublayers.count) d[@"sublayers"] = @(l.sublayers.count);
	return d.count ? d : nil;
}

static NSDictionary *viewJ(NSView *v, int depth) {
	gNodes++;
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	d[@"class"] = NSStringFromClass(v.class);
	d[@"frame"] = rectJ(winRect(v));
	if (v.hidden) d[@"hidden"] = @YES;
	if (v.alphaValue < 1) d[@"alpha"] = num(v.alphaValue);
	if (v.identifier.length) d[@"identifier"] = v.identifier;
	NSDictionary *lj = layerJ(v.layer);
	if (lj) d[@"layer"] = lj;
	if (!insetsZero(v.safeAreaInsets)) d[@"safeAreaInsets"] = insetsJ(v.safeAreaInsets);
	if (@available(macOS 27.0, *)) {
		NSViewCornerRadii *cr = v.effectiveCornerRadii;
		if (cr) d[@"effectiveCornerRadii"] = @{@"tl" : num(cr.topLeft), @"tr" : num(cr.topRight), @"bl" : num(cr.bottomLeft), @"br" : num(cr.bottomRight)};
		if (v.cornerConfiguration) d[@"cornerConfiguration"] = v.cornerConfiguration.description;
	}
	if ([v isKindOfClass:NSVisualEffectView.class]) {
		NSVisualEffectView *e = (id)v;
		d[@"material"] = materialName(e.material);
		d[@"blendingMode"] = e.blendingMode == NSVisualEffectBlendingModeBehindWindow ? @"behindWindow" : @"withinWindow";
		d[@"state"] = @[ @"followsWindowActiveState", @"active", @"inactive" ][e.state];
		if (e.emphasized) d[@"emphasized"] = @YES;
	}
	if (@available(macOS 26.0, *)) {
		if ([v isKindOfClass:NSGlassEffectView.class]) {
			NSGlassEffectView *g = (id)v;
			d[@"glass"] = @{@"cornerRadius" : num(g.cornerRadius), @"style" : g.style == NSGlassEffectViewStyleClear ? @"clear" : @"regular", @"tint" : colorJ(g.tintColor, gAp) ?: @"none"};
		}
		if ([v isKindOfClass:NSGlassEffectContainerView.class]) d[@"glassContainer"] = @{@"spacing" : num(((NSGlassEffectContainerView *)v).spacing)};
	}
	if ([v isKindOfClass:NSTextField.class]) {
		NSTextField *t = (id)v;
		NSString *str = t.stringValue;
		if (str.length > 60) str = [[str substringToIndex:60] stringByAppendingString:@"…"];
		d[@"text"] = str;
		if (t.placeholderString) d[@"placeholder"] = t.placeholderString;
		d[@"font"] = fontJ(t.font);
		d[@"textColor"] = colorJ(t.textColor, v.effectiveAppearance);
		if (t.drawsBackground) d[@"backgroundColor"] = colorJ(t.backgroundColor, v.effectiveAppearance);
		d[@"bezeled"] = @(t.bezeled);
	}
	if ([v isKindOfClass:NSButton.class]) {
		NSButton *b = (id)v;
		d[@"button"] = @{@"title" : b.title ?: @"", @"bezelStyle" : @(b.bezelStyle), @"bordered" : @(b.bordered), @"controlSize" : @(b.controlSize), @"image" : b.image ? NSStringFromSize(b.image.size) : @""};
	} else if ([v isKindOfClass:NSControl.class]) {
		d[@"controlSize"] = @(((NSControl *)v).controlSize);
	}
	if ([v isKindOfClass:NSImageView.class]) {
		NSImageView *iv = (id)v;
		if (iv.contentTintColor) d[@"contentTintColor"] = colorJ(iv.contentTintColor, v.effectiveAppearance);
		if (iv.image) d[@"imageSize"] = @[ num(iv.image.size.width), num(iv.image.size.height) ];
	}
	if ([v isKindOfClass:NSBox.class]) {
		NSBox *b = (id)v;
		d[@"box"] = @{@"type" : @(b.boxType), @"cornerRadius" : num(b.cornerRadius), @"fillColor" : colorJ(b.fillColor, v.effectiveAppearance) ?: @"", @"borderColor" : colorJ(b.borderColor, v.effectiveAppearance) ?: @""};
	}
	if ([v isKindOfClass:NSScrollView.class]) {
		NSScrollView *s = (id)v;
		d[@"scroll"] = @{
			@"contentInsets" : insetsJ(s.contentInsets),
			@"automaticallyAdjustsContentInsets" : @(s.automaticallyAdjustsContentInsets),
			@"scrollerInsets" : insetsJ(s.scrollerInsets),
			@"drawsBackground" : @(s.drawsBackground),
			@"backgroundColor" : colorJ(s.backgroundColor, v.effectiveAppearance) ?: @"",
			@"clipBounds" : rectJ(s.contentView.bounds),
			@"documentVisibleRect" : rectJ(s.documentVisibleRect),
			@"scrollerStyle" : s.scrollerStyle == NSScrollerStyleOverlay ? @"overlay" : @"legacy",
		};
	}
	if ([v isKindOfClass:NSTableRowView.class]) {
		NSTableRowView *r = (id)v;
		d[@"row"] = @{@"selected" : @(r.selected), @"emphasized" : @(r.emphasized), @"groupRowStyle" : @(r.groupRowStyle), @"selectionHighlightStyle" : @(r.selectionHighlightStyle), @"interiorBackgroundStyle" : @(r.interiorBackgroundStyle)};
	}
	if ([v isKindOfClass:NSSplitView.class]) {
		NSSplitView *s = (id)v;
		d[@"split"] = @{@"dividerThickness" : num(s.dividerThickness), @"dividerStyle" : @(s.dividerStyle), @"vertical" : @(s.vertical)};
	}
	if (depth < 40 && gNodes < 6000) {
		NSMutableArray *kids = [NSMutableArray array];
		for (NSView *c in v.subviews) [kids addObject:viewJ(c, depth + 1)];
		if (kids.count) d[@"subviews"] = kids;
	}
	return d;
}

static NSString *behaviorName(NSSplitViewItemBehavior b) {
	switch (b) {
	case NSSplitViewItemBehaviorSidebar: return @"sidebar";
	case NSSplitViewItemBehaviorContentList: return @"contentList";
	case NSSplitViewItemBehaviorInspector: return @"inspector";
	default: return @"default";
	}
}

static void collect(NSView *v, Class c, NSMutableArray *out) {
	if ([v isKindOfClass:c]) [out addObject:v];
	for (NSView *s in v.subviews) collect(s, c, out);
}

static NSDictionary *tableJ(NSTableView *tv) {
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	d[@"class"] = NSStringFromClass(tv.class);
	d[@"style"] = @(tv.style);
	d[@"effectiveStyle"] = @(tv.effectiveStyle);
	d[@"rowHeight"] = num(tv.rowHeight);
	d[@"effectiveRowSizeStyle"] = @(tv.effectiveRowSizeStyle);
	d[@"intercellSpacing"] = @[ num(tv.intercellSpacing.width), num(tv.intercellSpacing.height) ];
	d[@"frame"] = rectJ(winRect(tv));
	d[@"selectionHighlightStyle"] = @(tv.selectionHighlightStyle);
	d[@"alternating"] = @(tv.usesAlternatingRowBackgroundColors);
	d[@"backgroundColor"] = colorJ(tv.backgroundColor, tv.effectiveAppearance);
	d[@"isFirstResponder"] = @(tv.window.firstResponder == tv);
	if (tv.headerView) d[@"header"] = rectJ(winRect(tv.headerView));
	if ([tv isKindOfClass:NSOutlineView.class]) d[@"indentationPerLevel"] = num(((NSOutlineView *)tv).indentationPerLevel);
	NSRange vis = [tv rowsInRect:tv.visibleRect];
	NSMutableArray *rows = [NSMutableArray array];
	for (NSUInteger r = vis.location; r < NSMaxRange(vis) && rows.count < 40; r++) {
		NSMutableDictionary *rd = [NSMutableDictionary dictionary];
		NSRect rr = [tv convertRect:[tv rectOfRow:r] toView:nil];
		rr.origin.y = gWinH - NSMaxY(rr);
		rd[@"row"] = @(r);
		rd[@"rect"] = rectJ(rr);
		rd[@"selected"] = @([tv isRowSelected:r]);
		if ([tv isKindOfClass:NSOutlineView.class]) {
			NSOutlineView *ov = (id)tv;
			id item = [ov itemAtRow:r];
			rd[@"group"] = @([ov.delegate respondsToSelector:@selector(outlineView:isGroupItem:)] && [ov.delegate outlineView:ov isGroupItem:item]);
			rd[@"level"] = @([ov levelForRow:r]);
		}
		NSTableRowView *rv = [tv rowViewAtRow:r makeIfNecessary:NO];
		if (rv) {
			rd[@"rowView"] = rectJ(winRect(rv));
			rd[@"rowViewClass"] = NSStringFromClass(rv.class);
			rd[@"emphasized"] = @(rv.emphasized);
			NSView *cell = [rv viewAtColumn:0];
			if ([cell isKindOfClass:NSTableCellView.class]) {
				NSTableCellView *cv = (id)cell;
				rd[@"cell"] = rectJ(winRect(cv));
				if (cv.textField) rd[@"text"] = @{@"frame" : rectJ(winRect(cv.textField)), @"font" : fontJ(cv.textField.font) ?: @{}, @"color" : colorJ(cv.textField.textColor, cv.effectiveAppearance) ?: @"", @"string" : cv.textField.stringValue};
				if (cv.imageView) rd[@"image"] = @{@"frame" : rectJ(winRect(cv.imageView)), @"tint" : colorJ(cv.imageView.contentTintColor, cv.effectiveAppearance) ?: @"none"};
				rd[@"backgroundStyle"] = @(cv.backgroundStyle);
			}
		}
		[rows addObject:rd];
	}
	d[@"rows"] = rows;
	return d;
}

static NSDictionary *dumpScene(Scene *s, NSString *phase) {
	NSWindow *w = s.window;
	gWinH = w.frame.size.height;
	gAp = w.effectiveAppearance;
	gNodes = 0;
	NSMutableDictionary *d = [NSMutableDictionary dictionary];
	NSOperatingSystemVersion ov = NSProcessInfo.processInfo.operatingSystemVersion;
	d[@"system"] = @{
		@"os" : [NSString stringWithFormat:@"%ld.%ld.%ld", (long)ov.majorVersion, (long)ov.minorVersion, (long)ov.patchVersion],
		@"osBuild" : NSProcessInfo.processInfo.operatingSystemVersionString,
		@"screenScale" : num(w.backingScaleFactor),
		@"accent" : colorJ(NSColor.controlAccentColor, gAp),
		@"AppleAccentColor" : [NSUserDefaults.standardUserDefaults objectForKey:@"AppleAccentColor"] ?: @"(unset: multicolour/blue)",
		@"reduceTransparency" : @(NSWorkspace.sharedWorkspace.accessibilityDisplayShouldReduceTransparency),
		@"increaseContrast" : @(NSWorkspace.sharedWorkspace.accessibilityDisplayShouldIncreaseContrast),
	};
	d[@"scene"] = s.name;
	if (gTag.length) d[@"tag"] = gTag;
	d[@"phase"] = phase;
	d[@"pid"] = @(getpid());
	d[@"coordinates"] = @"points, window top-left origin (the window frame includes the titlebar: full-size content view)";
	NSRect cl = w.contentLayoutRect;
	NSView *frameView = w.contentView.superview;
	NSMutableDictionary *wd = [@{
		@"frame" : rectJ(w.frame),
		@"windowNumber" : @(w.windowNumber),
		@"title" : w.title,
		@"toolbarStyle" : @(w.toolbarStyle),
		@"contentLayoutRect" : rectJ(NSMakeRect(cl.origin.x, gWinH - NSMaxY(cl), cl.size.width, cl.size.height)),
		@"titlebarPlusToolbarHeight" : num(gWinH - NSMaxY(cl)),
		@"isKey" : @(w.isKeyWindow),
		@"isMain" : @(w.isMainWindow),
		@"appActive" : @(NSApp.isActive),
		@"appearance" : gAp.name,
		@"backgroundColor" : colorJ(w.backgroundColor, gAp) ?: @"",
		@"titlebarSeparatorStyle" : @(w.titlebarSeparatorStyle),
		@"firstResponder" : NSStringFromClass(w.firstResponder.class) ?: @"",
		@"occlusionVisible" : @((w.occlusionState & NSWindowOcclusionStateVisible) != 0),
	} mutableCopy];
	SEL crs = NSSelectorFromString(@"_cornerRadius");
	if ([w respondsToSelector:crs]) {
		CGFloat (*f)(id, SEL) = (void *)[w methodForSelector:crs];
		wd[@"cornerRadius(private _cornerRadius)"] = num(f(w, crs));
	}
	NSMutableDictionary *tl = [NSMutableDictionary dictionary];
	NSDictionary *bn = @{@"close" : @(NSWindowCloseButton), @"miniaturize" : @(NSWindowMiniaturizeButton), @"zoom" : @(NSWindowZoomButton)};
	for (NSString *k in bn) {
		NSButton *b = [w standardWindowButton:[bn[k] unsignedIntegerValue]];
		if (b) tl[k] = rectJ(winRect(b));
	}
	wd[@"trafficLights"] = tl;
	d[@"window"] = wd;

	// split view items
	NSMutableArray *items = [NSMutableArray array];
	for (NSSplitViewItem *it in s.split.splitViewItems) {
		NSMutableDictionary *id_ = [@{
			@"behavior" : behaviorName(it.behavior),
			@"viewFrame" : rectJ(winRect(it.viewController.view)),
			@"collapsed" : @(it.collapsed),
			@"allowsFullHeightLayout" : @(it.allowsFullHeightLayout),
			@"titlebarSeparatorStyle" : @(it.titlebarSeparatorStyle),
			@"safeAreaInsets" : insetsJ(it.viewController.view.safeAreaInsets),
		} mutableCopy];
		if (@available(macOS 26.0, *)) {
			id_[@"automaticallyAdjustsSafeAreaInsets"] = @(it.automaticallyAdjustsSafeAreaInsets);
			NSMutableArray *acc = [NSMutableArray array];
			for (NSSplitViewItemAccessoryViewController *a in it.bottomAlignedAccessoryViewControllers) [acc addObject:rectJ(winRect(a.view))];
			if (acc.count) id_[@"bottomAccessories"] = acc;
		}
		// the superview chain from the item's view up to the split view: where the glass/material wraps it
		NSMutableArray *chain = [NSMutableArray array];
		for (NSView *p = it.viewController.view.superview; p && p != s.split.splitView.superview; p = p.superview) {
			NSMutableDictionary *pd = [@{@"class" : NSStringFromClass(p.class), @"frame" : rectJ(winRect(p))} mutableCopy];
			NSDictionary *lj = layerJ(p.layer);
			if (lj) pd[@"layer"] = lj;
			if (@available(macOS 27.0, *)) {
				if (p.effectiveCornerRadii) pd[@"effectiveCornerRadii"] = @[ num(p.effectiveCornerRadii.topLeft), num(p.effectiveCornerRadii.topRight), num(p.effectiveCornerRadii.bottomLeft), num(p.effectiveCornerRadii.bottomRight) ];
			}
			[chain addObject:pd];
		}
		id_[@"superviews"] = chain;
		[items addObject:id_];
	}
	d[@"splitItems"] = items;
	d[@"splitView"] = @{@"frame" : rectJ(winRect(s.split.splitView)), @"dividerThickness" : num(s.split.splitView.dividerThickness), @"dividerStyle" : @(s.split.splitView.dividerStyle)};

	// toolbar items as the toolbar reports them
	NSMutableArray *tbi = [NSMutableArray array];
	for (NSToolbarItem *it in w.toolbar.items) {
		NSMutableDictionary *x = [@{@"id" : it.itemIdentifier, @"class" : NSStringFromClass(it.class), @"label" : it.label ?: @"", @"bordered" : @(it.bordered), @"navigational" : @(it.navigational), @"visible" : @(it.visible)} mutableCopy];
		if (@available(macOS 26.0, *)) x[@"style"] = @(it.style);
		if ([it isKindOfClass:NSToolbarItemGroup.class]) x[@"subitems"] = [((NSToolbarItemGroup *)it).subitems valueForKey:@"itemIdentifier"];
		if (it.view) x[@"viewFrame"] = rectJ(winRect(it.view));
		[tbi addObject:x];
	}
	d[@"toolbarItems"] = tbi;

	NSMutableArray *tables = [NSMutableArray array];
	NSMutableArray *found = [NSMutableArray array];
	collect(frameView, NSTableView.class, found);
	for (NSTableView *t in found) [tables addObject:tableJ(t)];
	d[@"tables"] = tables;

	NSMutableDictionary *colors = [NSMutableDictionary dictionary];
	NSDictionary *cs = @{
		@"windowBackgroundColor" : NSColor.windowBackgroundColor, @"underPageBackgroundColor" : NSColor.underPageBackgroundColor,
		@"controlBackgroundColor" : NSColor.controlBackgroundColor, @"textBackgroundColor" : NSColor.textBackgroundColor,
		@"separatorColor" : NSColor.separatorColor, @"gridColor" : NSColor.gridColor, @"labelColor" : NSColor.labelColor,
		@"secondaryLabelColor" : NSColor.secondaryLabelColor, @"tertiaryLabelColor" : NSColor.tertiaryLabelColor,
		@"selectedContentBackgroundColor" : NSColor.selectedContentBackgroundColor,
		@"unemphasizedSelectedContentBackgroundColor" : NSColor.unemphasizedSelectedContentBackgroundColor,
		@"alternatingContentBackgroundColors0" : NSColor.alternatingContentBackgroundColors[0],
		@"alternatingContentBackgroundColors1" : NSColor.alternatingContentBackgroundColors[1], @"controlAccentColor" : NSColor.controlAccentColor,
		@"quaternarySystemFillColor" : NSColor.quaternarySystemFillColor, @"quinarySystemFillColor" : NSColor.quinarySystemFillColor,
		@"headerTextColor" : NSColor.headerTextColor,
	};
	for (NSString *k in cs) colors[k] = colorJ(cs[k], gAp);
	d[@"colors"] = colors;
	d[@"tree"] = viewJ(frameView, 0);
	d[@"treeNodes"] = @(gNodes);
	return d;
}

#pragma mark - Phases

static NSMutableDictionary *gTops; // each scroll view's natural top, as AppKit first placed it

static void scrollTo(NSScrollView *sv, CGFloat fromNatural, BOOL fromBottom) {
	if (!sv) return;
	NSClipView *c = sv.contentView;
	NSView *doc = sv.documentView;
	[doc layoutSubtreeIfNeeded];
	if (!gTops) gTops = [NSMutableDictionary dictionary];
	NSValue *key = [NSValue valueWithPointer:(__bridge const void *)sv];
	if (!gTops[key]) gTops[key] = @(doc.isFlipped ? c.bounds.origin.y : -sv.contentInsets.top);
	// a table's header sits under the toolbar too: AppKit's natural top includes it
	CGFloat top = fmin([gTops[key] doubleValue], -sv.contentInsets.top);
	CGFloat maxY = fmax(top, NSHeight(doc.frame) - NSHeight(c.bounds) + sv.contentInsets.bottom);
	CGFloat y = fromBottom ? maxY - fromNatural : top + fromNatural;
	y = fmin(fmax(y, top), maxY);
	if (!doc.isFlipped) y = maxY - (y - top); // unflipped document: origin at the bottom
	[c scrollToPoint:NSMakePoint(c.bounds.origin.x, y)];
	[sv reflectScrolledClipView:c];
}

static Scene *gScene;
static NSString *gHS, *gAppearance;
static int gPhase;
static NSArray *gPhases;
static NSDate *gPhaseStart;
static BOOL gOffscreen;

static NSString *phasePath(NSString *phase, NSString *ext) {
	return [gHS stringByAppendingPathComponent:[NSString stringWithFormat:@"%@%@-%@-%@.%@", gScene.name, gTag.length ? [@"_" stringByAppendingString:gTag] : @"", gAppearance, phase, ext]];
}

static void writeJSON(id obj, NSString *path) {
	NSData *js = [NSJSONSerialization dataWithJSONObject:obj options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
	[js writeToFile:path atomically:YES];
}

static void runPhase(void);

static void afterSettle(NSTimeInterval t, void (^b)(void)) { dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(t * NSEC_PER_SEC)), dispatch_get_main_queue(), b); }

static void waitDone(void) {
	NSString *phase = gPhases[gPhase];
	if ([NSFileManager.defaultManager fileExistsAtPath:phasePath(phase, @"done")] || -[gPhaseStart timeIntervalSinceNow] > 30) {
		gPhase++;
		runPhase();
		return;
	}
	afterSettle(0.05, ^{
		waitDone();
	});
}

static void waitActive(int tries) {
	if ((NSApp.isActive && gScene.window.isKeyWindow) || tries > 160) {
		afterSettle(0.8, ^{
			NSString *phase = gPhases[gPhase];
			writeJSON(dumpScene(gScene, phase), phasePath(phase, @"json"));
			[@"" writeToFile:phasePath(phase, @"ready") atomically:YES encoding:NSUTF8StringEncoding error:nil];
			gPhaseStart = [NSDate date];
			waitDone();
		});
		return;
	}
	if (tries == 30) [@"" writeToFile:[gHS stringByAppendingPathComponent:@"need-activate"] atomically:YES encoding:NSUTF8StringEncoding error:nil];
	afterSettle(0.05, ^{
		waitActive(tries + 1);
	});
}

static void runPhase(void) {
	if (gPhase >= (int)gPhases.count) {
		[gScene.window close];
		[NSApp terminate:nil];
		return;
	}
	NSString *phase = gPhases[gPhase];
	BOOL scrolled = [phase hasSuffix:@"scrolled"], active = [phase hasPrefix:@"active"];
	scrollTo(gScene.main, scrolled ? gScene.mainScroll : 0, gScene.mainFromBottom);
	scrollTo(gScene.side, scrolled ? gScene.sideScroll : 0, NO);
	if (gOffscreen) {
		[gScene.window.contentView.superview layoutSubtreeIfNeeded];
		writeJSON(dumpScene(gScene, phase), phasePath(phase, @"json"));
		gPhase++;
		runPhase();
		return;
	}
	if (active && !NSApp.isActive) {
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
		[NSApp activateIgnoringOtherApps:YES];
#pragma clang diagnostic pop
		[gScene.window makeKeyAndOrderFront:nil];
	}
	waitActive(active ? 0 : 1000);
}

#pragma mark - Contact sheets

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
	[NSFileManager.defaultManager createDirectoryAtPath:path.stringByDeletingLastPathComponent withIntermediateDirectories:YES attributes:nil error:nil];
	[[r representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:path atomically:YES];
}

// sheet composes a grid: manifest {title, columns, scale, dark, cells: [{label, path}]}; images are 2x PNGs, drawn at
// scale x their point size, each row as tall as its tallest image, captions under the images.
static int sheet(NSString *manifest, NSString *outPath) {
	NSDictionary *m = [NSJSONSerialization JSONObjectWithData:[NSData dataWithContentsOfFile:manifest] options:0 error:nil];
	if (!m) {
		fprintf(stderr, "bad manifest %s\n", manifest.UTF8String);
		return 1;
	}
	NSInteger cols = [m[@"columns"] integerValue] ?: 4;
	CGFloat sc = [m[@"scale"] doubleValue] ?: 0.5;
	NSArray *cells = m[@"cells"];
	NSMutableArray *imgs = [NSMutableArray array], *sizes = [NSMutableArray array];
	NSInteger rows = (cells.count + cols - 1) / cols;
	CGFloat colW[64] = {0}, rowH[256] = {0};
	for (NSUInteger i = 0; i < cells.count; i++) {
		NSImage *im = [[NSImage alloc] initWithContentsOfFile:cells[i][@"path"]];
		NSImageRep *r = im.representations.firstObject;
		NSSize s = r ? NSMakeSize(r.pixelsWide / 2.0 * sc, r.pixelsHigh / 2.0 * sc) : NSMakeSize(100, 60);
		[imgs addObject:im ?: NSNull.null];
		[sizes addObject:[NSValue valueWithSize:s]];
		colW[i % cols] = fmax(colW[i % cols], s.width);
		rowH[i / cols] = fmax(rowH[i / cols], s.height);
	}
	const CGFloat margin = 24, gap = 18, cap = 18, titleH = 34;
	CGFloat W = margin * 2 + (cols - 1) * gap, H = margin * 2 + titleH + (rows - 1) * gap;
	for (NSInteger c = 0; c < cols; c++) W += colW[c];
	for (NSInteger r = 0; r < rows; r++) H += rowH[r] + cap;
	BOOL dark = [m[@"dark"] boolValue];
	NSBitmapImageRep *rep = newRep(NSMakeSize(ceil(W), ceil(H)));
	[NSGraphicsContext saveGraphicsState];
	NSGraphicsContext.currentContext = [NSGraphicsContext graphicsContextWithBitmapImageRep:rep];
	[(dark ? [NSColor colorWithWhite:0.12 alpha:1] : [NSColor colorWithWhite:0.86 alpha:1]) setFill];
	NSRectFill(NSMakeRect(0, 0, W, H));
	NSColor *fg = dark ? NSColor.whiteColor : NSColor.blackColor, *fg2 = dark ? [NSColor colorWithWhite:0.7 alpha:1] : [NSColor colorWithWhite:0.3 alpha:1];
	[m[@"title"] drawAtPoint:NSMakePoint(margin, H - margin - 20) withAttributes:@{NSFontAttributeName : [NSFont boldSystemFontOfSize:16], NSForegroundColorAttributeName : fg}];
	CGFloat top = H - margin - titleH;
	for (NSInteger r = 0; r < rows; r++) {
		CGFloat x = margin;
		for (NSInteger c = 0; c < cols; c++) {
			NSUInteger i = r * cols + c;
			if (i >= cells.count) break;
			NSImage *im = imgs[i] == NSNull.null ? nil : imgs[i];
			NSSize s = [sizes[i] sizeValue];
			if (im)
				[im drawInRect:NSMakeRect(x, top - s.height, s.width, s.height) fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1 respectFlipped:YES hints:@{NSImageHintInterpolation : @(NSImageInterpolationHigh)}];
			[cells[i][@"label"] drawAtPoint:NSMakePoint(x, top - s.height - cap + 3) withAttributes:@{NSFontAttributeName : [NSFont systemFontOfSize:11], NSForegroundColorAttributeName : fg2}];
			x += colW[c] + gap;
		}
		top -= rowH[r] + cap + gap;
	}
	[NSGraphicsContext.currentContext flushGraphics];
	[NSGraphicsContext restoreGraphicsState];
	savePNG(rep, outPath);
	printf("sheet -> %s\n", outPath.UTF8String);
	return 0;
}

int measureMain(int argc, const char **argv, int i); // measure.m

#pragma mark - main

int main(int argc, const char **argv) {
	@autoreleasepool {
		NSString *sceneName = nil;
		for (int i = 1; i < argc; i++) {
			if (!strcmp(argv[i], "-sheet") && i + 2 < argc) return sheet([NSString stringWithUTF8String:argv[i + 1]], [NSString stringWithUTF8String:argv[i + 2]]);
			if (!strcmp(argv[i], "-measure") || !strcmp(argv[i], "-crop") || !strcmp(argv[i], "-probe") || !strcmp(argv[i], "-profile") || !strcmp(argv[i], "-radius")) return measureMain(argc, argv, i);
			if (!strcmp(argv[i], "-scene") && i + 1 < argc) sceneName = [NSString stringWithUTF8String:argv[++i]];
			else if (!strcmp(argv[i], "-appearance") && i + 1 < argc) gAppearance = [NSString stringWithUTF8String:argv[++i]];
			else if (!strcmp(argv[i], "-hs") && i + 1 < argc) gHS = [NSString stringWithUTF8String:argv[++i]];
			else if (!strcmp(argv[i], "-offscreen")) gOffscreen = YES;
			else if (!strcmp(argv[i], "-width") && i + 1 < argc) gWidth = atof(argv[++i]);
			else if (!strcmp(argv[i], "-selectAll")) gSelectAll = YES;
			else if (!strcmp(argv[i], "-phases") && i + 1 < argc) gPhases = [[NSString stringWithUTF8String:argv[++i]] componentsSeparatedByString:@","];
			else if (!strcmp(argv[i], "-tag") && i + 1 < argc) gTag = [NSString stringWithUTF8String:argv[++i]];
		}
		if (!sceneName || !gHS || !([gAppearance isEqual:@"light"] || [gAppearance isEqual:@"dark"])) {
			fprintf(stderr, "usage: AppKitScenes -scene finder|notes|settings|chat -appearance light|dark -hs DIR [-offscreen] [-tag T] [-phases a,b] [-width W] [-selectAll]\n"
			                "       AppKitScenes -sheet MANIFEST.json OUT.png\n       AppKitScenes -measure DIR\n"
			                "       AppKitScenes -crop|-probe|-profile|-radius IN.png ... (see measure.m)\n");
			return 64;
		}
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		NSApp.appearance = [NSAppearance appearanceNamed:[gAppearance isEqual:@"dark"] ? NSAppearanceNameDarkAqua : NSAppearanceNameAqua];
		keep = [NSMutableArray array];
		[NSFileManager.defaultManager createDirectoryAtPath:gHS withIntermediateDirectories:YES attributes:nil error:nil];
		if ([sceneName isEqual:@"finder"]) gScene = finderScene();
		else if ([sceneName isEqual:@"notes"]) gScene = notesScene();
		else if ([sceneName isEqual:@"settings"]) gScene = settingsScene();
		else if ([sceneName isEqual:@"chat"]) gScene = chatScene();
		else {
			fprintf(stderr, "unknown scene %s\n", sceneName.UTF8String);
			return 64;
		}
		if (!gPhases) gPhases = @[ @"inactive-top", @"inactive-scrolled", @"active-top", @"active-scrolled" ];
		NSWindow *w = gScene.window;
		NSRect vf = NSScreen.mainScreen.visibleFrame;
		[w setFrameOrigin:NSMakePoint(NSMidX(vf) - w.frame.size.width / 2, NSMidY(vf) - w.frame.size.height / 2)];
		// watchdog: never outlive a stuck driver
		afterSettle(120, ^{
			exit(5);
		});
		if (gScene.focus) [w makeFirstResponder:gScene.focus];
		if (!gOffscreen) [w orderFrontRegardless];
		afterSettle(gOffscreen ? 0.1 : 1.0, ^{
			runPhase();
		});
		[NSApp run];
	}
	return 0;
}
