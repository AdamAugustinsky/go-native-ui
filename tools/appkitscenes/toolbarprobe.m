// toolbarprobe lays out an NSToolbar of the given items offscreen and prints
// the frames of its item viewers, glass platters, buttons and flexible
// spaces: how AppKit sizes toolbar items (rexui's ToolbarRecipe.GroupPad,
// EdgePad, FlexAfter).
//
//	clang -fobjc-arc -framework AppKit -o /tmp/toolbarprobe tools/appkitscenes/toolbarprobe.m
//	/tmp/toolbarprobe compact|unified 'g:textformat|checklist|tablecells;f;s:trash'
//
// Items are ';'-separated: f a flexible space, s:SYMBOL a one-symbol item,
// g:SYM|SYM|… a group.
#import <AppKit/AppKit.h>
@interface D : NSObject <NSToolbarDelegate>
@property NSArray *ids; @property NSArray *specs;
@end
static NSToolbarItem *btn(NSString *ident, NSString *s) {
	NSToolbarItem *b = [[NSToolbarItem alloc] initWithItemIdentifier:ident];
	b.image = [NSImage imageWithSystemSymbolName:s accessibilityDescription:nil];
	b.label = s; b.target = NSApp; b.action = @selector(self);
	return b;
}
@implementation D
- (NSArray *)toolbarAllowedItemIdentifiers:(NSToolbar *)t { return self.ids; }
- (NSArray *)toolbarDefaultItemIdentifiers:(NSToolbar *)t { return self.ids; }
- (NSToolbarItem *)toolbar:(NSToolbar *)t itemForItemIdentifier:(NSString *)ident willBeInsertedIntoToolbar:(BOOL)f {
	NSString *sp = self.specs[[self.ids indexOfObject:ident]];
	if ([sp hasPrefix:@"s:"]) return btn(ident, [sp substringFromIndex:2]);
	NSMutableArray *subs = [NSMutableArray array];
	int k = 0;
	for (NSString *s in [[sp substringFromIndex:2] componentsSeparatedByString:@"|"]) [subs addObject:btn([ident stringByAppendingFormat:@".%d", k++], s)];
	NSToolbarItemGroup *g = [[NSToolbarItemGroup alloc] initWithItemIdentifier:ident];
	g.subitems = subs; g.label = ident;
	return g;
}
@end
static void walk(NSView *v, NSView *root, NSMutableArray *out) {
	NSString *c = NSStringFromClass(v.class);
	if ([c isEqual:@"NSToolbarButton"] || [c isEqual:@"NSToolbarPlatterView"] || [c isEqual:@"NSToolbarItemViewer"] || [c isEqual:@"_NSToolbarFlexibleSpace"]) {
		NSRect r = [v convertRect:v.bounds toView:root];
		NSString *t = [c isEqual:@"NSToolbarButton"] ? @"btn" : [c isEqual:@"NSToolbarPlatterView"] ? @"plat" : [c isEqual:@"NSToolbarItemViewer"] ? @"VIEW" : @"FLEX";
		[out addObject:@[ @(r.origin.x), @(r.size.width), t ]];
	}
	for (NSView *s in v.subviews) walk(s, root, out);
}
int main(int argc, const char **argv) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		BOOL compact = !strcmp(argv[1], "compact");
		NSArray *specs = [[NSString stringWithUTF8String:argv[2]] componentsSeparatedByString:@";"];
		D *d = [D new]; d.specs = specs;
		NSMutableArray *ids = [NSMutableArray array];
		for (NSUInteger i = 0; i < specs.count; i++) [ids addObject:[specs[i] isEqual:@"f"] ? NSToolbarFlexibleSpaceItemIdentifier : [NSString stringWithFormat:@"i%lu", i]];
		d.ids = ids;
		NSWindow *w = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 1600, 400) styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskFullSizeContentView | NSWindowStyleMaskResizable backing:NSBackingStoreBuffered defer:NO];
		w.toolbarStyle = compact ? NSWindowToolbarStyleUnifiedCompact : NSWindowToolbarStyleUnified;
		w.titleVisibility = NSWindowTitleHidden;
		NSToolbar *tb = [[NSToolbar alloc] initWithIdentifier:@"p"]; tb.delegate = d; tb.displayMode = NSToolbarDisplayModeIconOnly;
		w.toolbar = tb;
		[w.contentView.superview layoutSubtreeIfNeeded];
		[[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.3]];
		[w.contentView.superview layoutSubtreeIfNeeded];
		NSMutableArray *out = [NSMutableArray array];
		walk(w.contentView.superview, w.contentView.superview, out);
		[out sortUsingComparator:^(NSArray *a, NSArray *b) { NSComparisonResult r = [a[0] compare:b[0]]; return r ? r : [b[1] compare:a[1]]; }];
		for (NSArray *o in out) printf("%s %g/%g  ", [o[2] UTF8String], [o[0] doubleValue], [o[1] doubleValue]);
		printf("\n");
	}
	return 0;
}
