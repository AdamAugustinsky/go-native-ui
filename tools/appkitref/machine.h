// machineInfo describes the machine a measurement was taken on, for the
// top-level "machine" object of every measurement file (README.md, "Which
// machine"). Header-only so that main.m, scroller.m and the scroll tools share
// it. The caller has made NSApplication (NSScreen needs it).
//
//	model            hw.model, e.g. "Mac16,10"
//	macOS            {version, build} from kern.osproductversion, kern.osversion
//	display          the main screen: name, points, backing scale, pixels, refresh rate
//	appearance       the system's light or dark mode (global default, not this process's)
//	accent           the system's accent colour; accentRendered is what this process saw
//	                 (differs when -AppleAccentColor is on the command line)
//	scrollerStyle    NSScroller.preferredScrollerStyle
//	date             the day of the run (local)
#import <AppKit/AppKit.h>
#include <sys/sysctl.h>

static NSString *mcSysctl(const char *name) {
	size_t n = 0;
	if (sysctlbyname(name, NULL, &n, NULL, 0) != 0 || n == 0) return @"";
	char *b = calloc(n + 1, 1);
	sysctlbyname(name, b, &n, NULL, 0);
	NSString *s = [NSString stringWithUTF8String:b];
	free(b);
	return s ?: @"";
}

static NSString *mcAccentName(id v) {
	if (!v) return @"multicolor";
	switch ([v integerValue]) {
	case -1: return @"graphite";
	case 0: return @"red";
	case 1: return @"orange";
	case 2: return @"yellow";
	case 3: return @"green";
	case 4: return @"blue";
	case 5: return @"purple";
	case 6: return @"pink";
	}
	return [NSString stringWithFormat:@"index %@", v];
}

static NSDictionary *machineInfo(void) {
	NSScreen *scr = NSScreen.mainScreen ?: NSScreen.screens.firstObject;
	NSMutableDictionary *disp = [NSMutableDictionary dictionary];
	if (scr) {
		NSSize px = [scr convertRectToBacking:scr.frame].size;
		disp[@"name"] = scr.localizedName;
		disp[@"widthPt"] = @(scr.frame.size.width);
		disp[@"heightPt"] = @(scr.frame.size.height);
		disp[@"backingScale"] = @(scr.backingScaleFactor);
		disp[@"widthPx"] = @(px.width);
		disp[@"heightPx"] = @(px.height);
		disp[@"refreshHz"] = @(scr.maximumFramesPerSecond);
	}
	// the global domain, not NSUserDefaults: a -AppleInterfaceStyle or
	// -AppleAccentColor argument must not hide the machine's own setting
	id style = CFBridgingRelease(CFPreferencesCopyValue(CFSTR("AppleInterfaceStyle"), kCFPreferencesAnyApplication, kCFPreferencesCurrentUser, kCFPreferencesAnyHost));
	id accent = CFBridgingRelease(CFPreferencesCopyValue(CFSTR("AppleAccentColor"), kCFPreferencesAnyApplication, kCFPreferencesCurrentUser, kCFPreferencesAnyHost));
	id seen = [NSUserDefaults.standardUserDefaults objectForKey:@"AppleAccentColor"];
	NSDateFormatter *df = [NSDateFormatter new];
	df.dateFormat = @"yyyy-MM-dd";
	df.locale = [NSLocale localeWithLocaleIdentifier:@"en_US_POSIX"];
	return @{
		@"model" : mcSysctl("hw.model"),
		@"macOS" : @{@"version" : mcSysctl("kern.osproductversion"), @"build" : mcSysctl("kern.osversion")},
		@"display" : disp,
		@"appearance" : [style isEqual:@"Dark"] ? @"dark" : @"light",
		@"accent" : mcAccentName(accent),
		@"accentRendered" : mcAccentName(seen),
		@"scrollerStyle" : NSScroller.preferredScrollerStyle == NSScrollerStyleOverlay ? @"overlay" : @"legacy",
		@"date" : [df stringFromDate:NSDate.date],
	};
}
