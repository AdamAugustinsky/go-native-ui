// AppKitKeynav prints which controls Tab reaches in a window of the usual
// AppKit controls, with System Settings > Keyboard > "Keyboard navigation"
// (full keyboard access) as the machine has it. For each control it reports whether the
// control is part of the key view loop (-[NSView canBecomeKeyView]), and it
// walks the loop from the first text field the way Tab does
// (-[NSView nextValidKeyView], the step of -[NSWindow selectNextKeyView:],
// which the Tab key calls) and prints the order, once with Tab and once with
// Shift+Tab.
//
//	sh tools/appkitref/keynav.sh [-machine]
//
// The tool never changes the system setting, and nothing short of changing it
// makes AppKit's controls take the "on" answer: the command-line override
// -AppleKeyboardUIMode, a per-application default and a replaced
// -[NSApplication isFullKeyboardAccessEnabled] were each tried and left the key
// view loop as it was. So it measures the setting the machine has, which the
// "fullKeyboardAccess" key records (-[NSApplication isFullKeyboardAccessEnabled]).
// It makes no window visible.
#import <AppKit/AppKit.h>
#import "machine.h"

// Controls by name, in the order they are added to the window.
static NSMutableArray<NSView *> *views;
static NSMutableDictionary<NSValue *, NSString *> *names;

static void add(NSView *parent, NSView *v, NSString *name, NSRect f) {
	v.frame = f;
	[parent addSubview:v];
	[views addObject:v];
	names[[NSValue valueWithNonretainedObject:v]] = name;
}

static NSString *nameOf(NSResponder *r) {
	NSView *v = (NSView *)r;
	while (v) {
		NSString *n = names[[NSValue valueWithNonretainedObject:v]];
		if (n) return n;
		v = v.superview;
	}
	return [NSString stringWithFormat:@"(%@)", NSStringFromClass([r class])];
}

int main(int argc, char **argv) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		NSMutableDictionary *out = [NSMutableDictionary dictionary];
		out[@"machine"] = machineInfo();
		if (argc > 1 && !strcmp(argv[argc - 1], "-machine")) {
			NSData *m = [NSJSONSerialization dataWithJSONObject:@{@"machine" : out[@"machine"]} options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
			fwrite(m.bytes, 1, m.length, stdout);
			fputc('\n', stdout);
			return 0;
		}
		views = [NSMutableArray array];
		names = [NSMutableDictionary dictionary];
		NSWindow *win = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 420, 900) styleMask:NSWindowStyleMaskTitled backing:NSBackingStoreBuffered defer:NO];
		NSView *root = win.contentView;
		CGFloat y = 860;
