// AppKitCorner prints the outline of a continuous rounded corner as Core
// Animation draws it (CALayer cornerCurve = .continuous, the curve macOS uses
// for selections, capsules, buttons, fields, popovers and windows) beside the
// circular one, as JSON: for each corner radius, how far in from the box's
// left edge the black fill begins on each row of its top-left corner, read
// from a render at 16x: one number per 1/16 pt row, as a string of them.
//
//	sh tools/appkitref/corner.sh [-machine]
//
// It makes no window visible and needs no GUI session: CALayer
// renderInContext: draws into a bitmap. The box is 100 pt square, so no
// radius here is limited by the box; rows run from the top to 1.6 radii
// down. gpu's squircle (gpu/corner_test.go) is fitted to
// and tested against it.
#import <AppKit/AppKit.h>
#import <QuartzCore/QuartzCore.h>
#import "machine.h"

static NSString *cornerRows(double radius, BOOL continuous) {
	const int scale = 16, box = 100;
	CALayer *l = [CALayer layer];
	l.frame = CGRectMake(0, 0, box, box);
	l.cornerRadius = radius;
	l.cornerCurve = continuous ? kCACornerCurveContinuous : kCACornerCurveCircular;
	l.backgroundColor = CGColorGetConstantColor(kCGColorBlack);
	l.masksToBounds = YES;
	CGColorSpaceRef cs = CGColorSpaceCreateDeviceGray();
	CGContextRef ctx = CGBitmapContextCreate(NULL, box * scale, box * scale, 8, box * scale, cs, (CGBitmapInfo)kCGImageAlphaNone);
	CGContextSetGrayFillColor(ctx, 1, 1);
	CGContextFillRect(ctx, CGRectMake(0, 0, box * scale, box * scale));
	CGContextScaleCTM(ctx, scale, scale);
	[l renderInContext:ctx];
	unsigned char *px = CGBitmapContextGetData(ctx);
	NSMutableString *rows = [NSMutableString string];
	for (int row = 0; row < radius * 1.6 * scale; row++) { // bitmap row 0 is the top
		int x = 0;
		while (x < box * scale && px[row * box * scale + x] > 127) x++;
		[rows appendFormat:@"%s%g", row ? " " : "", x / (double)scale];
	}
	CGContextRelease(ctx);
	CGColorSpaceRelease(cs);
	return rows;
}

int main(int argc, char **argv) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		if (argc > 1 && !strcmp(argv[1], "-machine")) {
			NSData *m = [NSJSONSerialization dataWithJSONObject:@{@"machine" : machineInfo()} options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
			fwrite(m.bytes, 1, m.length, stdout);
			fputc('\n', stdout);
			return 0;
		}
		NSMutableArray *corners = [NSMutableArray array];
		for (NSNumber *r in @[ @4, @8, @12, @16, @24 ]) {
			[corners addObject:@{@"radius" : r, @"continuous" : cornerRows(r.doubleValue, YES), @"circular" : cornerRows(r.doubleValue, NO)}];
		}
		NSData *j = [NSJSONSerialization dataWithJSONObject:@{@"machine" : machineInfo(), @"corners" : corners} options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:nil];
		fwrite(j.bytes, 1, j.length, stdout);
		fputc('\n', stdout);
	}
	return 0;
}
