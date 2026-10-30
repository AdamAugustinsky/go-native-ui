// AppKitScroller prints the geometry of legacy and overlay NSScrollers per
// control size as JSON: the scroller's width and, in a vertical scroller of
// a 200-pt scroll view over 1000 pt of content, the knob slot's and the
// knob's rectangles (-[NSScroller rectForPart:]). AppKitRef's metrics.json
// has the widths only, and offscreen renders do not capture the knob, whose
// inset widget/base and rexui copy.
//
//	sh tools/appkitref/scroller.sh [-machine | -knobs]
//
// It makes no window visible and needs no GUI session. The "machine" key
// says where it ran (machine.h); the rest are the style-size keys.
//
// -knobs prints the knob's length instead: for real scroll views (not a
// hand-set proportion) of several heights over documents of several lengths,
// with and without a content inset above them (the toolbar's), per style and
// size, the clip view's height, the document's, the inset, the knob slot's
// and the knob's lengths. It shows the rules widget/base copies: the knob is
// the slot times the clip's height over the document's plus the inset,
// rounded down to a half point, and never shorter than a minimum (20 pt
// regular legacy, 16 small, 26 overlay). A scroll view's document the
// NSScrollView itself sizes, so the proportion is AppKit's own.
#import <AppKit/AppKit.h>
#import "machine.h"

static NSDictionary *rectJ(NSRect r) {
	return @{@"x" : @(r.origin.x), @"y" : @(r.origin.y), @"w" : @(r.size.width), @"h" : @(r.size.height)};
}

int main(int argc, char **argv) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		if (argc > 1 && !strcmp(argv[1], "-machine")) { // only where it ran
			NSData *m = [NSJSONSerialization dataWithJSONObject:@{@"machine" : machineInfo()} options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
			fwrite(m.bytes, 1, m.length, stdout);
			fputc('\n', stdout);
			return 0;
		}
		if (argc > 1 && !strcmp(argv[1], "-knobs")) {
			NSMutableArray *rows = [NSMutableArray array];
			NSArray *kstyles = @[ @[ @"legacy", @(NSScrollerStyleLegacy) ], @[ @"overlay", @(NSScrollerStyleOverlay) ] ];
			NSArray *ksizes = @[ @[ @"regular", @(NSControlSizeRegular) ], @[ @"small", @(NSControlSizeSmall) ] ];
			for (NSArray *st in kstyles) {
				for (NSArray *cs in ksizes) {
					for (NSNumber *vh in @[ @100, @400, @640 ]) {
						for (NSNumber *dh in @[ @250, @1000, @4000, @20000 ]) {
							for (NSNumber *inset in @[ @0, @52 ]) {
								NSScrollView *sv = [[NSScrollView alloc] initWithFrame:NSMakeRect(0, 0, 400, vh.doubleValue)];
								sv.hasVerticalScroller = YES;
								sv.scrollerStyle = [st[1] integerValue];
								sv.autohidesScrollers = NO;
								sv.automaticallyAdjustsContentInsets = NO;
								sv.contentInsets = NSEdgeInsetsMake(inset.doubleValue, 0, 0, 0);
								sv.documentView = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, 380, dh.doubleValue)];
								sv.verticalScroller.controlSize = [cs[1] unsignedIntegerValue];
								[sv tile];
								[sv reflectScrolledClipView:sv.contentView];
								NSScroller *s = sv.verticalScroller;
								[rows addObject:@{
									@"style" : st[0],
									@"size" : cs[0],
									@"view" : @(sv.contentView.frame.size.height),
									@"document" : dh,
									@"inset" : inset,
									@"proportion" : @(s.knobProportion),
									@"slot" : @([s rectForPart:NSScrollerKnobSlot].size.height),
									@"knob" : @([s rectForPart:NSScrollerKnob].size.height),
								}];
							}
						}
					}
				}
			}
			NSData *j = [NSJSONSerialization dataWithJSONObject:@{@"machine" : machineInfo(), @"knobs" : rows} options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
			fwrite(j.bytes, 1, j.length, stdout);
			fputc('\n', stdout);
			return 0;
		}
		NSMutableDictionary *out = [NSMutableDictionary dictionary];
		out[@"machine"] = machineInfo();
		NSArray *sizes = @[ @[ @"regular", @(NSControlSizeRegular) ], @[ @"small", @(NSControlSizeSmall) ], @[ @"mini", @(NSControlSizeMini) ] ];
		NSArray *styles = @[ @[ @"legacy", @(NSScrollerStyleLegacy) ], @[ @"overlay", @(NSScrollerStyleOverlay) ] ];
		for (NSArray *cs in sizes) {
			for (NSArray *st in styles) {
				NSControlSize size = [cs[1] unsignedIntegerValue];
				NSScrollerStyle style = [st[1] integerValue];
				CGFloat w = [NSScroller scrollerWidthForControlSize:size scrollerStyle:style];
				NSScrollView *sv = [[NSScrollView alloc] initWithFrame:NSMakeRect(0, 0, 300, 200)];
				sv.hasVerticalScroller = YES;
				sv.scrollerStyle = style;
				sv.autohidesScrollers = NO;
				NSView *doc = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, 300, 1000)];
				sv.documentView = doc;
				NSScroller *s = sv.verticalScroller;
				s.controlSize = size;
				s.scrollerStyle = style;
				[sv tile];
				s.knobProportion = 0.2;
				s.doubleValue = 0;
				out[[NSString stringWithFormat:@"%@-%@", cs[0], st[0]]] = @{
					@"width" : @(w),
					@"frame" : rectJ(s.frame),
					@"slot" : rectJ([s rectForPart:NSScrollerKnobSlot]),
					@"knob" : rectJ([s rectForPart:NSScrollerKnob]),
					@"contentWidth" : @(sv.contentView.frame.size.width),
				};
			}
		}
		NSData *j = [NSJSONSerialization dataWithJSONObject:out options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
		fwrite(j.bytes, 1, j.length, stdout);
		fputc('\n', stdout);
	}
	return 0;
}