#define ROW(v, nm) add(root, v, @nm, NSMakeRect(10, y -= 30, 300, 24))
		ROW([[NSTextField alloc] init], "textField");
		ROW([NSButton buttonWithTitle:@"Push" target:nil action:nil], "pushButton");
		ROW([NSButton checkboxWithTitle:@"Check" target:nil action:nil], "checkbox");
		ROW([NSButton radioButtonWithTitle:@"Radio" target:nil action:nil], "radio");
		NSPopUpButton *pop = [[NSPopUpButton alloc] init];
		[pop addItemsWithTitles:@[ @"One", @"Two" ]];
		ROW(pop, "popUpButton");
		NSPopUpButton *pull = [[NSPopUpButton alloc] init];
		pull.pullsDown = YES;
		[pull addItemsWithTitles:@[ @"Pull", @"One" ]];
		ROW(pull, "pullDownButton");
		ROW([NSSlider sliderWithValue:0.5 minValue:0 maxValue:1 target:nil action:nil], "slider");
		NSArray *segLabels = @[ @"A", @"B" ];
		NSSegmentedControl *seg = [NSSegmentedControl segmentedControlWithLabels:segLabels trackingMode:NSSegmentSwitchTrackingSelectOne target:nil action:nil];
		ROW(seg, "segmented");
		ROW([NSStepper new], "stepper");
		NSButton *disclosure = [NSButton buttonWithTitle:@"" target:nil action:nil];
		disclosure.bezelStyle = NSBezelStyleDisclosure;
		disclosure.buttonType = NSButtonTypePushOnPushOff;
		ROW(disclosure, "disclosureTriangle");
		NSColorWell *well = [[NSColorWell alloc] init];
		ROW(well, "colorWell");
		NSDatePicker *dp = [[NSDatePicker alloc] init];
		dp.datePickerStyle = NSDatePickerStyleTextFieldAndStepper;
		ROW(dp, "datePicker");
		ROW([[NSSearchField alloc] init], "searchField");
		NSComboBox *combo = [[NSComboBox alloc] init];
		[combo addItemsWithObjectValues:@[ @"a", @"b" ]];
		ROW(combo, "comboBox");
		ROW([[NSTokenField alloc] init], "tokenField");
		ROW([[NSSecureTextField alloc] init], "secureTextField");
		ROW([NSButton buttonWithTitle:@"Push 2" target:nil action:nil], "pushButton2");
		// Scrolling content.
		NSScrollView *sv = [[NSScrollView alloc] init];
		NSTextView *tv = [[NSTextView alloc] initWithFrame:NSMakeRect(0, 0, 300, 60)];
		sv.documentView = tv;
		sv.hasVerticalScroller = YES;
		names[[NSValue valueWithNonretainedObject:tv]] = @"textView";
		add(root, sv, @"textViewScroll", NSMakeRect(10, y -= 70, 300, 60));
		NSScrollView *ts = [[NSScrollView alloc] init];
		NSTableView *table = [[NSTableView alloc] initWithFrame:NSMakeRect(0, 0, 300, 60)];
		NSTableColumn *col = [[NSTableColumn alloc] initWithIdentifier:@"c"];
		[table addTableColumn:col];
		ts.documentView = table;
		names[[NSValue valueWithNonretainedObject:table]] = @"tableView";
		add(root, ts, @"tableScroll", NSMakeRect(10, y -= 70, 300, 60));
		NSScrollView *os = [[NSScrollView alloc] init];
		NSOutlineView *outline = [[NSOutlineView alloc] initWithFrame:NSMakeRect(0, 0, 300, 60)];
		NSTableColumn *oc = [[NSTableColumn alloc] initWithIdentifier:@"o"];
		[outline addTableColumn:oc];
		os.documentView = outline;
		names[[NSValue valueWithNonretainedObject:outline]] = @"outlineView";
		add(root, os, @"outlineScroll", NSMakeRect(10, y -= 70, 300, 60));
		ROW([[NSTextField alloc] init], "textField2");
		NSTextField *label = [NSTextField labelWithString:@"Label"];
		ROW(label, "label");
		NSTextField *sel = [NSTextField labelWithString:@"Selectable"];
		sel.selectable = YES;
		ROW(sel, "selectableLabel");

		[win recalculateKeyViewLoop];
		out[@"fullKeyboardAccess"] = @([NSApp isFullKeyboardAccessEnabled]);

		// Whether each control can take the focus from the keyboard.
		NSMutableDictionary *can = [NSMutableDictionary dictionary];
		for (NSView *v in views) {
			NSView *target = v;
			if ([v isKindOfClass:[NSScrollView class]]) target = [(NSScrollView *)v documentView];
			can[nameOf(target)] = @{@"class" : NSStringFromClass([target class]), @"canBecomeKeyView" : @(target.canBecomeKeyView), @"acceptsFirstResponder" : @(target.acceptsFirstResponder)};
		}
		out[@"controls"] = can;

		// The order Tab walks: -[NSView nextValidKeyView] (the step
		// -[NSWindow selectNextKeyView:] takes) from the first text field until
		// it comes round, and -previousValidKeyView for Shift+Tab. A scroll
		// view's document view stands for it.
		for (NSString *dir in @[ @"tab", @"shiftTab" ]) {
			NSMutableArray *order = [NSMutableArray array];
			NSView *start = views[0];
			NSView *v = start;
			for (int i = 0; i < 40; i++) {
				v = [dir isEqual:@"tab"] ? v.nextValidKeyView : v.previousValidKeyView;
				if (!v) break;
				[order addObject:nameOf(v)];
				if (v == start) break;
			}
			out[dir] = order;
		}
		NSData *j = [NSJSONSerialization dataWithJSONObject:out options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
		fwrite(j.bytes, 1, j.length, stdout);
		fputc('\n', stdout);
	}
	return 0;
}
